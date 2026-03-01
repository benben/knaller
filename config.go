package knaller

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
)

// Config describes a microVM to run. At minimum, Kernel and RootFS must be set.
// All other fields have sensible defaults (1 vCPU, 128 MiB, auto-generated name,
// stdin/stdout/stderr connected to the host terminal).
type Config struct {
	Name           string    // VM name (auto-generated 8-char hex if empty)
	Kernel         string    // path to vmlinux kernel image
	RootFS         string    // path to base rootfs ext4 image (copied per-VM)
	CPUs           int       // number of vCPUs (default: 1)
	Memory         int       // memory in MiB (default: 128, minimum: 128)
	FirecrackerBin string    // path to the firecracker binary (default: "firecracker" from PATH)
	Stdout         io.Writer // where to send serial console output (default: os.Stdout)
	Stdin          io.Reader // where to read serial console input (default: os.Stdin)
	Stderr         io.Writer // where to send firecracker process stderr (default: os.Stderr)
}

// setDefaults fills in zero-value fields with sensible defaults.
// It generates a random hex name if none was provided, and connects
// stdio to the host terminal if not explicitly set.
func (c *Config) setDefaults() {
	if c.Name == "" {
		c.Name = randomName()
	}
	if c.CPUs == 0 {
		c.CPUs = 1
	}
	if c.Memory == 0 {
		c.Memory = 128
	}
	if c.FirecrackerBin == "" {
		c.FirecrackerBin = "firecracker"
	}
	if c.Stdout == nil {
		c.Stdout = os.Stdout
	}
	if c.Stdin == nil {
		c.Stdin = os.Stdin
	}
	if c.Stderr == nil {
		c.Stderr = os.Stderr
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
	if c.CPUs < 1 {
		return errors.New("cpus must be >= 1")
	}
	if c.Memory < 128 {
		return errors.New("memory must be >= 128 MiB")
	}
	return nil
}

// randomName generates an 8-character random hex string for use as a VM name.
// This ensures unique names when the user doesn't specify one.
func randomName() string {
	b := make([]byte, 4)
	rand.Read(b)
	return hex.EncodeToString(b)
}
