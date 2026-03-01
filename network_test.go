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
	got := nc.bootArgsIP()
	want := "ip=172.16.0.2::172.16.0.1:255.255.255.252::eth0:off"
	if got != want {
		t.Errorf("bootArgsIP() = %q, want %q", got, want)
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
