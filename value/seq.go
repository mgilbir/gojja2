// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package value

// Seq backs both list and tuple. The two share all behaviour except mutability
// and repr, so they share a payload and differ only in Kind.
type Seq struct {
	items []Value
}

// NewList returns a Python list over items. The slice is adopted, not copied.
func NewList(items ...Value) Value {
	if items == nil {
		items = []Value{}
	}
	return Value{kind: KindList, obj: &Seq{items: items}}
}

// NewTuple returns a Python tuple over items. The slice is adopted, not copied.
func NewTuple(items ...Value) Value {
	if items == nil {
		items = []Value{}
	}
	return Value{kind: KindTuple, obj: &Seq{items: items}}
}

// EmptyList returns a fresh empty list. Each call allocates, because a list is
// mutable and must not be shared between unrelated template variables.
func EmptyList() Value { return NewList() }

// Items returns the backing slice. Callers must not retain it across a
// mutation of the sequence.
func (s *Seq) Items() []Value { return s.items }

// Len is the number of elements.
func (s *Seq) Len() int { return len(s.items) }

// At returns the element at i, which must already be in range.
func (s *Seq) At(i int) Value { return s.items[i] }

// Append adds an element. Only valid for lists.
func (s *Seq) Append(v Value) { s.items = append(s.items, v) }

// Set replaces the element at i.
func (s *Seq) Set(i int, v Value) { s.items[i] = v }

// AsTuple returns the same elements as a tuple, sharing no storage.
func (v Value) AsTuple() Value {
	s, ok := v.Seq()
	if !ok {
		return v
	}
	if v.kind == KindTuple {
		return v
	}
	return NewTuple(append([]Value(nil), s.items...)...)
}

// AsList returns the same elements as a list, sharing no storage.
func (v Value) AsList() Value {
	s, ok := v.Seq()
	if !ok {
		return v
	}
	return NewList(append([]Value(nil), s.items...)...)
}
