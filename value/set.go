// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package value

import (
	"iter"
	"sort"
	"strings"
)

// Set is Python's set, as far as a template can reach one.
//
// Only one operation produces it: the difference of a dict's keys or items view
// with an iterable. jinja2's grammar has no `&` or `^`, and `|` is the filter
// operator, so `d.keys() - xs` is the whole of the set arithmetic a template can
// write -- and `set - list` is refused in CPython too, so the result cannot even
// be the left operand of another one.
//
// # Order
//
// CPython's set has no order, and its repr prints in hash order, which is not
// reproducible between two runs of CPython itself: string hashing is randomised
// per process, so `{'epsilon', 'delta', 'c', 'b'}`, `{'epsilon', 'b', 'c',
// 'delta'}` and `{'delta', 'b', 'epsilon', 'c'}` are three runs of the same
// expression. gojja2 sorts instead, by each element's repr, so the answer is the
// same every time. See docs/divergences.md.
//
// Sorting by repr rather than by value is what makes it total: a set may hold
// numbers and strings together, which Python cannot order, and repr can.
type Set struct {
	// items are deduplicated and sorted. index carries Python's own
	// equality -- 1 and True are one element, as they are one dict key.
	items []Value
	index *Dict
}

// SetOperand is an Object whose elements take part in set arithmetic. A dict's
// keys and items views are; its values view deliberately is not, because values
// need be neither unique nor hashable.
type SetOperand interface {
	Object
	// SetElements are the elements to treat as a set, and ok is false for
	// an object that is not a set operand after all -- a dict's values
	// view is the case, and it shares its type with the two that are.
	SetElements() (elements []Value, ok bool)
}

// NewSet builds a set from elements, refusing any that cannot be a member.
func NewSet(elements []Value, py PythonVersion, budget Budget) (*Set, error) {
	s := &Set{index: &Dict{}}
	for _, v := range elements {
		if err := CheckHashable(v, py, AsSetElement); err != nil {
			return nil, err
		}
		if _, seen := s.index.GetKnown(v); seen {
			continue
		}
		if err := chargeItems(budget, 1); err != nil {
			return nil, err
		}
		s.index.SetKnown(v, None)
		s.items = append(s.items, v)
	}
	sort.SliceStable(s.items, func(i, j int) bool {
		return Repr(s.items[i]) < Repr(s.items[j])
	})
	return s, nil
}

// GetAttr: a set has methods in Python, and none of them are reachable here --
// the only set a template can hold came from a view difference, and nothing in
// jinja2's grammar calls a method on the result of one.
func (s *Set) GetAttr(string) (Value, bool) { return Undefined, false }

func (s *Set) TypeName() string      { return "set" }
func (s *Set) QualifiedName() string { return "builtins.set" }
func (s *Set) Len() int              { return len(s.items) }

// Repr is Python's, including the empty case: `set()` and not `{}`, which is
// how a set is told from a dict when there is nothing in either.
func (s *Set) Repr() string {
	if len(s.items) == 0 {
		return "set()"
	}
	var b strings.Builder
	b.WriteString("{")
	for i, v := range s.items {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(Repr(v))
	}
	b.WriteString("}")
	return b.String()
}

func (s *Set) Iterate() iter.Seq[Value] {
	return func(yield func(Value) bool) {
		for _, v := range s.items {
			if !yield(v) {
				return
			}
		}
	}
}

func (s *Set) Contains(item Value) (found, known bool) {
	_, ok := s.index.GetKnown(item)
	return ok, true
}

// Equals compares as a set: same size, same members, order irrelevant.
func (s *Set) Equals(other Value) (bool, bool) {
	o, ok := other.Interface().(*Set)
	if !ok {
		return false, true
	}
	if len(s.items) != len(o.items) {
		return false, true
	}
	for _, v := range s.items {
		if _, in := o.index.GetKnown(v); !in {
			return false, true
		}
	}
	return true, true
}

// setDifference is `view - other`: the view's elements that other does not hold.
//
// other is any iterable, which is the view's rule rather than the set's -- a
// real set refuses anything that is not another set, and CPython says so.
func setDifference(left SetOperand, other Value, py PythonVersion, budget Budget) (Value, error) {
	seq, err := Iterate(other)
	if err != nil {
		return Undefined, err
	}
	remove := &Dict{}
	for v := range seq {
		if err := CheckHashable(v, py, AsSetElement); err != nil {
			return Undefined, err
		}
		if err := chargeItems(budget, 1); err != nil {
			return Undefined, err
		}
		remove.SetKnown(v, None)
	}
	elements, _ := left.SetElements()
	var kept []Value
	for _, v := range elements {
		if _, drop := remove.GetKnown(v); !drop {
			kept = append(kept, v)
		}
	}
	set, err := NewSet(kept, py, budget)
	if err != nil {
		return Undefined, err
	}
	return FromObject(set), nil
}
