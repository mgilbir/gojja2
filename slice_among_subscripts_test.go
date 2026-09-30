// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"context"
	"errors"
	"testing"

	"github.com/mgilbir/gojja2/errs"
	"github.com/mgilbir/gojja2/value"
)

// TestSliceAmongSubscriptsIsRefused: jinja2 writes a slice out as
// `start:stop:step`, and only a subscript holding it alone brackets it. Among
// several -- `x[1:2, 3]` -- it comes out as `(1:2, 3)`, which Python cannot
// parse, so the generated module is refused with `SyntaxError: invalid syntax`.
// The corpus holds the same shapes, but the golden's message names a line of
// the generated module, so they are known failures there and this pins what
// the class and the words are.
//
// The refusal is the parse error that it is: it comes before a compile error
// such as a repeated keyword, and after the generator's own refusals, and a
// subscript of constants never reaches it because it folds to an undefined
// first. Every shape was measured against CPython jinja2.
func TestSliceAmongSubscriptsIsRefused(t *testing.T) {
	env, err := New()
	if err != nil {
		t.Fatal(err)
	}
	for _, src := range []string{
		`{% set l = [1] %}{{ l[1:2, 3] }}`,
		`{% set l = [1] %}{{ l[:, ::] }}`,
		`{% if false %}{{ z[1:2, 3] }}{% endif %}`,
		`{% macro m() %}{{ z[1:2, 3] }}{% endmacro %}`,
		`{% if [1][0:1, 3] %}x{% endif %}`,
		`{% set x = [1, 2, 3][1:2, 3] %}`,
		`{% set l = [[1]] %}{{ l[l[0:1, 3]] }}`,
		// A parse error beats a compile error, in either order.
		`{% macro m(a=1) %}{% endmacro %}{% set l = [1] %}{{ m(a=1, a=2) }}{{ l[1:2, 3] }}`,
		`{% macro m(a=1) %}{% endmacro %}{% set l = [1] %}{{ l[1:2, 3] }}{{ m(a=1, a=2) }}`,
	} {
		_, err := env.FromString(src)
		if err == nil {
			t.Errorf("%s compiled; CPython refuses it with a SyntaxError", src)
			continue
		}
		if got := err.Error(); got != "invalid syntax" {
			t.Errorf("%s\n  got  %q\n  want %q", src, got, "invalid syntax")
		}
		if !errors.Is(err, errs.SyntaxError) {
			t.Errorf("%s: the error is not a SyntaxError: %v", src, err)
		}
	}

	// The generator's own refusals come first.
	_, err = env.FromString(`{% set l = [1] %}{{ l[1:2, 3] }}{{ 1|nosuch }}`)
	if err == nil || !errors.Is(err, errs.TemplateAssertionError) {
		t.Errorf("a missing filter should be named before the subscript, got %v", err)
	}
}

// TestSliceAmongConstantSubscriptsFoldsToUndefined is the other half: when
// every part is constant the print folds, getitem swallows the TypeError, and
// what renders is the undefined -- naming the whole tuple under DebugUndefined.
func TestSliceAmongConstantSubscriptsFoldsToUndefined(t *testing.T) {
	for _, tc := range []struct {
		src, want string
		debug     bool
	}{
		{`[{{ [1, 2, 3][1:2, 3] }}]`, "[]", false},
		{`{{ [1, 2, 3][1:2, 3]|default('d') }}`, "d", false},
		{`{{ [1, 2, 3][1:2, 3] is defined }}`, "False", false},
		{`{{ [1, 2, 3][1:2, 3] }}`,
			"{{ no such element: list object[(slice(1, 2, None), 3)] }}", true},
		{`{{ 'abc'[:, 'x', 3.5] }}`,
			"{{ no such element: str object[(slice(None, None, None), 'x', 3.5)] }}", true},
	} {
		var opts []Option
		if tc.debug {
			opts = append(opts, WithUndefined(value.UndefinedDebug))
		}
		env, err := New(opts...)
		if err != nil {
			t.Fatal(err)
		}
		tmpl, err := env.FromString(tc.src)
		if err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		got, err := tmpl.RenderString(context.Background(), nil)
		if err != nil || got != tc.want {
			t.Errorf("%s\n  got  %q, %v\n  want %q", tc.src, got, err, tc.want)
		}
	}
}
