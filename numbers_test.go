// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"context"
	"strings"
	"testing"
)

// TestIntAndFloatAtTheEdges pins the conversions Python does not bound.
//
// int() of a float is exact at any size, so a value past int64 is a big
// integer and not the most negative one -- which is what a raw conversion
// produced, silently. float() has no range to fail on either: a literal too
// large is an infinity and one too small is zero, and Go reports both as
// errors while returning exactly the value Python gives.
//
// The two non-finite cases differ from each other because do_int catches
// different exceptions in its two attempts: int(inf) is an OverflowError that
// only the second attempt catches, and int(nan) is a ValueError that both do.
//
// Expectations from CPython jinja2 3.1.6.
func TestIntAndFloatAtTheEdges(t *testing.T) {
	env := New()
	for _, tc := range []struct{ expr, want string }{
		// 2**63 exactly, by three routes.
		{`'9.223372036854776e+18'|int`, "9223372036854775808"},
		{`9223372036854775808|float|int`, "9223372036854775808"},
		{`'9223372036854775808'|int`, "9223372036854775808"},
		// float() answers where strconv reports a range error.
		{`'1e400'|float`, "inf"},
		{`'-1e400'|float`, "-inf"},
		{`'1e-400'|float`, "0.0"},
		// A string that parses as non-finite falls to the default,
		// because the attempt that raises is the one do_int catches.
		{`'inf'|int`, "0"},
		{`'nan'|int`, "0"},
		{`'inf'|int(7)`, "7"},
		{`'-inf'|int`, "0"},
	} {
		got, err := renderVars(t, env, "{{ "+tc.expr+" }}", nil)
		if err != nil {
			t.Errorf("%s: %v", tc.expr, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s = %q, want %q", tc.expr, got, tc.want)
		}
	}

	// A float that is already infinite raises instead: int(inf) is an
	// OverflowError, and do_int's first attempt does not catch that one.
	_, err := renderVars(t, env, `{{ z|float|int }}`, map[string]any{"z": "inf"})
	if err == nil {
		t.Fatal("inf|int answered; want the OverflowError CPython raises")
	}
	if got, want := err.Error(), "cannot convert float infinity to integer"; got != want {
		t.Errorf("error = %q, want %q", got, want)
	}
	// A NaN does not, because int(nan) is a ValueError.
	got, err := renderVars(t, env, `{{ z|float|int }}`, map[string]any{"z": "nan"})
	if err != nil {
		t.Fatalf("nan|int: %v", err)
	}
	if got != "0" {
		t.Errorf("nan|int = %q, want %q", got, "0")
	}
}

// TestNumericAttributes: int and float each expose a few real attributes, and
// jinja2 reaches them by getattr like any other. gojja2 had no table for
// numbers at all, so every one answered as a missing attribute -- which renders
// as nothing rather than failing, and is how `{{ "{0.real}".format(n) }}` came
// out as an AttributeError where CPython prints the number.
//
// bool is an int subclass, so True.real is the integer 1, not True.
func TestNumericAttributes(t *testing.T) {
	env := New()
	for _, tc := range []struct{ src, want string }{
		// int properties.
		{`{{ (3).real }}|{{ (3).imag }}|{{ (3).numerator }}|{{ (3).denominator }}`, `3|0|3|1`},
		{`{{ (-4).real }}|{{ (-4).numerator }}|{{ (-4).denominator }}`, `-4|-4|1`},
		// bool goes through as the integer it is.
		{`{{ true.real }}|{{ false.real }}|{{ true.numerator }}`, `1|0|1`},
		// int methods.
		{`{{ (3).bit_length() }}|{{ (0).bit_length() }}|{{ (-4).bit_length() }}`, `2|0|3`},
		{`{{ (3).bit_count() }}|{{ (0).bit_count() }}|{{ (-4).bit_count() }}`, `2|0|1`},
		{`{{ (3).as_integer_ratio() }}|{{ (0).as_integer_ratio() }}`, `(3, 1)|(0, 1)`},
		{`{{ (3).conjugate() }}|{{ (-4).conjugate() }}`, `3|-4`},
		// Bigger than an int64, which is where the big.Int path matters.
		{`{{ (10000000000000000000000).bit_length() }}`, `74`},
		{`{{ (10000000000000000000000).real }}`, `10000000000000000000000`},
		// to_bytes, both orders and signed.
		{`{{ (3).to_bytes(2,'big') }}|{{ (3).to_bytes(2,'little') }}`, `b'\x00\x03'|b'\x03\x00'`},
		{`{{ (255).to_bytes(1,'big') }}`, `b'\xff'`},
		// float properties and methods.
		{`{{ (2.5).real }}|{{ (2.5).imag }}`, `2.5|0.0`},
		{`{{ (2.5).is_integer() }}|{{ (1.0).is_integer() }}|{{ (0.0).is_integer() }}`, `False|True|True`},
		{`{{ (2.5).as_integer_ratio() }}|{{ (-1.5).as_integer_ratio() }}`, `(5, 2)|(-3, 2)`},
		{`{{ (2.5).conjugate() }}`, `2.5`},
		// hex(): thirteen digits after the point, exponent unpadded.
		{`{{ (2.5).hex() }}|{{ (1.0).hex() }}|{{ (0.0).hex() }}`,
			`0x1.4000000000000p+1|0x1.0000000000000p+0|0x0.0p+0`},
		{`{{ (-1.5).hex() }}|{{ (-0.5).hex() }}`, `-0x1.8000000000000p+0|-0x1.0000000000000p-1`},
		// A name that is not an attribute is still undefined, so it
		// renders as nothing rather than raising.
		{`[{{ (3).bogus }}]|[{{ (2.5).bogus }}]`, `[]|[]`},
		// And through a format field, which is how this surfaced.
		{`{{ '{0.real}'.format(3) }}|{{ '{0.imag}'.format(2.5) }}`, `3|0.0`},
		{`{{ '{0.real:>6}'.format(3) }}`, `     3`},
	} {
		tmpl, err := env.FromString(tc.src)
		if err != nil {
			t.Fatalf("compile %q: %v", tc.src, err)
		}
		got, err := tmpl.RenderString(context.Background(), nil)
		if err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s\n got %q\nwant %q", tc.src, got, tc.want)
		}
	}

	for _, tc := range []struct{ src, want string }{
		{`{{ (-1).to_bytes(2,'big') }}`, "can't convert negative int to unsigned"},
		{`{{ (300).to_bytes(1,'big') }}`, "int too big to convert"},
		{`{{ (3).to_bytes(2,'sideways') }}`, "byteorder must be either 'little' or 'big'"},
	} {
		tmpl, err := env.FromString(tc.src)
		if err != nil {
			t.Fatalf("compile %q: %v", tc.src, err)
		}
		_, err = tmpl.RenderString(context.Background(), nil)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want %q", tc.src, err, tc.want)
		}
	}
}

