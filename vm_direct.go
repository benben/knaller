// Direct mode: skip pasta and run firecracker inside a per-VM kernel network
// namespace (netns). Each VM gets its own netns containing the TAP device and
// all per-VM NAT rules; the host netns carries only a veth peer per VM plus a
// shared "knaller_host" nft table that DNATs inbound SSH (and other forwarded
// ports) to the per-VM veth-guest IP.
//
// This was added for environments where the KVM_CREATE_VM ioctl is blocked in
// user namespaces (notably Kubernetes on bare-metal Linux when the cluster's
// pod sandbox is itself a user-namespaced container — pasta wraps the VM in a
// user+network namespace, which breaks KVM). The kernel netns we create here
// does not interfere with KVM.
//
// Direct mode requires the caller to hold CAP_NET_ADMIN + CAP_NET_RAW in the
// host network namespace. In Kubernetes that means hostNetwork: true plus the
// right capability set; on a bare host you must run as root or grant the
// capabilities to the binary.
//
// Tooling required on the host PATH: ip, nft, nsenter (all from iproute2 +
// nftables + util-linux). e2fsprogs is required if you also use RootFSSize.
package knaller

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/benben/knaller/firecracker"
)

// RunDirect is a drop-in replacement for Run() that does not use pasta. The
// firecracker process is spawned via `nsenter --net=...` so it lives entirely
// inside the per-VM kernel netns we set up.
//
// All other Config semantics are preserved. Two extra Config fields are
// useful here:
//
//   - RawDiskPath: hand a pre-attached block device to firecracker as the
//     rootfs drive instead of copying RootFS.
//   - Netns: pin the netns name (defaults to a name derived from cfg.Name).
//   - EscapeCgroupSlice: move the firecracker process out of the parent's
//     cgroup so it survives container restarts.
func RunDirect(ctx context.Context, cfg *Config) (*VM, error) {
	cfg.setDefaults()
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}

	var snap *Snapshot
	if cfg.SnapshotID != "" {
		var err error
		snap, err = GetSnapshot(cfg.SnapshotID)
		if err != nil {
			return nil, err
		}
		cfg.Ports = mergeUniquePorts(snap.Ports, cfg.Ports)
	}

	socketDir := socketDirectory()
	if err := os.MkdirAll(socketDir, 0o755); err != nil {
		return nil, fmt.Errorf("create socket dir: %w", err)
	}
	socketPath := filepath.Join(socketDir, cfg.Name+".socket")
	os.Remove(socketPath)

	var diskPath string
	var err error
	switch {
	case cfg.RawDiskPath != "":
		// Caller manages the disk lifecycle (e.g. attached an NBD device
		// at this path). We don't touch its contents.
		diskPath = cfg.RawDiskPath
	case snap != nil:
		diskPath, err = prepareDiskFromSnapshot(cfg.Name, cfg.SnapshotID)
	default:
		diskPath, err = prepareDisk(cfg.Name, cfg.RootFS, cfg.RootFSSize)
	}
	if err != nil {
		return nil, fmt.Errorf("prepare disk: %w", err)
	}

	nc := deriveNetwork(cfg.Name)
	if snap != nil {
		orig := deriveNetwork(snap.VMName)
		nc.TAPDevice = orig.TAPDevice
		nc.HostIP = orig.HostIP
		nc.GuestIP = orig.GuestIP
		nc.GuestMAC = orig.GuestMAC
	}

	// Per-VM netns names + veth peer + supernet IPs. We use the original VM
	// name even on snapshot restore so the netns identity matches what the
	// caller's external state store recorded for this snapshot.
	netns := netnsName(cfg.Name)
	if cfg.Netns != "" {
		netns = cfg.Netns
	}
	vethHost := vethHostName(cfg.Name)
	vethGuest := vethGuestName(cfg.Name)
	vhIP := vethHostIP(cfg.Name)
	vgIP := vethGuestIP(cfg.Name)

	if err := setupBoxNetns(nc, vhIP, vgIP, vethHost, vethGuest, netns, cfg.Ports); err != nil {
		if cfg.RawDiskPath == "" {
			removeDisk(cfg.Name)
		}
		return nil, fmt.Errorf("setup netns: %w", err)
	}

	cleanup := func() {
		teardownBoxNetns(netns, vethHost)
		os.Remove(socketPath)
		if cfg.RawDiskPath == "" {
			removeDisk(cfg.Name)
		}
	}

	// Spawn firecracker via nsenter so it joins the per-VM netns. nsenter
	// execve's into firecracker, so /proc/<pid>/cmdline shows firecracker
	// rather than nsenter — discovery/adoption can match on /firecracker.
	//
	// We deliberately avoid --enable-pci because the Firecracker reference
	// CI kernel (vmlinux-6.1.x) is built without CONFIG_PCI, which causes
	// the guest to panic at mount_block_root if PCI is enabled.
	//
	// CRITICAL: use context.Background() — NOT ctx — so the firecracker
	// process outlives the request that triggered RunDirect. Tying the
	// child to the request ctx kills it the instant the handler returns.
	cmd := exec.Command("nsenter", "--net=/var/run/netns/"+netns, "--",
		cfg.FirecrackerBin, "--api-sock", socketPath)
	_ = ctx // keep ctx for the API client calls below; do NOT pass to Cmd
	logPath := filepath.Join(vmDataDir(cfg.Name), "firecracker.log")
	logFile, _ := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if cfg.Detach {
		if logFile != nil {
			cmd.Stdout = logFile
			cmd.Stderr = logFile
		} else {
			devNull, _ := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
			cmd.Stdout = devNull
			cmd.Stderr = devNull
		}
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	} else {
		cmd.Stdout = writerOf(cfg.Stdout)
		cmd.Stderr = writerOf(cfg.Stderr)
	}
	if err := cmd.Start(); err != nil {
		cleanup()
		return nil, fmt.Errorf("start firecracker: %w", err)
	}
	// Optional: move firecracker out of the parent's cgroup. Best-effort;
	// failure is non-fatal — worst case the VM shares the parent's lifetime.
	if cfg.EscapeCgroupSlice != "" {
		_ = EscapeContainerCgroup(cmd.Process.Pid, cfg.EscapeCgroupSlice)
	}
	if err := waitForSocket(socketPath, 5*time.Second); err != nil {
		cmd.Process.Kill()
		cleanup()
		return nil, fmt.Errorf("waiting for firecracker socket: %w", err)
	}

	client := firecracker.NewClient(socketPath)

	if snap != nil {
		snapDir := snapshotDir(cfg.SnapshotID)
		if err := client.LoadSnapshot(ctx,
			filepath.Join(snapDir, "state"),
			filepath.Join(snapDir, "memory"),
		); err != nil {
			cmd.Process.Kill()
			cleanup()
			return nil, fmt.Errorf("load snapshot: %w", err)
		}
		if err := client.PatchDrive(ctx, "rootfs", diskPath); err != nil {
			cmd.Process.Kill()
			cleanup()
			return nil, fmt.Errorf("patch drive: %w", err)
		}
		if err := client.ResumeVM(ctx); err != nil {
			cmd.Process.Kill()
			cleanup()
			return nil, fmt.Errorf("resume vm: %w", err)
		}
		memory := snap.MemSizeMib
		if memory == 0 {
			memory = cfg.Memory
		}
		saveVMPorts(cfg.Name, cfg.Ports)
		return &VM{
			Name:       cfg.Name,
			PID:        cmd.Process.Pid,
			SocketPath: socketPath,
			StartedAt:  time.Now(),
			Status:     "Running",
			CPUs:       float64(snap.VCPUs),
			Memory:     memory,
			Port:       nc.SSHPort,
			cmd:        cmd,
			client:     client,
			diskPath:   diskPath,
		}, nil
	}

	dns := hostNameservers()
	bootArgs := "console=ttyS0 reboot=k panic=1 net.ifnames=0 " + nc.bootArgsIP(dns)
	if err := client.SetBootSource(ctx, &firecracker.BootSource{
		KernelImagePath: cfg.Kernel,
		BootArgs:        bootArgs,
	}); err != nil {
		cmd.Process.Kill()
		cleanup()
		return nil, fmt.Errorf("set boot source: %w", err)
	}

	drive := &firecracker.Drive{
		DriveID:      "rootfs",
		PathOnHost:   diskPath,
		IsRootDevice: true,
		IsReadOnly:   false,
	}
	if cfg.DiskMBps > 0 || cfg.DiskIOPS > 0 {
		drive.RateLimiter = &firecracker.RateLimiter{}
		if cfg.DiskMBps > 0 {
			drive.RateLimiter.Bandwidth = &firecracker.TokenBucket{
				Size: int64(cfg.DiskMBps) * 1_000_000, RefillTimeMs: 1000,
			}
		}
		if cfg.DiskIOPS > 0 {
			drive.RateLimiter.Ops = &firecracker.TokenBucket{
				Size: int64(cfg.DiskIOPS), RefillTimeMs: 1000,
			}
		}
	}
	if err := client.SetDrive(ctx, drive); err != nil {
		cmd.Process.Kill()
		cleanup()
		return nil, fmt.Errorf("set drive: %w", err)
	}

	nic := &firecracker.NetworkInterface{
		IfaceID:     "eth0",
		HostDevName: nc.TAPDevice,
		GuestMAC:    nc.GuestMAC,
	}
	if cfg.NetworkMbps > 0 {
		bps := int64(cfg.NetworkMbps * 1_000_000 / 8)
		limiter := &firecracker.RateLimiter{
			Bandwidth: &firecracker.TokenBucket{Size: bps, RefillTimeMs: 1000},
		}
		nic.RxRateLimiter = limiter
		nic.TxRateLimiter = limiter
	}
	if err := client.SetNetworkInterface(ctx, nic); err != nil {
		cmd.Process.Kill()
		cleanup()
		return nil, fmt.Errorf("set network: %w", err)
	}

	vcpus := int(math.Ceil(cfg.CPUs))
	if vcpus < 1 {
		vcpus = 1
	}
	if err := client.SetMachineConfig(ctx, &firecracker.MachineConfig{
		VcpuCount:  vcpus,
		MemSizeMib: cfg.Memory,
		Smt:        false,
	}); err != nil {
		cmd.Process.Kill()
		cleanup()
		return nil, fmt.Errorf("set machine config: %w", err)
	}

	if err := client.StartInstance(ctx); err != nil {
		cmd.Process.Kill()
		cleanup()
		return nil, fmt.Errorf("start instance: %w", err)
	}

	saveVMPorts(cfg.Name, cfg.Ports)
	return &VM{
		Name:       cfg.Name,
		PID:        cmd.Process.Pid,
		SocketPath: socketPath,
		StartedAt:  time.Now(),
		Status:     "Running",
		CPUs:       cfg.CPUs,
		Memory:     cfg.Memory,
		Port:       nc.SSHPort,
		cmd:        cmd,
		client:     client,
		diskPath:   diskPath,
	}, nil
}

