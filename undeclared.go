// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import "github.com/mgilbir/gojja2/internal/ast"

// findUndeclared reports which of the given names a body reads without having
// bound them first.
//
// This is how a macro learns whether it accepts extra arguments: jinja2 gives
// a macro `varargs`, `kwargs` or `caller` only when its body actually mentions
// them, and rejects the corresponding arguments otherwise. A name that is
// stored before it is loaded is the body's own, and stops being watched --
// which is what the visitor below tracks, in the same order jinja2 walks.
func findUndeclared(body []ast.Stmt, names ...string) map[string]bool {
	v := &undeclaredVisitor{
		watching:   make(map[string]bool, len(names)),
		undeclared: make(map[string]bool, len(names)),
	}
	for _, name := range names {
		v.watching[name] = true
	}
	for _, stmt := range body {
		v.stmt(stmt)
	}
	return v.undeclared
}

type undeclaredVisitor struct {
	watching   map[string]bool
	undeclared map[string]bool
}

func (v *undeclaredVisitor) name(n *ast.Name) {
	if !v.watching[n.Name] {
		return
	}
	if n.Store {
		// Bound here, so any later read is of the body's own variable.
		delete(v.watching, n.Name)
		return
	}
	v.undeclared[n.Name] = true
}

func (v *undeclaredVisitor) stmts(body []ast.Stmt) {
	for _, stmt := range body {
		v.stmt(stmt)
	}
}

func (v *undeclaredVisitor) stmt(stmt ast.Stmt) {
	switch n := stmt.(type) {
	case *ast.Output:
		v.exprs(n.Nodes)
	case *ast.For:
		// The iterable is read before the target is bound.
		v.expr(n.Iter)
		v.expr(n.Target)
		v.expr(n.Test)
		v.stmts(n.Body)
		v.stmts(n.Else)
	case *ast.If:
		v.expr(n.Test)
		v.stmts(n.Body)
		for _, elif := range n.Elif {
			v.expr(elif.Test)
			v.stmts(elif.Body)
		}
		v.stmts(n.Else)
	case *ast.Assign:
		v.expr(n.Node)
		v.expr(n.Target)
	case *ast.AssignBlock:
		v.stmts(n.Body)
		v.expr(n.Filter)
		v.expr(n.Target)
	case *ast.With:
		v.exprs(n.Values)
		v.exprs(n.Targets)
		v.stmts(n.Body)
	case *ast.Macro:
		// A nested macro has its own varargs and kwargs.
	case *ast.CallBlock:
		v.expr(n.Call)
		v.stmts(n.Body)
	case *ast.FilterBlock:
		v.stmts(n.Body)
		v.expr(n.Filter)
	case *ast.Block:
		// jinja2 does not descend into blocks here: a block body is
		// compiled as its own function with its own scope.
	case *ast.ExprStmt:
		v.expr(n.Node)
	case *ast.Include:
		v.expr(n.Template)
	case *ast.Import:
		v.expr(n.Template)
	case *ast.FromImport:
		v.expr(n.Template)
	case *ast.Extends:
		v.expr(n.Template)
	case *ast.Scope:
		v.stmts(n.Body)
	case *ast.AutoescapeBlock:
		v.expr(n.Value)
		v.stmts(n.Body)
	}
}

func (v *undeclaredVisitor) exprs(list []ast.Expr) {
	for _, e := range list {
		v.expr(e)
	}
}

func (v *undeclaredVisitor) expr(e ast.Expr) {
	if e == nil || len(v.watching) == 0 {
		return
	}
	switch n := e.(type) {
	case *ast.Name:
		v.name(n)
	case *ast.NSRef:
		v.name(&ast.Name{Name: n.Name})
	case *ast.Tuple:
		v.exprs(n.Items)
	case *ast.List:
		v.exprs(n.Items)
	case *ast.Dict:
		for _, p := range n.Items {
			v.expr(p.Key)
			v.expr(p.Value)
		}
	case *ast.CondExpr:
		v.expr(n.Test)
		v.expr(n.True)
		v.expr(n.False)
	case *ast.BinOp:
		v.expr(n.Left)
		v.expr(n.Right)
	case *ast.UnaryOp:
		v.expr(n.Node)
	case *ast.Concat:
		v.exprs(n.Nodes)
	case *ast.Compare:
		v.expr(n.Expr)
		for _, op := range n.Ops {
			v.expr(op.Expr)
		}
	case *ast.Getattr:
		v.expr(n.Node)
	case *ast.Getitem:
		v.expr(n.Node)
		v.expr(n.Arg)
	case *ast.Slice:
		v.expr(n.Start)
		v.expr(n.Stop)
		v.expr(n.Step)
	case *ast.Call:
		v.expr(n.Node)
		v.args(n.Args)
	case *ast.Filter:
		v.expr(n.Node)
		v.args(n.Args)
	case *ast.Test:
		v.expr(n.Node)
		v.args(n.Args)
	}
}

func (v *undeclaredVisitor) args(a ast.Args) {
	v.exprs(a.Args)
	for _, kw := range a.Kwargs {
		v.expr(kw.Value)
	}
	v.expr(a.DynArgs)
	v.expr(a.DynKwargs)
}
