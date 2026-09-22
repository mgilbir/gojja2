// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package value

import (
	"math/big"
	"strings"

	"github.com/mgilbir/gojja2/errs"
)

// Python's builtin constructors, for `{{ x.__class__(...) }}`.
//
// A template reaches a type object through `__class__`, and calling one builds
// a value in CPython. The conversions are the same ones `%d` and `%f` already
// perform -- CPython's int() and float() are what a printf conversion runs --
// so these are the shared spelling rather than a second implementation.

// ConstructInt is Python's int(v): a number truncates toward zero, a string is
// parsed in the given base, and anything else is a TypeError naming what it
// got. base is 10 unless the caller passed one.
func ConstructInt(v Value, base int, explicitBase bool, py PythonVersion) (Value, error) {
	if explicitBase && v.kind != KindString && v.kind != KindBytes {
		return Undefined, errs.New(errs.TypeError,
			"int() can't convert non-string with explicit base")
	}
	if v.kind == KindString || v.kind == KindBytes {
		text, ok := pyNumericText(v.str, false, py)
		if ok {
			if b, good := new(big.Int).SetString(stripBasePrefix(text, base), base); good {
				return BigInt(b), nil
			}
		}
		return Undefined, errs.New(errs.ValueError,
			"invalid literal for int() with base %d: %s", base, Repr(v))
	}
	b, err := markupInt(v, 'd', py)
	if err != nil {
		// markupInt's type error is worded for a printf conversion; the
		// constructor names itself and the argument's type instead.
		if errs.KindOf(err) == errs.TypeError {
			return Undefined, intArgumentError(v)
		}
		return Undefined, err
	}
	return BigInt(b), nil
}

func intArgumentError(v Value) error {
	return errs.New(errs.TypeError, "int() argument must be a string, "+
		"a bytes-like object or a real number, not '%s'", v.TypeName())
}

// ConstructFloat is Python's float(v).
func ConstructFloat(v Value, py PythonVersion) (Value, error) {
	f, err := markupFloat(v, py)
	if err != nil {
		return Undefined, err
	}
	return Float(f), nil
}

// ConstructBytes is the part of Python's bytes(v) that does not walk anything:
// a bytes copies, a string needs an encoding, and a count is left to the caller
// to size and charge.
//
// done is false for the two shapes the caller must handle with the render in
// hand -- a count, which allocates what the template asked for, and an iterable,
// which walks for as long as the template says. Neither can be charged from
// here, and a constructor that cannot charge is a way to allocate without limit
// outside any {% for %}.
func ConstructBytes(v Value) (result Value, done bool, err error) {
	switch {
	case v.kind == KindBytes:
		return Bytes([]byte(v.str)), true, nil
	case v.kind == KindString:
		return Undefined, true, errs.New(errs.TypeError,
			"string argument without an encoding")
	case v.IsInteger():
		return Undefined, false, nil
	}
	if _, err := Iterate(v); err != nil {
		return Undefined, true, errs.New(errs.TypeError,
			"cannot convert '%s' object to bytes", v.TypeName())
	}
	return Undefined, false, nil
}

// BytesFromItems converts the elements of an already-walked iterable into
// bytes, which is the tail of bytes(iterable) once the walk has been charged.
func BytesFromItems(items []Value) (Value, error) {
	out := make([]byte, 0, len(items))
	for _, item := range items {
		if !item.IsInteger() {
			return Undefined, errs.New(errs.TypeError,
				"'%s' object cannot be interpreted as an integer",
				item.TypeName())
		}
		b, _ := item.BigInt()
		if !b.IsInt64() || b.Int64() < 0 || b.Int64() > 255 {
			return Undefined, errs.New(errs.ValueError,
				"bytes must be in range(0, 256)")
		}
		out = append(out, byte(b.Int64()))
	}
	return Bytes(out), nil
}

// stripBasePrefix removes the literal prefix Python allows in front of a
// number when the base spells it out: int("0x10", 16) is 16, where Go's
// SetString accepts "0x" only when it is inferring the base itself.
//
// Base 0 is left alone, because there the prefix is what chooses the base and
// Go reads it exactly as Python does.
func stripBasePrefix(text string, base int) string {
	var want string
	switch base {
	case 2:
		want = "b"
	case 8:
		want = "o"
	case 16:
		want = "x"
	default:
		return text
	}
	sign := ""
	if len(text) > 0 && (text[0] == '+' || text[0] == '-') {
		sign, text = text[:1], text[1:]
	}
	if len(text) > 2 && text[0] == '0' &&
		(text[1:2] == want || text[1:2] == strings.ToUpper(want)) {
		return sign + text[2:]
	}
	return sign + text
}
