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
}

func newScope(parent *scope) *scope {
	return &scope{vars: make(map[string]value.Value), parent: parent}
}

func (s *scope) lookup(name string) (value.Value, bool) {
	for cur := s; cur != nil; cur = cur.parent {
		if v, ok := cur.vars[name]; ok {
			return v, true
		}
	}
	return value.Undefined, false
}

// lookupUntil searches the chain up to and including stop, ignoring anything
// below it. It is how a frame asks whether an *enclosing frame* binds a name,
// without seeing the render arguments underneath them.
func (s *scope) lookupUntil(name string, stop *scope) (value.Value, bool) {
	for cur := s; cur != nil; cur = cur.parent {
		if v, ok := cur.vars[name]; ok {
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
		for k, v := range cur.vars {
			out[k] = v
		}
	}
	walk(s)
	return out
}
