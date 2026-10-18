package cmd

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zalando/go-keyring"

	"github.com/scientist-labs/pharmaobservatory-cli/internal/apierr"
	"github.com/scientist-labs/pharmaobservatory-cli/internal/auth/store"
)

type fakeKR struct {
	mu  sync.Mutex
	m   map[string]string
	err error
}

func (f *fakeKR) Get(s, u string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return "", f.err
	}
	v, ok := f.m[s+"/"+u]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return v, nil
}
func (f *fakeKR) Set(s, u, p string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.m[s+"/"+u] = p
	return nil
}
func (f *fakeKR) Delete(s, u string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.m[s+"/"+u]; !ok {
		return keyring.ErrNotFound
	}
	delete(f.m, s+"/"+u)
	return nil
}

func jwtFor(aud string, sub string, n int) string {
	b, _ := json.Marshal(map[string]any{"aud": aud, "sub": sub, "n": n})
	return "h." + base64.RawURLEncoding.EncodeToString(b) + ".s"
}

// env is a fake origin: discovery, token endpoint and /api/v1/whoami on one server.
type env struct {
	t       *testing.T
	srv     *httptest.Server
	kr      *fakeKR
	clock   time.Time
	forms   []url.Values
	whoami  func(w http.ResponseWriter, r *http.Request)
	whoamis int
	tokenN  int
	aud     string   // when set, minted tokens carry this audience
	refuse  bool     // when set, the token endpoint answers 400 invalid_grant
	authz   []string // Authorization headers seen by whoami
	stdin   string
	opener  func(authURL string) error
}

func newEnv(t *testing.T) *env {
	e := &env{t: t, kr: &fakeKR{m: map[string]string{}}, clock: time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)}
	t.Setenv("PO_CONFIG_DIR", filepath.Join(t.TempDir(), "po"))
	t.Setenv("PO_CREDENTIAL_STORE", "")
	t.Setenv("PO_PROFILE", "")
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/oauth-protected-resource/api/v1", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"authorization_servers": []string{e.srv.URL}, "scopes_supported": []string{"example:api"}})
	})
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"authorization_endpoint": e.srv.URL + "/authorize", "token_endpoint": e.srv.URL + "/token"})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		e.forms = append(e.forms, r.PostForm)
		if e.refuse {
			w.WriteHeader(400)
			w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		e.tokenN++
		aud := r.PostForm.Get("resource")
		if e.aud != "" {
			aud = e.aud
		}
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": jwtFor(aud, "user_1", e.tokenN), "refresh_token": "rt" + string(rune('0'+e.tokenN)), "expires_in": 300,
		})
	})
	mux.HandleFunc("/api/v1/whoami", func(w http.ResponseWriter, r *http.Request) {
		e.whoamis++
		e.authz = append(e.authz, r.Header.Get("Authorization"))
		if e.whoami != nil {
			e.whoami(w, r)
			return
		}
		w.Write([]byte(`{"ok":true,"sub":"user_1","roles":["example:api","example:read"]}`))
	})
	e.srv = httptest.NewServer(mux)
	t.Cleanup(e.srv.Close)
	return e
}

