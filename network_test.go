package knaller

import (
	"net"
	"strings"
	"testing"
)

func TestTapDevName(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"myvm", "kn-myvm"},
		{"short", "kn-short"},
		{"verylongname123", "kn-verylong"},
		{"12345678", "kn-12345678"},
		{"123456789", "kn-12345678"},
	}
	for _, tt := range tests {
		got := tapDevName(tt.name)
		if got != tt.want {
			t.Errorf("tapDevName(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestSubnetIndex(t *testing.T) {
	// Should be deterministic
	idx1 := subnetIndex("myvm")
	idx2 := subnetIndex("myvm")
	if idx1 != idx2 {
		t.Errorf("subnetIndex not deterministic: %d != %d", idx1, idx2)
	}

	// Should be in range 0-16383
	if idx1 > 0x3FFF {
		t.Errorf("subnetIndex(%q) = %d, exceeds 16383", "myvm", idx1)
	}

	// Different names should (usually) produce different indices
	idx3 := subnetIndex("other")
	if idx1 == idx3 {
		t.Log("warning: different names produced same subnet index (unlikely but possible)")
	}
}

func TestGuestMAC(t *testing.T) {
	mac := guestMAC("myvm")

	// Should start with AA:FC:00
	if !strings.HasPrefix(mac, "AA:FC:00:") {
		t.Errorf("MAC %q doesn't have expected prefix", mac)
	}

	// Should be valid MAC format (6 octets)
	_, err := net.ParseMAC(mac)
	if err != nil {
		t.Errorf("invalid MAC %q: %v", mac, err)
	}

	// Should be deterministic
	mac2 := guestMAC("myvm")
	if mac != mac2 {
		t.Errorf("guestMAC not deterministic: %q != %q", mac, mac2)
	}
}

func TestNetworkConfigBootArgsIP(t *testing.T) {
	nc := &networkConfig{
		HostIP:  net.IPv4(172, 16, 0, 1),
		GuestIP: net.IPv4(172, 16, 0, 2),
	}

	// No DNS servers.
	got := nc.bootArgsIP(nil)
	want := "ip=172.16.0.2::172.16.0.1:255.255.255.252::eth0:off"
	if got != want {
		t.Errorf("bootArgsIP(nil) = %q, want %q", got, want)
	}

	// One DNS server.
	got = nc.bootArgsIP([]string{"1.1.1.1"})
	want = "ip=172.16.0.2::172.16.0.1:255.255.255.252::eth0:off:1.1.1.1"
	if got != want {
		t.Errorf("bootArgsIP([1.1.1.1]) = %q, want %q", got, want)
	}

	// Two DNS servers.
	got = nc.bootArgsIP([]string{"1.1.1.1", "8.8.8.8"})
	want = "ip=172.16.0.2::172.16.0.1:255.255.255.252::eth0:off:1.1.1.1:8.8.8.8"
	if got != want {
		t.Errorf("bootArgsIP([1.1.1.1, 8.8.8.8]) = %q, want %q", got, want)
	}

	// More than two DNS servers — only first two used.
	got = nc.bootArgsIP([]string{"1.1.1.1", "8.8.8.8", "9.9.9.9"})
	want = "ip=172.16.0.2::172.16.0.1:255.255.255.252::eth0:off:1.1.1.1:8.8.8.8"
	if got != want {
		t.Errorf("bootArgsIP([3 servers]) = %q, want %q", got, want)
	}
}

func TestIPAllocation(t *testing.T) {
	// Verify that host and guest IPs form a valid /30 pair
	subnet := subnetIndex("test-vm")
	hostIP := net.IPv4(172, 16, byte(subnet>>8), byte(subnet<<2|1))
	guestIP := net.IPv4(172, 16, byte(subnet>>8), byte(subnet<<2|2))

	// Both should be valid IPv4
	if hostIP.To4() == nil {
		t.Error("hostIP is not valid IPv4")
	}
	if guestIP.To4() == nil {
		t.Error("guestIP is not valid IPv4")
	}

	// Guest should be host+1
	h := hostIP.To4()
	g := guestIP.To4()
	if h[0] != g[0] || h[1] != g[1] || h[2] != g[2] || h[3]+1 != g[3] {
		t.Errorf("IPs not adjacent: host=%s, guest=%s", hostIP, guestIP)
	}
}

func TestDeriveNetwork(t *testing.T) {
	nc := deriveNetwork("myvm")

	if nc.TAPDevice != "kn-myvm" {
		t.Errorf("TAPDevice = %q, want %q", nc.TAPDevice, "kn-myvm")
	}
	if nc.HostIP.To4() == nil {
		t.Error("HostIP is not valid IPv4")
	}
	if nc.GuestIP.To4() == nil {
		t.Error("GuestIP is not valid IPv4")
	}
	if !strings.HasPrefix(nc.GuestMAC, "AA:FC:00:") {
		t.Errorf("GuestMAC %q doesn't have expected prefix", nc.GuestMAC)
	}

	// Host and guest should be adjacent in a /30
	h := nc.HostIP.To4()
	g := nc.GuestIP.To4()
	if h[3]+1 != g[3] {
		t.Errorf("IPs not adjacent: host=%s, guest=%s", nc.HostIP, nc.GuestIP)
	}

	// SSHPort should be in range 10000-59999
	if nc.SSHPort < 10000 || nc.SSHPort >= 60000 {
		t.Errorf("SSHPort = %d, want 10000-59999", nc.SSHPort)
	}

	// Should be deterministic
	nc2 := deriveNetwork("myvm")
	if nc.TAPDevice != nc2.TAPDevice || !nc.HostIP.Equal(nc2.HostIP) ||
		!nc.GuestIP.Equal(nc2.GuestIP) || nc.GuestMAC != nc2.GuestMAC ||
		nc.SSHPort != nc2.SSHPort {
		t.Error("deriveNetwork not deterministic")
	}
}

func TestSSHPort(t *testing.T) {
	// Should be deterministic
	p1 := sshPort("myvm")
	p2 := sshPort("myvm")
	if p1 != p2 {
		t.Errorf("sshPort not deterministic: %d != %d", p1, p2)
	}

	// Should be in range 10000-59999
	if p1 < 10000 || p1 >= 60000 {
		t.Errorf("sshPort(%q) = %d, want 10000-59999", "myvm", p1)
	}

	// Different names should (usually) produce different ports
	p3 := sshPort("other")
	if p1 == p3 {
		t.Log("warning: different names produced same SSH port (unlikely but possible)")
	}
}

func TestNamespaceSetupScript(t *testing.T) {
	nc := &networkConfig{
		TAPDevice: "kn-myvm",
		HostIP:    net.IPv4(172, 16, 0, 1),
		GuestIP:   net.IPv4(172, 16, 0, 2),
		GuestMAC:  "AA:FC:00:01:02:03",
	}

	script := namespaceSetupScript(nc, "/usr/bin/firecracker", "/tmp/test.socket")

	// Should contain all expected commands
	expects := []string{
		"ip tuntap add dev kn-myvm mode tap",
		"ip addr add 172.16.0.1/30 dev kn-myvm",
		"ip link set kn-myvm up",
		"sysctl -qw net.ipv4.ip_forward=1",
		"dnat to 172.16.0.2",
		"masquerade",
		"exec /usr/bin/firecracker --api-sock /tmp/test.socket --enable-pci",
	}
	for _, want := range expects {
		if !strings.Contains(script, want) {
			t.Errorf("script missing %q\ngot: %s", want, script)
		}
	}
}
