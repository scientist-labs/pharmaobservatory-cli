// Package update is the passive new-release notice (the idiom of gh and npm): at most once a day it asks the
// GitHub releases API for the latest release, remembers the answer in the config dir, and after a command
// finishes it can say a newer one exists. It never blocks a command and a failure is silent.
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/scientist-labs/pharmaobservatory-cli/internal/apierr"
)

const (
	// LatestURL is the GitHub releases API answer for the newest non-prerelease, non-draft release.
	LatestURL = "https://api.github.com/repos/scientist-labs/pharmaobservatory-cli/releases/latest"
	// InstallURL is where the upgrade options are listed when the install method is not knowable.
	InstallURL = "https://pharmaobservatory.com/cli"
	// Interval is the shortest time between two checks.
	Interval = 24 * time.Hour
	// Timeout bounds the background request.
	Timeout   = 3 * time.Second
	cacheFile = "update-check.json"
	// OptOutEnv disables the notice when set to anything.
	OptOutEnv = "PHARMA_NO_UPDATE_NOTIFIER"
)

var semver = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)$`)

// parse reads a plain MAJOR.MINOR.PATCH (optionally v-prefixed). A prerelease, snapshot or dev
// version does not parse, which is how those builds opt out.
func parse(s string) ([3]int, bool) {
	var v [3]int
	m := semver.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return v, false
	}
	for i := range v {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return v, false
		}
		v[i] = n
	}
	return v, true
}

// Newer reports whether latest is a strictly greater release than current; both must be plain semver.
func Newer(current, latest string) bool {
	c, ok1 := parse(current)
	l, ok2 := parse(latest)
	if !ok1 || !ok2 {
		return false
	}
	for i := range c {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return false
}

// Notifier checks for a newer release. Every field is a seam; zero values mean the real thing.
type Notifier struct {
	HC      *http.Client
	Now     func() time.Time
	URL     string // LatestURL when empty
	Dir     func() (string, error)
	Current string
	Getenv  func(string) string
	IsTTY   bool // stderr is a terminal
	Exe     string
	Stderr  io.Writer

	result chan string
}

type cache struct {
	CheckedAt time.Time `json:"checked_at"`
	Latest    string    `json:"latest"`
}

func (n *Notifier) env(k string) string {
	if n.Getenv != nil {
		return n.Getenv(k)
	}
	return os.Getenv(k)
}

func (n *Notifier) now() time.Time {
	if n.Now != nil {
		return n.Now()
	}
	return time.Now()
}

// Enabled reports whether a notice may be shown at all: an interactive stderr, not CI, not opted out,
// and a release build (plain semver).
func (n *Notifier) Enabled() bool {
	if !n.IsTTY || n.env("CI") != "" || n.env(OptOutEnv) != "" {
		return false
	}
	_, ok := parse(n.Current)
	return ok
}

func (n *Notifier) path() (string, bool) {
	if n.Dir == nil {
		return "", false
	}
	d, err := n.Dir()
	if err != nil || d == "" {
		return "", false
	}
	return filepath.Join(d, cacheFile), true
}

func (n *Notifier) readCache() (cache, bool) {
	p, ok := n.path()
	if !ok {
		return cache{}, false
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return cache{}, false
	}
	var c cache
	if json.Unmarshal(b, &c) != nil {
		return cache{}, false
	}
	return c, true
}

func (n *Notifier) writeCache(c cache) {
	p, ok := n.path()
	if !ok {
		return
	}
	if os.MkdirAll(filepath.Dir(p), 0o700) != nil {
		return
	}
	b, err := json.Marshal(c)
	if err != nil {
		return
	}
	_ = os.WriteFile(p, b, 0o600)
}

// Start begins the check and returns at once. A fresh cache answers immediately without the network;
// a stale or missing one is refreshed in a goroutine. Call Finish after the command.
func (n *Notifier) Start(ctx context.Context) {
	n.result = make(chan string, 1)
	if !n.Enabled() {
		n.result <- ""
		return
	}
	c, have := n.readCache()
	if have && n.now().Sub(c.CheckedAt) < Interval && !c.CheckedAt.After(n.now()) {
		n.result <- c.Latest
		return
	}
	go func() {
		latest := c.Latest
		if l, err := n.fetch(ctx); err == nil {
			latest = l
		}
		// A failed check is recorded too, so an offline machine does not retry on every command.
		n.writeCache(cache{CheckedAt: n.now(), Latest: latest})
		n.result <- latest
	}()
}

func (n *Notifier) fetch(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	u := n.URL
	if u == "" {
		u = LatestURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	hc := n.HC
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var r struct {
		Tag string `json:"tag_name"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&r); err != nil {
		return "", err
	}
	if _, ok := parse(r.Tag); !ok {
		return "", fmt.Errorf("unreadable tag")
	}
	return strings.TrimPrefix(r.Tag, "v"), nil
}

// Finish prints the notice to Stderr when the check has already answered with a newer release. It never
// waits: a check still in flight is dropped.
func (n *Notifier) Finish() {
	if n.result == nil {
		return
	}
	select {
	case latest := <-n.result:
		if latest != "" && Newer(n.Current, latest) && n.Stderr != nil {
			_, _ = fmt.Fprintf(n.Stderr, "A new release of %s is available: %s -> %s (%s)\n", apierr.Bin, strings.TrimPrefix(n.Current, "v"), latest, UpgradeHow(n.Exe))
		}
	default:
	}
}

// brewMarkers are path fragments of a Homebrew-managed binary (the Cellar holds the real file behind the
// bin symlink on every prefix).
var brewMarkers = []string{"/Cellar/", "/opt/homebrew/", "/home/linuxbrew/.linuxbrew/", "/usr/local/Homebrew/"}

// UpgradeHow is how to upgrade the binary at exe: brew when it lives under a Homebrew prefix, else the
// install page. Only the resolved path is read, never run.
func UpgradeHow(exe string) string {
	p := filepath.ToSlash(exe)
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		p = filepath.ToSlash(r)
	}
	for _, m := range brewMarkers {
		if strings.Contains(p, m) {
			return "upgrade with: brew upgrade scientist-labs/pharmaobservatory/" + apierr.Bin
		}
	}
	return "upgrade: " + InstallURL
}
