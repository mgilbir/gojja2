// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package dataflow

import "github.com/mgilbir/gojja2/syntax"

// A Resolver produces the structure of another template by name, so the
// analysis can follow `{% extends %}`, `{% include %}` and `{% import %}`
// instead of giving up at them. It returns nil for a name it cannot supply.
//
// It is a function rather than an interface on the engine so that this package
// depends on nothing but [syntax]. A caller wires it to whatever they load
// templates with:
//
//	flow := dataflow.Analyze(tree, dataflow.WithResolver(func(name string) *syntax.Tree {
//		t, err := env.GetTemplate(name)
//		if err != nil {
//			return nil
//		}
//		return t.Syntax()
//	}))
//
// Without one, a template that pulls in another is reported as [Opaque],
// because the other one can print anything it was handed.
type Resolver func(name string) *syntax.Tree

// Option configures Analyze.
type Option func(*options)

type options struct{ resolve Resolver }

// WithResolver lets the analysis follow references to other templates.
func WithResolver(r Resolver) Option { return func(o *options) { o.resolve = r } }

// constTemplateName is the name a reference gives, when it gives one at all.
//
// `{% include "header.html" %}` names a template; `{% include page %}` names a
// value, and which template that is cannot be known here. Roughly one reference
// in twenty across the corpora is the second kind.
func constTemplateName(n *syntax.Node) (string, bool) {
	ref := n.Child(syntax.RoleTemplate)
	if ref == nil || ref.Kind != syntax.KindConst {
		return "", false
	}
	name, ok := ref.Attrs["value"].(string)
	return name, ok
}

// withContext reports whether a reference hands the current variables to the
// template it names.
//
// The defaults differ and the difference matters: `{% include %}` passes the
// context, `{% import %}` and `{% from ... import %}` do not. An import without
// context cannot see the caller's variables at all, so it cannot print them --
// which is a real answer rather than a shrug.
func withContext(n *syntax.Node) bool {
	v, ok := n.Attrs["with_context"].(bool)
	return ok && v
}

// contextEffectsOf analyses another template and reports what it does with the
// variables it is handed.
//
// A template already being analysed further up the chain returns nothing: the
// cycle is real -- a template can include its own parent -- and the effects it
// would contribute are already being collected by the frame that is waiting.
func (a *analyzer) contextEffectsOf(name string) map[string]Effect {
	if eff, done := a.cache[name]; done {
		return eff
	}
	if a.visiting[name] {
		return nil
	}
	if a.resolve == nil {
		a.opaqueSink = true
		return nil
	}
	tree := a.resolve(name)
	if tree == nil {
		// Named but not available. It may not exist, or the loader may
		// not be the one that will be used at render time; either way
		// what it would print is unknown.
		a.opaqueSink = true
		return nil
	}

	a.visiting[name] = true
	sub := newAnalyzer(tree, a.resolve, a.visiting, a.cache)
	sub.seedAliases()
	sub.stmt(tree.Root)
	sub.sealNamespaces()
	sub.propagate()
	delete(a.visiting, name)

	if sub.opaqueSink {
		// It can hand the variables somewhere neither of us can see.
		a.opaqueSink = true
	}
	eff := map[string]Effect{}
	for nm, sym := range tree.Info.Context {
		if sym.Kind == syntax.SymContext {
			eff[nm] = sub.effects[sym]
		}
	}
	// Including what *it* found by following its own references: a template
	// two levels down still names variables the caller has to supply, and
	// stopping at one level would lose them.
	for nm, sym := range sub.external {
		if _, ok := eff[nm]; !ok {
			eff[nm] = sub.effects[sym]
		}
	}
	a.cache[name] = eff
	return eff
}

// inherit applies what another template does with its variables to ours, which
// is what handing it the context means.
func (a *analyzer) inherit(name string) {
	for nm, e := range a.contextEffectsOf(name) {
		// Whatever the name means *here*, which need not be one of the
		// caller's variables: `{% set v = 'V' %}{% include 'x' %}` hands
		// the local v to x, and the caller's v is not involved.
		//
		// Even an effect of nothing is worth recording when it is a
		// caller's variable: the other template reads it, so it still has
		// to be supplied, and "supplied but provably cannot change the
		// output" is a different answer from "never mentioned".
		a.effects[a.lookup(nm)] |= e
	}
}

// lookup is what a name refers to here: a binding in an enclosing scope, or one
// of the caller's variables.
//
// An import binds a name, but not always a local one. jinja2's first-mention
// rule means a name merely *mentioned* in an earlier branch keeps resolving
// outward, so `{% if false %}{% else %}{{ m }}{% endif %}{% from "x" import m %}`
// writes the macro into the caller's m rather than into a new binding.
func (a *analyzer) lookup(name string) *syntax.Symbol {
	if s := a.scopeSymbol(name); s != nil {
		return s
	}
	return a.contextByName(name)
}

// contextByName is this template's symbol for one of the caller's variables,
// including names only the templates it pulls in ever mention.
func (a *analyzer) contextByName(name string) *syntax.Symbol {
	if s, ok := a.tree.Info.Context[name]; ok {
		return s
	}
	if s, ok := a.external[name]; ok {
		return s
	}
	s := &syntax.Symbol{Name: name, Kind: syntax.SymContext}
	a.external[name] = s
	return s
}

// importedNames is what an import binds locally: the module's alias for
// `{% import %}`, or each imported name's alias for `{% from ... import %}`.
func importedNames(n *syntax.Node) []string {
	if n.Kind == syntax.KindImport {
		if target := n.Attr("target"); target != "" {
			return []string{target}
		}
		return nil
	}
	raw, ok := n.Attrs["names"].([][2]string)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, pair := range raw {
		out = append(out, pair[1])
	}
	return out
}
