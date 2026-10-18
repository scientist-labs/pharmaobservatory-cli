package cmd

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/scientist-labs/pharmaobservatory-cli/internal/apierr"
)

// rawBody has a column and a key the typed SDK model does not know, odd spacing and a trailing
// newline: the output must be these bytes, never a re-marshal of a typed row.
const rawBody = "{\"rows\":[{\"unknown_column\":  1,\"b\":null}],\"extra\":{\"z\":[1,2]}}\n"

func loggedIn(t *testing.T) *env {
	t.Helper()
	e := newEnv(t)
	e.errBuf = &bytes.Buffer{}
	e.stdin = "the-code\n"
	if out, err := e.login("--no-browser"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	e.api = func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(rawBody))
	}
	return e
}

func (e *env) po(args ...string) (string, error) {
	return e.run(append(args, "--profile", "local")...)
}

func (e *env) lastSeen(t *testing.T) string {
	t.Helper()
	if len(e.seen) == 0 {
		t.Fatal("no request reached the server")
	}
	return e.seen[len(e.seen)-1]
}

func TestListFlagsReachTheQueryAndBodyIsRaw(t *testing.T) {
	e := loggedIn(t)
	out, err := e.po("biomarkers", "list", "--kind", "gene", "--limit", "5", "--facets", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if out != rawBody {
		t.Fatalf("not the raw body:\n%q", out)
	}
	if got := e.lastSeen(t); got != "/api/v1/biomarkers?facets=true&kind=gene&limit=5" {
		t.Fatalf("request: %s", got)
	}
	if want := "Bearer " + e.stored().AccessToken; e.apiAuth[0] != want {
		t.Fatal("bearer not sent from the stored credential")
	}
}

func TestUnsetFlagsAreNotSent(t *testing.T) {
	e := loggedIn(t)
	if _, err := e.po("cell-lines", "list", "--json"); err != nil {
		t.Fatal(err)
	}
	if got := e.lastSeen(t); got != "/api/v1/cell-lines" {
		t.Fatalf("request: %s", got)
	}
	// a zero value that was explicitly set is sent
	if _, err := e.po("cell-lines", "list", "--limit", "0", "--facets=false", "--json"); err != nil {
		t.Fatal(err)
	}
	if got := e.lastSeen(t); got != "/api/v1/cell-lines?facets=false&limit=0" {
		t.Fatalf("request: %s", got)
	}
}

func TestGetPassesIDVerbatimAndEncodesOnce(t *testing.T) {
	e := loggedIn(t)
	for id, want := range map[string]string{
		"EGFR":           "/api/v1/biomarkers/EGFR",
		"edgar:0001:x-1": "/api/v1/biomarkers/edgar:0001:x-1",
		"a b/c":          "/api/v1/biomarkers/a%20b%2Fc",
	} {
		out, err := e.po("biomarkers", "get", id, "--json")
		if err != nil || out != rawBody {
			t.Fatalf("%q: %v %q", id, err, out)
		}
		if got := e.lastSeen(t); got != want {
			t.Errorf("id %q: request %s want %s", id, got, want)
		}
	}
}

func TestGetSectionWithItsOwnFlag(t *testing.T) {
	e := loggedIn(t)
	for _, args := range [][]string{
		{"biomarkers", "get", "EGFR", "relations", "--chemicals", "gefitinib", "--json"},
		{"--json", "biomarkers", "get", "EGFR", "relations", "--chemicals=gefitinib"},
	} {
		out, err := e.po(args...)
		if err != nil || out != rawBody {
			t.Fatalf("%v: %v %q", args, err, out)
		}
		if got := e.lastSeen(t); got != "/api/v1/biomarkers/EGFR/relations?chemicals=gefitinib" {
			t.Fatalf("request: %s", got)
		}
	}
	if _, err := e.po("orgs", "get", "company:42", "identity", "--json"); err != nil {
		t.Fatal(err)
	}
	if got := e.lastSeen(t); got != "/api/v1/orgs/company:42/identity" {
		t.Fatalf("request: %s", got)
	}
}

func TestCollectionSections(t *testing.T) {
	e := loggedIn(t)
	if _, err := e.po("biomarkers", "hub_tier_tables", "--json"); err != nil {
		t.Fatal(err)
	}
	if got := e.lastSeen(t); got != "/api/v1/biomarkers/hub_tier_tables" {
		t.Fatalf("request: %s", got)
	}
	if _, err := e.po("cell-lines", "status_counts", "--species", "human", "--json"); err != nil {
		t.Fatal(err)
	}
	if got := e.lastSeen(t); got != "/api/v1/cell-lines/status_counts?species=human" {
		t.Fatalf("request: %s", got)
	}
}

func TestUnknownSectionFailsLocallyWithValidKeys(t *testing.T) {
	e := loggedIn(t)
	n := len(e.seen)
	_, err := e.po("biomarkers", "get", "EGFR", "nonsense")
	if err == nil || apierr.ExitCode(err) != apierr.ExitUsage {
		t.Fatalf("%v", err)
	}
	if !strings.Contains(err.Error(), `unknown section "nonsense"`) || !strings.Contains(err.Error(), "evidence_over_time") {
		t.Fatalf("message: %v", err)
	}
	if len(e.seen) != n {
		t.Fatal("a request was sent before the local check")
	}
}

func TestGetUsageErrors(t *testing.T) {
	e := loggedIn(t)
	for _, args := range [][]string{
		{"biomarkers", "get"},
		{"biomarkers", "get", "a", "relations", "extra"},
		{"biomarkers", "get", "EGFR", "relations", "--no-such-flag"},
		{"biomarkers", "list", "extra"},
		{"biomarkers", "nonsense"},
		{"biomarkers", "list", "--limit", "abc"},
	} {
		n := len(e.seen)
		_, err := e.po(args...)
		if apierr.ExitCode(err) != apierr.ExitUsage {
			t.Errorf("%v: exit %d (%v)", args, apierr.ExitCode(err), err)
		}
		if len(e.seen) != n {
			t.Errorf("%v: reached the network", args)
		}
	}
}

func TestHelpTextComesFromTheSpec(t *testing.T) {
	e := loggedIn(t)
	out, err := e.po("biomarkers", "--help")
	if err != nil || !strings.Contains(out, "evidence_over_time") || !strings.Contains(out, "hub_tier_tables") || !strings.Contains(out, "Biomarker evidence, labeled by type") {
		t.Fatalf("%v\n%s", err, out)
	}
	out, err = e.po("biomarkers", "list", "--help")
	if err != nil || !strings.Contains(out, "--kind") || !strings.Contains(out, "--limit int32") {
		t.Fatalf("%v\n%s", err, out)
	}
	out, err = e.po("biomarkers", "get", "--help")
	if err != nil || !strings.Contains(out, "get <id> [section]") {
		t.Fatalf("%v\n%s", err, out)
	}
	out, err = e.po("biomarkers", "get", "EGFR", "relations", "--help")
	if err != nil || !strings.Contains(out, "--chemicals") || !strings.Contains(out, "GET /api/v1/biomarkers/{id}/relations") {
		t.Fatalf("%v\n%s", err, out)
	}
	if out, err = e.po("biomarkers"); err != nil || !strings.Contains(out, "Available Commands") {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(e.seen) != 0 {
		t.Fatal("help reached the network")
	}
}

func TestNotLoggedInExitsThree(t *testing.T) {
	e := loggedIn(t)
	if _, err := e.po("logout"); err != nil {
		t.Fatal(err)
	}
	e.seen = nil
	for _, args := range [][]string{
		{"biomarkers", "list", "--json"},
		{"orgs", "get", "company:1", "identity"},
		{"cell-lines", "status_counts"},
		{"api", "biomarkers"},
	} {
		out, err := e.po(args...)
		if apierr.ExitCode(err) != apierr.ExitAuth || !strings.Contains(err.Error(), "not logged in to local; run: po login --profile local") {
			t.Errorf("%v: %v", args, err)
		}
		if out != "" {
			t.Errorf("%v: output %q", args, out)
		}
	}
	if len(e.seen) != 0 {
		t.Fatal("a request was sent without a credential")
	}
}

func TestServerErrorsMapAndJSONKeepsTheBody(t *testing.T) {
	e := loggedIn(t)
	e.api = func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(403)
		_, _ = w.Write([]byte(`{"error":"missing_role","role":"example:read"}`))
	}
	out, err := e.po("cell-lines", "list", "--json")
	if apierr.ExitCode(err) != apierr.ExitForbidden || !strings.Contains(err.Error(), "example:read") {
		t.Fatalf("%v", err)
	}
	if !strings.Contains(out, `"missing_role"`) {
		t.Fatalf("--json must print the server body: %q", out)
	}
	if out, _ = e.po("cell-lines", "list"); strings.Contains(out, `{"error"`) {
		t.Fatalf("the body of an error is not printed without --json: %q", out)
	}
	e.api = func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(502) }
	if _, err = e.po("cell-lines", "list"); apierr.ExitCode(err) != apierr.ExitUnavail {
		t.Fatalf("%v", err)
	}
}

