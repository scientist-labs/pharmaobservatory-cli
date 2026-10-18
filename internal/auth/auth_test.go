package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/scientist-labs/pharmaobservatory-cli/internal/apierr"
)

func jwt(claims map[string]any) string {
	b, _ := json.Marshal(claims)
	return "h." + base64.RawURLEncoding.EncodeToString(b) + ".s"
}

func TestPKCEKnownVector(t *testing.T) {
	// RFC 7636 appendix B.
	got := Challenge("dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk")
	if want := "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"; got != want {
		t.Fatalf("challenge %q want %q", got, want)
	}
}

func TestNewPKCE(t *testing.T) {
	v, c, err := NewPKCE()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(v)
	if err != nil || len(raw) != 48 {
		t.Fatalf("verifier is not 48 random bytes: len=%d err=%v", len(raw), err)
	}
	if c != Challenge(v) {
		t.Fatal("challenge does not match verifier")
	}
	v2, _, _ := NewPKCE()
	if v == v2 {
		t.Fatal("verifier repeated")
	}
}

func TestAuthorizeURL(t *testing.T) {
	u := AuthorizeURL(AuthorizeParams{
		Endpoint: "https://login.example/oauth2/authorize", ClientID: "client_x", RedirectURI: RedirectURI(53682),
		Scope: ScopeString([]string{"openid", "example:api"}), State: "st", Challenge: "ch", Resource: "https://o.example/api/v1",
	})
	pu, err := url.Parse(u)
	if err != nil {
		t.Fatal(err)
	}
	q := pu.Query()
	want := map[string]string{
		"response_type": "code", "client_id": "client_x", "redirect_uri": "http://127.0.0.1:53682/callback",
		"scope": "openid profile email offline_access example:api", "state": "st", "code_challenge": "ch",
		"code_challenge_method": "S256", "resource": "https://o.example/api/v1",
	}
	for k, v := range want {
		if q.Get(k) != v {
			t.Errorf("%s = %q want %q", k, q.Get(k), v)
		}
	}
	if pu.Host != "login.example" {
		t.Errorf("host %s", pu.Host)
	}
}

func TestCodeFromQuery(t *testing.T) {
	cases := []struct {
		name, q, wantCode, wantErr string
	}{
		{"ok", "code=abc&state=s1", "abc", ""},
		{"mismatch", "code=abc&state=other", "", "state mismatch"},
		{"missing state", "code=abc", "", "state mismatch"},
		{"error with description", "error=access_denied&error_description=nope&state=s1", "", "access_denied (nope)"},
		{"error bare", "error=server_error", "", "server_error"},
		{"no code", "state=s1", "", "no authorization code"},
	}
	for _, c := range cases {
		q, _ := url.ParseQuery(c.q)
		code, err := CodeFromQuery(q, "s1")
		if c.wantErr == "" {
			if err != nil || code != c.wantCode {
				t.Errorf("%s: %q %v", c.name, code, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), c.wantErr) || apierr.ExitCode(err) != apierr.ExitAuth {
			t.Errorf("%s: err=%v", c.name, err)
		}
	}
}

func TestParsePasted(t *testing.T) {
	if c, err := ParsePasted("http://127.0.0.1:53682/callback?code=zzz&state=s1\n", "s1"); err != nil || c != "zzz" {
		t.Fatalf("full url: %q %v", c, err)
	}
	if _, err := ParsePasted("http://127.0.0.1:53682/callback?code=zzz&state=bad", "s1"); err == nil {
		t.Fatal("state mismatch accepted")
	}
	if _, err := ParsePasted("http://127.0.0.1:53682/callback?error=access_denied&error_description=x", "s1"); err == nil || !strings.Contains(err.Error(), "access_denied") {
		t.Fatalf("callback error not surfaced: %v", err)
	}
	if c, err := ParsePasted("  barecode123 \n", "s1"); err != nil || c != "barecode123" {
		t.Fatalf("bare: %q %v", c, err)
	}
	if _, err := ParsePasted("   ", "s1"); apierr.ExitCode(err) != apierr.ExitUsage {
		t.Fatalf("empty paste: %v", err)
	}
}

func TestLoopbackCallback(t *testing.T) {
	ln, err := Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if !strings.HasPrefix(addr, "127.0.0.1:") {
		t.Fatalf("listener not on the literal loopback: %s", addr)
	}
	type res struct {
		code string
		err  error
	}
	ch := make(chan res, 1)
	go func() {
		c, e := WaitCallback(context.Background(), ln, "s1", 5*time.Second)
		ch <- res{c, e}
	}()
	resp, err := http.Get("http://" + addr + "/callback?code=abc&state=s1")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	r := <-ch
	if r.err != nil || r.code != "abc" {
		t.Fatalf("%q %v", r.code, r.err)
	}
}

func TestLoopbackStateMismatchAndError(t *testing.T) {
	for _, q := range []string{"code=abc&state=evil", "error=access_denied&error_description=no"} {
		ln, _ := Listen(0)
		ch := make(chan error, 1)
		go func() { _, e := WaitCallback(context.Background(), ln, "s1", 5*time.Second); ch <- e }()
		resp, err := http.Get("http://" + ln.Addr().String() + "/callback?" + q)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status %d", q, resp.StatusCode)
		}
		if e := <-ch; e == nil || apierr.ExitCode(e) != apierr.ExitAuth {
			t.Errorf("%s: err %v", q, e)
		}
	}
}

