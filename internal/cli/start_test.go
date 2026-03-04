package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExpandHome(t *testing.T) {
	home, _ := os.UserHomeDir()

	got := expandHome("~/.local/share/knaller/vmlinux")
	want := filepath.Join(home, ".local", "share", "knaller", "vmlinux")
	if got != want {
		t.Errorf("expandHome = %q, want %q", got, want)
	}

	// Non-tilde path should be unchanged.
	if expandHome("/tmp/foo") != "/tmp/foo" {
		t.Error("expandHome changed non-tilde path")
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
