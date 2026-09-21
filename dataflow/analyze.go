// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package dataflow

import "github.com/mgilbir/gojja2/syntax"

type symset map[*syntax.Symbol]bool

func (s symset) add(o symset) {
	for k := range o {
		s[k] = true
	}
}

type analyzer struct {
	tree    *syntax.Tree
	derives map[*syntax.Symbol]symset
	effects map[*syntax.Symbol]Effect

	// capture is the stack of block-set bodies being collected: while one is
	// open, output becomes a value instead of a document.
	capture []symset

	// scopes is the chain of enclosing scope nodes, which is how a macro's
	// name is looked up -- it is an attribute of the macro node rather than
	// a name node, so it has no entry in Defs.
	scopes []*syntax.Node

	macroOut    map[*syntax.Symbol]symset
	macroParams map[*syntax.Symbol][]*syntax.Symbol
	inMacro     map[*syntax.Symbol]bool

	// opaqueSink records that the whole context can be handed to a template
	// this did not follow, which puts every symbol in play.
	opaqueSink bool
}

// Analyze computes what a template does with the values it is given.
func Analyze(t *syntax.Tree) *Flow {
	a := &analyzer{
		tree:        t,
		derives:     map[*syntax.Symbol]symset{},
		effects:     map[*syntax.Symbol]Effect{},
		macroOut:    map[*syntax.Symbol]symset{},
		macroParams: map[*syntax.Symbol][]*syntax.Symbol{},
		inMacro:     map[*syntax.Symbol]bool{},
	}
	a.stmt(t.Root)
	a.propagate()

	out := &Flow{
		Derives: make(map[*syntax.Symbol][]*syntax.Symbol, len(a.derives)),
		Effects: map[*syntax.Symbol]Effect{},
	}
	for s, deps := range a.derives {
		list := make([]*syntax.Symbol, 0, len(deps))
		for d := range deps {
			list = append(list, d)
		}
		out.Derives[s] = list
	}
	for s, e := range a.effects {
		if a.opaqueSink {
			e |= Opaque
		}
		out.Effects[s] = e
	}
	// A symbol nothing touched still has an answer, and if the context can
	// escape to another template that answer is not "nothing".
	for _, s := range a.allSymbols() {
		if _, ok := out.Effects[s]; !ok {
			var e Effect
			if a.opaqueSink {
				e = Opaque
			}
			out.Effects[s] = e
		}
	}
	return out
}

func (a *analyzer) allSymbols() []*syntax.Symbol {
	var out []*syntax.Symbol
	for _, syms := range a.tree.Info.Scopes {
		out = append(out, syms...)
	}
	for _, s := range a.tree.Info.Context {
		out = append(out, s)
	}
	return out
}

// --- recording ---------------------------------------------------------

func (a *analyzer) apply(srcs symset, e Effect) {
	for s := range srcs {
		a.effects[s] |= e
	}
}

func (a *analyzer) taint(srcs symset) { a.apply(srcs, Opaque) }

// emit records that a value reaches the document, unless a block set is
// capturing it into a variable instead.
func (a *analyzer) emit(srcs symset) {
	if n := len(a.capture); n > 0 {
		a.capture[n-1].add(srcs)
		return
	}
	a.apply(srcs, Printed)
}

func (a *analyzer) depend(target *syntax.Symbol, srcs symset) {
	if target == nil || len(srcs) == 0 {
		return
	}
	if a.derives[target] == nil {
		a.derives[target] = symset{}
	}
	a.derives[target].add(srcs)
}

// bind records that an assignment target now holds a value derived from srcs.
func (a *analyzer) bind(target *syntax.Node, srcs symset) {
	if target == nil {
		return
	}
	switch target.Kind {
	case syntax.KindName:
		a.depend(a.tree.Info.Symbol(target), srcs)
	case syntax.KindNSRef:
		// A namespace field. Until the field is followed, a write into a
		// namespace makes the namespace opaque rather than silently lost.
		s := a.tree.Info.Symbol(target)
		a.depend(s, srcs)
		if s != nil {
			a.effects[s] |= Opaque
		}
	case syntax.KindTuple, syntax.KindList:
		// Unpacking: which element lands where is not tracked, so every
		// target derives from the whole right-hand side.
		for _, item := range target.Children(syntax.RoleItem) {
			a.bind(item, srcs)
		}
	default:
		a.taint(srcs)
	}
}

// propagate pushes effects backwards along the dependency edges to a fixpoint.
//
// A value that reaches the output means everything it came from reaches the
// output; the same for steering, and for not having been understood. The graph
// can hold cycles -- a loop that accumulates into a variable it also reads --
// so this iterates rather than recursing.
func (a *analyzer) propagate() {
	for changed := true; changed; {
		changed = false
		for s, deps := range a.derives {
			e := a.effects[s]
			if e == 0 {
				continue
			}
			for d := range deps {
				if a.effects[d]|e != a.effects[d] {
					a.effects[d] |= e
					changed = true
				}
			}
		}
	}
}
