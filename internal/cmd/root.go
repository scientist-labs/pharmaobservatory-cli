// Package cmd holds the hand-written cobra command tree.
package cmd

import (
	"github.com/spf13/cobra"

	"github.com/scientist-labs/pharmaobservatory-cli/internal/apierr"
)

// NewRoot builds the root command.
func NewRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           "po",
		Short:         "Pharma Observatory command line client",
		SilenceUsage:  true,
		SilenceErrors: false,
		// An unknown subcommand is a usage error (exit 2), not cobra's default of 1.
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return apierr.Usage(unknownCommand(cmd, args[0]))
			}
			return nil
		},
		Run: func(cmd *cobra.Command, args []string) { _ = cmd.Help() },
	}
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return apierr.Usage(err) })
	root.AddCommand(newVersionCmd())
	return root
}
