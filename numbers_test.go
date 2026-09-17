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
