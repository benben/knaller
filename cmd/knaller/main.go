// Command knaller is a CLI for running Firecracker microVMs.
//
// Usage:
//
//	knaller <command> [flags]
//
// Commands:
//
//	start    Start a microVM (connect via SSH)
//	stop     Stop a running microVM
//	list     List running microVMs (alias: ls)
//	version  Print the knaller version
package main

import (
	"fmt"
	"os"

	"github.com/benben/knaller/internal/cli"
)

func main() {
	if err := dispatch(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// dispatch routes the subcommand name to the appropriate handler function.
// It's separated from main() so it can be tested without os.Exit.
func dispatch(args []string) error {
	cmds := map[string]func([]string) error{
		"start":    cli.Start,
		"stop":     cli.Stop,
		"rm":       cli.Rm,
		"pause":    cli.Pause,
		"resume":   cli.Resume,
		"snapshot": cli.Snapshot,
		"list":     cli.List,
		"ls":       cli.List,
		"version":  cli.Version,
	}

	if len(args) == 0 {
		usage()
		return fmt.Errorf("no command specified")
	}

	if args[0] == "help" {
		usage()
		return nil
	}

	fn, ok := cmds[args[0]]
	if !ok {
		usage()
		return fmt.Errorf("unknown command: %s", args[0])
	}

	return fn(args[1:])
}

func usage() {
	fmt.Fprintln(os.Stderr, "Usage: knaller <command> [flags]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Commands:")
	fmt.Fprintln(os.Stderr, "  start             Start a microVM (connect via SSH)")
	fmt.Fprintln(os.Stderr, "  stop              Stop a running microVM")
	fmt.Fprintln(os.Stderr, "  rm                Remove a stopped microVM")
	fmt.Fprintln(os.Stderr, "  pause             Pause a running microVM")
	fmt.Fprintln(os.Stderr, "  resume            Resume a paused microVM")
	fmt.Fprintln(os.Stderr, "  snapshot          Create a VM snapshot")
	fmt.Fprintln(os.Stderr, "  snapshot ls       List all snapshots")
	fmt.Fprintln(os.Stderr, "  snapshot delete   Delete a snapshot")
	fmt.Fprintln(os.Stderr, "  list              List running microVMs")
	fmt.Fprintln(os.Stderr, "  ls                Alias for list")
	fmt.Fprintln(os.Stderr, "  version           Print version information")
	fmt.Fprintln(os.Stderr, "  help              Show this help")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Use knaller <command> --help for more information about a command.")
}
