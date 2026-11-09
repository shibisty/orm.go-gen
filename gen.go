package main

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// Options select what Generate reads and writes.
type Options struct {
	Dir   string   // package directory
	Types []string // struct types to generate for; empty: every struct with db tags
	Tests bool     // read _test.go files too (for models declared in tests)
}

// column is one db field of a model, with its Go access path (m.BaseModel.ID).
type column struct {
	col   string
	path  string // "BaseModel.ID"
	field string // "ID"
}

type model struct {
	name string
	cols []column
}

// known are the embeddable structs of the orm package; their fields cannot
// be read without type-checking the orm, so they are listed here.
var known = map[string][]column{
	"BaseModel":   {{col: "id", path: "ID", field: "ID"}},
	"Timestamps":  {{col: "created_at", path: "CreatedAt", field: "CreatedAt"}, {col: "updated_at", path: "UpdatedAt", field: "UpdatedAt"}},
	"SoftDeletes": {{col: "deleted_at", path: "DeletedAt", field: "DeletedAt"}},
}

// pkgFiles are the parsed files of one package.
type pkgFiles struct {
	name    string
	files   []*ast.File
	structs map[string]*ast.StructType
	generic map[string]bool
	models  map[string]bool // types with a TableName method
	order   []string
	ormName map[*ast.File]string // local name of the "orm" import per file
	fileOf  map[string]*ast.File
}

// Generate returns the generated source for the package in o.Dir and its
// package name.
func Generate(o Options) ([]byte, string, error) {
	pkgs, err := parseDir(o.Dir, o.Tests)
	if err != nil {
		return nil, "", err
	}
	p, err := pickPackage(pkgs, o)
	if err != nil {
		return nil, "", err
	}
	names := o.Types
	if len(names) == 0 {
		for _, n := range p.order {
			// Models only: a struct embedded in models must not get the
			// methods, or they would be promoted to models without their own.
			if p.models[n] && !p.generic[n] && hasDBTags(p, n, map[string]bool{}) {
				names = append(names, n)
			}
		}
		if len(names) == 0 {
			return nil, "", fmt.Errorf("orm-gen: no model (a struct with db tags and a TableName method) in package %s; name the types with -type", p.name)
		}
	}
	var models []model
	for _, n := range names {
		st, ok := p.structs[n]
		if !ok {
			return nil, "", fmt.Errorf("orm-gen: no struct type %s in package %s", n, p.name)
		}
		if p.generic[n] {
			return nil, "", fmt.Errorf("orm-gen: %s is generic; generic models are not supported", n)
		}
		cols, err := p.columns(n, st, "", map[string]bool{n: true})
		if err != nil {
			return nil, "", err
		}
		if len(cols) == 0 {
			return nil, "", fmt.Errorf("orm-gen: %s has no fields with db tags", n)
		}
		models = append(models, model{name: n, cols: cols})
	}
	src, err := render(p.name, models)
	return src, p.name, err
}

func parseDir(dir string, tests bool) (map[string]*pkgFiles, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	pkgs := map[string]*pkgFiles{}
	var names []string
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".go") || strings.HasPrefix(n, "orm_gen") {
			continue
		}
		if strings.HasSuffix(n, "_test.go") && !tests {
			continue
		}
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		f, err := parser.ParseFile(fset, filepath.Join(dir, n), nil, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		if isIgnored(f) {
			continue
		}
		p := pkgs[f.Name.Name]
		if p == nil {
			p = &pkgFiles{name: f.Name.Name, structs: map[string]*ast.StructType{}, generic: map[string]bool{}, models: map[string]bool{},
				ormName: map[*ast.File]string{}, fileOf: map[string]*ast.File{}}
			pkgs[f.Name.Name] = p
		}
		p.files = append(p.files, f)
		for _, imp := range f.Imports {
			if path, _ := strconv.Unquote(imp.Path.Value); path == "orm" {
				name := "orm"
				if imp.Name != nil {
					name = imp.Name.Name
				}
				p.ormName[f] = name
			}
		}
		for _, d := range f.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok && fn.Name.Name == "TableName" && fn.Recv != nil && len(fn.Recv.List) == 1 {
				recv := fn.Recv.List[0].Type
				if star, ok := recv.(*ast.StarExpr); ok {
					recv = star.X
				}
				if id, ok := recv.(*ast.Ident); ok {
					p.models[id.Name] = true
				}
				continue
			}
			g, ok := d.(*ast.GenDecl)
			if !ok || g.Tok != token.TYPE {
				continue
			}
			for _, s := range g.Specs {
				ts := s.(*ast.TypeSpec)
				st, ok := ts.Type.(*ast.StructType)
				if !ok {
					continue
				}
				p.structs[ts.Name.Name] = st
				p.generic[ts.Name.Name] = ts.TypeParams != nil && len(ts.TypeParams.List) > 0
				p.order = append(p.order, ts.Name.Name)
				p.fileOf[ts.Name.Name] = f
			}
		}
	}
	if len(pkgs) == 0 {
		return nil, fmt.Errorf("orm-gen: no Go files in %s", dir)
	}
	return pkgs, nil
}

