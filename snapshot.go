package knaller

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/benben/knaller/firecracker"
)

// Snapshot holds the metadata stored alongside a Firecracker snapshot.
// Written to metadata.json in the snapshot directory.
type Snapshot struct {
	ID         string        `json:"id"`
	VMName     string        `json:"vm_name"`
	CreatedAt  time.Time     `json:"created_at"`
	VCPUs      int           `json:"vcpus"`
	MemSizeMib int           `json:"mem_size_mib"`
	Ports      []PortMapping `json:"ports,omitempty"`
}

// snapshotID generates a random snapshot ID with the "snap_" prefix.
func snapshotID() string {
	b := make([]byte, 4)
	rand.Read(b)
	return "snap_" + hex.EncodeToString(b)
}

func snapshotBaseDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "knaller", "snapshots")
}

func snapshotDir(id string) string {
	return filepath.Join(snapshotBaseDir(), id)
}

// CreateSnapshot takes a snapshot of a running VM. The VM is briefly paused
// while Firecracker writes the device state and memory dump, then resumed.
// The VM's rootfs is also copied into the snapshot directory.
//
// Before taking the snapshot, we patch the drive to point to the snapshot
// directory's rootfs. This makes the snapshot self-contained: the state file
// references a path inside the snapshot directory, not the original VM's
// rootfs. This allows restoring even after the original VM is deleted.
//
// Progress messages are written to w (pass nil for silent operation).
// Returns the snapshot ID.
func CreateSnapshot(ctx context.Context, vmName string, w io.Writer) (string, error) {
	if w == nil {
		w = io.Discard
	}

	socketPath := filepath.Join(socketDirectory(), vmName+".socket")
	if _, err := os.Stat(socketPath); err != nil {
		return "", fmt.Errorf("VM %q not found (no socket at %s)", vmName, socketPath)
	}
	client := firecracker.NewClient(socketPath)

	vmCfg, err := client.GetVMConfig(ctx)
	if err != nil {
		return "", fmt.Errorf("get vm config: %w", err)
	}

	id := snapshotID()
	dir := snapshotDir(id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create snapshot dir: %w", err)
	}

	fmt.Fprintf(w, "Copying rootfs...\n")
	srcRootfs := filepath.Join(vmDataDir(vmName), "rootfs.ext4")
	dstRootfs := filepath.Join(dir, "rootfs.ext4")
	cp := exec.CommandContext(ctx, "cp", "--reflink=auto", srcRootfs, dstRootfs)
	if out, err := cp.CombinedOutput(); err != nil {
		os.RemoveAll(dir)
		return "", fmt.Errorf("copy rootfs: %s: %w", out, err)
	}

	fmt.Fprintf(w, "Pausing VM %q...\n", vmName)
	if err := client.PauseVM(ctx); err != nil {
		os.RemoveAll(dir)
		return "", fmt.Errorf("pause vm: %w", err)
	}

	// Always resume the VM and restore the original drive path, even on error.
	var snapErr error
	defer func() {
		// Restore the drive to the original rootfs path.
		client.PatchDrive(ctx, "rootfs", srcRootfs)
		fmt.Fprintf(w, "Resuming VM...\n")
		if rerr := client.ResumeVM(ctx); rerr != nil {
			if snapErr != nil {
				snapErr = fmt.Errorf("%w; also failed to resume: %v", snapErr, rerr)
			} else {
				snapErr = fmt.Errorf("resume vm: %w", rerr)
			}
		}
	}()

	// Patch the drive to point to the snapshot's rootfs copy. This way the
	// Firecracker snapshot state bakes in the snapshot directory path, making
	// the snapshot fully self-contained.
	if err := client.PatchDrive(ctx, "rootfs", dstRootfs); err != nil {
		os.RemoveAll(dir)
		snapErr = fmt.Errorf("patch drive for snapshot: %w", err)
		return "", snapErr
	}

	fmt.Fprintf(w, "Creating snapshot...\n")
	statePath := filepath.Join(dir, "state")
	memPath := filepath.Join(dir, "memory")
	if err := client.CreateSnapshot(ctx, &firecracker.SnapshotCreate{
		SnapshotType: "Full",
		SnapshotPath: statePath,
		MemFilePath:  memPath,
	}); err != nil {
		os.RemoveAll(dir)
		snapErr = fmt.Errorf("create snapshot: %w", err)
		return "", snapErr
	}

	meta := &Snapshot{
		ID:        id,
		VMName:    vmName,
		CreatedAt: time.Now(),
		Ports:     loadVMPorts(vmName),
	}
	if vmCfg.MachineConfig != nil {
		meta.VCPUs = vmCfg.MachineConfig.VcpuCount
		meta.MemSizeMib = vmCfg.MachineConfig.MemSizeMib
	}

	metaData, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		os.RemoveAll(dir)
		snapErr = fmt.Errorf("marshal metadata: %w", err)
		return "", snapErr
	}
	if err := os.WriteFile(filepath.Join(dir, "metadata.json"), metaData, 0o644); err != nil {
		os.RemoveAll(dir)
		snapErr = fmt.Errorf("write metadata: %w", err)
		return "", snapErr
	}

	return id, snapErr
}

// SnapshotRawResult bundles timing information alongside the snapshot ID so
// callers can attribute the wall-clock cost. PausedAt and ResumedAt bracket
// the actual guest-frozen window — useful for measuring pause-tail latency
// independently of any post-resume async durability work.
type SnapshotRawResult struct {
	ID        string
	PausedAt  time.Time
	ResumedAt time.Time
}

