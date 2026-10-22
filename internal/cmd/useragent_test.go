package cmd

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/scientist-labs/pharmaobservatory-cli/internal/update"
)

// serverCLIRegex is the server's CLI-traffic pattern (explore/app/models/api_usage_analytics.rb in
// scientist-hq/pharmaobservatory). A request whose User-Agent misses it is logged as client=http.
var serverCLIRegex = regexp.MustCompile(`\Apharma/(?P<version>[\w.+-]{1,40})(?:\s|\z)`)

// Every request the CLI makes, through the real Deps wiring: discovery and token (login), the raw
// Session.Get behind whoami, the generated SDK behind a page command, and the release check (which
// --json suppresses, so whoami runs without it).
func TestEveryRequestCarriesTheCLIUserAgent(t *testing.T) {
	old := version
	version = "9.8.7"
	t.Cleanup(func() { version = old })

	e := loggedIn(t)
	var mu sync.Mutex
	var relUAs []string
	rel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		relUAs = append(relUAs, r.Header.Get("User-Agent"))
		mu.Unlock()
		_, _ = w.Write([]byte(`{"tag_name":"v9.8.7"}`))
	}))
	t.Cleanup(rel.Close)
	dir := t.TempDir()
	var errw bytes.Buffer
	n := &update.Notifier{
		HC: rel.Client(), URL: rel.URL, Dir: func() (string, error) { return dir, nil }, Current: version,
		Now: func() time.Time { return e.clock }, Getenv: func(string) string { return "" }, IsTTY: true, Exe: e.exe, Stderr: &errw,
	}
	d := Deps{HC: e.srv.Client(), Keyring: e.kr, Now: func() time.Time { return e.clock }, Stderr: &e.notice, Update: n, Exe: e.exe}
	for _, args := range [][]string{{"whoami"}, {"companies", "list", "--json"}} {
		var out bytes.Buffer
		if err := ExecuteWith(d, append(args, "--profile", "local"), &out); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out.String())
		}
	}
	if e.whoamis == 0 || len(e.seen) == 0 {
		t.Fatalf("whoami %d, api %v: the commands did not reach the server", e.whoamis, e.seen)
	}
	mu.Lock()
	all := append(append([]string{}, e.uas...), relUAs...)
	mu.Unlock()
	if len(relUAs) == 0 {
		t.Fatal("the release check never ran")
	}
	want := userAgent()
	for i, ua := range all {
		m := serverCLIRegex.FindStringSubmatch(ua)
		if ua != want || m == nil || m[1] != "9.8.7" {
			t.Errorf("request %d: User-Agent %q, want %q matching the server's CLI regex", i, ua, want)
		}
	}
}