// isIgnored reports a //go:build ignore file.
func isIgnored(f *ast.File) bool {
	for _, cg := range f.Comments {
		if cg.Pos() > f.Package {
			break
		}
		for _, c := range cg.List {
			if strings.HasPrefix(c.Text, "//go:build") && strings.Contains(c.Text, "ignore") {
				return true
			}
		}
	}
	return false
}

func pickPackage(pkgs map[string]*pkgFiles, o Options) (*pkgFiles, error) {
	if len(pkgs) == 1 {
		for _, p := range pkgs {
			return p, nil
		}
	}
	// A package and its external test package: the one that declares the types.
	var found *pkgFiles
	for _, p := range pkgs {
		if len(o.Types) == 0 {
			if !strings.HasSuffix(p.name, "_test") {
				found = p
			}
			continue
		}
		if _, ok := p.structs[o.Types[0]]; ok {
			found = p
		}
	}
	if found == nil {
		return nil, errors.New("orm-gen: several packages in the directory; name the types with -type")
	}
	return found, nil
}

func hasDBTags(p *pkgFiles, name string, seen map[string]bool) bool {
	st, ok := p.structs[name]
	if !ok || seen[name] {
		return false
	}
	seen[name] = true
	for _, f := range st.Fields.List {
		if tag := dbTag(f); tag != "" && tag != "-" {
			return true
		}
		if len(f.Names) == 0 {
			if id, ok := f.Type.(*ast.Ident); ok && hasDBTags(p, id.Name, seen) {
				return true
			}
		}
	}
	return false
}

func dbTag(f *ast.Field) string {
	if f.Tag == nil {
		return ""
	}
	s, err := strconv.Unquote(f.Tag.Value)
	if err != nil {
		return ""
	}
	return reflect.StructTag(s).Get("db")
}

