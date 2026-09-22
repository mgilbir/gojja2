// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package syntax_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/mgilbir/gojja2"
	"github.com/mgilbir/gojja2/syntax"
)

// The point of exposing the tree is that a caller can ask a question nobody
// anticipated. This is one, written the way a caller would write it: which
// names appear in a position that decides something, rather than in one that
// contributes a value.
//
// It is eleven lines and needs no knowledge of the node set, because the edge
// labels carry the context. Asking the same thing of a tree where a condition
// is "the first field of an If and the third of a For and the test of a
// CondExpr" means knowing every node kind before writing a line.
func namesDecidingSomething(n *syntax.Node) []string {
	seen := map[string]bool{}
	syntax.Walk(n, func(n *syntax.Node, role syntax.Role) bool {
		if role != syntax.RoleTest {
			return true
		}
		syntax.Walk(n, func(n *syntax.Node, _ syntax.Role) bool {
			if n.Kind == syntax.KindName {
				seen[n.Attr("name")] = true
			}
			return true
		})
		return true
	})
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestWalkCarriesTheEdgeLabel(t *testing.T) {
	env, err := gojja2.New()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ src, want string }{
		{`{% if a %}{{ b }}{% endif %}`, "a"},
		{`{{ b if a else c }}`, "a"},
		{`{% for x in xs if p %}{{ x }}{% endfor %}`, "p"},
		{`{{ a }}{{ b }}`, ""},
		{`{% if a %}{% if b %}x{% endif %}{% endif %}`, "a b"},
	} {
		tmpl, err := env.FromString(tc.src)
		if err != nil {
			t.Fatalf("%s: %v", tc.src, err)
		}
		got := strings.Join(namesDecidingSomething(tmpl.Syntax().Root), " ")
		if got != tc.want {
			t.Errorf("%s\n got %q\nwant %q", tc.src, got, tc.want)
		}
	}
}

// A template's structure is reachable, labelled, and shaped the way the
// vocabulary says rather than the way either parser happens to build it.
func TestSyntaxShape(t *testing.T) {
	env, err := gojja2.New()
	if err != nil {
		t.Fatal(err)
	}
	tmpl, err := env.FromString(`{% for x in xs %}{{ x + 1 }}{% endfor %}`)
	if err != nil {
		t.Fatal(err)
	}
	root := tmpl.Syntax().Root
	if root.Kind != syntax.KindTemplate {
		t.Fatalf("root is %q, want %q", root.Kind, syntax.KindTemplate)
	}
	loop := root.Child(syntax.RoleBody)
	if loop == nil || loop.Kind != syntax.KindFor {
		t.Fatalf("first statement is %v, want a for", loop)
	}
	if target := loop.Child(syntax.RoleTarget); target.Attr("name") != "x" {
		t.Errorf("loop target is %q, want x", target.Attr("name"))
	}
	if iter := loop.Child(syntax.RoleIter); iter.Attr("name") != "xs" {
		t.Errorf("loop sequence is %q, want xs", iter.Attr("name"))
	}
	// jinja2 would call this an Add; the vocabulary calls every binary
	// operator a binop and puts the operator on it.
	add := loop.Child(syntax.RoleBody).Child(syntax.RoleValue)
	if add.Kind != syntax.KindBinOp || add.Attr("op") != "add" {
		t.Errorf("the body's expression is %q/%q, want binop/add", add.Kind, add.Attr("op"))
	}
	if left := add.Child(syntax.RoleLeft); left.Attr("name") != "x" {
		t.Errorf("left operand is %q, want x", left.Attr("name"))
	}
}

// Syntax is the template as written. The engine's own tree has been through the
// constant folder, and a query over that would be answering questions about the
// optimizer.
func TestSyntaxIsNotFolded(t *testing.T) {
	env, err := gojja2.New()
	if err != nil {
		t.Fatal(err)
	}
	tmpl, err := env.FromString(`{{ 1 + 1 }}`)
	if err != nil {
		t.Fatal(err)
	}
	add := tmpl.Syntax().Root.Child(syntax.RoleBody).Child(syntax.RoleValue)
	if add == nil || add.Kind != syntax.KindBinOp {
		t.Fatalf("got %v, want the addition as written", add)
	}
}