func (e *env) run(args ...string) (string, error) {
	d := Deps{
		HC: e.srv.Client(), Keyring: e.kr, Stdin: strings.NewReader(e.stdin), Now: func() time.Time { return e.clock },
		CallbackWait: 5 * time.Second,
		OpenBrowser: func(u string) error {
			if e.opener != nil {
				return e.opener(u)
			}
			return errors.New("no browser in tests")
		},
	}
	root := NewRootWith(d)
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func (e *env) login(extra ...string) (string, error) {
	return e.run(append([]string{"login", "--profile", "local", "--origin", e.srv.URL, "--client-id", "client_t"}, extra...)...)
}

func (e *env) stored() store.Credential {
	e.t.Helper()
	s, _ := store.Open(e.kr, "local")
	c, err := s.Get("local")
	if err != nil {
		e.t.Fatalf("nothing stored: %v", err)
	}
	return c
}

// authorizeParams pulls the state and redirect out of the printed authorize URL.
func authorizeParams(t *testing.T, authURL string) url.Values {
	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query()
}

func TestLoginNoBrowserBareCode(t *testing.T) {
	e := newEnv(t)
	e.stdin = "the-code\n"
	out, err := e.login("--no-browser")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "/authorize?") || !strings.Contains(out, "resource=") || !strings.Contains(out, "Logged in to local") {
		t.Fatalf("output: %s", out)
	}
	c := e.stored()
	if c.RefreshToken != "rt1" || c.Sub != "user_1" || c.Aud != e.srv.URL+"/api/v1" || c.ClientID != "client_t" {
		t.Fatalf("%+v", c)
	}
	f := e.forms[0]
	if f.Get("code") != "the-code" || f.Get("resource") != e.srv.URL+"/api/v1" || f.Get("code_verifier") == "" ||
		f.Get("redirect_uri") != "http://127.0.0.1:53682/callback" || f.Get("grant_type") != "authorization_code" {
		t.Fatalf("exchange form %v", f)
	}
	if strings.Contains(out, "rt1") || strings.Contains(out, c.AccessToken) {
		t.Fatal("a token reached the output")
	}
	// the custom profile was persisted
	out, err = e.run("whoami", "--profile", "local")
	if err != nil || !strings.Contains(out, "sub:     user_1") {
		t.Fatalf("%v %s", err, out)
	}
}

func TestLoginNoBrowserPastedURLChecksState(t *testing.T) {
	e := newEnv(t)
	e.stdin = "http://127.0.0.1:53682/callback?code=abc&state=wrong\n"
	_, err := e.login("--no-browser")
	if err == nil || apierr.ExitCode(err) != apierr.ExitAuth || !strings.Contains(err.Error(), "state mismatch") {
		t.Fatalf("%v", err)
	}
	if len(e.forms) != 0 {
		t.Fatal("token endpoint called after a state mismatch")
	}
	assertNoLogin(t, e)
}

func TestLoginNoBrowserPastedURLWithRightState(t *testing.T) {
	e := newEnv(t)
	// Feed stdin lazily: a reader that waits for the prompt, then builds the callback URL with the
	// state taken from the printed authorize URL.
	var buf bytes.Buffer
	lr := &lazyReader{fn: func() string {
		q := authorizeParams(t, firstURL(buf.String()))
		return "http://127.0.0.1:53682/callback?code=xyz&state=" + q.Get("state") + "\n"
	}}
	d := Deps{HC: e.srv.Client(), Keyring: e.kr, Stdin: lr, Now: func() time.Time { return e.clock }}
	root := NewRootWith(d)
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"login", "--no-browser", "--profile", "local", "--origin", e.srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("%v\n%s", err, buf.String())
	}
	if e.forms[0].Get("code") != "xyz" {
		t.Fatalf("%v", e.forms[0])
	}
	if e.stored().ClientID == "" {
		t.Fatal("no client id stored")
	}
}

type lazyReader struct {
	fn   func() string
	data *strings.Reader
}

func (l *lazyReader) Read(p []byte) (int, error) {
	if l.data == nil {
		l.data = strings.NewReader(l.fn())
	}
	return l.data.Read(p)
}

func firstURL(s string) string {
	i := strings.Index(s, "http")
	j := strings.IndexAny(s[i:], " \n")
	return s[i : i+j]
}

func TestLoginNoBrowserCallbackError(t *testing.T) {
	e := newEnv(t)
	e.stdin = "http://127.0.0.1:53682/callback?error=access_denied&error_description=User+said+no\n"
	_, err := e.login("--no-browser")
	if err == nil || apierr.ExitCode(err) != apierr.ExitAuth || !strings.Contains(err.Error(), "access_denied (User said no)") {
		t.Fatalf("%v", err)
	}
	if len(e.forms) != 0 {
		t.Fatal("exchange attempted")
	}
}

