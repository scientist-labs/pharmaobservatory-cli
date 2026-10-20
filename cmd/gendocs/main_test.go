package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRunWritesManPagesAndCompletions(t *testing.T) {
	out := t.TempDir()
	if err := run(out); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{
		"manpages/pharma.1.gz",
		"manpages/pharma-login.1.gz",
		"manpages/pharma-completion.1.gz",
		"completions/pharma.bash",
		"completions/_pharma",
		"completions/pharma.fish",
	} {
		fi, err := os.Stat(filepath.Join(out, f))
		if err != nil || fi.Size() == 0 {
			t.Errorf("%s missing or empty: %v", f, err)
		}
	}
}
