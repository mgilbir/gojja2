// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"errors"
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
	ex := &exec{st: st, sc: st.ctx, out: &out, stream: &out, autoescape: st.autoescape}

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
	// ctx is the template's root frame: the render arguments sit in a
	// scope beneath it, so a top-level `{% set %}` shadows an argument of
	// the same name from the start of the render.
	ctx *scope
	// contextVars is that underlying scope -- jinja2's context.vars. A
	// top-level assignment is written here as well as into the frame, and
	// blocks resolve against it, which is why a block sees an argument the
	// root frame has shadowed but not yet assigned.
	contextVars *scope

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
	// The render arguments get a scope of their own, below the one the
	// template writes into. A top-level `{% set %}` then shadows an
	// argument of the same name from the start of the render, which is
	// what makes `{% for %}{{ x }}{% endfor %}{% set x = 1 %}` render
	// nothing even when x was passed in.
	arguments := newScope(globals)
	for k, v := range vars {
		arguments.vars[k] = v
	}
	ctx := newScope(arguments)

	blocks := make(map[string][]blockEntry, len(t.blocks))
	for name, node := range t.blocks {
		blocks[name] = []blockEntry{{tmpl: t, node: node}}
	}

	st := &State{
		env:         t.env,
		tmpl:        t,
		root:        t,
		globals:     globals,
		ctx:         ctx,
		contextVars: arguments,
		blocks:      blocks,
		autoescape:  t.env.escapes(t.name),
	}
	declareFrameLocals(ctx, st, t.tree.Body)
	return st
}

// RecursionMessage is what CPython reports when the interpreter runs out of
// stack, and therefore what a template that recurses without a base case must
// report here.
//
// The wording names where inside CPython the limit was hit, which says nothing
// about a Go program -- but the actionable half is true in both, and anyone
// diffing the two implementations should not have to filter this out. The
// configured limit, which our own wording used to carry, is on the error's
// Limit field instead.
const RecursionMessage = "maximum recursion depth exceeded while calling a Python object"

// RecursionMessageComparison is the variant CPython produces when the stack
// ran out inside a comparison, which is what an inheritance cycle hits.
const RecursionMessageComparison = "maximum recursion depth exceeded in comparison"

// enter bounds template recursion. The limit is a safety control: a template
// that includes itself would otherwise exhaust the stack.
func (s *State) enter() error {
	s.depth++
	if s.depth > s.env.maxRecursion {
		e := errs.New(errs.RecursionError, "%s", RecursionMessage)
		e.Limit = s.env.maxRecursion
		return e
	}
	return nil
}

// enterExtends is enter for an {% extends %} chain, which CPython reports with
// its comparison wording because the cycle is detected while matching names.
func (s *State) enterExtends() error {
	err := s.enter()
	var e *errs.Error
	if errors.As(err, &e) && e.Kind == errs.RecursionError {
		e.Msg = RecursionMessageComparison
	}
	return err
}

func (s *State) leave() { s.depth-- }
