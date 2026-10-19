package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"github.com/scientist-labs/pharmaobservatory-cli/internal/apierr"
	"github.com/scientist-labs/pharmaobservatory-cli/internal/cmd/pages"
	"github.com/scientist-labs/pharmaobservatory-cli/internal/output"
	"github.com/scientist-labs/pharmaobservatory-cli/internal/sanitize"
)

// maxStdinSQL bounds what `query -` reads from stdin. It is a read bound, not SQL validation: the
// server owns every limit on the statement itself (SQL_TOO_LONG is its answer).
const maxStdinSQL = 1 << 20

// queryPage is the part of a POST /api/v1/query or GET /api/v1/results/{result_id} body the renderer
// reads. Rows are positional, beside columns. Numbers keep their text (json.Number).
type queryPage struct {
	Columns []struct {
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"columns"`
	Rows            [][]any              `json:"rows"`
	TotalRows       *int64               `json:"total_rows"`
	Truncated       bool                 `json:"truncated"`
	ResultID        string               `json:"result_id"`
	NextCursor      *string              `json:"next_cursor"`
	Attribution     []output.Attribution `json:"attribution"`
	SourceCredit    string               `json:"source_credit"`
	WithheldColumns []string             `json:"withheld_columns"`
	BuildState      *output.BuildState   `json:"build_state"`
	Notice          *string              `json:"notice"`
}

func parseQueryPage(body []byte) (*queryPage, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	p := &queryPage{}
	if err := dec.Decode(p); err != nil {
		return nil, fmt.Errorf("the response is not a query result (%v); use --json to see it", err)
	}
	if p.Columns == nil || p.ResultID == "" {
		return nil, errors.New("the response is not a query result (no columns or result_id); use --json to see it")
	}
	return p, nil
}

func (p *queryPage) next() string {
	if p.NextCursor == nil {
		return ""
	}
	return *p.NextCursor
}

// credit is the page as an envelope the shared footer and credit writers read: the attribution,
// falling back to the plain source_credit when the body lists no attribution, and the as-of state.
func (p *queryPage) credit() *output.Envelope {
	e := &output.Envelope{Attribution: p.Attribution, BuildState: p.BuildState}
	if len(p.Attribution) == 0 && p.SourceCredit != "" {
		e.AttrText = strings.TrimPrefix(p.SourceCredit, "Source: ")
	}
	return e
}

func resultPath(id, cursor string) string {
	p := "/api/v1/results/" + url.PathEscape(id)
	if cursor != "" {
		p += "?" + url.Values{"cursor": {cursor}}.Encode()
	}
	return p
}

func newQueryCmd(g *globals, d Deps) *cobra.Command {
	var resultID, cursor string
	c := &cobra.Command{
		Use:   "query <sql | ->",
		Short: "Run one SQL SELECT over a published dataset, or page a stored result",
		Long: "Run one SQL SELECT over one published dataset through POST /api/v1/query and print the first\n" +
			"page as a table. Pass - to read the SQL from stdin. SQL needs a signed-in account (" + apierr.Bin + " login).\n\n" +
			"The server stores the full result for 24 hours and enforces every limit (rows returned per page,\n" +
			"rows stored, statement length, timeout). The CLI does not check the SQL itself: a refused query\n" +
			"prints the server's error_code, message, hint and docs.\n\n" +
			"Page a stored result with --result <result_id> [--cursor <cursor>] (GET /api/v1/results/{result_id});\n" +
			"the footer prints the next command to run. --all follows the cursor to the end of the stored\n" +
			"result and prints one table.\n\n" +
			"Examples:\n" +
			"  " + apierr.Bin + " query \"SELECT biomarker_kind, count(*) AS n FROM biomarkers GROUP BY biomarker_kind\"\n" +
			"  " + apierr.Bin + " query \"SELECT display_name, gene_symbol FROM biomarkers WHERE gene_symbol = 'EGFR'\" --csv\n" +
			"  " + apierr.Bin + " query --result <result_id> --cursor <cursor>\n" +
			"  " + apierr.Bin + " query - < query.sql",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) > 1 {
				return apierr.Usage(errors.New("query takes one argument, the SQL (quote it), or - to read it from stdin"))
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			q := &queryRun{g: g, d: d, cmd: cmd, resultID: resultID, cursor: cursor}
			return q.run(args)
		},
	}
	c.Flags().StringVar(&resultID, "result", "", "page the stored result with this result_id instead of running SQL")
	c.Flags().StringVar(&cursor, "cursor", "", "with --result: start from this cursor (the next_cursor of the previous page)")
	return c
}

type queryRun struct {
	g        *globals
	d        Deps
	cmd      *cobra.Command
	resultID string
	cursor   string
	profile  string
}

func (q *queryRun) run(args []string) error {
	g := q.g
	switch {
	case g.json && g.csv:
		return apierr.Usage(errors.New("--json and --csv cannot be combined"))
	case g.json && g.all:
		return apierr.Usage(errors.New("--all cannot be combined with --json (one body per page); use --result and --cursor to page by hand"))
	case q.resultID != "" && len(args) > 0:
		return apierr.Usage(errors.New("give either SQL or --result, not both"))
	case q.resultID == "" && len(args) == 0:
		return apierr.Usage(fmt.Errorf("query needs SQL: %s query \"SELECT ...\" (or - to read stdin, or --result <result_id> to page a stored result)", apierr.Bin))
	case q.cursor != "" && q.resultID == "":
		return apierr.Usage(errors.New("--cursor pages a stored result: pass --result <result_id> with it"))
	}
	var payload []byte
	if len(args) == 1 {
		sql, err := q.sql(args[0])
		if err != nil {
			return err
		}
		payload, err = json.Marshal(map[string]string{"sql": sql})
		if err != nil {
			return err
		}
	}
	s, err := g.session(q.d)
	if err != nil {
		return err
	}
	q.profile = s.Profile.Name
	ctx := ctxOf(q.cmd.Context())
	fetch := func(cursor string) ([]byte, error) {
		return s.Get(ctx, resultPath(q.resultID, cursor))
	}
	var body []byte
	if payload != nil {
		body, err = s.PostJSON(ctx, "/api/v1/query", payload)
	} else {
		body, err = fetch(q.cursor)
	}
	if g.json {
		if len(body) > 0 {
			writeRaw(q.cmd.OutOrStdout(), body)
		}
		return err
	}
	if err != nil {
		return err
	}
	first, err := parseQueryPage(body)
	if err != nil {
		return err
	}
	if q.resultID == "" {
		q.resultID = first.ResultID
	}
	rows := append([][]any(nil), first.Rows...)
	last := first
	partial := ""
	pagesFetched := 1
	seen := map[string]bool{}
	if q.cursor != "" {
		seen[q.cursor] = true
	}
	for g.all && last.next() != "" {
		next := last.next()
		if seen[next] {
			partial = "the server returned a cursor that was already fetched (" + next + "), so the walk would never end"
			break
		}
		if pagesFetched >= pages.MaxPages {
			partial = fmt.Sprintf("reached the %d page limit with more pages remaining", pages.MaxPages)
			break
		}
		seen[next] = true
		b, ferr := fetch(next)
		if ferr != nil {
			// RESULT_EXPIRED, GENERATION_CHANGED, a 429 or anything else: print what was fetched and
			// say why the walk stopped, in the server's words.
			partial = ferr.Error()
			break
		}
		p, perr := parseQueryPage(b)
		if perr != nil {
			partial = perr.Error()
			break
		}
		if len(p.Columns) != len(first.Columns) {
			partial = "a later page has different columns from the first"
			break
		}
		rows = append(rows, p.Rows...)
		last = p
		pagesFetched++
	}
	if err := q.write(first, rows); err != nil {
		return err
	}
	q.footer(first, last, len(rows), partial)
	if partial != "" {
		return &apierr.ClassError{Class: apierr.ExitPartial, Msg: "partial result: " + partial}
	}
	return nil
}

// sql returns the statement: the argument, or stdin for "-". It is sent as given apart from
// surrounding whitespace.
func (q *queryRun) sql(arg string) (string, error) {
	if arg != "-" {
		return arg, nil
	}
	in := q.d.Stdin
	if in == nil {
		return "", apierr.Usage(errors.New("no stdin to read the SQL from"))
	}
	b, err := io.ReadAll(io.LimitReader(in, maxStdinSQL+1))
	if err != nil {
		return "", fmt.Errorf("reading the SQL from stdin: %w", err)
	}
	if len(b) > maxStdinSQL {
		return "", apierr.Usage(fmt.Errorf("stdin holds more than %d bytes; that is not one SQL statement", maxStdinSQL))
	}
	sql := strings.TrimSpace(string(b))
	if sql == "" {
		return "", apierr.Usage(errors.New("stdin was empty: pipe the SQL in, for example " + apierr.Bin + " query - < query.sql"))
	}
	return sql, nil
}

// write renders the positional rows under the column names, as a table or CSV. Every cell goes
// through the shared writers, which sanitize it for the sink. The credit follows the rows in CSV and
// in a piped table, as everywhere else.
func (q *queryRun) write(first *queryPage, rows [][]any) error {
	out := q.cmd.OutOrStdout()
	width := 0
	if !q.g.csv {
		width = terminalWidth(out)
	}
	n := len(first.Columns)
	for _, r := range rows {
		if len(r) > n {
			n = len(r)
		}
	}
	g := output.Grid{Header: make([]string, n)}
	for i, c := range first.Columns {
		g.Header[i] = c.Name
	}
	for _, r := range rows {
		cells := make([]string, n)
		for i, v := range r {
			cells[i] = output.Cell(v)
		}
		g.Rows = append(g.Rows, cells)
	}
	var err error
	if q.g.csv {
		err = output.WriteCSV(out, g.Header, g.Rows)
	} else {
		err = output.WriteTable(out, g, width)
	}
	if err != nil {
		return err
	}
	if q.g.csv || width == 0 {
		output.WriteCredits(out, first.credit())
	}
	return nil
}

// footer prints, on stderr: the source credit (always), the server's notice and the withheld
// columns (always: they say what the table leaves out), then the as-of and paging lines unless
// --quiet.
func (q *queryRun) footer(first, last *queryPage, shown int, partial string) {
	w := q.cmd.ErrOrStderr()
	say := func(s string) { _, _ = fmt.Fprintln(w, sanitize.Line(s)) }
	quiet := q.g.quiet || q.g.noFooter
	output.WriteFooter(w, first.credit(), output.FooterOpts{Quiet: true})
	if first.Notice != nil && *first.Notice != "" {
		say("notice: " + *first.Notice)
	}
	if len(first.WithheldColumns) > 0 {
		say("withheld columns: " + strings.Join(first.WithheldColumns, ", "))
	}
	if quiet {
		return
	}
	output.WriteFooter(w, &output.Envelope{BuildState: first.BuildState}, output.FooterOpts{Profile: q.profile})
	total := ""
	if last.TotalRows != nil {
		total = output.Thousands(*last.TotalRows)
	}
	var line string
	switch {
	case partial != "":
		line = fmt.Sprintf("partial: fetched %d rows, then stopped (%s)", shown, partial)
		if total != "" {
			line += "; stored " + total
		}
	case q.g.all:
		line = fmt.Sprintf("fetched %d rows", shown)
		if total != "" {
			line += " of " + total + " stored"
		}
	default:
		line = fmt.Sprintf("showing %d", shown)
		if total != "" {
			line += " of " + total + " stored"
		}
		if next := last.next(); next != "" {
			line += "; next: " + apierr.Bin + " query --result " + q.resultID + " --cursor " + next
		}
	}
	say(line + "; result " + q.resultID)
}
