// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"fmt"
	"reflect"
	"slices"

	"github.com/mgilbir/gojja2/errs"
	"github.com/mgilbir/gojja2/value"
)

// FilterFunc adapts an ordinary Go function to a [Filter], binding a template's
// arguments to its parameters the way Python binds a filter function's:
//
//	func truncateWords(s string, count int, suffix string) string { ... }
//
//	env.AddFilter("truncate_words",
//		gojja2.FilterFunc("truncate_words", truncateWords, "count", "suffix"))
//
// The filtered value is fn's first parameter, after an optional leading
// *[State]. params names the rest, so a template may pass them by keyword --
// `{{ s|truncate_words(3, suffix="...") }}` -- and so an error can name the one
// that is missing. name is what those errors call the function.
//
// A call of the wrong shape is refused before fn runs, with CPython's own
// messages: "truncate_words() takes 3 positional arguments but 4 were given",
// "missing 1 required positional argument: 'suffix'", "got an unexpected
// keyword argument 'width'". A Go function has no default values, so every
// named parameter is required; a variadic fn takes any number of positional
// arguments in its final ...T, which has no name and so cannot be passed by
// keyword.
//
// Each argument is converted to its parameter's type as a method call's is
// (see [value.ToGoAs]); one that cannot be is a TypeError. A parameter of type
// [value.Value] receives the template's value unconverted, and the same holds
// for the result. fn may return its result alone or with a trailing error,
// which fails the render; a panic in fn fails the render rather than unwinding
// the caller.
//
// FilterFunc panics if fn is not a function of that shape, or if params does
// not name each of its parameters after the value, exactly as
// text/template.Funcs panics on a bad function: it is a mistake in the program,
// made once, at setup.
func FilterFunc(name string, fn any, params ...string) Filter {
	g := newGoFunc("FilterFunc", name, fn, params, nil)
	return func(s *State, v value.Value, args *value.CallArgs) (value.Value, error) {
		return g.call(s, v, args)
	}
}

// TestFunc is [FilterFunc] for a [Test]: fn's result is a bool, alone or with
// a trailing error.
//
//	env.AddTest("multiple_of", gojja2.TestFunc("multiple_of",
//		func(n, of int) bool { return n%of == 0 }, "of"))
func TestFunc(name string, fn any, params ...string) Test {
	g := newGoFunc("TestFunc", name, fn, params, reflect.TypeFor[bool]())
	return func(s *State, v value.Value, args *value.CallArgs) (bool, error) {
		r, err := g.call(s, v, args)
		if err != nil {
			return false, err
		}
		return r.AsBool(), nil
	}
}

// goFunc is a Go function checked once, at adaptation, and called many times.
type goFunc struct {
	name string
	fn   reflect.Value
	// withState is whether fn's first parameter is the *State.
	withState bool
	// in is fn's parameter types after the *State: the value, then the
	// named ones, then the variadic slice if there is one.
	in []reflect.Type
	// sig is the Python signature the arguments are bound against.
	sig signature
}

var (
	stateType = reflect.TypeFor[*State]()
	valueType = reflect.TypeFor[value.Value]()
	errorType = reflect.TypeFor[error]()
)

func newGoFunc(caller, name string, fn any, params []string, result reflect.Type) *goFunc {
	bad := func(format string, args ...any) {
		panic(fmt.Sprintf("gojja2.%s(%q): ", caller, name) + fmt.Sprintf(format, args...))
	}
	rv := reflect.ValueOf(fn)
	if rv.Kind() != reflect.Func || rv.IsNil() {
		bad("%T is not a function", fn)
	}
	t := rv.Type()
	g := &goFunc{name: name, fn: rv}
	for i := range t.NumIn() {
		g.in = append(g.in, t.In(i))
	}
	if len(g.in) > 0 && g.in[0] == stateType {
		g.withState, g.in = true, g.in[1:]
	}
	if len(g.in) == 0 || (t.IsVariadic() && len(g.in) == 1) {
		bad("%s has no parameter for the value", t)
	}
	named := len(g.in) - 1
	if t.IsVariadic() {
		named--
	}
	if len(params) != named {
		bad("%s has %d parameter(s) after the value and %d name(s) were given", t, named, len(params))
	}
	for i, p := range params {
		if p == "" || slices.Contains(params[:i], p) {
			bad("parameter name %q is empty or repeated", p)
		}
	}
	switch {
	case t.NumOut() == 2 && t.Out(1) == errorType:
	case t.NumOut() == 1:
	default:
		bad("%s must return one result, optionally followed by an error", t)
	}
	if result != nil && t.Out(0) != result {
		bad("%s must return %s", t, result)
	}

	// The filtered value is Python's first parameter. It is called value
	// here because a Go function's parameter names cannot be read; a
	// keyword naming it is "multiple values", as it is in Python.
	total := 1 + named
	g.sig = signature{
		pyName:   name,
		params:   append([]string{"value"}, params...),
		required: total,
		total:    total,
	}
	if t.IsVariadic() {
		g.sig.total = -1
	}
	return g
}

func (g *goFunc) call(s *State, v value.Value, args *value.CallArgs) (out value.Value, err error) {
	if err := bindArgs(g.sig, args, 1); err != nil {
		return value.Undefined, err
	}
	// Bound, so every parameter is filled exactly once: positionally, then
	// by keyword into the slots left.
	vals := make([]value.Value, 0, 1+len(args.Pos))
	vals = append(vals, v)
	vals = append(vals, args.Pos...)
	for len(vals) < len(g.sig.params) {
		vals = append(vals, value.Undefined)
	}
	for _, kw := range args.Kwargs {
		vals[slices.Index(g.sig.params, kw.Name)] = kw.Value
	}

	in := make([]reflect.Value, 0, 1+len(vals))
	if g.withState {
		in = append(in, reflect.ValueOf(s))
	}
	variadic := g.fn.Type().IsVariadic()
	for i, a := range vals {
		want := g.in[min(i, len(g.in)-1)]
		if variadic && i >= len(g.in)-1 {
			want = want.Elem()
		}
		if want == valueType {
			in = append(in, reflect.ValueOf(a))
			continue
		}
		got, ok := value.ToGoAs(a, want)
		if !ok {
			return value.Undefined, value.ArgumentTypeError(g.name, i+1, want, a)
		}
		in = append(in, got)
	}

	res, err := g.invoke(in)
	if err != nil {
		return value.Undefined, err
	}
	if len(res) == 2 && !res[1].IsNil() {
		return value.Undefined, res[1].Interface().(error)
	}
	if res[0].Type() == valueType {
		return res[0].Interface().(value.Value), nil
	}
	var policy value.MethodPolicy
	if s != nil && s.env != nil {
		policy = s.env.methods
	}
	return value.FromGoBudget(res[0].Interface(), policy, s)
}

// invoke calls fn, turning a panic into an error. Host code reached from a
// template is called with arguments a template chose, so it can be driven into
// states its author never tested, and a render must fail rather than unwind
// the caller's goroutine.
func (g *goFunc) invoke(in []reflect.Value) (out []reflect.Value, err error) {
	defer func() {
		if r := recover(); r != nil {
			out, err = nil, errs.New(errs.TemplateRuntimeError, "%s() panicked: %v", g.name, r)
		}
	}()
	return g.fn.Call(in), nil
}
