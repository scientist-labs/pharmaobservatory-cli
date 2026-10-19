// Package pages builds the command tree: one group per object type (companies, cell-lines, sites,
// ...) and the dataset catalog. The operations are generated into zz_generated_*.go by internal/gen
// from the pinned SDK module; this file is the hand-written runtime they plug into.
package pages

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	sdk "github.com/scientist-labs/pharmaobservatory-sdks/go"

	"github.com/scientist-labs/pharmaobservatory-cli/internal/api"
	"github.com/scientist-labs/pharmaobservatory-cli/internal/apierr"
	"github.com/scientist-labs/pharmaobservatory-cli/internal/output"
	"github.com/scientist-labs/pharmaobservatory-cli/internal/sanitize"
)

// Op is one generated operation: its flags, its help and the typed SDK call.
type Op struct {
	Key     string
	IDName  string // the path parameter (id, or name for datasets), empty when the operation takes none
	Short   string
	Path    string
	Escaped bool
	Flags   *pflag.FlagSet
	Call    func(ctx context.Context, cl *sdk.APIClient, id string, fs *pflag.FlagSet) (*http.Response, error)
}

// PageSpec is one page: its operations by kind.
type PageSpec struct {
	Slug       string // the API's name for the object type (cell_lines)
	Command    string // its command name (cell-lines)
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

// filterFlag is the hand-written flag every dataset read gets (an operation that pages, so it
// declares --cursor). The server's filter grammar keys a filter by column name ({column}=v,
// {column}.gt=v, ...), so the document cannot declare it as a parameter; the transport appends each
// --filter to the query string the SDK built.
const filterFlag = "filter"

// reservedQuery are the dataset parameters the server reserves; a --filter cannot spell them.
var reservedQuery = map[string]bool{"cursor": true, "limit": true, "sort": true, "group_by": true, "measure": true, "scope": true, "format": true, "counts": true}

func addFilterFlag(o *Op) {
	if o == nil || o.Flags.Lookup("cursor") == nil || o.Flags.Lookup(filterFlag) != nil {
		return
	}
	o.Flags.StringArray(filterFlag, nil, "filter rows, column=value or column.op=value (op: gt, gte, lt, lte, ne, contains, null); repeatable, a repeated column=value is an IN list")
}

// filters reads --filter into query values, refusing a malformed or reserved key as a usage error.
func filters(fs *pflag.FlagSet) (url.Values, error) {
	if fs.Lookup(filterFlag) == nil {
		return nil, nil
	}
	raw, err := fs.GetStringArray(filterFlag)
	if err != nil || len(raw) == 0 {
		return nil, err
	}
	q := url.Values{}
	for _, f := range raw {
		k, v, ok := strings.Cut(f, "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" {
			return nil, apierr.Usage(fmt.Errorf("--filter wants column=value or column.op=value, got %q", sanitize.Line(f)))
		}
		// A column name never holds a bracket; refusing them closes the limit[]=5 spelling of a
		// reserved key, and the reserved check compares the trimmed, lowercased column.
		if strings.ContainsAny(k, "[]") {
			return nil, apierr.Usage(fmt.Errorf("--filter key %q is not a column name (no brackets)", sanitize.Line(k)))
		}
		if col, _, _ := strings.Cut(k, "."); reservedQuery[strings.ToLower(col)] {
			col = strings.ToLower(col)
			return nil, apierr.Usage(fmt.Errorf("--filter cannot set %q; use --%s", col, strings.ReplaceAll(col, "_", "-")))
		}
		q.Add(k, v)
	}
	return q, nil
}

func buildFrom(e *Env, specs []*PageSpec) []*cobra.Command {
	var top []*cobra.Command
	for _, s := range specs {
		addFilterFlag(s.List)
		addFilterFlag(s.Get)
		for _, o := range s.IDSections {
			addFilterFlag(o)
		}
		for _, o := range s.Sections {
			addFilterFlag(o)
		}
	}
	escape := &cobra.Command{
		Use:   "page <name>",
		Short: "Reach an object type whose name is also a command",
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

// command is a page's command name: the kebab form of its API name.
func (s *PageSpec) command() string {
	if s.Command != "" {
		return s.Command
	}
	return s.Slug
}

// cmdKey is a dataset's subcommand name: the kebab form of its API key. The API key itself is
// accepted too, so a name copied from a response works.
func cmdKey(key string) string { return strings.ReplaceAll(key, "_", "-") }

func aliases(name, apiName string) []string {
	if name == apiName {
		return nil
	}
	return []string{apiName}
}

func pageCmd(e *Env, s *PageSpec) *cobra.Command {
	name := s.command()
	page := &cobra.Command{
		Use:     name,
		Aliases: aliases(name, s.Slug),
		Short:   "Read " + name,
		Args:    unknownArgs,
		Run:     func(c *cobra.Command, _ []string) { _ = c.Help() },
	}
	if len(s.IDSections) > 0 {
		page.Short += " and the datasets of one record"
	}
	var long strings.Builder
	fmt.Fprintf(&long, "Read %s through the API (GET /api/v1/%s).\n", name, s.Slug)
	if len(s.IDSections) > 0 {
		long.WriteString("\nDatasets of one record (" + apierr.Bin + " " + name + " get <id> <dataset>):\n")
		for _, o := range sortedOps(s.IDSections) {
			fmt.Fprintf(&long, "  %-34s %s\n", cmdKey(o.Key), o.Short)
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
		page.AddCommand(opCmd(e, o, cmdKey(o.Key), nil))
	}
	if len(escaped) > 0 {
		sec := &cobra.Command{
			Use:   "section <key>",
			Short: "Reach a dataset whose key is also a command name",
			Args:  unknownArgs,
			Run:   func(c *cobra.Command, _ []string) { _ = c.Help() },
		}
		for _, o := range escaped {
			sec.AddCommand(opCmd(e, o, cmdKey(o.Key), nil))
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
		Use:     use,
		Aliases: aliases(use, o.Key),
		Short:   o.Short,
		Long:    o.Short + "\n\nGET " + o.Path,
		Args:    unknownArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return e.run(cmd, o, "", o.Flags)
		},
	}
	c.Flags().AddFlagSet(o.Flags)
	return c
}

// getCmd is `get <id> [dataset]`. The datasets have different flags, so it parses its own
// arguments: the first pass finds the id and dataset (flags it does not know are skipped), the
// second parses strictly against the chosen operation's flags.
func getCmd(e *Env, s *PageSpec) *cobra.Command {
	name := s.command()
	idName := "id"
	if s.Get != nil && s.Get.IDName != "" {
		idName = s.Get.IDName
	}
	arg := "<" + idName + ">"
	c := &cobra.Command{
		Use:                "get " + arg + " [dataset]",
		Short:              "Get one record, or one dataset of it",
		DisableFlagParsing: true,
	}
	if len(s.IDSections) == 0 {
		c.Use = "get " + arg
		c.Short = "Get one record"
	}
	var long strings.Builder
	long.WriteString(c.Short + ".\n\nThe " + idName + " is passed through verbatim and URL-encoded once. Put the " + idName + " and the dataset before any flags.")
	if s.Get != nil {
		long.WriteString("\n\n  " + apierr.Bin + " " + name + " get " + arg + "\n      " + s.Get.Short + "\n      GET " + s.Get.Path)
	}
	if len(s.IDSections) > 0 {
		long.WriteString("\n\nDatasets (" + apierr.Bin + " " + name + " get " + arg + " <dataset> --help shows a dataset's flags):\n")
		for _, o := range sortedOps(s.IDSections) {
			fmt.Fprintf(&long, "  %-34s %s\n", cmdKey(o.Key), o.Short)
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
			return apierr.Usage(fmt.Errorf("get needs an %s: %s %s get %s [dataset]", idName, apierr.Bin, name, arg))
		case len(pos) == 1:
			op = s.Get
			if op == nil {
				return apierr.Usage(fmt.Errorf("%s has no record command; datasets: %s", name, keys(s.IDSections)))
			}
		case len(pos) == 2:
			for _, o := range s.IDSections {
				if cmdKey(o.Key) == cmdKey(pos[1]) {
					op = o
				}
			}
			if op == nil {
				return apierr.Usage(fmt.Errorf("unknown dataset %q for %s; valid datasets: %s", pos[1], name, keys(s.IDSections)))
			}
		default:
			return apierr.Usage(fmt.Errorf("too many arguments: %s %s get %s [dataset]", apierr.Bin, name, arg))
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
			use := "get " + arg
			if op != s.Get {
				use += " " + cmdKey(op.Key)
			}
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s\n\nUsage:\n  %s %s %s [flags]\n\nFlags:\n%s", op.Short+"\nGET "+op.Path, apierr.Bin, name, use, op.Flags.FlagUsages())
			return err
		}
		return e.run(cmd, op, pos[0], op.Flags)
	}
	return c
}

func keys(ops []*Op) string {
	var k []string
	for _, o := range sortedOps(ops) {
		k = append(k, cmdKey(o.Key))
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
	extra, err := filters(fs)
	if err != nil {
		return err
	}
	cl, err := e.Client()
	if err != nil {
		return err
	}
	w := &walker{e: e, cmd: cmd, cl: cl, o: o, id: id, fs: fs, set: set, extra: extra}
	if set.JSON {
		return w.raw()
	}
	return w.render()
}

// MaxPages is the ceiling on pages one --all walk fetches. A walk that reaches it stops, marks
// the output partial and exits 9. It is a variable so the tests can lower it.
var MaxPages = 1000

type walker struct {
	e   *Env
	cmd *cobra.Command
	cl  *Client
	o   *Op
	id  string
	fs  *pflag.FlagSet
	set Settings
	// extra is the --filter query, appended by the transport to every page's request.
	extra url.Values
}

func (w *walker) ctx() context.Context {
	c := w.cmd.Context()
	if c == nil {
		c = context.Background()
	}
	return api.WithQuery(c, w.extra)
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
		return nil, apierr.Unavailable("reading the response failed: %v", rerr)
	}
	if resp.StatusCode != http.StatusOK {
		err := apierr.FromResponse(resp.StatusCode, resp.Header, body, w.cl.Profile)
		if resp.StatusCode == http.StatusConflict {
			var b struct {
				Error     string `json:"error"`
				ErrorCode string `json:"error_code"`
			}
			if json.Unmarshal(body, &b) == nil && (b.Error == "generation_changed" || b.ErrorCode == "GENERATION_CHANGED") {
				err = &generationChanged{err}
			}
		}
		return body, err
	}
	if callErr != nil && !inBodyState(body) {
		_, _ = fmt.Fprintf(w.cmd.ErrOrStderr(), "warning: the SDK (%s) could not decode this response (%v); printing the raw body. This is an SDK bug.\n", SDKVersion, sanitize.Line(callErr.Error()))
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
		pages       int
		seen        = map[string]bool{}
	)
	if origCursor != "" {
		seen[origCursor] = true
	}
	for {
		body, err := w.fetch()
		var gc *generationChanged
		if errors.As(err, &gc) && w.set.All {
			if !restarted {
				// The page count is NOT reset: one --all run fetches at most MaxPages pages in
				// all, restart included.
				restarted, first, last, rows = true, nil, nil, nil
				seen = map[string]bool{}
				if origCursor != "" {
					seen[origCursor] = true
				}
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
				return errors.New("the response is not a dataset response; use --json to see it")
			}
		}
		last = env
		pages++
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
		if seen[next] {
			partial = "the server returned a cursor that was already fetched (" + sanitize.Line(next) + "), so the walk would never end"
			break
		}
		if pages >= MaxPages {
			partial = fmt.Sprintf("reached the %d page limit with more pages remaining", MaxPages)
			break
		}
		seen[next] = true
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
	if w.e.Width != nil {
		width = w.e.Width(out)
	}
	if err := w.renderBody(first, rows, out, width); err != nil {
		return err
	}
	// The source credit travels with the saved file: CSV always, a table whenever stdout is not
	// a terminal. A terminal table already shows the credit on stderr. Never suppressible.
	if w.set.CSV || width == 0 {
		output.WriteCredits(out, first)
	}
	return nil
}

func (w *walker) renderBody(first *output.Envelope, rows [][]string, out io.Writer, width int) error {
	if w.set.CSV {
		width = 0
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
		if w.set.CSV || first.Index == nil || len(first.Index.Rows) == 0 {
			return nil
		}
		_, _ = io.WriteString(out, "\n")
		return emit(*first.Index)
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
