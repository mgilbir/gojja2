// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"fmt"
	"iter"
	"reflect"
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

// sizeGuard reports a function that fails once v has changed size, or nil for
// a container that may be resized while it is walked.
//
// Python raises RuntimeError when a dict changes size during iteration, and it
// raises it on every step including the one that would have ended the loop --
// so a single-key dict mutated in the body raises too. Lists are deliberately
// not guarded: CPython does not guard them either, and `{% for i in l %}` with
// an append in the body is an infinite loop there. Here the iteration budget
// stops it, which is the documented bound rather than this error.
func sizeGuard(v value.Value) func() error {
	size, what := func() int { return 0 }, ""
	switch o := v.Interface().(type) {
	case *dictView:
		size, what = o.Len, "dictionary"
	default:
		if d, ok := v.Dict(); ok {
			size, what = d.Len, "dictionary"
		}
	}
	if what == "" {
		return nil
	}
	start := size()
	return func() error {
		if size() != start {
			return errs.New(errs.RuntimeError, "%s changed size during iteration", what)
		}
		return nil
	}
}

// guardedSource is a snapshot of a container that must not be resized while the
// loop walks it. See sizeGuard.
type guardedSource struct {
	items []value.Value
	guard func() error
	bad   error
}

func (s *guardedSource) has(i int) bool {
	if s.bad != nil {
		return false
	}
	if s.bad = s.guard(); s.bad != nil {
		return false
	}
	return i >= 0 && i < len(s.items)
}

func (s *guardedSource) at(i int) value.Value { return s.items[i] }
func (s *guardedSource) length() int          { return len(s.items) }
func (s *guardedSource) err() error           { return s.bad }

// liveValues walks v the way a `{% for %}` must, which for a list means by
// index against whatever it holds now rather than over a snapshot of it.
//
// value.Iterate takes the slice as it is when the walk starts, which is right
// for a filter consuming a sequence in one go and wrong for a loop: the body,
// or the loop's own test, runs between two steps and can shorten the list. See
// liveSeqSource, which is the same rule for a loop without a test.
func liveValues(v value.Value) (iter.Seq[value.Value], error) {
	if v.Kind() != value.KindList {
		return value.Iterate(v)
	}
	seq, _ := v.Seq()
	return func(yield func(value.Value) bool) {
		for i := 0; i < seq.Len(); i++ {
			if !yield(seq.At(i)) {
				return
			}
		}
	}, nil
}

// liveSeqSource walks a list as Python's list iterator does: by index, against
// whatever the list holds now. See makeLoopSource.
type liveSeqSource struct{ seq *value.Seq }

func (s liveSeqSource) has(i int) bool       { return i >= 0 && i < s.seq.Len() }
func (s liveSeqSource) at(i int) value.Value { return s.seq.At(i) }
func (s liveSeqSource) length() int          { return s.seq.Len() }
func (s liveSeqSource) err() error           { return nil }

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
	// report tells the render that a pull failed, so that a *body* which
	// consumed the loop object -- `{{ loop|length }}`, `dict(loop)`,
	// `{% for a, b in loop %}` -- cannot leave the failure sitting here for
	// a `{% break %}` to discard. See runLoop.
	report func(error)
}

