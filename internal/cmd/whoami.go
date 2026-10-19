package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/scientist-labs/pharmaobservatory-cli/internal/output"
	"github.com/scientist-labs/pharmaobservatory-cli/internal/sanitize"

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
			if g.json {
				// The body exactly as the server sent it, error or not.
				if len(body) > 0 {
					writeRaw(out, body)
				}
				return err
			}
			if err != nil {
				return err
			}
			text, err := whoamiText(s.Profile.Name, s.Profile.Origin, body)
			if err != nil {
				return err
			}
			_, _ = io.WriteString(out, text)
			return nil
		},
	}
}

// Placeholders for a field the server sent as null or did not send. They are fixed CLI text, so they
// cannot be mistaken for a value.
const (
	notSent   = "(not provided)"
	noneOrg   = "(none: this login is not tied to an organization)"
	nonePlan  = "(none: no current subscription)"
	noneQuota = "(not reported)"
)

// whoamiBody is GET /api/v1/whoami (#5082). Fields are loose (any) where the document leaves them
// untyped or nullable, so a null or an unexpected type renders as a placeholder instead of failing.
type whoamiBody struct {
	Sub          any            `json:"sub"`
	Roles        []any          `json:"roles"`
	Name         any            `json:"name"`
	Email        any            `json:"email"`
	Organization map[string]any `json:"organization"`
	Plan         map[string]any `json:"plan"`
	Quota        *struct {
		KeyedBy any                       `json:"keyed_by"`
		Buckets map[string]map[string]any `json:"buckets"`
	} `json:"quota"`
}

// bucketOrder is the order the server documents its buckets in; any other bucket follows, sorted.
var bucketOrder = []string{"requests_per_minute", "requests_per_day", "rows_per_day", "query_seconds_per_day", "rwd_catalogue_calls_per_day", "benzinga_calls_per_day"}

// val renders one server value on one sanitized line, or placeholder when it is null, missing or
// blank.
func val(v any, placeholder string) string {
	s := strings.TrimSpace(sanitize.Line(output.Cell(v)))
	if s == "" {
		return placeholder
	}
	return s
}

// whoamiText is the human form: profile and origin, then who, the organization, the plan, the roles
// and the remaining quota per bucket. Every server value goes through sanitize.Line.
func whoamiText(profile, origin string, body []byte) (string, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var w whoamiBody
	if err := dec.Decode(&w); err != nil {
		return "", fmt.Errorf("whoami: unreadable response: %w", err)
	}
	var b strings.Builder
	line := func(label, value string) { fmt.Fprintf(&b, "%-9s%s\n", label+":", value) }
	line("profile", sanitize.Line(profile))
	line("origin", sanitize.Line(origin))
	line("sub", val(w.Sub, notSent))
	line("name", val(w.Name, notSent))
	line("email", val(w.Email, notSent))
	org := noneOrg
	if w.Organization != nil {
		org = val(w.Organization["name"], notSent)
	}
	line("org", org)
	plan := nonePlan
	if w.Plan != nil {
		plan = val(w.Plan["name"], notSent)
		var notes []string
		if st := val(w.Plan["status"], ""); st != "" {
			notes = append(notes, st)
		}
		if e, ok := w.Plan["entitled"].(bool); ok && !e {
			notes = append(notes, "not entitled")
		}
		if len(notes) > 0 {
			plan += " (" + strings.Join(notes, ", ") + ")"
		}
	}
	line("plan", plan)
	roles := make([]string, 0, len(w.Roles))
	for _, r := range w.Roles {
		roles = append(roles, val(r, ""))
	}
	line("roles", val(strings.Join(roles, ", "), notSent))
	if w.Quota == nil || len(w.Quota.Buckets) == 0 {
		line("quota", noneQuota)
		return b.String(), nil
	}
	line("quota", "remaining per bucket, keyed by "+val(w.Quota.KeyedBy, notSent))
	var names []string
	seen := map[string]bool{}
	for _, n := range bucketOrder {
		if _, ok := w.Quota.Buckets[n]; ok {
			names = append(names, n)
			seen[n] = true
		}
	}
	var rest []string
	for n := range w.Quota.Buckets {
		if !seen[n] {
			rest = append(rest, n)
		}
	}
	sort.Strings(rest)
	names = append(names, rest...)
	width := 0
	for _, n := range names {
		if l := len(sanitize.Line(n)); l > width {
			width = l
		}
	}
	for _, n := range names {
		q := w.Quota.Buckets[n]
		if q == nil {
			fmt.Fprintf(&b, "  %-*s  %s\n", width, sanitize.Line(n), notSent)
			continue
		}
		s := val(q["remaining"], "?") + " of " + val(q["limit"], "?")
		if u := val(q["unit"], ""); u != "" {
			s += " " + u
		}
		s += " left"
		if win := val(q["window"], ""); win != "" {
			s += " " + windowPhrase(win)
		}
		if at := val(q["resets_at"], ""); at != "" {
			s += "; resets " + at
		}
		fmt.Fprintf(&b, "  %-*s  %s\n", width, sanitize.Line(n), s)
	}
	return b.String(), nil
}

// windowPhrase reads "1 day" as "per 1 day", and leaves a window that already says "per" alone.
func windowPhrase(w string) string {
	if strings.HasPrefix(w, "per ") {
		return w
	}
	return "per " + w
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
