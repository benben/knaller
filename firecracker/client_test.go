package firecracker

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testServer creates a Unix socket HTTP server for testing.
func testServer(t *testing.T, handler http.Handler) (socketPath string, cleanup func()) {
	t.Helper()
	dir := t.TempDir()
	sock := filepath.Join(dir, "test.socket")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: handler}
	go srv.Serve(ln)
	return sock, func() {
		srv.Close()
		ln.Close()
	}
}

func TestSetBootSource(t *testing.T) {
	var gotPath, gotMethod string
	var gotBody BootSource

	mux := http.NewServeMux()
	mux.HandleFunc("/boot-source", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusNoContent)
	})

	sock, cleanup := testServer(t, mux)
	defer cleanup()

	client := NewClient(sock)
	err := client.SetBootSource(context.Background(), &BootSource{
		KernelImagePath: "/vmlinux",
		BootArgs:        "console=ttyS0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotMethod != "PUT" {
		t.Errorf("method = %s, want PUT", gotMethod)
	}
	if gotPath != "/boot-source" {
		t.Errorf("path = %s, want /boot-source", gotPath)
	}
	if gotBody.KernelImagePath != "/vmlinux" {
		t.Errorf("kernel = %q, want /vmlinux", gotBody.KernelImagePath)
	}
}

func TestSetDrive(t *testing.T) {
	var gotPath string
	mux := http.NewServeMux()
	mux.HandleFunc("/drives/", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	})

	sock, cleanup := testServer(t, mux)
	defer cleanup()

	client := NewClient(sock)
	err := client.SetDrive(context.Background(), &Drive{
		DriveID:    "rootfs",
		PathOnHost: "/rootfs.ext4",
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/drives/rootfs" {
		t.Errorf("path = %s, want /drives/rootfs", gotPath)
	}
}

func TestSetMachineConfig(t *testing.T) {
	var gotBody MachineConfig
	mux := http.NewServeMux()
	mux.HandleFunc("/machine-config", func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusNoContent)
	})

	sock, cleanup := testServer(t, mux)
	defer cleanup()

	client := NewClient(sock)
	err := client.SetMachineConfig(context.Background(), &MachineConfig{
		VcpuCount:  4,
		MemSizeMib: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotBody.VcpuCount != 4 {
		t.Errorf("vcpu_count = %d, want 4", gotBody.VcpuCount)
	}
	if gotBody.MemSizeMib != 1024 {
		t.Errorf("mem_size_mib = %d, want 1024", gotBody.MemSizeMib)
	}
}

func TestSetNetworkInterface(t *testing.T) {
	var gotPath string
	mux := http.NewServeMux()
	mux.HandleFunc("/network-interfaces/", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	})

	sock, cleanup := testServer(t, mux)
	defer cleanup()

	client := NewClient(sock)
	err := client.SetNetworkInterface(context.Background(), &NetworkInterface{
		IfaceID:     "eth0",
		HostDevName: "tap0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/network-interfaces/eth0" {
		t.Errorf("path = %s, want /network-interfaces/eth0", gotPath)
	}
}

func TestStartInstance(t *testing.T) {
	var gotBody Action
	mux := http.NewServeMux()
	mux.HandleFunc("/actions", func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusNoContent)
	})

	sock, cleanup := testServer(t, mux)
	defer cleanup()

	client := NewClient(sock)
	err := client.StartInstance(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if gotBody.ActionType != "InstanceStart" {
		t.Errorf("action_type = %q, want InstanceStart", gotBody.ActionType)
	}
}

func TestStopInstance(t *testing.T) {
	var gotBody Action
	mux := http.NewServeMux()
	mux.HandleFunc("/actions", func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusNoContent)
	})

	sock, cleanup := testServer(t, mux)
	defer cleanup()

	client := NewClient(sock)
	err := client.StopInstance(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if gotBody.ActionType != "SendCtrlAltDel" {
		t.Errorf("action_type = %q, want SendCtrlAltDel", gotBody.ActionType)
	}
}

func TestGetInfo(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(InstanceInfo{
			AppName:    "Firecracker",
			ID:         "test-id",
			State:      "Running",
			VmmVersion: "1.14.1",
		})
	})

	sock, cleanup := testServer(t, mux)
	defer cleanup()

	client := NewClient(sock)
	info, err := client.GetInfo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.State != "Running" {
		t.Errorf("state = %q, want Running", info.State)
	}
	if info.VmmVersion != "1.14.1" {
		t.Errorf("vmm_version = %q, want 1.14.1", info.VmmVersion)
	}
}

func TestGetMachineConfig(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/machine-config", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(MachineConfig{VcpuCount: 2, MemSizeMib: 512})
	})

	sock, cleanup := testServer(t, mux)
	defer cleanup()

	client := NewClient(sock)
	mc, err := client.GetMachineConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if mc.VcpuCount != 2 {
		t.Errorf("vcpu_count = %d, want 2", mc.VcpuCount)
	}
}

func TestGetVMConfig(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/vm/config", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(VMConfig{
			BootSource:    &BootSource{KernelImagePath: "/vmlinux"},
			MachineConfig: &MachineConfig{VcpuCount: 4, MemSizeMib: 2048},
		})
	})

	sock, cleanup := testServer(t, mux)
	defer cleanup()

	client := NewClient(sock)
	vc, err := client.GetVMConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if vc.BootSource.KernelImagePath != "/vmlinux" {
		t.Errorf("kernel = %q", vc.BootSource.KernelImagePath)
	}
	if vc.MachineConfig.VcpuCount != 4 {
		t.Errorf("vcpu_count = %d, want 4", vc.MachineConfig.VcpuCount)
	}
}

