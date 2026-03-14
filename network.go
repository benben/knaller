package knaller

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"net"
	"strings"
)

// tapMAC is the fixed MAC address used for all TAP devices. Each VM runs in
// its own network namespace so there's no conflict. Using a fixed MAC is
// critical for snapshot restore: the guest's ARP cache remembers the TAP's
// MAC from snapshot time, so it must be the same after restore to avoid a
// 15-45 second delay while the guest's stale ARP entry expires.
const tapMAC = "AA:FC:01:00:00:00"

// networkConfig holds the networking details for a single VM. The TAP device
// name, IPs, MAC, and SSH port are all derived deterministically from the VM
// name.
type networkConfig struct {
	TAPDevice string
	HostIP    net.IP
	GuestIP   net.IP
	GuestMAC  string
	SSHPort   int
}

// deriveNetwork computes all networking parameters for a VM from its name.
// This is pure computation — no syscalls or side effects. The actual network
// setup happens inside the pasta namespace via namespaceSetupScript().
func deriveNetwork(name string) *networkConfig {
	tapName := tapDevName(name)
	subnet := subnetIndex(name)
	hostIP := net.IPv4(172, 16, byte(subnet>>8), byte(subnet<<2|1))
	guestIP := net.IPv4(172, 16, byte(subnet>>8), byte(subnet<<2|2))
	return &networkConfig{
		TAPDevice: tapName,
		HostIP:    hostIP,
		GuestIP:   guestIP,
		GuestMAC:  guestMAC(name),
		SSHPort:   sshPort(name),
	}
}

// namespaceSetupScript generates the shell commands that run inside the pasta
// network namespace. It creates a TAP device for Firecracker (separate from
// pasta's own tap0), configures IP forwarding, NAT for outbound guest traffic,
// and DNAT so inbound SSH (forwarded by pasta) reaches the guest. Then it
// exec's Firecracker.
//
// Traffic flow:
//
//	Outbound: guest → Firecracker → kn-<name> → namespace kernel → IP forward
//	          → masquerade → tap0 → pasta L4 translation → host internet
//	Inbound:  host:<ssh_port> → pasta → tap0 → DNAT to guest IP →
//	          namespace kernel → kn-<name> → Firecracker → guest sshd
func namespaceSetupScript(nc *networkConfig, ports []PortMapping, fcBin, socketPath string) string {
	subnetBase := net.IPv4(172, 16, nc.HostIP[len(nc.HostIP)-2], nc.HostIP[len(nc.HostIP)-1]&0xFC)
	var b strings.Builder
	fmt.Fprintf(&b, "ip tuntap add dev %s mode tap", nc.TAPDevice)
	fmt.Fprintf(&b, " && ip addr add %s/30 dev %s", nc.HostIP, nc.TAPDevice)
	fmt.Fprintf(&b, " && ip link set dev %s address %s", nc.TAPDevice, tapMAC)
	fmt.Fprintf(&b, " && ip link set %s up", nc.TAPDevice)
	fmt.Fprintf(&b, " && sysctl -qw net.ipv4.ip_forward=1")
	fmt.Fprintf(&b, " && nft 'add table ip nat; add chain ip nat prerouting { type nat hook prerouting priority -100 ; }; add chain ip nat output { type nat hook output priority -100 ; }; add chain ip nat postrouting { type nat hook postrouting priority 100 ; }; add rule ip nat prerouting tcp dport 22 dnat to %s; add rule ip nat output tcp dport 22 dnat to %s",
		nc.GuestIP, nc.GuestIP)
	for _, p := range ports {
		fmt.Fprintf(&b, "; add rule ip nat prerouting tcp dport %d dnat to %s", p.Guest, nc.GuestIP)
		fmt.Fprintf(&b, "; add rule ip nat output tcp dport %d dnat to %s", p.Guest, nc.GuestIP)
	}
	fmt.Fprintf(&b, "; add rule ip nat postrouting ip saddr %s/30 oifname != \"%s\" masquerade'",
		subnetBase, nc.TAPDevice)
	fmt.Fprintf(&b, " && exec %s --api-sock %s --enable-pci", fcBin, socketPath)
	return b.String()
}

// bootArgsIP returns the kernel ip= boot argument that configures the guest's
// network interface at boot time. The format is defined by the Linux kernel:
// ip=CLIENT_IP::GATEWAY_IP:NETMASK::DEVICE:AUTOCONF:DNS0:DNS1
// The gateway is the host IP (our TAP side), and autoconf is "off" since
// we configure everything statically. DNS servers are appended so the kernel
// writes them to /proc/net/pnp, which the guest's /etc/resolv.conf symlinks to.
func (nc *networkConfig) bootArgsIP(dns []string) string {
	base := fmt.Sprintf("ip=%s::%s:255.255.255.252::eth0:off",
		nc.GuestIP, nc.HostIP)
	if len(dns) >= 2 {
		return base + ":" + dns[0] + ":" + dns[1]
	}
	if len(dns) == 1 {
		return base + ":" + dns[0]
	}
	return base
}

// tapDevName generates the host-side TAP device name from the VM name.
// Prefixed with "kn-" and truncated to fit the Linux interface name limit
// (max 15 chars total for IFNAMSIZ, "kn-" + 8 chars = 11 fits comfortably).
func tapDevName(name string) string {
	n := name
	if len(n) > 8 {
		n = n[:8]
	}
	return "kn-" + n
}

// sshPort derives a host-side SSH port from the VM name. Each VM gets a
// unique port in the range 10000-59999 for pasta to forward to the guest's
// SSH server. The port is deterministic — the same VM name always maps to the
// same port.
func sshPort(name string) int {
	h := sha256.Sum256([]byte(name))
	return int(binary.BigEndian.Uint16(h[2:4]))%50000 + 10000
}

// subnetIndex derives a /30 subnet index from the VM name. We hash the name
// with SHA-256 and take the first 14 bits, giving us 16384 possible subnets
// in the 172.16.0.0/12 range. This is deterministic — the same VM name always
// gets the same subnet.
func subnetIndex(name string) uint16 {
	h := sha256.Sum256([]byte(name))
	return binary.BigEndian.Uint16(h[:2]) & 0x3FFF
}

// guestMAC generates a MAC address for the guest NIC from the VM name.
// The prefix AA:FC:00 is a locally-administered MAC prefix (the "AA" has the
// local bit set). The last 3 bytes come from the SHA-256 hash of the name.
func guestMAC(name string) string {
	h := sha256.Sum256([]byte(name))
	return fmt.Sprintf("AA:FC:00:%02X:%02X:%02X", h[0], h[1], h[2])
}
