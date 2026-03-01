package cli

import "fmt"

// version, commit, and date are set at build time by GoReleaser using ldflags.
// When building locally with "go build", they keep their default "dev" values.
// GoReleaser sets them automatically via:
//
//	-X github.com/benben/knaller/internal/cli.version={{.Version}}
//	-X github.com/benben/knaller/internal/cli.commit={{.Commit}}
//	-X github.com/benben/knaller/internal/cli.date={{.Date}}
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

// Version implements the "knaller version" subcommand.
// It prints the version, git commit, and build date.
func Version(args []string) error {
	fmt.Printf("knaller %s (commit: %s, built: %s)\n", version, commit, date)
	return nil
}
