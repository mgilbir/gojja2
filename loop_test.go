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
