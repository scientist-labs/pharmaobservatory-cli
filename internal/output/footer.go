package output

import (
	"fmt"
	"github.com/scientist-labs/pharmaobservatory-cli/internal/sanitize"
	"io"
	"strconv"
	"strings"
)

// FooterOpts says what the footer may contain. Attribution is not an option: it is printed
// whenever the envelope carries any, whatever else is set.
type FooterOpts struct {
	Quiet   bool // drops everything except the attribution lines
	Profile string
	// Paging, set by the caller that knows how many rows were shown or fetched.
	Shown   int
	All     bool
	Partial string // non-empty: the walk stopped early, and why
	Restart bool   // a generation change restarted the walk
}

// AttributionLines are the source credit lines, `name (license) href`.
func AttributionLines(e *Envelope) []string {
	var out []string
	for _, a := range e.Attribution {
		parts := []string{a.Name}
		if a.License != nil && *a.License != "" {
			parts = append(parts, "("+*a.License+")")
		}
		if a.Href != "" {
			parts = append(parts, a.Href)
		}
		out = append(out, sanitize.Line(strings.Join(parts, " ")))
	}
	if e.AttrText != "" {
		out = append(out, sanitize.Line(e.AttrText))
	}
	return out
}

// WriteCredits writes the attribution as `# Source: <credit>` lines, for the end of a CSV file or
// a piped table. Plain credit only, never a tier word.
func WriteCredits(w io.Writer, e *Envelope) {
	for _, l := range AttributionLines(e) {
		_, _ = fmt.Fprintln(w, "# Source: "+l)
	}
}

// WriteFooter prints the footer to w (stderr in every mode).
func WriteFooter(w io.Writer, e *Envelope, o FooterOpts) {
	for _, l := range AttributionLines(e) {
		_, _ = fmt.Fprintln(w, l)
	}
	say := func(s string) { _, _ = fmt.Fprintln(w, sanitize.Line(s)) }
	if o.Quiet {
		return
	}
	if e.BuildState != nil {
		asOf := Cell(e.BuildState.LastLoaded)
		if asOf == "" {
			asOf = "generation " + e.BuildState.Generation
		}
		line := "as of " + asOf
		if o.Profile != "" {
			line += " (profile " + o.Profile + ")"
		}
		say(line)
	}
	if r := e.Resolution; r != nil {
		say(fmt.Sprintf("resolved: %s (%s)", r.MatchTier, r.Verdict))
	}
	if e.WebURL != "" {
		say(e.WebURL)
	}
	if e.Pagination != nil {
		say(pagingLine(e.Pagination, o))
	}
}

func pagingLine(p *Pagination, o FooterOpts) string {
	var total string
	if p.Total != nil {
		total = Thousands(*p.Total)
		if p.TotalBasis == "estimated" {
			total = "about " + total
		}
	}
	basis := p.TotalBasis
	switch {
	case o.Partial != "":
		s := fmt.Sprintf("partial: fetched %d rows, then stopped (%s)", o.Shown, o.Partial)
		if total != "" {
			s += "; total " + total + " (" + basis + ")"
		}
		return s
	case o.All:
		s := fmt.Sprintf("fetched %d rows", o.Shown)
		if total != "" {
			s += "; total " + total + " (" + basis + ")"
		}
		if o.Restart {
			s += "; restarted once because the warehouse was rebuilt mid-walk"
		}
		return s
	}
	s := fmt.Sprintf("showing %d", o.Shown)
	if total != "" {
		s += " of " + total + " (" + basis + ")"
	}
	if p.NextCursor != nil && *p.NextCursor != "" {
		s += "; next: --cursor " + *p.NextCursor
	}
	return s
}

// Thousands formats 12431 as 12,431.
func Thousands(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	if neg {
		s = "-" + s
	}
	return s
}
