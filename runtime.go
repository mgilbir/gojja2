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
//
// has() and length() are separate questions because a filtered loop answers
// them at different moments: whether there is an item at i can be settled by
// pulling one more, while the total cannot -- and the filter is only run as
// far as the template makes it run, because jinja2's is a generator the loop
// consumes an item at a time.
type loopSource interface {
	// has reports whether there is an item at i, pulling one more when it
	// has to.
	has(i int) bool
	at(i int) value.Value
	// length is the total, which for a filtered source means running the
	// filter over the rest of the input.
	length() int
	// err reports a failure met while pulling. The loop checks it before
	// each pass, so a filter that raises stops the loop where it raised.
	err() error
}

type sliceSource []value.Value

func (s sliceSource) has(i int) bool       { return i >= 0 && i < len(s) }
func (s sliceSource) at(i int) value.Value { return s[i] }
func (s sliceSource) length() int          { return len(s) }
func (s sliceSource) err() error           { return nil }

// filteredSource applies a loop's `if` as the loop walks it.
//
// Filtering up front is the same answer whenever the test is pure, and a
// different one when it is not: a test that reads what the body writes --
// `{% for i in xs if ns.found == 0 %}` over a body that sets ns.found -- sees
// the writes in jinja2 and saw none here, because every test had already run.
type filteredSource struct {
	next  func() (value.Value, bool, error)
	items []value.Value
	done  bool
	fail  error
}

func (s *filteredSource) pull() bool {
	if s.done || s.fail != nil {
		return false
	}
	v, ok, err := s.next()
	switch {
	case err != nil:
		s.fail, s.done = err, true
		return false
	case !ok:
		s.done = true
		return false
	}
	s.items = append(s.items, v)
	return true
}

func (s *filteredSource) has(i int) bool {
	for len(s.items) <= i && s.pull() {
	}
	return i >= 0 && i < len(s.items)
}

func (s *filteredSource) at(i int) value.Value { return s.items[i] }

func (s *filteredSource) length() int {
	for s.pull() {
	}
	return len(s.items)
}

func (s *filteredSource) err() error { return s.fail }

// objectSource adapts a value.Sequence, which knows its length and can be
// indexed without being copied.
type objectSource struct{ seq value.Sequence }

func (s objectSource) length() int    { return s.seq.Len() }
func (s objectSource) has(i int) bool { return i >= 0 && i < s.seq.Len() }
func (s objectSource) err() error     { return nil }

func (s objectSource) at(i int) value.Value {
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
	// The total is asked for only where it is needed: `loop.index` does
	// not run a filtered loop's test over the rest of the input, and
	// `loop.length` does -- which is the difference between jinja2 asking
	// its LoopContext for an index and asking it for a length.
	n := func() int { return l.src.length() }
	switch name {
	case "index":
		return value.Int(int64(l.index + 1)), true
	case "index0":
		return value.Int(int64(l.index)), true
	case "revindex":
		return value.Int(int64(n() - l.index)), true
	case "revindex0":
		return value.Int(int64(n() - l.index - 1)), true
	case "first":
		return value.Bool(l.index == 0), true
	case "last":
		return value.Bool(l.index == n()-1), true
	case "length":
		return value.Int(int64(n())), true
	case "depth":
		return value.Int(int64(l.depth)), true
	case "depth0":
		return value.Int(int64(l.depth - 1)), true
	case "previtem":
		if l.index == 0 {
			return value.UndefinedHint("there is no previous item"), true
		}
		return l.src.at(l.index - 1), true
	case "nextitem":
		if !l.src.has(l.index + 1) {
			return value.UndefinedHint("there is no next item"), true
		}
		return l.src.at(l.index + 1), true
	case "cycle":
		return value.FromObject(&builtinFunc{name: "cycle", fn: stateless(l.cycle)}), true
	case "changed":
		return value.FromObject(&builtinFunc{name: "changed", fn: stateless(l.changed)}), true
	}
	return value.Undefined, false
}

func (l *loopObject) cycle(args *value.CallArgs) (value.Value, error) {
	// cycle takes *args and no keywords, and Python refuses one before the
	// body's empty-cycle check runs.
	if err := bindArgs(runtimeSignatures["LoopContext.cycle"], args, 1); err != nil {
		return value.Undefined, err
	}
	if len(args.Pos) == 0 {
		return value.Undefined, errs.New(errs.TypeError, "no items for cycling given")
	}
	return args.Pos[l.index%len(args.Pos)], nil
}