func TestLoginLoopbackStoresIdenticalResult(t *testing.T) {
	e := newEnv(t)
	e.opener = func(authURL string) error {
		q := authorizeParams(t, authURL)
		ru, _ := url.Parse(q.Get("redirect_uri"))
		if ru.Hostname() != "127.0.0.1" || ru.Path != "/callback" {
			t.Errorf("redirect %s", q.Get("redirect_uri"))
		}
		go func() {
			resp, err := http.Get(q.Get("redirect_uri") + "?code=loop&state=" + q.Get("state"))
			if err == nil {
				resp.Body.Close()
			}
		}()
		return nil
	}
	port := freePort(t)
	out, err := e.login("--port", port)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	c := e.stored()
	if c.RefreshToken != "rt1" || c.Aud != e.srv.URL+"/api/v1" || c.Sub != "user_1" {
		t.Fatalf("%+v", c)
	}
	if e.forms[0].Get("code") != "loop" || e.forms[0].Get("redirect_uri") != "http://127.0.0.1:"+port+"/callback" {
		t.Fatalf("%v", e.forms[0])
	}
	if !strings.Contains(out, "scope=openid+profile+email+offline_access+example%3Aapi") {
		t.Fatalf("scopes not from discovery: %s", out)
	}
}

func TestLoginLoopbackStateMismatchStoresNothing(t *testing.T) {
	e := newEnv(t)
	e.opener = func(authURL string) error {
		q := authorizeParams(t, authURL)
		go func() {
			resp, err := http.Get(q.Get("redirect_uri") + "?code=loop&state=forged")
			if err == nil {
				resp.Body.Close()
			}
		}()
		return nil
	}
	_, err := e.login("--port", freePort(t))
	if err == nil || !strings.Contains(err.Error(), "state mismatch") || len(e.forms) != 0 {
		t.Fatalf("%v forms=%d", err, len(e.forms))
	}
	assertNoLogin(t, e)
}

func TestLoginLoopbackCallbackError(t *testing.T) {
	e := newEnv(t)
	e.opener = func(authURL string) error {
		q := authorizeParams(t, authURL)
		go func() {
			resp, err := http.Get(q.Get("redirect_uri") + "?error=access_denied&error_description=nope&state=" + q.Get("state"))
			if err == nil {
				resp.Body.Close()
			}
		}()
		return nil
	}
	_, err := e.login("--port", freePort(t))
	if err == nil || !strings.Contains(err.Error(), "access_denied (nope)") {
		t.Fatalf("%v", err)
	}
	assertNoLogin(t, e)
}

func assertNoLogin(t *testing.T, e *env) {
	t.Helper()
	if len(e.kr.m) != 0 {
		t.Fatal("keychain holds a credential")
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("PO_CONFIG_DIR"), "credentials.json")); err == nil {
		t.Fatal("credentials file written")
	}
}

func freePort(t *testing.T) string {
	t.Helper()
	l := httptest.NewServer(nil)
	defer l.Close()
	return l.Listener.Addr().String()[strings.LastIndex(l.Listener.Addr().String(), ":")+1:]
}

func TestLoginBrowserOpenFailureIsNonFatalAndBusyPortIsUsage(t *testing.T) {
	e := newEnv(t)
	busy := httptest.NewServer(nil)
	defer busy.Close()
	addr := busy.Listener.Addr().String()
	_, err := e.login("--port", addr[strings.LastIndex(addr, ":")+1:])
	if apierr.ExitCode(err) != apierr.ExitUsage || !strings.Contains(err.Error(), "--no-browser") {
		t.Fatalf("%v", err)
	}
	// open failure prints a notice and the wait proceeds (times out here).
	d := Deps{HC: e.srv.Client(), Keyring: e.kr, Now: time.Now, CallbackWait: 50 * time.Millisecond,
		OpenBrowser: func(string) error { return errors.New("no display") }}
	root := NewRootWith(d)
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"login", "--profile", "local", "--origin", e.srv.URL, "--port", freePort(t)})
	err = root.Execute()
	if err == nil || !strings.Contains(out.String(), "Could not open a browser (no display)") || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("%v %s", err, out.String())
	}
}