// TestIntFilterBaseParsing: |int with a base is Python's int(str, base), which
// big.Int.SetString is not. The old reading stripped "0"+"x" off the front and
// handed the rest over, which got the easy case right and little else.
//
// Failure is not an error: do_int catches it and falls back to
// int(float(value)), which is why "010"|int(0, 0) is 10 even though Python
// refuses that string with base 0.
func TestIntFilterBaseParsing(t *testing.T) {
	env := New()
	for _, tc := range []struct{ src, want string }{
		// Base 0 detects the prefix, in either case.
		{`{{ '0x1f'|int(-1,0) }}|{{ '0X1F'|int(-1,0) }}`, `31|31`},
		{`{{ '0b11'|int(-1,0) }}|{{ '0B11'|int(-1,0) }}`, `3|3`},
		{`{{ '0o17'|int(-1,0) }}|{{ '0O17'|int(-1,0) }}`, `15|15`},
		// A prefix is allowed, not required, when it matches the base.
		{`{{ '0x1f'|int(-1,16) }}|{{ '1f'|int(-1,16) }}`, `31|31`},
		{`{{ '0X1F'|int(-1,16) }}|{{ 'FF'|int(-1,16) }}`, `31|255`},
		// A prefix that does not match the base is just digits.
		{`{{ '0B11'|int(-1,16) }}`, `2833`},
		// A sign no longer puts the prefix out of reach.
		{`{{ '-0x10'|int(-1,16) }}|{{ '+0x10'|int(-1,0) }}`, `-16|16`},
		{`{{ ' 0x10 '|int(-1,0) }}`, `16`},
		// Underscores separate digits, including straight after a prefix.
		{`{{ '1_0'|int(-1,16) }}|{{ '1_0'|int(-1,2) }}|{{ '0x_1f'|int(-1,16) }}`, `16|2|31`},
		{`{{ '0_0'|int(-1,10) }}`, `0`},
		// And are refused doubled, leading or trailing -- which falls
		// through to the float attempt, hence the default.
		{`{{ '1__0'|int(-1,16) }}|{{ '_10'|int(-1,16) }}|{{ '10_'|int(-1,16) }}`, `-1|-1|-1`},
		// Zero in every base, and base 36's full alphabet.
		{`{{ '0'|int(-1,16) }}|{{ '00'|int(-1,2) }}|{{ 'z'|int(-1,36) }}|{{ 'Z'|int(-1,36) }}`,
			`0|0|35|35`},
		// A bare prefix is not a number, except where it is digits.
		{`{{ '0x'|int(-1,16) }}|{{ '0x'|int(-1,36) }}`, `-1|33`},
		// Base 0 refuses a leading zero, and the float fallback answers.
		{`{{ '010'|int(-1,0) }}|{{ '010'|int(-1,8) }}`, `10|8`},
		// Exactly one sign. big.Int reads one of its own, so a
		// repeated one used to cancel out and answer a number: "--4"
		// came back 4 and "-+4" came back -4, where int() raises and
		// |int therefore answers its default.
		{`{{ '--4'|int(-1) }}|{{ '++4'|int(-1) }}`, `-1|-1`},
		{`{{ '+-4'|int(-1) }}|{{ '-+4'|int(-1) }}`, `-1|-1`},
		{`{{ '--4'|int(-1,16) }}|{{ '--0x10'|int(-1,16) }}`, `-1|-1`},
		{`{{ '-4'|int(-1) }}|{{ '+4'|int(-1) }}|{{ ' -4 '|int(-1) }}`, `-4|4|-4`},
		// And the case the differential sweep turned it up on: a join
		// that produces a double sign is not a number.
		{`{{ ('-4'|join(d='-'))|int }}`, `0`},
	} {
		tmpl, err := env.FromString(tc.src)
		if err != nil {
			t.Fatalf("compile %q: %v", tc.src, err)
		}
		got, err := tmpl.RenderString(context.Background(), nil)
		if err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s\n got %q\nwant %q", tc.src, got, tc.want)
		}
	}
}