func TestExpiredTokenRefreshesOnceThroughTheTypedPath(t *testing.T) {
	e := loggedIn(t)
	calls := 0
	e.api = func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"error":"expired_token"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(rawBody))
	}
	out, err := e.po("cell-lines", "list", "--json")
	if err != nil || out != rawBody {
		t.Fatalf("%v %q", err, out)
	}
	if calls != 2 || e.apiAuth[0] == e.apiAuth[1] || len(e.forms) != 2 {
		t.Fatalf("calls=%d tokens differ=%v refreshes=%d", calls, e.apiAuth[0] != e.apiAuth[1], len(e.forms)-1)
	}
}

func TestInvalidAudienceIsNeverRetried(t *testing.T) {
	e := loggedIn(t)
	e.api = func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"error":"invalid_audience"}`))
	}
	_, err := e.po("cell-lines", "list")
	if apierr.ExitCode(err) != apierr.ExitAuth || !strings.Contains(err.Error(), "invalid_audience") {
		t.Fatalf("%v", err)
	}
	if len(e.seen) != 1 || len(e.forms) != 1 {
		t.Fatalf("seen %d, token calls %d", len(e.seen), len(e.forms))
	}
}

func TestTypedDecodeFailureWarnsAndStillPrintsTheRawBody(t *testing.T) {
	e := loggedIn(t)
	e.api = func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[1,2]`))
	}
	e.errBuf.Reset()
	out, err := e.po("cell-lines", "list", "--json")
	if err != nil {
		t.Fatalf("a decode failure with a rendered body must exit 0: %v", err)
	}
	if out != "[1,2]\n" {
		t.Fatalf("stdout must stay the raw body: %q", out)
	}
	if w := e.errBuf.String(); !strings.Contains(w, "warning: the SDK (v0.1.2)") || !strings.Contains(w, "SDK bug") {
		t.Fatalf("stderr: %q", w)
	}
}

