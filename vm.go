// Package knaller provides a high-level Go API for running Firecracker microVMs.
//
// Knaller starts Firecracker inside a pasta network namespace for each VM,
// connects to its API socket, configures the VM (kernel, rootfs, network,
// CPU/memory), and boots it. Each VM gets its own rootfs copy, network namespace
// with a TAP device, and DNS configuration — all without requiring root.
//
// The main entry points are Run (to start and boot a VM) and List (to discover
// running VMs). Call Cleanup() when done to release resources (rootfs copy, API
// socket). Network namespace cleanup is automatic when the pasta process exits.
package knaller

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"net"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/benben/knaller/firecracker"
)

// VM represents a running Firecracker microVM. It holds references to all the
// resources knaller created: the pasta+Firecracker process, rootfs copy, and
// API socket. Call Cleanup() when done to release resources. The network
// namespace is cleaned up automatically when the pasta process exits.
type VM struct {
	Name       string
	PID        int
	SocketPath string
	StartedAt  time.Time
	Status     string
	CPUs    float64
	Memory  int
	Port int // SSH port on localhost

	// Private fields for managing the VM's resources.
	cmd      *exec.Cmd
	client   *firecracker.Client
	diskPath string
}

// Run starts a Firecracker microVM inside a pasta network namespace. It:
//  1. Copies the base rootfs to a per-VM directory (for write isolation)
//  2. Derives network configuration (TAP name, IPs, MAC) from the VM name
//  3. Starts pasta which creates a network namespace, then runs a shell that
//     creates a TAP device, sets up IP forwarding/NAT, and exec's Firecracker
//  4. Configures and boots the VM via the Firecracker HTTP API
//
// DNS servers are passed to the guest via kernel boot args (ip= parameter).
// The guest rootfs should symlink /etc/resolv.conf → /proc/net/pnp for this
// to work (see Containerfile_guest).
//
// No root privileges are required — pasta provides network namespacing and
// the TAP device is created inside the namespace where we have CAP_NET_ADMIN.
//
// If any step fails, all previously created resources are cleaned up automatically.
// On success, the caller must eventually call Cleanup() to release resources.
func Run(ctx context.Context, cfg *Config) (*VM, error) {
	cfg.setDefaults()
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}

	// Load snapshot metadata if restoring from a snapshot.
	var snap *Snapshot
	if cfg.SnapshotID != "" {
		var err error
		snap, err = GetSnapshot(cfg.SnapshotID)
		if err != nil {
			return nil, err
		}
		// Merge snapshot ports with CLI-specified ports. Deduplicate so that
		// re-specifying the same port on the CLI doesn't produce duplicate
		// pasta -t args (which causes pasta to exit with a conflict error).
		cfg.Ports = mergeUniquePorts(snap.Ports, cfg.Ports)
	}

	// Each VM gets a socket at /tmp/knaller/<name>.socket (or $XDG_RUNTIME_DIR/knaller/).
	socketDir := socketDirectory()
	if err := os.MkdirAll(socketDir, 0o755); err != nil {
		return nil, fmt.Errorf("create socket dir: %w", err)
	}
	socketPath := filepath.Join(socketDir, cfg.Name+".socket")

	// Remove any stale socket from a previous run with the same name.
	os.Remove(socketPath)

	// Prepare the rootfs. For snapshot restore, copy the snapshot's rootfs.
	// For fresh VMs, copy the base rootfs image.
	var diskPath string
	var err error
	if snap != nil {
		diskPath, err = prepareDiskFromSnapshot(cfg.Name, cfg.SnapshotID)
	} else {
		diskPath, err = prepareDisk(cfg.Name, cfg.RootFS)
	}
	if err != nil {
		return nil, fmt.Errorf("prepare disk: %w", err)
	}

	// Derive network configuration. For snapshot restore, the TAP device name
	// and IPs must match the original VM (they're baked into the snapshot state),
	// but the SSH port comes from the new VM name for uniqueness.
	nc := deriveNetwork(cfg.Name)
	if snap != nil {
		orig := deriveNetwork(snap.VMName)
		nc.TAPDevice = orig.TAPDevice
		nc.HostIP = orig.HostIP
		nc.GuestIP = orig.GuestIP
		nc.GuestMAC = orig.GuestMAC
	}

	// cleanup tears down all resources created so far if we hit an error.
	var cmd *exec.Cmd
	cleanup := func() {
		if cmd != nil && cmd.Process != nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
		os.Remove(socketPath)
		removeDisk(cfg.Name)
	}

	// Start pasta which creates a user+network namespace with its own tap0
	// for outer connectivity. Inside the namespace, a setup script creates a
	// second TAP for Firecracker's guest NIC, configures IP forwarding, NAT,
	// and DNAT (so pasta's SSH port forwarding reaches the guest), then exec's
	// Firecracker. All without root — pasta provides CAP_NET_ADMIN in the namespace.
	script := namespaceSetupScript(nc, cfg.Ports, cfg.FirecrackerBin, socketPath)
	pastaArgs := []string{
		"--config-net",
		"-t", fmt.Sprintf("%d:22", nc.SSHPort),
	}
	for _, p := range cfg.Ports {
		pastaArgs = append(pastaArgs, "-t", fmt.Sprintf("%d:%d", p.Host, p.Guest))
	}
	pastaArgs = append(pastaArgs, "-4", "-f", "--", "sh", "-c", script)

	// Compute actual vCPU count (Firecracker requires integer vCPUs).
	// Fractional values like 0.5 mean "1 vCPU at 50% CPU quota" — the quota
	// is enforced via systemd-run's CPUQuota= cgroup setting.
	cpus := cfg.CPUs
	if snap != nil {
		cpus = float64(snap.VCPUs)
	}
	vcpus := int(math.Ceil(cpus))
	if vcpus < 1 {
		vcpus = 1
	}
	needsQuota := cpus != float64(vcpus)

	if needsQuota {
		// Wrap with systemd-run to enforce CPU quota via cgroup.
		// CPUQuota is a percentage: 0.5 CPUs = 50%, 1.5 CPUs = 150%.
		quota := int(math.Ceil(cpus * 100))
		args := []string{"--user", "--scope", "-q",
			"-p", fmt.Sprintf("CPUQuota=%d%%", quota),
			"--", cfg.PastaBin}
		args = append(args, pastaArgs...)
		cmd = exec.CommandContext(ctx, "systemd-run", args...)
	} else {
		cmd = exec.CommandContext(ctx, cfg.PastaBin, pastaArgs...)
	}
	if cfg.Detach {
		// Detach mode: redirect to /dev/null so the child's file descriptors
		// don't depend on the parent. Using writerOf() would create pipes that
		// break (SIGPIPE) when the CLI exits.
		devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
		if err != nil {
			cleanup()
			return nil, fmt.Errorf("open /dev/null: %w", err)
		}
		cmd.Stdout = devNull
		cmd.Stderr = devNull
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	} else {
		// Wrap writers so exec.Cmd creates pipes instead of passing file
		// descriptors directly. This ensures Wait() blocks until all child
		// process output has been consumed (not just until the process exits).
		cmd.Stdout = writerOf(cfg.Stdout)
		cmd.Stderr = writerOf(cfg.Stderr)
	}
	if err := cmd.Start(); err != nil {
		cleanup()
		return nil, fmt.Errorf("start pasta: %w", err)
	}

	// Wait for the API socket to appear. Firecracker creates it shortly after
	// starting. Poll briefly — if it doesn't appear, Firecracker likely crashed.
	if err := waitForSocket(socketPath, 5*time.Second); err != nil {
		cleanup()
		return nil, fmt.Errorf("waiting for firecracker socket: %w", err)
	}

	client := firecracker.NewClient(socketPath)

	if snap != nil {
		// Snapshot restore: load the snapshot (the state file references the
		// snapshot dir's rootfs which always exists), patch the drive to the
		// new VM's rootfs copy, then resume.
		snapDir := snapshotDir(cfg.SnapshotID)
		if err := client.LoadSnapshot(ctx,
			filepath.Join(snapDir, "state"),
			filepath.Join(snapDir, "memory"),
		); err != nil {
			cleanup()
			return nil, fmt.Errorf("load snapshot: %w", err)
		}
		if err := client.PatchDrive(ctx, "rootfs", diskPath); err != nil {
			cleanup()
			return nil, fmt.Errorf("patch drive: %w", err)
		}
		if err := client.ResumeVM(ctx); err != nil {
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
			Port:    nc.SSHPort,
			cmd:        cmd,
			client:     client,
			diskPath:   diskPath,
		}, nil
	}

	// Fresh VM: configure via the Firecracker API and boot.
	//
	// Kernel boot args configure the guest:
	//   reboot=k       — reboot on kernel panic instead of halting
	//   panic=1        — reboot after 1 second on panic
	//   ip=...         — static IP + DNS for the guest (parsed by the kernel at boot)
	dns := hostNameservers()
	bootArgs := "reboot=k panic=1 net.ifnames=0 " + nc.bootArgsIP(dns)
	if err := client.SetBootSource(ctx, &firecracker.BootSource{
		KernelImagePath: cfg.Kernel,
		BootArgs:        bootArgs,
	}); err != nil {
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
			// Convert MB/s to bytes per second.
			// The token bucket refills every 1000ms, so size = bytes per second.
			drive.RateLimiter.Bandwidth = &firecracker.TokenBucket{
				Size:         int64(cfg.DiskMBps) * 1_000_000,
				RefillTimeMs: 1000,
			}
		}
		if cfg.DiskIOPS > 0 {
			drive.RateLimiter.Ops = &firecracker.TokenBucket{
				Size:         int64(cfg.DiskIOPS),
				RefillTimeMs: 1000,
			}
		}
	}
	if err := client.SetDrive(ctx, drive); err != nil {
		cleanup()
		return nil, fmt.Errorf("set drive: %w", err)
	}

	nic := &firecracker.NetworkInterface{
		IfaceID:     "eth0",
		HostDevName: nc.TAPDevice,
		GuestMAC:    nc.GuestMAC,
	}
	if cfg.NetworkMbps > 0 {
		// Convert Mbps to bytes per second: Mbps * 1_000_000 / 8.
		// The token bucket refills every 1000ms, so size = bytes per second.
		bytesPerSecond := int64(cfg.NetworkMbps * 1_000_000 / 8)
		limiter := &firecracker.RateLimiter{
			Bandwidth: &firecracker.TokenBucket{
				Size:         bytesPerSecond,
				RefillTimeMs: 1000,
			},
		}
		nic.RxRateLimiter = limiter
		nic.TxRateLimiter = limiter
	}
	if err := client.SetNetworkInterface(ctx, nic); err != nil {
		cleanup()
		return nil, fmt.Errorf("set network: %w", err)
	}

	if err := client.SetMachineConfig(ctx, &firecracker.MachineConfig{
		VcpuCount:  vcpus,
		MemSizeMib: cfg.Memory,
		Smt:        false,
	}); err != nil {
		cleanup()
		return nil, fmt.Errorf("set machine config: %w", err)
	}

	// Boot the VM. After this, the guest kernel starts and the serial console
	// appears on Stdout.
	if err := client.StartInstance(ctx); err != nil {
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

// Wait blocks until the pasta process exits (which happens when Firecracker
// exits, since it was exec'd). Returns any error from the process exit.
func (vm *VM) Wait() error {
	if vm.cmd == nil {
		return nil
	}
	return vm.cmd.Wait()
}

// WaitForSSH polls the VM's SSH port until it accepts connections or the
// timeout expires. This is useful in detach mode to ensure the guest has
// fully booted before returning control to the user.
func (vm *VM) WaitForSSH(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	addr := fmt.Sprintf("localhost:%d", vm.Port)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err == nil {
			conn.Close()
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("SSH port %d not ready within %s", vm.Port, timeout)
}

// Stop asks the guest OS to shut down gracefully by sending Ctrl+Alt+Del via
// the Firecracker API. The guest handles this as a shutdown signal.
func (vm *VM) Stop(ctx context.Context) error {
	return vm.client.StopInstance(ctx)
}

// Cleanup releases all resources knaller created for this VM: removes the API
// socket and deletes the rootfs copy. The network namespace and TAP device are
// cleaned up automatically when the pasta process exits. Always call this after
// the VM exits to avoid leaking disk space. Safe to call multiple times.
func (vm *VM) Cleanup() error {
	var errs []error
	if err := os.Remove(vm.SocketPath); err != nil && !os.IsNotExist(err) {
		errs = append(errs, fmt.Errorf("remove socket: %w", err))
	}
	if err := removeDisk(vm.Name); err != nil {
		errs = append(errs, fmt.Errorf("remove disk: %w", err))
	}
	return errors.Join(errs...)
}

// RemoveVM deletes a stopped VM's data directory and any stale socket. Returns
// an error if the VM is still running.
func RemoveVM(name string) error {
	// Check if the VM is still running by trying to connect to its socket.
	socketPath := filepath.Join(socketDirectory(), name+".socket")
	client := firecracker.NewClient(socketPath)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	_, err := client.GetInfo(ctx)
	cancel()
	if err == nil {
		return fmt.Errorf("VM %q is still running, stop it first", name)
	}

	// Check the VM data dir exists.
	dir := vmDataDir(name)
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("VM %q not found", name)
	}

	// Clean up stale socket and VM data.
	os.Remove(socketPath)
	return removeDisk(name)
}

// StopVM stops a running VM by name. It connects to the VM's Firecracker API
// socket and sends Ctrl+Alt+Del to trigger a graceful guest shutdown. This is
// used by "knaller stop" to stop a VM from a different terminal.
func StopVM(ctx context.Context, name string) error {
	socketPath := filepath.Join(socketDirectory(), name+".socket")
	if _, err := os.Stat(socketPath); err != nil {
		return fmt.Errorf("VM %q not found (no socket at %s)", name, socketPath)
	}
	client := firecracker.NewClient(socketPath)
	return client.StopInstance(ctx)
}

// PauseVM pauses a running VM by name. The VM's vCPUs are frozen until
// ResumeVM is called.
func PauseVM(ctx context.Context, name string) error {
	socketPath := filepath.Join(socketDirectory(), name+".socket")
	if _, err := os.Stat(socketPath); err != nil {
		return fmt.Errorf("VM %q not found (no socket at %s)", name, socketPath)
	}
	client := firecracker.NewClient(socketPath)
	return client.PauseVM(ctx)
}

// ResumeVM resumes a paused VM by name.
func ResumeVM(ctx context.Context, name string) error {
	socketPath := filepath.Join(socketDirectory(), name+".socket")
	if _, err := os.Stat(socketPath); err != nil {
		return fmt.Errorf("VM %q not found (no socket at %s)", name, socketPath)
	}
	client := firecracker.NewClient(socketPath)
	return client.ResumeVM(ctx)
}

// List discovers all VMs — both running and stopped. Running/paused VMs are
// found by scanning the socket directory and querying the Firecracker API.
// Stopped VMs are found by scanning the per-VM data directory for entries that
// don't have a live socket.
func List() ([]*VM, error) {
	seen := map[string]*VM{}

	// First pass: find running/paused VMs via their API sockets.
	socketDir := socketDirectory()
	socketEntries, err := os.ReadDir(socketDir)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, e := range socketEntries {
		if !strings.HasSuffix(e.Name(), ".socket") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".socket")
		socketPath := filepath.Join(socketDir, e.Name())

		client := firecracker.NewClient(socketPath)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		info, err := client.GetInfo(ctx)
		if err != nil {
			cancel()
			continue
		}

		vm := &VM{
			Name:       name,
			SocketPath: socketPath,
			Status:     info.State,
			Port:       sshPort(name),
			client:     client,
		}

		vmCfg, err := client.GetVMConfig(ctx)
		if err == nil && vmCfg.MachineConfig != nil {
			vm.CPUs = float64(vmCfg.MachineConfig.VcpuCount)
			vm.Memory = vmCfg.MachineConfig.MemSizeMib
		}
		cancel()

		fi, err := e.Info()
		if err == nil {
			vm.StartedAt = fi.ModTime()
		}
		vm.PID = findFirecrackerPID(socketPath)

		seen[name] = vm
	}

	// Second pass: find stopped VMs from the data directory.
	home, _ := os.UserHomeDir()
	vmsDir := filepath.Join(home, ".local", "share", "knaller", "vms")
	vmEntries, err := os.ReadDir(vmsDir)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, e := range vmEntries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if seen[name] != nil {
			continue // already found as running
		}
		vm := &VM{
			Name:       name,
			SocketPath: filepath.Join(socketDir, name+".socket"),
			Status:     "Stopped",
			Port:       sshPort(name),
		}
		fi, err := e.Info()
		if err == nil {
			vm.StartedAt = fi.ModTime()
		}
		seen[name] = vm
	}

	vms := make([]*VM, 0, len(seen))
	for _, vm := range seen {
		vms = append(vms, vm)
	}
	return vms, nil
}

