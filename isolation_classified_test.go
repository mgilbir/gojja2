// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgilbir/gojja2/value"
)

// isolationClass says, for every type a template can hold as an object, why
// value.Isolate does or does not copy it. A type added without a line here
// fails TestEveryObjectIsClassifiedForIsolation: the question "can a template
// change this in place, and could it outlive a render?" has to be answered on
// purpose, because the default -- shared -- is the dangerous one.
var isolationClass = map[string]string{
	// Changed in place by a template; copied by IsolateWith or, for Set, by
	// value.Isolate itself.
	"namespaceObject": "copied", "cyclerObject": "copied", "joinerObject": "copied",
	"dictView": "copied", "mappingProxy": "copied", "groupObject": "copied",
	"value.Set": "copied",
	// Nothing a template does changes them.
	"rangeObject": "immutable", "classObject": "immutable", "descriptorObject": "immutable",
	"unboundMethodObject": "immutable", "builtinFunc": "immutable",
	"value.timeObject": "immutable", "value.opaqueObject": "immutable",
	"value.methodObject": "immutable", "templateReference": "immutable",
	// Made by a render for that render and never stored anywhere longer-lived.
	"loopObject": "render-local", "blockReference": "render-local",
	"moduleObject": "render-local", "macroObject": "render-local",
	// The host's own value, exposed on purpose: a template reads its fields
	// and calls only the methods the host's method policy allows, and a
	// method that changes the host's state is the host's decision.
	"value.structObject": "host",
}

func TestEveryObjectIsClassifiedForIsolation(t *testing.T) {
	found := map[string]bool{}
	for pkg, prefix := range map[string]string{".": "", "value": "value."} {
		files, err := filepath.Glob(filepath.Join(pkg, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		fset := token.NewFileSet()
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			file, err := parser.ParseFile(fset, f, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Recv == nil || fn.Name.Name != "GetAttr" {
					continue
				}
				recv := fn.Recv.List[0].Type
				if star, ok := recv.(*ast.StarExpr); ok {
					recv = star.X
				}
				id, ok := recv.(*ast.Ident)
				if !ok {
					continue
				}
				found[prefix+id.Name] = true
				if isolationClass[prefix+id.Name] == "" {
					t.Errorf("%s%s is a template object and is not classified for isolation: "+
						"if a template can change it in place, give it IsolateWith (value.Isolator) "+
						"and list it as copied; otherwise say why it need not be", prefix, id.Name)
				}
			}
		}
	}
	for name := range isolationClass {
		if !found[name] {
			t.Errorf("%s is classified for isolation and is not a template object; remove the line", name)
		}
	}
	// What is listed as copied must actually be.
	for _, v := range []value.Value{
		value.FromObject(newNamespace(0)), value.FromObject(&cyclerObject{}), value.FromObject(&joinerObject{}),
		value.FromObject(&dictView{d: value.NewDict()}), value.FromObject(&mappingProxy{d: value.NewDict()}),
		value.FromObject(&groupObject{}),
	} {
		if _, ok := v.Interface().(value.Isolator); !ok {
			t.Errorf("%T is listed as copied and is not a value.Isolator", v.Interface())
		}
	}
}