// netnsName returns the kernel netns name for a VM. Linux IFNAMSIZ caps at
// 15 chars (and netns names share the limit in practice); we use "kn-" + 8
// hex chars of the SHA-256 of the name. The "kn-" prefix matches the TAP
// device convention and namespaces all knaller-managed netns under a
// recognisable prefix.
func netnsName(name string) string { return "kn-" + nameHash8(name) }

// vethHostName / vethGuestName return the veth peer interface names. "vh-"
// for the host-side end, "vg-" for the netns-side end. Both fit IFNAMSIZ.
func vethHostName(name string) string  { return "vh-" + nameHash8(name) }
func vethGuestName(name string) string { return "vg-" + nameHash8(name) }

// NetnsName / VethHostName / VethGuestName are exported aliases so external
// supervisors can derive the same identities for state persistence and
// adoption without duplicating the hashing logic.
func NetnsName(name string) string     { return netnsName(name) }
func VethHostName(name string) string  { return vethHostName(name) }
func VethGuestName(name string) string { return vethGuestName(name) }

// TeardownBoxNetns is the exported teardown helper. Use this to clean up
// after an external Destroy or as the failure-path companion to a manual
// setupBoxNetns invocation.
func TeardownBoxNetns(netns, vethHost string) error {
	return teardownBoxNetns(netns, vethHost)
}

