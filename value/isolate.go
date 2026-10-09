// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package value

import (
	"reflect"
	"slices"
)

// Isolator is an Object a template can change in place -- a namespace, a cycler,
// a set -- that knows how to copy itself. Isolate asks it to, handing it the
// function to copy what it holds with, so the copy shares one memo with the rest
// of the walk.
type Isolator interface {
	Object
	IsolateWith(isolate func(Value) (Value, error)) (Value, error)
}

// MemoBudget is a Budget that keeps a render's isolation memo, which a
// conversion walking Go data for that render uses for the Values it meets in
// it: a host method returning the list it keeps then hands the render one copy
// of it however often it is called. See IsolateIn.
type MemoBudget interface {
	Budget
	IsolationMemo() map[any]Value
}

// Isolate returns v with every part a template could change in place replaced
// by a copy: lists, dicts, sets and every Isolator, at any depth. What nothing
// can change -- strings, numbers, Markup, bytes, tuples of those, functions,
// host objects -- is returned as it is, and v itself when nothing in it needed
// copying.
//
// It is what keeps one render's changes out of every other's. A value that
// reaches a template from somewhere longer-lived than the render -- an
// environment global, a value.Value inside Render's variables, what a host
// function returns, a constant folded into the compiled template -- is shared
// by every render that reaches it, and a template can append to a list, update
// a dict or advance a cycler. jinja2 lets those changes persist into the next
// render and the next tenant; gojja2 gives each render its own copy.
//
// Two references to one container stay one container in the copy, and a cycle
// stays a cycle. Each container copied is charged to b, so a huge value is not
// an unbounded walk.
func Isolate(v Value, b Budget) (Value, error) {
	return IsolateIn(v, b, nil)
}

// IsolateIn is Isolate remembering its copies in memo, keyed by the container
// copied, so that every isolation sharing memo hands back the same copy of the
// same container. A render keeps one: a global reached twice, or a host method
// that returns the list it keeps on every call, is one copy throughout the
// render -- an append through one reference is seen through the other, as in
// jinja2 -- and a fresh one in the next render. A nil memo is a fresh one.
func IsolateIn(v Value, b Budget, memo map[any]Value) (Value, error) {
	if !MayBeMutable(v) {
		return v, nil
	}
	if memo == nil {
		memo = map[any]Value{}
	} else if done, ok := remembered(v, memo); ok {
		// A container this render has copied already: the common case
		// for a host function returning what it keeps, called in a loop.
		return done, nil
	}
	iso := &isolator{b: b, memo: memo}
	return iso.isolate(v)
}

// remembered is v's copy in memo, if v is a container already copied there.
func remembered(v Value, memo map[any]Value) (Value, bool) {
	var key any
	switch v.kind {
	case KindList, KindTuple:
		key = v.obj
	case KindDict:
		d, _ := v.Dict()
		key = d
	case KindObject:
		k, ok := memoKey(v.obj)
		if !ok {
			return Value{}, false
		}
		key = k
	default:
		return Value{}, false
	}
	done, ok := memo[key]
	return done, ok
}

// MayBeMutable reports whether v could hold something a template can change in
// place. It answers without walking, so false is certain and true is "Isolate
// would have to look".
func MayBeMutable(v Value) bool {
	switch v.kind {
	case KindList, KindTuple, KindDict:
		return true
	case KindObject:
		_, isolator := v.obj.(Isolator)
		_, set := v.obj.(*Set)
		return isolator || set
	}
	return false
}

type isolator struct {
	b    Budget
	memo map[any]Value
	// lazy copies a dict by reference to the original, filled when first
	// needed, rather than at once; see IsolateLazy.
	lazy bool
}

// isolate returns v's copy, and whether it is one: a container whose contents
// needed nothing copied is still a container a template could change, so only
// a tuple -- which cannot -- comes back as itself.
func (iso *isolator) isolate(v Value) (Value, error) {
	c, _, err := iso.walk(v)
	return c, err
}