func TestLoginAudienceMismatchStoresNothing(t *testing.T) {
	e := newEnv(t)
	e.aud = "https://other.example/api/v1"
	e.stdin = "c\n"
	_, err := e.login("--no-browser")
	if apierr.ExitCode(err) != apierr.ExitAuth || !strings.Contains(err.Error(), "audience") {
		t.Fatalf("%v", err)
	}
	assertNoLogin(t, e)
}

func TestWhoamiAndJSON(t *testing.T) {
	e := newEnv(t)
	e.stdin = "c\n"
	if _, err := e.login("--no-browser"); err != nil {
		t.Fatal(err)
	}
	out, err := e.run("whoami", "--profile", "local")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"profile: local", "origin:  " + e.srv.URL, "sub:     user_1", "roles:   example:api, example:read"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
	if !strings.HasPrefix(e.authz[0], "Bearer ") {
		t.Fatal("no bearer")
	}
	raw, err := e.run("whoami", "--profile", "local", "--json")
	if err != nil || strings.TrimSpace(raw) != `{"ok":true,"sub":"user_1","roles":["example:api","example:read"]}` {
		t.Fatalf("%v %q", err, raw)
	}
}

func TestWhoamiRefreshesOnExpiryTransparently(t *testing.T) {
	e := newEnv(t)
	e.stdin = "c\n"
	e.login("--no-browser")
	if len(e.forms) != 1 {
		t.Fatal("setup")
	}
	e.clock = e.clock.Add(100 * time.Second) // still valid (expires at +300, skew 30)
	if _, err := e.run("whoami", "--profile", "local"); err != nil || len(e.forms) != 1 {
		t.Fatalf("refreshed too early: %v forms=%d", err, len(e.forms))
	}
	e.clock = e.clock.Add(190 * time.Second) // +290: inside the 30 s skew
	if _, err := e.run("whoami", "--profile", "local"); err != nil {
		t.Fatal(err)
	}
	if len(e.forms) != 2 {
		t.Fatalf("expected one refresh, token requests = %d", len(e.forms))
	}
	rf := e.forms[1]
	if rf.Get("grant_type") != "refresh_token" || rf.Get("resource") != e.srv.URL+"/api/v1" || rf.Get("refresh_token") != "rt1" || rf.Get("client_id") != "client_t" {
		t.Fatalf("refresh form %v", rf)
	}
	if e.stored().RefreshToken != "rt2" {
		t.Fatal("rotated refresh token not stored")
	}
	if e.authz[len(e.authz)-1] == e.authz[0] {
		t.Fatal("the call used the stale access token")
	}
}

func TestWhoamiRetriesExpiredTokenExactlyOnce(t *testing.T) {
	e := newEnv(t)
	e.stdin = "c\n"
	e.login("--no-browser")
	e.whoami = func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte(`{"error":"expired_token"}`))
	}
	_, err := e.run("whoami", "--profile", "local")
	if apierr.ExitCode(err) != apierr.ExitAuth || e.whoamis != 2 || len(e.forms) != 2 {
		t.Fatalf("err=%v whoamis=%d forms=%d", err, e.whoamis, len(e.forms))
	}
}

func TestWhoamiInvalidAudienceNeverRetried(t *testing.T) {
	e := newEnv(t)
	e.stdin = "c\n"
	e.login("--no-browser")
	e.whoami = func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte(`{"error":"invalid_audience"}`))
	}
	out, err := e.run("whoami", "--profile", "local", "--json")
	if apierr.ExitCode(err) != apierr.ExitAuth || e.whoamis != 1 || len(e.forms) != 1 || !strings.Contains(err.Error(), "invalid_audience") {
		t.Fatalf("err=%v whoamis=%d forms=%d", err, e.whoamis, len(e.forms))
	}
	if !strings.Contains(out, `"invalid_audience"`) {
		t.Fatalf("--json must print the server body: %q", out)
	}
}

