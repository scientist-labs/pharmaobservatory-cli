package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/scientist-labs/pharmaobservatory-cli/internal/apierr"
	"github.com/scientist-labs/pharmaobservatory-cli/internal/auth/store"
)

func TestHTTPCustomOriginRefused(t *testing.T) {
	e := newEnv(t)
	out, err := e.run("login", "--profile", "remote", "--origin", "http://pharma.example.com", "--no-browser")
	if apierr.ExitCode(err) != apierr.ExitUsage || !strings.Contains(err.Error(), "must be https") {
		t.Fatalf("%v %s", err, out)
	}
	if len(e.forms) != 0 {
		t.Fatal("a token request was made")
	}
	// Loopback http, the shape every other test uses, stays allowed.
	e.stdin = "c\n"
	if out, err := e.login("--no-browser"); err != nil {
		t.Fatalf("%v %s", err, out)
	}
}

func TestHTTPAPIBaseRefused(t *testing.T) {
	e := loggedIn(t)
	_, err := e.pharma("whoami", "--api-base", "http://evil.example.com")
	if apierr.ExitCode(err) != apierr.ExitUsage {
		t.Fatalf("%v", err)
	}
	if e.whoamis != 0 {
		t.Fatal("request sent")
	}
}

func TestLoginRefusesHTTPTokenEndpointFromDiscovery(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource/api/v1":
			_ = json.NewEncoder(w).Encode(map[string]any{"authorization_servers": []string{srv.URL}})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"authorization_endpoint": srv.URL + "/authorize", "token_endpoint": "http://tokens.example.com/token"})
		}
	}))
	defer srv.Close()
	e := newEnv(t)
	e.srv = srv
	e.stdin = "c\n"
	out, err := e.login("--no-browser")
	if apierr.ExitCode(err) != apierr.ExitUsage || !strings.Contains(err.Error(), "token endpoint") || !strings.Contains(err.Error(), "must be https") {
		t.Fatalf("%v %s", err, out)
	}
	if strings.Contains(out, "Open this URL") {
		t.Fatal("login proceeded past discovery")
	}
}

func TestLoginPrintsSanitizedCallbackError(t *testing.T) {
	e := newEnv(t)
	e.stdin = "http://127.0.0.1:53682/callback?error=access_denied&error_description=nope%1B%5B2J%07%0Aline2\n"
	_, err := e.login("--no-browser")
	if apierr.ExitCode(err) != apierr.ExitAuth || strings.ContainsAny(err.Error(), "\x1b\x07\n") || !strings.Contains(err.Error(), "nope line2") {
		t.Fatalf("%q", err)
	}
}

