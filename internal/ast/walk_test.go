// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package ast

import (
	goast "go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// Every node type Inspect can be handed, and every field of each that holds
// another node.
//
// A walk with a hole in it is the dangerous kind of bug: a check built on top
// reports nothing and reads as a clean bill of health. Reviewing a 40-case
// type switch does not catch a missing branch, and neither does a corpus --
// the construct simply is not looked at.
//
// So this does not review it. For each node type it fills every field that can
// hold a node with a uniquely numbered sentinel, walks the node, and requires
// every sentinel to come back. A node type whose children Inspect forgets
// fails here, and so does a new field added to a node that already works.
func TestInspectReachesEveryChild(t *testing.T) {
	for _, subject := range allNodes() {
		rt := reflect.TypeOf(subject).Elem()
		t.Run(rt.Name(), func(t *testing.T) {
			v := reflect.New(rt)
			setPos(v.Elem(), 1)
			want := map[int]string{}
			fill(v.Elem(), rt.Name(), want)

			got := map[int]bool{}
			Inspect(v.Interface().(Node), func(n Node) bool {
				got[n.Line()] = true
				return true
			})

			var missing []string
			for id, field := range want {
				if !got[id] {
					missing = append(missing, field)
				}
			}
			sort.Strings(missing)
			for _, field := range missing {
				t.Errorf("Inspect does not walk into %s", field)
			}
		})
	}
}

// TestInspectKnowsEveryNodeType: the list above is written by hand, so it can
// fall behind the package it describes. This reads the package's own source
// for the marker methods and requires the two to agree -- a node type added to
// expr.go or ast.go fails here until it is listed and, through the test above,
// until Inspect walks it.
func TestInspectKnowsEveryNodeType(t *testing.T) {
	declared := map[string]bool{}
	fset := token.NewFileSet()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*goast.FuncDecl)
			if !ok || fn.Recv == nil || len(fn.Recv.List) != 1 {
				continue
			}
			if fn.Name.Name != "exprNode" && fn.Name.Name != "stmtNode" {
				continue
			}
			// The receiver is written `(*Name)`, with no variable.
			star, ok := fn.Recv.List[0].Type.(*goast.StarExpr)
			if !ok {
				continue
			}
			if id, ok := star.X.(*goast.Ident); ok {
				declared[id.Name] = true
			}
		}
	}
	listed := map[string]bool{}
	for _, n := range allNodes() {
		listed[reflect.TypeOf(n).Elem().Name()] = true
	}
	for name := range declared {
		if !listed[name] {
			t.Errorf("ast.%s is a node but allNodes() does not list it, so "+
				"nothing checks that Inspect walks into it", name)
		}
	}
	for name := range listed {
		if !declared[name] {
			t.Errorf("allNodes() lists ast.%s, which is no longer a node", name)
		}
	}
	if len(declared) == 0 {
		t.Fatal("found no node types at all; the source scan is broken, " +
			"which would make this test pass for the wrong reason")
	}
}

// allNodes is one of every node type. Order does not matter; completeness is
// checked by TestInspectKnowsEveryNodeType.
func allNodes() []Node {
	return []Node{
		// statements
		&Template{}, &Output{}, &For{}, &If{}, &Assign{}, &AssignBlock{},
		&Macro{}, &CallBlock{}, &FilterBlock{}, &With{}, &Block{},
		&Extends{}, &Include{}, &Import{}, &FromImport{}, &ExprStmt{},
		&Scope{}, &AutoescapeBlock{}, &Break{}, &Continue{},
		// expressions
		&Const{}, &TemplateData{}, &Name{}, &NSRef{}, &Tuple{}, &List{},
		&Dict{}, &Pair{}, &Keyword{}, &CondExpr{}, &BinOp{}, &UnaryOp{},
		&Concat{}, &Compare{}, &Operand{}, &Getattr{}, &Getitem{},
		&Slice{}, &Call{}, &Filter{}, &Test{},
	}
}

var (
	exprType = reflect.TypeOf((*Expr)(nil)).Elem()
	stmtType = reflect.TypeOf((*Stmt)(nil)).Elem()
	nodeType = reflect.TypeOf((*Node)(nil)).Elem()
	posType  = reflect.TypeOf(Pos{})
)

// nextID hands out the line numbers that identify sentinels. It starts high
// enough not to collide with the subject node's own line.
var nextID = 1000

// fill puts a numbered sentinel in every field of v that can hold a node, and
// records which field each number stands for.
func fill(v reflect.Value, owner string, want map[int]string) {
	rt := v.Type()
	for i := range rt.NumField() {
		ft, fv := rt.Field(i), v.Field(i)
		if ft.Type == posType {
			continue
		}
		// Args is embedded in Call, Filter and Test, and its fields are
		// children of whichever one embeds it.
		if ft.Anonymous && ft.Type.Kind() == reflect.Struct && ft.Type != posType {
			fill(fv, owner+"."+ft.Name, want)
			continue
		}
		name := owner + "." + ft.Name
		switch ft.Type.Kind() {
		case reflect.Interface:
			if ft.Type == exprType || ft.Type == stmtType || ft.Type == nodeType {
				fv.Set(reflect.ValueOf(sentinel(ft.Type, name, want)))
			}
		case reflect.Pointer:
			if ft.Type.Implements(nodeType) {
				fv.Set(reflect.ValueOf(sentinel(ft.Type, name, want)))
			}
		case reflect.Slice:
			et := ft.Type.Elem()
			isNode := et == exprType || et == stmtType || et == nodeType ||
				(et.Kind() == reflect.Pointer && et.Implements(nodeType))
			if !isNode {
				continue
			}
			// Two, so that a walk reaching only the first is caught.
			s := reflect.MakeSlice(ft.Type, 2, 2)
			for j := range 2 {
				s.Index(j).Set(reflect.ValueOf(sentinel(et, name, want)))
			}
			fv.Set(s)
		}
	}
}

// sentinel builds a leaf node of a type assignable to ft, numbered so that the
// walk can be asked whether it saw it.
func sentinel(ft reflect.Type, field string, want map[int]string) any {
	nextID++
	id := nextID
	want[id] = field

	var n Node
	switch ft {
	case stmtType:
		// Break is the statement with no children of its own.
		n = &Break{Pos: At(id)}
	case exprType, nodeType:
		// Name is the expression with no children of its own.
		n = &Name{Pos: At(id)}
	default:
		// A concrete pointer field, such as []*Pair or *Call. Its own
		// children are covered when that type is the subject.
		p := reflect.New(ft.Elem())
		setPos(p.Elem(), id)
		n = p.Interface().(Node)
	}
	return n
}

// setPos writes the embedded Pos, which is how a sentinel is identified.
func setPos(v reflect.Value, id int) {
	for i := range v.Type().NumField() {
		if v.Type().Field(i).Type == posType {
			v.Field(i).Set(reflect.ValueOf(At(id)))
			return
		}
	}
}
