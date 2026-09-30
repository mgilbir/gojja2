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
// One operation produces it: the difference of a dict's keys or items view with
// an iterable. jinja2's grammar has no `&` or `^`, and `|` is the filter
// operator, so `d.keys() - xs` is the only set arithmetic a template can
// *write* as an operator -- and `set - list` is refused in CPython, so an
// iterable cannot be the right operand of a second one. Another set can:
// `(d.keys() - x) - (d.keys() - y)` is a set difference, and this used to say
// it was not.
//
// Everything else a set does, it does through its methods, all seventeen of
// which are in set_methods.go. Those take any iterable where the operator takes
// a set, which is CPython's rule and not a convenience.
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

// GetAttr answers nothing here: the set's methods are bound in the gojja2
// package, beside every other built-in type's, because that is where the arity
// table CPython's wordings are generated into lives. This used to say the
// methods were unreachable, on the grounds that only a view difference makes a
// set -- but `{% set s = d.keys() - xs %}{{ s.add(1) }}` is a method call on
// one, and all seventeen of them were missing.
func (s *Set) GetAttr(string) (Value, bool) { return Undefined, false }

// Elements are the set's members, in the order it keeps them: sorted by repr,
// for the reason the type comment gives. The slice is the set's own and must
// not be held past a mutation.
func (s *Set) Elements() []Value { return s.items }

// Has reports membership without hashing the item again, for a caller that has
// already checked -- a set's own elements are hashable by construction.
func (s *Set) Has(v Value) bool {
	_, ok := s.index.GetKnown(v)
	return ok
}

// Add inserts an element, keeping the order sorted. It refuses an unhashable
// one exactly as building a set does.
func (s *Set) Add(v Value, py PythonVersion, budget Budget) error {
	if err := CheckHashable(v, py, AsSetElement); err != nil {
		return err
	}
	if s.Has(v) {
		return nil
	}
	if err := chargeItems(budget, 1); err != nil {
		return err
	}
	s.index.SetKnown(v, None)
	s.items = append(s.items, v)
	s.resort()
	return nil
}

// Discard removes an element if it is there, reporting whether it was.
func (s *Set) Discard(v Value) bool {
	if !s.Has(v) {
		return false
	}
	s.index.DeleteKnown(v)
	kept := s.items[:0]
	for _, item := range s.items {
		if _, in := s.index.GetKnown(item); in {
			kept = append(kept, item)
		}
	}
	s.items = kept
	return true
}

// Pop removes and answers the first element in the set's order. CPython pops
// the first in *its* order, which is the hash table's; the two orders are the
// documented divergence and this is one more place it shows.
func (s *Set) Pop() (Value, bool) {
	if len(s.items) == 0 {
		return Undefined, false
	}
	v := s.items[0]
	s.index.DeleteKnown(v)
	s.items = s.items[1:]
	return v, true
}

// Reset replaces every element, which is how the in-place operations --
// update, the three ..._update and clear -- land their result.
func (s *Set) Reset(elements []Value, py PythonVersion, budget Budget) error {
	next, err := NewSet(elements, py, budget)
	if err != nil {
		return err
	}
	s.items, s.index = next.items, next.index
	return nil
}

func (s *Set) resort() {
	sort.SliceStable(s.items, func(i, j int) bool {
		return Repr(s.items[i]) < Repr(s.items[j])
	})
}

func (s *Set) TypeName() string { return "set" }

// Unhashable reports that a set cannot be a dict key or a set element. Python's
// set defines __eq__ without __hash__ -- only frozenset is hashable -- so
// `{{ (d.keys() - 'a') is filter }}` is "unhashable type: 'set'" and
// `{{ {(d.keys() - 'a'): 1} }}` is a TypeError. Without this a set hashed by
// identity and both answered.
func (s *Set) Unhashable() bool { return true }

// QualifiedName is plain "set": object_type_repr qualifies a class with its
// module only *outside* builtins, so a set is "set object" where a Joiner is
// "jinja2.utils.Joiner object". Writing "builtins.set" made 3.14's
// "cannot use 'builtins.set' as a dict key" and would have made an undefined
// built from a set report the wrong owner.
func (s *Set) QualifiedName() string { return "set" }
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

