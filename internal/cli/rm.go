package cli

import (
	"flag"
	"fmt"

	"github.com/benben/knaller"
)

// Rm implements the "knaller rm" subcommand. It removes a stopped VM's data
// directory and any stale socket.
func Rm(args []string) error {
	fs := flag.NewFlagSet("rm", flag.ExitOnError)
	name := fs.String("name", "", "VM name (required)")
	fs.Parse(args)

	if *name == "" {
		return fmt.Errorf("--name is required")
	}

	if err := knaller.RemoveVM(*name); err != nil {
		return err
	}

	fmt.Printf("Removed VM %q\n", *name)
	return nil
}
