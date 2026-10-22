package api

import "net/http"

// UserAgentTransport sets User-Agent on every request it sends, replacing whatever the caller
// (the generated SDK, net/http's default "Go-http-client/1.1") put there. The server tells CLI
// traffic apart by this header, so every client in the CLI goes through it: see WithUserAgent.
type UserAgentTransport struct {
	UA   string
	Base http.RoundTripper // nil means http.DefaultTransport
}

// RoundTrip clones req (a RoundTripper must not modify its request) and sets the header.
func (t *UserAgentTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r := req.Clone(req.Context())
	r.Header.Set("User-Agent", t.UA)
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(r)
}

// WithUserAgent returns a shallow copy of hc (nil means a zero http.Client) whose transport sets
// ua on every request. Timeout, CheckRedirect and Jar are kept. An hc already wrapped is
// re-pointed at ua rather than wrapped twice.
func WithUserAgent(hc *http.Client, ua string) *http.Client {
	c := &http.Client{}
	if hc != nil {
		*c = *hc
	}
	if t, ok := c.Transport.(*UserAgentTransport); ok {
		c.Transport = &UserAgentTransport{UA: ua, Base: t.Base}
		return c
	}
	c.Transport = &UserAgentTransport{UA: ua, Base: c.Transport}
	return c
}
