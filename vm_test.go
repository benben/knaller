package knaller

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/benben/knaller/firecracker"
)

// setTestHome overrides HOME so socketDirectory() and vmDataDir() use a temp
// directory. Returns the sockets directory path.
func setTestHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	// Create the sockets dir so List() finds it.
	os.MkdirAll(filepath.Join(dir, ".local", "share", "knaller", "sockets"), 0o755)
	return filepath.Join(dir, ".local", "share", "knaller", "sockets")
}

func TestListEmptyDir(t *testing.T) {
	setTestHome(t)

	vms, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(vms) != 0 {
		t.Errorf("expected 0 VMs, got %d", len(vms))
	}
}

func TestListNoDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	// Don't create the sockets dir — List() should handle missing dir.

	vms, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if vms != nil {
		t.Errorf("expected nil, got %v", vms)
	}
}

func TestListWithMockSocket(t *testing.T) {
	socketDir := setTestHome(t)

	// Create a mock Firecracker server
	socketPath := filepath.Join(socketDir, "testvm.socket")
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(firecracker.InstanceInfo{
			State:      "Running",
			VmmVersion: "1.14.1",
		})
	})
	mux.HandleFunc("/vm/config", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(firecracker.VMConfig{
			BootSource: &firecracker.BootSource{
				BootArgs: "reboot=k panic=1 net.ifnames=0 ip=172.16.0.2::172.16.0.1:255.255.255.252::eth0:off",
			},
			MachineConfig: &firecracker.MachineConfig{
				VcpuCount:  2,
				MemSizeMib: 256,
			},
		})
	})

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	defer srv.Close()

	vms, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(vms) != 1 {
		t.Fatalf("expected 1 VM, got %d", len(vms))
	}
	vm := vms[0]
	if vm.Name != "testvm" {
		t.Errorf("name = %q, want testvm", vm.Name)
	}
	if vm.Status != "Running" {
		t.Errorf("status = %q, want Running", vm.Status)
	}
	if vm.CPUs != 2 {
		t.Errorf("cpus = %g, want 2", vm.CPUs)
	}
	if vm.Memory != 256 {
		t.Errorf("memory = %d, want 256", vm.Memory)
	}
	if vm.Port == 0 {
		t.Error("expected non-zero Port")
	}
}

func TestListStaleSocket(t *testing.T) {
	socketDir := setTestHome(t)

	// Create a stale socket file (regular file, no listener)
	os.WriteFile(filepath.Join(socketDir, "stale.socket"), nil, 0o644)

	vms, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(vms) != 0 {
		t.Errorf("expected 0 VMs (stale socket should be skipped), got %d", len(vms))
	}
}

func TestParseGuestIP(t *testing.T) {
	tests := []struct {
		bootArgs string
		want     string
	}{
		{
			"reboot=k panic=1 net.ifnames=0 ip=172.16.0.2::172.16.0.1:255.255.255.252::eth0:off",
			"172.16.0.2",
		},
		{
			"reboot=k panic=1",
			"",
		},
		{
			"ip=10.0.0.5::10.0.0.1:255.255.255.0::eth0:off reboot=k",
			"10.0.0.5",
		},
	}
	for _, tt := range tests {
		got := parseGuestIP(tt.bootArgs)
		if got != tt.want {
			t.Errorf("parseGuestIP(%q) = %q, want %q", tt.bootArgs, got, tt.want)
		}
	}
}
