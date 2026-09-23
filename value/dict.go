// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package value

import (
	"fmt"
	"math"
	"math/big"
	"strconv"

	"github.com/mgilbir/gojja2/errs"
)

// Dict is a Python dict: insertion ordered, keyed by any hashable value.
//
// Keying on the *value* rather than on a Go string is what makes
// `{% set d = {1: "a"} %}{{ d[1] }}` work. Implementations that stringify keys
// silently return nothing for integer, float, tuple and None keys.
type Dict struct {
	entries []DictEntry
	// strIdx holds the string keys and index the rest.
	//
	// Every key a template writes as an attribute, and every key of a Go
	// map handed in as context, is a string, so that is the case worth
	// keeping cheap. A hashKey is forty bytes -- a kind, an int, a float
	// and a string header -- and Go hashes all of it, so a string key was
	// paying to hash twenty-four bytes of mostly-zero fields and to build
	// the struct first. Keying strings by themselves skips both, and the
	// hash() call with them.
	//
	// The two cannot be one map, and not only for speed: a bytes key
	// carries its bytes in the same field a string key uses, and they must
	// not collide.
	strIdx map[string]int
	index  map[hashKey]int
}

// smallDict is how many entries a dict holds before it builds a string index.
//
// Below it the entries are scanned instead. A map costs an allocation and a
// hash per lookup to save a comparison per entry, which does not pay for four
// fields -- and four fields is what a record handed in as context looks like.
// Converting a page of them was allocating one map per row.
const smallDict = 8

// scanString finds a string key by walking the entries.
//
// Only a str can equal a str in Python, so the walk can compare the raw
// strings and skip every entry of another kind outright.
func (d *Dict) scanString(key string) (int, bool) {
	for i := range d.entries {
		if e := &d.entries[i]; e.Key.kind == KindString && e.Key.str == key {
			return i, true
		}
	}
	return 0, false
}

// indexStrings builds the string index once a dict is big enough to want one.
//
// It indexes the entries already there. Allocating an empty map instead would
// hide every one of them: the lookup takes a non-nil index to mean the index
// is authoritative, and stops scanning.
func (d *Dict) indexStrings(capacity int) {
	d.strIdx = make(map[string]int, max(capacity, len(d.entries)+1))
	for i := range d.entries {
		if e := &d.entries[i]; e.Key.kind == KindString {
			d.strIdx[e.Key.str] = i
		}
	}
}

// lookupIdx finds the entry position for key.
func (d *Dict) lookupIdx(key Value, py PythonVersion, use HashUse) (int, bool, error) {
	if key.kind == KindString {
		if d.strIdx == nil {
			i, ok := d.scanString(key.str)
			return i, ok, nil
		}
		i, ok := d.strIdx[key.str]
		return i, ok, nil
	}
	h, err := hash(key, py, use)
	if err != nil {
		return 0, false, err
	}
	i, ok := d.index[h]
	return i, ok, nil
}

// lookupKnown and storeKnown are lookupIdx and storeIdx for a key that cannot
// fail to hash -- a Go string, or one already in the dict. Splitting them out
// keeps the interpreter version out of the signatures it cannot affect, so a
// signature that does carry it means something.
func (d *Dict) lookupKnown(key Value) (int, bool) {
	i, ok, err := d.lookupIdx(key, DefaultPythonVersion, AsDictKey)
	if err != nil {
		panic("value: lookupKnown on an unhashable key: " + err.Error())
	}
	return i, ok
}

func (d *Dict) storeKnown(key Value, i int) {
	if err := d.storeIdx(key, i, DefaultPythonVersion, AsDictKey); err != nil {
		panic("value: storeKnown on an unhashable key: " + err.Error())
	}
}

// storeIdx records that key lives at position i.
func (d *Dict) storeIdx(key Value, i int, py PythonVersion, use HashUse) error {
	if key.kind == KindString {
		if d.strIdx == nil {
			// i is the position this key is about to occupy, so the
			// dict is about to hold i+1 entries.
			if i+1 < smallDict {
				return nil
			}
			d.indexStrings(0)
		}
		d.strIdx[key.str] = i
		return nil
	}
	h, err := hash(key, py, use)
	if err != nil {
		return err
	}
	if d.index == nil {
		d.index = make(map[hashKey]int)
	}
	d.index[h] = i
	return nil
}

