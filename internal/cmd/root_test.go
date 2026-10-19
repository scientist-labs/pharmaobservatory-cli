package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/scientist-labs/pharmaobservatory-cli/internal/apierr"
)

func run(args ...string) (string, error) {
	root := NewRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func TestVersionDefaults(t *testing.T) {
	out, err := run("version")
	if err != nil {
		t.Fatal(err)
	}
	if want := "pharma version dev (commit dev, built dev)"; !strings.Contains(out, want) {
		t.Fatalf("got %q want %q", out, want)
	}
}

func TestHelpListsVersion(t *testing.T) {
	out, err := run("--help")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "version") {
		t.Fatalf("help missing version: %q", out)
	}
}

func TestUnknownCommandIsUsageError(t *testing.T) {
	out, err := run("nonsense")
	if err == nil {
		t.Fatal("expected error")
	}
	if got := apierr.ExitCode(err); got != apierr.ExitUsage {
		t.Fatalf("exit %d want %d", got, apierr.ExitUsage)
	}
	if !strings.Contains(out+err.Error(), "unknown command") {
		t.Fatalf("no usage message: %q", out)
	}
}

func TestBadFlagIsUsageError(t *testing.T) {
	_, err := run("--no-such-flag")
	if got := apierr.ExitCode(err); got != apierr.ExitUsage {
		t.Fatalf("exit %d want %d", got, apierr.ExitUsage)
	}
}
