// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import "testing"

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
	env := New()
	for _, tc := range []struct{ src, want string }{
		{`{% set q = 'abcdef' %}{{ q[1.5:] }}`,
			"slice indices must be integers or None or have an __index__ method"},
		{`{% set q = {'a': 1} %}{{ q['x':] }}`, "unhashable type: 'slice'"},
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
	env := New()
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