func (iso *isolator) walk(v Value) (out Value, copied bool, err error) {
	switch v.kind {
	case KindList:
		s := v.obj.(*Seq)
		if done, ok := iso.memo[s]; ok {
			return done, true, nil
		}
		if err := chargeItems(iso.b, int64(len(s.items))); err != nil {
			return Undefined, false, err
		}
		seq := &Seq{items: make([]Value, len(s.items))}
		out = Value{kind: KindList, obj: seq}
		iso.memo[s] = out // before the elements, so a cycle closes on the copy
		for i, item := range s.items {
			if seq.items[i], _, err = iso.walk(item); err != nil {
				return Undefined, false, err
			}
		}
		return out, true, nil
	case KindTuple:
		// A tuple cannot change, but what it holds can. It is copied only
		// when something inside it was, and a tuple cannot close a cycle
		// without a list or a dict in it, which the memo already holds.
		s := v.obj.(*Seq)
		var items []Value
		for i, item := range s.items {
			c, changed, err := iso.walk(item)
			if err != nil {
				return Undefined, false, err
			}
			if changed && items == nil {
				if err := chargeItems(iso.b, int64(len(s.items))); err != nil {
					return Undefined, false, err
				}
				items = make([]Value, i, len(s.items))
				copy(items, s.items[:i])
			}
			if items != nil {
				items = append(items, c)
			}
		}
		if items == nil {
			return v, false, nil
		}
		return NewTuple(items...), true, nil
	case KindDict:
		d, _ := v.Dict()
		if done, ok := iso.memo[d]; ok {
			return done, true, nil
		}
		if iso.lazy {
			// Charged now, for the entries the fill will hold: by the
			// time it runs there may be nowhere to report a refusal.
			if err := chargeItems(iso.b, int64(d.Len())); err != nil {
				return Undefined, false, err
			}
			ld := &lazyDict{p: pendingDict{from: d, iso: iso}}
			ld.pending = &ld.p
			out = Value{kind: KindDict, obj: &ld.Dict}
			iso.memo[d] = out
			return out, true, nil
		}
		entries := d.Entries()
		if err := chargeItems(iso.b, int64(len(entries))); err != nil {
			return Undefined, false, err
		}
		out = NewDict()
		target, _ := out.Dict()
		iso.memo[d] = out
		// Keys are hashable, so nothing in one can change.
		for _, e := range entries {
			c, _, err := iso.walk(e.Value)
			if err != nil {
				return Undefined, false, err
			}
			target.SetKnown(e.Key, c)
		}
		return out, true, nil
	case KindObject:
		key, remembered := memoKey(v.obj)
		if remembered {
			if done, ok := iso.memo[key]; ok {
				return done, true, nil
			}
		}
		switch o := v.obj.(type) {
		case *Set:
			if err := chargeItems(iso.b, int64(len(o.items))); err != nil {
				return Undefined, false, err
			}
			// Set elements are hashable, so the set's storage is all
			// that needs copying.
			out = FromObject(&Set{items: slices.Clone(o.items), index: o.index.clone()})
			iso.memo[key] = out
			return out, true, nil
		case Isolator:
			if out, err = o.IsolateWith(iso.isolate); err != nil {
				return Undefined, false, err
			}
			if remembered {
				iso.memo[key] = out
			}
			return out, true, nil
		}
	}
	return v, false, nil
}

// memoKey is the identity an object is remembered by: the pointer it is held
// through. An object held by value has no identity to share, and may not even
// be comparable, so it is not remembered.
func memoKey(obj any) (any, bool) {
	if reflect.ValueOf(obj).Kind() == reflect.Pointer {
		return obj, true
	}
	return nil, false
}

// clone is a Dict with the same entries in a storage of its own.
func (d *Dict) clone() *Dict {
	out := &Dict{}
	for _, e := range d.Entries() {
		out.SetKnown(e.Key, e.Value)
	}
	return out
}

// IsolateLazy is IsolateIn copying a dict only as far as a render reaches into
// it. A dict comes back as a copy that is filled from the original the first
// time something needs more than its length or one value by str key, and what
// it holds is copied the same way when it is reached; a list, a set or an
// Isolator is copied at once, one level deep, with any dict inside it left to
// copy itself lazily. So a render that reads three fields of a fifty-key global
// copies those, not fifty -- while a template that changes it still changes its
// own copy and nothing else.
//
// Each copy is charged when it is made. The copies belong to the render b
// bounds and memo serves: they read the original, and are filled through memo,
// when first needed, so nothing may use them once that render is over --
// anything that outlives it wants IsolateIn, which copies everything at once.
func IsolateLazy(v Value, b Budget, memo map[any]Value) (Value, error) {
	if !MayBeMutable(v) {
		return v, nil
	}
	if memo == nil {
		memo = map[any]Value{}
	} else if done, ok := remembered(v, memo); ok {
		return done, nil
	}
	return (&isolator{b: b, memo: memo, lazy: true}).isolate(v)
}

// lazyOrUndefined is lazy for a read that has no error to return: a refused
// charge is remembered by the budget, which fails the render, and the template
// is given nothing rather than the original it must not change.
func (iso *isolator) lazyOrUndefined(v Value) Value {
	c, err := iso.isolate(v)
	if err != nil {
		return Undefined
	}
	return c
}

// fillCopy fills d, a lazy copy, from the dict it copies: the same keys, and a
// copy of every value something could change. The entries were charged when d
// was made.
func (iso *isolator) fillCopy(d *Dict, from *Dict) {
	entries := from.Entries()
	d.Reserve(len(entries))
	for _, e := range entries {
		c := e.Value
		if MayBeMutable(c) {
			c = iso.lazyOrUndefined(c)
		}
		d.SetKnown(e.Key, c)
	}
}