func TestLoopbackTimeoutAndCancel(t *testing.T) {
	ln, _ := Listen(0)
	if _, err := WaitCallback(context.Background(), ln, "s", 10*time.Millisecond); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("timeout: %v", err)
	}
	ln, _ = Listen(0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := WaitCallback(ctx, ln, "s", time.Minute); err == nil {
		t.Fatal("cancel ignored")
	}
}

func TestListenBusyPort(t *testing.T) {
	ln, _ := Listen(0)
	defer func() { _ = ln.Close() }()
	port := ln.Addr().(interface{ String() string }).String()
	var p int
	for _, ch := range port[strings.LastIndex(port, ":")+1:] {
		p = p*10 + int(ch-'0')
	}
	if _, err := Listen(p); err == nil || !strings.Contains(err.Error(), "--no-browser") {
		t.Fatalf("busy port: %v", err)
	}
}

// idp is a fake discovery + token endpoint. It records every token request form.
type idp struct {
	srv       *httptest.Server
	forms     []url.Values
	aud       any
	noRefresh bool
	status    int
}

func newIDP(t *testing.T, aud any) *idp {
	f := &idp{aud: aud}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/oauth-protected-resource/api/v1", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"authorization_servers": []string{f.srv.URL + "/"},
			"scopes_supported":      []string{"openid", "example:api"},
		})
	})
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"authorization_endpoint": f.srv.URL + "/authorize", "token_endpoint": f.srv.URL + "/token",
		})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.forms = append(f.forms, r.PostForm)
		if f.status != 0 {
			w.WriteHeader(f.status)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "invalid_grant", "error_description": "bad"})
			return
		}
		resp := map[string]any{"access_token": jwt(map[string]any{"aud": f.aud, "sub": "user_1"}), "expires_in": 300}
		if !f.noRefresh {
			resp["refresh_token"] = "rt-" + r.PostForm.Get("grant_type")
		}
		_ = json.NewEncoder(w).Encode(resp)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func TestDiscover(t *testing.T) {
	f := newIDP(t, "x")
	ep, err := Discover(context.Background(), f.srv.Client(), f.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if ep.Authorize != f.srv.URL+"/authorize" || ep.Token != f.srv.URL+"/token" || len(ep.Scopes) != 2 {
		t.Fatalf("%+v", ep)
	}
	if got := ScopeString(ep.Scopes); got != "openid profile email offline_access example:api" {
		t.Fatalf("scopes %q", got)
	}
}

func TestDiscoverFailures(t *testing.T) {
	down := httptest.NewServer(http.NotFoundHandler())
	defer down.Close()
	if _, err := Discover(context.Background(), down.Client(), down.URL); apierr.ExitCode(err) != apierr.ExitUnavail {
		t.Fatalf("404 discovery: %v", err)
	}
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) }))
	defer empty.Close()
	if _, err := Discover(context.Background(), empty.Client(), empty.URL); err == nil {
		t.Fatal("no authorization_servers accepted")
	}
	if _, err := Discover(context.Background(), http.DefaultClient, "http://127.0.0.1:1"); apierr.ExitCode(err) != apierr.ExitUnavail {
		t.Fatalf("connection failure: %v", err)
	}
}

