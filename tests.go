// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"math"

	"github.com/mgilbir/gojja2/errs"
	"github.com/mgilbir/gojja2/value"
)

func registerDefaultTests(env *Environment) {
	simple := map[string]func(value.Value) bool{
		"undefined": func(v value.Value) bool { return v.IsUndefined() },
		"defined":   func(v value.Value) bool { return !v.IsUndefined() },
		"none":      func(v value.Value) bool { return v.IsNone() },
		"boolean":   func(v value.Value) bool { return v.Kind() == value.KindBool },
		"integer":   func(v value.Value) bool { return v.Kind() == value.KindInt },
		"float":     func(v value.Value) bool { return v.Kind() == value.KindFloat },
		"number":    func(v value.Value) bool { return v.IsNumber() },
		"string":    func(v value.Value) bool { return v.IsString() },
		"mapping":   func(v value.Value) bool { return v.IsMapping() },
		"escaped":   isEscaped,
		"true":      func(v value.Value) bool { return v.Kind() == value.KindBool && v.AsBool() },
		"false":     func(v value.Value) bool { return v.Kind() == value.KindBool && !v.AsBool() },
	}
	simple["callable"] = isCallableValue
	for name, fn := range simple {
		addTest(env, name, func(_ *State, v value.Value, _ *value.CallArgs) (bool, error) {
			return fn(v), nil
		})
	}

	// These four ask the value for something a StrictUndefined refuses, so
	// they cannot be registered as the plain predicates above: `sequence`
	// swallows the refusal and answers False, and the other three let it
	// out.
	addTest(env, "sequence", func(_ *State, v value.Value, _ *value.CallArgs) (bool, error) {
		// jinja2 writes this as len(value) and value[0] inside a
		// try/except, so a class that refuses len() is simply not a
		// sequence rather than an error.
		if value.StrictRefusal(v) != nil {
			return false, nil
		}
		return isSequenceValue(v), nil
	})
	addTest(env, "iterable", func(_ *State, v value.Value, _ *value.CallArgs) (bool, error) {
		// iter() is not wrapped, so its refusal reaches the template.
		if err := value.StrictRefusal(v); err != nil {
			return false, err
		}
		return isIterableValue(v), nil
	})
	addTest(env, "lower", stringCased(isLowerString))
	addTest(env, "upper", stringCased(isUpperString))

	addTest(env, "odd", intParity(1))
	addTest(env, "even", intParity(0))
	addTest(env, "divisibleby", testDivisibleBy)
	addTest(env, "sameas", testSameAs)
	addTest(env, "in", testIn)
	addTest(env, "filter", testHasFilter)
	addTest(env, "test", testHasTest)

	// The comparison tests take their operand as the single argument.
	for name, op := range map[string]string{
		"eq": "eq", "equalto": "eq", "==": "eq",
		"ne": "ne", "!=": "ne",
		"lt": "lt", "lessthan": "lt", "<": "lt",
		"le": "lteq", "<=": "lteq",
		"gt": "gt", "greaterthan": "gt", ">": "gt",
		"ge": "gteq", ">=": "gteq",
	} {
		env.AddTest(name, comparisonTest(op))
	}
}

// addTest registers a test.
//
// It used to carry a hand-written argument count per test, and the arity
// message that went with it -- the only such check in the engine, since
// filters had none at all. Both now come from jinja2's own signatures; see
// arity.go and tools/oracle/gen_arity.py.
func addTest(env *Environment, name string, fn Test) { env.AddTest(name, fn) }

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func isSequenceValue(v value.Value) bool {
	// jinja2's test asks for len() and __getitem__, and Undefined has
	// both, so it answers True even though using either one raises.
	if v.IsUndefined() {
		return true
	}
	switch v.Kind() {
	case value.KindList, value.KindTuple, value.KindString, value.KindBytes, value.KindDict:
		return true
	case value.KindObject:
		switch v.Interface().(type) {
		case value.Sequence, value.Mapping:
			return true
		}
	}
	return false
}

func isIterableValue(v value.Value) bool {
	// Undefined defines __iter__ -- it yields nothing -- so iter() accepts
	// it and the test answers True.
	if v.IsUndefined() {
		return true
	}
	if isSequenceValue(v) {
		return true
	}
	_, ok := v.Interface().(value.Iterable)
	return v.Kind() == value.KindObject && ok
}

func isCallableValue(v value.Value) bool {
	// Undefined defines __call__ -- it raises, but it is there -- so
	// callable() answers True for it.
	if v.IsUndefined() {
		return true
	}
	// A macro is invoked through a path of its own in exec.invoke, before
	// the value.Caller check, and so does not implement Caller. Testing
	// only for Caller answered False for the one callable a template
	// defines for itself, while calling it worked. Whatever invoke will
	// call, this has to agree is callable.
	if _, ok := v.Interface().(*macroObject); ok {
		return true
	}
	_, ok := v.Interface().(value.Caller)
	return ok
}

// stringCased implements `is lower` and `is upper`, which jinja2 writes as
// str(value).islower(). The stringification matters: a list is tested by its
// repr, so `['a'] is lower` is true.
//
// The predicate itself is the one str.islower uses, so the test and the method
// cannot disagree -- they did, because this rolled its own loop over Go's
// category predicates while the method used another.
// stringCased builds `is lower` and `is upper`, which jinja2 writes as
// str(value).islower(). The coercion is the operation, so a StrictUndefined
// refuses it rather than being tested as "".
func stringCased(f func(string) bool) Test {
	return func(_ *State, v value.Value, _ *value.CallArgs) (bool, error) {
		text, err := strictStr(v)
		if err != nil {
			return false, err
		}
		return f(text), nil
	}
}

