package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/scientist-labs/pharmaobservatory-cli/internal/apierr"
)

// noArgs refuses a stray argument as a usage error (exit 2); cobra.NoArgs would exit 1.
func noArgs(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		return apierr.Usage(fmt.Errorf("unexpected argument %q for %q\nRun '%s --help' for usage", args[0], cmd.CommandPath(), cmd.CommandPath()))
	}
	return nil
}

func newExitCodesCmd() *cobra.Command {
	var md bool
	c := &cobra.Command{
		Use:   "exit-codes",
		Short: "Print the exit code table",
		Long:  "The exit codes, which scripts can branch on:\n\n" + strings.TrimRight(apierr.TableText(), "\n"),
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if md {
				_, err := fmt.Fprint(cmd.OutOrStdout(), apierr.TableMarkdown())
				return err
			}
			_, err := fmt.Fprint(cmd.OutOrStdout(), apierr.TableText())
			return err
		},
	}
	c.Flags().BoolVar(&md, "markdown", false, "print the table as markdown (the README's source)")
	return c
}
