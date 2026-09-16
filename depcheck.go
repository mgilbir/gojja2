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
	c := &depChecker{env: e, name: name, source: source}
	c.stmts(body, false)
	return c.err
}

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
		c.expr(n.Iter, soft)
		c.expr(n.Test, soft)
		c.stmts(n.Body, soft)
		c.stmts(n.Else, soft)
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
		c.expr(n.Filter, soft)
		c.stmts(n.Body, soft)
	case *ast.With:
		c.exprs(n.Values, soft)
		c.stmts(n.Body, soft)
	case *ast.Macro:
		c.exprs(n.Defaults, soft)
		c.stmts(n.Body, soft)
	case *ast.CallBlock:
		c.expr(n.Call, soft)
		c.exprs(n.Defaults, soft)
		c.stmts(n.Body, soft)
	case *ast.FilterBlock:
		c.expr(n.Filter, soft)
		c.stmts(n.Body, soft)
	case *ast.Block:
		c.stmts(n.Body, soft)
	case *ast.ExprStmt:
		c.expr(n.Node, soft)
	case *ast.Include:
		c.expr(n.Template, soft)
	case *ast.Import:
		c.expr(n.Template, soft)
	case *ast.FromImport:
		c.expr(n.Template, soft)
	case *ast.Extends:
		c.expr(n.Template, soft)
	case *ast.Scope:
		c.stmts(n.Body, soft)
	case *ast.AutoescapeBlock:
		c.expr(n.Value, soft)
		c.stmts(n.Body, soft)
	}
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
