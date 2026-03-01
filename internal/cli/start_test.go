package cli

import (
	"os"
	"path/filepath"
	"strings"
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

func TestStartMissingName(t *testing.T) {
	err := Start([]string{"--kernel", "/tmp", "--rootfs", "/tmp"})
	if err == nil {
		t.Fatal("expected error for missing name")
	}
	if !strings.Contains(err.Error(), "--name is required") {
		t.Errorf("error = %q, want --name is required", err.Error())
	}
}

func TestStartMissingKernel(t *testing.T) {
	err := Start([]string{"--name", "test", "--kernel", "/nonexistent/vmlinux", "--rootfs", "/tmp"})
	if err == nil {
		t.Fatal("expected error for missing kernel")
	}
}