// ContainsErr answers `x in <set>` with the complaint hashing the item can make,
// which a template can provoke with any value at all: a set hashes what it is
// asked about, so `{{ {} in (d.keys() - 'a') }}` is "unhashable type: 'dict'" and
// not False.
//
// There is no Contains beside it. There was, because Container is one of the
// interfaces searchable() accepts -- but Iterate already makes a set an Iterable,
// which searchable() accepts too, and ContainsErr answers before either is
// consulted. Removing it left the whole suite green; it was the last function
// whole-suite coverage had never executed.
func (s *Set) ContainsErr(item Value, py PythonVersion) (found, known bool, err error) {
	if _, isSet := item.Interface().(*Set); isSet {
		// A set is unhashable, and `x in s` is the one place CPython
		// does not stop there: set_contains catches the TypeError, makes
		// a *frozenset* of the key and looks that up instead, so
		// `{1} in {2}` is False rather than a refusal. Nothing here can
		// build a frozenset, so the answer is always False -- but it is
		// an answer, which is what a chained `a not in s not in s`
		// needs. A dict does not do this: `{1} in d.keys()` refuses.
		return false, true, nil
	}
	if err := CheckHashable(item, py, AsSetElement); err != nil {
		return false, true, err
	}
	_, ok := s.index.GetKnown(item)
	return ok, true, nil
}

// Equals answers only against something that is not a set, which is never
// equal to one: `s == ['a']` is False.
//
// Two sets are compared by equalAsSets in compare.go, which runs before any
// Equaler is asked and is where the rule -- same size, every member of one in
// the other -- is kept. This carried a second copy of it that nothing reached:
// replacing it with a wrong answer left the suite green. So a set here has no
// opinion, and the caller's own rule decides.
func (s *Set) Equals(other Value) (bool, bool) {
	if _, ok := other.Interface().(*Set); ok {
		return false, false
	}
	return false, true
}

// setReverseDifference is `other - view`, which a template reaches by writing
// the view second: `xs - d.keys()`.
//
// CPython gets here through the view's __rsub__, which builds set(other) before
// it builds set(view) -- so a left operand that is not iterable is reported
// before the view is hashed at all, and a left operand holding something
// unhashable is reported before the view's own elements are looked at. Both
// orders are observable when each side is wrong in a different way.
func setReverseDifference(other Value, view SetOperand, py PythonVersion, budget Budget) (Value, error) {
	seq, err := Iterate(other)
	if err != nil {
		return Undefined, err
	}
	var items []Value
	for v := range seq {
		if err := chargeItems(budget, 1); err != nil {
			return Undefined, err
		}
		items = append(items, v)
	}
	left, err := NewSet(items, py, budget)
	if err != nil {
		return Undefined, err
	}
	elements, _ := view.SetElements()
	remove, err := NewSet(elements, py, budget)
	if err != nil {
		return Undefined, err
	}
	var kept []Value
	for _, v := range left.items {
		if _, drop := remove.index.GetKnown(v); !drop {
			kept = append(kept, v)
		}
	}
	out, err := NewSet(kept, py, budget)
	if err != nil {
		return Undefined, err
	}
	return FromObject(out), nil
}

// setDifference is `view - other`: the view's elements that other does not hold.
//
// other is any iterable, which is the view's rule rather than the set's -- a
// real set refuses anything that is not another set, and CPython says so.
func setDifference(left SetOperand, other Value, py PythonVersion, budget Budget) (Value, error) {
	// The left operand is hashed first, because CPython builds the set from
	// the view before it so much as looks at the other one: `d.items() - []`
	// on items holding a dict is "unhashable type: 'dict'", not a complaint
	// about the empty list, and `d.keys() - 1.5` on hashable keys is about
	// the 1.5. Doing it the other way round reports whichever operand is
	// wrong second.
	elements, _ := left.SetElements()
	kept, err := NewSet(elements, py, budget)
	if err != nil {
		return Undefined, err
	}
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
	var out []Value
	for _, v := range kept.items {
		if _, drop := remove.GetKnown(v); !drop {
			out = append(out, v)
		}
	}
	set, err := NewSet(out, py, budget)
	if err != nil {
		return Undefined, err
	}
	return FromObject(set), nil
}
