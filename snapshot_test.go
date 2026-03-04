package knaller

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSnapshotID(t *testing.T) {
	id1 := snapshotID()
	id2 := snapshotID()

	if !strings.HasPrefix(id1, "snap_") {
		t.Errorf("id %q missing snap_ prefix", id1)
	}
	if len(id1) != 13 { // "snap_" (5) + 8 hex chars
		t.Errorf("id %q length = %d, want 13", id1, len(id1))
	}
	if id1 == id2 {
		t.Error("expected unique IDs")
	}
}

func TestListSnapshotsNoDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	snapshots, err := ListSnapshots()
	if err != nil {
		t.Fatal(err)
	}
	if snapshots != nil {
		t.Errorf("expected nil, got %v", snapshots)
	}
}

func TestListSnapshotsEmptyDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	os.MkdirAll(filepath.Join(dir, ".local", "share", "knaller", "snapshots"), 0o755)

	snapshots, err := ListSnapshots()
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 0 {
		t.Errorf("expected 0 snapshots, got %d", len(snapshots))
	}
}

func TestListSnapshotsWithMetadata(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	snapDir := filepath.Join(dir, ".local", "share", "knaller", "snapshots", "snap_abcd1234")
	os.MkdirAll(snapDir, 0o755)

	meta := &Snapshot{
		ID:         "snap_abcd1234",
		VMName:     "testvm",
		CreatedAt:  time.Now(),
		VCPUs:      2,
		MemSizeMib: 512,
	}
	data, _ := json.Marshal(meta)
	os.WriteFile(filepath.Join(snapDir, "metadata.json"), data, 0o644)

	snapshots, err := ListSnapshots()
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(snapshots))
	}
	s := snapshots[0]
	if s.ID != "snap_abcd1234" {
		t.Errorf("id = %q, want snap_abcd1234", s.ID)
	}
	if s.VMName != "testvm" {
		t.Errorf("vm_name = %q, want testvm", s.VMName)
	}
	if s.VCPUs != 2 {
		t.Errorf("vcpus = %d, want 2", s.VCPUs)
	}
	if s.MemSizeMib != 512 {
		t.Errorf("mem = %d, want 512", s.MemSizeMib)
	}
}

func TestListSnapshotsSkipsCorrupt(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	snapDir := filepath.Join(dir, ".local", "share", "knaller", "snapshots", "snap_bad00000")
	os.MkdirAll(snapDir, 0o755)
	os.WriteFile(filepath.Join(snapDir, "metadata.json"), []byte("not json"), 0o644)

	snapshots, err := ListSnapshots()
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 0 {
		t.Errorf("expected 0 snapshots (corrupt should be skipped), got %d", len(snapshots))
	}
}

func TestGetSnapshot(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	snapDir := filepath.Join(dir, ".local", "share", "knaller", "snapshots", "snap_abcd1234")
	os.MkdirAll(snapDir, 0o755)

	meta := &Snapshot{
		ID:         "snap_abcd1234",
		VMName:     "testvm",
		VCPUs:      2,
		MemSizeMib: 512,
	}
	data, _ := json.Marshal(meta)
	os.WriteFile(filepath.Join(snapDir, "metadata.json"), data, 0o644)

	s, err := GetSnapshot("snap_abcd1234")
	if err != nil {
		t.Fatal(err)
	}
	if s.VMName != "testvm" {
		t.Errorf("vm_name = %q, want testvm", s.VMName)
	}
	if s.VCPUs != 2 {
		t.Errorf("vcpus = %d, want 2", s.VCPUs)
	}
}

func TestDeleteSnapshot(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	snapDir := filepath.Join(dir, ".local", "share", "knaller", "snapshots", "snap_abcd1234")
	os.MkdirAll(snapDir, 0o755)
	os.WriteFile(filepath.Join(snapDir, "metadata.json"), []byte("{}"), 0o644)
	os.WriteFile(filepath.Join(snapDir, "state"), []byte("state"), 0o644)

	if err := DeleteSnapshot("snap_abcd1234"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(snapDir); !os.IsNotExist(err) {
		t.Error("snapshot dir should be deleted")
	}
}

func TestDeleteSnapshotNotFound(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	err := DeleteSnapshot("snap_nonexist")
	if err == nil {
		t.Fatal("expected error for missing snapshot")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestGetSnapshotNotFound(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	_, err := GetSnapshot("snap_nonexist")
	if err == nil {
		t.Fatal("expected error for missing snapshot")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestListSnapshotsSkipsNonSnapDirs(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	baseDir := filepath.Join(dir, ".local", "share", "knaller", "snapshots")
	os.MkdirAll(filepath.Join(baseDir, "not_a_snapshot"), 0o755)
	os.WriteFile(filepath.Join(baseDir, "somefile"), nil, 0o644)

	snapshots, err := ListSnapshots()
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 0 {
		t.Errorf("expected 0 snapshots, got %d", len(snapshots))
	}
}
