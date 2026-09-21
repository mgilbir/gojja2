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
		got := strings.Join(namesDecidingSomething(tmpl.Syntax()), " ")
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
	root := tmpl.Syntax()
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
	add := tmpl.Syntax().Child(syntax.RoleBody).Child(syntax.RoleValue)
	if add == nil || add.Kind != syntax.KindBinOp {
		t.Fatalf("got %v, want the addition as written", add)
	}
}