func TestUnreachableServerIsClassSeven(t *testing.T) {
	e := loggedIn(t)
	e.srv.Close()
	_, err := e.po("cell-lines", "list")
	if apierr.ExitCode(err) != apierr.ExitUnavail || !strings.Contains(err.Error(), "cannot reach") {
		t.Fatalf("%v", err)
	}
}

func TestAPICommand(t *testing.T) {
	e := loggedIn(t)
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"api", "biomarkers"}, "/api/v1/biomarkers"},
		{[]string{"api", "biomarkers/EGFR/identity"}, "/api/v1/biomarkers/EGFR/identity"},
		{[]string{"api", "/api/v1/cell-lines/status_counts"}, "/api/v1/cell-lines/status_counts"},
		{[]string{"api", "biomarkers", "-F", "kind=gene", "-F", "q=a b&c", "--field", "limit=5"}, "/api/v1/biomarkers?kind=gene&limit=5&q=a+b%26c"},
		{[]string{"api", "biomarkers?dir=asc", "-F", "limit=2"}, "/api/v1/biomarkers?dir=asc&limit=2"},
	} {
		out, err := e.po(c.args...)
		if err != nil || out != rawBody {
			t.Fatalf("%v: %v %q", c.args, err, out)
		}
		if got := e.lastSeen(t); got != c.want {
			t.Errorf("%v: request %s want %s", c.args, got, c.want)
		}
	}
	if want := "Bearer " + e.stored().AccessToken; e.apiAuth[0] != want {
		t.Fatal("bearer not sent")
	}
}

func TestAPICommandRefusesBadInput(t *testing.T) {
	e := loggedIn(t)
	for _, args := range [][]string{
		{"api"},
		{"api", "a", "b"},
		{"api", "https://elsewhere.example/api/v1/whoami"},
		{"api", "biomarkers", "-F", "novalue"},
		{"api", "biomarkers", "-F", "=v"},
	} {
		_, err := e.po(args...)
		if apierr.ExitCode(err) != apierr.ExitUsage {
			t.Errorf("%v: exit %d (%v)", args, apierr.ExitCode(err), err)
		}
	}
	if len(e.seen) != 0 {
		t.Fatal("a refused call reached the server")
	}
}

func TestAPICommandErrorKeepsBodyAndExit(t *testing.T) {
	e := loggedIn(t)
	e.api = func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(403)
		_, _ = w.Write([]byte(`{"error":"access_pending"}`))
	}
	out, err := e.po("api", "biomarkers")
	if apierr.ExitCode(err) != apierr.ExitForbidden || !strings.Contains(out, "access_pending") {
		t.Fatalf("%v %q", err, out)
	}
}
