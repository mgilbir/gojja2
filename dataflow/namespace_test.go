// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package dataflow_test

import (
	"testing"

	"github.com/mgilbir/gojja2"
	"github.com/mgilbir/gojja2/dataflow"
)

// Namespaces exist because a `{% set %}` inside a loop does not escape it, which
// makes them the idiom for accumulate-then-print -- the case someone most wants
// traced, and the one that used to answer "might reach the output" about a
// variable that plainly does.
func TestNamespaceFieldsAreFollowed(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{
			"carried out of a loop",
			`{% set ns = namespace(total=0) %}
			 {% for row in rows %}{% set ns.total = ns.total + row %}{% endfor %}
			 {{ ns.total }}`,
			"rows:ofr",
		},
		{
			// Field by field, so a variable that only ever reaches an
			// unprinted field gets a real negative.
			"fields are separate",
			`{% set ns = namespace(a=p, b=q) %}{{ ns.a }}`,
			"p:o q:-",
		},
		{
			"a field nothing prints cannot affect the output",
			`{% set ns = namespace() %}{% set ns.x = secret %}done`,
			"secret:-",
		},
		{
			// Two names for one object, and a write through either
			// reaches the other. Following that is alias analysis;
			// this gives up instead, which is the safe direction.
			"an alias gives up",
			`{% set ns = namespace(v=0) %}{% set other = ns %}{% set ns.v = x %}{{ ns.v }}`,
			"x:o?",
		},
		{
			"handing it to a macro gives up",
			`{% macro m(o) %}{{ o.v }}{% endmacro %}
			 {% set ns = namespace(v=x) %}{{ m(ns) }}`,
			"x:or?",
		},
		{
			"fields nobody can name give up",
			`{% set ns = namespace(**d) %}{% set ns.v = x %}{{ ns.v }}`,
			"d:or? x:o?",
		},
		{
			// Not created here, so there is no telling what it is or
			// what else touches it.
			"one that was passed in is opaque",
			`{% set given.v = x %}{{ given.v }}`,
			"given:or? x:or?",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, err := gojja2.New()
			if err != nil {
				t.Fatal(err)
			}
			tmpl, err := env.FromString(tc.src)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			tree := tmpl.Syntax()
			if got := format(dataflow.Analyze(tree).Context(tree)); got != tc.want {
				t.Errorf("\n got %q\nwant %q", got, tc.want)
			}
		})
	}
}
