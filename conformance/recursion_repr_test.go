// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package conformance_test

import (
	"regexp"
	"testing"

	"github.com/mgilbir/gojja2"
	"github.com/mgilbir/gojja2/conformance"
)

// `|pprint` of a value that contains itself: the form is exact, the id cannot be.
//
// docs/divergences.md records this case, and says of the whole file that "Each
// one is asserted by a test, so it cannot quietly turn into something else".
// This one had none, and had quietly turned into something else -- gojja2 was
// printing repr's `[...]` collapse where CPython prints the mark.
//
// The corpus cannot hold it: CPython's answer carries an id that differs
// between two of its own runs, so a golden would record one run and fail on the
// next. That is why it went unwatched, and why the check belongs here.
func TestPprintOfACyclicValue(t *testing.T) {
	env, err := gojja2.New()
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	const pad = `'a string long enough that pprint will not fit this on one line'`
	for _, tc := range []struct{ name, src, want string }{
		// Each `want` is CPython's own answer with the id blanked.
		{"list",
			`{% set l = [] %}{% set _ = l.append(l) %}{% set _ = l.append(` + pad + `) %}{{ l|pprint }}`,
			"[<Recursion on list with id=N>,\n 'a string long enough that pprint will not fit this on one line']"},
		{"bare list",
			`{% set l = [] %}{% set _ = l.append(l) %}{{ l|pprint }}`,
			"[<Recursion on list with id=N>]"},
		{"dict",
			`{% set d = {} %}{% set _ = d.update({'self': d}) %}{% set _ = d.update({'pad': ` + pad + `}) %}{{ d|pprint }}`,
			"{'pad': 'a string long enough that pprint will not fit this on one line',\n 'self': <Recursion on dict with id=N>}"},
		{"indirect",
			`{% set i = [] %}{% set o = [i] %}{% set _ = i.append(o) %}{% set _ = o.append(` + pad + `) %}{{ o|pprint }}`,
			"[[<Recursion on list with id=N>],\n 'a string long enough that pprint will not fit this on one line']"},
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
			ids := regexp.MustCompile(`id=\d+`)
			if got := ids.ReplaceAllString(out, "id=N"); got != tc.want {
				t.Errorf("pprint of a cyclic value:\n got %q\nwant %q", got, tc.want)
			}
			// The id really is an address, which is the divergence: it
			// is not reproducible in CPython either, so nothing here
			// can assert a value for it.
			if !ids.MatchString(out) {
				t.Errorf("no id in %q", out)
			}
		})
	}
}

// An acyclic value must lay out exactly as it did before pprint learned about
// cycles, because the recursion-aware repr is only reached for a cyclic one.
func TestPprintOfAnAcyclicValueIsUnchanged(t *testing.T) {
	env, err := gojja2.New()
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	for _, tc := range []struct{ src, want string }{
		{`{{ {'b': 2, 'a': 1, 'C': 3}|pprint }}`, `{'C': 3, 'a': 1, 'b': 2}`},
		{`{{ {'x': {'y': [1, 2]}}|pprint }}`, `{'x': {'y': [1, 2]}}`},
		// The same list twice is shared, not cyclic, and expands both times.
		{`{% set x = [1] %}{{ [x, x]|pprint }}`, `[[1], [1]]`},
		{`{{ [1, 2, 3]|pprint }}`, `[1, 2, 3]`},
		{`{{ (1,)|pprint }}`, `(1,)`},
	} {
		tmpl, err := env.FromString(tc.src)
		if err != nil {
			t.Fatalf("compile %s: %v", tc.src, err)
		}
		out, err := tmpl.RenderString(t.Context(), nil)
		if err != nil {
			t.Fatalf("render %s: %v", tc.src, err)
		}
		if out != tc.want {
			t.Errorf("%s\n got %q\nwant %q", tc.src, out, tc.want)
		}
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
