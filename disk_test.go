package knaller

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrepareDisk(t *testing.T) {
	// Create a fake base rootfs
	dir := t.TempDir()
	baseRootFS := filepath.Join(dir, "rootfs.ext4")
	content := []byte("fake rootfs content for testing")
	if err := os.WriteFile(baseRootFS, content, 0o644); err != nil {
		t.Fatal(err)
	}

	// Override home dir for test
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", dir)
	defer os.Setenv("HOME", origHome)

	name := "test-vm"
	diskPath, err := prepareDisk(name, baseRootFS, 0)
	if err != nil {
		t.Fatal(err)
	}

	// Verify the copy exists
	got, err := os.ReadFile(diskPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Errorf("disk content mismatch: got %q", got)
	}

	// Verify path is under the expected directory
	expectedDir := filepath.Join(dir, ".local", "share", "knaller", "vms", name)
	if filepath.Dir(diskPath) != expectedDir {
		t.Errorf("disk path = %q, expected dir %q", diskPath, expectedDir)
	}
}

func TestPrepareDiskRootFSSizeNoGrowthBelowCurrent(t *testing.T) {
	// When rootFSSize <= current size, prepareDisk must not invoke
	// e2fsck/resize2fs (which would fail on our non-ext4 fake content).
	dir := t.TempDir()
	baseRootFS := filepath.Join(dir, "rootfs.ext4")
	content := []byte("rootfs content larger than the requested grow target")
	if err := os.WriteFile(baseRootFS, content, 0o644); err != nil {
		t.Fatal(err)
	}

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", dir)
	defer os.Setenv("HOME", origHome)

	// rootFSSize is smaller than current → growth path must be skipped.
	diskPath, err := prepareDisk("vm-no-grow", baseRootFS, 4)
	if err != nil {
		t.Fatalf("prepareDisk with sub-current rootFSSize: %v", err)
	}
	st, err := os.Stat(diskPath)
	if err != nil {
		t.Fatal(err)
	}
	if st.Size() != int64(len(content)) {
		t.Errorf("size = %d, want %d (no growth expected)", st.Size(), len(content))
	}
}

func TestRemoveDisk(t *testing.T) {
	dir := t.TempDir()
	baseRootFS := filepath.Join(dir, "rootfs.ext4")
	os.WriteFile(baseRootFS, []byte("fake"), 0o644)

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", dir)
	defer os.Setenv("HOME", origHome)

	name := "test-rm-vm"
	diskPath, err := prepareDisk(name, baseRootFS, 0)
	if err != nil {
		t.Fatal(err)
	}

	// Verify it exists
	if _, err := os.Stat(diskPath); err != nil {
		t.Fatal("disk should exist before removal")
	}

	if err := removeDisk(name); err != nil {
		t.Fatal(err)
	}

	// Verify it's gone
	if _, err := os.Stat(diskPath); !os.IsNotExist(err) {
		t.Error("disk should not exist after removal")
	}
}
