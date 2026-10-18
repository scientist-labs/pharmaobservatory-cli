package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

func unknownCommand(cmd *cobra.Command, name string) error {
	return fmt.Errorf("unknown command %q for %q\nRun '%s --help' for usage", name, cmd.CommandPath(), cmd.CommandPath())
}
