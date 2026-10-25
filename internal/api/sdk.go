package api

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"

	sdk "github.com/scientist-labs/pharmaobservatory-sdks/go"

	"github.com/scientist-labs/pharmaobservatory-cli/internal/apierr"
)

// SDK builds the typed SDK client for the session's profile. The bearer, the refresh and the
// single retry on expired_token live in the transport, not in the SDK's own auth hooks, so the
// SDK sees an ordinary http.Client. The User-Agent is not set here: s.HC's transport sets it on
// every request (WithUserAgent), raw Session calls included.
//
// The outer client deliberately has no Timeout. The --wait sleep runs inside RoundTrip, so an
// outer Client.Timeout would cover the first attempt, the sleep and the retry together, and any
// Retry-After near or over it would end in "context deadline exceeded" (#47). The timeout lives on
// s.HC instead, which bearerTransport.send uses for every attempt: each attempt, headers and body,
// gets its own s.HC.Timeout. The sleep is bounded by apierr.MaxWait and by the request context.
func (s *Session) SDK() *sdk.APIClient {
	cfg := sdk.NewConfiguration()
	cfg.Servers = sdk.ServerConfigurations{{URL: s.Profile.Base()}}
	cfg.OperationServers = nil
	cfg.HTTPClient = &http.Client{Transport: &bearerTransport{s: s}, CheckRedirect: apierr.SecureRedirect}
	return sdk.NewAPIClient(cfg)
}

type bearerTransport struct{ s *Session }

func (t *bearerTransport) send(req *http.Request, token string) (*http.Response, error) {
	r := req.Clone(req.Context())
	r.Header.Set("Authorization", "Bearer "+token)
	resp, err := t.s.HC.Do(r)
	if err != nil {
		if u := apierr.UsageCause(err); u != nil {
			return nil, u
		}
		return nil, apierr.Unavailable("cannot reach %s: %v", t.s.Profile.Base(), err)
	}
	return resp, nil
}

type queryKey struct{}

// WithQuery returns ctx carrying q, which the SDK transport appends to the query string of every
// request made with it. It is how the hand-written --filter reaches a typed SDK call whose
// document cannot declare column-keyed parameters.
func WithQuery(ctx context.Context, q url.Values) context.Context {
	if len(q) == 0 {
		return ctx
	}
	return context.WithValue(ctx, queryKey{}, q)
}

func withExtraQuery(req *http.Request) *http.Request {
	q, _ := req.Context().Value(queryKey{}).(url.Values)
	if len(q) == 0 {
		return req
	}
	r := req.Clone(req.Context())
	u := *r.URL
	if u.RawQuery == "" {
		u.RawQuery = q.Encode()
	} else {
		u.RawQuery += "&" + q.Encode()
	}
	r.URL = &u
	return r
}

// RoundTrip sends a GET with the bearer. A 401 expired_token refreshes and retries exactly once;
// invalid_audience is never retried. Under Wait a 429 with a usable Retry-After sleeps and retries
// once. Every call here is a bodyless GET, so a replay is safe.
func (t *bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = withExtraQuery(req)
	resp, err := t.authed(req)
	if err != nil || !t.s.Wait || resp.StatusCode != http.StatusTooManyRequests {
		return resp, err
	}
	d, ok := t.s.waitDelay(resp.StatusCode, resp.Header)
	if !ok {
		return resp, nil
	}
	_ = resp.Body.Close()
	if err := t.s.pause(req.Context(), d); err != nil {
		return nil, err
	}
	return t.authed(req)
}

func (t *bearerTransport) authed(req *http.Request) (*http.Response, error) {
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
