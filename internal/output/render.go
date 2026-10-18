package output

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"github.com/scientist-labs/pharmaobservatory-cli/internal/sanitize"
	"io"
	"strings"
	"unicode/utf8"
)

// Cell renders one value: null is empty, a nested value is compact JSON, a number keeps its text.
func Cell(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case json.Number:
		return t.String()
	case bool:
		if t {
			return "true"
		}
		return "false"
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

// Grid is a header and rows of already-rendered cells.
type Grid struct {
	Header []string
	Rows   [][]string
}

// FromEnvelope builds the labelled grid: header is columns[].label in order, cells are
// rows[][columns[].key].
func FromEnvelope(e *Envelope) Grid {
	g := Grid{}
	for _, c := range e.Columns {
		g.Header = append(g.Header, c.Label)
	}
	g.Rows = appendRows(nil, e)
	return g
}

func appendRows(dst [][]string, e *Envelope) [][]string {
	for _, r := range e.Rows {
		row := make([]string, len(e.Columns))
		for i, c := range e.Columns {
			row[i] = Cell(r[c.Key])
		}
		dst = append(dst, row)
	}
	return dst
}

// Rows renders just the rows of e (for walking pages that share one header).
func Rows(e *Envelope) [][]string { return appendRows(nil, e) }

// Keys is the CSV header.
func Keys(e *Envelope) []string {
	var k []string
	for _, c := range e.Columns {
		k = append(k, c.Key)
	}
	return k
}

// WriteCSV writes RFC 4180 quoted CSV: the header, then the rows.
func WriteCSV(w io.Writer, header []string, rows [][]string) error {
	cw := csv.NewWriter(w)
	if err := cw.Write(cleanCSV(header)); err != nil {
		return err
	}
	clean := make([][]string, len(rows))
	for i, r := range rows {
		clean[i] = cleanCSV(r)
	}
	if err := cw.WriteAll(clean); err != nil {
		return err
	}
	cw.Flush()
	return cw.Error()
}

func cleanCSV(cells []string) []string {
	out := make([]string, len(cells))
	for i, c := range cells {
		out[i] = sanitize.CSVCell(c)
	}
	return out
}

// WriteTable writes the grid: width-aware columns when width > 0 (a terminal), tab-separated
// otherwise.
func WriteTable(w io.Writer, g Grid, width int) error {
	clean := func(cells []string) []string {
		out := make([]string, len(cells))
		for i, c := range cells {
			out[i] = sanitize.Line(c)
		}
		return out
	}
	rows := make([][]string, 0, len(g.Rows)+1)
	rows = append(rows, clean(g.Header))
	for _, r := range g.Rows {
		rows = append(rows, clean(r))
	}
	if width <= 0 {
		for _, r := range rows {
			if _, err := io.WriteString(w, strings.Join(r, "\t")+"\n"); err != nil {
				return err
			}
		}
		return nil
	}
	widths := fit(rows, width)
	for _, r := range rows {
		var b strings.Builder
		for i, c := range r {
			c = trunc(c, widths[i])
			b.WriteString(c)
			if i < len(r)-1 {
				b.WriteString(strings.Repeat(" ", widths[i]-utf8.RuneCountInString(c)+2))
			}
		}
		if _, err := io.WriteString(w, b.String()+"\n"); err != nil {
			return err
		}
	}
	return nil
}

// fit gives each column its widest cell, then shaves the widest column one cell at a time until
// the line fits (a column never goes below 4 cells).
func fit(rows [][]string, width int) []int {
	if len(rows) == 0 {
		return nil
	}
	n := len(rows[0])
	ws := make([]int, n)
	for _, r := range rows {
		for i, c := range r {
			if l := utf8.RuneCountInString(c); l > ws[i] {
				ws[i] = l
			}
		}
	}
	total := func() int {
		t := 2 * (n - 1)
		for _, w := range ws {
			t += w
		}
		return t
	}
	for total() > width {
		wi := 0
		for i, w := range ws {
			if w > ws[wi] {
				wi = i
			}
		}
		if ws[wi] <= 4 {
			break
		}
		ws[wi]--
	}
	return ws
}

func trunc(s string, w int) string {
	if utf8.RuneCountInString(s) <= w {
		return s
	}
	r := []rune(s)
	if w <= 1 {
		return string(r[:w])
	}
	return string(r[:w-1]) + "…"
}

// IdentityGrid is the two-column label/value table of a get response.
// For CSV the first column is the row key, like every CSV header here.
func IdentityGrid(e *Envelope, csv bool) Grid {
	g := Grid{Header: []string{"field", "value"}}
	if csv {
		g.Header[0] = "key"
	}
	if e.Identity != nil {
		for _, r := range e.Identity.Rows {
			l := r.Label
			if l == "" || csv {
				l = r.Key
			}
			g.Rows = append(g.Rows, []string{l, Cell(r.Value)})
		}
	}
	return g
}

// SectionsGrid is the sections index: what to ask next, unavailable ones listed as such.
func SectionsGrid(e *Envelope) Grid {
	g := Grid{Header: []string{"section", "heading", "available"}}
	for _, s := range e.Sections {
		a := "yes"
		if !s.Available {
			a = "unavailable"
		}
		g.Rows = append(g.Rows, []string{s.Key, s.Heading, a})
	}
	return g
}
