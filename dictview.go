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
	// py is the interpreter being reproduced. A view implements
	// value.Object, whose Contains takes no arguments beyond the item, so
	// the version is injected here where the view is built rather than at
	// the call.
	py value.PythonVersion
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

// Unhashable reports that this view cannot be a dict key or a set element.
//
// dict_keys and dict_items compare as sets, and defining __eq__ without
// __hash__ leaves them unhashable. dict_values defines neither and so hashes by
// identity like any other object: `{{ d.values() in d }}` is a miss that
// answers False, where `{{ d.keys() in d }}` is a TypeError. Without this every
// view hashed by identity and all three answered False.
func (v *dictView) Unhashable() bool { return v.kind != viewValues }

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

// SetElements makes a keys or items view an operand of set arithmetic, which
// is what `d.keys() - xs` needs. A *values* view is deliberately not one: its
// elements need be neither unique nor hashable, and CPython refuses the
// operation on it for that reason.
func (v *dictView) SetElements() (elements []value.Value, ok bool) {
	if v.kind == viewValues {
		return nil, false
	}
	return v.entries(), true
}

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

// ContainsErr is Contains with an error channel, and the caller consults it
// *before* the item's own refusals: what a view examines decides whether those
// refusals apply at all.
//
//   - a keys view looks the item up, which hashes it, so an unhashable one is a
//     TypeError and a StrictUndefined refuses. That is also what makes
//     `k in d.keys()` cost what `k in d` costs;
//   - an items view unpacks before it looks, so anything that is not a
//     two-element pair simply is not in it -- `nope in d.items()` is False even
//     under StrictUndefined -- while the *key* of a pair is hashed, so
//     `(nope, 1) in d.items()` raises;
//   - a values view compares element by element, which scan below does.
//
// There is no Contains beside it. There was, because Container is one of the
// interfaces searchable() accepts -- but Iterate already makes a view an
// Iterable, which searchable() accepts too, and this answers before either is
// consulted. Removing it left the whole suite green.
func (v *dictView) ContainsErr(item value.Value, py value.PythonVersion) (found, known bool, err error) {
	d, ok := v.d.Dict()
	if !ok {
		return false, false, nil
	}
	if v.kind == viewValues {
		found, err := v.scan(item, py)
		return found, true, err
	}
	key := item
	if v.kind == viewItems {
		// A *tuple* of two, and nothing else: dict_items.__contains__
		// checks PyTuple_Check before the size, so a two-element list is
		// not a pair and is simply not in the view. Accepting any
		// sequence made `[['x'], 1] in d.items()` hash the inner list
		// and refuse where CPython answers False.
		pair, ok := item.Seq()
		if !ok || item.Kind() != value.KindTuple || pair.Len() != 2 {
			return false, true, nil
		}
		key = pair.At(0)
	}
	if err := value.CheckHashable(key, py, value.AsDictKey); err != nil {
		return false, true, err
	}
	got, ok, err := d.Get(key, py)
	if err != nil || !ok {
		return false, true, err
	}
	if v.kind == viewKeys {
		return true, true, nil
	}
	pair, _ := item.Seq()
	eq, err := value.EqualErr(pair.At(1), got, py)
	return eq, true, err
}

// scan is the values view's element-by-element search, which is a real `==` per
// element -- so a StrictUndefined among the *values* refuses rather than
// answering False. `{% set q = {'a': nope} %}{{ 1 in q.values() }}` answered
// False because the scan compared with a form that has nowhere to put an error.
func (v *dictView) scan(item value.Value, py value.PythonVersion) (bool, error) {
	for _, have := range v.entries() {
		eq, err := value.EqualErr(item, have, py)
		if err != nil {
			return false, err
		}
		if eq {
			return true, nil
		}
	}
	return false, nil
}

func (v *dictView) Repr() string {
	var b strings.Builder
	b.WriteString(v.kind.name())
	b.WriteByte('(')
	b.WriteString(value.ReprFor(value.NewList(v.entries()...), v.py))
	b.WriteByte(')')
	return b.String()
}

// EqualsErr compares a keys or items view the way Python does, as a set. A
// values view has no __eq__ at all there, so two of them are equal only by
// identity -- `{'a':1}.values() == {'a':1}.values()` is False.
//
// The set comparison compares elements, so a StrictUndefined among them refuses:
// an items view carries the dict's values and `{'a': nope}.items() ==
// {'a': 1}.items()` raises, where a keys view carries only the keys and answers
// True. This compared with a form that has nowhere to put an error and answered
// False for both.
func (v *dictView) EqualsErr(other value.Value, py value.PythonVersion) (bool, bool, error) {
	o, ok := other.Interface().(*dictView)
	if !ok || o.kind != v.kind {
		return false, true, nil
	}
	if v.kind == viewValues {
		return v == o, true, nil
	}
	mine, theirs := v.entries(), o.entries()
	if len(mine) != len(theirs) {
		return false, true, nil
	}
	for _, item := range mine {
		found := false
		for _, cand := range theirs {
			eq, err := value.EqualErr(item, cand, py)
			if err != nil {
				return false, true, err
			}
			if eq {
				found = true
				break
			}
		}
		if !found {
			return false, true, nil
		}
	}
	return true, true, nil
}
