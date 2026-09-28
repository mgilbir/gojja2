// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"github.com/mgilbir/gojja2/errs"
	"github.com/mgilbir/gojja2/internal/ast"
	"github.com/mgilbir/gojja2/value"
)

// depChecker rejects, at compile time, a filter or test the environment does
// not have.
//
// jinja2 does the same from its compiler, with one exception it documents in
// passing: inside an `{% if %}` or a conditional expression the lookup is
// deferred to runtime, because the branch may never execute. The error differs
// too -- "No test named 'x'." when compiled, "No test named 'x' found." when
// it finally runs -- so the distinction is visible and worth keeping.
//
// It has no walk of its own. jinja2 looks a name up as its code generator
// writes the node out, in the same pass that folds it, so the fold drives this
// one expression at a time: see constFolder.check. Which of two faults a
// template is refused for depends on that order.
//
// `soft` is jinja2's soft_frame, and topLevel and the rest are the generator's
// frame flags; constFolder carries them all and hands them over here.
type depChecker struct {
	env    *Environment
	name   string
	source string
	err    error
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
