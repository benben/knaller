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
