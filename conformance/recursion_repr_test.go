// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package conformance_test

import (
	"strings"
	"testing"

	"github.com/mgilbir/gojja2"
	"github.com/mgilbir/gojja2/conformance"
)

// What `|pprint` does with a value that contains itself.
//
// docs/divergences.md records this case, and says of the whole file that "Each
// one is asserted by a test, so it cannot quietly turn into something else".
// This one had no test, and it had quietly turned into something else -- the
// entry described gojja2 as printing "the same form, with the address of its
// own container", and it does not.
//
// The corpus cannot hold this: CPython's answer carries an id that differs
// between two of its own runs, so a golden would record one run and fail on the
// next. That is exactly why it went unwatched, and why the check belongs here.
func TestPprintOfACyclicValue(t *testing.T) {
	env, err := gojja2.New()
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	const pad = `'a string long enough that pprint will not fit this on one line'`
	for _, tc := range []struct{ name, src string }{
		// CPython: [<Recursion on list with id=NNN>,\n 'a string ...']
		{"list", `{% set l = [] %}{% set _ = l.append(l) %}{% set _ = l.append(` + pad + `) %}{{ l|pprint }}`},
		// CPython: [<Recursion on list with id=NNN>]
		{"bare list", `{% set l = [] %}{% set _ = l.append(l) %}{{ l|pprint }}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmpl, err := env.FromString(tc.src)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			out, err := tmpl.RenderString(t.Context(), nil)
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			if strings.Contains(out, "<Recursion on") {
				t.Fatalf("gojja2 now prints CPython's recursion mark, which it did not "+
					"when this test was written. That is the divergence closing: update "+
					"docs/divergences.md and this test.\n  %s", out)
			}
			if !strings.Contains(out, "[...]") {
				t.Errorf("expected repr's cycle collapse, got:\n  %s", out)
			}
		})
	}
}

// Comparable must screen out CPython's recursion mark.
//
// The id in it is an address, so a template producing one is no more gradable
// than `lipsum()` -- but the regexp that screens the other unstable reprs looks
// for an 0x-prefixed *hex* address, and this id is decimal. Nothing generated
// one until the generator learned to call append, at which point a
// self-referential subject would have reported a divergence per template and
// none of them real.
func TestComparableScreensOutARecursionID(t *testing.T) {
	for _, out := range []string{
		"[<Recursion on list with id=130853217871040>, 'x']",
		"{'self': <Recursion on dict with id=1>}",
		"<Recursion on tuple with id=99999999999999>",
	} {
		if conformance.Comparable(&conformance.OracleResult{OK: true, Output: out}) {
			t.Errorf("graded an output carrying a recursion id: %s", out)
		}
	}
	// ...and it must not screen out everything that merely says "id".
	for _, out := range []string{
		"id=5", "<Recursion on list>", "the id is 12", "[1, 2, 3]",
	} {
		if !conformance.Comparable(&conformance.OracleResult{OK: true, Output: out}) {
			t.Errorf("refused to grade an ordinary output: %s", out)
		}
	}
}
