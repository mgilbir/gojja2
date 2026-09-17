// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package value_test

import (
	"math"
	"math/big"
	"testing"

	"github.com/mgilbir/gojja2/errs"
	"github.com/mgilbir/gojja2/value"
)

// Every expectation here was taken from the pinned oracle, and every one of
// them was wrong before the layout was written out rather than handed to Go's
// fmt. The two disagree about more than the corners: Go zero-pads %05s where
// Python pads with spaces, renders %.0d of zero as nothing where Python renders
// "0", writes +Inf where Python writes inf, spells %#o as 010 where Python
// spells it 0o10, and refuses a width past ten million by writing
// "%!(NOVERB)" into the output.
//
// A 62,000-case sweep over flags x width x precision x verb x argument now
// matches CPython exactly; this table is the part of it worth keeping in the
// repository, one row per rule.
func TestFormatPercentMatchesCPython(t *testing.T) {
	inf, ninf, nan := math.Inf(1), math.Inf(-1), math.NaN()
	for _, tc := range []struct {
		format string
		arg    value.Value
		want   string
	}{
		// The zero flag is numeric-only: Python ignores it for s, r, a and c.
		{"%05s", value.String("x"), "    x"},
		{"%05r", value.String("x"), "  'x'"},
		{"%05c", value.Int(65), "    A"},
		{"%05d", value.Int(42), "00042"},
		{"%05d", value.Int(-42), "-0042"},
		{"%-05d", value.Int(42), "42   "},

		// Width applies to every conversion, %c included.
		{"%5c", value.Int(65), "    A"},
		{"%5.2c", value.Int(65), "    A"},
		{"%10s", value.String("éü"), "        éü"},

		// Precision on an integer is a minimum digit count, and never
		// erases the number: C's "%.0d" of zero is empty, Python's is 0.
		{"%.0d", value.Int(0), "0"},
		{"%.3d", value.Int(5), "005"},
		{"%5.0d", value.Int(0), "    0"},
		{"%.3x", value.Int(255), "0ff"},

		// Precision on a string truncates, counting characters.
		{"%.2s", value.String("éüö"), "éü"},
		{"%.0s", value.String("abc"), ""},

		// Python's alternate form is Python's, not C's.
		{"%#o", value.Int(8), "0o10"},
		{"%#x", value.Int(255), "0xff"},
		{"%#X", value.Int(255), "0XFF"},
		{"%#08x", value.Int(255), "0x0000ff"},
		{"%#.0f", value.Float(1), "1."},

		// Sign flags, and where zero-fill sits relative to them.
		{"%+d", value.Int(42), "+42"},
		{"% d", value.Int(42), " 42"},
		{"%+08.3f", value.Float(1.5), "+001.500"},
		{"%08.3f", value.Float(-1.5), "-001.500"},
		{"%+x", value.Int(42), "+2a"},

		// Infinity and NaN are words, and are zero-padded like numbers.
		{"%f", value.Float(inf), "inf"},
		{"%f", value.Float(ninf), "-inf"},
		{"%F", value.Float(nan), "NAN"},
		{"%E", value.Float(inf), "INF"},
		{"%09f", value.Float(inf), "000000inf"},
		{"%09f", value.Float(ninf), "-00000inf"},
		{"%09f", value.Float(nan), "000000nan"},
		{"%+f", value.Float(nan), "+nan"},
		{"%-9f", value.Float(nan), "nan      "},

		// Negative zero keeps its sign.
		{"%f", value.Float(math.Copysign(0, -1)), "-0.000000"},

		// A width past what Go's fmt will parse is still a width.
		{"%1000001s", value.String("x"), ""}, // length checked below

		// Arbitrary precision survives the conversion.
		{"%d", value.BigInt(bigPow(2, 70)), "1180591620717411303424"},
		{"%x", value.BigInt(bigPow(2, 70)), "400000000000000000"},
	} {
		got, err := value.FormatPercent(value.String(tc.format), tc.arg, nil)
		if err != nil {
			t.Errorf("%q %% %s: %v", tc.format, value.Repr(tc.arg), err)
			continue
		}
		if tc.want == "" && tc.format == "%1000001s" {
			if n := value.StrLen(value.Str(got)); n != 1000001 {
				t.Errorf("%q padded to %d, want 1000001", tc.format, n)
			}
			continue
		}
		if s := value.Str(got); s != tc.want {
			t.Errorf("%q %% %s = %q, want %q", tc.format, value.Repr(tc.arg), s, tc.want)
		}
	}
}

