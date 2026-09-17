// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import "testing"

// TestLoopIsTheIterator pins that `loop` is the iterator the loop is walking
// rather than a description of it.
//
// Consuming it from inside the body advances that walk: `{{ loop|list }}`
// yields the items not yet reached and the enclosing loop then ends with none
// left. Each item arrives as the (value, loop) pair jinja2's
// LoopContextIterator returns, and the loop in the pair is the same object --
// so its repr says where the walk had got to when the pair was rendered, which
// is what makes a filter that renders as it walks differ from one that
// collects first.
//
// It has a length and no indexing: __len__ without __getitem__.
//
// Expectations from CPython jinja2 3.1.6.
func TestLoopIsTheIterator(t *testing.T) {
	env := New()
	for _, tc := range []struct{ src, want string }{
		// Consuming it ends the loop: one pass printed, none left.
		{`{% for i in [1,2,3] %}[{{ loop|list }}]{% endfor %}`,
			"[[(2, <LoopContext 3/3>), (3, <LoopContext 3/3>)]]"},
		// |first takes exactly one, so one pass remains.
		{`{% for i in [1,2,3] %}[{{ loop|first }}]{% endfor %}`,
			"[(2, <LoopContext 2/3>)][]"},
		// |join renders each item as it takes it, so the reprs differ
		// from each other; |list collects first, so they do not.
		{`{% for i in [1,2,3] %}[{{ loop|join(',') }}]{% endfor %}`,
			"[(2, <LoopContext 2/3>),(3, <LoopContext 3/3>)]"},
		{`{% for i in [1,2,3] %}[{{ loop|map('string')|list }}]{% endfor %}`,
			`[['(2, <LoopContext 2/3>)', '(3, <LoopContext 3/3>)']]`},
		// A length, but not a sequence.
		{`{% for i in [1,2,3] %}[{{ loop|length }}{{ loop|count }}{{ loop is sequence }}{{ loop is iterable }}]{% endfor %}`,
			"[33FalseTrue][33FalseTrue][33FalseTrue]"},
		// dict() over it takes the pairs, which is not nothing.
		{`{% for i in [1,2,3] %}[{{ dict(loop, extra=2) }}]{% endfor %}`,
			"[{2: <LoopContext 3/3>, 3: <LoopContext 3/3>, 'extra': 2}]"},
		// The ordinary attributes still answer, untouched.
		{`{% for i in [1,2,3] %}[{{ loop.index }}{{ loop.length }}]{% endfor %}`,
			"[13][23][33]"},
	} {
		got, err := renderVars(t, env, tc.src, nil)
		if err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s\n  = %q\n want %q", tc.src, got, tc.want)
		}
	}
}

// TestLastNeedsSomethingReversible pins that |last goes through reversed(),
// which asks for indexing and not merely for iteration.
//
// A LoopContext knows its length and nothing else, so it is refused rather
// than walked to the end -- which is what jinja2 does, and what tells the two
// filters apart: |first iterates and takes one, |last reverses.
func TestLastNeedsSomethingReversible(t *testing.T) {
	env := New()
	if _, err := renderVars(t, env, `{% for i in [1,2,3] %}{{ loop|last }}{% endfor %}`, nil); err == nil {
		t.Error("loop|last answered; want the TypeError CPython raises")
	} else if got, want := err.Error(), "'LoopContext' object is not reversible"; got != want {
		t.Errorf("error = %q, want %q", got, want)
	}
	// What does reverse still answers.
	for _, tc := range []struct{ expr, want string }{
		{`[1,2]|last`, "2"},
		{`'ab'|last`, "b"},
		{`range(3)|last`, "2"},
		{`{1:2,3:4}|last`, "3"},
	} {
		got, err := renderVars(t, env, "{{ "+tc.expr+" }}", nil)
		if err != nil {
			t.Errorf("%s: %v", tc.expr, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s = %q, want %q", tc.expr, got, tc.want)
		}
	}
}

// TestFilteredLoopWalksTuples pins what a loop with an `if` filter iterates.
//
// jinja2 compiles the filter into a function that unpacks the target and
// yields it straight back -- `for a, b in fiter: if cond: yield (a, b)` -- so
// with a tuple target the loop walks tuples, whatever the source held. Without
// a filter there is no such function and the items are the source's own, which
// is why the same template answers a list one way and a tuple the other.
//
// Expectations from CPython jinja2 3.1.6.
func TestFilteredLoopWalksTuples(t *testing.T) {
	env := New()
	vars := map[string]any{
		"pairs":  []any{[]any{1, 2}, []any{3, 4}},
		"nested": []any{[]any{1, []any{2, 3}}, []any{4, []any{5, 6}}},
	}
	for _, tc := range []struct{ src, want string }{
		{`{% for a, b in pairs if true %}[{{ loop.previtem }}][{{ loop.nextitem }}]{% endfor %}`,
			"[][(3, 4)][(1, 2)][]"},
		{`{% for a, b in pairs %}[{{ loop.previtem }}]{% endfor %}`, "[][[1, 2]]"},
		// One target unpacks nothing, so nothing is repacked.
		{`{% for x in pairs if true %}[{{ loop.previtem }}]{% endfor %}`, "[][[1, 2]]"},
		// A nested target yields the nesting back.
		{`{% for a, (b, c) in nested if true %}[{{ loop.nextitem }}]{% endfor %}`, "[(4, (5, 6))][]"},
		// The filter still sees the unpacked names.
		{`{% for a, b in pairs if a > 1 %}[{{ a }}{{ b }}]{% endfor %}`, "[34]"},
	} {
		got, err := renderVars(t, env, tc.src, vars)
		if err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s\n  = %q\n want %q", tc.src, got, tc.want)
		}
	}
}

// TestURLEncodeRendersAsItWalks pins that |urlencode builds each pair as it
// takes it, which is what `"&".join(f"..." for k, v in items)` does.
//
// Collecting the pairs first is the same answer for an ordinary sequence and a
// different one for an iterator something else is also walking: every pair
// then reports the position the walk ended at rather than the one it was at.
func TestURLEncodeRendersAsItWalks(t *testing.T) {
	got, err := renderVars(t, New(), `{% for i in [1,2,3] %}[{{ loop|urlencode }}]{% endfor %}`, nil)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	want := "[2=%3CLoopContext+2%2F3%3E&3=%3CLoopContext+3%2F3%3E]"
	if got != want {
		t.Errorf("= %q\n want %q", got, want)
	}
}