// CreateSnapshotRaw is like CreateSnapshot but skips the rootfs copy and the
// drive-path patching. Use this when the VM's disk is a raw block device
// (e.g. an NBD device backed by a content-addressed cache, or a file
// managed outside knaller) that the caller manages out of band. The
// snapshot directory ends up with state, memory, and metadata; the disk's
// contents are restored separately by the caller (e.g. by seeding a
// content-addressed manifest, or pointing PatchDrive at a fresh device).
//
// whilePaused, if non-nil, is invoked AFTER the firecracker state+memory
// dump is written but BEFORE the VM is resumed. Use this to capture the
// disk's state-at-pause-time consistent with the memory dump (e.g. flush a
// dirty queue, copy a manifest into snapDir). Returning an error fails the
// snapshot but the VM is still resumed.
//
// On restore (Run/RunDirect with SnapshotID + RawDiskPath), LoadSnapshot is
// followed by PatchDrive so the new RawDiskPath replaces whatever device
// path was baked into the state file.
func CreateSnapshotRaw(ctx context.Context, vmName string, w io.Writer, whilePaused func(snapDir string) error) (result SnapshotRawResult, err error) {
	if w == nil {
		w = io.Discard
	}

	socketPath := filepath.Join(socketDirectory(), vmName+".socket")
	if _, err := os.Stat(socketPath); err != nil {
		return SnapshotRawResult{}, fmt.Errorf("VM %q not found (no socket at %s)", vmName, socketPath)
	}
	client := firecracker.NewClient(socketPath)

	vmCfg, err := client.GetVMConfig(ctx)
	if err != nil {
		return SnapshotRawResult{}, fmt.Errorf("get vm config: %w", err)
	}

	id := snapshotID()
	dir := snapshotDir(id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return SnapshotRawResult{}, fmt.Errorf("create snapshot dir: %w", err)
	}
	result.ID = id

	fmt.Fprintf(w, "Pausing VM %q...\n", vmName)
	result.PausedAt = time.Now()
	if perr := client.PauseVM(ctx); perr != nil {
		os.RemoveAll(dir)
		return SnapshotRawResult{}, fmt.Errorf("pause vm: %w", perr)
	}

	// Deferred resume + resume-time stamp. Because CreateSnapshotRaw uses
	// named returns, mutating result.ResumedAt here lands in the caller's
	// return value.
	defer func() {
		fmt.Fprintf(w, "Resuming VM...\n")
		if rerr := client.ResumeVM(ctx); rerr != nil {
			if err != nil {
				err = fmt.Errorf("%w; also failed to resume: %v", err, rerr)
			} else {
				err = fmt.Errorf("resume vm: %w", rerr)
			}
		}
		result.ResumedAt = time.Now()
	}()

	fmt.Fprintf(w, "Creating snapshot...\n")
	statePath := filepath.Join(dir, "state")
	memPath := filepath.Join(dir, "memory")
	if cerr := client.CreateSnapshot(ctx, &firecracker.SnapshotCreate{
		SnapshotType: "Full",
		SnapshotPath: statePath,
		MemFilePath:  memPath,
	}); cerr != nil {
		os.RemoveAll(dir)
		err = fmt.Errorf("create snapshot: %w", cerr)
		return
	}

	if whilePaused != nil {
		if werr := whilePaused(dir); werr != nil {
			os.RemoveAll(dir)
			err = fmt.Errorf("while-paused hook: %w", werr)
			return
		}
	}

	meta := &Snapshot{
		ID:        id,
		VMName:    vmName,
		CreatedAt: time.Now(),
		Ports:     loadVMPorts(vmName),
	}
	if vmCfg.MachineConfig != nil {
		meta.VCPUs = vmCfg.MachineConfig.VcpuCount
		meta.MemSizeMib = vmCfg.MachineConfig.MemSizeMib
	}

	metaData, merr := json.MarshalIndent(meta, "", "  ")
	if merr != nil {
		os.RemoveAll(dir)
		err = fmt.Errorf("marshal metadata: %w", merr)
		return
	}
	if werr := os.WriteFile(filepath.Join(dir, "metadata.json"), metaData, 0o644); werr != nil {
		os.RemoveAll(dir)
		err = fmt.Errorf("write metadata: %w", werr)
		return
	}
	return
}

// DeleteSnapshot removes a snapshot and all its files (state, memory, rootfs).
func DeleteSnapshot(id string) error {
	dir := snapshotDir(id)
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("snapshot %q not found", id)
	}
	return os.RemoveAll(dir)
}

// GetSnapshot reads the metadata for a single snapshot by ID.
func GetSnapshot(id string) (*Snapshot, error) {
	data, err := os.ReadFile(filepath.Join(snapshotDir(id), "metadata.json"))
	if err != nil {
		return nil, fmt.Errorf("snapshot %q not found", id)
	}
	var s Snapshot
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("corrupt snapshot metadata: %w", err)
	}
	return &s, nil
}

// ListSnapshots discovers all snapshots by scanning the snapshots directory
// and reading each metadata.json file. Corrupt or incomplete snapshots are
// silently skipped.
func ListSnapshots() ([]*Snapshot, error) {
	dir := snapshotBaseDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var snapshots []*Snapshot
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "snap_") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name(), "metadata.json"))
		if err != nil {
			continue
		}
		var s Snapshot
		if err := json.Unmarshal(data, &s); err != nil {
			continue
		}
		snapshots = append(snapshots, &s)
	}
	return snapshots, nil
}
