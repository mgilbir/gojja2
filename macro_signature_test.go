// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"context"
	"strings"
	"testing"
)

// A macro or call block that names a parameter twice is refused.
//
// It used to compile, with the later parameter winning, which is the one
// direction of divergence that actually costs someone something: a macro
// written against gojja2 works here and fails when the template is run under
// CPython jinja2.
//
// jinja2 refuses it too, but from further away. Its parser does not look at
// duplicates, so the duplicate reaches the Python compiler when the macro is
// compiled to a function, and the error names the identifier jinja2 generated
// -- `l_1_a` for a parameter the template called `a` -- and a line of the
// generated module. Neither exists here. The decision now matches; the wording
// cannot, and docs/divergences.md says so.
func TestDuplicateMacroParameterIsRefused(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{`{% macro m(a, a) %}{{ a }}{% endmacro %}`, "duplicate argument 'a' in function definition"},
		{`{% macro m(a, b, a) %}{% endmacro %}`, "duplicate argument 'a' in function definition"},
		// A default on one of them changes nothing.
		{`{% macro m(a, a=1) %}{% endmacro %}`, "duplicate argument 'a' in function definition"},
		// A call block has a signature too, and the same rule.
		{`{% call(a, a) m() %}{% endcall %}`, "duplicate argument 'a' in function definition"},
		{`{% call(x, x) m() %}{% endcall %}`, "duplicate argument 'x' in function definition"},
	} {
		_, err := New().FromString(tc.src)
		if err == nil {
			t.Errorf("%s compiled; want %q", tc.src, tc.want)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want %q", tc.src, err, tc.want)
		}
	}
}

// The shapes jinja2 *accepts* keep working. A duplicate is refused in a
// signature and nowhere else: unpacking, assignment and `with` all let the
// later binding win, and a macro may be redefined.
func TestDuplicateNamesAllowedOutsideASignature(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{`{% for a, a in [(1,2)] %}{{ a }}{% endfor %}`, "2"},
		{`{% set a, a = 1, 2 %}{{ a }}`, "2"},
		{`{% with a = 1, a = 2 %}{{ a }}{% endwith %}`, "2"},
		{`{% macro m(a) %}{% endmacro %}{% macro m(a) %}{% endmacro %}ok`, "ok"},
		{`{% macro m(a, b) %}{{ a }}{{ b }}{% endmacro %}{{ m(1, 2) }}`, "12"},
	} {
		tmpl, err := New().FromString(tc.src)
		if err != nil {
			t.Errorf("%s: compile: %v", tc.src, err)
			continue
		}
		got, err := tmpl.RenderString(context.Background(), nil)
		if err != nil || got != tc.want {
			t.Errorf("%s = %q, %v; want %q", tc.src, got, err, tc.want)
		}
	}
	// The other thing this signature refuses is unchanged, and that one
	// jinja2's own parser catches, so it matches word for word.
	if _, err := New().FromString(`{% macro m(a=1, b) %}{% endmacro %}`); err == nil ||
		err.Error() != "non-default argument follows default argument" {
		t.Errorf("got %v, want the non-default wording", err)
	}
}