func TestExchangeAndRefreshSendResource(t *testing.T) {
	const res = "https://o.example/api/v1"
	f := newIDP(t, []any{res})
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	c := Client{HC: f.srv.Client(), Token: f.srv.URL + "/token", ClientID: "client_x", Resource: res, Now: func() time.Time { return now }}
	cred, err := c.Exchange(context.Background(), "the-code", "the-verifier", RedirectURI(53682))
	if err != nil {
		t.Fatal(err)
	}
	ex := f.forms[0]
	for k, v := range map[string]string{
		"grant_type": "authorization_code", "client_id": "client_x", "code": "the-code",
		"code_verifier": "the-verifier", "redirect_uri": RedirectURI(53682), "resource": res,
	} {
		if ex.Get(k) != v {
			t.Errorf("exchange %s = %q want %q", k, ex.Get(k), v)
		}
	}
	if cred.Aud != res || cred.Sub != "user_1" || cred.RefreshToken != "rt-authorization_code" || !cred.ExpiresAt.Equal(now.Add(300*time.Second)) {
		t.Fatalf("%+v", cred)
	}
	n, err := c.Refresh(context.Background(), cred.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	rf := f.forms[1]
	for k, v := range map[string]string{
		"grant_type": "refresh_token", "client_id": "client_x", "refresh_token": "rt-authorization_code", "resource": res,
	} {
		if rf.Get(k) != v {
			t.Errorf("refresh %s = %q want %q", k, rf.Get(k), v)
		}
	}
	if n.Aud != res {
		t.Fatal("aud lost on refresh")
	}
}

func TestRefreshKeepsPreviousRefreshTokenWhenNotRotated(t *testing.T) {
	const res = "https://o.example/api/v1"
	f := newIDP(t, res)
	f.noRefresh = true
	c := Client{HC: f.srv.Client(), Token: f.srv.URL + "/token", ClientID: "c", Resource: res}
	n, err := c.Refresh(context.Background(), "old-rt")
	if err != nil || n.RefreshToken != "old-rt" {
		t.Fatalf("%+v %v", n, err)
	}
	if _, err := c.Exchange(context.Background(), "c", "v", "r"); err == nil || !strings.Contains(err.Error(), "no refresh token") {
		t.Fatalf("exchange without refresh token accepted: %v", err)
	}
}

func TestAudienceMismatchRefused(t *testing.T) {
	f := newIDP(t, "https://other.example/api/v1")
	c := Client{HC: f.srv.Client(), Token: f.srv.URL + "/token", ClientID: "c", Resource: "https://o.example/api/v1"}
	_, err := c.Exchange(context.Background(), "c", "v", "r")
	if err == nil || apierr.ExitCode(err) != apierr.ExitAuth || !strings.Contains(err.Error(), "audience") {
		t.Fatalf("exchange: %v", err)
	}
	if _, err := c.Refresh(context.Background(), "rt"); err == nil || !strings.Contains(err.Error(), "audience") {
		t.Fatalf("refresh: %v", err)
	}
}

func TestTokenEndpointRefusals(t *testing.T) {
	f := newIDP(t, "x")
	f.status = 400
	c := Client{HC: f.srv.Client(), Token: f.srv.URL + "/token", ClientID: "c", Resource: "x"}
	if _, err := c.Exchange(context.Background(), "c", "v", "r"); apierr.ExitCode(err) != apierr.ExitAuth || !strings.Contains(err.Error(), "invalid_grant (bad)") {
		t.Fatalf("400: %v", err)
	}
	f.status = 503
	if _, err := c.Refresh(context.Background(), "rt"); apierr.ExitCode(err) != apierr.ExitUnavail {
		t.Fatalf("503: %v", err)
	}
	down := Client{HC: http.DefaultClient, Token: "http://127.0.0.1:1/token", ClientID: "c", Resource: "x"}
	if _, err := down.Refresh(context.Background(), "rt"); apierr.ExitCode(err) != apierr.ExitUnavail {
		t.Fatalf("down: %v", err)
	}
}

func TestClaimsAndAudience(t *testing.T) {
	if _, err := Claims("nodots"); err == nil {
		t.Fatal("non-JWT accepted")
	}
	if _, err := Claims("a.!!!.c"); err == nil {
		t.Fatal("bad base64 accepted")
	}
	if _, err := Claims("a." + base64.RawURLEncoding.EncodeToString([]byte("nope")) + ".c"); err == nil {
		t.Fatal("bad json accepted")
	}
	if !HasAudience(map[string]any{"aud": "r"}, "r") || !HasAudience(map[string]any{"aud": []any{"x", "r"}}, "r") {
		t.Fatal("audience not found")
	}
	if HasAudience(map[string]any{"aud": "x"}, "r") || HasAudience(map[string]any{}, "r") || HasAudience(map[string]any{"aud": []any{1}}, "r") {
		t.Fatal("wrong audience accepted")
	}
}

func TestDefaultClock(t *testing.T) {
	if (Client{}).now().IsZero() {
		t.Fatal("clock")
	}
}

// insecureIDP serves discovery documents whose named field points at an http URL on a non-loopback host.
func insecureIDP(t *testing.T, field string) *httptest.Server {
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/oauth-protected-resource/api/v1", func(w http.ResponseWriter, _ *http.Request) {
		as := srv.URL
		if field == "issuer" {
			as = "http://idp.example.com"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"authorization_servers": []string{as}})
	})
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, _ *http.Request) {
		m := map[string]string{"authorization_endpoint": srv.URL + "/authorize", "token_endpoint": srv.URL + "/token"}
		if field == "authorize_endpoint" {
			m["authorization_endpoint"] = "http://idp.example.com/authorize"
		}
		if field == "token_endpoint" {
			m["token_endpoint"] = "http://idp.example.com/token"
		}
		_ = json.NewEncoder(w).Encode(m)
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestDiscoverRefusesNonHTTPSNonLoopback(t *testing.T) {
	for _, field := range []string{"issuer", "authorize_endpoint", "token_endpoint"} {
		srv := insecureIDP(t, field)
		_, err := Discover(context.Background(), srv.Client(), srv.URL)
		if apierr.ExitCode(err) != apierr.ExitUsage || !strings.Contains(err.Error(), "must be https") {
			t.Errorf("%s: %v", field, err)
		}
	}
	if _, err := Discover(context.Background(), http.DefaultClient, "http://origin.example.com"); apierr.ExitCode(err) != apierr.ExitUsage {
		t.Fatalf("http origin: %v", err)
	}
}

func TestLoopbackHTTPDiscoveryAllowed(t *testing.T) {
	f := newIDP(t, "x") // httptest listens on 127.0.0.1 over http
	if _, err := Discover(context.Background(), f.srv.Client(), f.srv.URL); err != nil {
		t.Fatal(err)
	}
}

func TestTokenRequestRefusesHTTPEndpointBeforeSending(t *testing.T) {
	c := Client{HC: http.DefaultClient, Token: "http://idp.example.com/token", ClientID: "c", Resource: "r"}
	_, err := c.Exchange(context.Background(), "code", "verifier", "http://127.0.0.1/cb")
	if apierr.ExitCode(err) != apierr.ExitUsage {
		t.Fatalf("%v", err)
	}
	if _, err := c.Refresh(context.Background(), "rt"); apierr.ExitCode(err) != apierr.ExitUsage {
		t.Fatalf("%v", err)
	}
}

func TestServerErrorDescriptionIsSanitized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"gone\u001b[31m RED\u001b]0;pwn\u0007\nsecond"}`))
	}))
	defer srv.Close()
	c := Client{HC: srv.Client(), Token: srv.URL, ClientID: "c", Resource: "r"}
	_, err := c.Refresh(context.Background(), "rt")
	if err == nil || strings.ContainsAny(err.Error(), "\x1b\x07\n") || !strings.Contains(err.Error(), "gone RED second") {
		t.Fatalf("%q", err)
	}
	q := url.Values{"error": {"access_denied"}, "error_description": {"no\x1b[2J way"}}
	_, err = CodeFromQuery(q, "s")
	if err == nil || strings.Contains(err.Error(), "\x1b") || !strings.Contains(err.Error(), "no way") {
		t.Fatalf("%q", err)
	}
}