func TestPauseVM(t *testing.T) {
	var gotMethod string
	var gotBody map[string]string
	mux := http.NewServeMux()
	mux.HandleFunc("/vm", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusNoContent)
	})

	sock, cleanup := testServer(t, mux)
	defer cleanup()

	client := NewClient(sock)
	if err := client.PauseVM(context.Background()); err != nil {
		t.Fatal(err)
	}
	if gotMethod != "PATCH" {
		t.Errorf("method = %s, want PATCH", gotMethod)
	}
	if gotBody["state"] != "Paused" {
		t.Errorf("state = %q, want Paused", gotBody["state"])
	}
}

func TestResumeVM(t *testing.T) {
	var gotBody map[string]string
	mux := http.NewServeMux()
	mux.HandleFunc("/vm", func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusNoContent)
	})

	sock, cleanup := testServer(t, mux)
	defer cleanup()

	client := NewClient(sock)
	if err := client.ResumeVM(context.Background()); err != nil {
		t.Fatal(err)
	}
	if gotBody["state"] != "Resumed" {
		t.Errorf("state = %q, want Resumed", gotBody["state"])
	}
}

func TestCreateSnapshot(t *testing.T) {
	var gotPath string
	var gotBody SnapshotCreate
	mux := http.NewServeMux()
	mux.HandleFunc("/snapshot/create", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusNoContent)
	})

	sock, cleanup := testServer(t, mux)
	defer cleanup()

	client := NewClient(sock)
	err := client.CreateSnapshot(context.Background(), &SnapshotCreate{
		SnapshotType: "Full",
		SnapshotPath: "/tmp/state",
		MemFilePath:  "/tmp/memory",
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/snapshot/create" {
		t.Errorf("path = %s, want /snapshot/create", gotPath)
	}
	if gotBody.SnapshotType != "Full" {
		t.Errorf("snapshot_type = %q, want Full", gotBody.SnapshotType)
	}
	if gotBody.SnapshotPath != "/tmp/state" {
		t.Errorf("snapshot_path = %q, want /tmp/state", gotBody.SnapshotPath)
	}
}

func TestLoadSnapshot(t *testing.T) {
	var gotPath, gotMethod string
	var gotBody map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("/snapshot/load", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusNoContent)
	})

	sock, cleanup := testServer(t, mux)
	defer cleanup()

	client := NewClient(sock)
	err := client.LoadSnapshot(context.Background(), "/tmp/state", "/tmp/memory")
	if err != nil {
		t.Fatal(err)
	}
	if gotMethod != "PUT" {
		t.Errorf("method = %s, want PUT", gotMethod)
	}
	if gotPath != "/snapshot/load" {
		t.Errorf("path = %s, want /snapshot/load", gotPath)
	}
	if gotBody["snapshot_path"] != "/tmp/state" {
		t.Errorf("snapshot_path = %v, want /tmp/state", gotBody["snapshot_path"])
	}
	memBackend, ok := gotBody["mem_backend"].(map[string]any)
	if !ok {
		t.Fatal("mem_backend missing or wrong type")
	}
	if memBackend["backend_type"] != "File" {
		t.Errorf("backend_type = %v, want File", memBackend["backend_type"])
	}
	if memBackend["backend_path"] != "/tmp/memory" {
		t.Errorf("backend_path = %v, want /tmp/memory", memBackend["backend_path"])
	}
	if gotBody["resume_vm"] != false {
		t.Errorf("resume_vm = %v, want false", gotBody["resume_vm"])
	}
}

func TestPatchDrive(t *testing.T) {
	var gotPath, gotMethod string
	var gotBody map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("/drives/", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusNoContent)
	})

	sock, cleanup := testServer(t, mux)
	defer cleanup()

	client := NewClient(sock)
	err := client.PatchDrive(context.Background(), "rootfs", "/new/rootfs.ext4")
	if err != nil {
		t.Fatal(err)
	}
	if gotMethod != "PATCH" {
		t.Errorf("method = %s, want PATCH", gotMethod)
	}
	if gotPath != "/drives/rootfs" {
		t.Errorf("path = %s, want /drives/rootfs", gotPath)
	}
	if gotBody["path_on_host"] != "/new/rootfs.ext4" {
		t.Errorf("path_on_host = %v, want /new/rootfs.ext4", gotBody["path_on_host"])
	}
}

func TestErrorResponse(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/boot-source", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrorResponse{FaultMessage: "invalid kernel path"})
	})

	sock, cleanup := testServer(t, mux)
	defer cleanup()

	client := NewClient(sock)
	err := client.SetBootSource(context.Background(), &BootSource{})
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); !strings.Contains(got, "invalid kernel path") {
		t.Errorf("error = %q, want to contain 'invalid kernel path'", got)
	}
}

func TestConnectionRefused(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "nonexistent.socket")
	// Create the socket file but don't listen on it
	os.WriteFile(sock, nil, 0o644)

	client := NewClient(sock)
	_, err := client.GetInfo(context.Background())
	if err == nil {
		t.Fatal("expected error for non-listening socket")
	}
}

