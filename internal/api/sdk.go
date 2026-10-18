package api

import (
	"bytes"
	"io"
	"net/http"

	sdk "github.com/scientist-labs/pharmaobservatory-sdks/go"

	"github.com/scientist-labs/pharmaobservatory-cli/internal/apierr"
)

// SDK builds the typed SDK client for the session's profile. The bearer, the refresh and the
// single retry on expired_token live in the transport, not in the SDK's own auth hooks, so the
// SDK sees an ordinary http.Client.
func (s *Session) SDK(userAgent string) *sdk.APIClient {
	cfg := sdk.NewConfiguration()
	cfg.Servers = sdk.ServerConfigurations{{URL: s.Profile.Base()}}
	cfg.OperationServers = nil
	cfg.UserAgent = userAgent
	cfg.HTTPClient = &http.Client{Transport: &bearerTransport{s: s}, Timeout: s.HC.Timeout}
	return sdk.NewAPIClient(cfg)
}

type bearerTransport struct{ s *Session }

func (t *bearerTransport) send(req *http.Request, token string) (*http.Response, error) {
	r := req.Clone(req.Context())
	r.Header.Set("Authorization", "Bearer "+token)
	resp, err := t.s.HC.Do(r)
	if err != nil {
		return nil, apierr.Unavailable("cannot reach %s: %v", t.s.Profile.Base(), err)
	}
	return resp, nil
}

// RoundTrip sends a GET with the bearer. A 401 expired_token refreshes and retries exactly once;
// invalid_audience is never retried. Every call here is a bodyless GET, so a replay is safe.
func (t *bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	c, err := t.s.Fresh(req.Context())
	if err != nil {
		return nil, err
	}
	resp, err := t.send(req, c.AccessToken)
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		return resp, err
	}
	body, rerr := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	_ = resp.Body.Close()
	if rerr != nil {
		return nil, rerr
	}
	if errorWord(body) != "expired_token" {
		resp.Body = io.NopCloser(bytes.NewReader(body))
		return resp, nil
	}
	if c, err = t.s.Refresh(req.Context(), c); err != nil {
		return nil, err
	}
	return t.send(req, c.AccessToken)
}
