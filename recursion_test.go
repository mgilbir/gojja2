// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mgilbir/gojja2"
	"github.com/mgilbir/gojja2/errs"
)

// The guards collected here all defend one property: no template may exhaust
// the goroutine stack. They are together because they used to be apart -- the
// bound existed for equality and not for ordering, for a macro call and not
// for a block -- and a reader checking whether a new recursive descent is
// covered should have one list to check against.
//
// A Go stack overflow is a fatal error rather than a panic, so catchPanic
// cannot turn it into a render error: a miss here is a dead process, not a
// failed render. That is why every case asserts CPython's RecursionError
// rather than merely asserting that the render returned.

// cyclicLists builds two distinct lists that point at each other, using only
// what a default environment offers: `{% set _ = l.append(x) %}` needs no
// extension. Two *distinct* cycles are the hard case -- a structure compared
// against itself short-circuits on identity and terminates either way.
const cyclicLists = `{% set a = [] %}{% set b = [] %}` +
	`{% set _ = a.append(b) %}{% set _ = b.append(a) %}`

func wantRecursionError(t *testing.T, src string, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: rendered without error, want a RecursionError", src)
	}
	if !errors.Is(err, errs.RecursionError) {
		t.Fatalf("%s: got %T %v, want a RecursionError", src, err, err)
	}
	if !strings.Contains(err.Error(), "maximum recursion depth exceeded") {
		t.Errorf("%s: got %q, want CPython's recursion wording", src, err)
	}
}

// TestCyclicOrderingRaises covers every operator and filter that reaches
// value.compare. Equality was already bounded; ordering was not, and CPython
// raises the same RecursionError for both.
func TestCyclicOrderingRaises(t *testing.T) {
	for name, expr := range map[string]string{
		"lt":      `{{ a < b }}`,
		"lteq":    `{{ a <= b }}`,
		"gt":      `{{ a > b }}`,
		"gteq":    `{{ a >= b }}`,
		"sort":    `{{ [a, b]|sort }}`,
		"min":     `{{ [a, b]|min }}`,
		"max":     `{{ [a, b]|max }}`,
		"groupby": `{{ [{"k": a}, {"k": b}]|groupby("k") }}`,
		"eq":      `{{ a == b }}`,
	} {
		t.Run(name, func(t *testing.T) {
			env := gojja2.New()
			err := renderWith(t, context.Background(), env, cyclicLists+expr)
			wantRecursionError(t, expr, err)
		})
	}
}

// TestDeepNonCyclicOrderingRaises is the same bound reached without a cycle:
// CPython raises RecursionError comparing structures nested past its limit,
// whether or not they close on themselves.
func TestDeepNonCyclicOrderingRaises(t *testing.T) {
	// A namespace, because a {% set %} inside a loop body does not survive
	// the iteration: the nesting has to accumulate somewhere the loop's
	// fresh scope does not discard.
	const build = `{% set ns = namespace(a=[0], b=[1]) %}` +
		`{% for i in range(4000) %}{% set ns.a = [ns.a] %}{% set ns.b = [ns.b] %}{% endfor %}`
	env := gojja2.New()
	err := renderWith(t, context.Background(), env, build+`{{ ns.a < ns.b }}`)
	wantRecursionError(t, "deeply nested <", err)
}

// TestSelfReferentialBlockRaises: rendering a block is entering another
// template function, so it is bounded by the same counter as include, extends
// and a macro call. Without that, `{{ self.x }}` inside block x recursed until
// the process died.
func TestSelfReferentialBlockRaises(t *testing.T) {
	for name, src := range map[string]string{
		"printed":    `{% block x %}{{ self.x }}{% endblock %}`,
		"called":     `{% block x %}{{ self.x() }}{% endblock %}`,
		"mutual":     `{% block a %}{{ self.b }}{% endblock %}{% block b %}{{ self.a }}{% endblock %}`,
		"via macro":  `{% macro m() %}{{ self.x }}{% endmacro %}{% block x %}{{ m() }}{% endblock %}`,
		"via filter": `{% block x %}{{ self.x|upper }}{% endblock %}`,
		"in a set":   `{% block x %}{% set t = self.x %}{{ t }}{% endblock %}`,
	} {
		t.Run(name, func(t *testing.T) {
			env := gojja2.New()
			err := renderWith(t, context.Background(), env, src)
			wantRecursionError(t, src, err)
		})
	}
}

// TestBlockRecursionAllowsRealTemplates guards the other direction. A bound
// that fires on ordinary inheritance would be worse than no bound at all, so
// the shapes real templates use are pinned here: nested blocks, a super()
// chain, and a block rendered through self by name.
func TestBlockRecursionAllowsRealTemplates(t *testing.T) {
	loader := gojja2.DictLoader(map[string]string{
		"base.html": `[{% block outer %}o{% block inner %}i{% endblock %}{% endblock %}]`,
		"mid.html":  `{% extends "base.html" %}{% block inner %}m{{ super() }}{% endblock %}`,
	})
	for name, tc := range map[string]struct{ src, want string }{
		"nested blocks":  {`{% block a %}A{% block b %}B{% endblock %}{% endblock %}`, "AB"},
		"self by name":   {`{% block a %}A{% endblock %}-{{ self.a }}`, "A-A"},
		"super chain":    {`{% extends "mid.html" %}{% block inner %}c{{ super() }}{% endblock %}`, "[ocmi]"},
		"sibling blocks": {`{% block a %}A{% endblock %}{% block b %}{{ self.a }}B{% endblock %}`, "AAB"},
	} {
		t.Run(name, func(t *testing.T) {
			env := gojja2.New(gojja2.WithLoader(loader))
			got, err := mustRender(t, env, tc.src)
			if err != nil {
				t.Fatalf("%s: %v", tc.src, err)
			}
			if got != tc.want {
				t.Errorf("%s = %q, want %q", tc.src, got, tc.want)
			}
		})
	}
}

// TestBlockRecursionRespectsTheConfiguredLimit pins that blocks are counted by
// the same knob as every other template boundary, rather than by a bound of
// their own that a caller cannot reach.
func TestBlockRecursionRespectsTheConfiguredLimit(t *testing.T) {
	var deep strings.Builder
	const levels = 40
	for i := range levels {
		deep.WriteString(`{% block b`)
		deep.WriteString(string(rune('a' + i%26)))
		deep.WriteString(string(rune('a' + i/26)))
		deep.WriteString(` %}`)
	}
	deep.WriteString("x")
	for i := levels - 1; i >= 0; i-- {
		_ = i
		deep.WriteString(`{% endblock %}`)
	}
	src := deep.String()

	if _, err := mustRender(t, gojja2.New(gojja2.WithMaxRecursion(levels+10)), src); err != nil {
		t.Fatalf("%d nested blocks under a limit of %d: %v", levels, levels+10, err)
	}
	err := renderWith(t, context.Background(),
		gojja2.New(gojja2.WithMaxRecursion(levels/2)), src)
	wantRecursionError(t, "nested blocks past the limit", err)
	var e *errs.Error
	if errors.As(err, &e) && e.Limit != levels/2 {
		t.Errorf("Limit = %d, want the configured %d", e.Limit, levels/2)
	}
}
