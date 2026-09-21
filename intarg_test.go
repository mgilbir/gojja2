// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"context"
	"testing"
)

// TestIntegerArgumentsAreNotOptionalNones: two filters read an argument that
// Python uses as an integer, and read it too loosely.
//
// |sort hands reverse to sorted(), which takes it as an integer -- so a string,
// a float or a None is refused there, not read for its truth. gojja2 asked
// value.IsTrue, which made `{{ xs|sort("x") }}` a reverse sort and
// `{{ xs|sort(none) }}` a forward one where CPython raises. Only
// case_sensitive is a plain `if` in jinja2's own code, and only that one is
// truthiness.
//
// |center's width has no None to fall back on: 80 is the default for an
// argument that was not written, and an explicit None reaches str.center and is
// refused. gojja2 took None for "use the default" and centred in 80 columns.
//
// Argument Clinic stopped converting sorted()'s reverse in 3.12 and started
// testing it for truth, which is what gojja2 used to do by accident and now
// does on purpose for a new enough interpreter. So the refusals below are
// pinned to the interpreter that still makes them, and the acceptance the
// default now gives is asserted underneath. center's width did not move.
func TestIntegerArgumentsAreNotOptionalNones(t *testing.T) {
	env := mustNew(WithPythonVersion(Python311))
	for _, tc := range []struct{ src, want string }{
		{`{{ [3,1,2]|sort(none) }}`, "'NoneType' object cannot be interpreted as an integer"},
		// And it is read after the value has been walked, because
		// sorted() builds its list before it looks at the keyword:
		// `{{ 1|sort(none) }}` is about the 1, not about the None.
		{`{{ 1|sort(none) }}`, "'int' object is not iterable"},
		{`{{ 1|sort("x") }}`, "'int' object is not iterable"},
		{`{{ [3,1,2]|sort("x") }}`, "'str' object cannot be interpreted as an integer"},
		{`{{ [3,1,2]|sort([]) }}`, "'list' object cannot be interpreted as an integer"},
		{`{{ [3,1,2]|sort(1.5) }}`, "'float' object cannot be interpreted as an integer"},
		// reverse is refused before case_sensitive is even looked at.
		{`{{ [3,1,2]|sort(none, none) }}`, "'NoneType' object cannot be interpreted as an integer"},
		{`{{ "abc"|center(none) }}`, "'NoneType' object cannot be interpreted as an integer"},
	} {
		tmpl, err := env.FromString(tc.src)
		if err != nil {
			t.Fatalf("compile %q: %v", tc.src, err)
		}
		out, err := tmpl.RenderString(context.Background(), nil)
		if err == nil {
			t.Errorf("%s: rendered %q, want %q", tc.src, out, tc.want)
			continue
		}
		if err.Error() != tc.want {
			t.Errorf("%s\n got %q\nwant %q", tc.src, err.Error(), tc.want)
		}
	}

	for _, tc := range []struct{ src, want string }{
		// An integer reverse still works, in either direction, and a
		// bool is an integer.
		{`{{ [3,1,2]|sort }}|{{ [3,1,2]|sort(true) }}`, "[1, 2, 3]|[3, 2, 1]"},
		{`{{ [3,1,2]|sort(1) }}|{{ [3,1,2]|sort(0) }}`, "[3, 2, 1]|[1, 2, 3]"},
		// case_sensitive is truthiness, so None is simply false there.
		{`{{ ["b","A"]|sort(false, none) }}|{{ ["b","A"]|sort(false, 1) }}`,
			"['A', 'b']|['A', 'b']"},
		// And center still defaults when nothing was written.
		{`[{{ "ab"|center(6) }}]`, "[  ab  ]"},
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

// TestBoolArgsAreTruthyOnANewInterpreter is the other half of
// TestIntegerArgumentsAreNotOptionalNones: what 3.12 made of the arguments it
// stopped converting.
//
// `bool(accept={int})` became an ordinary truth test there, so the values that
// test refuses are simply true or false for the default interpreter. Asserting
// only the refusal would leave the acceptance to nothing but the corpus, and a
// rule with one side tested is a rule that can be half wrong.
func TestBoolArgsAreTruthyOnANewInterpreter(t *testing.T) {
	env := mustNew()
	for _, tc := range []struct{ src, want string }{
		{`{{ [3,1,2]|sort(none) }}`, "[1, 2, 3]"},
		{`{{ [3,1,2]|sort("x") }}`, "[3, 2, 1]"},
		{`{{ [3,1,2]|sort([]) }}`, "[1, 2, 3]"},
		{`{{ [3,1,2]|sort(1.5) }}`, "[3, 2, 1]"},
		{`{{ "a\nb".splitlines(1.5)|list }}`, "['a\\n', 'b']"},
		{`{{ "a\nb".splitlines(none)|list }}`, "['a', 'b']"},
		// A value past a C int is just true now, where it used to
		// overflow the conversion.
		{`{{ [3,1]|sort(reverse=2147483648) }}`, "[3, 1]"},
		// center's width is a real integer and did not move.
		{`{{ "abc"|center(none) }}`, ""},
	} {
		tmpl, err := env.FromString(tc.src)
		if err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		got, err := tmpl.RenderString(context.Background(), nil)
		if tc.want == "" {
			if err == nil {
				t.Errorf("%s: rendered %q, want a refusal", tc.src, got)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("%s: got %q, %v; want %q", tc.src, got, err, tc.want)
		}
	}
}
