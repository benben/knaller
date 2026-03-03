package knaller

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
)

// Config describes a microVM to start. At minimum, Kernel and RootFS must be
// set. Knaller starts a Firecracker process, connects to its API socket,
// configures the VM, and boots it. Interact with the running VM via SSH
// (the guest IP is returned in VM.GuestIP).
type Config struct {
	Name           string    // VM name (random 8-char hex if empty)
	Kernel         string    // path to vmlinux kernel image
	RootFS         string    // path to base rootfs ext4 image (copied per-VM)
	CPUs           float64   // vCPUs (default: 1, e.g. 0.5 = 1 vCPU at 50% quota)
	Memory         int       // memory in MiB (default: 1024, minimum: 128)
	NetworkMbps    float64   // network bandwidth limit in Mbps per direction (0 = unlimited)
	DiskMBps       int       // disk bandwidth limit in MB/s (0 = unlimited)
	DiskIOPS       int       // disk I/O operations per second limit (0 = unlimited)
	FirecrackerBin string    // path to firecracker binary (default: "firecracker")
	PastaBin       string    // path to pasta binary (default: "pasta")
	Stdout         io.Writer // serial console log output (default: io.Discard)
	Stderr         io.Writer // firecracker process stderr (default: io.Discard)
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

// validate checks that all required fields are set and valid. Kernel and RootFS
// must point to existing files. CPUs must be >= 1 and Memory >= 128 MiB
// (Firecracker's minimum).
func (c *Config) validate() error {
	if c.Kernel == "" {
		return errors.New("kernel path is required")
	}
	if _, err := os.Stat(c.Kernel); err != nil {
		return fmt.Errorf("kernel: %w", err)
	}
	if c.RootFS == "" {
		return errors.New("rootfs path is required")
	}
	if _, err := os.Stat(c.RootFS); err != nil {
		return fmt.Errorf("rootfs: %w", err)
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
