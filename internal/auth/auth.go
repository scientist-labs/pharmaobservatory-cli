// Package auth implements the OAuth authorization code + PKCE client with the stdlib only
// (x/oauth2 cannot send resource= on refresh): discovery, PKCE, loopback and paste-back
// callbacks, token exchange and refresh, and the audience check.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/scientist-labs/pharmaobservatory-cli/internal/apierr"
	"github.com/scientist-labs/pharmaobservatory-cli/internal/auth/store"
)

// DefaultPort is the loopback port the WorkOS application names.
const DefaultPort = 53682

// baseScopes are always requested; the API scopes come from discovery.
var baseScopes = []string{"openid", "profile", "email", "offline_access"}

// Endpoints is what discovery yields.
type Endpoints struct {
	Authorize string
	Token     string
	Scopes    []string // scopes_supported of the protected resource
}

func getJSON(ctx context.Context, hc *http.Client, u string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return apierr.Unavailable("cannot reach %s: %v", u, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return apierr.Unavailable("discovery failed: GET %s answered HTTP %d", u, resp.StatusCode)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("discovery: %s is not JSON: %w", u, err)
	}
	return nil
}

// Discover reads the protected-resource document at base and the first authorization server's
// metadata.
func Discover(ctx context.Context, hc *http.Client, base string) (Endpoints, error) {
	if err := apierr.RequireSecure("origin", base); err != nil {
		return Endpoints{}, err
	}
	var prm struct {
		AuthorizationServers []string `json:"authorization_servers"`
		ScopesSupported      []string `json:"scopes_supported"`
	}
	if err := getJSON(ctx, hc, base+"/.well-known/oauth-protected-resource/api/v1", &prm); err != nil {
		return Endpoints{}, err
	}
	if len(prm.AuthorizationServers) == 0 {
		return Endpoints{}, errors.New("discovery: no authorization_servers advertised")
	}
	issuer := strings.TrimSuffix(prm.AuthorizationServers[0], "/")
	if err := apierr.RequireSecure("authorization server", issuer); err != nil {
		return Endpoints{}, err
	}
	var meta struct {
		AuthorizationEndpoint string `json:"authorization_endpoint"`
		TokenEndpoint         string `json:"token_endpoint"`
	}
	if err := getJSON(ctx, hc, issuer+"/.well-known/oauth-authorization-server", &meta); err != nil {
		return Endpoints{}, err
	}
	if meta.AuthorizationEndpoint == "" || meta.TokenEndpoint == "" {
		return Endpoints{}, errors.New("discovery: authorization server metadata lacks authorize or token endpoint")
	}
	if err := apierr.RequireSecure("authorize endpoint", meta.AuthorizationEndpoint); err != nil {
		return Endpoints{}, err
	}
	if err := apierr.RequireSecure("token endpoint", meta.TokenEndpoint); err != nil {
		return Endpoints{}, err
	}
	return Endpoints{Authorize: meta.AuthorizationEndpoint, Token: meta.TokenEndpoint, Scopes: prm.ScopesSupported}, nil
}

// ScopeString is the base scopes plus every discovered scope, de-duplicated, order kept.
func ScopeString(discovered []string) string {
	seen := map[string]bool{}
	var out []string
	for _, s := range append(append([]string{}, baseScopes...), discovered...) {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return strings.Join(out, " ")
}

func b64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// NewPKCE returns a verifier from 48 random bytes and its S256 challenge.
func NewPKCE() (verifier, challenge string, err error) {
	b := make([]byte, 48)
	if _, err = rand.Read(b); err != nil {
		return "", "", err
	}
	verifier = b64url(b)
	return verifier, Challenge(verifier), nil
}

// Challenge is the S256 code challenge for a verifier.
func Challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return b64url(sum[:])
}

// NewState returns a random nonce.
func NewState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// RedirectURI is the loopback redirect: host literal 127.0.0.1, path /callback.
func RedirectURI(port int) string { return fmt.Sprintf("http://127.0.0.1:%d/callback", port) }

// AuthorizeParams are the inputs to the authorize URL.
type AuthorizeParams struct {
	Endpoint, ClientID, RedirectURI, Scope, State, Challenge, Resource string
}

// AuthorizeURL composes the authorize URL.
func AuthorizeURL(p AuthorizeParams) string {
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", p.ClientID)
	q.Set("redirect_uri", p.RedirectURI)
	q.Set("scope", p.Scope)
	q.Set("state", p.State)
	q.Set("code_challenge", p.Challenge)
	q.Set("code_challenge_method", "S256")
	q.Set("resource", p.Resource)
	sep := "?"
	if strings.Contains(p.Endpoint, "?") {
		sep = "&"
	}
	return p.Endpoint + sep + q.Encode()
}

// CallbackError is an error reported on the redirect.
func callbackErr(q url.Values) error {
	if e := q.Get("error"); e != "" {
		msg := "authorization failed: " + apierr.Sanitize(e)
		if d := apierr.Sanitize(q.Get("error_description")); d != "" {
			msg += " (" + d + ")"
		}
		return apierr.Auth("%s", msg)
	}
	return nil
}

// CodeFromQuery validates a callback's query: error first, then state, then code.
func CodeFromQuery(q url.Values, state string) (string, error) {
	if err := callbackErr(q); err != nil {
		return "", err
	}
	if q.Get("state") != state {
		return "", apierr.Auth("state mismatch: refusing the callback; nothing was stored")
	}
	if q.Get("code") == "" {
		return "", apierr.Auth("no authorization code received")
	}
	return q.Get("code"), nil
}

