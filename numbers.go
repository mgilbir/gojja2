// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"math"
	"math/big"
	"strconv"
	"strings"

	"github.com/mgilbir/gojja2/errs"
	"github.com/mgilbir/gojja2/value"
)

// Numbers had no attribute table at all, so every one of these answered as a
// missing attribute -- which renders as nothing rather than failing, and is how
// `{{ "{0.real}".format(n) }}` came out as an AttributeError where CPython
// prints the number.
//
// int and float each expose a handful of real attributes, and jinja2 reaches
// them by getattr like any other. Some are properties and some are methods, so
// this answers both: a property gives its value, a method gives itself bound.
//
// bool is an int subclass, so True.real is the integer 1, not True.

// numericAttr resolves an attribute on an int, a bool or a float.
func numericAttr(s *State, base value.Value, name string) (value.Value, bool) {
	switch base.Kind() {
	case value.KindInt, value.KindBool:
		return intAttr(s, base, name)
	case value.KindFloat:
		return floatAttr(s, base, name)
	}
	return value.Undefined, false
}

// asBig is the integer behind an int or a bool.
func asBig(v value.Value) *big.Int {
	b, _ := v.BigInt()
	return b
}

func intAttr(s *State, base value.Value, name string) (value.Value, bool) {
	switch name {
	// Properties. An integer is its own real part and numerator.
	case "real", "numerator":
		return value.BigInt(asBig(base)), true
	case "imag":
		return value.Int(0), true
	case "denominator":
		return value.Int(1), true

	// Methods.
	case "conjugate":
		return boundNoArgs(s, base, name, func() (value.Value, error) {
			return value.BigInt(asBig(base)), nil
		}), true
	case "is_integer":
		// An int always is one. 3.12 added the method so that a caller
		// can ask without knowing which kind of number it holds; before
		// that the name is simply absent from an int.
		if !s.PythonVersion().IntHasIsInteger() {
			return value.Undefined, false
		}
		return boundNoArgs(s, base, name, func() (value.Value, error) {
			return value.True, nil
		}), true
	case "bit_length":
		return boundNoArgs(s, base, name, func() (value.Value, error) {
			return value.Int(int64(asBig(base).BitLen())), nil
		}), true
	case "bit_count":
		// The number of ones in the absolute value, which is what
		// Python counts -- it is defined on the magnitude, so -4 has
		// one bit set just as 4 does.
		return boundNoArgs(s, base, name, func() (value.Value, error) {
			n := 0
			for _, w := range new(big.Int).Abs(asBig(base)).Bits() {
				for ; w != 0; w &= w - 1 {
					n++
				}
			}
			return value.Int(int64(n)), nil
		}), true
	case "as_integer_ratio":
		return boundNoArgs(s, base, name, func() (value.Value, error) {
			return value.NewTuple(value.BigInt(asBig(base)), value.Int(1)), nil
		}), true
	case "to_bytes":
		return boundNumeric(s, name, func(st *State, args *value.CallArgs) (value.Value, error) {
			return intToBytes(st, asBig(base), args)
		}), true
	case "from_bytes":
		// A classmethod, so the receiver contributes nothing but the
		// route to it: a template cannot name int, only an int.
		return boundNumeric(s, name, func(st *State, args *value.CallArgs) (value.Value, error) {
			return bigFromBytes(st, value.Undefined, args)
		}), true
	}
	return value.Undefined, false
}

func floatAttr(s *State, base value.Value, name string) (value.Value, bool) {
	x := base.AsFloat()
	switch name {
	case "real":
		return value.Float(x), true
	case "imag":
		return value.Float(0), true

	case "conjugate":
		return boundNoArgs(s, base, name, func() (value.Value, error) {
			return value.Float(x), nil
		}), true
	case "is_integer":
		return boundNoArgs(s, base, name, func() (value.Value, error) {
			return value.Bool(!math.IsInf(x, 0) && !math.IsNaN(x) && x == math.Trunc(x)), nil
		}), true
	case "hex":
		return boundNoArgs(s, base, name, func() (value.Value, error) {
			h, err := floatHex(x)
			if err != nil {
				return value.Undefined, err
			}
			return value.String(h), nil
		}), true
	case "as_integer_ratio":
		return boundNoArgs(s, base, name, func() (value.Value, error) {
			return floatRatio(x)
		}), true
	case "fromhex":
		// A classmethod, reached through a float for the same reason
		// from_bytes is reached through an int.
		return boundNumeric(s, name, func(_ *State, args *value.CallArgs) (value.Value, error) {
			v, _ := args.Arg(0)
			if !v.IsString() {
				return value.Undefined, errs.New(errs.TypeError,
					"bad argument type for built-in operation")
			}
			return floatFromHex(value.Str(v))
		}), true
	}
	return value.Undefined, false
}

