package knaller

import (
	"net"
	"strings"
	"testing"
)

func TestNameHash8Stable(t *testing.T) {
	a := nameHash8("box-abc")
	b := nameHash8("box-abc")
	if a != b {
		t.Fatalf("nameHash8 not stable: %q != %q", a, b)
	}
	if len(a) != 8 {
		t.Fatalf("nameHash8 length = %d, want 8", len(a))
	}
	for _, c := range a {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Fatalf("nameHash8 has non-hex char %q in %q", c, a)
		}
	}
}

func TestNameHash8Distinct(t *testing.T) {
	if nameHash8("alpha") == nameHash8("beta") {
		t.Fatal("expected distinct hashes for distinct names")
	}
}

func TestVethSubnetIndexInRange(t *testing.T) {
	for _, name := range []string{"a", "long-name-here", "box-12345678", ""} {
		idx := vethSubnetIndex(name)
		if idx >= 1<<14 {
			t.Errorf("name %q: idx %d exceeds 14-bit range", name, idx)
		}
	}
}

func TestVethHostGuestIPInSupernet(t *testing.T) {
	for _, name := range []string{"box-1", "box-2", "abc"} {
		h4 := vethHostIP(name).To4()
		g4 := vethGuestIP(name).To4()
		if h4 == nil || h4[0] != 172 || h4[1] != 20 {
			t.Errorf("name %q: vethHostIP=%v not in 172.20/16", name, h4)
		}
		if g4 == nil || g4[0] != 172 || g4[1] != 20 {
			t.Errorf("name %q: vethGuestIP=%v not in 172.20/16", name, g4)
		}
		// Host and guest must be the .1 and .2 hosts inside the same /30.
		if h4[2] != g4[2] {
			t.Errorf("name %q: hIP and gIP not in same /30 (octet[2])", name)
		}
		if h4[3]&0xFC != g4[3]&0xFC {
			t.Errorf("name %q: hIP and gIP not in same /30 (octet[3])", name)
		}
		if h4[3]&0x03 != 1 || g4[3]&0x03 != 2 {
			t.Errorf("name %q: expected host low-2-bits=01, guest=10, got %v/%v", name, h4, g4)
		}
	}
}

func TestVethIPsDeterministic(t *testing.T) {
	if !vethHostIP("vmA").Equal(vethHostIP("vmA")) {
		t.Fatal("vethHostIP not deterministic")
	}
	if !vethGuestIP("vmA").Equal(vethGuestIP("vmA")) {
		t.Fatal("vethGuestIP not deterministic")
	}
}

func TestNetnsAndVethNamePrefixes(t *testing.T) {
	name := "box-deadbeef"
	if got := netnsName(name); !strings.HasPrefix(got, "kn-") {
		t.Errorf("netnsName=%q, want kn- prefix", got)
	}
	if got := vethHostName(name); !strings.HasPrefix(got, "vh-") {
		t.Errorf("vethHostName=%q, want vh- prefix", got)
	}
	if got := vethGuestName(name); !strings.HasPrefix(got, "vg-") {
		t.Errorf("vethGuestName=%q, want vg- prefix", got)
	}
	// All three must fit IFNAMSIZ-1 = 15.
	for _, n := range []string{netnsName(name), vethHostName(name), vethGuestName(name)} {
		if len(n) > 15 {
			t.Errorf("name %q exceeds IFNAMSIZ", n)
		}
	}
}

func TestExportedNameAliases(t *testing.T) {
	name := "vm-x"
	if NetnsName(name) != netnsName(name) {
		t.Error("NetnsName != netnsName")
	}
	if VethHostName(name) != vethHostName(name) {
		t.Error("VethHostName != vethHostName")
	}
	if VethGuestName(name) != vethGuestName(name) {
		t.Error("VethGuestName != vethGuestName")
	}
	if !VethHostIP(name).Equal(vethHostIP(name)) {
		t.Error("VethHostIP != vethHostIP")
	}
	if !VethGuestIP(name).Equal(vethGuestIP(name)) {
		t.Error("VethGuestIP != vethGuestIP")
	}
	if SSHPort(name) != sshPort(name) {
		t.Error("SSHPort != sshPort")
	}
	if TAPDeviceName(name) != tapDevName(name) {
		t.Error("TAPDeviceName != tapDevName")
	}
	if !GuestIP(name).Equal(deriveNetwork(name).GuestIP) {
		t.Error("GuestIP != deriveNetwork().GuestIP")
	}
	if GuestMAC(name) != guestMAC(name) {
		t.Error("GuestMAC != guestMAC")
	}
}

func TestIPBase30(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"172.20.0.1", "172.20.0.0"},
		{"172.20.0.2", "172.20.0.0"},
		{"172.20.0.5", "172.20.0.4"},
		{"172.20.5.10", "172.20.5.8"},
		{"10.0.0.255", "10.0.0.252"},
	}
	for _, c := range cases {
		got := ipBase30(net.ParseIP(c.in))
		if got.String() != c.want {
			t.Errorf("ipBase30(%s) = %s, want %s", c.in, got, c.want)
		}
	}
}

func TestEscapeContainerCgroupRequiresSlice(t *testing.T) {
	if err := EscapeContainerCgroup(1, ""); err == nil {
		t.Fatal("expected error for empty slice")
	}
}
