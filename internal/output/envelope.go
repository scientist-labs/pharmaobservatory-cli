// Package output renders a section or list envelope: a table or CSV built from the envelope's own
// columns, and the footer that always carries the source credit.
package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/scientist-labs/pharmaobservatory-cli/internal/apierr"
)

// Column is one envelope column.
type Column struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Type  string `json:"type"`
}

// Attribution is one source credit.
type Attribution struct {
	Name    string  `json:"name"`
	License *string `json:"license"`
	Href    string  `json:"href"`
}

// Pagination is the paged-section block.
type Pagination struct {
	Limit      int     `json:"limit"`
	Total      *int64  `json:"total"`
	TotalBasis string  `json:"total_basis"`
	NextCursor *string `json:"next_cursor"`
}

// SectionRef is one entry of a get response's sections index.
type SectionRef struct {
	Key       string `json:"key"`
	Heading   string `json:"heading"`
	Available bool   `json:"available"`
}

// Envelope is the part of a response body the renderer reads. Rows stay raw JSON so a column the
// typed SDK model does not know still renders.
type Envelope struct {
	Error       string           `json:"error"`
	Message     string           `json:"message"`
	Unbuilt     []string         `json:"unbuilt_relations"`
	Shape       *struct{}        `json:"shape"`
	Columns     []Column         `json:"columns"`
	Rows        []map[string]any `json:"rows"`
	RawAttr     json.RawMessage  `json:"attribution"`
	Pagination  *Pagination      `json:"pagination"`
	WebURL      string           `json:"web_url"`
	BuildState  *BuildState      `json:"build_state"`
	Resolution  *Resolution      `json:"resolution_confidence"`
	Identity    *Identity        `json:"identity"`
	Sections    []SectionRef     `json:"sections"`
	Attribution []Attribution    `json:"-"`
	// AttrText is a plain-string attribution (the not_entitled body carries one).
	AttrText string `json:"-"`
}

// BuildState is the as-of statement.
type BuildState struct {
	Built      bool   `json:"built"`
	Generation string `json:"generation"`
	LastLoaded any    `json:"last_loaded"`
}

// Resolution is how an id was resolved.
type Resolution struct {
	MatchTier string `json:"match_tier"`
	Verdict   string `json:"verdict"`
}

// Identity is a get response's identity block: label/value rows.
type Identity struct {
	Columns []Column `json:"columns"`
	Rows    []struct {
		Key   string `json:"key"`
		Label string `json:"label"`
		Value any    `json:"value"`
	} `json:"rows"`
}

// Parse reads a 200 body. A body that is not a JSON object is an error.
func Parse(body []byte) (*Envelope, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	e := &Envelope{}
	if err := dec.Decode(e); err != nil {
		return nil, fmt.Errorf("the response is not a section envelope (%v); use --json to see it", err)
	}
	if len(e.RawAttr) > 0 {
		if err := json.Unmarshal(e.RawAttr, &e.Attribution); err != nil {
			var s string
			if json.Unmarshal(e.RawAttr, &s) == nil {
				e.AttrText = s
			}
		}
	}
	return e, nil
}

// State turns an in-body state into its exit-8 error. It returns nil for an ok body.
func (e *Envelope) State() error {
	switch e.Error {
	case "":
		return nil
	case "unbuilt":
		return &apierr.ClassError{Class: apierr.ExitNotAnswered, Msg: "not answered: " + Line(strings.Join(e.Unbuilt, ", "))}
	case "not_applicable":
		msg := Line(e.Message)
		if msg == "" {
			msg = "this section does not apply to this record"
		}
		return &apierr.ClassError{Class: apierr.ExitNotAnswered, Msg: "not applicable: " + msg}
	}
	return fmt.Errorf("unexpected answer %q: %s", Line(e.Error), Line(e.Message))
}

// IsTable reports whether the body has a table to draw.
func (e *Envelope) IsTable() bool { return e.Columns != nil }
