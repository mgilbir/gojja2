// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mgilbir/gojja2"
)

// jinja2's Undefined(hint, obj, name, exc) raises with `exc(message)`. A
// non-callable exc fails before the message is used, and gojja2 reproduces
// that; a *callable* one is not reproduced, because jinja2 calls it at the
// raise and gojja2 has no evaluator there. See docs/divergences.md.
//
// Neither branch can be graded by the corpus: the callable one is a recorded
// divergence, so its cases are skipped, and a plant that removes the
// callability test changes only which *wrong* answer comes out. This pins
// gojja2's own choice, which is that a callable exc leaves the undefined's
// message alone rather than claiming the value is not callable -- a claim that
// would be false about the value as well as about CPython.
func TestCallableExcDoesNotClaimUncallable(t *testing.T) {
	env, err := gojja2.New()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ name, src, want string }{
		{"not callable", "{{ -nope.__class__(1, 2, 3, 4) }}",
			"'int' object is not callable"},
		{"class global", "{{ -nope.__class__(1, 2, 3, range) }}", "1"},
		{"macro", "{% macro m(x) %}{% endmacro %}{{ -nope.__class__(1, 2, 3, m) }}", "1"},
		{"joiner instance", "{{ -nope.__class__(1, 2, 3, joiner()) }}", "1"},
	} {
		t.Run(c.name, func(t *testing.T) {
			tpl, err := env.FromString(c.src)
			if err != nil {
				t.Fatal(err)
			}
			_, err = tpl.RenderString(context.Background(), map[string]any{})
			if err == nil {
				t.Fatalf("%s rendered without error", c.src)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("%s = %q, want it to contain %q", c.src, err, c.want)
			}
			if c.want == "1" && strings.Contains(err.Error(), "not callable") {
				t.Errorf("%s claims a callable value is not callable: %v", c.src, err)
			}
		})
	}
}
