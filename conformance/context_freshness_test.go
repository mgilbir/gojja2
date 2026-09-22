// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package conformance_test

import (
	"testing"

	"github.com/mgilbir/gojja2/conformance"
)

// Rendering a case twice must give the same answer both times.
//
// This is a guard on the harness rather than on the engine. A render can mutate
// what it is given -- RenderValues skips the conversion that protects the
// caller, deliberately -- so any harness that hands two renders the same
// [value.Value] lets the first one edit the input of the second. That has gone
// wrong three times here in three different shapes: a context decoded once into
// a struct field, and twice a shallow copy of such a field, which duplicates the
// map and shares the values inside it.
//
// None of the three could be found by looking for a syntax. The first is an
// ordinary field read; the other two are an ordinary map copy. What they have in
// common is only the behaviour, which is what this checks: it renders every case
// twice through the same path the harness uses, and requires the two answers to
// match. Any future arrangement that shares values fails here, whatever it looks
// like.
//
// [Case.Context] is what makes the mistake hard to write in the first place --
// it hands out a fresh decode per call and there is no decoded map to share.
// This is the check that the arrangement is actually working.
func TestRenderingACaseTwiceGivesTheSameAnswer(t *testing.T) {
	root := repoRoot(t)
	paths, err := conformance.Collect(root + "/testdata/corpus")
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("no cases collected")
	}

	var mutators, checked int
	for _, path := range paths {
		c, err := conformance.LoadCase(root+"/testdata/corpus", path)
		if err != nil {
			t.Errorf("%s: load: %v", path, err)
			continue
		}
		first, firstErr := c.Render()
		second, secondErr := c.Render()
		checked++

		if (firstErr == nil) != (secondErr == nil) {
			t.Errorf("%s: first render err=%v, second err=%v -- "+
				"the first render changed the second's input", c.Rel, firstErr, secondErr)
			continue
		}
		if firstErr != nil {
			if firstErr.Error() != secondErr.Error() {
				t.Errorf("%s: two renders, two errors:\n  %v\n  %v",
					c.Rel, firstErr, secondErr)
			}
			continue
		}
		if first != second {
			t.Errorf("%s: two renders disagree:\n  first:  %q\n  second: %q\n"+
				"the first render mutated a value the second was given",
				c.Rel, first, second)
		}

		// And count the cases that make this test worth having: ones
		// whose template really does write to what it was handed. A
		// guard nothing can trip is not a guard.
		if casesThatMutate[c.Rel] {
			mutators++
		}
	}

	// A guard nothing can trip is not a guard, so require every case that
	// gives this test its teeth to still be there.
	if mutators != len(casesThatMutate) {
		t.Errorf("found %d of the %d cases that make this test able to fail; "+
			"one has been removed or renamed, and the test is that much more "+
			"decorative until casesThatMutate is updated (%d checked)",
			mutators, len(casesThatMutate), checked)
	}
	t.Logf("%d cases rendered twice; %d of them write to the context they are given",
		checked, mutators)
}

// casesThatMutate are the corpus cases whose template writes to a value it was
// handed *and* whose second render would therefore differ. They are what gives
// the test above its teeth: with a shared context each of these changes the
// next render's input, and with a fresh one none can.
//
// The list is exactly the set that fails when the fresh decode is removed --
// taken from that run rather than from reading the corpus, because reading it
// got the answer wrong. `{% set d.v %}x{% endset %}` mutates a dict too, but
// writes the same key to the same value every time, so a shared context renders
// identically and proves nothing. Every case here appends, so the mutation
// accumulates and the second render can be told from the first.
//
// Listed rather than detected, because detecting them needs the very machinery
// being tested. If one is removed or renamed the test says so, by finding fewer
// than it expects.
var casesThatMutate = map[string]bool{
	"import/macro_sees_mutation.jj2":         true,
	"include/mutation_between_includes.jj2":  true,
	"include/mutation_in_nested_include.jj2": true,
	"include/mutation_survives.jj2":          true,
	"include/mutation_then_loop.jj2":         true,
	"include/mutation_through_alias.jj2":     true,
	"include/two_mutations.jj2":              true,
}
