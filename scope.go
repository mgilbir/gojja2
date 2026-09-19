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
	// names, vals and n are the first few bindings, held without a map.
	//
	// A loop frame binds two -- the loop variable and `loop` -- and
	// building a map for them was the single largest source of allocated
	// objects in a loop, 40% of them: a Go map with one string key and a
	// 48-byte value is two allocations and about 576 bytes, against 56 for
	// the scope that held it. Every iteration of every loop paid it.
	//
	// Bindings past these spill into vars, which also holds the environment
	// globals, since that scope is built around a map that already exists.
	names [inlineVars]string
	vals  [inlineVars]value.Value
	n     int

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

// inlineVars is how many bindings a scope holds before it needs a map.
//
// Two, because two is what a loop frame binds -- the loop variable and `loop` --
// and because measuring said so. Every slot costs 64 bytes in every scope
// whether it is used or not, and raising this to three, four or six did not
// remove a single further allocation from any benchmark here while adding
// 19%, 38% and 75% to the bytes a loop allocates. The frames that bind more
// than two are rare enough that widening every scope to hold them in line
// costs more than the map they fall back to.
const inlineVars = 2

// get reads a binding from this scope alone, slots before map.
//
// A name cannot be in both: set looks in both before it puts anything down.
func (s *scope) get(name string) (value.Value, bool) {
	for i := range s.n {
		if s.names[i] == name {
			return s.vals[i], true
		}
	}
	v, ok := s.vars[name]
	return v, ok
}

// each calls yield for every binding this scope holds.
func (s *scope) each(yield func(string, value.Value)) {
	for i := range s.n {
		yield(s.names[i], s.vals[i])
	}
	for k, v := range s.vars {
		yield(k, v)
	}
}

func (s *scope) lookup(name string) (value.Value, bool, error) {
	for cur := s; cur != nil; cur = cur.parent {
		if v, ok := cur.get(name); ok {
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
		if v, ok := cur.get(name); ok {
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

// set binds a name in this scope, rebinding it wherever it already lives.
//
// Both places are searched before anything is written, which is what keeps a
// name out of the slots and the map at once -- and keeps get free to stop at
// the first hit.
func (s *scope) set(name string, v value.Value) {
	for i := range s.n {
		if s.names[i] == name {
			s.vals[i] = v
			return
		}
	}
	if _, ok := s.vars[name]; ok {
		s.vars[name] = v
		return
	}
	if s.n < inlineVars {
		s.names[s.n], s.vals[s.n] = name, v
		s.n++
		return
	}
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
		cur.each(func(k string, v value.Value) { out[k] = v })
		return nil
	}
	if err := walk(s); err != nil {
		return nil, err
	}
	return out, nil
}
