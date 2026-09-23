// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package conformance_test

import (
	"math/rand/v2"
	"sort"
	"strings"
	"testing"

	"github.com/mgilbir/gojja2"
	"github.com/mgilbir/gojja2/conformance"
	"github.com/mgilbir/gojja2/dataflow"
	"github.com/mgilbir/gojja2/syntax"
)

// Properties that hold whatever the answer is.
//
// Every other check compares the analysis to something: to jinja2, to a render,
// to a recorded golden. Those cannot catch a mistake shared by both sides, and
// this stack has had several. These need no second opinion at all -- they say
// what must be true of the analysis on its own terms, so a shared mistake has
// nowhere to hide.

// Analysing the same template twice must give the same answer.
//
// It sounds like nothing to check and it is not: the frame rule for `{% if %}`
// once iterated a map, so which names a frame claimed came out in a different
// order on different runs, and a test that fails one time in five is worse than
// one that fails.
func TestAnalysisIsDeterministic(t *testing.T) {
	forGenerated(t, 2000, 20260923, func(t *testing.T, tmpl *gojja2.Template, _ string) {
		first := describe(tmpl)
		for range 3 {
			if got := describe(tmpl); got != first {
				t.Errorf("two runs, two answers:\n  %s\n  %s", first, got)
				return
			}
		}
	})
}

// Renaming the variables must rename the answer and change nothing else.
//
// The analysis is allowed to care about a handful of names -- `namespace`,
// `loop`, `caller`, the environment's globals -- and it must not care about any
// others. Renaming is done on the tree rather than in the source, so there is no
// chance of rewriting the inside of a string literal and comparing two templates
// that were never the same one.
func TestRenamingVariablesRenamesTheAnswer(t *testing.T) {
	forGenerated(t, 2000, 20260924, func(t *testing.T, tmpl *gojja2.Template, src string) {
		tree := tmpl.Syntax()
		before := dataflow.Analyze(tree).Context(tree)

		renamed := renameTree(tmpl.Syntax())
		after := dataflow.Analyze(renamed).Context(renamed)

		want := make(map[string]dataflow.Effect, len(before))
		for name, e := range before {
			want[name+"_r"] = e
		}
		if got, expected := show(after), show(want); got != expected {
			t.Errorf("renaming changed the answer:\n  before: %s\n  after:  %s\n  %q",
				expected, got, src)
		}
	})
}