// TestRoundNegativePrecisionOnAnInteger: Python's round preserves the numeric
// type, so round(3, -1) is the int 0 and not 0.0. gojja2 kept the type for a
// non-negative precision and then fell through to the float path for a
// negative one -- which also produced "-0.0" for a negative input, a thing
// Python never writes for an integer.
//
// The rounding is ties-to-even on the scaled value, and exact: an integer past
// 2**53 cannot be scaled and divided back without losing the digits that decide
// the answer.
func TestRoundNegativePrecisionOnAnInteger(t *testing.T) {
	env := New()
	for _, tc := range []struct{ src, want string }{
		// Ties go to the even multiple, both signs.
		{`{{ (5)|round(-1) }}|{{ (15)|round(-1) }}|{{ (25)|round(-1) }}|{{ (35)|round(-1) }}`,
			`0|20|20|40`},
		{`{{ (-5)|round(-1) }}|{{ (-15)|round(-1) }}|{{ (-25)|round(-1) }}`, `0|-20|-20`},
		// Not a tie, and already a multiple.
		{`{{ (99)|round(-1) }}|{{ (-99)|round(-1) }}|{{ (50)|round(-1) }}`, `100|-100|50`},
		{`{{ (150)|round(-2) }}|{{ (250)|round(-2) }}`, `200|200`},
		// Rounded away entirely, and never "-0.0" or "-0".
		{`{{ (3)|round(-1) }}|{{ (-4)|round(-1) }}|{{ (0)|round(-2) }}`, `0|0|0`},
		// Exact past what a float64 can carry.
		{`{{ (10000000000000000000000)|round(-1) }}`, `10000000000000000000000`},
		{`{{ (10000000000000000000000)|round(-25) }}`, `0`},
		// A non-negative precision is unchanged, and a float stays a float.
		{`{{ (3)|round(0) }}|{{ (3)|round(2) }}|{{ (2.5)|round(-1) }}`, `3|3|0.0`},
	} {
		tmpl, err := env.FromString(tc.src)
		if err != nil {
			t.Fatalf("compile %q: %v", tc.src, err)
		}
		got, err := tmpl.RenderString(context.Background(), nil)
		if err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s\n got %q\nwant %q", tc.src, got, tc.want)
		}
	}
}
