package cli

import (
	"flag"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/benben/knaller"
)

// List implements the "knaller list" subcommand. It discovers running VMs by
// scanning the socket directory and querying each Firecracker instance for its
// configuration. Output is a table with VM name, status, vCPUs, memory, IP,
// PID, and age. Use -q for quiet mode (names only).
func List(args []string) error {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	quiet := fs.Bool("q", false, "Quiet mode (names only)")
	fs.Parse(args)

	vms, err := knaller.List()
	if err != nil {
		return err
	}

	if *quiet {
		for _, vm := range vms {
			fmt.Println(vm.Name)
		}
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 4, 8, 4, ' ', 0)
	fmt.Fprintln(w, "NAME\tSTATUS\tVCPUS\tMEMORY\tSSH\tPID\tAGE")
	for _, vm := range vms {
		age := formatDuration(time.Since(vm.StartedAt))
		pid := ""
		if vm.PID > 0 {
			pid = fmt.Sprintf("%d", vm.PID)
		}
		ssh := fmt.Sprintf("localhost:%d", vm.SSHPort)
		fmt.Fprintf(w, "%s\t%s\t%d\t%dMiB\t%s\t%s\t%s\n",
			vm.Name, vm.Status, vm.CPUs, vm.Memory, ssh, pid, age)
	}
	w.Flush()
	return nil
}

// formatDuration converts a duration to a short human-readable string like
// "5s", "3m", "2h", or "5d".
func formatDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
