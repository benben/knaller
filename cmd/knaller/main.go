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
		"start":   cli.Start,
		"stop":    cli.Stop,
		"list":    cli.List,
		"ls":      cli.List,
		"version": cli.Version,
	}

	if len(args) == 0 {
		usage()
		return fmt.Errorf("no command specified")
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
	fmt.Fprintln(os.Stderr, "  start    Start a microVM (connect via SSH)")
	fmt.Fprintln(os.Stderr, "  stop     Stop a running microVM")
	fmt.Fprintln(os.Stderr, "  list     List running microVMs")
	fmt.Fprintln(os.Stderr, "  ls       Alias for list")
	fmt.Fprintln(os.Stderr, "  version  Print version information")
}
