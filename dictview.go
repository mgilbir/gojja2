// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"iter"
	"strings"

	"github.com/mgilbir/gojja2/value"
)

// dictView is what d.keys(), d.values() and d.items() answer.
//
// They returned plain lists, which is observable in five ways: the repr is
// `dict_keys(['b', 'a'])` and not `['b', 'a']`; a view is not a sequence, so
// `d.keys() is sequence` is False and `d.keys()[0]` does not index; json.dumps
// refuses one; and a view is a *view*, so a later insertion into the dict shows
// through it.
//
// It is deliberately not a Sequence. Sized is the interface for Python's
// __len__ without __getitem__, which is exactly what a view presents.
//
// This is not the lazy-sequence divergence in docs/divergences.md. That one is
// about jinja2's `map`, `select` and `items` *filters*, which return generators
// there and lists here on purpose. CPython's dict methods return views, which
// are a different thing from a generator -- they have a length, they can be
// walked more than once, and they track the dict.
type dictView struct {
	// d is the dict itself, not a snapshot of it, so the view tracks the
	// insertions and removals that happen after it was taken.
	d    value.Value
	kind dictViewKind
}

type dictViewKind int

const (
	viewKeys dictViewKind = iota
	viewValues
	viewItems
)

func (k dictViewKind) name() string {
	switch k {
	case viewValues:
		return "dict_values"
	case viewItems:
		return "dict_items"
	}
	return "dict_keys"
}

// entries is the view's current contents, read afresh each time.
func (v *dictView) entries() []value.Value {
	d, ok := v.d.Dict()
	if !ok {
		return nil
	}
	out := make([]value.Value, 0, d.Len())
	for _, e := range d.Entries() {
		switch v.kind {
		case viewValues:
			out = append(out, e.Value)
		case viewItems:
			out = append(out, value.NewTuple(e.Key, e.Value))
		default:
			out = append(out, e.Key)
		}
	}
	return out
}

func (v *dictView) GetAttr(string) (value.Value, bool) { return value.Undefined, false }

func (v *dictView) TypeName() string { return v.kind.name() }

func (v *dictView) Len() int {
	d, ok := v.d.Dict()
	if !ok {
		return 0
	}
	return d.Len()
}

func (v *dictView) Iterate() iter.Seq[value.Value] {
	return func(yield func(value.Value) bool) {
		for _, item := range v.entries() {
			if !yield(item) {
				return
			}
		}
	}
}

func (v *dictView) Contains(item value.Value) (found, known bool) {
	// A keys view answers by lookup rather than by scanning, which is what
	// makes `k in d.keys()` cost what `k in d` costs.
	if v.kind == viewKeys {
		d, ok := v.d.Dict()
		if !ok {
			return false, true
		}
		if err := value.CheckHashable(item); err != nil {
			return false, false
		}
		_, got, err := d.Get(item)
		if err != nil {
			return false, false
		}
		return got, true
	}
	for _, have := range v.entries() {
		if value.Equal(item, have) {
			return true, true
		}
	}
	return false, true
}

func (v *dictView) Repr() string {
	var b strings.Builder
	b.WriteString(v.kind.name())
	b.WriteByte('(')
	b.WriteString(value.Repr(value.NewList(v.entries()...)))
	b.WriteByte(')')
	return b.String()
}

// Equals compares a keys or items view the way Python does, as a set. A values
// view has no __eq__ at all there, so two of them are equal only by identity --
// `{'a':1}.values() == {'a':1}.values()` is False.
func (v *dictView) Equals(other value.Value) (bool, bool) {
	o, ok := other.Interface().(*dictView)
	if !ok || o.kind != v.kind {
		return false, true
	}
	if v.kind == viewValues {
		return v == o, true
	}
	mine, theirs := v.entries(), o.entries()
	if len(mine) != len(theirs) {
		return false, true
	}
	for _, item := range mine {
		found := false
		for _, cand := range theirs {
			if value.Equal(item, cand) {
				found = true
				break
			}
		}
		if !found {
			return false, true
		}
	}
	return true, true
}
