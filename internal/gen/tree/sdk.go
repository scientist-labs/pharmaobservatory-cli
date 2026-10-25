package tree

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// sdkPos is one positional parameter of a public SDK method.
type sdkPos struct {
	Name   string
	GoType string // string, or an integer type in PositionalTypes
}

// PositionalTypes are the Go types a positional (path) parameter may have. A string is passed
// through verbatim; an integer is parsed from the argument by pages.ParseIntID, which must know
// every integer type listed here.
var PositionalTypes = map[string]bool{"string": true, "int": true, "int8": true, "int16": true, "int32": true, "int64": true}

type sdkSetter struct {
	Name   string
	GoType string
}

// sdkOp is one operation as the SDK's Go source declares it.
type sdkOp struct {
	Method     string // public method, for example CellLinesList
	Service    string // service type, for example CellLinesAPIService
	HTTP       string // GET, from http.MethodGet
	Path       string
	Positional []sdkPos // positional parameters after ctx
	Query      map[string]sdkSetter
}

func (o sdkOp) key() string { return o.HTTP + " " + o.Path }

// parseSDK reads api_*.go and client.go in dir and returns the operations keyed "METHOD path" and
// the APIClient field for each service type.
func parseSDK(dir string) (map[string]sdkOp, map[string]string, error) {
	files, err := filepath.Glob(filepath.Join(dir, "api_*.go"))
	if err != nil {
		return nil, nil, err
	}
	if len(files) == 0 {
		return nil, nil, fmt.Errorf("no api_*.go files in %s", dir)
	}
	sort.Strings(files)
	fset := token.NewFileSet()
	var asts []*ast.File
	for _, f := range files {
		a, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			return nil, nil, err
		}
		asts = append(asts, a)
	}
	// setters by request type: field -> setter
	setters := map[string]map[string]sdkSetter{}
	var execs []*ast.FuncDecl
	publics := map[string]*ast.FuncDecl{}
	for _, a := range asts {
		for _, d := range a.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Recv == nil || len(fd.Recv.List) != 1 {
				continue
			}
			recv := recvName(fd.Recv.List[0].Type)
			switch {
			case strings.HasSuffix(recv, "APIService") && strings.HasSuffix(fd.Name.Name, "Execute"):
				execs = append(execs, fd)
			case strings.HasSuffix(recv, "APIService"):
				publics[recv+"."+fd.Name.Name] = fd
			case strings.HasPrefix(recv, "Api") && strings.HasSuffix(recv, "Request") && fd.Name.Name != "Execute":
				field, ok := setterField(fd)
				if !ok {
					continue
				}
				if setters[recv] == nil {
					setters[recv] = map[string]sdkSetter{}
				}
				setters[recv][field] = sdkSetter{Name: fd.Name.Name, GoType: types.ExprString(fd.Type.Params.List[0].Type)}
			}
		}
	}
	ops := map[string]sdkOp{}
	for _, fd := range execs {
		service := recvName(fd.Recv.List[0].Type)
		method := strings.TrimSuffix(fd.Name.Name, "Execute")
		op := sdkOp{Method: method, Service: service, Query: map[string]sdkSetter{}}
		reqType := ""
		if ps := fd.Type.Params.List; len(ps) == 1 {
			reqType = types.ExprString(ps[0].Type)
		}
		if reqType == "" {
			return nil, nil, fmt.Errorf("%s.%s: cannot find the request type", service, fd.Name.Name)
		}
		var walkErr error
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.ValueSpec:
				for i, name := range x.Names {
					if name.Name == "localVarHTTPMethod" && i < len(x.Values) {
						if sel, ok := x.Values[i].(*ast.SelectorExpr); ok {
							op.HTTP = strings.ToUpper(strings.TrimPrefix(sel.Sel.Name, "Method"))
						}
					}
				}
			case *ast.AssignStmt:
				if len(x.Lhs) == 1 && len(x.Rhs) == 1 {
					if id, ok := x.Lhs[0].(*ast.Ident); ok && id.Name == "localVarPath" && x.Tok == token.DEFINE {
						if be, ok := x.Rhs[0].(*ast.BinaryExpr); ok {
							if lit, ok := be.Y.(*ast.BasicLit); ok && lit.Kind == token.STRING {
								op.Path, walkErr = strconv.Unquote(lit.Value)
							}
						}
					}
				}
			case *ast.CallExpr:
				if id, ok := x.Fun.(*ast.Ident); ok && id.Name == "parameterAddToHeaderOrQuery" && len(x.Args) >= 3 {
					lit, ok1 := x.Args[1].(*ast.BasicLit)
					sel, ok2 := x.Args[2].(*ast.SelectorExpr)
					if ok1 && ok2 && lit.Kind == token.STRING {
						wire, _ := strconv.Unquote(lit.Value)
						s, found := setters[reqType][sel.Sel.Name]
						if !found {
							walkErr = fmt.Errorf("%s.%s: no setter for query parameter %q", service, fd.Name.Name, wire)
						}
						op.Query[wire] = s
					}
				}
			}
			return true
		})
		if walkErr != nil {
			return nil, nil, walkErr
		}
		if op.HTTP == "" || op.Path == "" {
			return nil, nil, fmt.Errorf("%s.%s: cannot read the HTTP method or path", service, fd.Name.Name)
		}
		pub := publics[service+"."+method]
		if pub == nil {
			return nil, nil, fmt.Errorf("%s.%s: no public method %s", service, fd.Name.Name, method)
		}
		for i, f := range pub.Type.Params.List {
			if i == 0 { // ctx
				continue
			}
			t := types.ExprString(f.Type)
			if !PositionalTypes[t] {
				return nil, nil, fmt.Errorf("%s.%s: positional parameter of type %s is not supported", service, method, t)
			}
			for _, n := range f.Names {
				op.Positional = append(op.Positional, sdkPos{Name: n.Name, GoType: t})
			}
		}
		if _, dup := ops[op.key()]; dup {
			return nil, nil, fmt.Errorf("the SDK declares %s twice", op.key())
		}
		ops[op.key()] = op
	}
	fields, err := clientFields(fset, dir)
	if err != nil {
		return nil, nil, err
	}
	return ops, fields, nil
}