// ParsePasted accepts the full callback URL (state is checked) or a bare code.
func ParsePasted(pasted, state string) (string, error) {
	pasted = strings.TrimSpace(pasted)
	if pasted == "" {
		return "", apierr.Usage(errors.New("nothing pasted"))
	}
	if strings.HasPrefix(pasted, "http://") || strings.HasPrefix(pasted, "https://") {
		u, err := url.Parse(pasted)
		if err != nil {
			return "", apierr.Usage(fmt.Errorf("cannot parse the pasted URL: %w", err))
		}
		return CodeFromQuery(u.Query(), state)
	}
	return pasted, nil
}

// CallbackHandler answers the loopback redirect and hands the result to ch exactly once.
func CallbackHandler(state string, ch chan<- Result) http.Handler {
	var once sync.Once
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		code, err := CodeFromQuery(r.URL.Query(), state)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprintln(w, "Sign-in failed. Return to the terminal.")
		} else {
			_, _ = fmt.Fprintln(w, "Signed in. You can close this tab.")
		}
		once.Do(func() { ch <- Result{Code: code, Err: err} })
	})
	return mux
}

// Result is the outcome of a callback.
type Result struct {
	Code string
	Err  error
}

// Listen binds the loopback port on 127.0.0.1.
func Listen(port int) (net.Listener, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, fmt.Errorf("cannot listen on 127.0.0.1:%d (%v); try --port or --no-browser", port, err)
	}
	return ln, nil
}

// WaitCallback serves ln until one callback arrives, ctx ends or timeout passes.
func WaitCallback(ctx context.Context, ln net.Listener, state string, timeout time.Duration) (string, error) {
	ch := make(chan Result, 1)
	srv := &http.Server{Handler: CallbackHandler(state, ch), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	select {
	case r := <-ch:
		return r.Code, r.Err
	case <-time.After(timeout):
		return "", apierr.Auth("timed out waiting for the browser; re-run, or use --no-browser")
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Error        string `json:"error"`
	Description  string `json:"error_description"`
}

func postToken(ctx context.Context, hc *http.Client, endpoint string, form url.Values) (tokenResponse, error) {
	var tr tokenResponse
	if err := apierr.RequireSecure("token endpoint", endpoint); err != nil {
		return tr, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return tr, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return tr, apierr.Unavailable("cannot reach the token endpoint: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	_ = json.Unmarshal(body, &tr)
	if resp.StatusCode != http.StatusOK || tr.AccessToken == "" {
		if resp.StatusCode >= 500 {
			return tr, apierr.Unavailable("token endpoint answered HTTP %d", resp.StatusCode)
		}
		msg := fmt.Sprintf("token request refused: HTTP %d", resp.StatusCode)
		if tr.Error != "" {
			msg += " " + apierr.Sanitize(tr.Error)
		}
		if d := apierr.Sanitize(tr.Description); d != "" {
			msg += " (" + d + ")"
		}
		return tr, apierr.Auth("%s", msg)
	}
	return tr, nil
}

// Claims decodes the claim names of a JWT payload (no signature check: the server verifies).
func Claims(token string) (map[string]any, error) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return nil, errors.New("access token is not a JWT")
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return nil, fmt.Errorf("access token payload: %w", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("access token payload: %w", err)
	}
	return m, nil
}

// HasAudience reports whether the claims' aud (string or array) contains resource.
func HasAudience(claims map[string]any, resource string) bool {
	switch v := claims["aud"].(type) {
	case string:
		return v == resource
	case []any:
		for _, a := range v {
			if s, ok := a.(string); ok && s == resource {
				return true
			}
		}
	}
	return false
}

// Client performs the token requests for one profile.
type Client struct {
	HC       *http.Client
	Token    string // token endpoint
	ClientID string
	Resource string
	Now      func() time.Time
}

func (c Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c Client) credential(tr tokenResponse, prevRefresh string) (store.Credential, error) {
	claims, err := Claims(tr.AccessToken)
	if err != nil {
		return store.Credential{}, err
	}
	if !HasAudience(claims, c.Resource) {
		return store.Credential{}, apierr.Auth("the access token's audience is not %s; nothing was stored", c.Resource)
	}
	sub, _ := claims["sub"].(string)
	refresh := tr.RefreshToken
	if refresh == "" {
		refresh = prevRefresh
	}
	now := c.now()
	return store.Credential{
		RefreshToken: refresh,
		AccessToken:  tr.AccessToken,
		ExpiresAt:    now.Add(time.Duration(tr.ExpiresIn) * time.Second),
		Sub:          sub,
		Aud:          c.Resource,
		ClientID:     c.ClientID,
		ObtainedAt:   now,
	}, nil
}

// Exchange redeems an authorization code with the PKCE verifier.
func (c Client) Exchange(ctx context.Context, code, verifier, redirectURI string) (store.Credential, error) {
	tr, err := postToken(ctx, c.HC, c.Token, url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {c.ClientID},
		"code":          {code},
		"code_verifier": {verifier},
		"redirect_uri":  {redirectURI},
		"resource":      {c.Resource},
	})
	if err != nil {
		return store.Credential{}, err
	}
	if tr.RefreshToken == "" {
		return store.Credential{}, apierr.Auth("the server returned no refresh token; nothing was stored")
	}
	return c.credential(tr, "")
}

// Refresh redeems a refresh token, sending resource= again.
func (c Client) Refresh(ctx context.Context, refreshToken string) (store.Credential, error) {
	tr, err := postToken(ctx, c.HC, c.Token, url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {c.ClientID},
		"refresh_token": {refreshToken},
		"resource":      {c.Resource},
	})
	if err != nil {
		return store.Credential{}, err
	}
	return c.credential(tr, refreshToken)
}
