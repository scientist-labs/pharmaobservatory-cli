// Package tree turns the OpenAPI document inside the pinned SDK module, joined with the SDK's own
// Go source, into the model the command-tree generator renders.
package tree

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Param is one query parameter of an operation.
type Param struct {
	Name   string // wire name, snake_case
	Flag   string // flag name without the leading dashes
	Desc   string
	Type   string // string, integer, boolean or number
	Setter string // SDK request method
	GoType string // SDK parameter type
	Getter string // pflag getter, for example GetInt32
	Define string // pflag definer, for example Int32
}

// Kinds of page operation.
const (
	KindList      = "list"      // GET /api/v1/{page}
	KindGet       = "get"       // GET /api/v1/{page}/{id} (or another single path parameter, {name})
	KindIDSection = "idsection" // GET /api/v1/{page}/{id}/{key}: a dataset of one record
	KindSection   = "section"   // GET /api/v1/{page}/{key}
)

// Op is one mapped page operation after the join with the SDK.
type Op struct {
	Page        string
	Kind        string
	Key         string // section key, empty for list and get
	Path        string // path template
	OperationID string
	Summary     string
	Params      []Param
	Service     string // APIClient field
	Method      string // SDK method name
	HasID       bool
	IDName      string // the path parameter's wire name (id, or name for datasets), empty without one
	Escaped     bool   // a collection section whose key is a reserved page-group name
}

// Model is everything the renderer needs.
type Model struct {
	SDKModule  string
	SDKVersion string
	Pages      []string // sorted slugs
	Ops        []Op     // sorted by page, kind order, key
	Unmapped   []string // sorted "GET /path" of operations that are not page operations
}

type specParam struct {
	Name        string `yaml:"name"`
	In          string `yaml:"in"`
	Description string `yaml:"description"`
	Schema      struct {
		Type string `yaml:"type"`
	} `yaml:"schema"`
}

type specOp struct {
	OperationID string      `yaml:"operationId"`
	Summary     string      `yaml:"summary"`
	Description string      `yaml:"description"`
	Tags        []string    `yaml:"tags"`
	Parameters  []specParam `yaml:"parameters"`
}

type specOpRef struct {
	Method string
	Path   string
	Op     specOp
}

var (
	// A page (an object type) may contain underscores (cell_lines); its command is the kebab form.
	opIDRe   = regexp.MustCompile(`^([a-z0-9_-]+)\.([a-z0-9_]+)$`)
	pathParm = regexp.MustCompile(`^\{([a-z][a-z0-9_]*)\}$`)
	keyRe    = regexp.MustCompile(`^[a-z0-9_]+$`)
	paramRe  = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	slugRe   = regexp.MustCompile(`^[a-z0-9_-]+$`)
	prefixV1 = "/api/v1/"
)

// readSpec returns the GET operations (and any other methods, as unmappable refs) in path order.
func readSpec(file string) ([]specOpRef, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Paths map[string]map[string]yaml.Node `yaml:"paths"`
	}
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	var refs []specOpRef
	for path, item := range doc.Paths {
		for method, node := range item {
			switch method {
			case "get", "put", "post", "delete", "patch", "head", "options", "trace":
			default:
				continue // path-level keys such as parameters or summary
			}
			var op specOp
			if err := node.Decode(&op); err != nil {
				return nil, fmt.Errorf("%s %s: %w", strings.ToUpper(method), path, err)
			}
			refs = append(refs, specOpRef{Method: strings.ToUpper(method), Path: path, Op: op})
		}
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Path != refs[j].Path {
			return refs[i].Path < refs[j].Path
		}
		return refs[i].Method < refs[j].Method
	})
	return refs, nil
}

// classify decides whether a spec operation is a page operation and which kind. ok=false means
// unmapped. A spec operation that looks like a page operation but disagrees with itself is an error.
func classify(r specOpRef) (page, kind, key string, ok bool, err error) {
	if r.Method != "GET" || !strings.HasPrefix(r.Path, prefixV1) {
		return "", "", "", false, nil
	}
	m := opIDRe.FindStringSubmatch(r.Op.OperationID)
	if m == nil || len(r.Op.Tags) != 1 || r.Op.Tags[0] != m[1] {
		return "", "", "", false, nil
	}
	page = m[1]
	segs := strings.Split(strings.TrimPrefix(r.Path, prefixV1), "/")
	if segs[0] != page {
		return "", "", "", false, nil
	}
	bad := func(want string) error {
		return fmt.Errorf("GET %s: operationId %q must end in .%s for this path shape", r.Path, r.Op.OperationID, want)
	}
	switch {
	case len(segs) == 1:
		if m[2] != "list" {
			return "", "", "", false, bad("list")
		}
		return page, KindList, "", true, nil
	case len(segs) == 2 && pathParm.MatchString(segs[1]):
		if m[2] != "get" {
			return "", "", "", false, bad("get")
		}
		return page, KindGet, "", true, nil
	case len(segs) == 3 && pathParm.MatchString(segs[1]) && keyRe.MatchString(segs[2]):
		if m[2] != segs[2] {
			return "", "", "", false, bad(segs[2])
		}
		return page, KindIDSection, segs[2], true, nil
	case len(segs) == 2 && keyRe.MatchString(segs[1]):
		if m[2] != segs[1] {
			return "", "", "", false, bad(segs[1])
		}
		return page, KindSection, segs[1], true, nil
	}
	return "", "", "", false, nil
}

// pathParam is the name of the single path parameter of a page operation's path, or "".
func pathParam(path string) string {
	segs := strings.Split(strings.TrimPrefix(path, prefixV1), "/")
	if len(segs) >= 2 {
		if m := pathParm.FindStringSubmatch(segs[1]); m != nil {
			return m[1]
		}
	}
	return ""
}

// Kebab turns a snake_case name into its flag or command name.
func Kebab(s string) string { return strings.ReplaceAll(s, "_", "-") }
