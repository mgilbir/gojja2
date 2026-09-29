// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"iter"
	"math"
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

func (v *dictView) GetAttr(name string) (value.Value, bool) {
	switch name {
	case "mapping":
		// Every view carries a read-only proxy of the dict it came from
		// (3.10). `{{ d.keys().mapping }}` printed nothing here.
		return value.FromObject(&mappingProxy{d: v.d, py: v.py}), true
	}
	// Only the set-like views have a method at all.
	if v.kind == viewValues {
		return value.Undefined, false
	}
	return boundObjectMethod(dictViewMethods, v.kind.name(), name,
		value.FromObject(v), v.py)
}

// boundObjectMethod is builtinMethod for a type of gojja2's own: the table
// decides which names exist, and the arity table -- keyed by the name CPython
// calls the type -- decides what the count errors say.
func boundObjectMethod(table map[string]func(*State, value.Value, *value.CallArgs) (value.Value, error),
	typeName, name string, recv value.Value, py value.PythonVersion) (value.Value, bool) {
	fn, ok := table[name]
	if !ok {
		return value.Undefined, false
	}
	return Func(name, func(s *State, a *value.CallArgs) (value.Value, error) {
		if err := checkMethodArity(typeName, name, a, py); err != nil {
			return value.Undefined, err
		}
		return fn(s, recv, a)
	}), true
}

// isdisjoint walks the argument and asks the view about each element, which is
// what decides the refusals: a keys view hashes what it is given, so an
// unhashable element is a TypeError, while an items view unpacks first and a
// two-element *list* is simply not in it.
func (v *dictView) isdisjoint(s *State, other value.Value) (value.Value, error) {
	seq, err := value.Iterate(other)
	if err != nil {
		if refusal := value.StrictRefusal(other); refusal != nil {
			return value.Undefined, refusal
		}
		return value.Undefined, err
	}
	for item := range seq {
		if err := s.Step(1); err != nil {
			return value.Undefined, err
		}
		found, _, err := v.ContainsErr(item, v.py)
		if err != nil {
			return value.Undefined, err
		}
		if found {
			return value.False, nil
		}
	}
	return value.True, nil
}

// mappingProxy is types.MappingProxyType: the read-only dict every view carries
// as `.mapping`. It is a dict in nearly every way a template can observe -- it
// indexes, iterates, sizes, compares equal to the dict itself and is `is
// mapping` -- and differs in three: its repr says so, it has only the five
// read-only methods, and it hashes like the dict, which is to say not at all.
type mappingProxy struct {
	// d is the dict itself, so the proxy tracks it, which is the whole
	// point of a proxy.
	d  value.Value
	py value.PythonVersion
}

// GetAttr: the five read-only methods, keyed to this type because the wording
// is -- `m.copy(1)` is "mappingproxy.copy() takes no arguments (1 given)" where
// the dict's own says "dict.copy()". `m.mapping` does not exist: a proxy of a
// proxy is not a thing.
func (m *mappingProxy) GetAttr(name string) (value.Value, bool) {
	return boundObjectMethod(mappingProxyMethods, "mappingproxy", name,
		value.FromObject(m), m.py)
}

// The proxy delegates to whatever it wraps, because mappingproxy_subscript is
// `PyObject_GetItem(pp->mapping, key)` and nothing more. The constructor takes
// what PyMapping_Check does minus list and tuple, so that is a dict, a string,
// a bytes, a range, another proxy or an undefined -- and each indexes here as
// it would on its own.
func (m *mappingProxy) GetItem(key value.Value) (value.Value, bool) {
	if d, ok := m.d.Dict(); ok {
		v, found, err := d.Get(key, m.py)
		if err != nil || !found {
			return value.Undefined, false
		}
		return v, true
	}
	if inner, ok := m.d.Interface().(value.Mapping); ok {
		return inner.GetItem(key)
	}
	// An undefined raises from its own __getitem__, whatever the key is.
	// This interface has nowhere to put that, so it answers a miss and
	// GetItemErr below is what the evaluator asks.
	if m.d.IsUndefined() {
		return value.Undefined, false
	}
	switch m.d.Kind() {
	case value.KindString:
		i, ok := key.Int64()
		if !ok {
			return value.Undefined, false
		}
		runes := []rune(m.d.AsString())
		if i < 0 {
			i += int64(len(runes))
		}
		if i < 0 || i >= int64(len(runes)) {
			return value.Undefined, false
		}
		return value.String(string(runes[i])), true
	case value.KindBytes:
		// A bytes indexes to the integer byte, as everywhere else.
		i, ok := key.Int64()
		if !ok {
			return value.Undefined, false
		}
		raw := m.d.AsString()
		if i < 0 {
			i += int64(len(raw))
		}
		if i < 0 || i >= int64(len(raw)) {
			return value.Undefined, false
		}
		return value.Int(int64(raw[i])), true
	}
	if seq, ok := m.d.Interface().(value.Sequence); ok {
		i, ok := key.Int64()
		if !ok {
			return value.Undefined, false
		}
		if i < 0 {
			i += int64(seq.Len())
		}
		if i < 0 || i > math.MaxInt32 {
			return value.Undefined, false
		}
		return seq.GetIndex(int(i))
	}
	// An object whose __getitem__ gojja2 answers through the attribute
	// path -- `self['body']` is the one -- indexes through the proxy the
	// same way, because the proxy only forwards the subscript.
	if key.Kind() == value.KindString {
		return lookupAttr(nil, m.d, key.AsString())
	}
	return value.Undefined, false
}

