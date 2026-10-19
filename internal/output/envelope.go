// Package output renders an API response: a dataset page (a table or CSV built from the response's
// own columns), a record, the dataset catalog, and the footer that always carries the source credit.
package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/scientist-labs/pharmaobservatory-cli/internal/sanitize"
	"strings"

	"github.com/scientist-labs/pharmaobservatory-cli/internal/apierr"
)

// Column is one response column. A dataset response names it ({name, type}); the older section
// shape keyed and labelled it. Parse fills Key and Label from Name when only Name is present.
type Column struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	Unit        string `json:"unit"`
	Description string `json:"description"`
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

// Redirect is a retired id the server answered with its current one.
type Redirect struct {
	From string `json:"from"`
	To   string `json:"to"`
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
	Attribution []Attribution    `json:"-"`

	// The dataset shape: a page carries total and next_cursor at the top level; a record carries
	// record, attributes and the datasets reachable from it; the catalog carries datasets, and one
	// catalog entry carries name, grain and its columns.
	Dataset         string           `json:"dataset"`
	Total           *int64           `json:"total"`
	NextCursor      *string          `json:"next_cursor"`
	Record          json.RawMessage  `json:"record"`
	Attributes      json.RawMessage  `json:"attributes"`
	Datasets        []map[string]any `json:"datasets"`
	Name            string           `json:"name"`
	Title           string           `json:"title"`
	Description     string           `json:"description"`
	Grain           string           `json:"grain"`
	SourceCredit    string           `json:"source_credit"`
	WithheldColumns []string         `json:"withheld_columns"`
	Redirect        *Redirect        `json:"redirect"`
	// Index is a second table a record or a catalog entry prints after its field/value rows: the
	// datasets reachable from the record, or the entry's columns.
	Index *Grid `json:"-"`
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

// Identity is the field/value rows a record (or the older identity block) prints.
type Identity struct {
	Columns []Column      `json:"columns"`
	Rows    []IdentityRow `json:"rows"`
}

// IdentityRow is one field of a record.
type IdentityRow struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Value any    `json:"value"`
}

// Parse reads a 200 body. A body that is not a JSON object is an error.
func Parse(body []byte) (*Envelope, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	e := &Envelope{}
	if err := dec.Decode(e); err != nil {
		return nil, fmt.Errorf("the response is not a dataset response (%v); use --json to see it", err)
	}
	if len(e.RawAttr) > 0 {
		if err := json.Unmarshal(e.RawAttr, &e.Attribution); err != nil {
			var s string
			if json.Unmarshal(e.RawAttr, &s) == nil {
				e.AttrText = s
			}
		}
	}
	if len(e.Attribution) == 0 && e.AttrText == "" && e.SourceCredit != "" {
		e.AttrText = strings.TrimPrefix(e.SourceCredit, "Source: ")
	}
	for i := range e.Columns {
		c := &e.Columns[i]
		if c.Key == "" {
			c.Key = c.Name
		}
		if c.Label == "" {
			c.Label = c.Key
		}
	}
	if err := e.datasetShape(); err != nil {
		return nil, fmt.Errorf("the response is not a dataset response (%v); use --json to see it", err)
	}
	return e, nil
}

// datasetShape maps the dataset response forms onto what the renderer draws: a page's paging
// fields onto Pagination, a record onto Identity and Index, the catalog onto a table, and one
// catalog entry onto Identity and Index. Tiers are never carried over: they are internal labels.
func (e *Envelope) datasetShape() error {
	switch {
	case len(e.Record) > 0 && string(e.Record) != "null":
		return e.recordShape()
	case e.Dataset == "" && e.Rows == nil && e.Name != "" && e.Grain != "":
		e.catalogEntryShape()
	case e.Dataset == "" && e.Columns == nil && e.Datasets != nil:
		e.Columns = []Column{{Key: "name", Label: "name"}, {Key: "title", Label: "title"}, {Key: "grain", Label: "grain"}, {Key: "object_types", Label: "object_types"}, {Key: "column_count", Label: "column_count"}}
		e.Rows = e.Datasets
	}
	if e.Pagination == nil && e.Columns != nil && (e.Dataset != "" || e.Total != nil || e.NextCursor != nil) {
		p := &Pagination{Total: e.Total, NextCursor: e.NextCursor}
		if e.Total != nil {
			p.TotalBasis = "exact"
		}
		e.Pagination = p
	}
	return nil
}

