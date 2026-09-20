// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"context"
	"testing"
)

// TestTruncateArgumentsAreCompared: jinja2's |truncate never converts its
// length, leeway or end. It asserts `length >= len(end)` and `leeway >= 0` with
// Python's own comparison, slices by the length only once the text is too long
// to keep, and concatenates the end rather than formatting it.
//
// gojja2 read length and leeway as integers at the filter's door, so a float
// was refused where CPython compares it happily, None was taken for "use the
// default" where CPython raises, a negative leeway was accepted where CPython
// asserts, and the assertion reported a bool as 1 rather than True. The end was
// stringified, so a list end quietly appended "['z']" instead of failing.
func TestTruncateArgumentsAreCompared(t *testing.T) {
	env := mustNew()
	ctx := map[string]any{"t": "  the quick brown fox jumps over the lazy dog  "}

	for _, tc := range []struct{ src, want string }{
		// A float compares its way past both assertions, and only has
		// to be whole once something is sliced by it.
		{`{{ t|truncate(10, false, "...", 1.5) }}`, "  the..."},
		{`{{ "abc"|truncate(5.5) }}`, "abc"},
		// An explicit none leeway is the policy's, the way jinja2's
		// `leeway=None` default is.
		{`{{ t|truncate(10, false, "...", none) }}`, "  the..."},
		// A bool is an int, and compares as one.
		{`{{ "abcdefghij"|truncate(3, true, "") }}`, "abc"},
		{`{{ t|truncate(10, true, "..") }}`, "  the qu.."},
		{`{{ t|truncate(true, false, "") }}`, ""},
		// An end with no length of its own fails as len() does.
		{`{{ "x"|truncate(5, true, "…") }}`, "x"},
	} {
		tmpl, err := env.FromString(tc.src)
		if err != nil {
			t.Fatalf("compile %q: %v", tc.src, err)
		}
		got, err := tmpl.RenderString(context.Background(), ctx)
		if err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s\n got %q\nwant %q", tc.src, got, tc.want)
		}
	}

	for _, tc := range []struct{ src, want string }{
		// The comparison is what refuses a non-number, and it says so
		// by naming the operator rather than the argument.
		{`{{ t|truncate(none) }}`, "'>=' not supported between instances of 'NoneType' and 'int'"},
		{`{{ t|truncate("x") }}`, "'>=' not supported between instances of 'str' and 'int'"},
		{`{{ t|truncate([]) }}`, "'>=' not supported between instances of 'list' and 'int'"},
		{`{{ t|truncate({}) }}`, "'>=' not supported between instances of 'dict' and 'int'"},
		{`{{ t|truncate(10, false, "...", "x") }}`, "'>=' not supported between instances of 'str' and 'int'"},
		// A number that is too small fails the assertion, which reports
		// it as Python prints it: True, not 1, and 2.0, not 2.
		{`{{ t|truncate(1.5) }}`, "expected length >= 3, got 1.5"},
		{`{{ t|truncate(2.0) }}`, "expected length >= 3, got 2.0"},
		{`{{ t|truncate(true) }}`, "expected length >= 3, got True"},
		{`{{ t|truncate(false) }}`, "expected length >= 3, got False"},
		{`{{ t|truncate(2) }}`, "expected length >= 3, got 2"},
		// A negative leeway is an assertion too, not a silent zero.
		{`{{ t|truncate(10, false, "...", -1) }}`, "expected leeway >= 0, got -1"},
		{`{{ t|truncate(10, false, "...", -0.5) }}`, "expected leeway >= 0, got -0.5"},
		// A whole-valued float passes both, then has to be an index.
		{`{{ t|truncate(3.0) }}`, "slice indices must be integers or None or have an __index__ method"},
		// The end is concatenated, so a list end on a string input is a
		// TypeError and not an appended repr.
		{`{{ t|truncate(10, true, ["z"]) }}`, `can only concatenate str (not "list") to str`},
		{`{{ t|truncate(10, false, (1,2)) }}`, `can only concatenate str (not "tuple") to str`},
	} {
		tmpl, err := env.FromString(tc.src)
		if err != nil {
			t.Fatalf("compile %q: %v", tc.src, err)
		}
		out, err := tmpl.RenderString(context.Background(), ctx)
		if err == nil {
			t.Errorf("%s: rendered %q, want %q", tc.src, out, tc.want)
			continue
		}
		if err.Error() != tc.want {
			t.Errorf("%s\n got %q\nwant %q", tc.src, err.Error(), tc.want)
		}
	}
}