// waitForSocket polls until the given socket file appears, or the timeout
// expires. Firecracker creates the socket shortly after starting.
func waitForSocket(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("socket %s did not appear within %s", path, timeout)
}

// socketDirectory returns the path where VM API sockets are stored:
// ~/.local/share/knaller/sockets/. This keeps all knaller data in one place.
func socketDirectory() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "knaller", "sockets")
}

// parseGuestIP extracts the guest IP from kernel boot args. The kernel ip=
// argument format is: ip=GUEST_IP::HOST_IP:NETMASK::IFACE:off
// For example: ip=172.16.0.2::172.16.0.1:255.255.255.252::eth0:off
func parseGuestIP(bootArgs string) string {
	for _, arg := range strings.Fields(bootArgs) {
		if strings.HasPrefix(arg, "ip=") {
			parts := strings.SplitN(strings.TrimPrefix(arg, "ip="), "::", 2)
			if len(parts) >= 1 {
				return parts[0]
			}
		}
	}
	return ""
}

// findFirecrackerPID searches /proc for a Firecracker process that was started
// with the given socket path in its command line. This is a best-effort lookup
// used by List() to populate the PID field.
func findFirecrackerPID(socketPath string) int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// /proc directories with numeric names are process entries.
		pid := 0
		for _, c := range e.Name() {
			if c < '0' || c > '9' {
				pid = -1
				break
			}
			pid = pid*10 + int(c-'0')
		}
		if pid <= 0 {
			continue
		}
		// Read the process command line and check if it mentions our socket.
		cmdline, err := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
		if err != nil {
			continue
		}
		if strings.Contains(string(cmdline), socketPath) {
			return pid
		}
	}
	return 0
}

// pipeWriter wraps an io.Writer so that exec.Cmd sees a plain io.Writer
// instead of an *os.File. This forces Go to create a pipe and copy goroutine,
// making Wait() block until all output is consumed.
type pipeWriter struct{ w io.Writer }

func (pw pipeWriter) Write(p []byte) (int, error) { return pw.w.Write(p) }

func writerOf(w io.Writer) io.Writer { return pipeWriter{w} }

// mergeUniquePorts combines two port lists, deduplicating by Host port.
// Ports from a take precedence over ports from b when there's a conflict.
func mergeUniquePorts(a, b []PortMapping) []PortMapping {
	seen := map[int]bool{}
	var result []PortMapping
	for _, p := range a {
		if !seen[p.Host] {
			seen[p.Host] = true
			result = append(result, p)
		}
	}
	for _, p := range b {
		if !seen[p.Host] {
			seen[p.Host] = true
			result = append(result, p)
		}
	}
	return result
}