// The scope facts answer the questions a tree alone cannot, and the first one
// is usually "what does this template want from me".
func TestInfoNamesWhatTheCallerMustSupply(t *testing.T) {
	env, err := gojja2.New()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ src, want string }{
		{`{{ a }}{{ b }}`, "a b"},
		// Bound by the template, so not the caller's.
		{`{% set a = 1 %}{{ a }}`, ""},
		{`{% for x in xs %}{{ x }}{{ loop.index }}{% endfor %}`, "xs"},
		{`{% macro m(p) %}{{ p }}{{ caller() }}{% endmacro %}{{ m(q) }}`, "q"},
		// The environment supplies these, so the caller is not being asked.
		{`{{ range(3)|list }}{{ n }}`, "n"},
		// jinja2's first-mention rule, which is the whole reason these facts
		// cannot be worked out from the tree alone.
		{`{{ x }}{% set x = 1 %}`, "x"},
		{`{% for i in [1] %}{{ x }}{% endfor %}{% set x = 1 %}`, ""},
	} {
		tmpl, err := env.FromString(tc.src)
		if err != nil {
			t.Fatalf("%s: %v", tc.src, err)
		}
		var names []string
		for name, sym := range tmpl.Syntax().Info.Context {
			if sym.Kind == syntax.SymContext {
				names = append(names, name)
			}
		}
		sort.Strings(names)
		if got := strings.Join(names, " "); got != tc.want {
			t.Errorf("%s\n got %q\nwant %q", tc.src, got, tc.want)
		}
	}
}

// Two occurrences of a name refer to the same symbol exactly when they touch
// the same storage, which is what a rename or a dataflow pass is really asking.
func TestInfoDistinguishesShadowedNames(t *testing.T) {
	env, err := gojja2.New()
	if err != nil {
		t.Fatal(err)
	}
	tmpl, err := env.FromString(`{{ x }}{% for x in xs %}{{ x }}{% endfor %}`)
	if err != nil {
		t.Fatal(err)
	}
	tree := tmpl.Syntax()

	var xs []*syntax.Symbol
	syntax.Walk(tree.Root, func(n *syntax.Node, _ syntax.Role) bool {
		if n.Kind == syntax.KindName && n.Attr("name") == "x" {
			xs = append(xs, tree.Info.Symbol(n))
		}
		return true
	})
	if len(xs) != 3 {
		t.Fatalf("found %d occurrences of x, want 3", len(xs))
	}
	if xs[0] == xs[1] {
		t.Error("the outer x and the loop target are the same symbol")
	}
	if xs[1] != xs[2] {
		t.Error("the loop target and the read inside the loop are different symbols")
	}
	if xs[0].Kind != syntax.SymContext {
		t.Errorf("the outer x is %q, want %q", xs[0].Kind, syntax.SymContext)
	}
	if xs[1].Kind != syntax.SymTarget {
		t.Errorf("the loop target is %q, want %q", xs[1].Kind, syntax.SymTarget)
	}
	if xs[1].Scope == nil || xs[1].Scope.Kind != syntax.KindFor {
		t.Errorf("the loop target is owned by %v, want the loop", xs[1].Scope)
	}
}

// Attr answers for a node that has no attributes, and for a nil one, because a
// query walking a tree should not have to check first.
func TestAttrIsSafe(t *testing.T) {
	var none *syntax.Node
	if got := none.Attr("name"); got != "" {
		t.Errorf("a nil node answered %q", got)
	}
	bare := &syntax.Node{Kind: syntax.KindBreak}
	if got := bare.Attr("name"); got != "" {
		t.Errorf("a node with no attributes answered %q", got)
	}
	if got := (&syntax.Node{Kind: syntax.KindConst,
		Attrs: map[string]any{"value": 1}}).Attr("value"); got != "" {
		t.Errorf("a non-string attribute answered %q, want the empty string", got)
	}
}
