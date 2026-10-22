package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/scientist-labs/pharmaobservatory-cli/internal/auth/store"
	"github.com/scientist-labs/pharmaobservatory-cli/internal/config"
)

type memStore struct{ c store.Credential }

func (m *memStore) Get(string) (store.Credential, error)   { return m.c, nil }
func (m *memStore) Set(_ string, c store.Credential) error { m.c = c; return nil }
func (m *memStore) Delete(string) error                    { return nil }
func (m *memStore) Name() string                           { return "mem" }

const testUA = "pharma/1.2.3 (testos/testarch)"

// uaSession is a logged-in Session over an httptest server that records each request's User-Agent.
func uaSession(t *testing.T) (*Session, func() map[string]string) {
	t.Helper()
	var mu sync.Mutex
	seen := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.URL.Path] = r.Header.Get("User-Agent")
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"data":[]}`))
	}))
	t.Cleanup(srv.Close)
	p := config.Profile{Name: "t", Origin: srv.URL, ClientID: "c"}
	cred := store.Credential{AccessToken: "at", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Hour), Aud: p.Resource(), ClientID: "c"}
	s := &Session{Profile: p, Store: &memStore{c: cred}, HC: WithUserAgent(srv.Client(), testUA)}
	return s, func() map[string]string {
		mu.Lock()
		defer mu.Unlock()
		out := map[string]string{}
		for k, v := range seen {
			out[k] = v
		}
		return out
	}
}

func TestUserAgentOnRawSessionRequests(t *testing.T) {
	s, seen := uaSession(t)
	if _, err := s.Get(context.Background(), "/api/v1/whoami"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PostJSON(context.Background(), "/api/v1/query", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/whoami", "/api/v1/query"} {
		if got := seen()[path]; got != testUA {
			t.Errorf("%s: User-Agent %q, want %q", path, got, testUA)
		}
	}
}

func TestUserAgentOnSDKRequests(t *testing.T) {
	s, seen := uaSession(t)
	// The fake body does not satisfy the response schema; only the request matters here.
	_, _, _ = s.SDK().CompaniesAPI.CompaniesList(context.Background()).Execute()
	got, ok := seen()["/api/v1/companies"]
	if !ok {
		t.Fatal("the SDK request never reached the server")
	}
	if got != testUA {
		t.Fatalf("User-Agent %q, want %q (the SDK's own default must not leak through)", got, testUA)
	}
}

func TestWithUserAgentKeepsClientSettingsAndDoesNotStack(t *testing.T) {
	base := &http.Client{Timeout: 7 * time.Second}
	c := WithUserAgent(WithUserAgent(base, "pharma/old"), testUA)
	if c.Timeout != 7*time.Second {
		t.Fatalf("timeout %v", c.Timeout)
	}
	tr, ok := c.Transport.(*UserAgentTransport)
	if !ok || tr.UA != testUA {
		t.Fatalf("transport %#v", c.Transport)
	}
	if _, nested := tr.Base.(*UserAgentTransport); nested {
		t.Fatal("wrapped twice")
	}
	if base.Transport != nil {
		t.Fatal("the caller's client was modified")
	}
}
