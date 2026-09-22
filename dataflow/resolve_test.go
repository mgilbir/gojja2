// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package dataflow_test

import (
	"testing"

	"github.com/mgilbir/gojja2"
	"github.com/mgilbir/gojja2/dataflow"
	"github.com/mgilbir/gojja2/syntax"
)

// analyzeIn compiles one of a set of templates and analyses it with the others
// available to be followed -- which is also the whole of what wiring a resolver
// takes.
func analyzeIn(t *testing.T, sources map[string]string, name string) map[string]dataflow.Effect {
	t.Helper()
	env, err := gojja2.New(gojja2.WithLoader(gojja2.DictLoader(sources)))
	if err != nil {
		t.Fatal(err)
	}
	tmpl, err := env.GetTemplate(name)
	if err != nil {
		t.Fatalf("compile %s: %v", name, err)
	}
	tree := tmpl.Syntax()
	flow := dataflow.Analyze(tree, dataflow.WithResolver(func(n string) *syntax.Tree {
		other, err := env.GetTemplate(n)
		if err != nil {
			return nil
		}
		return other.Syntax()
	}))
	return flow.Context(tree)
}

func TestFollowingOtherTemplates(t *testing.T) {
	for _, tc := range []struct {
		name    string
		sources map[string]string
		want    string
	}{
		{
			// The variable is never mentioned here, and the caller still
			// has to supply it. Without following the reference there is
			// no way to know that.
			name:    "include finds what the other template needs",
			sources: map[string]string{"a": `{% include "b" %}`, "b": `{{ deep }}`},
			want:    "deep:o",
		},
		{
			// Handed nothing, so it can print nothing of ours -- and the
			// answer is a real negative rather than a shrug.
			name:    "include without context hands over nothing",
			sources: map[string]string{"a": `{% include "b" without context %}{{ here }}`, "b": `{{ deep }}`},
			want:    "here:o",
		},
		{
			// `{% import %}` does not pass the context by default, so the
			// caller's variables cannot reach the imported template.
			name:    "import does not leak the caller's variables",
			sources: map[string]string{"a": `{% import "b" as m %}{{ here }}`, "b": `{% macro g() %}{{ deep }}{% endmacro %}`},
			want:    "here:o",
		},
		{
			name:    "import with context does",
			sources: map[string]string{"a": `{% import "b" as m with context %}`, "b": `{{ deep }}`},
			want:    "deep:o",
		},
		{
			name:    "extends reaches the parent",
			sources: map[string]string{"a": `{% extends "b" %}`, "b": `{{ fromParent }}`},
			want:    "fromParent:o",
		},
		{
			name: "through two levels",
			sources: map[string]string{
				"a": `{% include "b" %}`,
				"b": `{% include "c" %}`,
				"c": `{{ deepest }}`,
			},
			want: "deepest:o",
		},
		{
			// A template that includes itself terminates rather than
			// recurring for ever; the fixpoint already carries the
			// effects round the cycle.
			name:    "a cycle terminates",
			sources: map[string]string{"a": `{{ v }}{% include "b" %}`, "b": `{% include "a" %}`},
			want:    "v:o",
		},
		{
			// Which template runs is not a static fact, so everything
			// is in play -- and the name itself steers the output
			// without ever being printed, which is the same thing
			// `{% if %}` does with its condition.
			name:    "a computed name steers, and is opaque",
			sources: map[string]string{"a": `{% include page %}{{ v }}`, "b": ``},
			want:    "page:fr? v:o?",
		},
		{
			// Named but not there. It may exist at render time under a
			// different loader, so what it would print is unknown.
			name:    "a missing template is opaque",
			sources: map[string]string{"a": `{% include "gone" %}{{ v }}`},
			want:    "v:o?",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := format(analyzeIn(t, tc.sources, "a")); got != tc.want {
				t.Errorf("\n got %q\nwant %q", got, tc.want)
			}
		})
	}
}

// Without a resolver the analysis cannot follow anything, and says so instead of
// pretending the reference is not there.
func TestWithoutAResolverAReferenceIsOpaque(t *testing.T) {
	env, err := gojja2.New(gojja2.WithLoader(gojja2.DictLoader(map[string]string{
		"a": `{% include "b" %}{{ v }}`,
		"b": `{{ deep }}`,
	})))
	if err != nil {
		t.Fatal(err)
	}
	tmpl, err := env.GetTemplate("a")
	if err != nil {
		t.Fatal(err)
	}
	tree := tmpl.Syntax()
	if got := format(dataflow.Analyze(tree).Context(tree)); got != "v:o?" {
		t.Errorf("got %q, want %q", got, "v:o?")
	}
}

// A template named by an expression rather than a constant. Which template runs
// is not a static fact, so it steers the output and can stop the render, and the
// import cannot be followed.
//
// Mutation testing found this: removing the line that records it broke nothing,
// because every import in the corpus names a constant.
func TestComputedImportName(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"import", `{% import which as m %}{{ m.a() }}`, "which:fr"},
		{"from import", `{% from which import a %}{{ a() }}`, "which:fr"},
		{"include", `{% include which %}`, "which:fr?"},
		{"extends", `{% extends which %}`, "which:fr?"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, err := gojja2.New(gojja2.WithLoader(gojja2.DictLoader(map[string]string{
				"mod": `{% macro a() %}A{% endmacro %}`,
				"t":   tc.src,
			})))
			if err != nil {
				t.Fatal(err)
			}
			tmpl, err := env.GetTemplate("t")
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
