package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultPaths(t *testing.T) {
	kernel, rootfs := defaultPaths()
	home, _ := os.UserHomeDir()
	expectedBase := filepath.Join(home, ".local", "share", "knaller")

	if kernel != filepath.Join(expectedBase, "vmlinux") {
		t.Errorf("kernel = %q", kernel)
	}
	if rootfs != filepath.Join(expectedBase, "rootfs.ext4") {
		t.Errorf("rootfs = %q", rootfs)
	}
}

func TestRunMissingKernel(t *testing.T) {
	err := Run([]string{"--kernel", "/nonexistent/vmlinux", "--rootfs", "/tmp"})
	if err == nil {
		t.Fatal("expected error for missing kernel")
	}
}
