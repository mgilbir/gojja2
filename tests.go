// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"math"
	"strings"
	"unicode"

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
		"escaped":   func(v value.Value) bool { return v.IsSafe() },
		"true":      func(v value.Value) bool { return v.Kind() == value.KindBool && v.AsBool() },
		"false":     func(v value.Value) bool { return v.Kind() == value.KindBool && !v.AsBool() },
		"sequence":  isSequenceValue,
		"iterable":  isIterableValue,
		// "callable" is registered separately: jinja2 maps it straight
		// to Python's builtin, which words its arity error differently.
		"lower": allCased(unicode.IsLower, unicode.IsUpper),
		"upper": allCased(unicode.IsUpper, unicode.IsLower),
	}
	simple["callable"] = isCallableValue
	for name, fn := range simple {
		addTest(env, name, "test_"+name, 0,
			func(_ *State, v value.Value, _ *value.CallArgs) (bool, error) {
				return fn(v), nil
			})
	}
	// Re-register with the builtin's own name so its arity error matches.
	addTest(env, "callable", "callable", 0,
		func(_ *State, v value.Value, _ *value.CallArgs) (bool, error) {
			return isCallableValue(v), nil
		})

	addTest(env, "odd", "test_odd", 0, intParity(1))
	addTest(env, "even", "test_even", 0, intParity(0))
	addTest(env, "divisibleby", "test_divisibleby", 1, testDivisibleBy)
	addTest(env, "sameas", "test_sameas", 1, testSameAs)
	addTest(env, "in", "test_in", 1, testIn)
	addTest(env, "filter", "test_filter", 0, testHasFilter)
	addTest(env, "test", "test_test", 0, testHasTest)

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

// addTest registers a test that refuses more positional arguments than the
// jinja2 function of the same name accepts.
//
// The message names the Python function and counts the tested value as the
// first argument, because that is what CPython reports and a template author
// comparing the two would otherwise see a different error.
func addTest(env *Environment, name, pyName string, maxArgs int, fn Test) {
	env.AddTest(name, func(s *State, v value.Value, args *value.CallArgs) (bool, error) {
		if len(args.Pos) > maxArgs {
			if pyName == "callable" {
				// A C builtin phrases this its own way.
				return false, errs.New(errs.TypeError,
					"callable() takes exactly one argument (%d given)", len(args.Pos)+1)
			}
			return false, errs.New(errs.TypeError,
				"%s() takes %d positional argument%s but %d were given",
				pyName, maxArgs+1, plural(maxArgs+1), len(args.Pos)+1)
		}
		return fn(s, v, args)
	})
}

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

// allCased implements `is lower` and `is upper`, which jinja2 writes as
// str(value).islower(). The stringification matters: a list is tested by its
// repr, so `['a'] is lower` is true.
func allCased(want, other func(rune) bool) func(value.Value) bool {
	return func(v value.Value) bool {
		seen := false
		for _, r := range value.Str(v) {
			if other(r) {
				return false
			}
			if want(r) {
				seen = true
			}
		}
		return seen
	}
}

// intParity implements `is odd` and `is even`.
//
// jinja2 writes these as `value % 2`, which is not the same as asking whether
// the value is an integer: `'0' is even` reaches str.__mod__ and fails as
// printf formatting, not as a type mismatch. Going through the same operator
// inherits that for free.
func intParity(want int64) Test {
	return func(_ *State, v value.Value, _ *value.CallArgs) (bool, error) {
		rem, err := value.Mod(v, value.Int(2))
		if err != nil {
			return false, err
		}
		return value.Equal(rem, value.Int(want)), nil
	}
}

func testDivisibleBy(_ *State, v value.Value, args *value.CallArgs) (bool, error) {
	divisor, ok := args.Arg(0)
	if !ok {
		return false, errs.New(errs.TypeError, "divisibleby requires an argument")
	}
	rem, err := value.Mod(v, divisor)
	if err != nil {
		return false, err
	}
	truth, err := value.IsTrue(rem)
	if err != nil {
		return false, err
	}
	return !truth, nil
}

// testSameAs is Python's `is`, identity rather than equality. Only reference
// types have an identity; for everything else jinja2's answer coincides with
// equality of value and type.
func testSameAs(_ *State, v value.Value, args *value.CallArgs) (bool, error) {
	other, ok := args.Arg(0)
	if !ok {
		return false, errs.New(errs.TypeError, "sameas requires an argument")
	}
	if v.Kind() != other.Kind() {
		return false, nil
	}
	switch v.Kind() {
	case value.KindList, value.KindTuple, value.KindDict, value.KindObject, value.KindFunc:
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

func testIn(_ *State, v value.Value, args *value.CallArgs) (bool, error) {
	container, ok := args.Arg(0)
	if !ok {
		return false, errs.New(errs.TypeError, "in requires an argument")
	}
	return value.Contains(v, container)
}

func testHasFilter(s *State, v value.Value, _ *value.CallArgs) (bool, error) {
	if !v.IsString() {
		return false, nil
	}
	_, ok := s.env.filters[v.AsString()]
	return ok, nil
}

func testHasTest(s *State, v value.Value, _ *value.CallArgs) (bool, error) {
	if !v.IsString() {
		return false, nil
	}
	_, ok := s.env.tests[v.AsString()]
	return ok, nil
}

func comparisonTest(op string) Test {
	return func(_ *State, v value.Value, args *value.CallArgs) (bool, error) {
		other, ok := args.Arg(0)
		if !ok {
			return false, errs.New(errs.TypeError, "%s requires an argument", op)
		}
		return compareStep(op, v, other)
	}
}

// escapeIfNeeded is shared by the escaping filters.
func escapeIfNeeded(v value.Value) value.Value {
	if v.IsSafe() {
		return v
	}
	return value.Safe(escapeHTML(value.Str(v)))
}

// stringOf renders a value for a filter that works on text.
func stringOf(v value.Value) string { return value.Str(v) }

// joinStrings is used by filters that assemble output from parts.
func joinStrings(parts []string, sep string) string { return strings.Join(parts, sep) }
