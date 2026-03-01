// Package knaller provides a high-level Go API for running Firecracker microVMs.
//
// The main entry points are Run (to start a VM) and List (to discover running VMs).
// Each VM gets its own rootfs copy, TAP network device, and Firecracker process.
// DNS is auto-configured from the host's resolv.conf. Call Cleanup() when done
// to release all resources (TAP device, rootfs copy, API socket).
package knaller

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/benben/knaller/firecracker"
)

// VM represents a running Firecracker microVM. It holds references to all the
// resources created for this VM: the Firecracker process, TAP network device,
// rootfs copy, and API socket. Call Cleanup() when done to release everything.
type VM struct {
	Name       string
	PID        int
	SocketPath string
	StartedAt  time.Time
	Status     string
	CPUs       int
	Memory     int
	GuestIP    string

	// Private fields for managing the VM's lifecycle and resources.
	cmd      *exec.Cmd
	client   *firecracker.Client
	net      *networkConfig
	diskPath string
}

// Run creates and boots a new microVM. It:
//  1. Copies the base rootfs to a per-VM directory (for write isolation)
//  2. Writes DNS config into the rootfs copy from the host's resolv.conf
//  3. Creates a persistent TAP network device for the VM
//  4. Starts a Firecracker process and configures it via its HTTP API
//  5. Boots the VM
//
// If any step fails, all previously created resources are cleaned up automatically.
// On success, the caller must eventually call Cleanup() to release resources.
func Run(ctx context.Context, cfg *Config) (*VM, error) {
	cfg.setDefaults()
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}

	// Copy the base rootfs image so this VM has its own writable copy.
	// Uses cp --reflink=auto for efficient copy-on-write when the filesystem
	// supports it (btrfs, xfs with reflink).
	diskPath, err := prepareDisk(cfg.Name, cfg.RootFS)
	if err != nil {
		return nil, fmt.Errorf("prepare disk: %w", err)
	}

	// cleanup tears down all resources created so far if we hit an error.
	// Each function is nil-safe, so it's fine to call this at any point.
	var nc *networkConfig
	var cmd *exec.Cmd
	cleanup := func() {
		if cmd != nil && cmd.Process != nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
		removeNetwork(nc)
		removeDisk(cfg.Name)
	}

	// Write /etc/resolv.conf into the rootfs copy with the host's DNS servers.
	// This is done by temporarily mounting the ext4 image, writing the file,
	// and unmounting. The guest can resolve DNS names immediately after boot.
	if err := configureDNS(diskPath); err != nil {
		cleanup()
		return nil, fmt.Errorf("configure dns: %w", err)
	}

	// Create a TAP device for this VM's network. Each VM gets its own TAP with
	// a /30 subnet (one host IP, one guest IP). The TAP is marked persistent
	// so Firecracker can open it by name after we close our file descriptor.
	nc, err = createNetwork(cfg.Name)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("create network: %w", err)
	}

	// Set up the API socket path. Firecracker creates an HTTP server on this
	// Unix domain socket, which we use to configure and control the VM.
	socketDir := socketDirectory()
	if err := os.MkdirAll(socketDir, 0o755); err != nil {
		cleanup()
		return nil, fmt.Errorf("create socket dir: %w", err)
	}
	socketPath := filepath.Join(socketDir, cfg.Name+".socket")
	os.Remove(socketPath) // remove stale socket from a previous crashed run

	// Start the Firecracker process. It creates the API socket and waits for
	// us to send configuration before booting the VM.
	cmd = exec.CommandContext(ctx, cfg.FirecrackerBin, "--api-sock", socketPath)
	cmd.Stdout = cfg.Stdout
	cmd.Stdin = cfg.Stdin
	cmd.Stderr = cfg.Stderr

	if err := cmd.Start(); err != nil {
		cleanup()
		return nil, fmt.Errorf("start firecracker: %w", err)
	}

	// Poll for the socket file to appear on disk before sending API requests.
	if err := waitForSocket(socketPath, 5*time.Second); err != nil {
		cleanup()
		return nil, fmt.Errorf("wait for socket: %w", err)
	}

	// Configure the VM via the Firecracker HTTP API. All configuration must
	// be done before calling StartInstance — after boot, the VM is immutable.
	client := firecracker.NewClient(socketPath)

	// Kernel boot args configure the guest:
	//   console=ttyS0  — serial console output (so we can interact with the VM)
	//   reboot=k       — reboot on kernel panic instead of halting
	//   panic=1        — reboot after 1 second on panic
	//   pci=off        — Firecracker doesn't emulate PCI
	//   ip=...         — static IP for the guest (parsed by the kernel at boot)
	bootArgs := "console=ttyS0 reboot=k panic=1 pci=off " + nc.bootArgsIP()
	if err := client.SetBootSource(ctx, &firecracker.BootSource{
		KernelImagePath: cfg.Kernel,
		BootArgs:        bootArgs,
	}); err != nil {
		cleanup()
		return nil, fmt.Errorf("set boot source: %w", err)
	}

	if err := client.SetDrive(ctx, &firecracker.Drive{
		DriveID:      "rootfs",
		PathOnHost:   diskPath,
		IsRootDevice: true,
		IsReadOnly:   false,
	}); err != nil {
		cleanup()
		return nil, fmt.Errorf("set drive: %w", err)
	}

	if err := client.SetNetworkInterface(ctx, &firecracker.NetworkInterface{
		IfaceID:     "eth0",
		HostDevName: nc.TAPDevice,
		GuestMAC:    nc.GuestMAC,
	}); err != nil {
		cleanup()
		return nil, fmt.Errorf("set network: %w", err)
	}

	if err := client.SetMachineConfig(ctx, &firecracker.MachineConfig{
		VcpuCount:  cfg.CPUs,
		MemSizeMib: cfg.Memory,
		Smt:        false,
	}); err != nil {
		cleanup()
		return nil, fmt.Errorf("set machine config: %w", err)
	}

	// Boot the VM. After this, the guest kernel starts and eventually presents
	// a login prompt on the serial console (stdout).
	if err := client.StartInstance(ctx); err != nil {
		cleanup()
		return nil, fmt.Errorf("start instance: %w", err)
	}

	return &VM{
		Name:       cfg.Name,
		PID:        cmd.Process.Pid,
		SocketPath: socketPath,
		StartedAt:  time.Now(),
		Status:     "Running",
		CPUs:       cfg.CPUs,
		Memory:     cfg.Memory,
		GuestIP:    nc.GuestIP.String(),
		cmd:        cmd,
		client:     client,
		net:        nc,
		diskPath:   diskPath,
	}, nil
}

