package knaller

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

// tunSetPersist is the ioctl number for TUNSETPERSIST. When set to 1, the TAP
// device survives after the creating process closes its file descriptor. This
// lets us create and configure the TAP, then close our fd, and Firecracker
// opens the TAP independently by name. Without this, the TAP would disappear
// the moment we close our fd.
const tunSetPersist = 0x400454cb

// networkConfig holds the networking details for a single VM: the TAP device
// name on the host, the host and guest IP addresses, and the guest MAC address.
// All of these are derived deterministically from the VM name.
type networkConfig struct {
	TAPDevice string
	HostIP    net.IP
	GuestIP   net.IP
	GuestMAC  string
}

// createNetwork sets up networking for a VM. It creates a persistent TAP device
// and assigns it a /30 subnet (two usable IPs: one for the host side, one for
// the guest). The TAP device name, IP addresses, and MAC address are all derived
// from the VM name so they're deterministic and won't collide between VMs.
//
// The flow is:
//  1. Open /dev/net/tun and create a TAP device (layer 2, no packet info header)
//  2. Mark the TAP persistent so it survives after we close the fd
//  3. Close our fd (Firecracker will open the TAP by name later)
//  4. Assign the host-side IP address and netmask to the TAP
//  5. Bring the TAP interface up
func createNetwork(name string) (*networkConfig, error) {
	tapName := tapDevName(name)
	subnet := subnetIndex(name)

	// Each VM gets a /30 subnet from the 172.16.0.0/12 range.
	// The host gets .1 and the guest gets .2 within each /30 block.
	hostIP := net.IPv4(172, 16, byte(subnet>>8), byte(subnet<<2|1))
	guestIP := net.IPv4(172, 16, byte(subnet>>8), byte(subnet<<2|2))
	mask := net.IPv4Mask(255, 255, 255, 252)
	mac := guestMAC(name)

	fd, err := createTAP(tapName)
	if err != nil {
		return nil, fmt.Errorf("create TAP: %w", err)
	}

	// Make TAP persistent so Firecracker can open it by name after we close
	// our file descriptor. Without this, the device would disappear immediately.
	if err := ioctl(fd, tunSetPersist, 1); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("TUNSETPERSIST: %w", err)
	}

	// Close our fd — the TAP lives on because of TUNSETPERSIST.
	unix.Close(fd)

	// Configure the host side of the TAP with an IP address and netmask.
	if err := setIfaceAddr(tapName, hostIP, mask); err != nil {
		destroyTAP(tapName)
		return nil, fmt.Errorf("set TAP address: %w", err)
	}

	// Bring the interface up so packets can flow.
	if err := setIfaceUp(tapName); err != nil {
		destroyTAP(tapName)
		return nil, fmt.Errorf("set TAP up: %w", err)
	}

	return &networkConfig{
		TAPDevice: tapName,
		HostIP:    hostIP,
		GuestIP:   guestIP,
		GuestMAC:  mac,
	}, nil
}

// removeNetwork destroys the persistent TAP device for a VM. This is called
// during Cleanup() to ensure no network interfaces are left behind.
// Safe to call with a nil networkConfig.
func removeNetwork(nc *networkConfig) error {
	if nc == nil {
		return nil
	}
	return destroyTAP(nc.TAPDevice)
}

// destroyTAP removes a persistent TAP device. It re-opens /dev/net/tun,
// attaches to the named TAP, clears the persistence flag, and closes the fd.
// When the last fd to a non-persistent TAP is closed, the kernel automatically
// removes the network interface.
func destroyTAP(name string) error {
	fd, err := unix.Open("/dev/net/tun", os.O_RDWR, 0)
	if err != nil {
		return err
	}

	var ifr [unix.IFNAMSIZ + 64]byte
	copy(ifr[:unix.IFNAMSIZ], name)
	binary.LittleEndian.PutUint16(ifr[unix.IFNAMSIZ:], unix.IFF_TAP|unix.IFF_NO_PI)

	if err := ioctl(fd, unix.TUNSETIFF, uintptr(unsafe.Pointer(&ifr[0]))); err != nil {
		unix.Close(fd)
		return err
	}

	// Clear persistence — the device will be removed when we close the fd.
	ioctl(fd, tunSetPersist, 0)
	return unix.Close(fd)
}

