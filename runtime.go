// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"fmt"
	"iter"
	"strings"

	"github.com/mgilbir/gojja2/errs"
	"github.com/mgilbir/gojja2/internal/ast"
	"github.com/mgilbir/gojja2/value"
)

// --- loop --------------------------------------------------------------------

// loopSource is a sequence a for loop walks.
//
// Keeping it indexable rather than materialising every iterable means
// `{% for i in range(10000000000) %}` costs nothing until the body runs, and
// that `loop.length` is answerable without consuming anything.
type loopSource interface {
	Len() int
	At(i int) value.Value
}

type sliceSource []value.Value

func (s sliceSource) Len() int             { return len(s) }
func (s sliceSource) At(i int) value.Value { return s[i] }

// objectSource adapts a value.Sequence, which knows its length and can be
// indexed without being copied.
type objectSource struct{ seq value.Sequence }

func (s objectSource) Len() int { return s.seq.Len() }

func (s objectSource) At(i int) value.Value {
	v, ok := s.seq.GetIndex(i)
	if !ok {
		return value.Undefined
	}
	return v
}

// makeLoopSource turns an iterable into something a loop can index. Anything
// that already knows its length is used in place; everything else, including
// the filtered form of `{% for x in y if cond %}`, is materialised.
//
// The materialising path is charged against the budget as it walks: it runs
// to completion before the first iteration of the loop does, so the per-pass
// charge in runLoop would never be reached.
func makeLoopSource(st *State, v value.Value) (loopSource, error) {
	switch v.Kind() {
	case value.KindList, value.KindTuple:
		s, _ := v.Seq()
		return sliceSource(s.Items()), nil
	case value.KindDict:
		d, _ := v.Dict()
		return sliceSource(d.Keys()), nil
	case value.KindObject:
		if seq, ok := v.Interface().(value.Sequence); ok {
			return objectSource{seq}, nil
		}
	}
	seq, err := value.Iterate(v)
	if err != nil {
		return nil, err
	}
	var items []value.Value
	for item := range seq {
		if err := st.Step(1); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return sliceSource(items), nil
}

// loopObject is the `loop` variable.
type loopObject struct {
	src   loopSource
	index int // 0-based position of the current item
	depth int // 1-based recursion depth for a recursive loop
	// recurse renders the loop body again over a new sequence, which is
	// what calling loop(...) inside a recursive loop does.
	recurse func(items value.Value, depth int) (value.Value, error)
	// cycleState tracks loop.cycle across iterations.
	lastChanged  value.Value
	hasLastValue bool
}

func (l *loopObject) GetAttr(name string) (value.Value, bool) {
	n := l.src.Len()
	switch name {
	case "index":
		return value.Int(int64(l.index + 1)), true
	case "index0":
		return value.Int(int64(l.index)), true
	case "revindex":
		return value.Int(int64(n - l.index)), true
	case "revindex0":
		return value.Int(int64(n - l.index - 1)), true
	case "first":
		return value.Bool(l.index == 0), true
	case "last":
		return value.Bool(l.index == n-1), true
	case "length":
		return value.Int(int64(n)), true
	case "depth":
		return value.Int(int64(l.depth)), true
	case "depth0":
		return value.Int(int64(l.depth - 1)), true
	case "previtem":
		if l.index == 0 {
			return value.UndefinedHint("there is no previous item"), true
		}
		return l.src.At(l.index - 1), true
	case "nextitem":
		if l.index+1 >= n {
			return value.UndefinedHint("there is no next item"), true
		}
		return l.src.At(l.index + 1), true
	case "cycle":
		return value.FromObject(&builtinFunc{name: "cycle", fn: stateless(l.cycle)}), true
	case "changed":
		return value.FromObject(&builtinFunc{name: "changed", fn: stateless(l.changed)}), true
	}
	return value.Undefined, false
}

func (l *loopObject) cycle(args *value.CallArgs) (value.Value, error) {
	if len(args.Pos) == 0 {
		return value.Undefined, errs.New(errs.TypeError, "no items for cycling given")
	}
	return args.Pos[l.index%len(args.Pos)], nil
}

// changed reports whether its arguments differ from the previous call's, which
// is how templates group consecutive rows.
func (l *loopObject) changed(args *value.CallArgs) (value.Value, error) {
	current := value.NewTuple(args.Pos...)
	if l.hasLastValue && value.Equal(l.lastChanged, current) {
		return value.False, nil
	}
	l.lastChanged, l.hasLastValue = current, true
	return value.True, nil
}

// Call makes `loop(...)` work inside a recursive loop.
func (l *loopObject) Call(args *value.CallArgs) (value.Value, error) {
	if l.recurse == nil {
		return value.Undefined, errs.New(errs.TypeError,
			"the loop must have the 'recursive' marker to be called")
	}
	if len(args.Pos) != 1 {
		return value.Undefined, errs.New(errs.TypeError,
			"loop() takes exactly one argument, got %d", len(args.Pos))
	}
	return l.recurse(args.Pos[0], l.depth+1)
}

// Iterate makes `dict(loop)` yield nothing.
//
// A LoopContext wraps the iterator the enclosing loop is already consuming, so
// anything that iterates it from inside the body sees it exhausted. That is
// why jinja2's `dict(loop, extra=2)` is just {'extra': 2}.
func (l *loopObject) Iterate() iter.Seq[value.Value] {
	return func(func(value.Value) bool) {}
}

func (l *loopObject) TypeName() string { return "LoopContext" }

func (l *loopObject) QualifiedName() string { return "jinja2.runtime.LoopContext" }

func (l *loopObject) Repr() string {
	return fmt.Sprintf("<LoopContext %d/%d>", l.index+1, l.src.Len())
}

// --- callables ---------------------------------------------------------------

// builtinFunc adapts a Go closure to a template callable.
type builtinFunc struct {
	name string
	fn   func(s *State, args *value.CallArgs) (value.Value, error)
}

func (f *builtinFunc) GetAttr(name string) (value.Value, bool) {
	if name == "name" {
		return value.String(f.name), true
	}
	return value.Undefined, false
}

// Call satisfies value.Caller for a caller that has no render to offer, which
// is what constant folding is. The budget on a nil State is nil, and State.Step
// treats that as "nothing to charge".
func (f *builtinFunc) Call(args *value.CallArgs) (value.Value, error) { return f.fn(nil, args) }

// callWith is the path the evaluator uses, so a global is handed the render it
// is running inside.
func (f *builtinFunc) callWith(s *State, args *value.CallArgs) (value.Value, error) {
	return f.fn(s, args)
}

func (f *builtinFunc) TypeName() string { return "function" }
func (f *builtinFunc) Repr() string     { return "<function " + f.name + ">" }

// stateless adapts a closure that has no use for the render state to the
// signature every template callable now carries.
func stateless(fn func(*value.CallArgs) (value.Value, error)) func(*State, *value.CallArgs) (value.Value, error) {
	return func(_ *State, args *value.CallArgs) (value.Value, error) { return fn(args) }
}

// statefulCaller is a callable that wants the render it is being called from.
//
// value.Caller cannot carry a *State, because the value package cannot import
// this one. Globals need it: without the render's budget a global has no way to
// charge for the work it is about to do, which left the whole global namespace
// exempt from WithMaxIterations and WithMaxOutputBytes by construction rather
// than by oversight.
type statefulCaller interface {
	callWith(s *State, args *value.CallArgs) (value.Value, error)
}

// Func wraps a Go function as a template global.
//
// The *State is the render the call belongs to. A global that walks a
// caller-controlled sequence, or allocates a result whose size a template
// chooses, must charge it with State.Step before doing so -- charging
// afterwards is useless, because by then the memory is already committed. It is
// nil during constant folding, which State.Step handles.
func Func(name string, fn func(s *State, args *value.CallArgs) (value.Value, error)) value.Value {
	return value.FromObject(&builtinFunc{name: name, fn: fn})
}

// --- macros ------------------------------------------------------------------

// macroObject is a `{% macro %}`, closed over the scope it was defined in.
type macroObject struct {
	name string
	node *ast.Macro
	// defaults are the default-argument *expressions*, not their values.
	//
	// jinja2 compiles them into the macro body, so they are evaluated once
	// per call rather than once per definition. That is observable twice
	// over: `{% macro m(v=[]) %}` gets a fresh list every call, where a
	// stored value would carry one call's appends into the next; and a
	// default naming an outer variable sees the value that variable has at
	// the call, not at the definition.
	defaults []ast.Expr
	// defScope is the scope the macro was defined in; its body resolves
	// free names there, not at the call site.
	defScope *scope
	st       *State
	// tmpl is the template the macro was defined in, which decides
	// autoescaping of its output.
	tmpl       *Template
	autoescape bool
	// catchKwargs, catchVarargs and caller record whether the body reads
	// `kwargs`, `varargs` or `caller`. jinja2 decides this when the macro
	// is compiled and refuses the corresponding arguments otherwise, so a
	// macro that ignores extra arguments is an error rather than a no-op.
	catchKwargs  bool
	catchVarargs bool
	caller       bool
	// explicitCaller is set when `caller` is a declared parameter.
	explicitCaller bool
}

func (m *macroObject) GetAttr(name string) (value.Value, bool) {
	switch name {
	case "name":
		if m.name == "" {
			return value.None, true
		}
		return value.String(m.name), true
	case "arguments":
		args := make([]value.Value, len(m.node.Args))
		for i, a := range m.node.Args {
			args[i] = value.String(a.Name)
		}
		return value.NewTuple(args...), true
	case "catch_kwargs":
		return value.Bool(m.catchKwargs), true
	case "catch_varargs":
		return value.Bool(m.catchVarargs), true
	case "caller":
		return value.Bool(m.caller), true
	}
	return value.Undefined, false
}

func (m *macroObject) TypeName() string { return "Macro" }

func (m *macroObject) QualifiedName() string { return "jinja2.runtime.Macro" }

func (m *macroObject) Repr() string {
	if m.name == "" {
		return "<Macro>"
	}
	return "<Macro " + value.Repr(value.String(m.name)) + ">"
}

// --- namespace ---------------------------------------------------------------

// namespaceObject is what `namespace()` returns: a mutable holder that
// survives the scope a loop body would otherwise discard.
type namespaceObject struct {
	d *value.Dict
}

func newNamespace() *namespaceObject {
	v := value.NewDict()
	d, _ := v.Dict()
	return &namespaceObject{d: d}
}

func (n *namespaceObject) GetAttr(name string) (value.Value, bool) {
	return n.d.GetString(name)
}

func (n *namespaceObject) SetAttr(name string, v value.Value) { n.d.SetString(name, v) }

// A Namespace holds attributes, not items: jinja2's is neither a mapping nor
// iterable, so `{% for x in ns %}` and `ns|items` fail there and must here.

func (n *namespaceObject) TypeName() string { return "Namespace" }

func (n *namespaceObject) QualifiedName() string { return "jinja2.utils.Namespace" }

// AttributeError is the message jinja2's Namespace raises for a missing
// attribute: its __getattribute__ raises AttributeError(name), so the message
// is the bare name with no explanation around it.
func (n *namespaceObject) AttributeError(name string) string { return name }

func (n *namespaceObject) Repr() string {
	return "<Namespace " + value.Repr(value.Value(dictValue(n.d))) + ">"
}

// dictValue re-wraps a Dict so it can be rendered.
func dictValue(d *value.Dict) value.Value {
	out := value.NewDict()
	target, _ := out.Dict()
	for _, e := range d.Entries() {
		_ = target.Set(e.Key, e.Value)
	}
	return out
}

// --- template reference and blocks -------------------------------------------

// templateReference is `self`: a handle on the current template's blocks.
type templateReference struct {
	st *State
}

func (r *templateReference) GetAttr(name string) (value.Value, bool) {
	if _, ok := r.st.blocks[name]; !ok {
		return value.Undefined, false
	}
	return value.FromObject(&blockReference{st: r.st, name: name, index: 0}), true
}

func (r *templateReference) TypeName() string { return "TemplateReference" }

func (r *templateReference) QualifiedName() string { return "jinja2.runtime.TemplateReference" }

func (r *templateReference) Repr() string {
	return "<TemplateReference " + value.Repr(value.String(r.st.root.name)) + ">"
}

// blockReference renders one definition of a block. It is what `self.name` and
// `super()` both hand back.
type blockReference struct {
	st    *State
	name  string
	index int
	// sc is the scope the block should render in; nil means the template
	// context, which is the unscoped default.
	sc *scope
}

func (b *blockReference) GetAttr(string) (value.Value, bool) { return value.Undefined, false }

func (b *blockReference) TypeName() string { return "BlockReference" }

func (b *blockReference) QualifiedName() string { return "jinja2.runtime.BlockReference" }

func (b *blockReference) Repr() string {
	return "<BlockReference " + value.Repr(value.String(b.name)) + ">"
}

func (b *blockReference) Call(args *value.CallArgs) (value.Value, error) {
	if len(args.Pos) > 0 || len(args.Kwargs) > 0 {
		return value.Undefined, errs.New(errs.TypeError,
			"block %s takes no arguments", value.Repr(value.String(b.name)))
	}
	return b.render()
}

// render executes one definition of the block and returns its output.
//
// Rendering a block is entering another template function, so it is bounded by
// the same counter as include, extends and a macro call. It has to be: a block
// that prints itself -- `{% block x %}{{ self.x }}{% endblock %}` -- otherwise
// recurses until the goroutine stack is exhausted, and a Go stack overflow is
// a fatal error rather than a panic, so the backstop in catchPanic never sees
// it and the process dies.
func (b *blockReference) render() (value.Value, error) {
	if err := b.st.enter(); err != nil {
		return value.Undefined, err
	}
	defer b.st.leave()

	chain := b.st.blocks[b.name]
	if b.index >= len(chain) {
		return value.Undefined, errs.New(errs.UndefinedError,
			"there is no parent block called %s.", value.Repr(value.String(b.name)))
	}
	entry := chain[b.index]

	sc := b.sc
	if sc == nil {
		// A block body is its own function, resolving against
		// context.vars rather than against the root frame's locals.
		sc = newScope(b.st.contextVars)
	}
	declareFrameLocals(sc, b.st, entry.node.Body)
	var out strings.Builder
	ex := &exec{
		st:     b.st,
		sc:     sc,
		out:    &out,
		stream: &out,
		// The body escapes by the template's own setting, not by an
		// {% autoescape %} the reference sits inside: jinja2 compiles
		// each block against a fresh eval context. Whether the *result*
		// is trusted is decided at the call instead, below, exactly as
		// a macro's is.
		autoescape: b.st.escapeDefault,
		blockName:  b.name,
		blockIndex: b.index,
	}
	prev := b.st.tmpl
	b.st.tmpl = entry.tmpl
	err := ex.execBody(entry.node.Body)
	b.st.tmpl = prev
	if err != nil {
		return value.Undefined, err
	}
	return markup(out.String(), b.st.autoescape), nil
}

// Str makes `{{ self.body }}` render the block without an explicit call, which
// is how jinja2's BlockReference behaves.
//
// Rendering can fail and Str cannot say so, so the error is parked on the
// render state and raised by the statement loop. Discarding it rendered the
// block as "" and reported success -- a ZeroDivisionError inside a block
// reached through `self` simply vanished.
func (b *blockReference) Str() string {
	v, err := b.render()
	if err != nil {
		b.st.deferError(err)
		return ""
	}
	return value.Str(v)
}

// markup marks rendered output safe when autoescaping, so that embedding it
// elsewhere does not escape it a second time.
func markup(s string, autoescape bool) value.Value {
	if autoescape {
		return value.Safe(s)
	}
	return value.String(s)
}
