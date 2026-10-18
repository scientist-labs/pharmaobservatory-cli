// Package api holds the authenticated session: bearer calls with transparent refresh.
package api

import (
	"context"
	"encoding/json"
	"errors"
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
}

func (s *Session) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Session) notLoggedIn() error {
	return apierr.Auth("not logged in to %s; run: po login --profile %s", s.Profile.Name, s.Profile.Name)
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
		return c, apierr.Auth("the stored login for %s was made for %s, not %s; run: po login --profile %s",
			s.Profile.Name, c.Aud, s.Profile.Resource(), s.Profile.Name)
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
			return c, apierr.Auth("the stored session could not be renewed (%s); run: po login --profile %s", ce.Msg, s.Profile.Name)
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

func (s *Session) do(ctx context.Context, path, token string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.Profile.Base()+path, nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := s.HC.Do(req)
	if err != nil {
		return 0, nil, apierr.Unavailable("cannot reach %s: %v", s.Profile.Base(), err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	return resp.StatusCode, b, err
}

// Get performs GET path with the bearer. A 401 expired_token refreshes and retries exactly once;
// invalid_audience is never retried. A non-200 answer is returned as the rendered error.
func (s *Session) Get(ctx context.Context, path string) ([]byte, error) {
	c, err := s.Fresh(ctx)
	if err != nil {
		return nil, err
	}
	status, body, err := s.do(ctx, path, c.AccessToken)
	if err != nil {
		return nil, err
	}
	if status == http.StatusUnauthorized && errorWord(body) == "expired_token" {
		if c, err = s.Refresh(ctx, c); err != nil {
			return nil, err
		}
		if status, body, err = s.do(ctx, path, c.AccessToken); err != nil {
			return nil, err
		}
	}
	if status != http.StatusOK {
		return body, apierr.FromResponse(status, body, s.Profile.Name)
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
