package cmd

import (
	"bufio"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/scientist-labs/pharmaobservatory-cli/internal/apierr"
	"github.com/scientist-labs/pharmaobservatory-cli/internal/auth"
	"github.com/scientist-labs/pharmaobservatory-cli/internal/auth/store"
	"github.com/scientist-labs/pharmaobservatory-cli/internal/config"
)

func newLoginCmd(g *globals, d Deps) *cobra.Command {
	var (
		noBrowser bool
		port      int
		origin    string
		clientID  string
	)
	c := &cobra.Command{
		Use:   "login",
		Short: "Sign in with your browser and store a refresh token",
		Long: "Sign in with the browser authorization code flow (PKCE). With --no-browser the authorize URL is\n" +
			"printed; sign in on any machine, then paste the full URL the browser fails to load, or just its code.\n" +
			"Create or update a custom profile with --profile NAME --origin URL [--client-id ID] [--api-base URL].",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			p, f, err := g.resolveForLogin(origin, clientID)
			if err != nil {
				return apierr.Usage(err)
			}
			ctx := ctxOf(cmd.Context())
			ep, err := auth.Discover(ctx, d.HC, p.Base())
			if err != nil {
				return err
			}
			verifier, challenge, err := auth.NewPKCE()
			if err != nil {
				return err
			}
			state, err := auth.NewState()
			if err != nil {
				return err
			}
			if port == 0 {
				port = auth.DefaultPort
			}
			redirect := auth.RedirectURI(port)
			authURL := auth.AuthorizeURL(auth.AuthorizeParams{
				Endpoint: ep.Authorize, ClientID: p.ClientID, RedirectURI: redirect,
				Scope: auth.ScopeString(ep.Scopes), State: state, Challenge: challenge, Resource: p.Resource(),
			})

			var code string
			if noBrowser {
				_, _ = fmt.Fprintf(out, "Open this URL in any browser and sign in:\n\n%s\n\n", authURL)
				_, _ = fmt.Fprintf(out, "The browser will then fail to load %s?code=...&state=... .\nPaste that full URL (or just the code) here: ", redirect)
				line, _ := bufio.NewReader(d.Stdin).ReadString('\n')
				if code, err = auth.ParsePasted(line, state); err != nil {
					return err
				}
			} else {
				ln, err := auth.Listen(port)
				if err != nil {
					return apierr.Usage(err)
				}
				_, _ = fmt.Fprintf(out, "Opening the browser (if nothing opens, re-run with --no-browser):\n%s\n", authURL)
				if err := d.OpenBrowser(authURL); err != nil {
					_, _ = fmt.Fprintf(out, "Could not open a browser (%v); open the URL above yourself.\n", err)
				}
				if code, err = auth.WaitCallback(ctx, ln, state, d.CallbackWait); err != nil {
					return err
				}
			}

			cl := auth.Client{HC: d.HC, Token: ep.Token, ClientID: p.ClientID, Resource: p.Resource(), Now: d.Now}
			cred, err := cl.Exchange(ctx, code, verifier, redirect)
			if err != nil {
				return err
			}
			st, err := store.Open(d.Keyring, p.Name)
			if err != nil {
				return err
			}
			if err := st.Set(p.Name, cred); err != nil {
				if st.Name() == "file" {
					return err
				}
				_, _ = fmt.Fprintf(out, "Keychain unavailable (%v); using the credentials file instead.\n", err)
				dir, derr := config.EnsureDir()
				if derr != nil {
					return derr
				}
				st = store.FileStore{Dir: dir}
				if err := st.Set(p.Name, cred); err != nil {
					return err
				}
			}
			if origin != "" || clientID != "" || g.apiBase != "" {
				config.Upsert(&f, p.Name, config.Profile{Origin: origin, ClientID: clientID, APIBase: g.apiBase})
				if err := config.Save(f); err != nil {
					return err
				}
			}
			_, _ = fmt.Fprintf(out, "Logged in to %s (%s) as %s. Credential stored in the %s.\n", p.Name, p.Origin, cred.Sub, st.Name())
			return nil
		},
	}
	c.Flags().BoolVar(&noBrowser, "no-browser", false, "print the authorize URL and read the redirect URL or code from stdin")
	c.Flags().IntVar(&port, "port", auth.DefaultPort, "loopback port for the redirect")
	c.Flags().StringVar(&origin, "origin", "", "create or update a custom profile with this origin")
	c.Flags().StringVar(&clientID, "client-id", "", "client id for a custom profile (default: the staging id)")
	return c
}

// resolveForLogin resolves the profile, letting --origin define a new custom one.
func (g *globals) resolveForLogin(origin, clientID string) (config.Profile, config.File, error) {
	f, err := config.Load()
	if err != nil {
		return config.Profile{}, f, err
	}
	name := config.SelectName(g.profile, f)
	trial := f
	if origin != "" || clientID != "" {
		trial = config.File{DefaultProfile: f.DefaultProfile, Profiles: map[string]config.Profile{}}
		for k, v := range f.Profiles {
			trial.Profiles[k] = v
		}
		config.Upsert(&trial, name, config.Profile{Origin: origin, ClientID: clientID})
	}
	p, err := config.Resolve(name, trial)
	if err != nil {
		return p, f, err
	}
	if g.apiBase != "" {
		p.APIBase = g.apiBase
	}
	return p, f, nil
}