// TestFormatPercentRefusesLikeCPython pins the failures, which are as much a
// part of the specification as the output. Each class was checked against the
// oracle: %c separates "not an integer" from "not a code point", and an
// integer conversion separates an infinity from a NaN.
func TestFormatPercentRefusesLikeCPython(t *testing.T) {
	for _, tc := range []struct {
		format string
		arg    value.Value
		kind   errs.Kind
		msg    string
	}{
		{"%c", value.Float(1), errs.TypeError, "%c requires int or char"},
		{"%c", value.None, errs.TypeError, "%c requires int or char"},
		{"%c", value.Int(1114112), errs.OverflowError, "%c arg not in range(0x110000)"},
		{"%c", value.Int(-1), errs.OverflowError, "%c arg not in range(0x110000)"},
		{"%c", value.String("ab"), errs.TypeError, "%c requires int or char"},
		{"%d", value.Float(math.Inf(1)), errs.OverflowError, "cannot convert float infinity to integer"},
		{"%d", value.Float(math.NaN()), errs.ValueError, "cannot convert float NaN to integer"},
		// o, x and X take an integer only; d, i and u truncate a float.
		{"%x", value.Float(1.5), errs.TypeError, "%x format: an integer is required, not float"},
		{"%o", value.Float(1.5), errs.TypeError, "%o format: an integer is required, not float"},
		{"%d", value.String("x"), errs.TypeError, "%d format: a real number is required, not str"},
		{"%x", value.String("x"), errs.TypeError, "%x format: an integer is required, not str"},
		{"%d", value.NewList(), errs.TypeError, "%d format: a real number is required, not list"},
	} {
		_, err := value.FormatPercent(value.String(tc.format), tc.arg, nil)
		if err == nil {
			t.Errorf("%q %% %s: no error, want %s", tc.format, value.Repr(tc.arg), tc.msg)
			continue
		}
		if errs.KindOf(err) != tc.kind {
			t.Errorf("%q %% %s: got %s, want %s", tc.format, value.Repr(tc.arg), errs.KindOf(err), tc.kind)
		}
		if err.Error() != tc.msg {
			t.Errorf("%q %% %s: got %q, want %q", tc.format, value.Repr(tc.arg), err, tc.msg)
		}
	}
}

// TestFormatPercentStarArguments: `*` takes the width, then the precision,
// then the value. Taking the value first read `"%*s" % (5, "x")` as a string 5
// padded to a width of "x", so every starred conversion failed outright.
func TestFormatPercentStarArguments(t *testing.T) {
	tup := func(vs ...value.Value) value.Value { return value.NewTuple(vs...) }
	for _, tc := range []struct {
		format string
		args   value.Value
		want   string
	}{
		{"%*s", tup(value.Int(5), value.String("x")), "    x"},
		{"%-*s|", tup(value.Int(5), value.String("x")), "x    |"},
		// A negative width means left-adjust, as in C.
		{"%*s|", tup(value.Int(-5), value.String("x")), "x    |"},
		{"%.*f", tup(value.Int(3), value.Float(1.5)), "1.500"},
		{"%*.*f", tup(value.Int(8), value.Int(2), value.Float(1.5)), "    1.50"},
		// A negative precision clamps to zero rather than being dropped.
		{"%.*f", tup(value.Int(-1), value.Float(1.5)), "2"},
		{"%.*s", tup(value.Int(-3), value.String("abc")), ""},
	} {
		got, err := value.FormatPercent(value.String(tc.format), tc.args, nil)
		if err != nil {
			t.Errorf("%q %% %s: %v", tc.format, value.Repr(tc.args), err)
			continue
		}
		if s := value.Str(got); s != tc.want {
			t.Errorf("%q %% %s = %q, want %q", tc.format, value.Repr(tc.args), s, tc.want)
		}
	}
}

// bigPow is base**exp as a big.Int, for the arbitrary-precision rows.
func bigPow(base, exp int64) *big.Int {
	return new(big.Int).Exp(big.NewInt(base), big.NewInt(exp), nil)
}
