// Package api holds the authenticated session: bearer calls with transparent refresh.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/scientist-labs/pharmaobservatory-cli/internal/apierr"
	"github.com/scientist-labs/pharmaobservatory-cli/internal/auth"
	"github.com/scientist-labs/pharmaobservatory-cli/internal/auth/store"
	"github.com/scientist-labs/pharmaobservatory-cli/internal/config"
)

// RefreshSkew is how close to expiry a refresh happens before a call.
const RefreshSkew = 30 * time.Second

// Session is one profile's stored login plus the means to use it.
type Session struct {
	Profile config.Profile
	Store   store.Store
	HC      *http.Client
	Now     func() time.Time
	// Wait makes a 429 with a Retry-After sleep and retry once (--wait). Sleep and Notice are
	// seams: nil means a real sleep and no message.
	Wait   bool
	Sleep  func(ctx context.Context, d time.Duration) error
	Notice io.Writer
}

// pause sleeps d for a --wait retry, saying so first, and reports whether to go on.
func (s *Session) pause(ctx context.Context, d time.Duration) error {
	if s.Notice != nil {
		_, _ = fmt.Fprintf(s.Notice, "rate limited; waiting %ds before retrying once (--wait)\n", int(d/time.Second))
	}
	if s.Sleep != nil {
		return s.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// waitDelay is the sleep for a 429 under --wait, and whether to retry at all.
func (s *Session) waitDelay(status int, h http.Header) (time.Duration, bool) {
	if !s.Wait || status != http.StatusTooManyRequests {
		return 0, false
	}
	return apierr.RetryDelay(h)
}

func (s *Session) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Session) notLoggedIn() error {
	return apierr.Auth("not logged in to %s; run: %s login --profile %s", s.Profile.Name, apierr.Bin, s.Profile.Name)
}

// Load reads the stored credential and refuses a profile/origin mismatch locally.
func (s *Session) Load() (store.Credential, error) {
	c, err := s.Store.Get(s.Profile.Name)
	if errors.Is(err, store.ErrNotFound) {
		return c, s.notLoggedIn()
	}
	if err != nil {
		return c, err
	}
	if c.Aud != s.Profile.Resource() || c.ClientID != s.Profile.ClientID {
		return c, apierr.Auth("the stored login for %s was made for %s, not %s; run: %s login --profile %s",
			s.Profile.Name, c.Aud, s.Profile.Resource(), apierr.Bin, s.Profile.Name)
	}
	return c, nil
}

// Refresh redeems the stored refresh token and stores the result.
func (s *Session) Refresh(ctx context.Context, c store.Credential) (store.Credential, error) {
	ep, err := auth.Discover(ctx, s.HC, s.Profile.Base())
	if err != nil {
		return c, err
	}
	cl := auth.Client{HC: s.HC, Token: ep.Token, ClientID: s.Profile.ClientID, Resource: s.Profile.Resource(), Now: s.Now}
	n, err := cl.Refresh(ctx, c.RefreshToken)
	if err != nil {
		var ce *apierr.ClassError
		if errors.As(err, &ce) && ce.Class == apierr.ExitAuth {
			return c, apierr.Auth("the stored session could not be renewed (%s); run: %s login --profile %s", ce.Msg, apierr.Bin, s.Profile.Name)
		}
		return c, err
	}
	if err := s.Store.Set(s.Profile.Name, n); err != nil {
		return c, err
	}
	return n, nil
}

// Fresh returns a credential whose access token is valid, refreshing when it expires within
// RefreshSkew.
func (s *Session) Fresh(ctx context.Context) (store.Credential, error) {
	c, err := s.Load()
	if err != nil {
		return c, err
	}
	if s.now().Add(RefreshSkew).Before(c.ExpiresAt) {
		return c, nil
	}
	return s.Refresh(ctx, c)
}

func (s *Session) do(ctx context.Context, method, path, token string, payload []byte) (int, http.Header, []byte, error) {
	var rd io.Reader
	if payload != nil {
		rd = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.Profile.Base()+path, rd)
	if err != nil {
		return 0, nil, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := s.HC.Do(req)
	if err != nil {
		if u := apierr.UsageCause(err); u != nil {
			return 0, nil, nil, u
		}
		return 0, nil, nil, apierr.Unavailable("cannot reach %s: %v", s.Profile.Base(), err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return 0, nil, nil, apierr.Unavailable("reading the response from %s failed: %v", s.Profile.Base(), err)
	}
	return resp.StatusCode, resp.Header, b, nil
}

// Get performs GET path with the bearer. A 401 expired_token refreshes and retries exactly once;
// invalid_audience is never retried. Under Wait a 429 with a usable Retry-After sleeps and retries
// once. A non-200 answer is returned as the rendered error.
func (s *Session) Get(ctx context.Context, path string) ([]byte, error) {
	return s.send(ctx, http.MethodGet, path, nil)
}

// PostJSON performs POST path with the bearer and payload as the JSON body, with the same refresh,
// --wait and error rules as Get. A replay after expired_token or a 429 resends the same bytes: the
// server answered either refusal before running anything.
func (s *Session) PostJSON(ctx context.Context, path string, payload []byte) ([]byte, error) {
	if payload == nil {
		payload = []byte{}
	}
	return s.send(ctx, http.MethodPost, path, payload)
}

func (s *Session) send(ctx context.Context, method, path string, payload []byte) ([]byte, error) {
	c, err := s.Fresh(ctx)
	if err != nil {
		return nil, err
	}
	status, h, body, err := s.do(ctx, method, path, c.AccessToken, payload)
	if err != nil {
		return nil, err
	}
	if status == http.StatusUnauthorized && errorWord(body) == "expired_token" {
		if c, err = s.Refresh(ctx, c); err != nil {
			return nil, err
		}
		if status, h, body, err = s.do(ctx, method, path, c.AccessToken, payload); err != nil {
			return nil, err
		}
	}
	if d, ok := s.waitDelay(status, h); ok {
		if err := s.pause(ctx, d); err != nil {
			return nil, err
		}
		if status, h, body, err = s.do(ctx, method, path, c.AccessToken, payload); err != nil {
			return nil, err
		}
	}
	if status != http.StatusOK {
		return body, apierr.FromResponse(status, h, body, s.Profile.Name)
	}
	return body, nil
}

func errorWord(body []byte) string {
	var b struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(body, &b)
	return b.Error
}
