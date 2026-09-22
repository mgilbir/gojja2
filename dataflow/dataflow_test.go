// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package dataflow_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/mgilbir/gojja2"
	"github.com/mgilbir/gojja2/dataflow"
	"github.com/mgilbir/gojja2/syntax"
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

		// A conditional's test steers; its branches print. It is also
		// Required, and always: the test picks which value the expression
		// yields, and what happens to that value afterwards is not visible
		// from the conditional -- `{{ f + (xs if c else 1) }}` fails on one
		// branch and not the other. Over-reported here, never under-reported
		// there.
		{`{{ "yes" if flag else "no" }}`, "flag:fr"},
		{`{{ a if flag else b }}`, "a:o b:o flag:fr"},
		{`{% set label = "yes" if flag else "no" %}{{ label }}`, "flag:fr"},

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

// A condition decides whether the code it guards runs, so it decides whether
// that code's failures happen. The fuzzer found this by rendering: a template
// whose `{% if %}` guarded a failing expression succeeded or died depending on a
// variable the analysis had reported as unable to break it.
func TestGuardingConditionsAreRequired(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		// Nothing under the condition can fail, so neither can it.
		{`{% if c %}plain{% else %}text{% endif %}`, "c:f"},
		// A filter can, and c decides whether it runs.
		{`{% if c %}{{ 1|upper }}{% endif %}`, "c:fr"},
		// From the else side too.
		{`{% if c %}plain{% else %}{{ 1|upper }}{% endif %}`, "c:fr"},
		// An elif does not own the else it shares: `b` decides whether the
		// else arm runs, so the failure in it is b's doing as well as a's.
		{`{% if a %}x{% elif b %}y{% else %}{{ 1|upper }}{% endif %}`, "a:fr b:fr"},
		// ...and an `{% if %}` written inside an `{% else %}` does own it,
		// so the outer condition is not answerable for what the inner one
		// guards. The two spellings render the same and are not the same
		// question.
		{`{% if a %}x{% else %}{% if b %}y{% else %}z{% endif %}{% endif %}`, "a:f b:f"},
		// A loop's own filter guards the body in the same way.
		{`{% for i in xs if p %}{{ 1|upper }}{% endfor %}`, "p:fr xs:fr"},
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

// The tree type is public, so a caller can hand Analyze something the parser
// would never build. An assignment to a target that is not a name, a namespace
// field or an unpacking is the case the walk has a default for, and the default
// has to be the safe one: whatever was being assigned becomes opaque rather than
// silently losing its way.
//
// Mutation testing found this line untested, for the good reason that gojja2's
// own parser cannot produce it. A caller's tree is not gojja2's own parser.
func TestAnalyzeToleratesATargetItDoesNotKnow(t *testing.T) {
	name := &syntax.Node{Kind: syntax.KindName, Attrs: map[string]any{"name": "x"}}
	odd := &syntax.Node{Kind: syntax.KindConst, Attrs: map[string]any{"value": 1}}
	assign := &syntax.Node{Kind: syntax.KindAssign, Edges: []syntax.Edge{
		{Role: syntax.RoleTarget, Node: odd},
		{Role: syntax.RoleValue, Node: name},
	}}
	root := &syntax.Node{Kind: syntax.KindTemplate, Edges: []syntax.Edge{
		{Role: syntax.RoleBody, Node: assign},
	}}
	sym := &syntax.Symbol{Name: "x", Kind: syntax.SymContext}
	tree := &syntax.Tree{Root: root, Info: &syntax.Info{
		Defs:    map[*syntax.Node]*syntax.Symbol{},
		Uses:    map[*syntax.Node]*syntax.Symbol{name: sym},
		Scopes:  map[*syntax.Node][]*syntax.Symbol{root: nil},
		Context: map[string]*syntax.Symbol{"x": sym},
	}}

	got := dataflow.Analyze(tree).Context(tree)["x"]
	if got&dataflow.Opaque == 0 {
		t.Errorf("assigning to a target the walk does not know left %q as %v; "+
			"it has to be opaque, because where the value went is not known",
			"x", got)
	}
}
