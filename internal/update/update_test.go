package update

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type fixture struct {
	t     *testing.T
	hits  atomic.Int32
	tag   string
	code  int
	srv   *httptest.Server
	clock time.Time
	dir   string
	env   map[string]string
	out   bytes.Buffer
}

func newFixture(t *testing.T) *fixture {
	f := &fixture{t: t, tag: "v0.1.4", code: 200, clock: time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC), dir: filepath.Join(t.TempDir(), "pharma"), env: map[string]string{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f.hits.Add(1)
		w.WriteHeader(f.code)
		_, _ = w.Write([]byte(`{"tag_name":"` + f.tag + `"}`))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fixture) notifier(current string, tty bool) *Notifier {
	return &Notifier{
		HC: f.srv.Client(), Now: func() time.Time { return f.clock }, URL: f.srv.URL,
		Dir: func() (string, error) { return f.dir, nil }, Current: current,
		Getenv: func(k string) string { return f.env[k] }, IsTTY: tty, Exe: "/usr/local/bin/pharma", Stderr: &f.out,
	}
}

// settle waits for the background check to answer, then prints.
func settle(n *Notifier) {
	n.Start(context.Background())
	deadline := time.After(2 * time.Second)
	for len(n.result) == 0 {
		select {
		case <-deadline:
			return
		case <-time.After(time.Millisecond):
		}
	}
	n.Finish()
}

func TestNoticeShownForNewerRelease(t *testing.T) {
	f := newFixture(t)
	settle(f.notifier("0.1.1", true))
	want := "A new release of pharma is available: 0.1.1 -> 0.1.4 (upgrade: https://pharmaobservatory.com/cli)\n"
	if f.out.String() != want {
		t.Fatalf("got %q want %q", f.out.String(), want)
	}
	if strings.Count(f.out.String(), "\n") != 1 {
		t.Fatal("the notice is one line")
	}
}

func TestNoNoticeWhenCurrent(t *testing.T) {
	for _, cur := range []string{"0.1.4", "0.2.0", "v0.1.4"} {
		f := newFixture(t)
		settle(f.notifier(cur, true))
		if f.out.Len() != 0 {
			t.Fatalf("%s: unexpected %q", cur, f.out.String())
		}
	}
}

func TestSuppressedCases(t *testing.T) {
	cases := map[string]func(f *fixture) *Notifier{
		"not a tty":  func(f *fixture) *Notifier { return f.notifier("0.1.1", false) },
		"CI":         func(f *fixture) *Notifier { f.env["CI"] = "true"; return f.notifier("0.1.1", true) },
		"opt out":    func(f *fixture) *Notifier { f.env[OptOutEnv] = "1"; return f.notifier("0.1.1", true) },
		"dev build":  func(f *fixture) *Notifier { return f.notifier("dev", true) },
		"snapshot":   func(f *fixture) *Notifier { return f.notifier("0.1.1-SNAPSHOT-abc", true) },
		"prerelease": func(f *fixture) *Notifier { return f.notifier("0.1.1-rc1", true) },
	}
	for name, mk := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			n := mk(f)
			settle(n)
			if f.out.Len() != 0 {
				t.Fatalf("printed %q", f.out.String())
			}
			if f.hits.Load() != 0 {
				t.Fatal("a suppressed run must not touch the network")
			}
			if _, err := os.Stat(filepath.Join(f.dir, cacheFile)); err == nil {
				t.Fatal("a suppressed run must not write the cache")
			}
		})
	}
}

func TestCacheHonouredForADay(t *testing.T) {
	f := newFixture(t)
	settle(f.notifier("0.1.1", true))
	if f.hits.Load() != 1 {
		t.Fatalf("first run hits %d", f.hits.Load())
	}
	// 23h later: cached answer, no request, notice still shown.
	f.clock = f.clock.Add(23 * time.Hour)
	f.out.Reset()
	settle(f.notifier("0.1.1", true))
	if f.hits.Load() != 1 || !strings.Contains(f.out.String(), "0.1.1 -> 0.1.4") {
		t.Fatalf("cache not honoured: hits %d out %q", f.hits.Load(), f.out.String())
	}
	// 25h after the check: asked again, and it sees the newer tag.
	f.clock = f.clock.Add(2 * time.Hour)
	f.tag = "v0.1.9"
	f.out.Reset()
	settle(f.notifier("0.1.1", true))
	if f.hits.Load() != 2 || !strings.Contains(f.out.String(), "0.1.1 -> 0.1.9") {
		t.Fatalf("stale cache not refreshed: hits %d out %q", f.hits.Load(), f.out.String())
	}
}

func TestClockSkewedCacheIsRefreshed(t *testing.T) {
	f := newFixture(t)
	settle(f.notifier("0.1.1", true))
	f.clock = f.clock.Add(-48 * time.Hour)
	settle(f.notifier("0.1.1", true))
	if f.hits.Load() != 2 {
		t.Fatalf("a cache dated in the future must not be trusted, hits %d", f.hits.Load())
	}
}