func (e *Envelope) recordShape() error {
	fields, err := ordered(e.Record)
	if err != nil {
		return err
	}
	id := &Identity{}
	add := func(key string, v json.RawMessage) error {
		val, err := decodeValue(v)
		if err != nil {
			return err
		}
		id.Rows = append(id.Rows, IdentityRow{Key: key, Value: val})
		return nil
	}
	for _, f := range fields {
		if err := add(f.key, f.value); err != nil {
			return err
		}
	}
	if len(e.Attributes) > 0 && string(e.Attributes) != "null" {
		groups, err := ordered(e.Attributes)
		if err != nil {
			return err
		}
		for _, g := range groups {
			if string(g.value) == "null" {
				continue
			}
			inner, err := ordered(g.value)
			if err != nil {
				return err
			}
			for _, f := range inner {
				if err := add(g.key+"."+f.key, f.value); err != nil {
					return err
				}
			}
		}
	}
	e.Identity = id
	if len(e.Datasets) > 0 {
		counts := false
		for _, d := range e.Datasets {
			if d["row_count"] != nil {
				counts = true
			}
		}
		g := &Grid{Header: []string{"dataset", "title", "entitled"}}
		if counts {
			g.Header = append(g.Header, "rows")
		}
		for _, d := range e.Datasets {
			entitled := "yes"
			if b, ok := d["entitled"].(bool); ok && !b {
				entitled = "no"
			}
			row := []string{strings.ReplaceAll(Cell(d["name"]), "_", "-"), Cell(d["title"]), entitled}
			if counts {
				row = append(row, Cell(d["row_count"]))
			}
			g.Rows = append(g.Rows, row)
		}
		e.Index = g
	}
	e.Datasets = nil
	return nil
}

func (e *Envelope) catalogEntryShape() {
	id := &Identity{}
	for _, f := range [][2]string{{"name", e.Name}, {"title", e.Title}, {"description", e.Description}, {"grain", e.Grain}} {
		if f[1] != "" {
			id.Rows = append(id.Rows, IdentityRow{Key: f[0], Value: f[1]})
		}
	}
	e.Identity = id
	g := &Grid{Header: []string{"column", "type", "unit", "description"}}
	for _, c := range e.Columns {
		g.Rows = append(g.Rows, []string{c.Name, c.Type, c.Unit, c.Description})
	}
	e.Index = g
	e.Columns = nil
}

type field struct {
	key   string
	value json.RawMessage
}

// ordered reads a JSON object keeping its key order (a record's columns arrive in catalog order).
func ordered(raw json.RawMessage) ([]field, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	t, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := t.(json.Delim); !ok || d != '{' {
		// Name only the JSON kind: the token is server text and this message reaches the terminal.
		return nil, fmt.Errorf("expected an object, got %s", jsonKind(t))
	}
	var out []field
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return nil, err
		}
		k, _ := t.(string)
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		out = append(out, field{key: k, value: v})
	}
	return out, nil
}

// jsonKind names the kind of a JSON token without its value.
func jsonKind(t json.Token) string {
	switch v := t.(type) {
	case json.Delim:
		if v == '[' {
			return "an array"
		}
		return "a delimiter"
	case string:
		return "a string"
	case float64, json.Number:
		return "a number"
	case bool:
		return "a boolean"
	case nil:
		return "null"
	}
	return "an unexpected value"
}

func decodeValue(raw json.RawMessage) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	err := dec.Decode(&v)
	return v, err
}

// State turns an in-body state into its exit-8 error. It returns nil for an ok body.
func (e *Envelope) State() error {
	switch e.Error {
	case "":
		return nil
	case "unbuilt":
		return &apierr.ClassError{Class: apierr.ExitNotAnswered, Msg: "not answered: " + sanitize.Line(strings.Join(e.Unbuilt, ", "))}
	case "not_applicable":
		msg := sanitize.Line(e.Message)
		if msg == "" {
			msg = "this dataset does not apply to this record"
		}
		return &apierr.ClassError{Class: apierr.ExitNotAnswered, Msg: "not applicable: " + msg}
	}
	return fmt.Errorf("unexpected answer %q: %s", sanitize.Line(e.Error), sanitize.Line(e.Message))
}

// IsTable reports whether the body has a table to draw.
func (e *Envelope) IsTable() bool { return e.Columns != nil }
