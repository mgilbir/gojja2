// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"context"
	"slices"
	"strings"
	"sync"

	"github.com/mgilbir/gojja2/errs"
	"github.com/mgilbir/gojja2/value"
)

// Module is a template rendered for its definitions: the macros and top-level
// `{% set %}` names it exports, and the text its body produced. It is what
// `{% import %}` binds inside a template, handed to the host instead.
//
// It is jinja2's TemplateModule, as Template.make_module(vars) returns it. The
// exported names follow jinja2's rules: a top-level `{% set %}` -- including
// `{% set a, b = ... %}` and a capturing `{% set x %}...{% endset %}` -- or
// `{% macro %}` exports its name,
// unless the name starts with an underscore; a name bound by `{% import %}` or
// `{% from ... import %}` is not re-exported; a name bound inside a block, a
// loop or any other scoped construct is not exported. A template that extends
// another exports what both define, and its body is the parent's rendering.
//
// A Module is safe for concurrent use. Its calls are serialised, because a
// macro shares the module's top-level state -- a `namespace()` it updates, a
// list it appends to -- with every other call, exactly as in jinja2, where
// `module.inc()` twice returns "1" and then "2". The names and the body are
// fixed when Module returns, as a TemplateModule's are. A value returned by
// [Module.Get] is the module's own, though, so a list it holds is the list a
// macro appends to: reading one while another goroutine calls the module is a
// data race. Use [Module.Call] to run a macro.
type Module struct {
	mu  sync.Mutex
	obj *moduleObject
}

// Module renders the template with vars and returns it as a module, which is
// jinja2's
//
//	t.make_module(vars)
//
// The render is bounded by ctx and the environment's limits, as
// [Template.Render] is, and a failure in it -- `{% set q = 1 // 0 %}` -- is
// returned here, as make_module raises it.
//
// vars are converted before Module returns, all of them, and the module keeps
// no reference to the map or to anything in it: a Module outlives the call
// that made it, so reading the caller's data later would race with whatever the
// caller does to it next. (Render converts a name only when the template reads
// it, because its use of the map ends when it returns.)
//
// jinja2's Template.module -- make_module() with no variables, cached on the
// template -- has no counterpart. A *Template is shared between goroutines and
// renders, and a cached module would hold one caller's context and one set of
// mutable top-level values for all of them. Call Module(ctx, nil) once and keep
// the result for the same effect.
func (t *Template) Module(ctx context.Context, vars map[string]any) (mod *Module, err error) {
	defer catchPanic(&err)
	st := t.newState(nil, 0, newBudget(ctx, t.env))
	st.contextVars.raw, st.contextVars.expose, st.contextVars.budget = vars, t.env.methods, st
	// importModule's path: renderState, not the body alone, so a template
	// that extends gets the parent's output and the parent's exports too.
	var body strings.Builder
	if err := t.renderState(st, &body, nil); err != nil {
		return nil, err
	}
	if err := st.contextVars.realise(); err != nil {
		return nil, err
	}
	st.contextVars.raw = nil
	return &Module{obj: &moduleObject{st: st, name: t.name, body: body.String()}}, nil
}

// Name is the name of the template the module was made from, empty for one
// compiled from a string.
func (m *Module) Name() string { return m.obj.name }

// String is the text the template's body rendered: jinja2's str(module). An
// autoescaping template's output is already escaped, as its __html__ says.
func (m *Module) String() string { return m.obj.body }

// Names lists the exported names, sorted. jinja2 keeps them in a set, so the
// order it would give is not one to reproduce.
func (m *Module) Names() []string {
	names := make([]string, 0, len(m.obj.st.exports))
	for name := range m.obj.st.exports {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// Get returns an exported name's value: jinja2's module.name. A macro comes back
// as a value whose Kind is KindObject; call it with [Module.Call]. ok is false
// for a name the module does not export, including one that starts with an
// underscore and one bound by an import.
func (m *Module) Get(name string) (v value.Value, ok bool) {
	return m.obj.GetAttr(name)
}

// Call calls the exported name -- a macro, or anything else callable a
// top-level `{% set %}` bound -- with positional arguments, converted from Go as
// render variables are. It is jinja2's module.name(*args). Use
// [Module.CallArgs] to pass keyword arguments or a caller.
func (m *Module) Call(ctx context.Context, name string, args ...any) (value.Value, error) {
	return m.call(ctx, name, func(st *State) (*value.CallArgs, error) {
		out := &value.CallArgs{Pos: make([]value.Value, 0, len(args))}
		for _, a := range args {
			v, err := value.FromGoBudget(a, st.env.methods, st)
			if err != nil {
				return nil, err
			}
			out.Pos = append(out.Pos, v)
		}
		return out, nil
	})
}

// CallArgs calls the exported macro name with arguments that are already
// template values: jinja2's module.name(*args, **kwargs). The binding is a
// template call's -- defaults, `varargs`, `kwargs`, and `caller` passed as a
// keyword (a [Func] serves) -- and so are its refusals: too many arguments, or
// a keyword the macro neither declares nor collects, is a TypeError.
//
// As with [Template.RenderValues], the values are used as they are, so a macro
// that mutates one mutates the caller's.
func (m *Module) CallArgs(ctx context.Context, name string, args *value.CallArgs) (value.Value, error) {
	return m.call(ctx, name, func(*State) (*value.CallArgs, error) {
		if args == nil {
			return &value.CallArgs{}, nil
		}
		return args, nil
	})
}

// call runs one macro call against the module's own state, which is what the
// macro closes over: its globals, the module's variables, `self`.
//
// Each call has a budget of its own, built from ctx. The one the module was
// made under belongs to a context that may well be over by now, and sharing it
// would make the tenth call fail for the work of the first nine.
//
// Everything else a call moves on the state -- the recursion depth, the
// escaping an {% autoescape %} sets, the executing template -- is put back by
// the code that moved it, on the way out, so it needs nothing here. Two things
// are not: the executing template is restored by an assignment rather than a
// defer, so a panic recovered below would leave a later call reporting the
// wrong template; and a loop filter's failure, recorded for its loop to report,
// stays recorded when the loop's body fails first. A render ends there; a
// module goes on to its next call, which would inherit both.
//
// What comes back is Markup when the macro was defined where autoescaping was
// on. That is jinja2's rule for a call that brings no eval context, as a call
// from Python does not: Macro.__call__ falls back to the setting in force where
// the macro was defined -- which, for a macro the module re-exports from a
// template it imported, is that template's setting rather than this one's.
func (m *Module) call(ctx context.Context, name string, build func(*State) (*value.CallArgs, error)) (v value.Value, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.obj.st
	st.budget, st.loopFailure = newBudget(ctx, st.env), nil
	prevTmpl := st.tmpl
	defer func() { st.tmpl = prevTmpl }()
	defer catchPanic(&err)

	callee, ok := m.obj.GetAttr(name)
	if !ok {
		e := errs.New(errs.AttributeError, "'TemplateModule' object has no attribute %s",
			value.ReprFor(value.String(name), st.PythonVersion()))
		e.Name = m.obj.name
		return value.Undefined, e
	}
	args, err := build(st)
	if err != nil {
		return value.Undefined, err
	}
	v, err = st.invoke(callee, args)
	if err != nil {
		return value.Undefined, err
	}
	// As in renderState: a charge refused somewhere that had nowhere to
	// report it -- a global that dropped Step's error -- still fails the call.
	if st.budget.failed != nil {
		return value.Undefined, st.budget.failed
	}
	if mac, ok := callee.Interface().(*macroObject); ok {
		v = markup(v.AsString(), mac.autoescape)
	}
	return v, nil
}
