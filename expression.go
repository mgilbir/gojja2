// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"context"

	"github.com/mgilbir/gojja2/internal/ast"
	"github.com/mgilbir/gojja2/internal/parser"
	"github.com/mgilbir/gojja2/value"
)

// Expression is one compiled expression, evaluated for its value rather than
// rendered as text. It is jinja2's TemplateExpression, which
// [Environment.CompileExpression] returns.
//
// An Expression is safe to evaluate from many goroutines at once, as a
// [Template] is to render.
type Expression struct {
	tmpl          *Template
	keepUndefined bool
}

// CompileExpression compiles source as a single expression -- `user.age >= 18`,
// `items|selectattr("active")|list` -- which is jinja2's
// Environment.compile_expression, for using the template language's rules in
// configuration or in conditions a host evaluates.
//
// The source is read as though it stood inside `{{ }}`. Anything after the
// expression is the syntax error "chunk after expression", as it is in jinja2:
// `a b`, `a, b` (a tuple needs parentheses here), and `a }} b`.
//
// An expression whose value is undefined evaluates to None; see
// [Expression.KeepUndefined] to have the undefined itself instead.
func (e *Environment) CompileExpression(source string) (*Expression, error) {
	expr, err := parser.ParseExpression(e.syntax, e.parseOpts, source, "")
	if err != nil {
		return nil, err
	}
	// jinja2 compiles exactly this tree, `result = <expr>` at the top
	// level, through from_string -- so the expression is folded, checked
	// and evaluated by the same rules as a template's top-level `{% set %}`.
	line := expr.Line()
	tree := &ast.Template{Body: []ast.Stmt{&ast.Assign{
		Pos:    ast.Pos{L: line},
		Target: &ast.Name{Pos: ast.Pos{L: line}, Name: expressionResult, Store: true},
		Node:   expr,
	}}}
	tmpl, err := e.compileTree(tree, source, "", true)
	if err != nil {
		return nil, err
	}
	return &Expression{tmpl: tmpl}, nil
}

// expressionResult is the name jinja2's compile_expression assigns to.
const expressionResult = "result"

// KeepUndefined returns a copy of x that evaluates to an undefined value as
// itself rather than as None -- compile_expression's undefined_to_none=False.
func (x *Expression) KeepUndefined() *Expression {
	return &Expression{tmpl: x.tmpl, keepUndefined: true}
}

// Eval evaluates the expression with vars as its variables, converting them as
// [Template.Render] does. ctx bounds the evaluation as it bounds a render, and
// so do the environment's limits.
func (x *Expression) Eval(ctx context.Context, vars map[string]any) (v value.Value, err error) {
	defer catchPanic(&err)
	st := x.tmpl.newState(nil, 0, newBudget(ctx, x.tmpl.env))
	st.contextVars.raw, st.contextVars.expose, st.contextVars.budget = vars, x.tmpl.env.methods, st
	return x.result(st)
}

// EvalValues is [Expression.Eval] with variables that are already template
// values. As with [Template.RenderValues], they are used as they are, so an
// expression that mutates one -- `lst.append(1)` -- mutates the caller's.
func (x *Expression) EvalValues(ctx context.Context, vars map[string]value.Value) (v value.Value, err error) {
	defer catchPanic(&err)
	return x.result(x.tmpl.newState(vars, 0, newBudget(ctx, x.tmpl.env)))
}

// result runs the compiled assignment and reads back what it assigned.
func (x *Expression) result(st *State) (value.Value, error) {
	if err := x.tmpl.renderState(st, discardWriter{}, nil); err != nil {
		return value.Undefined, err
	}
	v, _ := st.ctx.get(expressionResult)
	if v.IsUndefined() && !x.keepUndefined {
		return value.None, nil
	}
	return v, nil
}

// discardWriter is where an expression's render writes, which is nowhere: the
// tree is one assignment and prints nothing.
type discardWriter struct{}

func (discardWriter) WriteString(s string) (int, error) { return len(s), nil }
