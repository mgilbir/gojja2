// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package dataflow_test

import (
	"testing"

	"github.com/mgilbir/gojja2"
	"github.com/mgilbir/gojja2/dataflow"
)

// Calls the analysis cannot see into.
//
// The result of `{{ msg.strip() }}` comes out of msg and goes in the document,
// which is not in doubt; what needs care is that a call can also *change* its
// receiver, and a rule that followed only results would miss the flow and
// report a negative that is not true.
func TestCalls(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{
			"a method's result comes from its receiver",
			`{{ msg.strip() }}`,
			"msg:o",
		},
		{
			"and from its arguments",
			`{{ s.replace(a, b) }}`,
			"a:o b:o s:o",
		},
		{
			// The reason calls used to be given up on. This renders
			// nothing, and leaves x inside l.
			"mutation reaches the receiver",
			`{% do l.append(x) %}{{ l }}`,
			"l:o x:o",
		},
		{
			// ... and the negative still has to hold when nothing
			// prints the receiver afterwards.
			"mutation nobody reads afterwards",
			`{% do l.append(x) %}done`,
			"l:- x:-",
		},
		{
			"through a longer path",
			`{% do a.b.append(x) %}{{ a }}`,
			"a:o x:o",
		},
		{
			"a call on something the caller supplies",
			`{{ f(x) }}`,
			"f:o x:o",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, err := gojja2.New(gojja2.WithExtensions("do"))
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
