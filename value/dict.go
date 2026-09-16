// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package value

import (
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"

	"github.com/mgilbir/gojja2/errs"
)

// Dict is a Python dict: insertion ordered, keyed by any hashable value.
//
// Keying on the *value* rather than on a Go string is what makes
// `{% set d = {1: "a"} %}{{ d[1] }}` work. Implementations that stringify keys
// silently return nothing for integer, float, tuple and None keys.
type Dict struct {
	entries []DictEntry
	index   map[hashKey]int
}

// DictEntry is one key/value pair, in insertion order.
type DictEntry struct {
	Key   Value
	Value Value
}

// NewDict returns an empty dict.
func NewDict() Value { return Value{kind: KindDict, obj: &Dict{}} }

// DictOf builds a dict from alternating key/value pairs, which must be of even
// length. It panics otherwise, since that is always a programming error.
func DictOf(kv ...Value) Value {
	if len(kv)%2 != 0 {
		panic("value.DictOf: odd number of arguments")
	}
	v := NewDict()
	d, _ := v.Dict()
	for i := 0; i < len(kv); i += 2 {
		if err := d.Set(kv[i], kv[i+1]); err != nil {
			panic("value.DictOf: " + err.Error())
		}
	}
	return v
}

// StringDict builds a dict with str keys, in the order given by keys.
func StringDict(keys []string, vals []Value) Value {
	v := NewDict()
	d, _ := v.Dict()
	for i, k := range keys {
		_ = d.Set(String(k), vals[i])
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
func (d *Dict) Get(key Value) (Value, bool, error) {
	h, err := hash(key)
	if err != nil {
		return Undefined, false, err
	}
	i, ok := d.index[h]
	if !ok {
		return Undefined, false, nil
	}
	return d.entries[i].Value, true, nil
}

// GetString is the common case: lookup by a str key.
func (d *Dict) GetString(key string) (Value, bool) {
	i, ok := d.index[hashKey{kind: KindString, str: key}]
	if !ok {
		return Undefined, false
	}
	return d.entries[i].Value, true
}

// Set inserts or replaces key.
//
// Replacing an existing entry keeps the original key object and position, so
// {1: "a", True: "c"} is {1: 'c'} -- key 1, value from the later assignment --
// exactly as CPython reports it.
func (d *Dict) Set(key, val Value) error {
	h, err := hash(key)
	if err != nil {
		return err
	}
	if i, ok := d.index[h]; ok {
		d.entries[i].Value = val
		return nil
	}
	if d.index == nil {
		d.index = make(map[hashKey]int)
	}
	d.index[h] = len(d.entries)
	d.entries = append(d.entries, DictEntry{Key: key, Value: val})
	return nil
}

// SetString inserts or replaces a str key.
func (d *Dict) SetString(key string, val Value) { _ = d.Set(String(key), val) }

// Delete removes key, reporting whether it was present.
func (d *Dict) Delete(key Value) (bool, error) {
	h, err := hash(key)
	if err != nil {
		return false, err
	}
	i, ok := d.index[h]
	if !ok {
		return false, nil
	}
	d.entries = append(d.entries[:i], d.entries[i+1:]...)
	delete(d.index, h)
	// Entries after the hole shifted down by one.
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
	out.index = make(map[hashKey]int, len(d.index))
	for k, v := range d.index {
		out.index[k] = v
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

func hash(v Value) (hashKey, error) {
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
	case KindTuple:
		s, _ := v.Seq()
		var b strings.Builder
		for _, item := range s.items {
			h, err := hash(item)
			if err != nil {
				return hashKey{}, err
			}
			encodeKey(&b, h)
		}
		return hashKey{kind: KindTuple, str: b.String()}, nil
	case KindObject:
		if tv, ok := v.obj.(TupleView); ok {
			// A tuple subclass hashes as its tuple, so one holding
			// a list is unhashable just as a plain tuple would be.
			return hash(tv.AsTuple())
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
	return hashKey{}, errs.New(errs.TypeError, "unhashable type: '%s'", v.TypeName())
}

// hashFloat folds an integral float onto the integer key space, because Python
// guarantees 1.0 == 1 and hash(1.0) == hash(1).
func hashFloat(f float64) hashKey {
	if math.IsInf(f, 0) || math.IsNaN(f) || f != math.Trunc(f) {
		return hashKey{kind: KindFloat, flt: f}
	}
	if f >= math.MinInt64 && f <= math.MaxInt64 {
		return hashKey{kind: KindInt, num: int64(f)}
	}
	bi, _ := big.NewFloat(f).Int(nil)
	return hashKey{kind: KindInt, str: bi.String()}
}

// encodeKey writes an unambiguous, length-prefixed encoding of h, so that
// tuple keys cannot collide by concatenation.
func encodeKey(b *strings.Builder, h hashKey) {
	b.WriteByte(byte('0' + h.kind))
	b.WriteString(strconv.FormatInt(h.num, 36))
	b.WriteByte(':')
	if h.flt != 0 {
		b.WriteString(strconv.FormatUint(math.Float64bits(h.flt), 36))
	}
	b.WriteByte(':')
	b.WriteString(strconv.Itoa(len(h.str)))
	b.WriteByte(':')
	b.WriteString(h.str)
}

// Hashable reports whether v can be used as a dict key.
func Hashable(v Value) bool {
	_, err := hash(v)
	return err == nil
}
