// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package syntax_test

import (
	"fmt"
	"log"
	"sort"

	"github.com/mgilbir/gojja2"
	"github.com/mgilbir/gojja2/syntax"
)

// A query that needs no knowledge of the node set: the edge label says what a
// child is to its parent, so a condition is a condition wherever it appears.
func ExampleWalk() {
	env, err := gojja2.New()
	if err != nil {
		log.Fatal(err)
	}
	tmpl, err := env.FromString(
		`{% if admin %}{{ name }}{% endif %}{{ "a" if beta else "b" }}`)
	if err != nil {
		log.Fatal(err)
	}

	var deciding []string
	syntax.Walk(tmpl.Syntax().Root, func(n *syntax.Node, role syntax.Role) bool {
		if role == syntax.RoleTest && n.Kind == syntax.KindName {
			deciding = append(deciding, n.Attr("name"))
		}
		return true
	})
	sort.Strings(deciding)
	fmt.Println(deciding)
	// Output: [admin beta]
}

// The scope facts say which names the caller has to supply, which a tree alone
// cannot: jinja2 decides per frame on a name's first mention.
func ExampleInfo() {
	env, err := gojja2.New()
	if err != nil {
		log.Fatal(err)
	}
	for _, src := range []string{
		`{{ x }}{% set x = 1 %}`,
		`{% for i in [1] %}{{ x }}{% endfor %}{% set x = 1 %}`,
	} {
		tmpl, err := env.FromString(src)
		if err != nil {
			log.Fatal(err)
		}
		var wanted []string
		for name, sym := range tmpl.Syntax().Info.Context {
			if sym.Kind == syntax.SymContext {
				wanted = append(wanted, name)
			}
		}
		sort.Strings(wanted)
		fmt.Printf("%-52s %v\n", src, wanted)
	}
	// Output:
	// {{ x }}{% set x = 1 %}                               [x]
	// {% for i in [1] %}{{ x }}{% endfor %}{% set x = 1 %} []
}
