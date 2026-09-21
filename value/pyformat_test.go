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
		got, err := value.FormatPercent(value.String(tc.format), tc.arg, nil, value.DefaultPythonVersion)
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
		{"%c", value.Float(1), errs.TypeError, "%c requires an int or a unicode character, not float"},
		{"%c", value.None, errs.TypeError, "%c requires an int or a unicode character, not NoneType"},
		{"%c", value.Int(1114112), errs.OverflowError, "%c arg not in range(0x110000)"},
		{"%c", value.Int(-1), errs.OverflowError, "%c arg not in range(0x110000)"},
		{"%c", value.String("ab"), errs.TypeError, "%c requires an int or a unicode character, not a string of length 2"},
		{"%d", value.Float(math.Inf(1)), errs.OverflowError, "cannot convert float infinity to integer"},
		{"%d", value.Float(math.NaN()), errs.ValueError, "cannot convert float NaN to integer"},
		// o, x and X take an integer only; d, i and u truncate a float.
		{"%x", value.Float(1.5), errs.TypeError, "%x format: an integer is required, not float"},
		{"%o", value.Float(1.5), errs.TypeError, "%o format: an integer is required, not float"},
		{"%d", value.String("x"), errs.TypeError, "%d format: a real number is required, not str"},
		{"%x", value.String("x"), errs.TypeError, "%x format: an integer is required, not str"},
		{"%d", value.NewList(), errs.TypeError, "%d format: a real number is required, not list"},
	} {
		_, err := value.FormatPercent(value.String(tc.format), tc.arg, nil, value.DefaultPythonVersion)
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
		got, err := value.FormatPercent(value.String(tc.format), tc.args, nil, value.DefaultPythonVersion)
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

// TestFormatSpecRules pins the parts of the format mini-language that a
// 40,000-case sweep against CPython turned up. value.None of them is guessable from
// the others.
//
//   - A grouping option is allowed or refused by the format code alone, before
//     the value is looked at -- so `{:,x}` on a float is about the comma and
//     `{:x}` on the same float is about the code. Underscore is allowed in the
//     power-of-two bases, where it separates every four digits.
//   - A type with no __format__ of its own takes the empty spec and nothing
//     else, and never looks at what the spec says.
//   - A leading zero is a fill for any value, and an alignment only for one
//     that aligns right by default.
//   - The alternate form keeps a float's decimal point, and neither it nor the
//     grouping reaches past the exponent.
//   - Zeros written into a grouped number join it and take separators of their
//     own.
//   - A precision with no type is 'g' with the threshold one place lower and a
//     ".0" kept on an all-digit result.
func TestFormatSpecRules(t *testing.T) {
	for _, tc := range []struct {
		v    value.Value
		spec string
		want string
	}{
		// Grouping by code, not by value.
		{value.Float(1.5), ",f", "1.500000"},
		{value.Int(1048575), "_x", "f_ffff"},
		{value.Int(1048575), "_b", "1111_1111_1111_1111_1111"},
		{value.Int(1048575), "_d", "1_048_575"},
		// A leading zero fills a string but does not align it.
		{value.String("ab"), "0.0s", ""},
		{value.String("ab"), "05", "ab000"},
		{value.Int(1), ">06,", "000001"},
		{value.Float(1.5), "=-06g", "0001.5"},
		// The alternate form keeps the point, and stops at the exponent.
		{value.Float(1.5), "#.0f", "2."},
		{value.Float(1.5), "#.0e", "2.e+00"},
		{value.Float(math.Copysign(0, -1)), "#.0f", "-0."},
		{value.Int(1), ",.0E", "1E+00"},
		{value.Float(123456.0), ",g", "123,456"},
		// Padding zeros are grouped with the number.
		{value.Int(1), "06,d", "00,001"},
		{value.Int(1), "-#06,.0%", "0,100.%"},
		{value.Int(-1), "=+#06_.0F", "-0_001."},
		// A precision with no type.
		{value.Float(1.5), ".0", "2e+00"},
		{value.Float(1.5), ".1", "2e+00"},
		{value.Float(1.5), ".2", "1.5"},
		{value.Float(12.0), ".3", "12.0"},
		{value.Float(123456.789), ".2", "1.2e+05"},
		{value.Float(1.5), ".0g", "2"},
		{value.Float(1.5), "#.3", "1.50"},
	} {
		got, err := value.FormatValue(tc.v, tc.spec, nil)
		if err != nil {
			t.Errorf("format(%s, %q): %v", value.Repr(tc.v), tc.spec, err)
			continue
		}
		if got != tc.want {
			t.Errorf("format(%s, %q)\n got %q\nwant %q", value.Repr(tc.v), tc.spec, got, tc.want)
		}
	}

	for _, tc := range []struct {
		v    value.Value
		spec string
		want string
	}{
		// The grouping check runs on the code, whatever the value is.
		{value.Float(1.5), ",x", "Cannot specify ',' with 'x'."},
		{value.Int(5), ",n", "Cannot specify ',' with 'n'."},
		{value.Int(5), "_c", "Cannot specify '_' with 'c'."},
		{value.Int(5), ",b", "Cannot specify ',' with 'b'."},
		{value.String("ab"), ",", "Cannot specify ',' with 's'."},
		{value.String("ab"), "+_s", "Cannot specify '_' with 's'."},
		// The string checks, in CPython's order.
		{value.String("ab"), " s", "Space not allowed in string format specifier"},
		{value.String("ab"), "+s", "Sign not allowed in string format specifier"},
		{value.String("ab"), "#s", "Alternate form (#) not allowed in string format specifier"},
		{value.String("ab"), "=s", "'=' alignment not allowed in string format specifier"},
		{value.String("ab"), "=#s", "Alternate form (#) not allowed in string format specifier"},
		// The integer checks, in CPython's order.
		{value.Int(5), ".3d", "Precision not allowed in integer format specifier"},
		{value.Int(5), "+.3c", "Precision not allowed in integer format specifier"},
		{value.Int(5), "+c", "Sign not allowed with integer format specifier 'c'"},
		{value.Int(5), "#c", "Alternate form (#) not allowed with integer format specifier 'c'"},
		// A type with no __format__ never reads the spec.
		{value.None, ",n", "unsupported format string passed to NoneType.__format__"},
		{value.NewList(value.Int(1)), "_a", "unsupported format string passed to list.__format__"},
		{value.None, ">5", "unsupported format string passed to NoneType.__format__"},
	} {
		_, err := value.FormatValue(tc.v, tc.spec, nil)
		if err == nil {
			t.Errorf("format(%s, %q): no error, want %q", value.Repr(tc.v), tc.spec, tc.want)
			continue
		}
		if err.Error() != tc.want {
			t.Errorf("format(%s, %q)\n got %q\nwant %q", value.Repr(tc.v), tc.spec, err.Error(), tc.want)
		}
	}
}
