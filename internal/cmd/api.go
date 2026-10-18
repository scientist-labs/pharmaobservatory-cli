package cmd

import (
	"fmt"
	"io"
	"net/url"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/scientist-labs/pharmaobservatory-cli/internal/apierr"
	"github.com/scientist-labs/pharmaobservatory-cli/internal/cmd/pages"
)

func userAgent() string { return fmt.Sprintf("po/%s (%s/%s)", version, runtime.GOOS, runtime.GOARCH) }

// pageEnv hands the generated page commands a session-backed SDK client.
func pageEnv(g *globals, d Deps) *pages.Env {
	return &pages.Env{
		Client: func() (*pages.Client, error) {
			s, err := g.session(d)
			if err != nil {
				return nil, err
			}
			return &pages.Client{SDK: s.SDK(userAgent()), Profile: s.Profile.Name}, nil
		},
		JSON: func() bool { return g.json },
	}
}

// apiPath turns the argument into a request path: a path starting with "/" is used as given,
// anything else is relative to /api/v1/. A full URL is refused so the bearer only ever goes to
// the profile's own origin.
func apiPath(arg string, fields []string) (string, error) {
	if strings.Contains(arg, "://") {
		return "", apierr.Usage(fmt.Errorf("give a path, not a URL: the token is only sent to the profile origin"))
	}
	p := arg
	if !strings.HasPrefix(p, "/") {
		p = "/api/v1/" + p
	}
	if len(fields) == 0 {
		return p, nil
	}
	q := url.Values{}
	for _, f := range fields {
		k, v, ok := strings.Cut(f, "=")
		if !ok || k == "" {
			return "", apierr.Usage(fmt.Errorf("-F wants key=value, got %q", f))
		}
		q.Add(k, v)
	}
	sep := "?"
	if strings.Contains(p, "?") {
		sep = "&"
	}
	return p + sep + q.Encode(), nil
}

func newAPICmd(g *globals, d Deps) *cobra.Command {
	var fields []string
	c := &cobra.Command{
		Use:   "api <path>",
		Short: "Make an authenticated GET to the API and print the body",
		Long: "Make an authenticated GET to <origin>/api/v1/<path> and print the response body.\n\n" +
			"A path starting with / is used as given (for example /api/v1/whoami). The request goes to the\n" +
			"profile origin only. Use -F key=value, repeatable, to add query parameters.\n\n" +
			"Example: po api biomarkers -F kind=gene -F limit=5",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 1 {
				return apierr.Usage(fmt.Errorf("api needs exactly one path"))
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := apiPath(args[0], fields)
			if err != nil {
				return err
			}
			s, err := g.session(d)
			if err != nil {
				return err
			}
			body, err := s.Get(ctxOf(cmd.Context()), path)
			if err != nil {
				if len(body) > 0 {
					writeRaw(cmd.OutOrStdout(), body)
				}
				return err
			}
			writeRaw(cmd.OutOrStdout(), body)
			return nil
		},
	}
	c.Flags().StringArrayVarP(&fields, "field", "F", nil, "add a query parameter, key=value (repeatable)")
	return c
}

func writeRaw(w io.Writer, body []byte) {
	_, _ = w.Write(body)
	if len(body) == 0 || body[len(body)-1] != '\n' {
		_, _ = io.WriteString(w, "\n")
	}
}