func TestNetworkFailureIsSilentAndNotRetriedForADay(t *testing.T) {
	f := newFixture(t)
	f.code = 500
	settle(f.notifier("0.1.1", true))
	if f.out.Len() != 0 {
		t.Fatalf("printed %q", f.out.String())
	}
	settle(f.notifier("0.1.1", true))
	if f.hits.Load() != 1 {
		t.Fatalf("a failed check is recorded: hits %d", f.hits.Load())
	}
	f.srv.Close()
	f.clock = f.clock.Add(25 * time.Hour)
	settle(f.notifier("0.1.1", true))
	if f.out.Len() != 0 {
		t.Fatalf("a dead server must be silent, got %q", f.out.String())
	}
}

func TestFailureKeepsLastKnownRelease(t *testing.T) {
	f := newFixture(t)
	settle(f.notifier("0.1.1", true))
	f.code = 500
	f.clock = f.clock.Add(25 * time.Hour)
	f.out.Reset()
	settle(f.notifier("0.1.1", true))
	if !strings.Contains(f.out.String(), "0.1.4") {
		t.Fatalf("got %q", f.out.String())
	}
}

func TestUnreadableAnswersAreSilent(t *testing.T) {
	for _, tag := range []string{"nightly", "v0.2.0-rc1", ""} {
		f := newFixture(t)
		f.tag = tag
		settle(f.notifier("0.1.1", true))
		if f.out.Len() != 0 {
			t.Fatalf("tag %q printed %q", tag, f.out.String())
		}
	}
}

func TestSlowServerNeverBlocksFinish(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release
		_, _ = w.Write([]byte(`{"tag_name":"v9.9.9"}`))
	}))
	defer srv.Close()
	f := newFixture(t)
	n := f.notifier("0.1.1", true)
	n.URL = srv.URL
	n.Start(context.Background())
	done := make(chan struct{})
	go func() { n.Finish(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Finish waited for the network")
	}
	if f.out.Len() != 0 {
		t.Fatalf("printed %q", f.out.String())
	}
	close(release)
	<-n.result // let the abandoned check finish before the temp dir goes
}

func TestNewer(t *testing.T) {
	yes := [][2]string{{"0.1.1", "0.1.4"}, {"0.1.9", "0.2.0"}, {"0.9.9", "1.0.0"}, {"v0.1.1", "v0.1.2"}, {"0.1.9", "0.1.10"}}
	for _, p := range yes {
		if !Newer(p[0], p[1]) {
			t.Fatalf("%v should be newer", p)
		}
	}
	no := [][2]string{{"0.1.4", "0.1.4"}, {"0.2.0", "0.1.9"}, {"dev", "0.1.4"}, {"0.1.1", "x"}, {"0.1.1-rc1", "0.1.4"}}
	for _, p := range no {
		if Newer(p[0], p[1]) {
			t.Fatalf("%v should not be newer", p)
		}
	}
}

func TestUpgradeHow(t *testing.T) {
	if got := UpgradeHow("/opt/homebrew/bin/pharma"); !strings.Contains(got, "brew upgrade scientist-labs/pharmaobservatory/pharma") {
		t.Fatalf("got %q", got)
	}
	if got := UpgradeHow("/usr/local/Cellar/pharma/0.1.1/bin/pharma"); !strings.Contains(got, "brew upgrade") {
		t.Fatalf("got %q", got)
	}
	if got := UpgradeHow("/home/linuxbrew/.linuxbrew/bin/pharma"); !strings.Contains(got, "brew upgrade") {
		t.Fatalf("got %q", got)
	}
	// A symlink out of the Homebrew prefix into the Cellar is brew-managed.
	dir := t.TempDir()
	cellar := filepath.Join(dir, "Cellar", "pharma", "0.1.1", "bin")
	if err := os.MkdirAll(cellar, 0o755); err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(cellar, "pharma")
	if err := os.WriteFile(real, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "pharma")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if got := UpgradeHow(link); !strings.Contains(got, "brew upgrade") {
		t.Fatalf("symlink: got %q", got)
	}
	for _, p := range []string{"/usr/local/bin/pharma", "/home/me/go/bin/pharma", ""} {
		if got := UpgradeHow(p); got != "upgrade: "+InstallURL {
			t.Fatalf("%q: got %q", p, got)
		}
	}
}

func TestCorruptCacheIsTreatedAsAbsentAndReplaced(t *testing.T) {
	f := newFixture(t)
	if err := os.MkdirAll(f.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(f.dir, cacheFile)
	if err := os.WriteFile(p, []byte(`{"checked_at":"2026-10-18T12:0`), 0o600); err != nil {
		t.Fatal(err)
	}
	settle(f.notifier("0.1.1", true))
	if f.hits.Load() != 1 || !strings.Contains(f.out.String(), "0.1.1 -> 0.1.4") {
		t.Fatalf("corrupt cache not treated as absent: hits %d out %q", f.hits.Load(), f.out.String())
	}
	// The rewrite is whole, and no temp file is left behind.
	if _, ok := f.notifier("0.1.1", true).readCache(); !ok {
		t.Fatal("cache not rewritten as valid JSON")
	}
	ents, _ := os.ReadDir(f.dir)
	if len(ents) != 1 {
		t.Fatalf("expected only the cache file, found %d entries", len(ents))
	}
}
