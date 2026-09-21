// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

// Package dataflow answers what a template does with the values it is given.
//
// The question is not which names a template mentions. It is what happens if
// you pass a variable: does its value end up in the document, does it only
// decide which document is produced, or can it not affect the result at all.
// Those are different questions and templates give all three answers.
//
// A syntactic scan cannot tell them apart. `{% set y = x %}{{ y }}` prints x
// without ever naming it at an output position; `{{ "yes" if flag else "no" }}`
// prints neither operand while flag decides which; and `{% set unused = x %}`
// with nothing reading unused means x cannot change the output at all, which is
// the answer worth having. So this is a graph: every binding is a node, effects
// attach where a value is consumed, and they propagate backwards along the
// edges to whatever the value came from.
//
// # It is built on the public tree
//
// Everything here reads [syntax.Tree] and nothing else -- the same tree any
// caller gets from Template.Syntax, with the same scope facts. That is
// deliberate. An analysis the engine could only write from the inside would say
// the exposed tree is not enough to reason with; this one is the demonstration
// that it is, and the edge labels are what make it short. A value under
// [syntax.RoleTest] is steering wherever it appears, so there is no table of
// "which field of which node is a condition" anywhere below.
//
// # Soundness has a direction
//
// Where the analysis cannot see -- a computed subscript, a namespace, a
// template pulled in by [syntax.KindInclude] -- the answer carries [Opaque]
// rather than a guess. A symbol reported without it is a real answer in both
// directions; one with it may reach the output by a route that was not
// followed. Nothing here ever reports that a variable cannot reach the output
// when it might.
package dataflow

import "github.com/mgilbir/gojja2/syntax"

// Effect is what a value can do to the rendered output.
type Effect uint8

const (
	// Printed means the value can appear in the output, possibly
	// transformed on the way: `{{ x }}`, `{{ x|upper }}`, `{{ x|length }}`.
	Printed Effect = 1 << iota
	// Steers means it can change the output without appearing in it: a
	// condition, a loop's length, an autoescape setting.
	Steers
	// Opaque means some route was not followed, so the answer has no
	// reliable negative.
	Opaque
)

// Flow is the result: a dependency graph over symbols, and what each one can do.
//
// Both are exposed. Effects is the common question and is already propagated;
// Derives is the graph it was propagated over, for a caller whose question is
// not this one -- "what would change if I stopped passing x", "which of these
// bindings is dead" -- which is the reason this is a graph rather than three
// booleans.
type Flow struct {
	// Derives maps a symbol to the symbols its value can come from. An
	// edge means "this may hold something derived from that".
	Derives map[*syntax.Symbol][]*syntax.Symbol
	// Effects is what each symbol can do, after propagation.
	Effects map[*syntax.Symbol]Effect
}

// Of returns what one symbol can do.
func (f *Flow) Of(s *syntax.Symbol) Effect { return f.Effects[s] }

// Context is what the caller's variables can do, by name. Names the environment
// supplies are left out: a template reading `range` is not asking for anything.
func (f *Flow) Context(t *syntax.Tree) map[string]Effect {
	out := map[string]Effect{}
	for name, sym := range t.Info.Context {
		if sym.Kind == syntax.SymContext {
			out[name] = f.Effects[sym]
		}
	}
	return out
}