// SSHPort exposes the deterministic per-name SSH port so external
// supervisors can rebuild network state without re-deriving the hash.
func SSHPort(name string) int { return sshPort(name) }

// VethHostIP / VethGuestIP / TAPDeviceName / GuestIP / GuestMAC are exported
// accessors for the deterministic per-name network identities, used by
// adopting supervisors to write state records that match what RunDirect set up.
func VethHostIP(name string) net.IP    { return vethHostIP(name) }
func VethGuestIP(name string) net.IP   { return vethGuestIP(name) }
func TAPDeviceName(name string) string { return tapDevName(name) }
func GuestIP(name string) net.IP       { return deriveNetwork(name).GuestIP }
func GuestMAC(name string) string      { return guestMAC(name) }

// nameHash8 returns the first 8 hex chars of SHA-256(name). Stable across
// runs, no spaces or special chars, fits in IFNAMSIZ alongside a short prefix.
func nameHash8(name string) string {
	h := sha256.Sum256([]byte(name))
	return fmt.Sprintf("%02x%02x%02x%02x", h[0], h[1], h[2], h[3])
}

// vethHostIP returns the host-side veth IP for a VM. Allocated out of
// 172.20.0.0/16 so the IPs always land inside RFC1918 and never collide
// with the guest /30s in 172.16.0.0/14.
func vethHostIP(name string) net.IP {
	idx := vethSubnetIndex(name)
	return net.IPv4(172, 20, byte(idx>>6), byte((idx&0x3F)<<2|1))
}