// boundNumeric wraps a numeric method as the callable an attribute lookup hands
// back, the way builtinMethod does for the container types.
// boundNoArgs is boundNumeric for a method that takes nothing at all, which is
// most of them.
//
// CPython refuses an argument rather than ignoring it, and names the receiver's
// own type rather than where the method was defined: `true.bit_length(1)` is
// "bool.bit_length() takes no arguments (1 given)", not "int.". A keyword beats
// a count, as it does everywhere else in CPython's binding.
//
// None of these had an arity check at all, so `{{ (1).bit_length(1) }}` answered
// 1 where CPython refuses. Found by auditing the error sites no corpus case
// reaches: the *absence* of a message is invisible to that audit, but the
// methods showed up when their neighbours were probed.
func boundNoArgs(s *State, base value.Value, name string,
	fn func() (value.Value, error)) value.Value {
	return boundNumeric(s, name, func(_ *State, args *value.CallArgs) (value.Value, error) {
		if len(args.Kwargs) > 0 {
			return value.Undefined, errs.New(errs.TypeError,
				"%s.%s() takes no keyword arguments", base.TypeName(), name)
		}
		if len(args.Pos) > 0 {
			return value.Undefined, errs.New(errs.TypeError,
				"%s.%s() takes no arguments (%d given)",
				base.TypeName(), name, len(args.Pos))
		}
		return fn()
	})
}

func boundNumeric(s *State, name string, fn func(*State, *value.CallArgs) (value.Value, error)) value.Value {
	return Func(name, func(callState *State, args *value.CallArgs) (value.Value, error) {
		if callState == nil {
			callState = s
		}
		return fn(callState, args)
	})
}

// floatHex is float.hex(): a leading hex digit, thirteen after the point, and a
// binary exponent with no leading zeros. Go writes the same form but trims the
// mantissa and pads the exponent, so both are adjusted.
func floatHex(x float64) (string, error) {
	switch {
	case math.IsNaN(x):
		return "nan", nil
	case math.IsInf(x, 1):
		return "inf", nil
	case math.IsInf(x, -1):
		return "-inf", nil
	}
	sign := ""
	if math.Signbit(x) {
		sign, x = "-", -x
	}
	if x == 0 {
		return sign + "0x0.0p+0", nil
	}
	s := strconv.FormatFloat(x, 'x', 13, 64)
	mant, exp, found := strings.Cut(s, "p")
	if !found {
		return sign + s, nil
	}
	// Go writes the exponent with a sign and at least two digits.
	signCh, digits := exp[:1], strings.TrimLeft(exp[1:], "0")
	if digits == "" {
		digits = "0"
	}
	return sign + mant + "p" + signCh + digits, nil
}

// floatRatio is float.as_integer_ratio(): the exact ratio, since every finite
// float is one.
func floatRatio(x float64) (value.Value, error) {
	if math.IsInf(x, 0) {
		return value.Undefined, errs.New(errs.OverflowError,
			"cannot convert Infinity to integer ratio")
	}
	if math.IsNaN(x) {
		return value.Undefined, errs.New(errs.ValueError,
			"cannot convert NaN to integer ratio")
	}
	r := new(big.Rat).SetFloat64(x)
	return value.NewTuple(value.BigInt(r.Num()), value.BigInt(r.Denom())), nil
}