// DictEntry is one key/value pair, in insertion order.
type DictEntry struct {
	Key   Value
	Value Value
}

// NewDict returns an empty dict.
func NewDict() Value { return Value{kind: KindDict, obj: &Dict{}} }

// DictOf builds a dict from alternating key/value pairs.
//
// It reports an odd number of arguments, and a key Python would refuse to
// hash, rather than panicking on either: a caller building a dict from data it
// did not write cannot know in advance that every key is hashable.
func DictOf(py PythonVersion, kv ...Value) (Value, error) {
	if len(kv)%2 != 0 {
		return Undefined, errs.New(errs.TypeError,
			"DictOf needs an even number of arguments, got %d", len(kv))
	}
	v := NewDict()
	d, _ := v.Dict()
	d.Reserve(len(kv) / 2)
	for i := 0; i < len(kv); i += 2 {
		if err := d.Set(kv[i], kv[i+1], py); err != nil {
			return Undefined, err
		}
	}
	return v, nil
}

// StringDict builds a dict with str keys, in the order given by keys.
func StringDict(keys []string, vals []Value) Value {
	v := NewDict()
	d, _ := v.Dict()
	for i, k := range keys {
		d.SetKnown(String(k), vals[i])
	}
	return v
}

// Len is the number of entries.
func (d *Dict) Len() int { return len(d.entries) }

// Entries returns the entries in insertion order. Callers must not retain the
// slice across a mutation.
func (d *Dict) Entries() []DictEntry { return d.entries }

// Keys returns the keys in insertion order.
func (d *Dict) Keys() []Value {
	keys := make([]Value, len(d.entries))
	for i, e := range d.entries {
		keys[i] = e.Key
	}
	return keys
}

// Values returns the values in insertion order.
func (d *Dict) Values() []Value {
	vals := make([]Value, len(d.entries))
	for i, e := range d.entries {
		vals[i] = e.Value
	}
	return vals
}

// Get looks up key. An unhashable key is reported as an error rather than a
// miss, because Python raises TypeError for it.
func (d *Dict) Get(key Value, py PythonVersion) (Value, bool, error) {
	i, ok, err := d.lookupIdx(key, py, AsDictKey)
	if err != nil || !ok {
		return Undefined, false, err
	}
	return d.entries[i].Value, true, nil
}

// GetString is the common case: lookup by a str key.
func (d *Dict) GetString(key string) (Value, bool) {
	var (
		i  int
		ok bool
	)
	if d.strIdx == nil {
		i, ok = d.scanString(key)
	} else {
		i, ok = d.strIdx[key]
	}
	if !ok {
		return Undefined, false
	}
	return d.entries[i].Value, true
}

// GetKnown is Get for a key that cannot fail to hash. See hashKnown.
func (d *Dict) GetKnown(key Value) (Value, bool) {
	i, ok := d.lookupKnown(key)
	if !ok {
		return Undefined, false
	}
	return d.entries[i].Value, true
}

// SetKnown is Set for a key that cannot fail to hash. See hashKnown.
func (d *Dict) SetKnown(key, val Value) { d.setKnown(key, val) }

// Set inserts or replaces key.
//
// Replacing an existing entry keeps the original key object and position, so
// {1: "a", True: "c"} is {1: 'c'} -- key 1, value from the later assignment --
// exactly as CPython reports it.
func (d *Dict) Set(key, val Value, py PythonVersion) error {
	i, ok, err := d.lookupIdx(key, py, AsDictKey)
	if err != nil {
		return err
	}
	if ok {
		d.entries[i].Value = val
		return nil
	}
	d.storeKnown(key, len(d.entries))
	d.entries = append(d.entries, DictEntry{Key: key, Value: val})
	return nil
}

// setKnown and storeKnown are Set and storeIdx for a key that cannot fail to
// hash. See hashKnown.
func (d *Dict) setKnown(key, val Value) {
	if i, ok := d.lookupKnown(key); ok {
		d.entries[i].Value = val
		return
	}
	d.storeKnown(key, len(d.entries))
	d.entries = append(d.entries, DictEntry{Key: key, Value: val})
}

