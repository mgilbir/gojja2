// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package dataflow_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/mgilbir/gojja2"
	"github.com/mgilbir/gojja2/dataflow"
)

// What the analysis answers, case by case.
//
// conformance/dataflow_test.go grades the whole corpus against the independent
// implementation over jinja2's AST, which is what makes the answers
// trustworthy. This says in one place what they are supposed to mean, so a
// change of intent shows up as an argument about this table rather than as
// drift the corpus happens to allow.
//
// Notation: "o" the value can be printed, "f" it can change the output without
// being printed, "-" neither, "?" no reliable negative.
func TestAnalyze(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		// The distinction this exists for.
		{`{{ a }}`, "a:o"},
		{`{% if flag %}hello{% endif %}`, "flag:f"},
		{`{% if flag %}{{ flag }}{% endif %}`, "flag:of"},

		// Values that reach the output without being named at one.
		{`{% set y = x %}{{ y }}`, "x:o"},
		{`{% set a = b %}{% set c = a %}{{ c }}`, "b:o"},
		{`{% set t %}{{ a }}{% endset %}{{ t }}`, "a:o"},
		{`{% with q = p %}{{ q }}{% endwith %}`, "p:o"},
		{`{% macro m(v) %}{{ v }}{% endmacro %}{{ m(q) }}`, "q:or"},
		{`{% filter upper %}{{ a }}{% endfilter %}`, "a:o"},

		// A conditional's test steers; its branches print.
		{`{{ "yes" if flag else "no" }}`, "flag:f"},
		{`{{ a if flag else b }}`, "a:o b:o flag:f"},
		{`{% set label = "yes" if flag else "no" %}{{ label }}`, "flag:f"},

		// A loop's sequence does both.
		{`{% for x in items %}{{ x }}{% endfor %}`, "items:ofr"},
		{`{% for x in items %}fixed{% endfor %}`, "items:fr"},
		{`{% for x in xs %}{{ loop.index }}{% endfor %}`, "xs:ofr"},

		// The useful negative.
		{`{% set unused = secret %}done`, "secret:-"},

		// jinja2's first-mention rule, which comes from the tree's own
		// scope facts rather than from anything this analysis works out.
		{`{{ x }}{% set x = 1 %}`, "x:o"},
		{`{% for i in [1] %}{{ x }}{% endfor %}{% set x = 1 %}`, ""},
		{`{% if a %}{% set z = 1 %}{% endif %}{{ z }}`, "a:f z:o"},

		// Given rather than asked for.
		{`{{ range(3)|list }}`, ""},
		{`{% macro m() %}{{ caller() }}{% endmacro %}`, ""},

		// A computed lookup reads out of the container whatever the key
		// turns out to be, so the container is printed and the key steers
		// -- it chooses among the values rather than being one of them.
		{`{{ data[key] }}`, "data:or key:fr"},
		{`{{ o|attr(n) }}`, "n:fr o:or"},

		// Where it cannot see, it says so.
		{`{% set ns = namespace(v=0) %}{% for i in xs %}{% set ns.v = i %}{% endfor %}{{ ns.v }}`, "xs:ofr"},
		{`{% include "other.html" %}{{ a }}`, "a:o?"},

		// Not fooled by the constant folder, because the tree is the
		// template as written.
		{`{% if false %}{{ secret }}{% endif %}`, "secret:o"},
		{`{{ xs[[]] }}`, "xs:or"},
	} {
		env, err := gojja2.New()
		if err != nil {
			t.Fatal(err)
		}
		tmpl, err := env.FromString(tc.src)
		if err != nil {
			t.Errorf("%s: compile: %v", tc.src, err)
			continue
		}
		tree := tmpl.Syntax()
		if got := format(dataflow.Analyze(tree).Context(tree)); got != tc.want {
			t.Errorf("%s\n got %q\nwant %q", tc.src, got, tc.want)
		}
	}
}

// The graph is exposed, not just the verdict, because the interesting questions
// are not always "can this be printed". Here: what does the output actually
// depend on, one edge at a time.
func TestDerivesIsAGraph(t *testing.T) {
	env, err := gojja2.New()
	if err != nil {
		t.Fatal(err)
	}
	tmpl, err := env.FromString(`{% set y = x %}{% set z = y %}{{ z }}`)
	if err != nil {
		t.Fatal(err)
	}
	tree := tmpl.Syntax()
	flow := dataflow.Analyze(tree)

	byName := map[string][]string{}
	for sym, deps := range flow.Derives {
		var names []string
		for _, d := range deps {
			names = append(names, d.Name)
		}
		sort.Strings(names)
		byName[sym.Name] = names
	}
	if got := strings.Join(byName["z"], ","); got != "y" {
		t.Errorf("z derives from %q, want y", got)
	}
	if got := strings.Join(byName["y"], ","); got != "x" {
		t.Errorf("y derives from %q, want x", got)
	}
}

func format(m map[string]dataflow.Effect) string {
	out := make([]string, 0, len(m))
	for name, e := range m {
		s := ""
		if e&dataflow.Printed != 0 {
			s += "o"
		}
		if e&dataflow.Steers != 0 {
			s += "f"
		}
		if e&dataflow.Required != 0 {
			s += "r"
		}
		if s == "" {
			s = "-"
		}
		if e&dataflow.Opaque != 0 {
			s += "?"
		}
		out = append(out, name+":"+s)
	}
	sort.Strings(out)
	return strings.Join(out, " ")
}

// Required is about whether there is a document, not what is in it.
//
// Printed and Steers both describe the output. A variable can do neither and
// still stop the render dead, and before this existed such a variable answered
// "-" -- which a caller could reasonably read as "need not be passed".
func TestRequired(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		// Read, and nothing can go wrong: reading a name that is not
		// there gives Undefined rather than an error.
		{`{% set unused = x %}done`, "x:-"},
		// The same read, one operator later.
		{`{% set unused = x + 1 %}done`, "x:r"},
		// Printing does not raise under the default undefined policy, and
		// neither does testing something for truth.
		{`{{ a }}`, "a:o"},
		{`{% if c %}yes{% endif %}`, "c:f"},
		// A filter, a call, a loop and a subscript all can.
		{`{% set unused = x|upper %}done`, "x:r"},
		{`{% set unused = x.strip() %}done`, "x:r"},
		{`{% for i in xs %}{% endfor %}done`, "xs:fr"},
		{`{% set unused = xs[0] %}done`, "xs:r"},
		// A key that cannot be hashed stops the render, and is printed
		// besides.
		{`{{ {k: 1} }}`, "k:or"},
	} {
		env, err := gojja2.New()
		if err != nil {
			t.Fatal(err)
		}
		tmpl, err := env.FromString(tc.src)
		if err != nil {
			t.Errorf("%s: compile: %v", tc.src, err)
			continue
		}
		tree := tmpl.Syntax()
		if got := format(dataflow.Analyze(tree).Context(tree)); got != tc.want {
			t.Errorf("%s\n got %q\nwant %q", tc.src, got, tc.want)
		}
	}
}
