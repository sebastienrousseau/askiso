// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

package iso20022

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// This package is AskISO's public API, and pkg.go.dev is where its users
// read it. An exported identifier without a doc comment renders there as a
// bare signature, so every one must carry a comment.
func TestPublicAPIIsDocumented(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range undocumented(f) {
			t.Errorf("%s: %s has no doc comment", name, id)
		}
	}
}

// undocumented lists the exported declarations in f that have no doc
// comment.
func undocumented(f *ast.File) []string {
	var names []string
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			if d.Name.IsExported() && exportedReceiver(d) && d.Doc == nil {
				names = append(names, d.Name.Name)
			}
		case *ast.GenDecl:
			names = append(names, undocumentedSpecs(d)...)
		}
	}
	return names
}

// exportedReceiver reports whether a function is reachable from outside the
// package: a plain function, or a method on an exported type.
func exportedReceiver(d *ast.FuncDecl) bool {
	if d.Recv == nil || len(d.Recv.List) == 0 {
		return true
	}
	typ := d.Recv.List[0].Type
	if star, ok := typ.(*ast.StarExpr); ok {
		typ = star.X
	}
	id, ok := typ.(*ast.Ident)
	return ok && id.IsExported()
}

// undocumentedSpecs lists the exported types, constants and variables in a
// declaration that have no comment of their own or on their group.
func undocumentedSpecs(d *ast.GenDecl) []string {
	if d.Doc != nil {
		return nil
	}
	var names []string
	for _, s := range d.Specs {
		switch s := s.(type) {
		case *ast.TypeSpec:
			if s.Name.IsExported() && s.Doc == nil {
				names = append(names, s.Name.Name)
			}
		case *ast.ValueSpec:
			names = append(names, undocumentedValues(s)...)
		}
	}
	return names
}

// undocumentedValues lists the exported names in a constant or variable
// spec that has no comment.
func undocumentedValues(s *ast.ValueSpec) []string {
	if s.Doc != nil || s.Comment != nil {
		return nil
	}
	var names []string
	for _, n := range s.Names {
		if n.IsExported() {
			names = append(names, n.Name)
		}
	}
	return names
}