func TestWhoamiRefusalRendering(t *testing.T) {
	cases := []struct {
		status int
		body   string
		code   int
		want   string
	}{
		{403, `{"error":"missing_role","role":"example:api"}`, apierr.ExitForbidden, "lacks the role example:api"},
		{403, `{"error":"access_pending"}`, apierr.ExitForbidden, "no entitlement"},
		{503, `{"error":"no_identity_provider"}`, apierr.ExitUnavail, "no identity provider"},
	}
	for _, c := range cases {
		e := newEnv(t)
		e.stdin = "c\n"
		e.login("--no-browser")
		e.whoami = func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(c.status)
			w.Write([]byte(c.body))
		}
		_, err := e.run("whoami", "--profile", "local")
		if apierr.ExitCode(err) != c.code || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%d: %v", c.status, err)
		}
	}
}

func TestWhoamiUnreadableBody(t *testing.T) {
	e := newEnv(t)
	e.stdin = "c\n"
	e.login("--no-browser")
	e.whoami = func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(`<html>`)) }
	if _, err := e.run("whoami", "--profile", "local"); err == nil {
		t.Fatal("garbage accepted")
	}
}

func TestLogoutThenWhoamiIsExit3(t *testing.T) {
	e := newEnv(t)
	e.stdin = "c\n"
	e.login("--no-browser")
	out, err := e.run("logout", "--profile", "local")
	if err != nil || !strings.Contains(out, "Logged out of local (removed from the keychain)") {
		t.Fatalf("%v %s", err, out)
	}
	assertNoLogin(t, e)
	_, err = e.run("whoami", "--profile", "local")
	if apierr.ExitCode(err) != apierr.ExitAuth || !strings.Contains(err.Error(), "not logged in to local") {
		t.Fatalf("whoami after logout: %v", err)
	}
	if e.whoamis != 0 {
		t.Fatal("whoami called the API with no login")
	}
	out, err = e.run("logout", "--profile", "local")
	if err != nil || !strings.Contains(out, "nothing to remove") {
		t.Fatalf("second logout: %v %s", err, out)
	}
}

func TestFileFallbackWhenKeychainBroken(t *testing.T) {
	e := newEnv(t)
	e.kr.err = errors.New("no secret service")
	e.stdin = "c\n"
	out, err := e.login("--no-browser")
	if err != nil || !strings.Contains(out, "stored in the file") {
		t.Fatalf("%v %s", err, out)
	}
	p := filepath.Join(os.Getenv("PO_CONFIG_DIR"), "credentials.json")
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", fi.Mode().Perm())
	}
	if _, err := e.run("whoami", "--profile", "local"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.run("logout", "--profile", "local"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.run("whoami", "--profile", "local"); apierr.ExitCode(err) != apierr.ExitAuth {
		t.Fatalf("%v", err)
	}
}

func TestLoginFallsBackWhenKeychainSetFailsAfterProbe(t *testing.T) {
	e := newEnv(t)
	e.kr = &fakeKR{m: map[string]string{}}
	failSet := &setFails{fakeKR: e.kr}
	d := Deps{HC: e.srv.Client(), Keyring: failSet, Stdin: strings.NewReader("c\n"), Now: time.Now}
	root := NewRootWith(d)
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"login", "--no-browser", "--profile", "local", "--origin", e.srv.URL})
	if err := root.Execute(); err != nil || !strings.Contains(out.String(), "Keychain unavailable") {
		t.Fatalf("%v %s", err, out.String())
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("PO_CONFIG_DIR"), "credentials.json")); err != nil {
		t.Fatal("fallback file missing")
	}
	// whoami finds the fallback file even though the keychain probe works.
	root = NewRootWith(Deps{HC: e.srv.Client(), Keyring: failSet, Now: time.Now})
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"whoami", "--profile", "local"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
}

