package cli

import (
	"context"
	"flag"
	"fmt"

	"github.com/benben/knaller"
)

// Pause implements the "knaller pause" subcommand. It pauses a running VM's
// vCPUs until resumed with "knaller resume".
func Pause(args []string) error {
	fs := flag.NewFlagSet("pause", flag.ExitOnError)
	name := fs.String("name", "", "VM name (required)")
	fs.Parse(args)

	if *name == "" {
		return fmt.Errorf("--name is required")
	}

	if err := knaller.PauseVM(context.Background(), *name); err != nil {
		return err
	}

	fmt.Printf("VM %q paused\n", *name)
	return nil
}