// changed reports whether its arguments differ from the previous call's, which
// is how templates group consecutive rows.
func (l *loopObject) changed(args *value.CallArgs) (value.Value, error) {
	if err := bindArgs(runtimeSignatures["LoopContext.changed"], args, 1); err != nil {
		return value.Undefined, err
	}
	current := value.NewTuple(args.Pos...)
	if l.hasLastValue && value.Equal(l.lastChanged, current) {
		return value.False, nil
	}
	l.lastChanged, l.hasLastValue = current, true
	return value.True, nil
}

// Call makes `loop(...)` work inside a recursive loop.
//
// LoopContext.__call__ takes one iterable, so Python binds the call before the
// body can complain about the missing marker: `{{ loop() }}` in a plain loop is
// a missing argument, not the marker. The parameter is named, so
// `loop(iterable=x)` binds too.
func (l *loopObject) Call(args *value.CallArgs) (value.Value, error) {
	if err := bindArgs(runtimeSignatures["LoopContext.__call__"], args, 1); err != nil {
		return value.Undefined, err
	}
	if l.recurse == nil {
		return value.Undefined, errs.New(errs.TypeError,
			"The loop must have the 'recursive' marker to be called recursively.")
	}
	// Bound, so there is exactly one argument -- positionally or by name.
	iterable, _ := arg(args, 0, "iterable")
	return l.recurse(iterable, l.depth+1)
}

// Len is the number of items the loop walks, which is `loop.length`.
//
// It is a length without indexing: jinja2's LoopContext defines __len__ and no
// __getitem__, so `{{ loop|length }}` answers and `loop is sequence` does not.
func (l *loopObject) Len() int { return l.src.length() }

// Iterate consumes the loop the body is running inside.
//
// A LoopContext *is* the iterator the enclosing loop is walking, so iterating
// it from within the body advances that loop: `{{ loop|list }}` yields the
// items that have not been reached yet and the enclosing loop then ends,
// having none left. Each item arrives as the (value, loop) pair jinja2's
// LoopContextIterator returns, and the loop in it is this same object, so its
// repr shows where the walk stopped rather than where the pair was made.
//
// Yielding nothing instead -- on the reasoning that the iterator is already
// exhausted -- was wrong in a way a template can print:
// `{{ dict(loop, extra=2) }}` is {2: <LoopContext 3/3>, 'extra': 2} in CPython
// and was {'extra': 2} here.
func (l *loopObject) Iterate() iter.Seq[value.Value] {
	return func(yield func(value.Value) bool) {
		for l.src.has(l.index + 1) {
			l.index++
			if !yield(value.NewTuple(l.src.at(l.index), value.FromObject(l))) {
				return
			}
		}
	}
}

func (l *loopObject) TypeName() string { return "LoopContext" }

func (l *loopObject) QualifiedName() string { return "jinja2.runtime.LoopContext" }

func (l *loopObject) Repr() string {
	return fmt.Sprintf("<LoopContext %d/%d>", l.index+1, l.src.length())
}

// --- callables ---------------------------------------------------------------

// builtinFunc adapts a Go closure to a template callable.
type builtinFunc struct {
	name string
	// class is the qualified class name when this callable is a *type*
	// rather than a function, and empty when it is a function.
	//
	// Most of the globals are classes in jinja2 -- range and dict are
	// builtin types, and cycler, joiner and namespace are classes in
	// jinja2.utils. Only lipsum is a function. Calling one constructs a
	// value either way, so the difference is invisible until something
	// names the type, and then it is everywhere: every arithmetic,
	// iteration and length error says 'type' where this said 'function',
	// and `{{ range }}` is `<class 'range'>`.
	class string
	fn    func(s *State, args *value.CallArgs) (value.Value, error)
}

// className is the bare name, which is what __name__ reports: "range" for
// builtins, "Cycler" for jinja2.utils.Cycler.
func (f *builtinFunc) className() string {
	if i := strings.LastIndexByte(f.class, '.'); i >= 0 {
		return f.class[i+1:]
	}
	return f.class
}

