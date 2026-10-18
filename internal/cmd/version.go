package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Set by GoReleaser ldflags; "dev" for a plain go build.
var (
	version = "dev"
	commit  = "dev"
	date    = "dev"

	// binaryName is the executable's name. It is set from .goreleaser.yml (the single place the
	// name is defined) by ldflags; "po" is only the value of a plain go build.
	binaryName = "po"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the " + binaryName + " version, commit and build date",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s version %s (commit %s, built %s)\n", binaryName, version, commit, date)
			return err
		},
	}
}
