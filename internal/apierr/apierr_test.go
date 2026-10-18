package apierr

import (
	"errors"
	"fmt"
	"testing"
)

func TestExitCode(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, ExitOK},
		{"plain", errors.New("boom"), ExitUnexpected},
		{"usage", Usage(errors.New("bad flag")), ExitUsage},
		{"wrapped usage", fmt.Errorf("ctx: %w", Usage(errors.New("x"))), ExitUsage},
	}
	for _, c := range cases {
		if got := ExitCode(c.err); got != c.want {
			t.Errorf("%s: got %d want %d", c.name, got, c.want)
		}
	}
}