func (f *builtinFunc) GetAttr(name string) (value.Value, bool) {
	if name == "name" {
		return value.String(f.name), true
	}
	if f.class == "" {
		return value.Undefined, false
	}
	// A type object answers the attributes classObject answers, because
	// that is what it is.
	switch name {
	case "__name__", "__qualname__":
		return value.String(f.className()), true
	case "__module__":
		if i := strings.LastIndexByte(f.class, '.'); i >= 0 {
			return value.String(f.class[:i]), true
		}
		return value.String("builtins"), true
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

// TypeName is what an error message calls this value. type(range) is type,
// not function.
func (f *builtinFunc) TypeName() string {
	if f.class != "" {
		return "type"
	}
	return "function"
}

// QualifiedName is what __class__ reports, and the type of a type is type.
func (f *builtinFunc) QualifiedName() string {
	if f.class != "" {
		return "type"
	}
	return "function"
}

func (f *builtinFunc) Repr() string {
	if f.class != "" {
		return "<class '" + f.class + "'>"
	}
	return "<function " + f.name + ">"
}

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

// Class is [Func] for a global that is a class in jinja2 rather than a
// function. qualified is the name repr shows, e.g. "range" or
// "jinja2.utils.Cycler".
func Class(name, qualified string, fn func(s *State, args *value.CallArgs) (value.Value, error)) value.Value {
	return value.FromObject(&builtinFunc{name: name, class: qualified, fn: fn})
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
	tmpl           *Template
	autoescape     bool
	volatileEscape bool
	// blockName and blockIndex are the block the macro was *written* in,
	// which is where its body's super() resolves. jinja2 compiles super
	// into a block function's frame, so a macro defined there closes over
	// it: the macro carries that binding to wherever it is called, and a
	// macro written outside any block has none however deep in one it is
	// called. Lexical, like defScope and volatileEscape.
	blockName  string
	blockIndex int
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

// Repr is jinja2's `<{type} {name}>`, where an unnamed macro -- the one a
// `{% call %}` block builds -- is spelled "anonymous" rather than left out.
func (m *macroObject) Repr() string {
	if m.name == "" {
		return "<Macro anonymous>"
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
		target.SetKnown(e.Key, e.Value)
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

// Repr is `<TemplateReference {context.name!r}>`, and a template compiled from
// a string has no name -- which Python prints as None, not as ”.
func (r *templateReference) Repr() string {
	return "<TemplateReference " + templateNameRepr(r.st.root.name) + ">"
}

// templateNameRepr is `{name!r}` for a template name, where a template with
// none carries None rather than the empty string.
func templateNameRepr(name string) string {
	if name == "" {
		return "None"
	}
	return value.Repr(value.String(name))
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

// GetAttr answers `super`, which is the next definition of the same block --
// what `{{ self.body.super() }}` reaches. It is a property, so it is the
// reference itself and not a method returning one, and past the end of the
// chain it is an undefined that says so when it is used.
func (b *blockReference) GetAttr(name string) (value.Value, bool) {
	if name != "super" {
		return value.Undefined, false
	}
	if b.index+1 >= len(b.st.blocks[b.name]) {
		return b.st.Undefined(value.UndefinedHint(
			"there is no parent block called %s.",
			value.Repr(value.String(b.name)))), true
	}
	return value.FromObject(&blockReference{
		st: b.st, name: b.name, index: b.index + 1, sc: b.sc,
	}), true
}

func (b *blockReference) TypeName() string { return "BlockReference" }

func (b *blockReference) QualifiedName() string { return "jinja2.runtime.BlockReference" }

// A BlockReference defines no __str__ and no __repr__. gojja2 gave it a Str
// that rendered the block, so `{{ self.body }}` printed the block's output
// where jinja2 prints the object -- and, because printing it re-entered the
// block, `{% block x %}{{ self.x }}{% endblock %}` was a RecursionError where
// jinja2 prints one line. Only a call renders.
func (b *blockReference) Repr() string { return pyObjectRepr(b.QualifiedName(), b) }

func (b *blockReference) Call(args *value.CallArgs) (value.Value, error) {
	// BlockReference.__call__ takes nothing but self, and says so the way
	// any Python method does -- naming itself and counting self, not naming
	// the block.
	if err := bindArgs(runtimeSignatures["BlockReference.__call__"], args, 1); err != nil {
		return value.Undefined, err
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
	declareRootLocals(sc, b.st, entry.node, entry.node.Body)
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

// markup marks rendered output safe when autoescaping, so that embedding it
// elsewhere does not escape it a second time.
func markup(s string, autoescape bool) value.Value {
	if autoescape {
		return value.Safe(s)
	}
	return value.String(s)
}