func TestTokenErrorDescriptionPrintedSanitized(t *testing.T) {
	e := loggedIn(t)
	e.clock = e.clock.Add(time.Hour) // force a refresh
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"revoked\u001b[31m!\u0007"}`))
			return
		}
		e.srv.Config.Handler.ServeHTTP(w, r)
	}))
	defer srv.Close()
	// Point discovery's token endpoint at the failing handler by wrapping the transport.
	hc := &http.Client{Transport: rewrite{to: srv.URL, from: e.srv.URL + "/token"}}
	_, err := runWith(e, hc, "whoami", "--profile", "local")
	if err == nil || strings.ContainsAny(err.Error(), "\x1b\x07") || !strings.Contains(err.Error(), "revoked!") {
		t.Fatalf("%q", err)
	}
}

// rewrite sends requests for one URL elsewhere, so a test can swap a single endpoint.
type rewrite struct{ to, from string }

func (r rewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.String() == r.from {
		u2 := *req.URL
		t, _ := http.NewRequest(req.Method, r.to+u2.Path, req.Body)
		t.Header = req.Header
		return http.DefaultTransport.RoundTrip(t)
	}
	return http.DefaultTransport.RoundTrip(req)
}

func runWith(e *env, hc *http.Client, args ...string) (string, error) {
	d := Deps{HC: hc, Keyring: e.kr, Stdin: strings.NewReader(""), Now: func() time.Time { return e.clock }}
	root := NewRootWith(d)
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func TestLogoutClearsKeychainAndFile(t *testing.T) {
	e := newEnv(t)
	e.stdin = "c\n"
	if out, err := e.login("--no-browser"); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	cred := e.stored()
	// A stale copy in the file fallback, as after a login that once fell back.
	fs := store.FileStore{Dir: os.Getenv("PHARMA_CONFIG_DIR")}
	if err := fs.Set("local", cred); err != nil {
		t.Fatal(err)
	}
	if err := fs.Set("other", cred); err != nil {
		t.Fatal(err)
	}
	out, err := e.run("logout", "--profile", "local")
	if err != nil || !strings.Contains(out, "removed from the keychain and the file") {
		t.Fatalf("%v %s", err, out)
	}
	if _, err := (store.KeychainStore{KR: e.kr}).Get("local"); err != store.ErrNotFound {
		t.Fatalf("keychain entry remains: %v", err)
	}
	if _, err := fs.Get("local"); err != store.ErrNotFound {
		t.Fatalf("file entry remains: %v", err)
	}
	if _, err := fs.Get("other"); err != nil {
		t.Fatalf("logout removed another profile: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(os.Getenv("PHARMA_CONFIG_DIR"), "credentials.json")); strings.Contains(string(b), cred.RefreshToken) && !strings.Contains(string(b), "other") {
		t.Fatal("file lost the other profile")
	}
	out, err = e.run("logout", "--profile", "local")
	if err != nil || !strings.Contains(out, "nothing to remove") {
		t.Fatalf("%v %s", err, out)
	}
}

func TestLogoutFileOnlyWhenKeychainBroken(t *testing.T) {
	e := newEnv(t)
	e.stdin = "c\n"
	if _, err := e.login("--no-browser"); err != nil {
		t.Fatal(err)
	}
	fs := store.FileStore{Dir: os.Getenv("PHARMA_CONFIG_DIR")}
	if err := fs.Set("local", e.stored()); err != nil {
		t.Fatal(err)
	}
	e.kr.err = io.ErrClosedPipe // keychain unusable now
	out, err := e.run("logout", "--profile", "local")
	if err != nil || !strings.Contains(out, "removed from the file") {
		t.Fatalf("%v %s", err, out)
	}
	if _, err := fs.Get("local"); err != store.ErrNotFound {
		t.Fatal("file entry remains")
	}
}

// capture is a transport that never touches the network: it records the host and path of each
// request and answers 200.
type capture struct{ hosts, paths []string }

func (c *capture) RoundTrip(req *http.Request) (*http.Response, error) {
	c.hosts = append(c.hosts, req.URL.Host)
	c.paths = append(c.paths, req.URL.EscapedPath())
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true}`)), Header: http.Header{}, Request: req}, nil
}

func TestAPIPathTricksKeepTheProfileHost(t *testing.T) {
	e := loggedIn(t)
	host := strings.TrimPrefix(e.srv.URL, "http://")
	for _, arg := range []string{"//evil.com/x", `/\evil.com`, "/api/v1/../../x", "/@evil.com/x"} {
		c := &capture{}
		_, err := runWith(e, &http.Client{Transport: c}, "api", arg, "--profile", "local")
		if err != nil {
			t.Errorf("%q: %v", arg, err)
			continue
		}
		t.Logf("%q -> %v %v", arg, c.hosts, c.paths)
		if len(c.hosts) != 1 || c.hosts[0] != host {
			t.Errorf("%q: sent to %v, want only %s", arg, c.hosts, host)
		}
	}
}

func TestLogoutFileStoreLeavesTheKeychainAlone(t *testing.T) {
	e := newEnv(t)
	e.stdin = "c\n"
	if out, err := e.login("--no-browser"); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	cred := e.stored()
	fs := store.FileStore{Dir: os.Getenv("PHARMA_CONFIG_DIR")}
	if err := fs.Set("local", cred); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PHARMA_CREDENTIAL_STORE", "file")
	if out, err := e.run("logout", "--profile", "local"); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	if _, err := fs.Get("local"); err != store.ErrNotFound {
		t.Fatalf("file entry remains: %v", err)
	}
	if _, err := (store.KeychainStore{KR: e.kr}).Get("local"); err != nil {
		t.Fatalf("file-mode logout touched the keychain: %v", err)
	}
}

func TestLogoutLeavesAnotherProfileAlone(t *testing.T) {
	e := newEnv(t)
	e.stdin = "c\n"
	if out, err := e.login("--no-browser"); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	cred := e.stored()
	kc := store.KeychainStore{KR: e.kr}
	if err := kc.Set("other", cred); err != nil {
		t.Fatal(err)
	}
	if out, err := e.run("logout", "--profile", "local"); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	if _, err := kc.Get("local"); err != store.ErrNotFound {
		t.Fatalf("local remains: %v", err)
	}
	if _, err := kc.Get("other"); err != nil {
		t.Fatalf("logout removed another profile's keychain entry: %v", err)
	}
}