// clientFields maps each service type to its APIClient field name.
func clientFields(fset *token.FileSet, dir string) (map[string]string, error) {
	a, err := parser.ParseFile(fset, filepath.Join(dir, "client.go"), nil, 0)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, d := range a.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, s := range gd.Specs {
			ts, ok := s.(*ast.TypeSpec)
			if !ok || ts.Name.Name != "APIClient" {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				continue
			}
			for _, f := range st.Fields.List {
				star, ok := f.Type.(*ast.StarExpr)
				if !ok || len(f.Names) != 1 {
					continue
				}
				if id, ok := star.X.(*ast.Ident); ok && strings.HasSuffix(id.Name, "APIService") {
					out[id.Name] = f.Names[0].Name
				}
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("client.go: no service fields on APIClient")
	}
	return out, nil
}

func recvName(e ast.Expr) string {
	if s, ok := e.(*ast.StarExpr); ok {
		e = s.X
	}
	if id, ok := e.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

// setterField finds "r.field = &x" in a setter body and returns field.
func setterField(fd *ast.FuncDecl) (string, bool) {
	if fd.Body == nil || fd.Type.Params == nil || len(fd.Type.Params.List) != 1 {
		return "", false
	}
	field := ""
	for _, st := range fd.Body.List {
		as, ok := st.(*ast.AssignStmt)
		if !ok || len(as.Lhs) != 1 {
			continue
		}
		if sel, ok := as.Lhs[0].(*ast.SelectorExpr); ok {
			field = sel.Sel.Name
		}
	}
	return field, field != ""
}
