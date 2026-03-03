package cli

import (
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/benben/knaller"
)

// List implements the "knaller list" subcommand. It discovers running VMs by
// scanning the socket directory and querying each Firecracker instance for its
// configuration.
func List(args []string) error {
	vms, err := knaller.List()
	if err != nil {
		return err
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
		fmt.Fprintf(w, "%s\t%s\t%g\t%dMiB\t%s\t%s\t%s\n",
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
