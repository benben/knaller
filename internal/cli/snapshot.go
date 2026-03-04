package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/benben/knaller"
)

// Snapshot implements the "knaller snapshot" subcommand. It dispatches to
// snapshot creation (default) or listing (ls subcommand).
//
// Usage:
//
//	knaller snapshot --name <vm>    Create a snapshot
//	knaller snapshot ls             List all snapshots
func Snapshot(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "ls":
			return snapshotList()
		case "delete":
			return snapshotDelete(args[1:])
		}
	}
	return snapshotCreate(args)
}

func snapshotCreate(args []string) error {
	fs := flag.NewFlagSet("snapshot", flag.ExitOnError)
	name := fs.String("name", "", "VM name (required)")
	fs.Parse(args)

	if *name == "" {
		return fmt.Errorf("--name is required")
	}

	start := time.Now()
	id, err := knaller.CreateSnapshot(context.Background(), *name, os.Stderr)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "Snapshot %s created from VM %q (%s)\n", id, *name, time.Since(start).Round(time.Millisecond))
	return nil
}

func snapshotDelete(args []string) error {
	fs := flag.NewFlagSet("snapshot delete", flag.ExitOnError)
	id := fs.String("id", "", "Snapshot ID (required)")
	fs.Parse(args)

	if *id == "" {
		return fmt.Errorf("--id is required")
	}

	if err := knaller.DeleteSnapshot(*id); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Snapshot %s deleted\n", *id)
	return nil
}

func snapshotList() error {
	snapshots, err := knaller.ListSnapshots()
	if err != nil {
		return err
	}

	w := tabwriter.NewWriter(os.Stdout, 4, 8, 4, ' ', 0)
	fmt.Fprintln(w, "ID\tVM\tVCPUS\tMEMORY\tAGE")
	for _, s := range snapshots {
		age := formatDuration(time.Since(s.CreatedAt))
		fmt.Fprintf(w, "%s\t%s\t%d\t%dMiB\t%s\n",
			s.ID, s.VMName, s.VCPUs, s.MemSizeMib, age)
	}
	w.Flush()
	return nil
}