// intParity implements `is odd` and `is even`.
//
// jinja2 writes these as `value % 2`, which is not the same as asking whether
// the value is an integer: `'0' is even` reaches str.__mod__ and fails as
// printf formatting, not as a type mismatch. Going through the same operator
// inherits that for free.
func intParity(want int64) Test {
	return func(s *State, v value.Value, _ *value.CallArgs) (bool, error) {
		rem, err := value.Mod(v, value.Int(2), s, s.PythonVersion())
		if err != nil {
			return false, err
		}
		return value.Equal(rem, value.Int(want)), nil
	}
}

// The parameter names here are jinja2's own, and a template may use them:
// `{{ 4 is divisibleby(num=2) }}` is the same call as `divisibleby(2)`. They
// are the names arity.go carries, which is what a wrong one is checked
// against.
func testDivisibleBy(s *State, v value.Value, args *value.CallArgs) (bool, error) {
	divisor, ok := arg(args, 0, "num")
	if !ok {
		return false, errs.New(errs.TypeError, "divisibleby requires an argument")
	}
	rem, err := value.Mod(v, divisor, s, s.PythonVersion())
	if err != nil {
		return false, err
	}
	// jinja2 writes `value % num == 0`, which is a comparison against zero
	// and not a truth test. They part company wherever % is not division:
	// on a string it is *formatting*, so `"" is divisibleby([])` is
	// `"" == 0` -- False -- where an empty result read as falsey answered
	// True.
	return value.Equal(rem, value.Int(0)), nil
}

// testSameAs is Python's `is`, identity rather than equality. Only reference
// types have an identity; for everything else jinja2's answer coincides with
// equality of value and type.
func testSameAs(_ *State, v value.Value, args *value.CallArgs) (bool, error) {
	other, ok := arg(args, 0, "other")
	if !ok {
		return false, errs.New(errs.TypeError, "sameas requires an argument")
	}
	if v.Kind() != other.Kind() {
		return false, nil
	}
	switch v.Kind() {
	case value.KindTuple:
		// CPython shares one empty tuple, so `() is ()` is true where
		// every other pair of separately built tuples is not.
		if a, _ := v.Seq(); a.Len() == 0 {
			b, _ := other.Seq()
			return b.Len() == 0, nil
		}
		return v.Interface() == other.Interface(), nil
	case value.KindList, value.KindDict, value.KindObject, value.KindFunc:
		return v.Interface() == other.Interface(), nil
	case value.KindFloat:
		// NaN is not identical to another NaN unless it is the same
		// object, which for a float value it never is here.
		if math.IsNaN(v.AsFloat()) {
			return false, nil
		}
	}
	return value.Equal(v, other), nil
}

func testIn(s *State, v value.Value, args *value.CallArgs) (bool, error) {
	container, ok := arg(args, 0, "seq")
	if !ok {
		return false, errs.New(errs.TypeError, "in requires an argument")
	}
	return value.Contains(v, container, s, s.PythonVersion())
}

// testHasFilter and testHasTest are jinja2's `value in env.filters` and
// `value in env.tests`.
//
// That is a dict membership test, so the value is hashed before anything looks
// at whether it is a name: `{{ [1] is filter }}` is "unhashable type: 'list'"
// and not False. Answering False for everything that is not a string made a
// template asking an unanswerable question look like it had an answer.
func testHasFilter(s *State, v value.Value, _ *value.CallArgs) (bool, error) {
	return hasRegistered(v, func(name string) bool {
		_, ok := s.env.filters[name]
		return ok
	}, s.PythonVersion())
}

func testHasTest(s *State, v value.Value, _ *value.CallArgs) (bool, error) {
	return hasRegistered(v, func(name string) bool {
		_, ok := s.env.tests[name]
		return ok
	}, s.PythonVersion())
}

func hasRegistered(v value.Value, lookup func(string) bool, py value.PythonVersion) (bool, error) {
	if err := value.Hashable(v, py, value.AsDictKey); err != nil {
		return false, err
	}
	if !v.IsString() {
		// Hashable, but no name can equal it.
		return false, nil
	}
	return lookup(v.AsString()), nil
}

func comparisonTest(op string) Test {
	return func(s *State, v value.Value, args *value.CallArgs) (bool, error) {
		other, ok := args.Arg(0)
		if !ok {
			return false, errs.New(errs.TypeError, "%s requires an argument", op)
		}
		return compareStep(op, v, other, s, s.PythonVersion())
	}
}

// isEscaped is jinja2's `escaped` test, which is `hasattr(value, "__html__")`
// -- so it is true of anything that carries its own escaped form and not only
// of a Markup string.
func isEscaped(v value.Value) bool {
	if v.IsSafe() {
		return true
	}
	_, ok := value.HTML(v)
	return ok
}

// escapeIfNeeded is shared by the escaping filters. It is markupsafe's
// escape(): Markup passes through, a value carrying its own escaped form hands
// that over, and everything else is escaped.
func escapeIfNeeded(v value.Value) value.Value {
	if v.IsSafe() {
		return v
	}
	if html, ok := value.HTML(v); ok {
		return value.Safe(html)
	}
	return value.Safe(escapeHTML(value.Str(v)))
}
