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
// frameLocalsOf is frameLocals with the answer remembered.
//
// The names a body owns are a property of the tree, which does not change after
// it is compiled -- but this was walking the whole body again on every entry to
// the frame. A macro called fifty times walked its body fifty times, and a loop
// walked its body once per iteration, which is where 15% of the allocations in
// BenchmarkRenderMacro came from.
//
// The key is the node that owns the body, so two frames never share an entry.
// A sync.Map because one compiled template is rendered from many goroutines.
//
// It hangs off the template being rendered rather than off the Environment: a
// long-lived Environment compiles new templates, each with new nodes, so a
// cache there would grow without bound -- the same reason the template cache
// itself is bounded. Here it is reachable only while the template is, and holds
// at most one entry per frame in the templates that render reaches.
func (t *Template) frameLocalsOf(key any, body []ast.Stmt) frameNames {
	if key == nil || t == nil {
		return frameLocals(body)
	}
	if v, ok := t.frameLocals.Load(key); ok {
		return v.(frameNames)
	}
	names := frameLocals(body)
	t.frameLocals.Store(key, names)
	return names
}

func frameLocals(body []ast.Stmt) frameNames {
	v := &frameVisitor{seen: map[string]bool{}}
	v.stmts(body)
	return frameNames{owns: v.locals, refs: v.seen}
}

// frameNames is what one frame does with names at its own level: owns are the
// ones whose first mention is a write, refs are every name mentioned at all.
//
// The difference decides what a *nested* frame sees. jinja2's Symbols.store
// asks the parent symbol table for a reference before settling on undefined --
// so a name the enclosing frame merely reads is aliased into the inner frame,
// while one it never mentions starts undefined there.
type frameNames struct {
	owns []string
	refs map[string]bool
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
		// A scope of its own, like {% with %} and {% filter %}: only
		// the expression is read here. jinja2 compiles the block as a
		// Scope, so what the body assigns does not reach this frame --
		// and, the other way round, a name this frame assigns *later*
		// is already its local when the body reads it, which makes the
		// read undefined rather than a fall-through to the context.
		v.expr(n.Value)
	}
}

// ifStmt applies jinja2's branch rule.
//
// Two things happen. A name merely *mentioned* in a branch settles at this
// level, so a later assignment no longer claims it -- which is why
// `{% if m %}{{ m }}{% endif %}{% from "x" import m %}` still sees the
// argument m. And a name *assigned* in a branch only counts as bound here when
// every branch binds it; jinja2 always counts three -- body, elifs and else --
// so an if/else pair alone is not enough.
func (v *frameVisitor) ifStmt(n *ast.If) {
	v.expr(n.Test)

	branch := func(body []ast.Stmt) (mentioned, stored map[string]bool) {
		sub := &frameVisitor{seen: map[string]bool{}}
		sub.stmts(body)
		stored = make(map[string]bool, len(sub.locals))
		for _, name := range sub.locals {
			stored[name] = true
		}
		return sub.seen, stored
	}

	var elifBody []ast.Stmt
	for _, elif := range n.Elif {
		elifBody = append(elifBody, elif)
	}
	bodies := [][]ast.Stmt{n.Body, elifBody, n.Else}

	mentioned := map[string]bool{}
	counts := map[string]int{}
	for _, body := range bodies {
		seen, stored := branch(body)
		for name := range seen {
			mentioned[name] = true
		}
		for name := range stored {
			counts[name]++
		}
	}
	for name := range mentioned {
		if counts[name] == len(bodies) {
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
// declareFrameLocals gives a frame the names it owns before it runs.
//
// A name an *enclosing frame* owns is not cleared: jinja2 aliases the
// enclosing frame's binding into the new one, so a macro body sees what the
// template had assigned by the time it was called. A name nothing above owns
// starts undefined, which is what makes a loop inside the frame see nothing
// until the assignment runs.
//
// enclosing is the frame this one nests inside. It is nil for the two that
// nest inside none: the template's own frame, and a {% block %} body -- which
// jinja2 compiles as a standalone function resolving against the context. The
// context is not an enclosing frame, so a value passed in does *not* survive
// the block owning the name, and a block that assigns `x` late reads nothing
// for it early even when `x` was an argument.
// declareRootLocals is declareFrameLocals for a frame with nothing enclosing
// it: a template's root frame, and a block body, which resolves against the
// render arguments rather than against an enclosing frame.
//
// It is separate so that "this call cannot be refused" is something the
// signature states rather than something a reader has to re-derive. Nothing is
// looked up when there is no enclosing frame, so no render argument is
// converted, so there is no budget to refuse it.
func declareRootLocals(sc *scope, st *State, key any, body []ast.Stmt) {
	names := st.root.frameLocalsOf(key, body)
	sc.refs = names.refs
	for _, name := range names.owns {
		sc.set(name, st.Undefined(value.NewUndefined(name)))
	}
}

// declareFrameLocals declares a nested frame's locals, aliasing each from the
// enclosing frame where that frame binds it.
//
// It can be refused: resolving a name against the enclosing chain may be what
// first converts a render argument, and that conversion is charged.
func declareFrameLocals(sc *scope, st *State, key any, body []ast.Stmt, enclosing *scope) error {
	names := st.root.frameLocalsOf(key, body)
	// Recorded so a frame nested inside this one can ask what this one
	// mentions, which is what decides whether its own stores alias.
	sc.refs = names.refs
	for _, name := range names.owns {
		if enclosing != nil {
			v, found, err := enclosing.lookupUntil(name, st.ctx)
			if err != nil {
				return err
			}
			if found {
				// Aliased at entry, as jinja2 does it, so a
				// later change to the enclosing binding does
				// not reach in here.
				sc.set(name, v)
				continue
			}
			// Nothing above binds it yet -- but jinja2 asks the
			// parent symbol table for a *reference*, not a value.
			// An enclosing frame that merely reads the name has one,
			// resolving from the context, and the store aliases
			// that. So a root-level `{{ m }}` anywhere in the
			// template, even inside `{% if false %}`, is enough to
			// make this frame's m the context's rather than
			// undefined -- while a read inside a nested frame is
			// not, because that is a different symbol table.
			if enclosingReferences(enclosing, st.ctx, name) {
				v, found, err := enclosing.lookup(name)
				if err != nil {
					return err
				}
				if found {
					sc.set(name, v)
					continue
				}
			}
		}
		sc.set(name, st.Undefined(value.NewUndefined(name)))
	}
	return nil
}

// enclosingReferences reports whether any frame from sc up to and including
// stop mentions name at its own level.
func enclosingReferences(sc *scope, stop *scope, name string) bool {
	for cur := sc; cur != nil; cur = cur.parent {
		if cur.refs[name] {
			return true
		}
		if cur == stop {
			break
		}
	}
	return false
}