// Wait blocks until the Firecracker process exits. This happens when the guest
// shuts down (e.g., after Stop sends Ctrl+Alt+Del) or the process is killed.
func (vm *VM) Wait() error {
	return vm.cmd.Wait()
}

// Stop asks the guest OS to shut down gracefully by sending Ctrl+Alt+Del via
// the Firecracker API. The guest handles this as a shutdown signal. Call Wait()
// after Stop() to block until the VM process actually exits.
func (vm *VM) Stop(ctx context.Context) error {
	return vm.client.StopInstance(ctx)
}

// Cleanup releases all resources for this VM: removes the API socket, destroys
// the TAP network device, and deletes the rootfs copy. Always call this after
// Wait() returns to avoid leaking network interfaces and disk space.
// Safe to call multiple times.
func (vm *VM) Cleanup() error {
	var errs []error
	os.Remove(vm.SocketPath)
	if err := removeNetwork(vm.net); err != nil {
		errs = append(errs, fmt.Errorf("remove network: %w", err))
	}
	if err := removeDisk(vm.Name); err != nil {
		errs = append(errs, fmt.Errorf("remove disk: %w", err))
	}
	return errors.Join(errs...)
}

// List discovers running VMs by scanning the socket directory and querying each
// Firecracker instance via its API. We don't keep any state files — a socket
// that responds to API calls means the VM is running. Stale sockets (from
// crashed VMs) are silently skipped.
func List() ([]*VM, error) {
	dir := socketDirectory()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var vms []*VM
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".socket") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".socket")
		socketPath := filepath.Join(dir, e.Name())

		// Try to connect to the Firecracker API. If this fails, the socket is
		// stale (the VM crashed or was killed without cleanup) — just skip it.
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
			client:     client,
		}

		// Fetch the full VM config to get CPU count, memory, and guest IP.
		// The guest IP is encoded in the kernel boot args (ip=GUEST::HOST:...).
		vmCfg, err := client.GetVMConfig(ctx)
		if err == nil {
			if vmCfg.MachineConfig != nil {
				vm.CPUs = vmCfg.MachineConfig.VcpuCount
				vm.Memory = vmCfg.MachineConfig.MemSizeMib
			}
			if vmCfg.BootSource != nil {
				vm.GuestIP = parseGuestIP(vmCfg.BootSource.BootArgs)
			}
		}
		cancel()

		// Use the socket file's modification time as a rough start time.
		fi, err := e.Info()
		if err == nil {
			vm.StartedAt = fi.ModTime()
		}

		// Find the Firecracker process PID by scanning /proc for a process
		// whose command line mentions this socket path.
		vm.PID = findFirecrackerPID(socketPath)

		vms = append(vms, vm)
	}
	return vms, nil
}

// socketDirectory returns the path where VM API sockets are stored.
// Uses $XDG_RUNTIME_DIR/knaller if set, otherwise /tmp/knaller.
func socketDirectory() string {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return filepath.Join(dir, "knaller")
	}
	return "/tmp/knaller"
}

// waitForSocket polls for a Unix socket file to appear on disk. Firecracker
// creates this file shortly after its process starts.
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
