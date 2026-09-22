// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"github.com/mgilbir/gojja2/errs"
	"github.com/mgilbir/gojja2/internal/ast"
	"github.com/mgilbir/gojja2/value"
)

// checkDependencies rejects, at compile time, a filter or test the environment
// does not have.
//
// jinja2 does the same from its compiler, with one exception it documents in
// passing: inside an `{% if %}` or a conditional expression the lookup is
// deferred to runtime, because the branch may never execute. The error differs
// too -- "No test named 'x'." when compiled, "No test named 'x' found." when
// it finally runs -- so the distinction is visible and worth keeping.
func (e *Environment) checkDependencies(body []ast.Stmt, name, source string) error {
	c := &depChecker{env: e, name: name, source: source, topLevel: true}
	c.stmts(body, false)
	return c.err
}

type depChecker struct {
	env    *Environment
	name   string
	source string
	err    error
	// topLevel tracks whether the statements being walked are compiled
	// into the template's own function. Only `{% extends %}` reads it; see
	// inner for what clears it.
	topLevel bool
}

// inner walks a body that jinja2 compiles into a frame of its own.
//
// Every block-opening construct does that except `{% if %}`, which compiles
// inline and so leaves the template's top level intact -- that is what makes
// the conditional-extends idiom legal at any depth of conditions.
//
// A frame of its own also stops a branch from softening what it holds. jinja2
// sets soft_frame on an `{% if %}` (and on an inline conditional) and clears it
// on every frame.inner(), so an unknown filter directly inside a branch is
// reported when the branch runs, while the same filter one loop or macro deeper
// is refused when the template compiles -- the loop's function is generated
// whether or not the branch can be taken.
//
//	{% if nil %}{{ 1|nosuch }}{% endif %}                              renders ""
//	{% if nil %}{% for i in xs %}{{ 1|nosuch }}{% endfor %}{% endif %}  refused
func (c *depChecker) inner(body []ast.Stmt) {
	saved := c.topLevel
	c.topLevel = false
	c.stmts(body, false)
	c.topLevel = saved
}

func (c *depChecker) fail(kind string, filterName string, line int) {
	if c.err != nil {
		return
	}
	e := errs.New(errs.TemplateAssertionError, "No %s named %s.",
		kind, value.Repr(value.String(filterName)))
	e.Line = line
	e.Name = c.name
	e.Source = c.source
	c.err = e
}

func (c *depChecker) stmts(body []ast.Stmt, soft bool) {
	for _, stmt := range body {
		c.stmt(stmt, soft)
	}
}

func (c *depChecker) stmt(stmt ast.Stmt, soft bool) {
	if c.err != nil {
		return
	}
	switch n := stmt.(type) {
	case *ast.Output:
		c.exprs(n.Nodes, soft)
	case *ast.For:
		// `loop` is bound by the loop itself, so assigning it anywhere
		// inside would leave the two fighting over one name.
		if line, found := findLoopStore(n); found {
			c.failAt(line, "Can't assign to special loop variable in for-loop target")
		}
		c.expr(n.Iter, soft)
		// The loop's own test is part of the function the loop becomes,
		// so it is refused at compile time even inside a branch that
		// cannot be taken. Its *iterable* is evaluated where the loop is
		// written, and softens with everything else there.
		c.expr(n.Test, false)
		c.inner(n.Body)
		c.inner(n.Else)
	case *ast.If:
		// An if softens its whole subtree, condition and body alike.
		c.expr(n.Test, true)
		c.stmts(n.Body, true)
		for _, elif := range n.Elif {
			c.expr(elif.Test, true)
			c.stmts(elif.Body, true)
		}
		c.stmts(n.Else, true)
	case *ast.Assign:
		c.expr(n.Node, soft)
	case *ast.AssignBlock:
		// The filter runs over the block's buffer, and is resolved with
		// that buffer's frame rather than the one the block sits in.
		c.expr(n.Filter, false)
		c.inner(n.Body)
	case *ast.With:
		c.exprs(n.Values, soft)
		c.inner(n.Body)
	case *ast.Macro:
		c.checkCallerDefault(n.Args, n.Defaults, n.Line())
		// Defaults are part of the macro's signature, generated with the
		// body rather than at the point of definition.
		c.exprs(n.Defaults, false)
		c.inner(n.Body)
	case *ast.CallBlock:
		c.checkCallerDefault(n.Args, n.Defaults, n.Line())
		// The call itself is made where the block is written, so it
		// softens; the block's own parameters belong to its signature.
		c.expr(n.Call, soft)
		c.exprs(n.Defaults, false)
		c.inner(n.Body)
	case *ast.FilterBlock:
		// The filter naming the block is resolved where the block is
		// generated, which happens whether or not the branch runs.
		c.expr(n.Filter, false)
		c.inner(n.Body)
	case *ast.Block:
		c.inner(n.Body)
	case *ast.ExprStmt:
		c.expr(n.Node, soft)
	case *ast.Include:
		c.expr(n.Template, soft)
	case *ast.Import:
		c.expr(n.Template, soft)
	case *ast.FromImport:
		c.expr(n.Template, soft)
	case *ast.Extends:
		// Which template a render extends has to be settled once, for
		// the whole render. Reached from a frame, it would depend on
		// control flow: gojja2 let `{% for i in [] %}{% extends %}`
		// through, so an empty sequence silently skipped the
		// inheritance and a non-empty one applied it.
		if !c.topLevel {
			c.failAt(n.Line(), "cannot use extend from a non top-level scope")
		}
		c.expr(n.Template, soft)
	case *ast.Scope:
		c.inner(n.Body)
	case *ast.AutoescapeBlock:
		c.expr(n.Value, soft)
		c.inner(n.Body)
	}
}