// Reserve makes room for n entries.
//
// Filling a dict of known size otherwise pays twice for growing: the entries
// slice doubles its way up from nothing, and the index map rehashes as it
// fills. Converting a caller's map[string]any is the common case -- a page
// with fifty rows of four fields was doing it two hundred times.
func (d *Dict) Reserve(n int) {
	if n <= 0 {
		return
	}
	if cap(d.entries)-len(d.entries) < n {
		grown := make([]DictEntry, len(d.entries), len(d.entries)+n)
		copy(grown, d.entries)
		d.entries = grown
	}
	// The string index is the one sized ahead, when there will be enough
	// entries to want one: the caller that reserves is converting a Go
	// map, whose keys are all strings. A dict that turns out to hold other
	// kinds builds the second map when it meets one.
	if d.strIdx == nil && len(d.entries)+n >= smallDict {
		d.indexStrings(len(d.entries) + n)
	}
}

// SetString inserts or replaces a str key.
func (d *Dict) SetString(key string, val Value) { d.setKnown(String(key), val) }

// setFresh inserts a key the caller knows is not present yet.
//
// Set has to ask the index whether the key is already there, which costs a
// second hash and a second probe of the same map it is about to write to. A
// dict being filled from a Go map cannot have a duplicate -- the keys came
// from a map -- so that question has a known answer, and filling a page's
// worth of records asked it once per field for nothing.
func (d *Dict) setFresh(key, val Value) error { //nolint:unparam // error kept for the caller's shape
	// storeIdx builds whichever index it needs, so this does not rest on
	// the caller having reserved -- writing to a nil map panics, and a
	// second caller added later would find that out the hard way.
	d.storeKnown(key, len(d.entries))
	d.entries = append(d.entries, DictEntry{Key: key, Value: val})
	return nil
}

// DeleteKnown is Delete for a key that cannot fail to hash -- one that is
// already in the dict. See hashKnown.
func (d *Dict) DeleteKnown(key Value) bool {
	ok, err := d.Delete(key, DefaultPythonVersion)
	if err != nil {
		panic("value: DeleteKnown on an unhashable key: " + err.Error())
	}
	return ok
}

// Delete removes key, reporting whether it was present.
func (d *Dict) Delete(key Value, py PythonVersion) (bool, error) {
	i, ok, err := d.lookupIdx(key, py, AsDictKey)
	if err != nil || !ok {
		return false, err
	}
	d.entries = append(d.entries[:i], d.entries[i+1:]...)
	if key.kind == KindString {
		if d.strIdx != nil {
			delete(d.strIdx, key.str)
		}
	} else {
		// The key is already in the dict, so it hashed once and cannot
		// fail now.
		delete(d.index, hashKnown(key))
	}
	// Entries after the hole shifted down by one, in both indexes -- they
	// number positions in one shared entry list.
	for k, j := range d.strIdx {
		if j > i {
			d.strIdx[k] = j - 1
		}
	}
	for k, j := range d.index {
		if j > i {
			d.index[k] = j - 1
		}
	}
	return true, nil
}

// Clone returns a shallow copy.
func (d *Dict) Clone() Value {
	out := &Dict{entries: append([]DictEntry(nil), d.entries...)}
	if d.strIdx != nil {
		out.strIdx = make(map[string]int, len(d.strIdx))
		for k, v := range d.strIdx {
			out.strIdx[k] = v
		}
	}
	if d.index != nil {
		out.index = make(map[hashKey]int, len(d.index))
		for k, v := range d.index {
			out.index[k] = v
		}
	}
	return Value{kind: KindDict, obj: out}
}

// --- hashing -----------------------------------------------------------------

// hashKey is the normalised, Go-comparable form of a Python hashable value.
//
// Normalisation is what gives Python's cross-type key identity: True, 1 and
// 1.0 all reduce to the same integer key, so a dict written with one can be
// read with any of them.
type hashKey struct {
	kind Kind
	num  int64
	flt  float64
	str  string
}

// hash is Python's hash(), reduced to a Go-comparable key.
//
// A tuple's key is built from its elements', and a tuple may hold tuples
// without limit, so that walk runs on an explicit stack. The nesting is chosen
// at render time -- `{% for %}{% set ns.t = (ns.t,) %}{% endfor %}` builds one
// as deep as the loop is long -- and a recursive walk died on a deep one with
// `fatal error: stack overflow`, which is not a panic and so is not something
// the render can report. CPython has no wall of its own here: its tuple hash
// is iterative, and it hashes a 65,000-deep tuple without complaint.
// HashUse is what a value was about to be used as, which from 3.14 on is part
// of the message when it turns out not to be hashable: "cannot use 'list' as a
// dict key". The hashing code cannot know this, so it is passed in.
type HashUse string

