// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mgilbir/gojja2/errs"
)

// jinja2 writes a call's keywords out as `name=value` pairs, so a name written
// twice reaches the Python compiler and CPython refuses the *generated module*:
// "keyword argument repeated: a (<template>, line 23)". The line is a line of
// that module, which has no counterpart here, so gojja2 refuses the same
// templates with the same wording and no line. docs/divergences.md records the
// difference and testdata/known_failures.txt admits it for the corpus cases
// that pin jinja2's half -- which is why the rule is graded here: a case listed
// there is only required *not* to match, so it cannot say what gojja2 does.
//
// It was a behavioural divergence before: gojja2 bound the first keyword and
// swept the second into kwargs, so the template rendered here and failed under
// CPython.
func TestRepeatedKeywordIsRefused(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{`{% macro m(a=1) %}{{ a }}{% endmacro %}{{ m(a=2, a=3) }}`,
			"keyword argument repeated: a"},
		{`{{ lst|join(d='-', d='+') }}`, "keyword argument repeated: d"},
		{`{{ n is divisibleby(num=2, num=3) }}`, "keyword argument repeated: num"},
		{`{{ dict(a=1, a=2) }}`, "keyword argument repeated: a"},
		{`{{ s.split(sep='a', sep='b') }}`, "keyword argument repeated: sep"},
		{`{% macro m(a=1) %}{{ a }}{{ caller() }}{% endmacro %}` +
			`{% call m(a=2, a=3) %}c{% endcall %}`, "keyword argument repeated: a"},
		// The *second* occurrence is the one named, so three keywords
		// with the first repeated names it rather than the one between.
		{`{% macro m(a=1, b=2) %}{{ a }}{% endmacro %}{{ m(b=1, a=2, b=3) }}`,
			"keyword argument repeated: b"},
		// A branch that cannot be taken is still generated.
		{`{% macro m(a=1) %}{{ a }}{% endmacro %}` +
			`{% if false %}{{ m(a=2, a=3) }}{% endif %}ok`,
			"keyword argument repeated: a"},
		// A block's body is generated too, however deep.
		{`{% macro m(a=1) %}{{ a }}{% endmacro %}` +
			`{% block b %}{{ m(a=2, a=3) }}{% endblock %}`,
			"keyword argument repeated: a"},
	} {
		_, err := mustNew().FromString(tc.src)
		if err == nil {
			t.Errorf("%s compiled; CPython refuses it with %q", tc.src, tc.want)
			continue
		}
		if got := err.Error(); got != tc.want {
			t.Errorf("%s\n  got  %q\n  want %q", tc.src, got, tc.want)
		}
		// The class is CPython's, because the refusal is CPython's.
		if !errors.Is(err, errs.SyntaxError) {
			t.Errorf("%s: refused as %v, want SyntaxError", tc.src, errs.KindOf(err))
		}
	}
}

// TestRepeatedKeywordThatFoldsIsNot is the other half, and the one that says
// the refusal is placed where jinja2's is rather than in the parser: a node
// that folds never reaches the code generator, and jinja2's as_const collects
// keywords through a dict comprehension, where a repeated name quietly keeps
// the last. Without these the gate above could refuse in the parser and still
// pass, and three templates jinja2 renders would not compile.
func TestRepeatedKeywordThatFoldsIsNot(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{`{{ [1]|join(d='-', d='+') }}`, "1"},
		{`{% set v = [1]|join(d='-', d='+') %}[{{ v }}]`, "[1]"},
		{`{{ ['a','b']|join(d='-', d='+') }}`, "a+b"},
		// Below a root-level extends the print tag is not written at
		// all, so nothing in it is refused. The parent renders.
		{`{% extends 'base.txt' %}{% macro m(a=1) %}{{ a }}{% endmacro %}` +
			`{{ m(a=2, a=3) }}`, "B"},
	} {
		env := mustNew(WithLoader(DictLoader{"base.txt": "B"}))
		tmpl, err := env.FromString(tc.src)
		if err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		var b strings.Builder
		if err := tmpl.RenderValues(context.Background(), &b, nil); err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		if got := b.String(); got != tc.want {
			t.Errorf("%s = %q, want %q", tc.src, got, tc.want)
		}
	}
}