// renameTree copies a tree with every variable renamed, except the ones the
// analysis is entitled to recognise: the environment's globals, and the names a
// construct supplies rather than the caller.
func renameTree(t *syntax.Tree) *syntax.Tree {
	keep := func(s *syntax.Symbol) bool {
		return s.Kind == syntax.SymGlobal || s.Kind == syntax.SymProvided
	}
	syms := map[*syntax.Symbol]*syntax.Symbol{}
	copySym := func(s *syntax.Symbol) *syntax.Symbol {
		if s == nil {
			return nil
		}
		if c, ok := syms[s]; ok {
			return c
		}
		c := &syntax.Symbol{Name: s.Name, Kind: s.Kind}
		if !keep(s) {
			c.Name = s.Name + "_r"
		}
		syms[s] = c
		return c
	}

	nodes := map[*syntax.Node]*syntax.Node{}
	var clone func(*syntax.Node) *syntax.Node
	clone = func(n *syntax.Node) *syntax.Node {
		if n == nil {
			return nil
		}
		c := &syntax.Node{Kind: n.Kind, Line: n.Line}
		if n.Attrs != nil {
			c.Attrs = make(map[string]any, len(n.Attrs))
			for k, v := range n.Attrs {
				c.Attrs[k] = v
			}
		}
		nodes[n] = c
		for _, e := range n.Edges {
			c.Edges = append(c.Edges, syntax.Edge{Role: e.Role, Node: clone(e.Node)})
		}
		return c
	}
	root := clone(t.Root)

	rename := func(from map[*syntax.Node]*syntax.Symbol, to map[*syntax.Node]*syntax.Symbol) {
		for n, s := range from {
			c := copySym(s)
			to[nodes[n]] = c
			// The name on the node has to move with the symbol, or a query
			// reading the tree and a query reading Info would disagree.
			if nodes[n].Attrs != nil {
				if _, ok := nodes[n].Attrs["name"]; ok {
					nodes[n].Attrs["name"] = c.Name
				}
			}
		}
	}
	info := &syntax.Info{
		Defs:    map[*syntax.Node]*syntax.Symbol{},
		Uses:    map[*syntax.Node]*syntax.Symbol{},
		Scopes:  map[*syntax.Node][]*syntax.Symbol{},
		Context: map[string]*syntax.Symbol{},
	}
	rename(t.Info.Defs, info.Defs)
	rename(t.Info.Uses, info.Uses)
	for scope, list := range t.Info.Scopes {
		out := make([]*syntax.Symbol, 0, len(list))
		for _, s := range list {
			out = append(out, copySym(s))
		}
		info.Scopes[nodes[scope]] = out
	}
	for _, s := range t.Info.Context {
		c := copySym(s)
		info.Context[c.Name] = c
	}
	for s, c := range syms {
		c.Aliases = copySym(s.Aliases)
		if s.Scope != nil {
			c.Scope = nodes[s.Scope]
		}
	}

	// Three constructs name a binding in an attribute rather than through a
	// name node, so the rename has to reach them by hand -- and which ones
	// they are is worth writing down. A block's name, a filter's, a test's and
	// a keyword argument's are not bindings and must not move.
	syntax.Walk(root, func(n *syntax.Node, _ syntax.Role) bool {
		switch n.Kind {
		case syntax.KindMacro:
			n.Attrs["name"] = n.Attr("name") + "_r"
		case syntax.KindImport:
			n.Attrs["target"] = n.Attr("target") + "_r"
		case syntax.KindFromImport:
			if raw, ok := n.Attrs["names"].([][2]string); ok {
				out := make([][2]string, 0, len(raw))
				for _, pair := range raw {
					out = append(out, [2]string{pair[0], pair[1] + "_r"})
				}
				n.Attrs["names"] = out
			}
		}
		return true
	})
	return &syntax.Tree{Root: root, Info: info}
}

// forGenerated runs a property over generated templates that compile.
func forGenerated(t *testing.T, count, seed int, check func(*testing.T, *gojja2.Template, string)) {
	n := envInt(t, "GOJJA2_FUZZ_N", count)
	s := uint64(envInt(t, "GOJJA2_FUZZ_SEED", seed))
	rng := rand.New(rand.NewPCG(s, 0x9e3779b97f4a7c15))
	templates := conformance.FuzzTemplates()

	var checked int
	for range n {
		input := make([]byte, 1+rng.IntN(96))
		for i := range input {
			input[i] = byte(rng.UintN(256))
		}
		c := conformance.GenerateCase(input)
		if strings.TrimSpace(c.Source) == "" {
			continue
		}
		sources := make(map[string]string, len(templates)+1)
		for name, text := range templates {
			sources[name] = text
		}
		sources[fuzzTemplateName] = c.Source
		env, err := gojja2.New(append(caseOptions(c),
			gojja2.WithLoader(gojja2.DictLoader(sources)))...)
		if err != nil {
			continue
		}
		tmpl, err := env.GetTemplate(fuzzTemplateName)
		if err != nil {
			continue
		}
		checked++
		check(t, tmpl, c.Source)
		if t.Failed() {
			return
		}
	}
	t.Logf("%d generated templates (seed %d)", checked, s)
}

// describe is the whole analysis as one comparable string.
func describe(tmpl *gojja2.Template) string {
	tree := tmpl.Syntax()
	raw, _ := syntax.Canonical(tree.Root)
	info, _ := syntax.CanonicalInfo(tree)
	return string(raw) + "\x00" + string(info) + "\x00" +
		show(dataflow.Analyze(tree).Context(tree))
}

func show(m map[string]dataflow.Effect) string {
	out := make([]string, 0, len(m))
	for name, e := range m {
		out = append(out, name+":"+encodeEffect(e))
	}
	sort.Strings(out)
	return strings.Join(out, " ")
}
