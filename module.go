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
// Every call starts from the state the module had when [Template.Module]
// returned: a macro that updates a `namespace()` or appends to a module-level
// list changes it for the length of that call only, so `module.inc()` twice
// returns "1" both times, and one caller's call cannot change what the next
// caller's sees. [WithSharedState]([ShareModuleState]) gives back jinja2's
// TemplateModule, whose state carries from call to call, so the same two calls
// return "1" and then "2".
//
// A Module is safe for concurrent use. Its calls are serialised, since each
// works on the module's state while it runs. The names and the body are fixed
// when Module returns, as a TemplateModule's are. [Module.Get] returns a copy of
// what it reads; under ShareModuleState it returns the module's own value
// instead, which a later call can change, so reading one while another
// goroutine calls the module is then a data race.
type Module struct {
	mu  sync.Mutex
	obj *moduleObject
	// pristine is the module's state as Template.Module left it, kept so
	// each call can start from it; nil when the environment lets the state
	// carry over. See ShareModuleState.
	pristine *moduleSnapshot
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
	// The exports go back to the host and are read after this render is
	// over, so the context is converted in full now, while this budget and
	// ctx are watching -- not lazily, the way a render's own values are.
	// See eagerConversion.
	st.contextVars.raw, st.contextVars.expose, st.contextVars.budget = vars, t.env.methods, eagerConversion{st}
	st.eager = true
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
	mod = &Module{obj: &moduleObject{st: st, name: t.name, body: body.String()}}
	if t.env.shared&ShareModuleState == 0 {
		if mod.pristine, err = snapshotModule(st); err != nil {
			return nil, err
		}
	}
	return mod, nil
}

// moduleSnapshot is a module's state, copied: every value a call could change
// in place, in every scope a call can reach, as Template.Module left it.
type moduleSnapshot struct {
	scopes []*scope
	// vals holds, per scope, the copy of each binding a template could
	// change. Bindings nothing can change are left out: a call cannot rebind
	// a module-level name, only change what one holds.
	vals []map[string]value.Value
}

// snapshotModule copies the state a module's calls can reach.
//
// A macro resolves free names in the scope it was defined in, so the scopes
// are the module's own chain -- its top level, its variables, the globals it
// has reached -- and the chain of every macro found in them, at any depth
// inside a container or a namespace. One memo serves the whole copy, so two
// names for one list stay two names for one list.
func snapshotModule(st *State) (*moduleSnapshot, error) {
	snap := &moduleSnapshot{}
	seen := map[*scope]bool{}
	var queue []*scope
	addChain := func(sc *scope) {
		for ; sc != nil; sc = sc.parent {
			if !seen[sc] {
				seen[sc] = true
				queue = append(queue, sc)
			}
		}
	}
	addChain(st.ctx)
	memo := &value.Memo{}
	walked := map[any]bool{}
	for len(queue) > 0 {
		sc := queue[0]
		queue = queue[1:]
		vals := map[string]value.Value{}
		var err error
		sc.each(func(name string, v value.Value) {
			findMacros(v, walked, func(m *macroObject) { addChain(m.defScope) })
			if err != nil || !value.MayBeMutable(v) {
				return
			}
			vals[name], err = value.IsolateIn(v, st, memo)
		})
		if err != nil {
			return nil, err
		}
		snap.scopes = append(snap.scopes, sc)
		snap.vals = append(snap.vals, vals)
	}
	return snap, nil
}

// findMacros calls found for every macro inside v, however deeply a list, a
// dict or a namespace holds it.
func findMacros(v value.Value, walked map[any]bool, found func(*macroObject)) {
	var children []value.Value
	switch v.Kind() {
	case value.KindList, value.KindTuple:
		s, _ := v.Seq()
		if walked[s] {
			return
		}
		walked[s] = true
		children = s.Items()
	case value.KindDict:
		d, _ := v.Dict()
		if walked[d] {
			return
		}
		walked[d] = true
		for _, e := range d.Entries() {
			children = append(children, e.Value)
		}
	case value.KindObject:
		switch o := v.Interface().(type) {
		case *macroObject:
			found(o)
		case *namespaceObject:
			if walked[o] {
				return
			}
			walked[o] = true
			for _, e := range o.d.Entries() {
				children = append(children, e.Value)
			}
		}
	}
	for _, c := range children {
		findMacros(c, walked, found)
	}
}

// restore puts a fresh copy of the snapshot back in place, so the next call
// starts from the state Template.Module left. Nothing outside a call reads the
// live state -- Get hands out copies -- so restoring after each call is enough.
//
// It is not charged to any budget. Its size is the snapshot's, which was
// charged when the module was made, and a refusal half way through would leave
// a state that is neither the old one nor the new.
func (p *moduleSnapshot) restore() {
	memo := &value.Memo{}
	for i, sc := range p.scopes {
		for name, v := range p.vals[i] {
			c, err := value.IsolateIn(v, nil, memo)
			if err != nil {
				// With no budget nothing can refuse: the walk is the
				// copy of a value that was copied once already.
				panic("gojja2: restoring a module's state: " + err.Error())
			}
			sc.set(name, c)
		}
	}
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
//
// What comes back is a copy, made for this Get, of anything in it a call could
// change: the module's state is its own, and no later call changes the value
// returned here. Under [WithSharedState]([ShareModuleState]) it is the
// module's own value instead, as in jinja2, and a later call can change it.
func (m *Module) Get(name string) (v value.Value, ok bool) {
	if m.pristine == nil {
		return m.obj.GetAttr(name)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if v, ok = m.obj.GetAttr(name); !ok {
		return v, ok
	}
	c, err := value.IsolateIn(v, nil, nil)
	if err != nil {
		// With no budget nothing can refuse; see moduleSnapshot.restore.
		panic("gojja2: copying a module's export: " + err.Error())
	}
	return c, true
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
// As with [Template.RenderValues], what the macro could change is copied first,
// so it cannot write to the caller's values -- unless
// [WithSharedState]([ShareRenderValues]) says to use them as they are.
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
	// A budget of the call's own. Under ShareModuleState the call keeps the
	// copies of the globals and host values the module has reached: its
	// state outlives one call, and its view of a global it has changed is
	// part of that state. Otherwise the call starts from the snapshot and
	// makes its own copies. See State.isolate.
	b := newBudget(ctx, st.env)
	if m.pristine != nil {
		// Every call starts from the module as Template.Module left it,
		// and leaves it that way, whatever the call did or however it
		// ended. Its copies of globals are its own, too.
		defer m.pristine.restore()
	} else if st.budget != nil {
		b.isolated = st.budget.isolated
	}
	st.budget, st.loopFailure = b, nil
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
	if !st.env.sharesValues() {
		if args, err = isolateArgs(st, args); err != nil {
			return value.Undefined, err
		}
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

// isolateArgs copies what a call's arguments hold that the macro could change,
// so a call cannot write to the caller's values; see ShareRenderValues.
func isolateArgs(st *State, args *value.CallArgs) (*value.CallArgs, error) {
	out := &value.CallArgs{
		Pos:    make([]value.Value, len(args.Pos)),
		Kwargs: make([]value.Kwarg, len(args.Kwargs)),
	}
	for i, a := range args.Pos {
		c, err := st.isolate(a)
		if err != nil {
			return nil, err
		}
		out.Pos[i] = c
	}
	for i, kw := range args.Kwargs {
		c, err := st.isolate(kw.Value)
		if err != nil {
			return nil, err
		}
		out.Kwargs[i] = value.Kwarg{Name: kw.Name, Value: c}
	}
	return out, nil
}
