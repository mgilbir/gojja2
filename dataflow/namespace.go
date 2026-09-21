// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package dataflow

import "github.com/mgilbir/gojja2/syntax"

// Namespaces, which are the idiom for carrying a value out of a loop.
//
//	{% set ns = namespace(total=0) %}
//	{% for row in rows %}{% set ns.total = ns.total + row.n %}{% endfor %}
//	{{ ns.total }}
//
// A plain `{% set %}` inside a loop does not escape it, which is exactly why
// namespace exists, and exactly why it is the shape worth following: accumulate
// then print is the case someone most wants traced. Treating the namespace as
// one opaque blob makes the answer for `rows` "might reach the output", when it
// plainly does.
//
// So each field gets a symbol of its own and the ordinary dataflow applies:
// `ns.total` derives from `rows`, and printing it prints them.
//
// # Where it stops
//
// Only while the namespace itself is never handed anywhere. `{% set other = ns %}`
// makes two names for one object, and a write through either reaches the other;
// passing it to a macro or a filter is the same. Tracking that properly is alias
// analysis, and getting it subtly wrong would mean reporting a real negative
// that is not true -- the one thing this must never do. So a namespace that is
// read anywhere except as the subject of a field access collapses: every field
// it has becomes [Opaque], and the answers go back to what they were before this
// file existed.
//
// The test is deliberately crude and deliberately in the safe direction.

// namespaceField is the symbol standing for one field of one namespace.
//
// These are synthesised here rather than coming from [syntax.Info], because a
// field is not a name in any scope: nothing declares `ns.total`, it simply
// starts existing when something assigns it.
func (a *analyzer) namespaceField(ns *syntax.Symbol, field string) *syntax.Symbol {
	fields, ok := a.namespaces[ns]
	if !ok {
		return nil
	}
	if s, ok := fields[field]; ok {
		return s
	}
	s := &syntax.Symbol{Name: ns.Name + "." + field, Kind: ns.Kind, Scope: ns.Scope}
	fields[field] = s
	return s
}

// declareNamespace records that a symbol holds a namespace, and what its fields
// start out holding.
//
// A symbol assigned a namespace in one place and something else in another is
// not one this can follow, so the second assignment takes the tracking away
// rather than pretending the first was the whole story.
func (a *analyzer) declareNamespace(sym *syntax.Symbol, call *syntax.Node) {
	if sym == nil {
		return
	}
	if _, seen := a.namespaces[sym]; seen {
		a.aliased[sym] = true
		return
	}
	a.namespaces[sym] = map[string]*syntax.Symbol{}
	for _, kw := range call.Children(syntax.RoleKwarg) {
		if f := a.namespaceField(sym, kw.Attr("name")); f != nil {
			a.depend(f, a.expr(kw.Child(syntax.RoleValue)))
		}
	}
	// `namespace(d)` and `namespace(**d)` fill it from something this cannot
	// name the fields of, so it holds whatever that held and the fields
	// cannot be told apart.
	rest := symset{}
	for _, role := range []syntax.Role{syntax.RoleArg, syntax.RoleDynArgs, syntax.RoleDynKw} {
		for _, c := range call.Children(role) {
			rest.add(a.expr(c))
		}
	}
	if len(rest) > 0 {
		a.depend(sym, rest)
		a.aliased[sym] = true
	}
}

// namespaceOf reports the namespace a node refers to, if it refers to one
// directly by name.
func (a *analyzer) namespaceOf(n *syntax.Node) *syntax.Symbol {
	if n == nil || n.Kind != syntax.KindName {
		return nil
	}
	sym := a.tree.Info.Uses[n]
	if sym == nil {
		return nil
	}
	if _, ok := a.namespaces[sym]; !ok {
		return nil
	}
	return sym
}

// isNamespaceCall reports whether an expression is `namespace(...)`.
func isNamespaceCall(n *syntax.Node) bool {
	if n == nil || n.Kind != syntax.KindCall {
		return false
	}
	callee := n.Child(syntax.RoleCallee)
	return callee != nil && callee.Kind == syntax.KindName &&
		callee.Attr("name") == "namespace"
}

// sealNamespaces gives up on every namespace that got away, which has to happen
// after the walk: the assignment that aliases one may come after the reads that
// looked safe.
func (a *analyzer) sealNamespaces() {
	for ns, fields := range a.namespaces {
		if !a.aliased[ns] {
			continue
		}
		a.effects[ns] |= Opaque
		all := symset{}
		for _, f := range fields {
			a.effects[f] |= Opaque
			// A write through the other name could have put anything
			// in any field, including whatever the namespace itself
			// was built from.
			a.depend(f, symset{ns: true})
			all[f] = true
		}
		// ...and the other way round: whoever holds the namespace can read
		// every field, so doing anything with it does that to all of them.
		a.depend(ns, all)
	}
}
