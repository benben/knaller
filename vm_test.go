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
// directory. Returns the base knaller data directory.
func setTestHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	base := filepath.Join(dir, ".local", "share", "knaller")
	os.MkdirAll(filepath.Join(base, "sockets"), 0o755)
	os.MkdirAll(filepath.Join(base, "vms"), 0o755)
	return base
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
	// Don't create any dirs — List() should handle missing dirs.

	vms, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(vms) != 0 {
		t.Errorf("expected 0 VMs, got %d", len(vms))
	}
}

func TestListWithMockSocket(t *testing.T) {
	base := setTestHome(t)

	// Create a VM data directory so List() discovers it.
	os.MkdirAll(filepath.Join(base, "vms", "testvm"), 0o755)

	// Create a mock Firecracker server
	socketPath := filepath.Join(base, "sockets", "testvm.socket")
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

func TestListStoppedVM(t *testing.T) {
	base := setTestHome(t)

	// Create a VM data directory with no live socket — should appear as Stopped.
	os.MkdirAll(filepath.Join(base, "vms", "stoppedvm"), 0o755)

	vms, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(vms) != 1 {
		t.Fatalf("expected 1 VM, got %d", len(vms))
	}
	if vms[0].Status != "Stopped" {
		t.Errorf("status = %q, want Stopped", vms[0].Status)
	}
	if vms[0].Name != "stoppedvm" {
		t.Errorf("name = %q, want stoppedvm", vms[0].Name)
	}
}

func TestMergeUniquePorts(t *testing.T) {
	tests := []struct {
		name string
		a, b []PortMapping
		want []PortMapping
	}{
		{"no overlap", []PortMapping{{5432, 5432}}, []PortMapping{{8080, 80}}, []PortMapping{{5432, 5432}, {8080, 80}}},
		{"exact duplicate", []PortMapping{{5432, 5432}}, []PortMapping{{5432, 5432}}, []PortMapping{{5432, 5432}}},
		{"host port conflict", []PortMapping{{5432, 5432}}, []PortMapping{{5432, 3306}}, []PortMapping{{5432, 5432}}},
		{"both nil", nil, nil, nil},
		{"a nil", nil, []PortMapping{{80, 80}}, []PortMapping{{80, 80}}},
		{"b nil", []PortMapping{{80, 80}}, nil, []PortMapping{{80, 80}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergeUniquePorts(tt.a, tt.b)
			if len(got) != len(tt.want) {
				t.Fatalf("len = %d, want %d: %v", len(got), len(tt.want), got)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("[%d] = %v, want %v", i, got[i], tt.want[i])
				}
			}
		})
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

// startMockFirecracker stands up a fake firecracker API on a Unix socket and
// returns the socket path. It answers GET / with a Running InstanceInfo so
// AdoptVM's GetInfo health check passes.
func startMockFirecracker(t *testing.T, name string) string {
	t.Helper()
	base := setTestHome(t)
	socketPath := filepath.Join(base, "sockets", name+".socket")
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(firecracker.InstanceInfo{
			State:      "Running",
			VmmVersion: "1.14.1",
		})
	})
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	t.Cleanup(func() {
		srv.Close()
		ln.Close()
	})
	return socketPath
}

func TestAdoptVMSuccess(t *testing.T) {
	name := "adoptme"
	socketPath := startMockFirecracker(t, name)

	// os.Getpid() is guaranteed alive — kill -0 will succeed.
	vm, err := AdoptVM(name, socketPath, os.Getpid())
	if err != nil {
		t.Fatalf("AdoptVM: %v", err)
	}
	if vm.Name != name {
		t.Errorf("Name = %q, want %q", vm.Name, name)
	}
	if vm.PID != os.Getpid() {
		t.Errorf("PID = %d, want %d", vm.PID, os.Getpid())
	}
	if vm.SocketPath != socketPath {
		t.Errorf("SocketPath = %q, want %q", vm.SocketPath, socketPath)
	}
	if vm.Status != "Running" {
		t.Errorf("Status = %q, want Running", vm.Status)
	}
	if vm.Port == 0 {
		t.Error("expected non-zero Port (derived from name)")
	}
	if vm.cmd != nil {
		t.Error("expected cmd == nil for adopted VM")
	}
	if vm.client == nil {
		t.Error("expected client to be set")
	}
}

func TestAdoptVMInvalidPID(t *testing.T) {
	if _, err := AdoptVM("x", "/tmp/does-not-matter", 0); err == nil {
		t.Fatal("expected error for pid=0")
	}
	if _, err := AdoptVM("x", "/tmp/does-not-matter", -1); err == nil {
		t.Fatal("expected error for negative pid")
	}
}

func TestAdoptVMDeadPID(t *testing.T) {
	socketPath := startMockFirecracker(t, "deadpid")

	// PID_MAX_LIMIT on Linux is 2^22 = 4194304; using 4194303 is virtually
	// guaranteed to be unallocated. We don't care about the exact errno —
	// just that AdoptVM bails before opening the socket.
	deadPID := 4194303
	if _, err := AdoptVM("deadpid", socketPath, deadPID); err == nil {
		t.Fatal("expected error for dead pid")
	}
}

func TestAdoptVMMissingSocket(t *testing.T) {
	dir := setTestHome(t)
	missing := filepath.Join(dir, "sockets", "ghost.socket")

	if _, err := AdoptVM("ghost", missing, os.Getpid()); err == nil {
		t.Fatal("expected error for missing socket")
	}
}

func TestVMKillNoOpWithZeroPID(t *testing.T) {
	// An adopted VM with cmd=nil and PID<=0 is a degenerate case (the
	// caller misused the type), but Kill should not panic — it should
	// be a clean no-op.
	vm := &VM{Name: "x"}
	if err := vm.Kill(); err != nil {
		t.Fatalf("Kill on zero-PID adopted VM: %v", err)
	}
}

func TestVMWaitNoOpWithZeroPID(t *testing.T) {
	vm := &VM{Name: "x"}
	// Wait on a zero-PID adopted VM returns immediately with no error.
	if err := vm.Wait(); err != nil {
		t.Fatalf("Wait on zero-PID adopted VM: %v", err)
	}
}
