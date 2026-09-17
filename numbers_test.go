// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import "testing"

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
