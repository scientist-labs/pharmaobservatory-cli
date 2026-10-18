// Command po is the Pharma Observatory command line client.
package main

import (
	"os"

	"github.com/scientist-labs/pharmaobservatory-cli/internal/apierr"
	"github.com/scientist-labs/pharmaobservatory-cli/internal/cmd"
)

func main() {
	if err := cmd.NewRoot().Execute(); err != nil {
		os.Exit(apierr.ExitCode(err))
	}
}
