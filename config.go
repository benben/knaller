package knaller

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
)

// PortMapping maps a host port to a guest port for TCP forwarding.
type PortMapping struct {
	Host  int
	Guest int
}

// Config describes a microVM to start. At minimum, Kernel and RootFS must be
// set. Knaller starts a Firecracker process, connects to its API socket,
// configures the VM, and boots it. Interact with the running VM via SSH
// (the SSH port is returned in VM.Port).
type Config struct {
	Name           string        // VM name (random 8-char hex if empty)
	Kernel         string        // path to vmlinux kernel image
	RootFS         string        // path to base rootfs ext4 image (copied per-VM)
	CPUs           float64       // vCPUs (default: 1, e.g. 0.5 = 1 vCPU at 50% quota)
	Memory         int           // memory in MiB (default: 1024, minimum: 128)
	NetworkMbps    float64       // network bandwidth limit in Mbps per direction (0 = unlimited)
	DiskMBps       int           // disk bandwidth limit in MB/s (0 = unlimited)
	DiskIOPS       int           // disk I/O operations per second limit (0 = unlimited)
	Ports          []PortMapping // additional TCP port forwarding (host:guest)
	SnapshotID     string        // restore from this snapshot instead of booting fresh
	Detach         bool          // start VM in background (survives terminal close)
	FirecrackerBin string        // path to firecracker binary (default: "firecracker")
	PastaBin       string        // path to pasta binary (default: "pasta")
	Stdout         io.Writer     // serial console log output (default: io.Discard)
	Stderr         io.Writer     // firecracker process stderr (default: io.Discard)

	// RootFSSize, when > 0 and larger than the source rootfs, expands the
	// per-VM rootfs to this byte size after the cp+reflink (truncate to
	// size, then e2fsck -fp + resize2fs). Sparse — actual host disk
	// consumption is what the guest writes. Requires e2fsprogs on PATH.
	// Ignored when RawDiskPath is set (caller owns the device).
	RootFSSize int64

	// RawDiskPath, when non-empty, bypasses the per-VM rootfs copy and
	// hands the named block device or file to firecracker as the rootfs
	// drive. Use this when the caller manages the disk lifecycle out of
	// band (e.g. an NBD device backed by a content-addressed cache, or a
	// raw image on shared storage). Knaller does not touch its contents
	// — no copy, no truncate, no resize — and Cleanup() leaves it alone.
	// Snapshot restore with RawDiskPath set will PatchDrive the
	// post-LoadSnapshot drive path to point here.
	RawDiskPath string

	// Netns, when non-empty, overrides the per-VM kernel network namespace
	// name that RunDirect would otherwise derive from cfg.Name. Adoption
	// uses this to pin the netns identity captured by an external state
	// store, so the new process attaches to the same namespace the
	// previous lifetime created.
	Netns string

	// EscapeCgroupSlice, when non-empty, names a cgroupv2 slice (e.g.
	// "knaller-vms.slice") that the firecracker process is moved into
	// immediately after spawn. Used in container-managed environments
	// (Kubernetes, systemd-nspawn) so the VM survives a restart of the
	// supervising container's own cgroup. Requires the host's cgroupv2
	// hierarchy to be visible at /sys/fs/cgroup. The slice is created
	// on demand. Errors are non-fatal — the VM still starts; it just
	// shares the parent process's lifetime.
	EscapeCgroupSlice string
}

// setDefaults fills in zero-value fields with sensible defaults.
func (c *Config) setDefaults() {
	if c.Name == "" {
		c.Name = randomName()
	}
	if c.CPUs == 0 {
		c.CPUs = 1.0
	}
	if c.Memory == 0 {
		c.Memory = 1024
	}
	if c.FirecrackerBin == "" {
		c.FirecrackerBin = "firecracker"
	}
	if c.PastaBin == "" {
		c.PastaBin = "pasta"
	}
	if c.Stdout == nil {
		c.Stdout = io.Discard
	}
	if c.Stderr == nil {
		c.Stderr = io.Discard
	}
}

// validate checks that all required fields are set and valid. When restoring
// from a snapshot (SnapshotID is set), kernel/rootfs/cpus/memory come from the
// snapshot and are not validated here. When the caller manages the rootfs out
// of band (RawDiskPath is set), the RootFS path is similarly skipped.
func (c *Config) validate() error {
	if c.SnapshotID != "" {
		return nil
	}
	if c.Kernel == "" {
		return errors.New("kernel path is required")
	}
	if _, err := os.Stat(c.Kernel); err != nil {
		return fmt.Errorf("kernel: %w", err)
	}
	if c.RawDiskPath == "" {
		if c.RootFS == "" {
			return errors.New("rootfs path is required")
		}
		if _, err := os.Stat(c.RootFS); err != nil {
			return fmt.Errorf("rootfs: %w", err)
		}
	}
	if c.CPUs <= 0 {
		return errors.New("cpus must be > 0")
	}
	if c.Memory < 128 {
		return errors.New("memory must be >= 128 MiB")
	}
	return nil
}

// randomName generates an 8-character random hex string for use as a VM name.
func randomName() string {
	b := make([]byte, 4)
	rand.Read(b)
	return hex.EncodeToString(b)
}
