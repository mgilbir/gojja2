// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"context"
	"testing"

	"github.com/mgilbir/gojja2/value"
)

// TestSliceAsksTheBaseFirst pins the order a subscript decides in.
//
// Python builds the slice object out of whatever the operands are --
// slice(1.5, None) is a perfectly good object -- and leaves the complaining to
// __getitem__. So a base with no subscript at all says so before the operands
// are judged, a dict calls the slice unhashable, and only a sequence gets as
// far as minding that 1.5 is not an index.
//
// Expectations from CPython jinja2 3.1.6.
func TestSliceAsksTheBaseFirst(t *testing.T) {
	env := mustNew()
	for _, tc := range []struct{ src, want string }{
		{`{% set q = 'abcdef' %}{{ q[1.5:] }}`,
			"slice indices must be integers or None or have an __index__ method"},
		{`{% set q = {'a': 1} %}{{ q['x':] }}`, "slice('x', None, None)"},
		{`{% set q = 3 %}{{ q[1.5:] }}`, "'int' object is not subscriptable"},
		{`{% set q = 3.5 %}{{ q['a':] }}`, "'float' object is not subscriptable"},
		// A step of zero is a ValueError, which getitem does not
		// swallow -- so it survives being folded as well as not being.
		{`{{ 'abcdef'[::0] }}`, "slice step cannot be zero"},
		{`{% set q = 'abcdef' %}{{ q[::0] }}`, "slice step cannot be zero"},
	} {
		_, err := renderVars(t, env, tc.src, nil)
		if err == nil {
			t.Errorf("%s: no error, want %q", tc.src, tc.want)
			continue
		}
		if got := err.Error(); got != tc.want {
			t.Errorf("%s = %q, want %q", tc.src, got, tc.want)
		}
	}
}

// TestFoldedSliceMatchesTheRunTimeOne is the reason the two share an
// implementation: they had one each, and only the evaluator's knew that a
// tuple subclass slices as a tuple. A constant expression took the other path.
func TestFoldedSliceMatchesTheRunTimeOne(t *testing.T) {
	env := mustNew()
	vars := map[string]any{"users": []any{map[string]any{"city": "Lisbon"}}}
	for _, tc := range []struct{ src, want string }{
		{`{{ ([2.675]|groupby('age')|list|max)[::2] }}`, "(Undefined,)"},
		{`{% set g = [2.675]|groupby('age')|list|max %}{{ g[::2] }}`, "(Undefined,)"},
		{`{{ (users|groupby('city')|first)[::2] }}`, "('Lisbon',)"},
	} {
		got, err := renderVars(t, env, tc.src, vars)
		if err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s = %q, want %q", tc.src, got, tc.want)
		}
	}
}

// TestSubscriptEdges: two rules a 3,531-case slicing sweep against CPython
// turned up.
//
// A slice index too big for the machine is still an integer, and CPython clamps
// it -- `"abcde"[:2**70]` is the whole string. gojja2 read it as "does not fit"
// and answered an empty slice on a string and a TypeError on a list, neither of
// which is what a template asked for.
//
// And jinja2's getitem catches TypeError and LookupError alike, so a key the
// container cannot take at all renders as nothing rather than raising:
// `{{ xs[none] }}` is empty, and so is `{{ d[[]] }}`, where an unhashable key
// would otherwise be an error.
func TestSubscriptEdges(t *testing.T) {
	env := mustNew()
	ctx := map[string]any{
		"xs": []any{1, 2, 3},
		"d":  map[string]any{"a": 1},
	}
	for _, tc := range []struct{ src, want string }{
		// A huge bound clamps, in either direction.
		{`{{ "abcde"[:2**70] }}|{{ "abcde"[1:2**70] }}`, "abcde|bcde"},
		{`{{ "abcde"[-(2**70):2] }}|{{ "abcde"[2**70:] }}`, "ab|"},
		{`{{ xs[:2**70] }}|{{ xs[:-(2**70)] }}`, "[1, 2, 3]|[]"},
		{`{{ "abcde"[::2**70] }}|{{ "abcde"[::-(2**70)] }}`, "a|e"},
		// And a step at the edge of int64 no longer overflows the
		// count: `(span + stride - 1) / stride` went negative and
		// reached make(), which panicked.
		{`{{ [1,2,3][::9223372036854775807] }}`, "[1]"},
		{`{{ [1,2,3][::-9223372036854775808] }}`, "[3]"},
		{`{{ "abc"[::9223372036854775807] }}`, "a"},
		// A key that is not an index at all is an undefined, which
		// renders as nothing.
		{`[{{ xs[none] }}][{{ xs[1.5] }}][{{ xs[[]] }}]`, "[][][]"},
		{`[{{ d[[]] }}][{{ d[none] }}]`, "[][]"},
		// And one that is an index but out of range still is.
		{`[{{ xs[9] }}][{{ "ab"[9] }}]`, "[][]"},
		// A key that works still works.
		{`{{ xs[0] }}|{{ xs[-1] }}|{{ d["a"] }}`, "1|3|1"},
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

	// A strict undefined says what was missing, which is how the message
	// stays visible when the class asks for it.
	strict := mustNew(WithUndefined(value.UndefinedStrict))
	tmpl, err := strict.FromString(`{{ xs[none] }}`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if _, err := tmpl.RenderString(context.Background(), ctx); err == nil ||
		err.Error() != "list object has no element None" {
		t.Errorf("strict subscript: got %v", err)
	}
}
