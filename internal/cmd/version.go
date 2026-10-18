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
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the po version, commit and build date",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "po version %s (commit %s, built %s)\n", version, commit, date)
			return err
		},
	}
}
