package cmd

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/scientist-labs/pharmaobservatory-cli/internal/apierr"
	"github.com/scientist-labs/pharmaobservatory-cli/internal/update"
)

func answer(status int, ctype, body string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		if ctype != "" {
			w.Header().Set("Content-Type", ctype)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

// TestRemovedRouteVersusMissingRecord pins the two 404 shapes of the server: a record or dataset it
// looked up (a JSON body with an error word or error_code) keeps exit 5, a path no route matches (Rails'
// exceptions app: generic JSON, an HTML page, a markdown page or nothing) is exit 10 with the upgrade hint.
func TestRemovedRouteVersusMissingRecord(t *testing.T) {
	record := []struct{ name, ctype, body string }{
		{"UNKNOWN_RECORD", "application/json", `{"error_code":"UNKNOWN_RECORD","message":"There is no company \"company:9\".","hint":"List the records with GET /api/v1/companies","docs":"d"}`},
		{"not_found", "application/json", `{"error":"not_found","message":"No record found."}`},
		{"unknown_page", "application/json", `{"error":"unknown_page","message":"Unknown page."}`},
		{"unknown_section", "application/json", `{"error":"unknown_section","message":"no section"}`},
	}
	for _, c := range record {
		t.Run("record/"+c.name, func(t *testing.T) {
			e := loggedIn(t)
			e.api = answer(404, c.ctype, c.body)
			out, err := e.pharma("companies", "get", "company:9")
			if got := apierr.ExitCode(err); got != apierr.ExitNotFound {
				t.Fatalf("exit %d want 5: %v %s", got, err, out)
			}
			if strings.Contains(err.Error(), "no longer exist") {
				t.Fatalf("a missing record must not hint at an upgrade: %v", err)
			}
		})
	}
	routes := []struct{ name, ctype, body string }{
		{"rails generic json", "application/json; charset=utf-8", `{"status":404,"error":"Not Found"}`},
		{"html page", "text/html", "<!doctype html><html><body>The page you were looking for doesn't exist.</body></html>"},
		{"markdown page", "text/markdown; charset=utf-8", "# Not found\n\nThere is no page at this address."},
		{"empty", "", ""},
	}
	for _, c := range routes {
		for _, args := range [][]string{{"companies", "get", "company:9"}, {"companies", "list"}, {"companies", "get", "company:9", "sponsor-trials"}, {"companies", "list", "--json"}} {
			t.Run("route/"+c.name+"/"+strings.Join(args, " "), func(t *testing.T) {
				e := loggedIn(t)
				e.exe = "/usr/local/bin/pharma"
				e.api = answer(404, c.ctype, c.body)
				_, err := e.pharma(args...)
				if got := apierr.ExitCode(err); got != apierr.ExitRemoved {
					t.Fatalf("exit %d want %d: %v", got, apierr.ExitRemoved, err)
				}
				for _, want := range []string{"may no longer exist", "GET /api/v1/companies", "upgrade: " + update.InstallURL} {
					if !strings.Contains(err.Error(), want) {
						t.Fatalf("missing %q in %v", want, err)
					}
				}
			})
		}
	}
}

func TestRemovedRouteHintNamesBrewWhenBrewInstalled(t *testing.T) {
	e := loggedIn(t)
	e.exe = "/opt/homebrew/bin/pharma"
	e.api = answer(404, "text/html", "<html>")
	_, err := e.pharma("companies", "list")
	if apierr.ExitCode(err) != apierr.ExitRemoved || !strings.Contains(err.Error(), "brew upgrade scientist-labs/pharmaobservatory/pharma") {
		t.Fatalf("got %v", err)
	}
}

func TestOtherStatusesAreNotRemoved(t *testing.T) {
	for _, st := range []int{400, 403, 410, 500} {
		e := loggedIn(t)
		e.api = answer(st, "text/html", "<html>")
		_, err := e.pharma("companies", "list")
		if apierr.ExitCode(err) == apierr.ExitRemoved {
			t.Fatalf("HTTP %d must not be class removed", st)
		}
	}
}

func TestRouteMissingTable(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   bool
	}{
		{404, `{"status":404,"error":"Not Found"}`, true},
		{404, ``, true},
		{404, `not json`, true},
		{404, `null`, true},
		{404, `[]`, true},
		{404, `{"error":"not_found"}`, false},
		{404, `{"error_code":"UNKNOWN_DATASET","message":"x"}`, false},
		{404, `{"message":"x"}`, false},
		{410, `{"status":404,"error":"Not Found"}`, false},
		{500, ``, false},
	}
	for _, c := range cases {
		if got := apierr.RouteMissing(c.status, []byte(c.body)); got != c.want {
			t.Errorf("%d %q: got %v want %v", c.status, c.body, got, c.want)
		}
	}
}

// noticeRun runs a command through ExecuteWith with a fake release endpoint and returns what the notifier
// wrote to stderr and how many times the release endpoint was asked.
func noticeRun(t *testing.T, e *env, current string, tty bool, args ...string) (stderr string, hits int) {
	t.Helper()
	var n atomic.Int32
	rel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n.Add(1)
		_, _ = w.Write([]byte(`{"tag_name":"v0.1.4"}`))
	}))
	t.Cleanup(rel.Close)
	var errw bytes.Buffer
	dir := t.TempDir()
	notifier := &update.Notifier{
		HC: rel.Client(), URL: rel.URL, Dir: func() (string, error) { return dir, nil }, Current: current,
		Now: func() time.Time { return e.clock }, Getenv: func(string) string { return "" }, IsTTY: tty, Exe: e.exe, Stderr: &errw,
	}
	d := Deps{HC: e.srv.Client(), Keyring: e.kr, Now: func() time.Time { return e.clock }, Stderr: &e.notice, Update: notifier, Exe: e.exe}
	// A real command takes a moment: hold the API answer until the background check has been made.
	inner := e.api
	e.api = func(w http.ResponseWriter, r *http.Request) {
		for i := 0; i < 400 && (n.Load() == 0 || i < 10); i++ {
			time.Sleep(5 * time.Millisecond)
		}
		inner(w, r)
	}
	var out bytes.Buffer
	_ = ExecuteWith(d, append(args, "--profile", "local"), &out)
	return errw.String(), int(n.Load())
}

