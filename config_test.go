package knaller

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigDefaults(t *testing.T) {
	cfg := &Config{}
	cfg.setDefaults()

	if cfg.Name == "" {
		t.Error("expected auto-generated name")
	}
	if len(cfg.Name) != 8 {
		t.Errorf("expected 8-char hex name, got %q (len %d)", cfg.Name, len(cfg.Name))
	}
	if cfg.CPUs != 1 {
		t.Errorf("CPUs = %d, want 1", cfg.CPUs)
	}
	if cfg.Memory != 128 {
		t.Errorf("Memory = %d, want 128", cfg.Memory)
	}
	if cfg.FirecrackerBin != "firecracker" {
		t.Errorf("FirecrackerBin = %q, want firecracker", cfg.FirecrackerBin)
	}
	if cfg.Stdout != os.Stdout {
		t.Error("expected Stdout = os.Stdout")
	}
	if cfg.Stdin != os.Stdin {
		t.Error("expected Stdin = os.Stdin")
	}
	if cfg.Stderr != os.Stderr {
		t.Error("expected Stderr = os.Stderr")
	}
}

func TestConfigDefaultsPreserveValues(t *testing.T) {
	cfg := &Config{
		Name:   "myvm",
		CPUs:   4,
		Memory: 512,
	}
	cfg.setDefaults()

	if cfg.Name != "myvm" {
		t.Errorf("Name = %q, want myvm", cfg.Name)
	}
	if cfg.CPUs != 4 {
		t.Errorf("CPUs = %d, want 4", cfg.CPUs)
	}
	if cfg.Memory != 512 {
		t.Errorf("Memory = %d, want 512", cfg.Memory)
	}
}

func TestConfigValidateMissingKernel(t *testing.T) {
	cfg := &Config{RootFS: "/tmp"}
	cfg.setDefaults()
	err := cfg.validate()
	if err == nil {
		t.Fatal("expected error for missing kernel")
	}
	if got := err.Error(); got != "kernel path is required" {
		t.Errorf("error = %q", got)
	}
}

func TestConfigValidateMissingRootFS(t *testing.T) {
	dir := t.TempDir()
	kernel := filepath.Join(dir, "vmlinux")
	os.WriteFile(kernel, []byte("fake"), 0o644)

	cfg := &Config{Kernel: kernel}
	cfg.setDefaults()
	err := cfg.validate()
	if err == nil {
		t.Fatal("expected error for missing rootfs")
	}
	if got := err.Error(); got != "rootfs path is required" {
		t.Errorf("error = %q", got)
	}
}

func TestConfigValidateNonexistentKernel(t *testing.T) {
	cfg := &Config{Kernel: "/nonexistent/vmlinux", RootFS: "/tmp"}
	cfg.setDefaults()
	err := cfg.validate()
	if err == nil {
		t.Fatal("expected error for nonexistent kernel")
	}
}

func TestConfigValidateBadCPUs(t *testing.T) {
	dir := t.TempDir()
	kernel := filepath.Join(dir, "vmlinux")
	rootfs := filepath.Join(dir, "rootfs.ext4")
	os.WriteFile(kernel, []byte("fake"), 0o644)
	os.WriteFile(rootfs, []byte("fake"), 0o644)

	cfg := &Config{Kernel: kernel, RootFS: rootfs, CPUs: -1, Memory: 128}
	err := cfg.validate()
	if err == nil {
		t.Fatal("expected error for negative CPUs")
	}
}

func TestConfigValidateBadMemory(t *testing.T) {
	dir := t.TempDir()
	kernel := filepath.Join(dir, "vmlinux")
	rootfs := filepath.Join(dir, "rootfs.ext4")
	os.WriteFile(kernel, []byte("fake"), 0o644)
	os.WriteFile(rootfs, []byte("fake"), 0o644)

	cfg := &Config{Kernel: kernel, RootFS: rootfs, CPUs: 1, Memory: 64}
	err := cfg.validate()
	if err == nil {
		t.Fatal("expected error for memory < 128")
	}
}

func TestConfigValidateSuccess(t *testing.T) {
	dir := t.TempDir()
	kernel := filepath.Join(dir, "vmlinux")
	rootfs := filepath.Join(dir, "rootfs.ext4")
	os.WriteFile(kernel, []byte("fake"), 0o644)
	os.WriteFile(rootfs, []byte("fake"), 0o644)

	cfg := &Config{Kernel: kernel, RootFS: rootfs}
	cfg.setDefaults()
	if err := cfg.validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRandomName(t *testing.T) {
	name1 := randomName()
	name2 := randomName()
	if name1 == name2 {
		t.Error("expected unique names")
	}
	if len(name1) != 8 {
		t.Errorf("expected 8-char name, got %q", name1)
	}
}
