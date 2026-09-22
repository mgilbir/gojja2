// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package conformance_test

import (
	"testing"

	"github.com/mgilbir/gojja2"
	"github.com/mgilbir/gojja2/conformance"
)

// A set prints in sorted order, every time.
//
// CPython's does not print in any order at all: a set is unordered and its repr
// follows the hash table, which string hashing randomises per process. Three
// runs of the same expression on the same four keys gave
//
//	{'b', 'delta', 'a', 'c'}
//	{'c', 'delta', 'a', 'b'}
//	{'a', 'b', 'delta', 'c'}
//
// so there is nothing to record a golden against. The corpus holds the cases
// with one element or none, whose spelling is fixed either way; this is the rest
// of the claim, and it is about gojja2 alone because CPython makes no claim to
// compare against. See docs/divergences.md.
func TestSetOrderIsSorted(t *testing.T) {
	env, err := gojja2.New()
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	const src = `{% set d = {'b':2,'a':1,'c':3,'delta':4} %}{{ d.keys() - [] }}`
	tmpl, err := env.FromString(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	const want = "{'a', 'b', 'c', 'delta'}"
	for i := range 5 {
		out, err := tmpl.RenderString(t.Context(), nil)
		if err != nil {
			t.Fatalf("render %d: %v", i, err)
		}
		if out != want {
			t.Errorf("render %d = %q, want %q", i, out, want)
		}
	}
}

// ...and the differential must not try to grade one.
//
// The generator can write `d.keys() - xs` now, so without this a soak would
// report a divergence per template and none of them real -- the same hole the
// recursion id went through before it was screened.
func TestComparableScreensOutAMultiElementSet(t *testing.T) {
	for _, tc := range []struct {
		out      string
		gradable bool
	}{
		// Unstable: more than one element, no colon at the top level.
		{"{'a', 'b'}", false},
		{"{('b', 2), ('a', 1)}", false},
		{"[{'a', 'b'}]", false},
		// Stable, and must keep being graded.
		{"{'a'}", true},
		{"{1}", true},
		{"set()", true},
		{"{('b', 2)}", true}, // one element; the comma is inside the tuple
		{"{'a': 1, 'b': 2}", true},
		{"{}", true},
		{"[1, 2]", true},
		{"plain text", true},
	} {
		got := conformance.Comparable(&conformance.OracleResult{OK: true, Output: tc.out})
		if got != tc.gradable {
			t.Errorf("Comparable(%q) = %v, want %v", tc.out, got, tc.gradable)
		}
	}
}
