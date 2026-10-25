package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/scientist-labs/pharmaobservatory-cli/internal/auth/store"
	"github.com/scientist-labs/pharmaobservatory-cli/internal/config"
)

// The per-request timeout these tests give the session's client. Every sleep and hang below is a
// multiple of it, so a timeout that wrongly covers the --wait sleep fails the test.
const attemptTimeout = 200 * time.Millisecond

// waitSession is a logged-in Session under --wait whose HC has a short per-request timeout,
// over a server driven by h.
func waitSession(t *testing.T, h http.HandlerFunc) *Session {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	p := config.Profile{Name: "t", Origin: srv.URL, ClientID: "c"}
	cred := store.Credential{AccessToken: "at", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Hour), Aud: p.Resource(), ClientID: "c"}
	hc := srv.Client()
	hc.Timeout = attemptTimeout
	return &Session{Profile: p, Store: &memStore{c: cred}, HC: hc, Wait: true}
}

// sdkGet sends one GET through the SDK's own http.Client, the client every typed call uses.
func sdkGet(ctx context.Context, s *Session) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.Profile.Base()+"/api/v1/companies", nil)
	if err != nil {
		return nil, err
	}
	return s.SDK().GetConfig().HTTPClient.Do(req)
}

// A Retry-After wait longer than the per-request timeout is not a timeout: the timeout bounds
// each attempt, not the sleep between them (#47).
func TestWaitSleepLongerThanRequestTimeoutSucceeds(t *testing.T) {
	var calls atomic.Int32
	s := waitSession(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "55")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"quota_exceeded"}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	var slept time.Duration
	s.Sleep = func(ctx context.Context, d time.Duration) error {
		slept = d
		// Really sleep past the per-request timeout, standing in for the 55s Retry-After.
		select {
		case <-time.After(3 * attemptTimeout):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	resp, err := sdkGet(context.Background(), s)
	if err != nil {
		t.Fatalf("the --wait retry failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200 after the retry", resp.StatusCode)
	}
	if slept != 55*time.Second {
		t.Fatalf("slept for %v, want the server's Retry-After of 55s", slept)
	}
	if n := calls.Load(); n != 2 {
		t.Fatalf("%d requests, want 2 (one 429, one retry)", n)
	}
}

// A request that never answers still fails at the per-request timeout.
func TestHungRequestStillTimesOut(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	s := waitSession(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	start := time.Now()
	resp, err := sdkGet(context.Background(), s)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("a hung request returned without error")
	}
	if el := time.Since(start); el > 10*attemptTimeout {
		t.Fatalf("a hung request took %v to fail, want about the %v per-request timeout", el, attemptTimeout)
	}
}

// A hung retry after a --wait sleep times out too: the second attempt gets its own deadline.
func TestHungRetryAfterWaitTimesOut(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	var calls atomic.Int32
	s := waitSession(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	s.Sleep = func(context.Context, time.Duration) error { return nil }
	start := time.Now()
	resp, err := sdkGet(context.Background(), s)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("a hung retry returned without error")
	}
	if el := time.Since(start); el > 10*attemptTimeout {
		t.Fatalf("a hung retry took %v to fail", el)
	}
}

// Cancelling the caller's context interrupts the --wait sleep (the real timer, no seam).
func TestCancelInterruptsWaitSleep(t *testing.T) {
	s := waitSession(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(attemptTimeout, cancel)
	start := time.Now()
	resp, err := sdkGet(ctx, s)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("a cancelled --wait sleep returned without error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error %v, want context.Canceled", err)
	}
	if el := time.Since(start); el > 10*attemptTimeout {
		t.Fatalf("cancel took %v to interrupt a 60s sleep", el)
	}
}
