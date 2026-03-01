package firecracker

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBootSourceJSON(t *testing.T) {
	bs := BootSource{
		KernelImagePath: "/path/to/vmlinux",
		BootArgs:        "console=ttyS0",
		InitrdPath:      "/path/to/initrd",
	}
	data, err := json.Marshal(bs)
	if err != nil {
		t.Fatal(err)
	}
	var got BootSource
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got != bs {
		t.Errorf("got %+v, want %+v", got, bs)
	}
}

func TestBootSourceJSONOmitsEmpty(t *testing.T) {
	bs := BootSource{KernelImagePath: "/vmlinux"}
	data, err := json.Marshal(bs)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if want := `"boot_args"`; strings.Contains(s, want) {
		t.Errorf("expected empty boot_args to be omitted, got %s", s)
	}
}

func TestDriveJSON(t *testing.T) {
	d := Drive{
		DriveID:      "rootfs",
		PathOnHost:   "/path/to/rootfs.ext4",
		IsRootDevice: true,
		IsReadOnly:   false,
	}
	data, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	var got Drive
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got != d {
		t.Errorf("got %+v, want %+v", got, d)
	}
}

func TestMachineConfigJSON(t *testing.T) {
	mc := MachineConfig{VcpuCount: 2, MemSizeMib: 512, Smt: true}
	data, err := json.Marshal(mc)
	if err != nil {
		t.Fatal(err)
	}
	var got MachineConfig
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got != mc {
		t.Errorf("got %+v, want %+v", got, mc)
	}
}

func TestNetworkInterfaceJSON(t *testing.T) {
	ni := NetworkInterface{
		IfaceID:     "eth0",
		HostDevName: "tap0",
		GuestMAC:    "AA:FC:00:01:02:03",
	}
	data, err := json.Marshal(ni)
	if err != nil {
		t.Fatal(err)
	}
	var got NetworkInterface
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got != ni {
		t.Errorf("got %+v, want %+v", got, ni)
	}
}

func TestActionJSON(t *testing.T) {
	a := Action{ActionType: "InstanceStart"}
	data, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"action_type":"InstanceStart"}`
	if string(data) != want {
		t.Errorf("got %s, want %s", data, want)
	}
}

func TestInstanceInfoJSON(t *testing.T) {
	raw := `{"app_name":"Firecracker","id":"abc123","state":"Running","vmm_version":"1.14.1"}`
	var info InstanceInfo
	if err := json.Unmarshal([]byte(raw), &info); err != nil {
		t.Fatal(err)
	}
	if info.State != "Running" {
		t.Errorf("got state %q, want %q", info.State, "Running")
	}
	if info.VmmVersion != "1.14.1" {
		t.Errorf("got vmm_version %q, want %q", info.VmmVersion, "1.14.1")
	}
}

func TestErrorResponseJSON(t *testing.T) {
	raw := `{"fault_message":"something went wrong"}`
	var e ErrorResponse
	if err := json.Unmarshal([]byte(raw), &e); err != nil {
		t.Fatal(err)
	}
	if e.FaultMessage != "something went wrong" {
		t.Errorf("got %q, want %q", e.FaultMessage, "something went wrong")
	}
}

func TestVMConfigJSON(t *testing.T) {
	raw := `{
		"boot-source": {"kernel_image_path": "/vmlinux", "boot_args": "console=ttyS0"},
		"drives": [{"drive_id": "rootfs", "path_on_host": "/rootfs.ext4", "is_root_device": true, "is_read_only": false}],
		"machine-config": {"vcpu_count": 2, "mem_size_mib": 256, "smt": false},
		"network-interfaces": [{"iface_id": "eth0", "host_dev_name": "tap0"}]
	}`
	var vc VMConfig
	if err := json.Unmarshal([]byte(raw), &vc); err != nil {
		t.Fatal(err)
	}
	if vc.BootSource.KernelImagePath != "/vmlinux" {
		t.Errorf("got kernel %q", vc.BootSource.KernelImagePath)
	}
	if len(vc.Drives) != 1 || vc.Drives[0].DriveID != "rootfs" {
		t.Errorf("unexpected drives: %+v", vc.Drives)
	}
	if vc.MachineConfig.VcpuCount != 2 {
		t.Errorf("got vcpu_count %d, want 2", vc.MachineConfig.VcpuCount)
	}
	if len(vc.NetworkInterfaces) != 1 {
		t.Errorf("unexpected network interfaces: %+v", vc.NetworkInterfaces)
	}
}

