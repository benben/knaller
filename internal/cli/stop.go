package cli

import (
	"context"
	"flag"
	"fmt"

	"github.com/benben/knaller"
)

// Stop implements the "knaller stop" subcommand. It connects to a running VM's
// Firecracker API socket and sends Ctrl+Alt+Del, triggering a graceful guest
// shutdown.
func Stop(args []string) error {
	fs := flag.NewFlagSet("stop", flag.ExitOnError)
	name := fs.String("name", "", "VM name (required)")
	fs.Parse(args)

	if *name == "" {
		return fmt.Errorf("--name is required")
	}

	if err := knaller.StopVM(context.Background(), *name); err != nil {
		return err
	}

	fmt.Printf("Sent shutdown signal to VM %q\n", *name)
	return nil
}