// vethGuestIP is the netns-side end of the same /30.
func vethGuestIP(name string) net.IP {
	idx := vethSubnetIndex(name)
	return net.IPv4(172, 20, byte(idx>>6), byte((idx&0x3F)<<2|2))
}

// vethSubnetIndex picks a /30 inside 172.20.0.0/16 from the VM name. 14
// bits = 16384 /30 slots; far more than any single host's VM-count cap.
func vethSubnetIndex(name string) uint32 {
	h := sha256.Sum256([]byte(name + "-veth"))
	return binary.BigEndian.Uint32(h[:4]) & 0x3FFF
}

// setupBoxNetns wires up the per-VM kernel netns:
//   - creates the netns + veth pair, plumbs IPs on both sides
//   - creates the TAP inside the netns and the in-netns nft NAT rules
//   - adds host-side knaller_host nft rules (DNAT inbound SSH + egress filter)
//   - adds the host route to the guest IP via the veth peer
//
// Idempotent on its structural pieces (table create) but the per-VM rules are
// only added on this call, not flushed-and-refilled — siblings stay alive.
// Forward/input chains, however, ARE flushed each call so the egress filter
// stays consistent (rule ordering matters; ct established/related must come
// first).
func setupBoxNetns(nc *networkConfig, vhIP, vgIP net.IP, vethHost, vethGuest, netns string, ports []PortMapping) error {
	// Idempotent teardown of any leftover from a previous VM with the same
	// name (snapshot restore reuses the original VM's netns name).
	_ = exec.Command("ip", "netns", "del", netns).Run()
	_ = exec.Command("ip", "link", "del", vethHost).Run()

	if err := run("ip", "netns", "add", netns); err != nil {
		return err
	}
	if err := run("ip", "link", "add", vethHost, "type", "veth", "peer", "name", vethGuest); err != nil {
		return err
	}
	if err := run("ip", "link", "set", vethGuest, "netns", netns); err != nil {
		return err
	}

	// Host side of the veth.
	if err := run("ip", "addr", "add", fmt.Sprintf("%s/30", vhIP), "dev", vethHost); err != nil {
		return err
	}
	if err := run("ip", "link", "set", vethHost, "up"); err != nil {
		return err
	}

	// Inside the netns: bring up lo + vethGuest, install default route via host.
	for _, args := range [][]string{
		{"ip", "-n", netns, "link", "set", "lo", "up"},
		{"ip", "-n", netns, "addr", "add", fmt.Sprintf("%s/30", vgIP), "dev", vethGuest},
		{"ip", "-n", netns, "link", "set", vethGuest, "up"},
		{"ip", "-n", netns, "route", "add", "default", "via", vhIP.String()},
		// TAP for firecracker.
		{"ip", "-n", netns, "tuntap", "add", "dev", nc.TAPDevice, "mode", "tap"},
		{"ip", "-n", netns, "link", "set", "dev", nc.TAPDevice, "address", tapMAC},
		{"ip", "-n", netns, "addr", "add", fmt.Sprintf("%s/30", nc.HostIP), "dev", nc.TAPDevice},
		{"ip", "-n", netns, "link", "set", nc.TAPDevice, "up"},
		// Pin the guest IP to the TAP — without an explicit /32 route the
		// kernel falls through to the default via the veth, which loops.
		{"ip", "-n", netns, "route", "add", fmt.Sprintf("%s/32", nc.GuestIP), "dev", nc.TAPDevice},
		// sysctls (ip_forward + route_localnet for the loopback DNAT path).
		{"ip", "netns", "exec", netns, "sysctl", "-qw", "net.ipv4.ip_forward=1"},
		{"ip", "netns", "exec", netns, "sysctl", "-qw", "net.ipv4.conf.all.route_localnet=1"},
		{"ip", "netns", "exec", netns, "sysctl", "-qw", "net.ipv4.conf.lo.route_localnet=1"},
	} {
		if err := run(args[0], args[1:]...); err != nil {
			return err
		}
	}

	// In-netns NAT. Two functions:
	//   - DNAT inbound (dport 22 → GuestIP:22) so traffic arriving at
	//     vethGuestIP gets handed off to the actual guest.
	//   - SNAT outbound for NEW connections from the guest, so the host
	//     sees src=vethGuestIP (per-VM, unique) instead of src=GuestIP
	//     (shared across snapshot restores). `ct state new` keeps replies
	//     to inbound-DNAT'd traffic out of this rule — those use conntrack
	//     reverse-NAT automatically.
	nsCmd := func(args ...string) error {
		return run("ip", append([]string{"netns", "exec", netns}, args...)...)
	}
	_ = nsCmd("nft", "add", "table", "ip", "knaller_box_nat")
	for _, ch := range []string{
		"add chain ip knaller_box_nat prerouting { type nat hook prerouting priority -100 ; }",
		"add chain ip knaller_box_nat output { type nat hook output priority -100 ; }",
		"add chain ip knaller_box_nat postrouting { type nat hook postrouting priority 100 ; }",
	} {
		args := append([]string{"nft"}, strings.Split(ch, " ")...)
		_ = nsCmd(args...)
	}
	natRules := []string{
		fmt.Sprintf("add rule ip knaller_box_nat prerouting tcp dport 22 dnat to %s:22", nc.GuestIP),
		fmt.Sprintf("add rule ip knaller_box_nat output    tcp dport 22 dnat to %s:22", nc.GuestIP),
	}
	for _, p := range ports {
		natRules = append(natRules,
			fmt.Sprintf("add rule ip knaller_box_nat prerouting tcp dport %d dnat to %s:%d", p.Guest, nc.GuestIP, p.Guest),
			fmt.Sprintf("add rule ip knaller_box_nat output    tcp dport %d dnat to %s:%d", p.Guest, nc.GuestIP, p.Guest),
		)
	}
	natRules = append(natRules, fmt.Sprintf(
		"add rule ip knaller_box_nat postrouting oifname \"%s\" ct state new masquerade",
		vethGuest))
	for _, r := range natRules {
		args := append([]string{"nft"}, strings.Split(r, " ")...)
		if err := nsCmd(args...); err != nil {
			return fmt.Errorf("netns nft %q: %w", r, err)
		}
	}

	// Host-side knaller_host table. Structural chains created idempotently;
	// the forward + input filter chains AND the global postrouting
	// masquerade rule are flushed-and-refilled on every call so ordering
	// stays correct. Per-VM DNAT rules in prerouting/output are *added*
	// (not flushed) so concurrent VMs keep their rules.
	_ = exec.Command("nft", "add", "table", "ip", "knaller_host").Run()
	for _, ch := range []string{
		"add chain ip knaller_host prerouting { type nat hook prerouting priority -100 ; }",
		"add chain ip knaller_host output { type nat hook output priority -100 ; }",
		"add chain ip knaller_host postrouting { type nat hook postrouting priority 100 ; }",
		"add chain ip knaller_host forward { type filter hook forward priority filter ; policy accept ; }",
		"add chain ip knaller_host input { type filter hook input priority filter ; policy accept ; }",
	} {
		_ = exec.Command("nft", strings.Split(ch, " ")...).Run()
	}
	for _, c := range []string{"forward", "input"} {
		_ = exec.Command("nft", "flush", "chain", "ip", "knaller_host", c).Run()
	}
	// route_localnet=1 lets WaitForSSH (dial localhost:port → output-chain
	// DNAT → off-loopback) survive the kernel's "drop packets from
	// 127.0.0.0/8 routed off-lo" guard. Idempotent sysctl.
	_ = exec.Command("sysctl", "-qw", "net.ipv4.ip_forward=1").Run()
	_ = exec.Command("sysctl", "-qw", "net.ipv4.conf.all.route_localnet=1").Run()
	_ = exec.Command("sysctl", "-qw", "net.ipv4.conf.lo.route_localnet=1").Run()
	// Egress filter operates on saddr=vethGuestIP (172.20/14) — the
	// in-netns masquerade rewrites guest src to vethGuestIP before the
	// packet leaves the netns, so by the time we see it on the host's
	// forward chain it's already in 172.20/14. Reject rules block
	// guest-to-host-network reachability (RFC1918 ranges + carrier-grade
	// NAT + multicast + the knaller veth supernet itself), so guests can
	// only reach the public internet, not their host's neighbours.
	hostRules := []string{
		"add rule ip knaller_host forward ct state established,related accept",
		"add rule ip knaller_host forward ip saddr 172.20.0.0/14 ip daddr 169.254.169.253 udp dport 53 accept",
		"add rule ip knaller_host forward ip saddr 172.20.0.0/14 ip daddr 169.254.169.253 tcp dport 53 accept",
		"add rule ip knaller_host forward ip saddr 172.20.0.0/14 ip daddr 169.254.0.0/16 reject",
		"add rule ip knaller_host forward ip saddr 172.20.0.0/14 ip daddr 10.0.0.0/8 reject",
		"add rule ip knaller_host forward ip saddr 172.20.0.0/14 ip daddr 192.168.0.0/16 reject",
		"add rule ip knaller_host forward ip saddr 172.20.0.0/14 ip daddr 100.64.0.0/10 reject",
		"add rule ip knaller_host forward ip saddr 172.20.0.0/14 ip daddr 224.0.0.0/4 reject",
		"add rule ip knaller_host forward ip saddr 172.20.0.0/14 ip daddr 172.20.0.0/14 reject",
		"add rule ip knaller_host input ct state established,related accept",
		"add rule ip knaller_host input ip saddr 172.20.0.0/14 reject",
	}
	for _, r := range hostRules {
		out, err := exec.Command("nft", strings.Split(r, " ")...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("host nft %q: %s: %w", r, out, err)
		}
	}

	// Per-VM DNAT: host:port → vethGuestIP:22 (per-VM unique, so the
	// host's auto-installed /30 route to vethGuest delivers the packet
	// into the right netns; no host /32 collision across VMs).
	perBox := []string{
		fmt.Sprintf("add rule ip knaller_host prerouting tcp dport %d dnat to %s:22", nc.SSHPort, vgIP),
		fmt.Sprintf("add rule ip knaller_host output    tcp dport %d dnat to %s:22", nc.SSHPort, vgIP),
	}
	for _, p := range ports {
		perBox = append(perBox,
			fmt.Sprintf("add rule ip knaller_host prerouting tcp dport %d dnat to %s:%d", p.Host, vgIP, p.Guest),
			fmt.Sprintf("add rule ip knaller_host output    tcp dport %d dnat to %s:%d", p.Host, vgIP, p.Guest),
		)
	}
	// Per-VM outbound masquerade so the upstream NIC sees the host's IP,
	// not the per-VM vethGuestIP, on the way out to the public internet.
	perBox = append(perBox, fmt.Sprintf(
		"add rule ip knaller_host postrouting ip saddr %s/30 oifname != \"%s\" masquerade",
		ipBase30(vhIP), vethHost))
	for _, r := range perBox {
		out, err := exec.Command("nft", strings.Split(r, " ")...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("per-vm nft %q: %s: %w", r, out, err)
		}
	}

	return nil
}