// bootArgsIP returns the kernel ip= boot argument that configures the guest's
// network interface at boot time. The format is defined by the Linux kernel:
// ip=CLIENT_IP::GATEWAY_IP:NETMASK::DEVICE:AUTOCONF
// The gateway is the host IP (our TAP side), and autoconf is "off" since
// we configure everything statically.
func (nc *networkConfig) bootArgsIP() string {
	return fmt.Sprintf("ip=%s::%s:255.255.255.252::eth0:off",
		nc.GuestIP, nc.HostIP)
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

// createTAP opens /dev/net/tun and creates a new TAP device with the given name.
// TAP operates at layer 2 (Ethernet frames), which is what Firecracker expects.
// IFF_NO_PI disables the extra packet information header that TUN/TAP can prepend.
func createTAP(name string) (int, error) {
	fd, err := unix.Open("/dev/net/tun", os.O_RDWR, 0)
	if err != nil {
		return -1, fmt.Errorf("open /dev/net/tun: %w", err)
	}

	// Fill the ifreq struct: name in the first IFNAMSIZ bytes, flags after.
	var ifr [unix.IFNAMSIZ + 64]byte
	copy(ifr[:unix.IFNAMSIZ], name)
	binary.LittleEndian.PutUint16(ifr[unix.IFNAMSIZ:], unix.IFF_TAP|unix.IFF_NO_PI)

	if err := ioctl(fd, unix.TUNSETIFF, uintptr(unsafe.Pointer(&ifr[0]))); err != nil {
		unix.Close(fd)
		return -1, fmt.Errorf("TUNSETIFF: %w", err)
	}

	return fd, nil
}

// setIfaceAddr assigns an IP address and netmask to a network interface using
// the SIOCSIFADDR and SIOCSIFNETMASK ioctls. This is the equivalent of
// "ip addr add <ip>/<mask> dev <name>" but done directly via syscall.
func setIfaceAddr(name string, ip net.IP, mask net.IPMask) error {
	sock, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM, 0)
	if err != nil {
		return err
	}
	defer unix.Close(sock)

	var addrReq ifreqAddr
	copy(addrReq.Name[:], name)
	addrReq.Addr.Family = unix.AF_INET
	copy(addrReq.Addr.Addr[:], ip.To4())
	if err := ioctl(sock, unix.SIOCSIFADDR, uintptr(unsafe.Pointer(&addrReq))); err != nil {
		return fmt.Errorf("SIOCSIFADDR: %w", err)
	}

	var maskReq ifreqAddr
	copy(maskReq.Name[:], name)
	maskReq.Addr.Family = unix.AF_INET
	copy(maskReq.Addr.Addr[:], mask)
	if err := ioctl(sock, unix.SIOCSIFNETMASK, uintptr(unsafe.Pointer(&maskReq))); err != nil {
		return fmt.Errorf("SIOCSIFNETMASK: %w", err)
	}

	return nil
}

// setIfaceUp brings a network interface up. Equivalent to "ip link set <name> up".
func setIfaceUp(name string) error {
	return setIfaceFlags(name, unix.IFF_UP|unix.IFF_RUNNING)
}

// setIfaceFlags sets the flags on a network interface using SIOCSIFFLAGS ioctl.
func setIfaceFlags(name string, flags uint16) error {
	sock, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM, 0)
	if err != nil {
		return err
	}
	defer unix.Close(sock)

	var req ifreqFlags
	copy(req.Name[:], name)
	req.Flags = flags
	return ioctl(sock, unix.SIOCSIFFLAGS, uintptr(unsafe.Pointer(&req)))
}

// ifreqAddr matches the C struct ifreq with a sockaddr_in for address-related
// ioctls (SIOCSIFADDR, SIOCSIFNETMASK). The kernel expects this exact layout.
type ifreqAddr struct {
	Name [unix.IFNAMSIZ]byte
	Addr unix.RawSockaddrInet4
	_    [8]byte // padding to match the 40-byte ifreq union
}

// ifreqFlags matches the C struct ifreq for flag-related ioctls (SIOCSIFFLAGS).
type ifreqFlags struct {
	Name  [unix.IFNAMSIZ]byte
	Flags uint16
	_     [22]byte // padding to match the 40-byte ifreq union
}

// ioctl is a thin wrapper around the SYS_IOCTL syscall.
func ioctl(fd int, req uint, arg uintptr) error {
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), uintptr(req), arg)
	if errno != 0 {
		return errno
	}
	return nil
}
