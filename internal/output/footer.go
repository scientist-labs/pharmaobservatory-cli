package output

import (
	"fmt"
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
		out = append(out, strings.Join(parts, " "))
	}
	if e.AttrText != "" {
		out = append(out, e.AttrText)
	}
	return out
}

// WriteFooter prints the footer to w (stderr in every mode).
func WriteFooter(w io.Writer, e *Envelope, o FooterOpts) {
	for _, l := range AttributionLines(e) {
		fmt.Fprintln(w, l)
	}
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
		fmt.Fprintln(w, line)
	}
	if r := e.Resolution; r != nil {
		fmt.Fprintf(w, "resolved: %s (%s)\n", r.MatchTier, r.Verdict)
	}
	if e.WebURL != "" {
		fmt.Fprintln(w, e.WebURL)
	}
	if e.Pagination != nil {
		fmt.Fprintln(w, pagingLine(e.Pagination, o))
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
