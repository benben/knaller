package cli

import (
	"context"
	"flag"
	"fmt"

	"github.com/benben/knaller"
)

// Resume implements the "knaller resume" subcommand. It resumes a paused VM.
func Resume(args []string) error {
	fs := flag.NewFlagSet("resume", flag.ExitOnError)
	name := fs.String("name", "", "VM name (required)")
	fs.Parse(args)

	if *name == "" {
		return fmt.Errorf("--name is required")
	}

	if err := knaller.ResumeVM(context.Background(), *name); err != nil {
		return err
	}

	fmt.Printf("VM %q resumed\n", *name)
	return nil
}
