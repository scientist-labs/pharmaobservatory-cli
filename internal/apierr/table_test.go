package apierr

import (
	"os"
	"strings"
	"testing"
)

func TestReadmeCarriesTheGeneratedTable(t *testing.T) {
	b, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), TableMarkdown()) {
		t.Fatal("README exit table is stale: regenerate it with `exit-codes --markdown`")
	}
}

func TestRateLimitedWithoutBudgetKeepsTheWaitHint(t *testing.T) {
	err := FromResponse(429, map[string][]string{"Retry-After": {"5"}}, []byte(`{"error":"quota_exceeded"}`), "p")
	r, ok := err.(*RateLimited)
	if !ok || !r.HasRetryAfter || r.RetryAfter.Seconds() != 5 || ExitCode(err) != ExitRateLimited {
		t.Fatalf("%#v", err)
	}
}