func (c *depChecker) failAt(line int, format string, args ...any) {
	if c.err != nil {
		return
	}
	e := errs.New(errs.TemplateAssertionError, format, args...)
	e.Line, e.Name, e.Source = line, c.name, c.source
	c.err = e
}

// checkCallerDefault enforces that a declared `caller` parameter has a
// default. Without one, a macro invoked outside a {% call %} block would leave
// it unbound, and jinja2 refuses the definition rather than the call.
func (c *depChecker) checkCallerDefault(params []*ast.Name, defaults []ast.Expr, line int) {
	// Defaults align with the tail of the parameter list, so a parameter
	// before that tail has none.
	firstDefault := len(params) - len(defaults)
	for i, p := range params {
		if p.Name == "caller" && i < firstDefault {
			c.failAt(line, "When defining macros or call blocks the special"+
				` "caller" argument must be omitted or be given a default.`)
			return
		}
	}
}

// findLoopStore reports an assignment to `loop` anywhere inside a for loop.
func findLoopStore(n *ast.For) (int, bool) {
	var line int
	found := false
	var walkExpr func(ast.Expr)
	walkExpr = func(e ast.Expr) {
		switch t := e.(type) {
		case *ast.Name:
			if t.Store && t.Name == "loop" && !found {
				line, found = t.Line(), true
			}
		case *ast.Tuple:
			for _, item := range t.Items {
				walkExpr(item)
			}
		}
	}
	var walk func([]ast.Stmt)
	walk = func(body []ast.Stmt) {
		for _, stmt := range body {
			switch t := stmt.(type) {
			case *ast.Assign:
				walkExpr(t.Target)
			case *ast.AssignBlock:
				walkExpr(t.Target)
			case *ast.For:
				walkExpr(t.Target)
				walk(t.Body)
				walk(t.Else)
			case *ast.If:
				walk(t.Body)
				for _, elif := range t.Elif {
					walk(elif.Body)
				}
				walk(t.Else)
			case *ast.With:
				for _, target := range t.Targets {
					walkExpr(target)
				}
				walk(t.Body)
			}
		}
	}
	walkExpr(n.Target)
	walk(n.Body)
	walk(n.Else)
	return line, found
}

func (c *depChecker) exprs(list []ast.Expr, soft bool) {
	for _, e := range list {
		c.expr(e, soft)
	}
}

func (c *depChecker) expr(e ast.Expr, soft bool) {
	if e == nil || c.err != nil {
		return
	}
	switch n := e.(type) {
	case *ast.Filter:
		if _, ok := c.env.filters[n.Name]; !ok && !soft {
			c.fail("filter", n.Name, n.Line())
		}
		c.expr(n.Node, soft)
		c.args(n.Args, soft)
	case *ast.Test:
		if _, ok := c.env.tests[n.Name]; !ok && !soft {
			c.fail("test", n.Name, n.Line())
		}
		c.expr(n.Node, soft)
		c.args(n.Args, soft)
	case *ast.CondExpr:
		// A conditional expression softens its branches too.
		c.expr(n.Test, true)
		c.expr(n.True, true)
		c.expr(n.False, true)
	case *ast.Tuple:
		c.exprs(n.Items, soft)
	case *ast.List:
		c.exprs(n.Items, soft)
	case *ast.Dict:
		for _, p := range n.Items {
			c.expr(p.Key, soft)
			c.expr(p.Value, soft)
		}
	case *ast.BinOp:
		c.expr(n.Left, soft)
		c.expr(n.Right, soft)
	case *ast.UnaryOp:
		c.expr(n.Node, soft)
	case *ast.Concat:
		c.exprs(n.Nodes, soft)
	case *ast.Compare:
		c.expr(n.Expr, soft)
		for _, op := range n.Ops {
			c.expr(op.Expr, soft)
		}
	case *ast.Getattr:
		c.expr(n.Node, soft)
	case *ast.Getitem:
		c.expr(n.Node, soft)
		c.expr(n.Arg, soft)
	case *ast.Slice:
		c.expr(n.Start, soft)
		c.expr(n.Stop, soft)
		c.expr(n.Step, soft)
	case *ast.Call:
		c.expr(n.Node, soft)
		c.args(n.Args, soft)
	}
}

func (c *depChecker) args(a ast.Args, soft bool) {
	c.exprs(a.Args, soft)
	for _, kw := range a.Kwargs {
		c.expr(kw.Value, soft)
	}
	c.expr(a.DynArgs, soft)
	c.expr(a.DynKwargs, soft)
}