// teardownBoxNetns deletes the netns (which cascades and removes the TAP +
// vethGuest + in-netns nft rules) and the host-side veth peer. Per-VM host
// nft rules (DNAT, masquerade) are intentionally left behind — they
// reference a now-vanished veth peer, are harmless, and will be replaced
// on the next setup with the same name.
func teardownBoxNetns(netns, vethHost string) error {
	_ = exec.Command("ip", "netns", "del", netns).Run()
	_ = exec.Command("ip", "link", "del", vethHost).Run()
	return nil
}

// EscapeContainerCgroup moves pid into a host-level cgroupv2 slice (e.g.
// "knaller-vms.slice") so it survives a restart of the parent container's
// own cgroup. Idempotent — creates the slice if missing. The caller's
// container must be privileged with the host's cgroupv2 hierarchy mounted
// at /sys/fs/cgroup for this to work.
//
// Use this after spawning firecracker if you need VM lifetimes that outlive
// the supervisor's container restart cycle (e.g. Kubernetes DaemonSet
// rollouts). Pass the slice name your operator owns; do not collide with
// systemd-managed slices.
func EscapeContainerCgroup(pid int, slice string) error {
	if slice == "" {
		return fmt.Errorf("escape cgroup: slice name required")
	}
	dir := filepath.Join("/sys/fs/cgroup", slice)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	procs := filepath.Join(dir, "cgroup.procs")
	return os.WriteFile(procs, []byte(fmt.Sprintf("%d\n", pid)), 0o644)
}

// ipBase30 returns the network base address for a /30 containing ip.
func ipBase30(ip net.IP) net.IP {
	v := ip.To4()
	return net.IPv4(v[0], v[1], v[2], v[3]&0xFC)
}

func run(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %s: %w", name, strings.Join(args, " "), out, err)
	}
	return nil
}

// Discard is exported so callers can pin Stdout/Stderr to it without importing io.
var Discard io.Writer = io.Discard
