// Package pages builds the page command tree. The per-page operations are generated into
// zz_generated_*.go by internal/gen from the pinned SDK module; this file is the hand-written
// runtime they plug into.
package pages

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	sdk "github.com/scientist-labs/pharmaobservatory-sdks/go"

	"github.com/scientist-labs/pharmaobservatory-cli/internal/apierr"
)

// Op is one generated operation: its flags, its help and the typed SDK call.
type Op struct {
	Key     string
	Short   string
	Path    string
	Escaped bool
	Flags   *pflag.FlagSet
	Call    func(ctx context.Context, cl *sdk.APIClient, id string, fs *pflag.FlagSet) (*http.Response, error)
}

// PageSpec is one page: its operations by kind.
type PageSpec struct {
	Slug       string
	Escaped    bool
	List, Get  *Op
	IDSections []*Op // GET /api/v1/{page}/{id}/{key}
	Sections   []*Op // GET /api/v1/{page}/{key}
}

// OpRef names a generated operation without its code, for the tests.
type OpRef struct{ Page, Kind, Key, Path string }

// Client is a session's SDK client plus the profile name that error messages quote.
type Client struct {
	SDK     *sdk.APIClient
	Profile string
}

// Env carries what the generated commands need from the hand-written side.
type Env struct {
	Client func() (*Client, error)
	// JSON reports --json. Output is the raw body either way until tables land.
	JSON func() bool
}

// Build returns the top-level commands for every page. A page whose slug collides with a
// top-level command is registered only under `po page <slug>`.
func Build(e *Env) []*cobra.Command { return buildFrom(e, allPages()) }

func buildFrom(e *Env, specs []*PageSpec) []*cobra.Command {
	var top []*cobra.Command
	escape := &cobra.Command{
		Use:   "page <slug>",
		Short: "Reach a page whose name is also a command",
		Args:  unknownArgs,
		Run:   func(c *cobra.Command, _ []string) { _ = c.Help() },
	}
	for _, s := range specs {
		c := pageCmd(e, s)
		if s.Escaped {
			escape.AddCommand(c)
			continue
		}
		top = append(top, c)
	}
	if escape.HasSubCommands() {
		top = append(top, escape)
	}
	return top
}

func unknownArgs(c *cobra.Command, args []string) error {
	if len(args) > 0 {
		return apierr.Usage(fmt.Errorf("unknown command %q for %q\nRun '%s --help' for usage", args[0], c.CommandPath(), c.CommandPath()))
	}
	return nil
}

func pageCmd(e *Env, s *PageSpec) *cobra.Command {
	page := &cobra.Command{
		Use:   s.Slug,
		Short: "Query the " + s.Slug + " page",
		Args:  unknownArgs,
		Run:   func(c *cobra.Command, _ []string) { _ = c.Help() },
	}
	var long strings.Builder
	fmt.Fprintf(&long, "Query the %s page of the API.\n", s.Slug)
	if len(s.IDSections) > 0 {
		long.WriteString("\nSections of a record (po " + s.Slug + " get <id> <section>):\n")
		for _, o := range sortedOps(s.IDSections) {
			fmt.Fprintf(&long, "  %-34s %s\n", o.Key, o.Short)
		}
	}
	page.Long = strings.TrimRight(long.String(), "\n")
	if s.List != nil {
		page.AddCommand(opCmd(e, s.List, "list", nil))
	}
	if s.Get != nil || len(s.IDSections) > 0 {
		page.AddCommand(getCmd(e, s))
	}
	var escaped []*Op
	for _, o := range sortedOps(s.Sections) {
		if o.Escaped {
			escaped = append(escaped, o)
			continue
		}
		page.AddCommand(opCmd(e, o, o.Key, nil))
	}
	if len(escaped) > 0 {
		sec := &cobra.Command{
			Use:   "section <key>",
			Short: "Reach a section whose key is also a command name",
			Args:  unknownArgs,
			Run:   func(c *cobra.Command, _ []string) { _ = c.Help() },
		}
		for _, o := range escaped {
			sec.AddCommand(opCmd(e, o, o.Key, nil))
		}
		page.AddCommand(sec)
	}
	return page
}

func sortedOps(ops []*Op) []*Op {
	out := append([]*Op(nil), ops...)
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// opCmd is the cobra command for an operation that takes no id.
func opCmd(e *Env, o *Op, use string, _ any) *cobra.Command {
	c := &cobra.Command{
		Use:   use,
		Short: o.Short,
		Long:  o.Short + "\n\nGET " + o.Path,
		Args:  unknownArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return e.run(cmd, o, "", o.Flags)
		},
	}
	c.Flags().AddFlagSet(o.Flags)
	return c
}

