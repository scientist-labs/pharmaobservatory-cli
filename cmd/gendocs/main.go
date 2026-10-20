// Command gendocs writes the man pages and shell completions for the release packaging, built from
// the live cobra command tree (generated dataset commands included). GoReleaser runs it in a
// before hook; its output (manpages/, completions/) is gitignored and never committed.
//
//	go run ./cmd/gendocs [outdir]    (default: the current directory)
package main

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"

	"github.com/scientist-labs/pharmaobservatory-cli/internal/apierr"
	"github.com/scientist-labs/pharmaobservatory-cli/internal/cmd"
)

func main() {
	out := "."
	if len(os.Args) > 1 {
		out = os.Args[1]
	}
	if err := run(out); err != nil {
		fmt.Fprintln(os.Stderr, "gendocs:", err)
		os.Exit(1)
	}
}

func run(out string) error {
	root := cmd.NewRoot()
	// Cobra adds `completion` and `help` at Execute time; add them now so the man pages list them.
	root.InitDefaultCompletionCmd()
	root.InitDefaultHelpCmd()
	root.DisableAutoGenTag = true // no timestamp: the output is reproducible

	man := filepath.Join(out, "manpages")
	comp := filepath.Join(out, "completions")
	for _, d := range []string{man, comp} {
		if err := os.RemoveAll(d); err != nil {
			return err
		}
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}

	hdr := &doc.GenManHeader{Title: apierr.Bin, Section: "1", Source: "Pharma Observatory", Manual: "Pharma Observatory CLI"}
	tmp, err := os.MkdirTemp("", "gendocs")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	if err := doc.GenManTree(root, hdr, tmp); err != nil {
		return err
	}
	pages, err := filepath.Glob(filepath.Join(tmp, "*.1"))
	if err != nil {
		return err
	}
	for _, p := range pages {
		if err := gzipTo(p, filepath.Join(man, filepath.Base(p)+".gz")); err != nil {
			return err
		}
	}

	return writeCompletions(root, comp)
}

func writeCompletions(root *cobra.Command, dir string) error {
	gens := []struct {
		file string
		gen  func(*bytes.Buffer) error
	}{
		{apierr.Bin + ".bash", func(b *bytes.Buffer) error { return root.GenBashCompletionV2(b, true) }},
		{"_" + apierr.Bin, func(b *bytes.Buffer) error { return root.GenZshCompletion(b) }},
		{apierr.Bin + ".fish", func(b *bytes.Buffer) error { return root.GenFishCompletion(b, true) }},
	}
	for _, g := range gens {
		var b bytes.Buffer
		if err := g.gen(&b); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, g.file), b.Bytes(), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// gzipTo compresses src to dst with an empty header (no name or mtime) so the bytes are reproducible.
func gzipTo(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	var b bytes.Buffer
	zw, err := gzip.NewWriterLevel(&b, gzip.BestCompression)
	if err != nil {
		return err
	}
	if _, err := zw.Write(data); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return os.WriteFile(dst, b.Bytes(), 0o644)
}
