// Package cmd holds the hand-written cobra command tree.
package cmd

import (
	"github.com/spf13/cobra"

	"github.com/scientist-labs/pharmaobservatory-cli/internal/apierr"
	"github.com/scientist-labs/pharmaobservatory-cli/internal/cmd/pages"
)

// NewRoot builds the root command.
func NewRoot() *cobra.Command { return NewRootWith(defaultDeps()) }

// NewRootWith builds the root command over explicit dependencies (tests).
func NewRootWith(d Deps) *cobra.Command {
	g := &globals{}
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
	root.PersistentFlags().StringVar(&g.profile, "profile", "", "profile to use (default: $PO_PROFILE, config default_profile, then prod)")
	root.PersistentFlags().StringVar(&g.apiBase, "api-base", "", "send requests here instead of the profile origin (audience unchanged)")
	root.PersistentFlags().BoolVar(&g.json, "json", false, "print the raw response body (the envelope carries the attribution)")
	root.PersistentFlags().BoolVar(&g.csv, "csv", false, "print CSV built from the response columns (a get prints its identity rows only); source credits follow the rows as `# Source: ...` comment lines, which most readers skip (pandas: comment='#')")
	root.PersistentFlags().BoolVar(&g.all, "all", false, "follow the cursor to the last page and print one table")
	root.PersistentFlags().BoolVar(&g.quiet, "quiet", false, "drop the footer's as-of, link and paging lines; the source credit is always printed")
	root.PersistentFlags().BoolVar(&g.noFooter, "no-footer", false, "same as --quiet; the source credit is always printed")
	root.AddCommand(newVersionCmd(), newLoginCmd(g, d), newLogoutCmd(g, d), newWhoamiCmd(g, d), newAuthCmd(g, d), newAPICmd(g, d))
	root.AddCommand(pages.Build(pageEnv(g, d))...)
	return root
}