const (
	// AsDictKey covers a mapping key and anything that becomes one, which
	// includes `x in d`.
	AsDictKey HashUse = "a dict key"
	// AsSetElement covers set membership, which is what |unique and the
	// `in` of a set reach.
	AsSetElement HashUse = "a set element"
)

// errUnhashable words the refusal for the chosen interpreter. Before 3.14 it
// named only the type that could not be hashed; 3.14 also names the value the
// key was -- which for a tuple containing a list is the tuple, while the
// unhashable type is still the list.
// ErrUnhashable words an unhashable-key refusal where the outer container is
// known by name rather than as a value -- a template name goes into jinja2's
// cache key, which is a tuple that never exists here as a Value.
func ErrUnhashable(outerType string, inner Value, py PythonVersion, use HashUse) error {
	if py.UnhashableNamesTheUse() {
		return errs.New(errs.TypeError, "cannot use '%s' as %s (unhashable type: '%s')",
			outerType, string(use), inner.TypeName())
	}
	return errs.New(errs.TypeError, "unhashable type: '%s'", inner.TypeName())
}

func errUnhashable(outer, inner Value, py PythonVersion, use HashUse) error {
	return ErrUnhashable(outer.TypeName(), inner, py, use)
}

// hashKnown is hash for a key that cannot fail: a Go string, or one that
// already hashed successfully when it was first stored. Those paths take no
// version because none of them can reach a message that depends on one --
// which is the whole point of passing it explicitly everywhere else.
func hashKnown(v Value) hashKey {
	h, err := hash(v, DefaultPythonVersion, AsDictKey)
	if err != nil {
		// Unreachable: callers pass a string or a key already in the
		// dict. Panicking beats returning a zero key, which would
		// silently collide every unhashable value into one slot.
		panic("value: hashKnown on an unhashable key: " + err.Error())
	}
	return h
}

func hash(v Value, py PythonVersion, use HashUse) (hashKey, error) {
	// StrictUndefined defines __hash__ as a failure, so anything that
	// hashes one -- a dict key, a set member, `value in env.filters`
	// behind the `filter` test -- raises rather than answering.
	if err := StrictRefusal(v); err != nil {
		return hashKey{}, err
	}
	if items, ok := tupleItems(v); ok {
		return hashTuple(items, v, py, use)
	}
	return hashScalar(v, v, py, use)
}

// Hashable reports what Python's hash() would refuse about v, and nil when it
// would not. A tuple is hashable exactly when its elements are.
func Hashable(v Value, py PythonVersion, use HashUse) error {
	_, err := hash(v, py, use)
	return err
}

// tupleItems reports the elements v hashes as a tuple over. A tuple subclass
// hashes as its tuple, so one holding a list is unhashable just as a plain
// tuple would be.
func tupleItems(v Value) ([]Value, bool) {
	switch v.kind {
	case KindTuple:
		s, _ := v.Seq()
		return s.items, true
	case KindObject:
		if tv, ok := v.obj.(TupleView); ok {
			if s, ok := tv.AsTuple().Seq(); ok {
				return s.items, true
			}
		}
	}
	return nil, false
}

// hashFrame is one tuple whose elements are still being appended.
type hashFrame struct {
	items []Value
	i     int
}

// tupleOpen and tupleClose bracket a tuple in an encoded key. They sit outside
// the range appendKey writes for a scalar's kind byte, so a reader walking the
// encoding can always tell which it is looking at.
const (
	tupleOpen  = '['
	tupleClose = ']'
)

// hashTuple encodes a whole tuple tree into one buffer, in one pass.
//
// Folding each subtree into its own key and concatenating those is exact, and
// it is quadratic: for a tuple nested n deep it builds n prefixes of length
// O(n). A 400,000-deep tuple took two minutes and nineteen seconds to hash,
// uninterruptibly, on a graph a template can build inside the default
// iteration budget. Writing the tree out once instead is exactly as
// discriminating -- the encoding is unambiguous, so distinct tuples still get
// distinct keys and no two unequal tuples can collide -- and costs O(nodes).
func hashTuple(items []Value, outer Value, py PythonVersion, use HashUse) (hashKey, error) {
	buf := []byte{tupleOpen}
	stack := []hashFrame{{items: items}}
	for len(stack) > 0 {
		top := &stack[len(stack)-1]
		if top.i >= len(top.items) {
			buf = append(buf, tupleClose)
			*top = hashFrame{}
			stack = stack[:len(stack)-1]
			continue
		}
		child := top.items[top.i]
		top.i++
		if sub, ok := tupleItems(child); ok {
			buf = append(buf, tupleOpen)
			stack = append(stack, hashFrame{items: sub})
			continue
		}
		h, err := hashScalar(child, outer, py, use)
		if err != nil {
			return hashKey{}, err
		}
		buf = appendKey(buf, h)
	}
	return hashKey{kind: KindTuple, str: string(buf)}, nil
}

