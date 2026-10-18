package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/scientist-labs/pharmaobservatory-cli/internal/apierr"
)

const sinkHost = "sink.example.com"

func sinkURL(r *redirEnv) string { return "http://" + sinkHost + ":80" }

// redirEnv is an https origin whose named paths answer 307 to a plain-http sink that counts
// every request it receives. The client under test is the one the binary ships (defaultDeps)
// with only its trust roots swapped for the test server's.
type redirEnv struct {
	t      *testing.T
	srv    *httptest.Server
	sink   *httptest.Server
	hits   atomic.Int32
	bounce map[string]string // path -> redirect target
	clock  time.Time
	kr     *fakeKR
	tokens int
}

func newRedirEnv(t *testing.T) *redirEnv {
	r := &redirEnv{t: t, bounce: map[string]string{}, kr: &fakeKR{m: map[string]string{}}, clock: time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)}
	t.Setenv("PO_CONFIG_DIR", filepath.Join(t.TempDir(), "po"))
	t.Setenv("PO_CREDENTIAL_STORE", "")
	t.Setenv("PO_PROFILE", "")
	r.sink = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		r.hits.Add(1)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(r.sink.Close)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		if to, ok := r.bounce[req.URL.Path]; ok {
			http.Redirect(w, req, to, http.StatusTemporaryRedirect)
			return
		}
		switch req.URL.Path {
		case "/.well-known/oauth-protected-resource/api/v1", "/mirror":
			_ = json.NewEncoder(w).Encode(map[string]any{"authorization_servers": []string{r.srv.URL}, "scopes_supported": []string{"example:api"}})
		case "/.well-known/oauth-authorization-server":
			_ = json.NewEncoder(w).Encode(map[string]any{"authorization_endpoint": r.srv.URL + "/authorize", "token_endpoint": r.srv.URL + "/token"})
		case "/token":
			_ = req.ParseForm()
			r.tokens++
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": jwtFor(req.PostForm.Get("resource"), "user_1", r.tokens), "refresh_token": "rt", "expires_in": 300})
		case "/api/v1/whoami", "/api/v1/whoami-final":
			_, _ = w.Write([]byte(`{"ok":true,"sub":"user_1","roles":[]}`))
		default:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true}`))
		}
	})
	r.srv = httptest.NewTLSServer(mux)
	t.Cleanup(r.srv.Close)
	return r
}

func (r *redirEnv) run(stdin string, args ...string) (string, error) {
	d := defaultDeps()
	tr := r.srv.Client().Transport.(*http.Transport).Clone()
	dial := (&net.Dialer{}).DialContext
	sinkAddr := strings.TrimPrefix(r.sink.URL, "http://")
	// sinkHost is not a loopback literal, so RequireSecure must refuse it; the dialer still
	// lands it on the local sink so a leak is observable.
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if strings.HasPrefix(addr, sinkHost+":") {
			addr = sinkAddr
		}
		return dial(ctx, network, addr)
	}
	d.HC.Transport = tr
	d.Keyring, d.Stdin, d.Now = r.kr, strings.NewReader(stdin), func() time.Time { return r.clock }
	d.OpenBrowser = func(string) error { return nil }
	d.CallbackWait = 5 * time.Second
	root := NewRootWith(d)
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func (r *redirEnv) login() (string, error) {
	return r.run("c\n", "login", "--profile", "local", "--origin", r.srv.URL, "--client-id", "client_t", "--no-browser")
}

func (r *redirEnv) refused(out string, err error) {
	r.t.Helper()
	if apierr.ExitCode(err) != apierr.ExitUsage || !strings.Contains(err.Error(), "must be https") {
		r.t.Fatalf("want exit 2 naming https, got %v\n%s", err, out)
	}
	if n := r.hits.Load(); n != 0 {
		r.t.Fatalf("the http hop was reached %d time(s)", n)
	}
}

func TestRedirectToHTTPIsRefusedEverywhere(t *testing.T) {
	t.Run("discovery GET", func(t *testing.T) {
		r := newRedirEnv(t)
		r.bounce["/.well-known/oauth-protected-resource/api/v1"] = sinkURL(r) + "/x"
		r.refused(r.login())
	})
	t.Run("token POST on code exchange", func(t *testing.T) {
		r := newRedirEnv(t)
		r.bounce["/token"] = sinkURL(r) + "/x"
		r.refused(r.login())
	})
	t.Run("token POST on refresh", func(t *testing.T) {
		r := newRedirEnv(t)
		if out, err := r.login(); err != nil {
			t.Fatalf("%v %s", err, out)
		}
		r.clock = r.clock.Add(time.Hour)
		r.bounce["/token"] = sinkURL(r) + "/x"
		r.refused(r.run("", "whoami", "--profile", "local"))
	})
	t.Run("API GET with bearer, whoami path", func(t *testing.T) {
		r := newRedirEnv(t)
		if out, err := r.login(); err != nil {
			t.Fatalf("%v %s", err, out)
		}
		r.bounce["/api/v1/whoami"] = sinkURL(r) + "/x"
		r.refused(r.run("", "whoami", "--profile", "local"))
	})
	t.Run("API GET with bearer, typed SDK path", func(t *testing.T) {
		r := newRedirEnv(t)
		if out, err := r.login(); err != nil {
			t.Fatalf("%v %s", err, out)
		}
		r.bounce["/api/v1/biomarkers"] = sinkURL(r) + "/x"
		r.refused(r.run("", "biomarkers", "list", "--profile", "local"))
	})
	t.Run("API GET with bearer, api command", func(t *testing.T) {
		r := newRedirEnv(t)
		if out, err := r.login(); err != nil {
			t.Fatalf("%v %s", err, out)
		}
		r.bounce["/api/v1/biomarkers"] = sinkURL(r) + "/x"
		r.refused(r.run("", "api", "/api/v1/biomarkers", "--profile", "local"))
	})
}

func TestRedirectHTTPSToHTTPSStillWorks(t *testing.T) {
	r := newRedirEnv(t)
	r.bounce["/.well-known/oauth-protected-resource/api/v1"] = r.srv.URL + "/mirror"
	if out, err := r.login(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	r.bounce["/api/v1/whoami"] = r.srv.URL + "/api/v1/whoami-final"
	out, err := r.run("", "whoami", "--profile", "local")
	if err != nil || !strings.Contains(out, "user_1") {
		t.Fatalf("%v %s", err, out)
	}
	r.bounce["/api/v1/biomarkers"] = r.srv.URL + "/api/v1/whoami-final"
	if out, err := r.run("", "biomarkers", "list", "--profile", "local"); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	if r.hits.Load() != 0 {
		t.Fatal("sink reached")
	}
}
