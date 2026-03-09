package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/benben/knaller"
)

// portList implements flag.Value for repeatable --port flags.
type portList []knaller.PortMapping

func (p *portList) String() string { return "" }

func (p *portList) Set(val string) error {
	host, guest, ok := strings.Cut(val, ":")
	if !ok {
		return fmt.Errorf("invalid port mapping %q, expected HOST:GUEST", val)
	}
	h, err := strconv.Atoi(host)
	if err != nil {
		return fmt.Errorf("invalid host port %q", host)
	}
	g, err := strconv.Atoi(guest)
	if err != nil {
		return fmt.Errorf("invalid guest port %q", guest)
	}
	*p = append(*p, knaller.PortMapping{Host: h, Guest: g})
	return nil
}

// Start implements the "knaller start" subcommand. It starts a Firecracker
// microVM, prints the SSH connection info, and blocks until the VM exits.
// The VM is non-interactive — connect via SSH. Press Ctrl+C to stop.
func Start(args []string) error {
	fs := flag.NewFlagSet("start", flag.ExitOnError)
	name := fs.String("name", "", "VM name (required)")
	kernel := fs.String("kernel", "~/.local/share/knaller/vmlinux", "Kernel image path")
	rootfs := fs.String("rootfs", "~/.local/share/knaller/rootfs.ext4", "Base rootfs path")
	cpus := fs.Float64("cpus", 1, "vCPUs (e.g. 0.5 = 1 vCPU at 50% CPU quota)")
	mem := fs.Int("mem", 1024, "Memory in MiB")
	netBw := fs.Float64("network-bandwidth", 0, "Network bandwidth limit in Mbps per direction (0 = unlimited)")
	diskBw := fs.Int("disk-bandwidth", 0, "Disk bandwidth limit in MB/s (0 = unlimited)")
	diskIOPS := fs.Int("disk-iops", 0, "Disk I/O operations per second limit (0 = unlimited)")
	var ports portList
	fs.Var(&ports, "port", "Port forwarding HOST:GUEST (repeatable)")
	fromSnapshot := fs.String("from-snapshot", "", "Restore from snapshot ID")
	fcBin := fs.String("firecracker", "firecracker", "Firecracker binary path")
	pastaBin := fs.String("pasta", "pasta", "pasta binary path")
	detach := fs.Bool("detach", false, "Run VM in the background")
	verbose := fs.Bool("verbose", false, "Show serial console and process output")
	fs.Parse(args)

	if *name == "" {
		return fmt.Errorf("--name is required")
	}

	cfg := &knaller.Config{
		Name:           *name,
		Kernel:         expandHome(*kernel),
		RootFS:         expandHome(*rootfs),
		CPUs:           *cpus,
		Memory:         *mem,
		NetworkMbps:    *netBw,
		DiskMBps:       *diskBw,
		DiskIOPS:       *diskIOPS,
		Ports:          ports,
		SnapshotID:     *fromSnapshot,
		FirecrackerBin: *fcBin,
		PastaBin:       *pastaBin,
	}
	cfg.Detach = *detach
	if *verbose {
		cfg.Stdout = os.Stdout
		cfg.Stderr = os.Stderr
	}

	ctx := context.Background()
	var cancel context.CancelFunc
	if !*detach {
		ctx, cancel = context.WithCancel(ctx)
		defer cancel()
	}

	vm, err := knaller.Run(ctx, cfg)
	if err != nil {
		return err
	}

	// Print connection info so the user knows how to reach the VM.
	if *fromSnapshot != "" {
		fmt.Fprintf(os.Stderr, "\nVM %q started from snapshot %s (%g vCPUs, %d MiB)\n", vm.Name, *fromSnapshot, vm.CPUs, vm.Memory)
	} else {
		netInfo := "net: unlimited"
		if *netBw > 0 {
			netInfo = fmt.Sprintf("net: %g Mbps", *netBw)
		}
		diskInfo := "disk: unlimited"
		if *diskBw > 0 || *diskIOPS > 0 {
			parts := []string{}
			if *diskBw > 0 {
				parts = append(parts, fmt.Sprintf("%d MB/s", *diskBw))
			}
			if *diskIOPS > 0 {
				parts = append(parts, fmt.Sprintf("%d IOPS", *diskIOPS))
			}
			diskInfo = "disk: " + strings.Join(parts, ", ")
		}
		fmt.Fprintf(os.Stderr, "\nVM %q started (%g vCPUs, %d MiB, %s, %s)\n", vm.Name, vm.CPUs, vm.Memory, netInfo, diskInfo)
	}
	if *detach {
		fmt.Fprintf(os.Stderr, "Waiting for VM to boot...")
		if err := vm.WaitForSSH(30 * time.Second); err != nil {
			fmt.Fprintf(os.Stderr, " failed\n")
			return err
		}
		fmt.Fprintf(os.Stderr, " ready\n")
	}
	fmt.Fprintf(os.Stderr, "  ssh -p %d root@localhost\n", vm.Port)
	if *detach {
		fmt.Fprintf(os.Stderr, "  knaller stop --name %s\n\n", vm.Name)
		return nil
	}
	fmt.Fprintf(os.Stderr, "  Press Ctrl+C to stop\n\n")

	// Catch Ctrl+C and SIGTERM to gracefully shut down the VM before exiting.
	// Stop() sends Ctrl+Alt+Del to the guest, which triggers a clean shutdown.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Fprintln(os.Stderr, "\nShutting down VM...")
		vm.Stop(context.Background())
	}()

	// Block until the Firecracker process exits (guest shut down or killed).
	vm.Wait()

	// Remove the stale socket. The rootfs is kept so the VM shows as
	// "Stopped" in knaller ls. Use "knaller rm" to fully remove it.
	os.Remove(vm.SocketPath)
	fmt.Fprintf(os.Stderr, "\nVM stopped.\n")
	return nil
}

// expandHome replaces a leading ~ with the user's home directory.
func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, path[2:])
	}
	return path
}