func TestNoticeAfterCommand(t *testing.T) {
	e := loggedIn(t)
	got, hits := noticeRun(t, e, "0.1.1", true, "companies", "list")
	want := "A new release of pharma is available: 0.1.1 -> 0.1.4 (upgrade: https://pharmaobservatory.com/cli)\n"
	if got != want || hits != 1 {
		t.Fatalf("got %q hits %d", got, hits)
	}
}

func TestNoticeAlsoAfterAFailedCommand(t *testing.T) {
	e := loggedIn(t)
	e.api = answer(404, "text/html", "<html>")
	got, _ := noticeRun(t, e, "0.1.1", true, "companies", "list")
	if !strings.Contains(got, "0.1.1 -> 0.1.4") {
		t.Fatalf("got %q", got)
	}
}

func TestNoticeSuppressed(t *testing.T) {
	for name, c := range map[string]struct {
		tty  bool
		cur  string
		args []string
	}{
		"json":      {true, "0.1.1", []string{"companies", "list", "--json"}},
		"csv":       {true, "0.1.1", []string{"companies", "list", "--csv"}},
		"not a tty": {false, "0.1.1", []string{"companies", "list"}},
		"dev":       {true, "dev", []string{"companies", "list"}},
	} {
		t.Run(name, func(t *testing.T) {
			e := loggedIn(t)
			got, hits := noticeRun(t, e, c.cur, c.tty, c.args...)
			if got != "" || hits != 0 {
				t.Fatalf("got %q hits %d", got, hits)
			}
		})
	}
}