// getCmd is `get <id> [section]`. The sections have different flags, so it parses its own
// arguments: the first pass finds the id and section (flags it does not know are skipped), the
// second parses strictly against the chosen operation's flags.
func getCmd(e *Env, s *PageSpec) *cobra.Command {
	c := &cobra.Command{
		Use:                "get <id> [section]",
		Short:              "Get one record, or one section of it",
		DisableFlagParsing: true,
	}
	var long strings.Builder
	long.WriteString("Get one record, or one section of it.\n\nThe id is passed through verbatim and URL-encoded once. Put the id and the section before any flags.")
	if s.Get != nil {
		long.WriteString("\n\n  po " + s.Slug + " get <id>\n      " + s.Get.Short + "\n      GET " + s.Get.Path)
	}
	if len(s.IDSections) > 0 {
		long.WriteString("\n\nSections (po " + s.Slug + " get <id> <section> --help shows a section's flags):\n")
		for _, o := range sortedOps(s.IDSections) {
			fmt.Fprintf(&long, "  %-34s %s\n", o.Key, o.Short)
		}
	}
	c.Long = strings.TrimRight(long.String(), "\n")
	c.RunE = func(cmd *cobra.Command, args []string) error {
		fs := pflag.NewFlagSet("get", pflag.ContinueOnError)
		fs.SetOutput(io.Discard)
		fs.ParseErrorsAllowlist.UnknownFlags = true
		fs.AddFlagSet(cmd.InheritedFlags())
		help := fs.BoolP("help", "h", false, "")
		if err := fs.Parse(args); err != nil {
			return apierr.Usage(err)
		}
		pos := fs.Args()
		var op *Op
		switch {
		case len(pos) == 0 && *help:
			return cmd.Help()
		case len(pos) == 0:
			return apierr.Usage(fmt.Errorf("get needs an id: po %s get <id> [section]", s.Slug))
		case len(pos) == 1:
			op = s.Get
			if op == nil {
				return apierr.Usage(fmt.Errorf("%s has no record command; sections: %s", s.Slug, keys(s.IDSections)))
			}
		case len(pos) == 2:
			for _, o := range s.IDSections {
				if o.Key == pos[1] {
					op = o
				}
			}
			if op == nil {
				return apierr.Usage(fmt.Errorf("unknown section %q for %s; valid sections: %s", pos[1], s.Slug, keys(s.IDSections)))
			}
		default:
			return apierr.Usage(fmt.Errorf("too many arguments: po %s get <id> [section]", s.Slug))
		}
		strict := pflag.NewFlagSet("get", pflag.ContinueOnError)
		strict.SetOutput(io.Discard)
		strict.AddFlagSet(cmd.InheritedFlags())
		strict.AddFlagSet(op.Flags)
		strict.BoolP("help", "h", false, "")
		if err := strict.Parse(args); err != nil {
			return apierr.Usage(err)
		}
		if h, _ := strict.GetBool("help"); h {
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s\n\nUsage:\n  po %s get <id> %s [flags]\n\nFlags:\n%s", op.Short+"\nGET "+op.Path, s.Slug, op.Key, op.Flags.FlagUsages())
			return err
		}
		return e.run(cmd, op, pos[0], op.Flags)
	}
	return c
}

func keys(ops []*Op) string {
	var k []string
	for _, o := range sortedOps(ops) {
		k = append(k, o.Key)
	}
	return strings.Join(k, ", ")
}

// run calls the typed SDK operation and renders the raw body from the returned response.
func (e *Env) run(cmd *cobra.Command, o *Op, id string, fs *pflag.FlagSet) error {
	cl, err := e.Client()
	if err != nil {
		return err
	}
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	resp, callErr := o.Call(ctx, cl.SDK, id, fs)
	if resp == nil {
		return transportErr(callErr)
	}
	defer func() { _ = resp.Body.Close() }()
	body, rerr := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if rerr != nil {
		return rerr
	}
	out := cmd.OutOrStdout()
	if resp.StatusCode != http.StatusOK {
		if e.JSON != nil && e.JSON() && len(body) > 0 {
			writeBody(out, body)
		}
		return apierr.FromResponse(resp.StatusCode, body, cl.Profile)
	}
	if callErr != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warning: the SDK (%s) could not decode this response (%v); printing the raw body. This is an SDK bug.\n", SDKVersion, callErr)
	}
	writeBody(out, body)
	return nil
}

// transportErr surfaces the CLI's own classed error (not logged in, cannot reach) from under the
// net/http wrapper the SDK returns.
func transportErr(err error) error {
	var ce *apierr.ClassError
	if errors.As(err, &ce) {
		return ce
	}
	if err == nil {
		return errors.New("the SDK returned no response")
	}
	return err
}

func writeBody(w io.Writer, body []byte) {
	_, _ = w.Write(body)
	if len(body) == 0 || body[len(body)-1] != '\n' {
		_, _ = io.WriteString(w, "\n")
	}
}