// hashScalar answers for every value that does not hash over children.
func hashScalar(v, outer Value, py PythonVersion, use HashUse) (hashKey, error) {
	switch v.kind {
	case KindNone:
		return hashKey{kind: KindNone}, nil
	case KindUndefined:
		// jinja2 hashes Undefined by identity; treating every undefined
		// as one key is close enough and never silently wrong, because
		// a template that keys on undefined is already broken.
		return hashKey{kind: KindUndefined}, nil
	case KindBool:
		return hashKey{kind: KindInt, num: int64(v.num)}, nil
	case KindInt:
		if b, ok := v.obj.(*big.Int); ok {
			return hashKey{kind: KindInt, str: b.String()}, nil
		}
		return hashKey{kind: KindInt, num: int64(v.num)}, nil
	case KindFloat:
		return hashFloat(v.AsFloat()), nil
	case KindString:
		return hashKey{kind: KindString, str: v.str}, nil
	case KindBytes:
		return hashKey{kind: KindBytes, str: v.str}, nil
	case KindObject:
		// A set-like view defines __eq__ without __hash__, which leaves
		// it unhashable although it has no failure of its own to
		// report. It has to say so before the identity fallback below,
		// which would otherwise make `d.keys() in d` a miss.
		if o, ok := v.obj.(interface{ Unhashable() bool }); ok && o.Unhashable() {
			return hashKey{}, errUnhashable(outer, v, py, use)
		}
		if o, ok := v.obj.(interface{ HashKey() (string, bool) }); ok {
			if s, ok := o.HashKey(); ok {
				return hashKey{kind: KindObject, str: s}, nil
			}
		}
		// A Python object with no __hash__ of its own hashes by
		// identity, so `ns in d` is a lookup that misses rather than a
		// TypeError. Only list, dict and set are actually unhashable.
		return hashKey{kind: KindObject, str: fmt.Sprintf("%p", v.obj)}, nil
	case KindFunc:
		return hashKey{kind: KindFunc, str: fmt.Sprintf("%p", v.obj)}, nil
	}
	return hashKey{}, errUnhashable(outer, v, py, use)
}

// hashFloat folds an integral float onto the integer key space, because Python
// guarantees 1.0 == 1 and hash(1.0) == hash(1).
func hashFloat(f float64) hashKey {
	if math.IsInf(f, 0) || math.IsNaN(f) || f != math.Trunc(f) {
		return hashKey{kind: KindFloat, flt: f}
	}
	if n, ok := FloatToInt64(f); ok {
		return hashKey{kind: KindInt, num: n}
	}
	bi, _ := big.NewFloat(f).Int(nil)
	return hashKey{kind: KindInt, str: bi.String()}
}

// appendKey writes an unambiguous, length-prefixed encoding of h, so that
// tuple keys cannot collide by concatenation.
//
// It appends to a byte slice rather than to a strings.Builder because the
// buffers live in a growable stack: a Builder moved by a reallocation trips
// its own copy check.
func appendKey(dst []byte, h hashKey) []byte {
	dst = append(dst, byte('0'+h.kind))
	dst = strconv.AppendInt(dst, h.num, 36)
	dst = append(dst, ':')
	if h.flt != 0 {
		dst = strconv.AppendUint(dst, math.Float64bits(h.flt), 36)
	}
	dst = append(dst, ':')
	dst = strconv.AppendInt(dst, int64(len(h.str)), 10)
	dst = append(dst, ':')
	return append(dst, h.str...)
}

// CheckHashable reports why v cannot be a key, naming the element at fault.
//
// The distinction matters: a tuple holding a list is unhashable, and Python
// blames the list rather than the tuple that contains it.
func CheckHashable(v Value, py PythonVersion, use HashUse) error {
	_, err := hash(v, py, use)
	return err
}
