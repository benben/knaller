package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/benben/knaller"
)

// Start implements the "knaller start" subcommand. It starts a Firecracker
// microVM, prints the SSH connection info, and blocks until the VM exits.
// The VM is non-interactive — connect via SSH. Press Ctrl+C to stop.
func Start(args []string) error {
	fs := flag.NewFlagSet("start", flag.ExitOnError)
	defaultKernel, defaultRootFS := defaultPaths()
	name := fs.String("name", "", "VM name (required)")
	kernel := fs.String("kernel", defaultKernel, "Kernel image path")
	rootfs := fs.String("rootfs", defaultRootFS, "Base rootfs path")
	cpus := fs.Float64("cpus", 1, "vCPUs (e.g. 0.5 = 1 vCPU at 50% CPU quota)")
	mem := fs.Int("mem", 1024, "Memory in MiB")
	netBw := fs.Float64("network-bandwidth", 0, "Network bandwidth limit in Mbps per direction (0 = unlimited)")
	diskBw := fs.Int("disk-bandwidth", 0, "Disk bandwidth limit in MB/s (0 = unlimited)")
	diskIOPS := fs.Int("disk-iops", 0, "Disk I/O operations per second limit (0 = unlimited)")
	fcBin := fs.String("firecracker", "firecracker", "Firecracker binary path")
	pastaBin := fs.String("pasta", "pasta", "pasta binary path")
	verbose := fs.Bool("verbose", false, "Show serial console and process output")
	fs.Parse(args)

	if *name == "" {
		return fmt.Errorf("--name is required")
	}

	cfg := &knaller.Config{
		Name:           *name,
		Kernel:         *kernel,
		RootFS:         *rootfs,
		CPUs:           *cpus,
		Memory:         *mem,
		NetworkMbps:    *netBw,
		DiskMBps:       *diskBw,
		DiskIOPS:       *diskIOPS,
		FirecrackerBin: *fcBin,
		PastaBin:       *pastaBin,
	}
	if *verbose {
		cfg.Stdout = os.Stdout
		cfg.Stderr = os.Stderr
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	vm, err := knaller.Run(ctx, cfg)
	if err != nil {
		return err
	}

	// Print connection info so the user knows how to reach the VM.
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
	fmt.Fprintf(os.Stderr, "  ssh -p %d root@localhost\n", vm.SSHPort)
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

	// Clean up all resources: socket, rootfs copy.
	vm.Cleanup()
	fmt.Fprintf(os.Stderr, "\nVM stopped and cleaned up.\n")
	return nil
}

// defaultPaths returns the default kernel and rootfs paths under
// ~/.local/share/knaller/.
func defaultPaths() (kernel, rootfs string) {
	home, _ := os.UserHomeDir()
	base := filepath.Join(home, ".local", "share", "knaller")
	return filepath.Join(base, "vmlinux"), filepath.Join(base, "rootfs.ext4")
}
