// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"github.com/mgilbir/gojja2/internal/ast"
	"github.com/mgilbir/gojja2/value"
)

// frameLocals reports the names a frame binds before it runs.
//
// jinja2 decides this per frame, in source order, on the *first* mention of a
// name: a read first means the name resolves from the context, a write first
// means the frame owns it and it starts out undefined. The difference is
// visible whenever a template both receives a variable and assigns it:
//
//	{{ x }}{% set x = 1 %}                            -> the context's x
//	{% for i in [1] %}{{ x }}{% endfor %}{% set x = 1 %} -> undefined
//
// In the second case the root frame owns x because its first mention is the
// assignment, so the loop -- which reads through to the root -- sees nothing,
// even though x was passed in.
//
// The walk stops where jinja2's does: at the bodies of for, macro, block,
// block-set and scope, which are frames of their own. It descends into if,
// where a name assigned in only some branches keeps resolving from the
// context, because the branch that assigns it may not run.
func frameLocals(body []ast.Stmt) []string {
	v := &frameVisitor{seen: map[string]bool{}}
	v.stmts(body)
	return v.locals
}

type frameVisitor struct {
	// seen records the names already decided at this level.
	seen map[string]bool
	// locals are the names whose first mention was a write.
	locals []string
}

func (v *frameVisitor) load(name string) {
	if !v.seen[name] {
		v.seen[name] = true
	}
}

func (v *frameVisitor) store(name string) {
	if !v.seen[name] {
		v.seen[name] = true
		v.locals = append(v.locals, name)
	}
}

// settle marks a name as decided without claiming it, which is what a
// conditional assignment does.
func (v *frameVisitor) settle(name string) { v.seen[name] = true }

func (v *frameVisitor) stmts(body []ast.Stmt) {
	for _, stmt := range body {
		v.stmt(stmt)
	}
}

func (v *frameVisitor) stmt(stmt ast.Stmt) {
	switch n := stmt.(type) {
	case *ast.Output:
		v.exprs(n.Nodes)
	case *ast.ExprStmt:
		v.expr(n.Node)
	case *ast.Assign:
		// The value is read before the target is bound.
		v.expr(n.Node)
		v.expr(n.Target)
	case *ast.AssignBlock:
		// The body is a frame of its own; only the target binds here.
		v.expr(n.Target)
	case *ast.For:
		// A loop body is its own frame; only the iterable is read here.
		v.expr(n.Iter)
	case *ast.With:
		v.exprs(n.Values)
	case *ast.Macro:
		v.store(n.Name)
	case *ast.CallBlock:
		v.expr(n.Call)
	case *ast.FilterBlock:
		v.expr(n.Filter)
	case *ast.Block, *ast.Scope:
		// Compiled as separate functions; nothing binds here.
	case *ast.If:
		v.ifStmt(n)
	case *ast.Include:
		v.expr(n.Template)
	case *ast.Import:
		v.expr(n.Template)
		v.store(n.Target)
	case *ast.FromImport:
		v.expr(n.Template)
		for _, entry := range n.Names {
			v.store(entry.Alias)
		}
	case *ast.Extends:
		v.expr(n.Template)
	case *ast.AutoescapeBlock:
		v.expr(n.Value)
		v.stmts(n.Body)
	}
}

// ifStmt applies jinja2's branch rule: a name counts as bound here only when
// every branch binds it, and jinja2 always counts three -- body, elifs and
// else -- so an if/else pair alone is not enough.
func (v *frameVisitor) ifStmt(n *ast.If) {
	v.expr(n.Test)

	branch := func(body []ast.Stmt) map[string]bool {
		sub := &frameVisitor{seen: map[string]bool{}}
		sub.stmts(body)
		out := make(map[string]bool, len(sub.locals))
		for _, name := range sub.locals {
			out[name] = true
		}
		return out
	}

	var elifBody []ast.Stmt
	for _, elif := range n.Elif {
		elifBody = append(elifBody, elif)
	}
	branches := []map[string]bool{branch(n.Body), branch(elifBody), branch(n.Else)}

	counts := map[string]int{}
	for _, b := range branches {
		for name := range b {
			counts[name]++
		}
	}
	for name, count := range counts {
		if count == len(branches) {
			v.store(name)
			continue
		}
		v.settle(name)
	}
}

func (v *frameVisitor) exprs(items []ast.Expr) {
	for _, e := range items {
		v.expr(e)
	}
}

func (v *frameVisitor) expr(e ast.Expr) {
	switch n := e.(type) {
	case nil:
		return
	case *ast.Name:
		if n.Store {
			v.store(n.Name)
			return
		}
		v.load(n.Name)
	case *ast.NSRef:
		v.load(n.Name)
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

func (v *frameVisitor) args(a ast.Args) {
	v.exprs(a.Args)
	for _, kw := range a.Kwargs {
		v.expr(kw.Value)
	}
	v.expr(a.DynArgs)
	v.expr(a.DynKwargs)
}

// declareFrameLocals pre-binds a frame's own names to undefined.
//
// A name an *enclosing frame* already binds is left alone, so it reads through
// instead -- jinja2 aliases to the outer binding in exactly that case. The
// render arguments are not a frame, which is why the root declares
// unconditionally and shadows them.
func declareFrameLocals(sc *scope, st *State, body []ast.Stmt) {
	isRoot := sc == st.ctx
	for _, name := range frameLocals(body) {
		if !isRoot && sc.parent != nil {
			if _, found := sc.parent.lookupUntil(name, st.ctx); found {
				continue
			}
		}
		sc.set(name, st.Undefined(value.NewUndefined(name)))
	}
}
