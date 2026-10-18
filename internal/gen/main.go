// Command gen is the command-tree generator. It reads the OpenAPI document and the Go source of the
// pinned SDK module, joins them on (GET, path) and writes the page commands as Go source. Run it
// through go generate; CI fails when its output differs from what is committed.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/scientist-labs/pharmaobservatory-cli/internal/gen/tree"
)

const module = "github.com/scientist-labs/pharmaobservatory-sdks/go"

func main() {
	out := flag.String("out", "pages", "directory the generated files are written to")
	flag.Parse()
	if err := run(*out); err != nil {
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(1)
	}
}

func run(out string) error {
	b, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}\t{{.Version}}", module).Output()
	if err != nil {
		return fmt.Errorf("cannot locate the SDK module %s: %w", module, err)
	}
	dir, version, ok := strings.Cut(strings.TrimSpace(string(b)), "\t")
	if !ok || dir == "" || version == "" {
		return fmt.Errorf("unexpected go list output %q", b)
	}
	m, err := tree.Build(dir, module, version)
	if err != nil {
		return err
	}
	files, err := tree.Render(m)
	if err != nil {
		return err
	}
	old, err := filepath.Glob(filepath.Join(out, "zz_generated_*.go"))
	if err != nil {
		return err
	}
	for _, f := range old {
		if err := os.Remove(f); err != nil {
			return err
		}
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(out, f.Name), f.Data, 0o644); err != nil {
			return err
		}
	}
	return nil
}
