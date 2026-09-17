// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import "github.com/mgilbir/gojja2/value"

// scope is one level of the lexical chain a name resolves through.
//
// The chain bottoms out at the environment globals, above which sits the
// template context, above which sit the frames that loops, `with`, macros and
// blocks introduce. Assignment always writes to the innermost scope, which is
// what makes `{% set %}` inside a loop invisible after it.
type scope struct {
	vars   map[string]value.Value
	parent *scope

	// raw holds render arguments that have not been converted from Go yet,
	// and expose is the policy to convert them under.
	//
	// A caller hands one context to every template, and a template reads a
	// handful of names out of it. Converting the whole map up front made a
	// variable nobody mentions cost as much as one they do: `{{ title }}`
	// with a fifty-element `users` beside it in the map took 23x as long
	// and 13x the memory as the same render without it. Each name is
	// converted on first lookup instead, and memoised into vars.
	raw    map[string]any
	expose value.MethodPolicy
}

func newScope(parent *scope) *scope {
	return &scope{vars: make(map[string]value.Value), parent: parent}
}

func (s *scope) lookup(name string) (value.Value, bool) {
	for cur := s; cur != nil; cur = cur.parent {
		if v, ok := cur.vars[name]; ok {
			return v, true
		}
		if v, ok := cur.convert(name); ok {
			return v, true
		}
	}
	return value.Undefined, false
}

// convert realises one not-yet-converted render argument, memoising it so that
// the second reference is the same value as the first -- which matters, since
// a template can mutate what it was handed within a render.
func (s *scope) convert(name string) (value.Value, bool) {
	raw, ok := s.raw[name]
	if !ok {
		return value.Undefined, false
	}
	// Memoise into vars and leave raw alone: raw *is* the caller's map, so
	// deleting from it would empty the caller's context as the first render
	// walked it, and the second render would find nothing there.
	v := value.FromGoWith(raw, s.expose)
	s.vars[name] = v
	return v, true
}

// realise converts everything still pending, for the callers that need the
// whole context rather than one name of it.
func (s *scope) realise() {
	for name := range s.raw {
		s.convert(name)
	}
}

// lookupUntil searches the chain up to and including stop, ignoring anything
// below it. It is how a frame asks whether an *enclosing frame* binds a name,
// without seeing the render arguments underneath them.
func (s *scope) lookupUntil(name string, stop *scope) (value.Value, bool) {
	for cur := s; cur != nil; cur = cur.parent {
		if v, ok := cur.vars[name]; ok {
			return v, true
		}
		if v, ok := cur.convert(name); ok {
			return v, true
		}
		if cur == stop {
			break
		}
	}
	return value.Undefined, false
}

func (s *scope) set(name string, v value.Value) {
	if s.vars == nil {
		s.vars = make(map[string]value.Value)
	}
	s.vars[name] = v
}

// flatten collects every visible binding, innermost first, for handing a whole
// context to an included template.
func (s *scope) flatten() map[string]value.Value {
	out := make(map[string]value.Value)
	var walk func(*scope)
	walk = func(cur *scope) {
		if cur == nil {
			return
		}
		walk(cur.parent)
		// Handing the whole context somewhere -- an {% include with
		// context %} -- is the one place laziness cannot help.
		cur.realise()
		for k, v := range cur.vars {
			out[k] = v
		}
	}
	walk(s)
	return out
}
