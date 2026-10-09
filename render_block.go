// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"context"
	"io"
	"strings"

	"github.com/mgilbir/gojja2/errs"
	"github.com/mgilbir/gojja2/value"
)

// RenderBlock renders one `{% block %}` of the template on its own, which is
// what a page that swaps a fragment in place -- an htmx response, say -- asks
// for: the block's output and nothing around it.
//
// It is jinja2's
//
//	"".join(t.blocks[name](t.new_context(vars)))
//
// which is also what the jinja2-fragments package's render_block does, and it
// behaves the same in every respect, because rendering a block on its own is a
// different thing from rendering the template and cutting the block out:
//
//   - Only the template's own blocks can be named. A child template's block
//     overrides the parent's and renders; a block only the parent defines is
//     a KeyError, as is a name no block has.
//   - Nothing outside the block runs. `{% extends %}` is never evaluated, so
//     `super()` has no parent block to reach and is an UndefinedError; a
//     top-level `{% set %}`, `{% import %}` or `{% macro %}` was never
//     executed, so the block does not see it; and a `required` block is not
//     checked. Put what the block needs inside it, or pass it in vars.
//   - `self.other()` reaches the template's other blocks, a scoped block sees
//     the loop variables vars provides, and autoescaping is the template's.
//
// The block is rendered completely before anything is written, so a block
// that fails writes nothing to w. The render is bounded by ctx and the
// environment's limits, as [Template.Render] is.
func (t *Template) RenderBlock(ctx context.Context, w io.Writer, name string, vars map[string]any) (err error) {
	defer catchPanic(&err)
	if err := t.requireBlock(name); err != nil {
		return err
	}
	st := t.newState(nil, 0, newBudget(ctx, t.env))
	st.contextVars.raw, st.contextVars.expose, st.contextVars.budget = vars, t.env.methods, st
	return t.renderBlock(st, w, name)
}

// RenderBlockString is [Template.RenderBlock] returning the block's output.
func (t *Template) RenderBlockString(ctx context.Context, name string, vars map[string]any) (string, error) {
	var out strings.Builder
	if err := t.RenderBlock(ctx, &out, name, vars); err != nil {
		return "", err
	}
	return out.String(), nil
}

// RenderBlockValues is [Template.RenderBlock] with variables that are already
// template values. As with [Template.RenderValues], they are used as they are,
// so a block that mutates one mutates the caller's.
func (t *Template) RenderBlockValues(ctx context.Context, w io.Writer, name string, vars map[string]value.Value) (err error) {
	defer catchPanic(&err)
	if err := t.requireBlock(name); err != nil {
		return err
	}
	return t.renderBlock(t.newState(vars, 0, newBudget(ctx, t.env)), w, name)
}

// requireBlock refuses a name the template defines no block for, as jinja2's
// t.blocks[name] does: a KeyError whose message is the name's repr.
func (t *Template) requireBlock(name string) error {
	if _, ok := t.blocks[name]; ok {
		return nil
	}
	e := errs.New(errs.KeyError, "%s", value.Repr(value.String(name)))
	e.Name = t.name
	return e
}

// renderBlock runs the block the way `self.name()` does -- the same
// BlockReference, so super(), scoping, escaping and the recursion bound are
// that code's -- and writes what it returns.
func (t *Template) renderBlock(st *State, w io.Writer, name string) error {
	v, err := (&blockReference{st: st, name: name}).render()
	if err != nil {
		return err
	}
	// As in renderState: a charge refused somewhere that had nowhere to
	// report it still fails the render.
	if st.budget != nil && st.budget.failed != nil {
		return st.budget.failed
	}
	_, err = io.WriteString(w, v.AsString())
	return err
}