// columns lists the db fields of struct name the way the orm walks it by
// reflection: exported fields with a db tag other than "-", in order;
// embedded exported structs recursively.
func (p *pkgFiles) columns(name string, st *ast.StructType, prefix string, seen map[string]bool) ([]column, error) {
	var out []column
	file := p.fileOf[name]
	for _, f := range st.Fields.List {
		tag := dbTag(f)
		if len(f.Names) > 0 { // named fields
			for _, n := range f.Names {
				if n.IsExported() && tag != "" && tag != "-" {
					out = append(out, column{col: tag, path: prefix + n.Name, field: n.Name})
				}
			}
			continue
		}
		// An embedded field.
		typ := f.Type
		if star, ok := typ.(*ast.StarExpr); ok {
			// *T is not walked by the orm: a pointer embedding is a plain field.
			fieldName := embeddedName(star.X)
			if fieldName != "" && ast.IsExported(fieldName) && tag != "" && tag != "-" {
				out = append(out, column{col: tag, path: prefix + fieldName, field: fieldName})
			}
			continue
		}
		fieldName := embeddedName(typ)
		if fieldName == "" {
			return nil, fmt.Errorf("orm-gen: %s: unsupported embedded field %T", name, typ)
		}
		if !ast.IsExported(fieldName) {
			continue // unexported embeddings are skipped by the orm too
		}
		switch t := typ.(type) {
		case *ast.Ident:
			inner, ok := p.structs[t.Name]
			if !ok { // a local non-struct type: an ordinary field
				if tag != "" && tag != "-" {
					out = append(out, column{col: tag, path: prefix + fieldName, field: fieldName})
				}
				continue
			}
			if seen[t.Name] {
				return nil, fmt.Errorf("orm-gen: %s embeds itself through %s", name, t.Name)
			}
			seen[t.Name] = true
			cols, err := p.columns(t.Name, inner, prefix+fieldName+".", seen)
			delete(seen, t.Name)
			if err != nil {
				return nil, err
			}
			out = append(out, cols...)
		case *ast.SelectorExpr:
			pkg, _ := t.X.(*ast.Ident)
			if pkg != nil && pkg.Name == p.ormName[file] {
				if cols, ok := known[t.Sel.Name]; ok {
					for _, c := range cols {
						out = append(out, column{col: c.col, path: prefix + fieldName + "." + c.path, field: c.field})
					}
					continue
				}
			}
			if tag == "-" {
				continue
			}
			return nil, fmt.Errorf("orm-gen: %s embeds %s.%s, whose fields orm-gen cannot see; declare the fields in %s or embed a struct of this package", name, pkg.Name, t.Sel.Name, name)
		default:
			return nil, fmt.Errorf("orm-gen: %s: unsupported embedded field", name)
		}
	}
	return out, nil
}

// embeddedName is the field name of an embedded type expression (T, pkg.T, T[X]).
func embeddedName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		return t.Sel.Name
	case *ast.IndexExpr:
		return embeddedName(t.X)
	case *ast.IndexListExpr:
		return embeddedName(t.X)
	}
	return ""
}

func render(pkg string, models []model) ([]byte, error) {
	if pkg == "orm" {
		return nil, errors.New("orm-gen: cannot generate into package orm itself")
	}
	var b bytes.Buffer
	b.WriteString("// Code generated by orm-gen. DO NOT EDIT.\n\n")
	fmt.Fprintf(&b, "package %s\n\nimport (\n\t\"fmt\"\n\n\t\"orm\"\n)\n", pkg)
	for _, m := range models {
		v := lowerFirst(m.name) + "OrmColumns"
		fmt.Fprintf(&b, "\nvar _ orm.Mapper = (*%s)(nil)\n\n", m.name)
		fmt.Fprintf(&b, "var %s = []string{", v)
		for i, c := range m.cols {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(strconv.Quote(c.col))
		}
		b.WriteString("}\n\n")
		fmt.Fprintf(&b, "// OrmColumns implements orm.Mapper.\nfunc (m *%s) OrmColumns() []string { return append([]string(nil), %s...) }\n\n", m.name, v)
		fmt.Fprintf(&b, "// OrmScanTargets implements orm.Mapper.\nfunc (m *%s) OrmScanTargets() []any {\n\treturn []any{", m.name)
		for i, c := range m.cols {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "&m.%s", c.path)
		}
		b.WriteString("}\n}\n\n")
		fmt.Fprintf(&b, "// OrmValues implements orm.Mapper.\nfunc (m *%s) OrmValues() map[string]any {\n\treturn map[string]any{\n", m.name)
		seen := map[string]bool{}
		for _, c := range m.cols {
			if seen[c.col] {
				continue
			}
			seen[c.col] = true
			fmt.Fprintf(&b, "\t\t%q: m.%s,\n", c.col, c.path)
		}
		b.WriteString("\t}\n}\n\n")
		fmt.Fprintf(&b, "// OrmFill implements orm.Mapper.\nfunc (m *%s) OrmFill(row map[string]any) error {\n", m.name)
		for _, c := range m.cols {
			fmt.Fprintf(&b, "\tif v := row[%q]; v != nil {\n\t\tif err := orm.Assign(&m.%s, v); err != nil {\n\t\t\treturn fmt.Errorf(\"orm: field %%q (db:%%q): %%w\", %q, %q, err)\n\t\t}\n\t}\n", c.col, c.path, c.field, c.col)
		}
		b.WriteString("\treturn nil\n}\n")
	}
	return format.Source(b.Bytes())
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}
