// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import "github.com/mgilbir/gojja2/value"

// The objects a template can change in place, each copying itself for
// value.Isolate. See that function for why: a value that outlives one render
// must not carry that render's changes into the next.
//
// TestEveryObjectIsClassifiedForIsolation fails when an object type is added to
// this package without being listed as one of these or as one nothing can
// change.

// IsolateWith copies the namespace's attributes.
func (n *namespaceObject) IsolateWith(isolate func(value.Value) (value.Value, error)) (value.Value, error) {
	out := newNamespace(n.py)
	for _, e := range n.d.Entries() {
		c, err := isolate(e.Value)
		if err != nil {
			return value.Undefined, err
		}
		out.d.SetKnown(e.Key, c)
	}
	return value.FromObject(out), nil
}

// IsolateWith copies the cycler and where it is: next() and reset() change it.
func (c *cyclerObject) IsolateWith(isolate func(value.Value) (value.Value, error)) (value.Value, error) {
	items := make([]value.Value, len(c.items))
	for i, item := range c.items {
		var err error
		if items[i], err = isolate(item); err != nil {
			return value.Undefined, err
		}
	}
	return value.FromObject(&cyclerObject{items: items, pos: c.pos}), nil
}

// IsolateWith copies the joiner: calling it the first time changes it.
func (j *joinerObject) IsolateWith(isolate func(value.Value) (value.Value, error)) (value.Value, error) {
	sep, err := isolate(j.sep)
	if err != nil {
		return value.Undefined, err
	}
	return value.FromObject(&joinerObject{sep: sep, used: j.used}), nil
}

// IsolateWith is a view of the copy of its dict, which the walk's memo makes
// the same copy every other reference to that dict gets.
func (v *dictView) IsolateWith(isolate func(value.Value) (value.Value, error)) (value.Value, error) {
	d, err := isolate(v.d)
	if err != nil {
		return value.Undefined, err
	}
	return value.FromObject(&dictView{d: d, kind: v.kind, py: v.py}), nil
}

// IsolateWith is a proxy over the copy of what it wraps.
func (m *mappingProxy) IsolateWith(isolate func(value.Value) (value.Value, error)) (value.Value, error) {
	d, err := isolate(m.d)
	if err != nil {
		return value.Undefined, err
	}
	return value.FromObject(&mappingProxy{d: d, py: m.py}), nil
}

// IsolateWith copies a |groupby pair, whose list of items can be changed.
func (g *groupObject) IsolateWith(isolate func(value.Value) (value.Value, error)) (value.Value, error) {
	key, err := isolate(g.key)
	if err != nil {
		return value.Undefined, err
	}
	items, err := isolate(g.items)
	if err != nil {
		return value.Undefined, err
	}
	return value.FromObject(&groupObject{key: key, items: items, py: g.py}), nil
}

// isolate is value.IsolateIn with this render's memo: the copy every boundary
// of the render hands out for one container, made the first time any of them
// reaches it.
func (s *State) isolate(v value.Value) (value.Value, error) {
	if !value.MayBeMutable(v) {
		return v, nil
	}
	return value.IsolateIn(v, s, s.IsolationMemo())
}

// isolateGlobal is isolate for an environment global, which is copied lazily:
// the original lives as long as the environment and no render changes it, so a
// copy filled later in the render is the copy it would have been at once. A
// render whose values outlive it copies everything now.
func (s *State) isolateGlobal(v value.Value) (value.Value, error) {
	if !value.MayBeMutable(v) {
		return v, nil
	}
	if s == nil || s.eager || s.budget == nil {
		return s.isolate(v)
	}
	return value.IsolateLazy(v, s, s.IsolationMemo())
}

// IsolationMemo is this render's memo of the copies it has made of values from
// outside it, for value.MemoBudget: the value package's conversions use it so
// that a host value met through a Go method is the same copy as the one met
// through a global. A host has no reason to call it. Nil outside a render.
func (s *State) IsolationMemo() *value.Memo {
	if s == nil || s.budget == nil {
		return nil
	}
	if s.budget.isolated == nil {
		s.budget.isolated = &value.Memo{}
	}
	return s.budget.isolated
}

// callFilter calls a filter, and isolates what it returns unless it is one of
// jinja2's own. A filter the host registered may return something it keeps --
// a cached list, a shared dict -- and a template could change that in place for
// every render after it.
func (s *State) callFilter(name string, fn Filter, v value.Value, args *value.CallArgs) (value.Value, error) {
	out, err := fn(s, v, args)
	if err != nil || s.env.stockFilters[name] {
		return out, err
	}
	return s.isolate(out)
}
