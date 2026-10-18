// Package pages builds the page command tree. The per-page operations are generated into
// zz_generated_*.go by internal/gen from the pinned SDK module; this file is the hand-written
// runtime they plug into.
package pages

import (
	"context"
	"encoding/json"
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
	"github.com/scientist-labs/pharmaobservatory-cli/internal/output"
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
	// Settings reports the output flags.
	Settings func() Settings
	// Width returns the terminal width when w is a terminal, else 0 (tab-separated output).
	Width func(w io.Writer) int
}

// Settings are the global output flags. Quiet drops the footer's as-of, URL and paging lines;
// the attribution lines are printed regardless.
type Settings struct{ JSON, CSV, All, Quiet bool }

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

// run calls the typed SDK operation and renders the body: raw with --json, otherwise a table or
// CSV built from the envelope's columns, with the footer on stderr.
func (e *Env) run(cmd *cobra.Command, o *Op, id string, fs *pflag.FlagSet) error {
	set := Settings{}
	if e.Settings != nil {
		set = e.Settings()
	}
	if set.JSON && set.CSV {
		return apierr.Usage(errors.New("--json and --csv cannot be combined"))
	}
	if set.JSON && set.All {
		return apierr.Usage(errors.New("--all cannot be combined with --json (one body per page); use --cursor to page by hand"))
	}
	cl, err := e.Client()
	if err != nil {
		return err
	}
	w := &walker{e: e, cmd: cmd, cl: cl, o: o, id: id, fs: fs, set: set}
	if set.JSON {
		return w.raw()
	}
	return w.render()
}

type walker struct {
	e   *Env
	cmd *cobra.Command
	cl  *Client
	o   *Op
	id  string
	fs  *pflag.FlagSet
	set Settings
}

func (w *walker) ctx() context.Context {
	if c := w.cmd.Context(); c != nil {
		return c
	}
	return context.Background()
}

// generationChanged marks a 409 generation_changed: a walk may restart once, anything else
// reports it.
type generationChanged struct{ err error }

func (g *generationChanged) Error() string { return g.err.Error() }
func (g *generationChanged) Unwrap() error { return g.err }

// fetch makes one call and returns the 200 body. A non-200 answer is its classed error.
func (w *walker) fetch() ([]byte, error) {
	resp, callErr := w.o.Call(w.ctx(), w.cl.SDK, w.id, w.fs)
	if resp == nil {
		return nil, transportErr(callErr)
	}
	defer func() { _ = resp.Body.Close() }()
	body, rerr := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if rerr != nil {
		return nil, rerr
	}
	if resp.StatusCode != http.StatusOK {
		err := apierr.FromResponse(resp.StatusCode, body, w.cl.Profile)
		if resp.StatusCode == http.StatusConflict {
			var b struct {
				Error string `json:"error"`
			}
			if json.Unmarshal(body, &b) == nil && b.Error == "generation_changed" {
				err = &generationChanged{err}
			}
		}
		return body, err
	}
	if callErr != nil && !inBodyState(body) {
		_, _ = fmt.Fprintf(w.cmd.ErrOrStderr(), "warning: the SDK (%s) could not decode this response (%v); printing the raw body. This is an SDK bug.\n", SDKVersion, callErr)
	}
	return body, nil
}

// inBodyState reports an unbuilt or not_applicable answer: those never fit the typed model, and
// the state message says everything, so the SDK warning would be noise.
func inBodyState(body []byte) bool {
	env, err := output.Parse(body)
	return err == nil && env.Error != ""
}

// raw prints the body unchanged. An in-body state still sets its exit code.
func (w *walker) raw() error {
	body, err := w.fetch()
	if len(body) > 0 {
		writeBody(w.cmd.OutOrStdout(), body)
	}
	if err != nil {
		return err
	}
	if env, perr := output.Parse(body); perr == nil {
		return env.State()
	}
	return nil
}

func (w *walker) setCursor(c string) {
	if f := w.fs.Lookup("cursor"); f != nil {
		_ = w.fs.Set("cursor", c)
	}
}

// restoreCursor puts the cursor flag back as the user gave it.
func (w *walker) restoreCursor(orig string, changed bool) {
	f := w.fs.Lookup("cursor")
	if f == nil {
		return
	}
	_ = f.Value.Set(orig)
	f.Changed = changed
}

func (w *walker) render() error {
	var origCursor string
	var origChanged bool
	if f := w.fs.Lookup("cursor"); f != nil {
		origCursor, origChanged = f.Value.String(), f.Changed
		defer w.restoreCursor(origCursor, origChanged)
	}
	var (
		first, last *output.Envelope
		rows        [][]string
		restarted   bool
		partial     string
	)
	for {
		body, err := w.fetch()
		var gc *generationChanged
		if errors.As(err, &gc) && w.set.All {
			if !restarted {
				restarted, first, last, rows = true, nil, nil, nil
				w.restoreCursor(origCursor, origChanged)
				continue
			}
			partial = "generation_changed twice"
			err = nil
			if last == nil {
				return gc
			}
			break
		}
		if err != nil {
			if last == nil {
				return err
			}
			partial = err.Error()
			break
		}
		env, perr := output.Parse(body)
		if perr != nil {
			if last == nil {
				return perr
			}
			partial = perr.Error()
			break
		}
		if serr := env.State(); serr != nil {
			if last == nil {
				return serr
			}
			partial = serr.Error()
			break
		}
		if first == nil {
			first = env
			if !env.IsTable() && env.Identity == nil {
				return errors.New("the response is not a section envelope; use --json to see it")
			}
		}
		last = env
		rows = append(rows, output.Rows(env)...)
		next := ""
		if env.Pagination != nil && env.Pagination.NextCursor != nil {
			next = *env.Pagination.NextCursor
		}
		if !w.set.All || next == "" {
			break
		}
		if w.fs.Lookup("cursor") == nil {
			partial = "this operation takes no --cursor, so the next page cannot be requested"
			break
		}
		w.setCursor(next)
	}
	if err := w.write(first, rows); err != nil {
		return err
	}
	out := w.cmd.ErrOrStderr()
	fo := output.FooterOpts{Quiet: w.set.Quiet, Profile: w.cl.Profile, Shown: len(rows), All: w.set.All, Partial: partial, Restart: restarted}
	// The footer describes the last page fetched (its cursor and total); the credit and as-of
	// lines are the same on every page.
	output.WriteFooter(out, last, fo)
	if partial != "" {
		return &apierr.ClassError{Class: apierr.ExitPartial, Msg: "partial result: " + partial}
	}
	return nil
}

func (w *walker) write(first *output.Envelope, rows [][]string) error {
	out := w.cmd.OutOrStdout()
	width := 0
	if !w.set.CSV && w.e.Width != nil {
		width = w.e.Width(out)
	}
	emit := func(g output.Grid) error {
		if w.set.CSV {
			return output.WriteCSV(out, g.Header, g.Rows)
		}
		return output.WriteTable(out, g, width)
	}
	if first.Identity != nil {
		if err := emit(output.IdentityGrid(first, w.set.CSV)); err != nil {
			return err
		}
		if w.set.CSV || len(first.Sections) == 0 {
			return nil
		}
		_, _ = io.WriteString(out, "\n")
		return emit(output.SectionsGrid(first))
	}
	g := output.Grid{Rows: rows}
	if w.set.CSV {
		g.Header = output.Keys(first)
	} else {
		g.Header = output.FromEnvelope(first).Header
	}
	return emit(g)
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
