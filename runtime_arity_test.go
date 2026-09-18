// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"context"
	"testing"
)

// TestRuntimeArity: loop, a block reference and the macro a {% call %} block
// builds are all Python objects, and a call to one binds against a method with
// self counted. gojja2 had hand-written checks for each that were looser and
// worded differently:
//
//   - loop.cycle and loop.changed ignored a keyword argument entirely, so
//     `loop.changed(a=1)` quietly reported a change;
//   - `loop()` reported the missing 'recursive' marker where Python has not
//     finished binding the call yet, and the marker message itself was worded
//     differently from jinja2's;
//   - `loop(iterable=x)` was a missing argument, though the parameter is named;
//   - a block reference named the block rather than itself;
//   - the caller macro has no name, and CPython prints that as None, not ”.
func TestRuntimeArity(t *testing.T) {
	env := New()
	for _, tc := range []struct{ src, want string }{
		// loop.cycle and loop.changed take *args and no keywords, and a
		// keyword is refused before the body runs.
		{`{% for i in [1,2] %}{{ loop.cycle(a=1) }}{% endfor %}`,
			"LoopContext.cycle() got an unexpected keyword argument 'a'"},
		{`{% for i in [1,2] %}{{ loop.cycle(1,a=2) }}{% endfor %}`,
			"LoopContext.cycle() got an unexpected keyword argument 'a'"},
		{`{% for i in [1,2] %}{{ loop.changed(a=1) }}{% endfor %}`,
			"LoopContext.changed() got an unexpected keyword argument 'a'"},
		{`{% for i in [1,2] %}{{ loop.cycle() }}{% endfor %}`, "no items for cycling given"},

		// loop() is bound before the marker is looked at, so a wrong
		// count is about the count even in a plain loop.
		{`{% for i in [1,2] %}{{ loop() }}{% endfor %}`,
			"LoopContext.__call__() missing 1 required positional argument: 'iterable'"},
		{`{% for i in [1,2] %}{{ loop(i,i) }}{% endfor %}`,
			"LoopContext.__call__() takes 2 positional arguments but 3 were given"},
		{`{% for i in [[1]] recursive %}{{ loop() }}{% endfor %}`,
			"LoopContext.__call__() missing 1 required positional argument: 'iterable'"},
		{`{% for i in [[1]] recursive %}{{ loop(i,a=1) }}{% endfor %}`,
			"LoopContext.__call__() got an unexpected keyword argument 'a'"},
		// And only a correctly bound call reaches the marker.
		{`{% for i in [1,2] %}{{ loop(i) }}{% endfor %}`,
			"The loop must have the 'recursive' marker to be called recursively."},

		// A block reference names itself, and counts self.
		{`{% block b %}x{% endblock %}{{ self.b(1) }}`,
			"BlockReference.__call__() takes 1 positional argument but 2 were given"},
		{`{% block b %}x{% endblock %}{{ self.b(1,2) }}`,
			"BlockReference.__call__() takes 1 positional argument but 3 were given"},
		{`{% block b %}x{% endblock %}{{ self.b(a=1) }}`,
			"BlockReference.__call__() got an unexpected keyword argument 'a'"},

		// The caller macro has no name, and jinja2 prints that as None.
		{`{% macro m() %}{{ caller(1) }}{% endmacro %}{% call m() %}x{% endcall %}`,
			"macro None takes not more than 0 argument(s)"},
		{`{% macro m() %}{{ caller(a=1) }}{% endmacro %}{% call m() %}x{% endcall %}`,
			"macro None takes no keyword argument 'a'"},
		{`{% macro m() %}{{ caller(1,2) }}{% endmacro %}{% call(a) m() %}{{ a }}{% endcall %}`,
			"macro None takes not more than 1 argument(s)"},
		// A macro that does have one is still named.
		{`{% macro m(a) %}{{ a }}{% endmacro %}{{ m(1,2) }}`,
			"macro 'm' takes not more than 1 argument(s)"},
		{`{% macro m(a) %}{{ a }}{% endmacro %}{{ m(b=1) }}`,
			"macro 'm' takes no keyword argument 'b'"},
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
		// The parameter is named, so this binds.
		{`{% for i in [[1],[2]] recursive %}{{ loop(iterable=i) if i is sequence else i }}{% endfor %}`, "12"},
		{`{% for i in [1,2,3] %}{{ loop.cycle("a","b") }}{% endfor %}`, "aba"},
		{`{% for i in [1,1,2] %}{{ loop.changed(i) }}{% endfor %}`, "TrueFalseTrue"},
		{`{% block b %}x{% endblock %}{{ self.b() }}`, "xx"},
		// An unnamed macro prints as anonymous, a named one by its name.
		{`{% macro m() %}{{ caller }}{% endmacro %}{% call m() %}x{% endcall %}`,
			"<Macro anonymous>"},
		{`{% macro m() %}{% endmacro %}{{ m }}`, "<Macro 'm'>"},
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
