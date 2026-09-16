// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"io"
	"strings"

	"github.com/mgilbir/gojja2/errs"
	"github.com/mgilbir/gojja2/internal/ast"
	"github.com/mgilbir/gojja2/value"
)

// Template is a compiled template.
type Template struct {
	env    *Environment
	name   string
	source string
	tree   *ast.Template
	blocks map[string]*ast.Block
}

// Name returns the template's name, empty for one compiled from a string.
func (t *Template) Name() string { return t.name }

// Render renders the template with the given variables.
func (t *Template) Render(vars map[string]any) (string, error) {
	values := make(map[string]value.Value, len(vars))
	for k, v := range vars {
		values[k] = value.FromGo(v)
	}
	return t.RenderValues(values)
}

// RenderTo renders the template into w.
func (t *Template) RenderTo(w io.Writer, vars map[string]any) error {
	out, err := t.Render(vars)
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, out)
	return err
}

// RenderValues renders the template with variables that are already template
// values, skipping the Go conversion.
func (t *Template) RenderValues(vars map[string]value.Value) (string, error) {
	return t.render(vars, 0)
}

// render is RenderValues with an inherited recursion depth.
//
// The depth has to cross the template boundary: an `{% include %}` renders
// into a fresh State, so a counter that started at zero each time would never
// fire and a self-including template would take the stack out instead.
func (t *Template) render(vars map[string]value.Value, depth int) (string, error) {
	st := t.newState(vars)
	st.depth = depth
	var out strings.Builder
	ex := &exec{st: st, sc: st.ctx, out: &out, autoescape: st.autoescape}

	if err := ex.execBody(t.tree.Body); err != nil {
		return "", err
	}
	// A template that extends renders nothing itself beyond whatever came
	// before the extends tag; the parent is rendered afterwards, with the
	// blocks the child registered. The loop handles a chain of any depth.
	for st.parent != nil {
		parent := st.parent
		st.parent = nil
		prev := st.tmpl
		st.tmpl = parent
		err := ex.execBody(parent.tree.Body)
		st.tmpl = prev
		if err != nil {
			return "", err
		}
	}
	return out.String(), nil
}

// blockEntry is one definition of a block in the inheritance chain. Index 0 of
// a name's slice is the most derived definition, which is the one that
// renders; super() steps toward the base.
type blockEntry struct {
	tmpl *Template
	node *ast.Block
}

// State is the per-render state a filter, test or global may need.
type State struct {
	env  *Environment
	tmpl *Template // the template whose body is executing
	root *Template // the template the render started from

	globals *scope
	// ctx holds template-level variables: the render arguments plus
	// whatever top-level `{% set %}` assigns. Macros and unscoped blocks
	// resolve against it rather than against the local frame.
	ctx *scope

	blocks     map[string][]blockEntry
	autoescape bool
	parent     *Template

	// exported lists the names a top-level `{% set %}` bound, in order, so
	// `{% import %}` can expose them.
	exported []string
	// depth bounds include/extends/macro nesting.
	depth int
}

// Env returns the environment the render is running under.
func (s *State) Env() *Environment { return s.env }

// Name returns the name of the template currently executing.
func (s *State) Name() string { return s.tmpl.name }

// Autoescape reports whether the render is currently escaping output.
func (s *State) Autoescape() bool { return s.autoescape }

// Resolve looks a name up in the template context.
func (s *State) Resolve(name string) (value.Value, bool) { return s.ctx.lookup(name) }

// Undefined builds an undefined value under the environment's policy.
func (s *State) Undefined(v value.Value) value.Value {
	return v.WithBehavior(s.env.undefined)
}

func (t *Template) newState(vars map[string]value.Value) *State {
	globals := &scope{vars: t.env.globals}
	ctx := newScope(globals)
	for k, v := range vars {
		ctx.vars[k] = v
	}

	blocks := make(map[string][]blockEntry, len(t.blocks))
	for name, node := range t.blocks {
		blocks[name] = []blockEntry{{tmpl: t, node: node}}
	}

	return &State{
		env:        t.env,
		tmpl:       t,
		root:       t,
		globals:    globals,
		ctx:        ctx,
		blocks:     blocks,
		autoescape: t.env.escapes(t.name),
	}
}

// enter bounds template recursion. The limit is a safety control: a template
// that includes itself would otherwise exhaust the stack.
func (s *State) enter() error {
	s.depth++
	if s.depth > s.env.maxRecursion {
		return errs.New(errs.RecursionError,
			"maximum template recursion depth of %d exceeded", s.env.maxRecursion)
	}
	return nil
}

func (s *State) leave() { s.depth-- }
