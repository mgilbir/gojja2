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
	// budget bounds the conversion, which is proportional to the argument
	// rather than to the template that names it. See scope.convert.
	budget value.Budget

	// refs are the names this frame mentions at its own level, recorded by
	// declareFrameLocals. A frame nested inside this one consults them to
	// decide whether its own stores alias an outer value or start
	// undefined; see declareFrameLocals.
	refs map[string]bool
}

// newScope leaves vars nil. A scope is created for every loop iteration, every
// `{% with %}` and every macro call, and most of them bind one or two names or
// none at all -- a 50-iteration loop was allocating 50 maps before anything was
// put in them. set builds the map when there is something to put in it, and
// reading from a nil map is already legal.
func newScope(parent *scope) *scope {
	return &scope{parent: parent}
}

func (s *scope) lookup(name string) (value.Value, bool, error) {
	for cur := s; cur != nil; cur = cur.parent {
		if v, ok := cur.vars[name]; ok {
			return v, true, nil
		}
		v, ok, err := cur.convert(name)
		if err != nil {
			return value.Undefined, false, err
		}
		if ok {
			return v, true, nil
		}
	}
	return value.Undefined, false, nil
}

// convert realises one not-yet-converted render argument, memoising it so that
// the second reference is the same value as the first -- which matters, since
// a template can mutate what it was handed within a render.
//
// It is charged to the render, because its cost is the argument's size and not
// the template's: a million-element argument took a second to convert, and did
// it before anything a deadline could interrupt had started. An expression that
// never iterates -- `{{ big|length }}` -- then rendered successfully against an
// already-cancelled context, because nothing afterwards ever consulted it.
func (s *scope) convert(name string) (value.Value, bool, error) {
	raw, ok := s.raw[name]
	if !ok {
		return value.Undefined, false, nil
	}
	// Memoise into vars and leave raw alone: raw *is* the caller's map, so
	// deleting from it would empty the caller's context as the first render
	// walked it, and the second render would find nothing there.
	v, err := value.FromGoBudget(raw, s.expose, s.budget)
	if err != nil {
		return value.Undefined, false, err
	}
	s.set(name, v)
	return v, true, nil
}

// realise converts everything still pending, for the callers that need the
// whole context rather than one name of it.
func (s *scope) realise() error {
	for name := range s.raw {
		if _, _, err := s.convert(name); err != nil {
			return err
		}
	}
	return nil
}

// lookupUntil searches the chain up to and including stop, ignoring anything
// below it. It is how a frame asks whether an *enclosing frame* binds a name,
// without seeing the render arguments underneath them.
func (s *scope) lookupUntil(name string, stop *scope) (value.Value, bool, error) {
	for cur := s; cur != nil; cur = cur.parent {
		if v, ok := cur.vars[name]; ok {
			return v, true, nil
		}
		v, ok, err := cur.convert(name)
		if err != nil {
			return value.Undefined, false, err
		}
		if ok {
			return v, true, nil
		}
		if cur == stop {
			break
		}
	}
	return value.Undefined, false, nil
}

func (s *scope) set(name string, v value.Value) {
	if s.vars == nil {
		s.vars = make(map[string]value.Value)
	}
	s.vars[name] = v
}

// flatten collects every visible binding, innermost first, for handing a whole
// context to an included template.
func (s *scope) flatten() (map[string]value.Value, error) {
	out := make(map[string]value.Value)
	var walk func(*scope) error
	walk = func(cur *scope) error {
		if cur == nil {
			return nil
		}
		if err := walk(cur.parent); err != nil {
			return err
		}
		// Handing the whole context somewhere -- an {% include with
		// context %} -- is the one place laziness cannot help.
		if err := cur.realise(); err != nil {
			return err
		}
		for k, v := range cur.vars {
			out[k] = v
		}
		return nil
	}
	if err := walk(s); err != nil {
		return nil, err
	}
	return out, nil
}
