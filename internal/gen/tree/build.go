package tree

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// typeMap pairs a spec schema type with the SDK Go types it may be joined to, and says which pflag
// definer and getter carry it.
var typeMap = map[string]map[string][2]string{
	"string":  {"string": {"String", "GetString"}},
	"integer": {"int32": {"Int32", "GetInt32"}, "int64": {"Int64", "GetInt64"}},
	"boolean": {"bool": {"Bool", "GetBool"}},
	"number":  {"float32": {"Float32", "GetFloat32"}, "float64": {"Float64", "GetFloat64"}},
}

// globalFlags are the persistent flags of the root command (and cobra's help). A query parameter
// of the same name becomes --param-<name>.
var globalFlags = map[string]bool{
	"profile": true, "api-base": true, "json": true, "csv": true, "all": true,
	"verbose": true, "quiet": true, "no-footer": true, "no-wait": true, "wait": true, "help": true,
}

// PageReserved are the names under a page group that a collection section cannot take. Such a
// section is registered only as `po <page> section <key>`.
var PageReserved = map[string]bool{"list": true, "get": true, "help": true, "completion": true, "section": true}

// TopReserved are the names at the top level that a page slug cannot take. Such a page is
// registered only as `po page <slug>`.
var TopReserved = map[string]bool{
	"login": true, "logout": true, "whoami": true, "version": true, "config": true, "auth": true,
	"api": true, "query": true, "exit-codes": true, "help": true, "completion": true, "get": true, "list": true, "page": true,
}

var kindOrder = map[string]int{KindList: 0, KindGet: 1, KindIDSection: 2, KindSection: 3}

// Build reads dir (the SDK module root) and returns the model. It fails when a page operation
// cannot be joined to the SDK exactly: a mismatch must break generation, not guess.
func Build(dir, module, version string) (*Model, error) {
	refs, err := readSpec(filepath.Join(dir, "api", "openapi.yaml"))
	if err != nil {
		return nil, err
	}
	sdkOps, fields, err := parseSDK(dir)
	if err != nil {
		return nil, err
	}
	m := &Model{SDKModule: module, SDKVersion: version}
	pages := map[string]bool{}
	seen := map[string]string{}
	for _, r := range refs {
		page, kind, key, ok, err := classify(r)
		if err != nil {
			return nil, err
		}
		if !ok {
			m.Unmapped = append(m.Unmapped, r.Method+" "+r.Path)
			continue
		}
		if !slugRe.MatchString(page) {
			return nil, fmt.Errorf("page slug %q is not a command name", page)
		}
		so, found := sdkOps["GET "+r.Path]
		if !found {
			return nil, fmt.Errorf("GET %s (%s) is in the document but the SDK source has no such operation", r.Path, r.Op.OperationID)
		}
		field, found := fields[so.Service]
		if !found {
			return nil, fmt.Errorf("%s: service %s is not a field of APIClient", r.Path, so.Service)
		}
		op := Op{Page: page, Kind: kind, Key: key, Path: r.Path, OperationID: r.Op.OperationID,
			Summary: strings.TrimSpace(r.Op.Summary), Service: field, Method: so.Method,
			HasID: strings.Contains(r.Path, "{id}")}
		wantPos := 0
		if op.HasID {
			wantPos = 1
		}
		if len(so.Positional) != wantPos || (wantPos == 1 && so.Positional[0] != "id") {
			return nil, fmt.Errorf("GET %s: SDK positional parameters %v do not match the path", r.Path, so.Positional)
		}
		specQ := map[string]specParam{}
		for _, p := range r.Op.Parameters {
			switch p.In {
			case "path":
				if p.Name != "id" {
					return nil, fmt.Errorf("GET %s: path parameter %q is not supported", r.Path, p.Name)
				}
			case "query":
				if !paramRe.MatchString(p.Name) {
					return nil, fmt.Errorf("GET %s: query parameter %q is not a valid flag source", r.Path, p.Name)
				}
				specQ[p.Name] = p
			default:
				return nil, fmt.Errorf("GET %s: parameter %q is in %q, which is not supported", r.Path, p.Name, p.In)
			}
		}
		for name := range so.Query {
			if _, ok := specQ[name]; !ok {
				return nil, fmt.Errorf("GET %s: the SDK sends query parameter %q the document does not declare", r.Path, name)
			}
		}
		names := make([]string, 0, len(specQ))
		for n := range specQ {
			names = append(names, n)
		}
		sort.Strings(names)
		flags := map[string]bool{}
		for _, n := range names {
			sp := specQ[n]
			s, ok := so.Query[n]
			if !ok {
				return nil, fmt.Errorf("GET %s: the document declares query parameter %q the SDK does not send", r.Path, n)
			}
			t := sp.Schema.Type
			if t == "" {
				t = "string"
			}
			def, ok := typeMap[t][s.GoType]
			if !ok {
				return nil, fmt.Errorf("GET %s: parameter %q is %s in the document but %s in the SDK", r.Path, n, t, s.GoType)
			}
			flag := Kebab(n)
			if globalFlags[flag] {
				flag = "param-" + flag
			}
			if flags[flag] {
				return nil, fmt.Errorf("GET %s: two parameters map to --%s", r.Path, flag)
			}
			flags[flag] = true
			desc := strings.TrimSpace(sp.Description)
			if desc == "" {
				desc = "query parameter " + n
			}
			op.Params = append(op.Params, Param{Name: n, Flag: flag, Desc: desc, Type: t,
				Setter: s.Name, GoType: s.GoType, Define: def[0], Getter: def[1]})
		}
		id := fmt.Sprintf("%s|%s|%s", page, kind, key)
		if prev, dup := seen[id]; dup {
			return nil, fmt.Errorf("GET %s and %s both map to %s", r.Path, prev, id)
		}
		seen[id] = r.Path
		op.Escaped = kind == KindSection && PageReserved[key]
		pages[page] = true
		m.Ops = append(m.Ops, op)
	}
	for p := range pages {
		m.Pages = append(m.Pages, p)
	}
	sort.Strings(m.Pages)
	sort.Strings(m.Unmapped)
	sort.Slice(m.Ops, func(i, j int) bool {
		a, b := m.Ops[i], m.Ops[j]
		if a.Page != b.Page {
			return a.Page < b.Page
		}
		if a.Kind != b.Kind {
			return kindOrder[a.Kind] < kindOrder[b.Kind]
		}
		return a.Key < b.Key
	})
	return m, nil
}

// CheckAllowlist fails when the unmapped (non-page) operations differ from the hand-mapped set.
func CheckAllowlist(unmapped, allow []string) error {
	have := map[string]bool{}
	for _, u := range unmapped {
		have[u] = true
	}
	ok := map[string]bool{}
	for _, a := range allow {
		ok[a] = true
	}
	var unexpected, gone []string
	for _, u := range unmapped {
		if !ok[u] {
			unexpected = append(unexpected, u)
		}
	}
	for _, a := range allow {
		if !have[a] {
			gone = append(gone, a)
		}
	}
	if len(unexpected) == 0 && len(gone) == 0 {
		return nil
	}
	return fmt.Errorf("non-page operations not hand-mapped: %v; allowlisted but no longer in the document: %v", unexpected, gone)
}

// PageEscaped reports whether a page slug collides with a top-level command.
func PageEscaped(slug string) bool { return TopReserved[slug] }
