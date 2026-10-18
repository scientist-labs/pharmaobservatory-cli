package cmd

import (
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/scientist-labs/pharmaobservatory-cli/internal/api"
	"github.com/scientist-labs/pharmaobservatory-cli/internal/auth/store"
	"github.com/scientist-labs/pharmaobservatory-cli/internal/config"
)

// Deps are the seams tests replace: HTTP, keychain, browser, stdin, clock, callback timeout.
type Deps struct {
	HC           *http.Client
	Keyring      store.Keyring
	OpenBrowser  func(url string) error
	Stdin        io.Reader
	Now          func() time.Time
	CallbackWait time.Duration
	DefaultPort  int
}

func defaultDeps() Deps {
	return Deps{
		HC:           &http.Client{Timeout: 30 * time.Second},
		Keyring:      store.SystemKeyring(),
		OpenBrowser:  openBrowser,
		Stdin:        os.Stdin,
		Now:          time.Now,
		CallbackWait: 5 * time.Minute,
	}
}

func openBrowser(url string) error {
	var c *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		c = exec.Command("open", url)
	case "windows":
		c = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		c = exec.Command("xdg-open", url)
	}
	if err := c.Start(); err != nil {
		return err
	}
	go func() { _ = c.Wait() }()
	return nil
}

// globals are the persistent flags.
type globals struct {
	profile  string
	apiBase  string
	json     bool
	csv      bool
	all      bool
	quiet    bool
	noFooter bool
}

// resolve picks and resolves the profile for a command, applying the one-off --api-base.
func (g *globals) resolve() (config.Profile, config.File, error) {
	f, err := config.Load()
	if err != nil {
		return config.Profile{}, f, err
	}
	p, err := config.Resolve(config.SelectName(g.profile, f), f)
	if err != nil {
		return p, f, err
	}
	if g.apiBase != "" {
		p.APIBase = g.apiBase
	}
	return p, f, nil
}

func (g *globals) session(d Deps) (*api.Session, error) {
	p, _, err := g.resolve()
	if err != nil {
		return nil, err
	}
	st, err := store.Open(d.Keyring, p.Name)
	if err != nil {
		return nil, err
	}
	return &api.Session{Profile: p, Store: st, HC: d.HC, Now: d.Now}, nil
}

func ctxOf(cmdCtx context.Context) context.Context {
	if cmdCtx == nil {
		return context.Background()
	}
	return cmdCtx
}
