package apierr

import (
	"errors"
	"fmt"
	"strings"
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

func TestFromResponse(t *testing.T) {
	cases := []struct {
		status int
		body   string
		code   int
		want   string
	}{
		{401, `{"error":"invalid_audience"}`, ExitAuth, "po login --profile prod"},
		{401, `{"error":"expired_token"}`, ExitAuth, "po login --profile prod"},
		{401, `{"error":"missing_token"}`, ExitAuth, "po login --profile prod"},
		{401, `{"error":"invalid_token"}`, ExitAuth, "invalid_token"},
		{403, `{"error":"missing_role","role":"example:api"}`, ExitForbidden, "role example:api"},
		{403, `{"error":"missing_role"}`, ExitForbidden, "missing_role"},
		{403, `{"error":"access_pending"}`, ExitForbidden, "access_pending"},
		{503, `{"error":"no_identity_provider"}`, ExitUnavail, "no_identity_provider"},
		{502, `bad gateway`, ExitUnavail, "HTTP 502"},
		{418, `{"error":"teapot"}`, ExitUnexpected, "teapot"},
		{418, strings.Repeat("x", 500), ExitUnexpected, "HTTP 418"},
	}
	for _, c := range cases {
		err := FromResponse(c.status, []byte(c.body), "prod")
		if ExitCode(err) != c.code || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%d %s: code %d msg %q", c.status, c.body[:min(len(c.body), 30)], ExitCode(err), err)
		}
	}
	if len(FromResponse(418, []byte(strings.Repeat("x", 500)), "p").Error()) > 400 {
		t.Error("raw body not truncated")
	}
}
