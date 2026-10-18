package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/scientist-labs/pharmaobservatory-cli/internal/apierr"
	"github.com/scientist-labs/pharmaobservatory-cli/internal/auth/store"
)

func newWhoamiCmd(g *globals, d Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Show who the stored login is, as the API sees it",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := g.session(d)
			if err != nil {
				return err
			}
			body, err := s.Get(ctxOf(cmd.Context()), "/api/v1/whoami")
			out := cmd.OutOrStdout()
			if err != nil {
				if g.json && len(body) > 0 {
					_, _ = fmt.Fprintln(out, strings.TrimSpace(string(body)))
				}
				return err
			}
			if g.json {
				_, _ = fmt.Fprintln(out, strings.TrimSpace(string(body)))
				return nil
			}
			var w struct {
				Sub   string   `json:"sub"`
				Roles []string `json:"roles"`
			}
			if err := json.Unmarshal(body, &w); err != nil {
				return fmt.Errorf("whoami: unreadable response: %w", err)
			}
			_, _ = fmt.Fprintf(out, "profile: %s\norigin:  %s\nsub:     %s\nroles:   %s\n",
				s.Profile.Name, s.Profile.Origin, apierr.Sanitize(w.Sub), apierr.Sanitize(strings.Join(w.Roles, ", ")))
			return nil
		},
	}
}

func newLogoutCmd(g *globals, d Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Remove the stored credential for the profile",
		Long:  "Removes the stored credential locally. The refresh token is not revoked server-side.",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, _, err := g.resolve()
			if err != nil {
				return err
			}
			// Remove from every store that holds it: a login that fell back to the file and a
			// later one that used the keychain can leave the profile in both.
			stores, err := store.All(d.Keyring, p.Name)
			if err != nil {
				return err
			}
			var removed []string
			for _, st := range stores {
				_, gerr := st.Get(p.Name)
				if errors.Is(gerr, store.ErrNotFound) {
					continue
				}
				derr := st.Delete(p.Name)
				if gerr != nil {
					// Unreadable store (a broken keychain): best effort, the other store still runs.
					continue
				}
				if derr != nil {
					return derr
				}
				removed = append(removed, st.Name())
			}
			if len(removed) == 0 {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Not logged in to %s; nothing to remove.\n", p.Name)
				return nil
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Logged out of %s (removed from the %s).\n", p.Name, strings.Join(removed, " and the "))
			return nil
		},
	}
}

// newAuthCmd is the hidden `po auth refresh`, to force a refresh and check the audience.
func newAuthCmd(g *globals, d Deps) *cobra.Command {
	auth := &cobra.Command{Use: "auth", Short: "Credential maintenance", Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return apierr.Usage(fmt.Errorf("unknown auth subcommand %q", args[0]))
			}
			return cmd.Help()
		}}
	auth.AddCommand(&cobra.Command{
		Use:   "refresh",
		Short: "Force a refresh now and verify the audience",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := g.session(d)
			if err != nil {
				return err
			}
			c, err := s.Load()
			if err != nil {
				return err
			}
			n, err := s.Refresh(ctxOf(cmd.Context()), c)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Refreshed %s: aud %s, access token expires %s.\n",
				s.Profile.Name, n.Aud, n.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z"))
			return nil
		},
	})
	return auth
}