type setFails struct{ *fakeKR }

func (s *setFails) Set(_, _, _ string) error { return errors.New("keychain locked") }

func TestPOCredentialStoreFile(t *testing.T) {
	e := newEnv(t)
	t.Setenv("PO_CREDENTIAL_STORE", "file")
	e.stdin = "c\n"
	out, err := e.login("--no-browser")
	if err != nil || !strings.Contains(out, "stored in the file") || len(e.kr.m) != 0 {
		t.Fatalf("%v %s", err, out)
	}
}

func TestWhoamiProfileOriginMismatchRefusedLocally(t *testing.T) {
	e := newEnv(t)
	e.stdin = "c\n"
	e.login("--no-browser")
	// Same profile name, now pointing at a different origin: the stored aud no longer matches.
	f := filepath.Join(os.Getenv("PO_CONFIG_DIR"), "config.yml")
	os.WriteFile(f, []byte("profiles:\n  local:\n    origin: "+e.srv.URL+"\n    client_id: client_other\n"), 0o600)
	_, err := e.run("whoami", "--profile", "local")
	if apierr.ExitCode(err) != apierr.ExitAuth || e.whoamis != 0 || !strings.Contains(err.Error(), "po login --profile local") {
		t.Fatalf("%v whoamis=%d", err, e.whoamis)
	}
}

func TestUnknownProfileIsUsage(t *testing.T) {
	e := newEnv(t)
	if _, err := e.run("login", "--profile", "nope", "--no-browser"); apierr.ExitCode(err) != apierr.ExitUsage {
		t.Fatalf("%v", err)
	}
	if _, err := e.run("whoami", "--profile", "nope"); err == nil {
		t.Fatal("unknown profile accepted")
	}
}

func TestAPIBaseKeepsAudience(t *testing.T) {
	e := newEnv(t)
	e.stdin = "c\n"
	// origin is a name that is NOT reachable; --api-base points discovery and calls at the fake.
	out, err := e.run("login", "--no-browser", "--profile", "local", "--origin", "https://origin.invalid", "--api-base", e.srv.URL)
	if err != nil {
		t.Fatalf("%v %s", err, out)
	}
	if e.forms[0].Get("resource") != "https://origin.invalid/api/v1" {
		t.Fatalf("resource %v", e.forms[0])
	}
	// the fake mints aud from the request resource, so whoami through --api-base works.
	if _, err := e.run("whoami", "--profile", "local", "--api-base", e.srv.URL); err != nil {
		t.Fatal(err)
	}
}

func TestAuthRefreshCommand(t *testing.T) {
	e := newEnv(t)
	e.stdin = "c\n"
	e.login("--no-browser")
	out, err := e.run("auth", "refresh", "--profile", "local")
	if err != nil || !strings.Contains(out, "aud "+e.srv.URL+"/api/v1") || len(e.forms) != 2 {
		t.Fatalf("%v %s", err, out)
	}
	if e.forms[1].Get("resource") != e.srv.URL+"/api/v1" {
		t.Fatal("resource missing on forced refresh")
	}
	if _, err := e.run("auth", "bogus"); apierr.ExitCode(err) != apierr.ExitUsage {
		t.Fatalf("%v", err)
	}
	e.run("logout", "--profile", "local")
	if _, err := e.run("auth", "refresh", "--profile", "local"); apierr.ExitCode(err) != apierr.ExitAuth {
		t.Fatalf("%v", err)
	}
}

func TestRefreshRejectedAsksForLogin(t *testing.T) {
	e := newEnv(t)
	e.stdin = "c\n"
	e.login("--no-browser")
	e.clock = e.clock.Add(time.Hour)
	e.refuse = true
	_, err := e.run("whoami", "--profile", "local")
	if apierr.ExitCode(err) != apierr.ExitAuth || !strings.Contains(err.Error(), "po login --profile local") || e.whoamis != 0 {
		t.Fatalf("%v whoamis=%d", err, e.whoamis)
	}
}
