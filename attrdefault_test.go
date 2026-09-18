// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"context"
	"testing"
)

// TestAttributeDefaultOfNoneIsNoDefault: make_attrgetter substitutes the
// default with `if default is not None`, so an explicit None is no default at
// all -- the undefined stays, and whatever the undefined does next is what
// happens.
//
// gojja2 substituted it whenever the argument was written, which turned
// `{{ xs|groupby("nope", none) }}` from the attribute error jinja2 raises into
// a comparison of two Nones, and `{{ xs|map(attribute="nope", default=none) }}`
// from a list of undefineds into a list of Nones.
func TestAttributeDefaultOfNoneIsNoDefault(t *testing.T) {
	env := New()
	ctx := map[string]any{"rows": []any{
		map[string]any{"a": 1},
		map[string]any{},
	}}
	for _, tc := range []struct{ src, want string }{
		{`{{ rows|groupby("a", none)|list }}`, "'dict object' has no attribute 'a'"},
		{`{{ [1,2]|groupby(1, none)|list }}`, "int object has no element 1"},
		// Without the argument at all, the same thing happens.
		{`{{ rows|groupby("a")|list }}`, "'dict object' has no attribute 'a'"},
	} {
		tmpl, err := env.FromString(tc.src)
		if err != nil {
			t.Fatalf("compile %q: %v", tc.src, err)
		}
		out, err := tmpl.RenderString(context.Background(), ctx)
		if err == nil {
			t.Errorf("%s: rendered %q, want %q", tc.src, out, tc.want)
			continue
		}
		if err.Error() != tc.want {
			t.Errorf("%s\n got %q\nwant %q", tc.src, err.Error(), tc.want)
		}
	}

	for _, tc := range []struct{ src, want string }{
		// A default that is not None still stands in, and 0 and "" are
		// not None.
		{`{{ rows|groupby("a", 0)|list }}`, "[(0, [{}]), (1, [{'a': 1}])]"},
		{`{{ [1,2]|map(attribute="x", default="d")|list }}`, "['d', 'd']"},
		// A None default leaves the undefined in place, which is the
		// same list as no default at all.
		{`{{ [1,2]|map(attribute="x", default=none)|list }}`, "[Undefined, Undefined]"},
		{`{{ [1,2]|map(attribute="x")|list }}`, "[Undefined, Undefined]"},
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
}
