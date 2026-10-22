package cmd

import (
	"io"
	"net/http"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/scientist-labs/pharmaobservatory-cli/internal/config"
	"github.com/scientist-labs/pharmaobservatory-cli/internal/update"
)

// Execute runs the real command line and, after it, the once-a-day new-release notice on stderr.
func Execute(stderr *os.File) error {
	d := defaultDeps()
	d.Update = &update.Notifier{
		HC:      &http.Client{Timeout: update.Timeout},
		Dir:     config.Dir,
		Current: version,
		IsTTY:   term.IsTerminal(int(stderr.Fd())),
		Exe:     d.exe(),
		Stderr:  stderr,
	}
	return ExecuteWith(d, nil, nil)
}

// ExecuteWith runs the command tree over d. The notice is started before the command and printed after it
// when its answer is ready, whether or not the command failed. Machine output (--json, --csv) and the
// notifier's own gates (TTY, CI, opt-out, dev builds) suppress it. args nil means os.Args; out nil
// leaves cobra's stdout and stderr.
func ExecuteWith(d Deps, args []string, out io.Writer) error {
	d = d.withUserAgent()
	root, g := newRoot(d)
	if out != nil {
		root.SetOut(out)
		root.SetErr(out)
	}
	if args != nil {
		root.SetArgs(args)
	}
	n := d.Update
	if n != nil {
		prev := root.PersistentPreRun
		root.PersistentPreRun = func(cmd *cobra.Command, a []string) {
			if prev != nil {
				prev(cmd, a)
			}
			if !g.json && !g.csv {
				n.Start(ctxOf(cmd.Context()))
			}
		}
	}
	err := root.Execute()
	if n != nil {
		n.Finish()
	}
	return err
}