// intToBytes is int.to_bytes(length, byteorder, *, signed=False).
func intToBytes(st *State, b *big.Int, args *value.CallArgs) (value.Value, error) {
	length, err := intArg(args, 0, "length", 1, cSSizeT)
	if err != nil {
		return value.Undefined, err
	}
	order := "big"
	if v, ok := arg(args, 1, "byteorder"); ok {
		if !v.IsString() {
			return value.Undefined, errs.New(errs.TypeError,
				"to_bytes() argument 'byteorder' must be str, not %s", v.TypeName())
		}
		order = value.Str(v)
	}
	if order != "big" && order != "little" {
		return value.Undefined, errs.New(errs.ValueError,
			"byteorder must be either 'little' or 'big'")
	}
	signed, err := boolArg(args, 2, "signed", false)
	if err != nil {
		return value.Undefined, err
	}
	if length < 0 {
		return value.Undefined, errs.New(errs.ValueError,
			"length argument must be non-negative")
	}
	if b.Sign() < 0 && !signed {
		return value.Undefined, errs.New(errs.OverflowError,
			"can't convert negative int to unsigned")
	}
	// The result is sized by an argument the template chose, so it is
	// charged before it is built.
	if err := st.ChargeBytes(int64(length)); err != nil {
		return value.Undefined, err
	}
	out := make([]byte, length)
	mag := new(big.Int).Abs(b)
	if signed && b.Sign() < 0 {
		// Two's complement in `length` bytes.
		mod := new(big.Int).Lsh(big.NewInt(1), uint(length)*8)
		mag = new(big.Int).Add(b, mod)
		if mag.Sign() < 0 {
			return value.Undefined, errs.New(errs.OverflowError, "int too big to convert")
		}
	}
	if mag.BitLen() > length*8 {
		return value.Undefined, errs.New(errs.OverflowError, "int too big to convert")
	}
	mag.FillBytes(out)
	if order == "little" {
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
	}
	return value.Bytes(out), nil
}

// floatFromHex is float.fromhex, which is not strconv.ParseFloat with a
// different base.
//
// Python's grammar is its own: the "0x" is optional, the binary exponent is
// optional, and the digits are hexadecimal either way -- so "1.8" is 1.5 there
// and 1.8 to Go. It also takes the three non-finite spellings, and tolerates
// surrounding whitespace. The normalisation here puts a string into the form
// Go's hexadecimal float syntax accepts, and refuses anything else in Python's
// words rather than Go's.
func floatFromHex(s string) (value.Value, error) {
	bad := func() (value.Value, error) {
		return value.Undefined, errs.New(errs.ValueError,
			"invalid hexadecimal floating-point string")
	}
	t := strings.TrimSpace(s)
	if t == "" {
		return bad()
	}
	sign := ""
	if t[0] == '+' || t[0] == '-' {
		sign, t = string(t[0]), t[1:]
	}
	switch strings.ToLower(t) {
	case "inf", "infinity":
		return value.Float(math.Inf(map[string]int{"-": -1}[sign] | 1)), nil
	case "nan":
		return value.Float(math.NaN()), nil
	}
	if strings.HasPrefix(t, "0x") || strings.HasPrefix(t, "0X") {
		t = t[2:]
	}
	if t == "" {
		return bad()
	}
	// Split off the binary exponent, which Go requires and Python does not.
	mantissa, exponent := t, "p0"
	if i := strings.IndexAny(t, "pP"); i >= 0 {
		mantissa, exponent = t[:i], "p"+t[i+1:]
		if exponent == "p" {
			return bad()
		}
	}
	// The mantissa has to be hex digits with at most one point, and at
	// least one digit. Go would otherwise accept things Python does not.
	seenPoint, seenDigit := false, false
	for i := range len(mantissa) {
		switch c := mantissa[i]; {
		case c == '.':
			if seenPoint {
				return bad()
			}
			seenPoint = true
		case isHexDigit(c):
			seenDigit = true
		default:
			return bad()
		}
	}
	if !seenDigit {
		return bad()
	}
	f, err := strconv.ParseFloat(sign+"0x"+mantissa+exponent, 64)
	if err != nil {
		// Out of range is an overflow rather than a syntax error, which
		// is what Python reports for a hex float too large to hold.
		if strings.Contains(err.Error(), "value out of range") {
			return value.Undefined, errs.New(errs.OverflowError,
				"hexadecimal value too large to represent as a float")
		}
		return bad()
	}
	return value.Float(f), nil
}
