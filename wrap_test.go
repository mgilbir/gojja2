// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import "testing"

// TestWrapAndIndentFollowPythonsOrder pins when |wordwrap and |indent look at
// their arguments, and what they do with the odd ones.
//
// Both hand work to Python code that reads its arguments in a particular
// order, and a template can see that order: a bad argument beside an undefined
// value decides which error comes out. wordwrap resolves `wrapstring.join`
// before it reads the value at all, and passes the width to textwrap, which
// only looks at it once there is a line to wrap. indent sizes its prefix with
// `" " * width` before touching the value.
//
// The width is not converted at the filter's door for the same reason. textwrap
// compares against it -- so a float wraps as its value -- and gives up only
// when a word has to be cut at it, because that is a slice and a slice index
// has to be whole.
//
// Every expectation is CPython jinja2 3.1.6's.
func TestWrapAndIndentFollowPythonsOrder(t *testing.T) {
	env := New()
	vars := map[string]any{"text": "  the quick brown fox jumps over the lazy dog  "}

	for _, tc := range []struct{ expr, want string }{
		{`text|wordwrap(12)`, "  the quick\nbrown fox\njumps over\nthe lazy dog"},
		// splitlines breaks on a carriage return too, and the pieces
		// are rejoined with the wrapstring.
		{`'a\rb\r\nc'|wordwrap(9)`, "a\nb\nc"},
		{`'a\rb\r\nc'|indent(2, true)`, "  a\n  b\n  c"},
		// No lines, so the width is never looked at.
		{`''|wordwrap('z')`, ""},
		// A float width wraps by its value, and only fails when a word
		// has to be sliced at it -- which a width below 1 never does,
		// because that cuts exactly one character.
		{`'ab cd ef'|wordwrap(2.5)`, "ab\ncd\nef"},
		{`'abcd'|wordwrap(0.5)`, "a\nb\nc\nd"},
		{`'abcd'|wordwrap(2.5, false)`, "abcd"},
	} {
		got, err := renderVars(t, env, "{{ "+tc.expr+" }}", vars)
		if err != nil {
			t.Errorf("%s: %v", tc.expr, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s = %q, want %q", tc.expr, got, tc.want)
		}
	}

	for _, tc := range []struct{ expr, want string }{
		// The wrapstring has no join, and that is settled before the
		// undefined value is read.
		{`nope|wordwrap(1, 2, 3)`, "'int' object has no attribute 'join'"},
		// The width is not, so here the undefined wins.
		{`nope|wordwrap('z')`, "'nope' is undefined"},
		{`'ab cd'|wordwrap('z')`, "'<=' not supported between instances of 'str' and 'int'"},
		{`'abcd'|wordwrap(2.5)`, "slice indices must be integers or None or have an __index__ method"},
		// indent sizes the prefix first, so a width that cannot repeat
		// a string outlives an undefined value.
		{`nope|indent(1.5)`, "can't multiply sequence by non-int of type 'float'"},
		{`'a'|indent([1])`, "can't multiply sequence by non-int of type 'list'"},
		{`nope|indent(2)`, "'nope' is undefined"},
	} {
		_, err := renderVars(t, env, "{{ "+tc.expr+" }}", vars)
		if err == nil {
			t.Errorf("%s: no error, want %q", tc.expr, tc.want)
			continue
		}
		if got := err.Error(); got != tc.want {
			t.Errorf("%s = %q, want %q", tc.expr, got, tc.want)
		}
	}
}
