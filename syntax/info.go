// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package syntax

// The scope and binding facts about a tree: who binds each name, what each read
// resolves to, and which nodes introduce a scope.
//
// These are kept beside the tree rather than on its nodes, for the reason
// go/types keeps them beside go/ast. Some of them are not properties of a node
// at all: which scope owns a name is a property of a (scope, name) pair, and a
// macro's parameter is bound differently at each call site. A field on a node
// has nowhere to put either. Keeping the tree free of them also keeps it
// immutable, which matters because one compiled template is walked from many
// goroutines.
//
// They cannot be recomputed by a caller, which is why they are here at all.
// jinja2 decides per frame, on a name's first mention, whether the name belongs
// to the frame or resolves from the caller -- `{{ x }}{% set x = 1 %}` reads the
// caller's x and `{% for i in [1] %}{{ x }}{% endfor %}{% set x = 1 %}` does not,
// because the root frame claimed x before the loop ran. Anyone holding only a
// tree would have to work that out again, and get it wrong.

// SymbolKind is where a name comes from.
type SymbolKind string

const (
	// SymContext is a name the caller supplies. These are the ones a
	// template is asking for.
	SymContext SymbolKind = "context"
	// SymGlobal is a name the environment supplies -- range, dict, lipsum
	// and their neighbours. A template reading one is not asking the caller
	// for anything.
	SymGlobal SymbolKind = "global"
	// SymLocal is a name the template binds with `{% set %}`.
	SymLocal SymbolKind = "local"
	// SymParam is a macro's or call block's declared parameter.
	SymParam SymbolKind = "param"
	// SymTarget is a name bound by the construct that introduces it: a
	// loop's target, a `{% with %}` binding, an import's alias.
	SymTarget SymbolKind = "target"
	// SymProvided is a name the construct supplies rather than the caller:
	// loop inside a for, and caller, varargs and kwargs inside a macro.
	SymProvided SymbolKind = "provided"
)

// Symbol is one storage location a name can refer to.
//
// Two occurrences of a name refer to the same Symbol exactly when they read or
// write the same thing, which is the question a rename, a dependency check or a
// dataflow analysis is really asking.
type Symbol struct {
	Name string
	Kind SymbolKind
	// Scope is the node that owns the symbol, or nil for a name that comes
	// from outside the template.
	Scope *Node
}

// Info is the scope and binding facts about one tree.
//
// Defs and Uses are keyed by the node where the name is written: a [KindName]
// node, or the [KindNSRef] of a namespace assignment. A node is in Defs when it
// binds and in Uses when it reads; no node is in both.
type Info struct {
	// Defs maps a binding occurrence to what it binds.
	Defs map[*Node]*Symbol
	// Uses maps a reading occurrence to what it reads.
	Uses map[*Node]*Symbol
	// Scopes maps each scope-introducing node to the symbols it owns, in
	// the order they were introduced.
	Scopes map[*Node][]*Symbol
	// Context is every symbol the caller is expected to supply, by name.
	Context map[string]*Symbol
}

// Symbol returns what a name node refers to, whether it reads or binds, or nil
// if the node is not a name.
func (i *Info) Symbol(n *Node) *Symbol {
	if s, ok := i.Defs[n]; ok {
		return s
	}
	return i.Uses[n]
}

// Tree is a template's structure together with what is known about its names.
//
// The two travel together because Info is keyed by the nodes of one particular
// build: asking for the tree twice produces two sets of nodes, and an Info from
// one says nothing about the other.
type Tree struct {
	Root *Node
	Info *Info
}