func (s *filteredSource) pull() bool {
	if s.done || s.fail != nil {
		return false
	}
	v, ok, err := s.next()
	switch {
	case err != nil:
		s.fail, s.done = err, true
		if s.report != nil {
			s.report(err)
		}
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
	case value.KindList:
		// Live, not a snapshot: Python's list iterator holds an index and
		// asks the list its length each time, so a body that shortens the
		// list ends the loop early --
		// `{% for i in lst %}{{ i }}{% set _ = lst.pop() %}{% endfor %}`
		// on [1,2,3,4] prints "12" there and printed "1234" here. One that
		// lengthens it runs forever in CPython and runs into the iteration
		// budget here, which is the bound docs/limits.md records.
		seq, _ := v.Seq()
		return liveSeqSource{seq}, nil
	case value.KindTuple:
		// A tuple cannot be mutated, so a snapshot and the live sequence
		// are the same thing.
		s, _ := v.Seq()
		return sliceSource(s.Items()), nil
	case value.KindDict:
		d, _ := v.Dict()
		return &guardedSource{items: d.Keys(), guard: sizeGuard(v)}, nil
	case value.KindObject:
		if _, ok := v.Interface().(*dictView); ok {
			break // a view is guarded too; fall through to Iterate
		}
		if seq, ok := v.Interface().(value.Sequence); ok {
			return objectSource{seq}, nil
		}
	}
	if g := sizeGuard(v); g != nil {
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
		return &guardedSource{items: items, guard: g}, nil
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
	// undefined is the environment's Undefined class, which previtem and
	// nextitem have to build their answer under: those two are the only
	// undefineds a loop hands out, and without this they were always the
	// default one. Under DebugUndefined `{{ loop.previtem }}` prints the
	// hint, and under StrictUndefined it raises; both rendered as nothing.
	undefined value.UndefinedBehavior
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
		// jinja2's `last` is `_peek_next() is missing`: it pulls one more
		// item and no further. Asking for the *length* instead ran a
		// filtered loop's test over the whole of the rest of the input,
		// so `{% for i in xs if d.popitem() %}{{ loop.last }}` emptied
		// the dict where CPython pops exactly once more -- visible
		// whenever the loop does not run to the end, which is what a
		// `{% break %}` in the body arranges.
		return value.Bool(!l.src.has(l.index + 1)), true
	case "length":
		return value.Int(int64(n())), true
	case "depth":
		return value.Int(int64(l.depth)), true
	case "depth0":
		return value.Int(int64(l.depth - 1)), true
	case "previtem":
		if l.index == 0 {
			return value.UndefinedHint("there is no previous item").
				WithBehavior(l.undefined), true
		}
		return l.src.at(l.index - 1), true
	case "nextitem":
		if !l.src.has(l.index + 1) {
			return value.UndefinedHint("there is no next item").
				WithBehavior(l.undefined), true
		}
		return l.src.at(l.index + 1), true
	case "cycle":
		return Method("cycle", "LoopContext", "jinja2.runtime.LoopContext",
			value.FromObject(l), stateless(l.cycle)), true
	case "changed":
		return Method("changed", "LoopContext", "jinja2.runtime.LoopContext",
			value.FromObject(l), l.changed), true
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
//
// jinja2 writes it as `self._last_checked_value != value`, a real `!=` on two
// tuples -- so a StrictUndefined among the arguments raises instead of
// answering, and it raises from the *second* call rather than the first: the
// first has only the `missing` sentinel to compare against, which is not a
// tuple, so Python falls back to identity and never looks at the elements. This
// compared without consulting the refusal, so `{% for a in [1, 2] %}{{
// loop.changed(nope) }}` answered "TrueTrue" where jinja2 refuses the second
// iteration. Found by a soak seed, which noticed only that the two engines
// failed in different places.
func (l *loopObject) changed(s *State, args *value.CallArgs) (value.Value, error) {
	if err := bindArgs(runtimeSignatures["LoopContext.changed"], args, 1); err != nil {
		return value.Undefined, err
	}
	current := value.NewTuple(args.Pos...)
	if l.hasLastValue {
		same, err := value.EqualErr(l.lastChanged, current, s.PythonVersion())
		if err != nil {
			return value.Undefined, err
		}
		if same {
			return value.False, nil
		}
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
	// recv is the receiver's type when this callable is a *bound method*
	// rather than a free function or a class, and pyClass is that type's
	// qualified name when the class is written in Python rather than C.
	//
	// Python spells the three differently and a template can see all of it:
	// `d.get` is a builtin_function_or_method whose repr is
	// `<built-in method get of dict object at 0x...>`, `cycler('a').next` is
	// a method whose repr is `<bound method Cycler.next of <...>>`, and
	// lipsum is a plain function. Every one of them answered "function"
	// here, which is also what `{{ d.get.__class__() }}` refused as.
	recv    string
	pyClass string
	// self is the receiver, for the repr's address and for the equality two
	// bound methods have.
	self value.Value
	// pyName is what __name__ reports when it is not the name the attribute
	// was reached by: lipsum is jinja2.utils.generate_lorem_ipsum.
	pyName   string
	pyModule string
	fn       func(s *State, args *value.CallArgs) (value.Value, error)
}

// Method is [Func] for a callable reached as an attribute of a value.
//
// recv is the receiver's type name as an error message spells it, and pyClass
// is the class's qualified name when it is written in Python -- which is what
// tells a `method` from a `builtin_function_or_method`. self is the receiver,
// which the repr takes an address from and which two bound methods compare by.
func Method(name, recv, pyClass string, self value.Value,
	fn func(s *State, args *value.CallArgs) (value.Value, error)) value.Value {
	return value.FromObject(&builtinFunc{
		name: name, recv: recv, pyClass: pyClass, self: self, fn: fn,
	})
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
	if f.class != "" {
		// A type object answers the attributes classObject answers,
		// because that is what it is.
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
	// A function and a bound method carry the same three, and each spells
	// them for what it is: `dict.get` is the qualified name of `d.get`,
	// `Cycler.next` of a method, and a C method's module is None rather
	// than a name.
	switch name {
	case "__name__":
		return value.String(f.pythonName()), true
	case "__qualname__":
		switch {
		case f.pyClass != "":
			return value.String(f.className2() + "." + f.name), true
		case f.recv != "":
			return value.String(f.recv + "." + f.name), true
		}
		return value.String(f.pythonName()), true
	case "__module__":
		switch {
		case f.pyClass != "":
			if i := strings.LastIndexByte(f.pyClass, '.'); i >= 0 {
				return value.String(f.pyClass[:i]), true
			}
			return value.None, true
		case f.recv != "":
			return value.None, true
		}
		if f.pyModule != "" {
			return value.String(f.pyModule), true
		}
		return value.None, true
	}
	return value.Undefined, false
}

// No Call here, so *builtinFunc is not a value.Caller. There used to be one,
// "for a caller that has no render to offer, which is what constant folding is",
// and it was dead twice over: the evaluator goes through callWith so a global is
// handed the render it runs inside, the folder does not fold a call to a global
// at all -- folding `{{ lipsum() }}` would bake one random paragraph into the
// template -- and `is callable` answers through statefulCaller, which callWith
// satisfies, one check before value.Caller. Replacing the body with a panic left
// the whole suite green and so did deleting the method; tests/callable_globals
// grades the half that matters.

// callWith is the path the evaluator uses, so a global is handed the render it
// is running inside.
func (f *builtinFunc) callWith(s *State, args *value.CallArgs) (value.Value, error) {
	return f.fn(s, args)
}

// TypeName is what an error message calls this value. type(range) is type, not
// function, and a bound method is not a function either.
func (f *builtinFunc) TypeName() string {
	switch {
	case f.class != "":
		return "type"
	case f.pyClass != "":
		return "method"
	case f.recv != "":
		return "builtin_function_or_method"
	}
	return "function"
}

// QualifiedName is what __class__ reports, and the type of a type is type.
func (f *builtinFunc) QualifiedName() string { return f.TypeName() }

func (f *builtinFunc) Repr() string {
	switch {
	case f.class != "":
		return "<class '" + f.class + "'>"
	case f.pyClass != "":
		// `<bound method Cycler.next of <jinja2.utils.Cycler object at
		// 0x...>>`: the class's *bare* name in the method, its
		// qualified one in the receiver's own repr.
		return "<bound method " + f.className2() + "." + f.name + " of " +
			value.ReprFor(f.self, value.DefaultPythonVersion) + ">"
	case f.recv != "":
		return fmt.Sprintf("<built-in method %s of %s object at 0x%x>",
			f.name, f.recv, reflect.ValueOf(f).Pointer())
	}
	return fmt.Sprintf("<function %s at 0x%x>", f.pythonName(), reflect.ValueOf(f).Pointer())
}

// className2 is the bare name of the Python class a bound method belongs to.
func (f *builtinFunc) className2() string {
	if i := strings.LastIndexByte(f.pyClass, '.'); i >= 0 {
		return f.pyClass[i+1:]
	}
	return f.pyClass
}

// pythonName is what __name__ reports: the name the attribute was reached by,
// unless the underlying function has one of its own.
func (f *builtinFunc) pythonName() string {
	if f.pyName != "" {
		return f.pyName
	}
	return f.name
}

// Equals is the equality two bound methods have: CPython compares the receiver
// and the function slot, so `d.get == d.get` is True although the two objects
// are built one at a time. A free function has no such rule and compares by
// identity.
//
// The receiver is compared by identity where it has one and by value where it
// does not, which is a str's or an int's case. CPython compares the object
// there too, and two equal strings in one template are usually the same
// interned object -- `{% set a = 'ab' %}{% set b = 'a' + 'b' %}{{ a.upper ==
// b.upper }}` is where the two answers part, and it is the only one.
func (f *builtinFunc) Equals(other value.Value) (bool, bool) {
	o, ok := other.Interface().(*builtinFunc)
	if !ok {
		return false, true
	}
	if f.recv == "" && f.pyClass == "" {
		return f == o, true
	}
	if f.name != o.name || f.recv != o.recv || f.pyClass != o.pyClass {
		return false, true
	}
	if value.SameObject(f.self, o.self) {
		return true, true
	}
	// A receiver that has an identity settles it: two dicts that are not the
	// same dict give two bound methods that are not equal, however equal the
	// dicts look. Only a scalar falls through, and there CPython is
	// comparing an object gojja2 does not have: two literal 300s are two
	// objects there. Equality of value is what a variable receiver answers
	// on both; see "`is sameas` on two literals" in docs/divergences.md.
	if value.HasIdentity(f.self) || value.HasIdentity(o.self) {
		return false, true
	}
	return value.EqualBool(f.self, o.self), true
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

// pyFunc is [Func] for a global whose underlying Python function has a name of
// its own: what the attribute is reached by and what __name__ answers are two
// different things for lipsum.
func pyFunc(name, pyName, pyModule string,
	fn func(s *State, args *value.CallArgs) (value.Value, error)) value.Value {
	return value.FromObject(&builtinFunc{
		name: name, pyName: pyName, pyModule: pyModule, fn: fn,
	})
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
	blockScope *scope
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
	// py is the interpreter being reproduced. Repr comes from the Reprer
	// interface, which takes no arguments, so the version is carried here
	// -- as dictView carries it for the same reason. repr escapes by
	// isprintable, and which characters are printable is the
	// interpreter's answer.
	py value.PythonVersion
}

func newNamespace(py value.PythonVersion) *namespaceObject {
	v := value.NewDict()
	d, _ := v.Dict()
	return &namespaceObject{d: d, py: py}
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
	return "<Namespace " + value.ReprFor(value.Value(dictValue(n.d)), n.py) + ">"
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
	// chunks is the piece count of the stream a {% block %} tag renders into;
	// nil for a reference that joins the pieces into a value first.
	chunks *int
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
		blockScope: b.sc,
	}
	// A block body is a buffer of its own, and counts its pieces when the
	// template it was written in has a filter block to number them for.
	if b.chunks != nil {
		ex.chunks = b.chunks
	} else if entry.tmpl.countsChunks {
		ex.chunks = new(int)
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