// GetItemErr is [mappingProxy.GetItem] for the one wrapped value whose
// subscript raises rather than answering.
//
// `mappingproxy(nope)[0]` is the undefined's own error, because
// mappingproxy_subscript is PyObject_GetItem and Undefined.__getitem__ is
// _fail_with_undefined_error. Reported through a sibling with somewhere to put
// it, as ContainsErr is: the plain Mapping interface can only say "no such
// key", and that renders as nothing.
func (m *mappingProxy) GetItemErr(key value.Value) (value.Value, bool, error) {
	if m.d.IsUndefined() {
		return value.Undefined, true, m.d.UndefinedError()
	}
	v, ok := m.GetItem(key)
	return v, ok, nil
}

func (m *mappingProxy) Keys() []value.Value {
	if d, ok := m.d.Dict(); ok {
		out := make([]value.Value, 0, d.Len())
		for _, e := range d.Entries() {
			out = append(out, e.Key)
		}
		return out
	}
	if inner, ok := m.d.Interface().(value.Mapping); ok {
		return inner.Keys()
	}
	// A string, whose keys are what iterating it yields.
	seq, err := value.Iterate(m.d)
	if err != nil {
		return nil
	}
	var out []value.Value
	for item := range seq {
		out = append(out, item)
	}
	return out
}

func (m *mappingProxy) Len() int {
	if d, ok := m.d.Dict(); ok {
		return d.Len()
	}
	n, err := value.Len(m.d)
	if err != nil {
		return 0
	}
	return n
}

func (m *mappingProxy) Iterate() iter.Seq[value.Value] {
	return func(yield func(value.Value) bool) {
		for _, k := range m.Keys() {
			if !yield(k) {
				return
			}
		}
	}
}

// Unhashable: a proxy hashes exactly as well as what it wraps, which is to say
// `{{ m in d }}` is "unhashable type: 'dict'".
func (m *mappingProxy) Unhashable() bool { return true }

func (m *mappingProxy) TypeName() string { return "mappingproxy" }

// UnhashableAs names what the refusal complains about, which 3.12 changed:
// before it, the proxy itself. The *outer* name -- 3.14's "cannot use 'X' as a
// dict key" half -- stays the proxy either way.
func (m *mappingProxy) UnhashableAs() value.Value {
	if m.py.ProxyHashNamesTheMapping() {
		return m.d
	}
	return value.FromObject(m)
}

// Str is the dict's, and Repr is not: `{{ m }}` prints `{'a': 1}` where
// `{{ m|pprint }}` prints `mappingproxy({'a': 1})`.
func (m *mappingProxy) Str() string { return value.StrFor(m.d, m.py) }

func (m *mappingProxy) Repr() string {
	return "mappingproxy(" + value.ReprFor(m.d, m.py) + ")"
}

// EqualsErr compares what is behind the proxy: `m == d` and `d == m` are both
// True, because mappingproxy delegates __eq__ to the mapping it wraps.
func (m *mappingProxy) EqualsErr(other value.Value, py value.PythonVersion) (bool, bool, error) {
	if o, ok := other.Interface().(*mappingProxy); ok {
		other = o.d
	}
	eq, err := value.EqualErr(m.d, other, py)
	return eq, true, err
}

// ContainsErr delegates, because mappingproxy_check_key is
// `PySequence_Contains(pp->mapping, key)` -- the wrapped object answers, with
// its own rules and its own refusals. A proxy over a string is a string here:
// `0 in mappingproxy('ab')` is "'in <string>' requires string as left operand"
// and `'a' in mappingproxy('ab')` is True, where hashing the key and looking it
// up as a dict would made the first True and the second False.
func (m *mappingProxy) ContainsErr(item value.Value, py value.PythonVersion) (found, known bool, err error) {
	if d, ok := m.d.Dict(); ok {
		if err := value.CheckHashable(item, py, value.AsDictKey); err != nil {
			return false, true, err
		}
		_, got, err := d.Get(item, py)
		return got, true, err
	}
	got, err := value.Contains(item, m.d, nil, py)
	return got, true, err
}

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
	eq, err := value.EqualBoolErr(pair.At(1), got, py)
	return eq, true, err
}

// scan is the values view's element-by-element search, which is a real `==` per
// element -- so a StrictUndefined among the *values* refuses rather than
// answering False. `{% set q = {'a': nope} %}{{ 1 in q.values() }}` answered
// False because the scan compared with a form that has nowhere to put an error.
func (v *dictView) scan(item value.Value, py value.PythonVersion) (bool, error) {
	for _, have := range v.entries() {
		// The element on the left, as every containment scan in
		// CPython has it.
		eq, err := value.EqualBoolErr(have, item, py)
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

// EqualsErr is a view's own opinion about ==, and by the time it is asked the
// interesting case has already been answered.
//
// Two keys views, or two items views, are both set-like, so `equalAsSets` in
// the value package compares them as sets: the lengths, then every element of
// one looked for in the other *through the view*. That is what makes
// `{'a': nope}.items() == {'a': 1}.items()` raise while the keys of the same
// two answer True -- an items view carries the values and a keys view does not.
// This is reached only when that did not apply: a values view, which
// PyDictViewSet_Check refuses, or a view against something that is not a view.
//
// So what is left is identity, which is what CPython has for a values view:
// dict_values defines no __eq__, so `{'a':1}.values() == {'a':1}.values()` is
// False. The element scan that used to be here was a second copy of the set
// comparison and could not run -- it asks about two same-kind views, and no
// pair of those gets this far. A panic in its place survived the suite, 4,784
// corpus cases and a 30,000-template soak.
func (v *dictView) EqualsErr(other value.Value, _ value.PythonVersion) (bool, bool, error) {
	o, ok := other.Interface().(*dictView)
	if !ok || o.kind != v.kind {
		return false, true, nil
	}
	return v == o, true, nil
}
