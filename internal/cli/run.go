package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/benben/knaller"
)

// Run implements the "knaller run" subcommand. It starts a Firecracker microVM
// and connects the user's terminal to its serial console. The VM runs until
// the guest shuts down or the user presses Ctrl+C.
func Run(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	defaultKernel, defaultRootFS := defaultPaths()
	name := fs.String("name", "", "VM name (default: random)")
	kernel := fs.String("kernel", defaultKernel, "Kernel image path")
	rootfs := fs.String("rootfs", defaultRootFS, "Base rootfs path")
	cpus := fs.Int("cpus", 1, "Number of vCPUs")
	mem := fs.Int("mem", 128, "Memory in MiB")
	fcBin := fs.String("firecracker", "firecracker", "Firecracker binary path")
	fs.Parse(args)

	cfg := &knaller.Config{
		Name:           *name,
		Kernel:         *kernel,
		RootFS:         *rootfs,
		CPUs:           *cpus,
		Memory:         *mem,
		FirecrackerBin: *fcBin,
		Stdout:         os.Stdout,
		Stdin:          os.Stdin,
		Stderr:         os.Stderr,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	vm, err := knaller.Run(ctx, cfg)
	if err != nil {
		return err
	}

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

	// Clean up all resources: TAP device, rootfs copy, API socket.
	vm.Cleanup()
	return nil
}

// defaultPaths returns the default kernel and rootfs paths under
// ~/.local/share/knaller/. These match the download instructions in the README.
func defaultPaths() (kernel, rootfs string) {
	home, _ := os.UserHomeDir()
	base := filepath.Join(home, ".local", "share", "knaller")
	return filepath.Join(base, "vmlinux"), filepath.Join(base, "rootfs.ext4")
}
