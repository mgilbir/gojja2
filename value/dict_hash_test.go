// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package value_test

import (
	"testing"

	"github.com/mgilbir/gojja2/value"
)

// TestTupleKeysAreExact is the correctness half of the linear tuple hash. The
// encoding has to stay unambiguous: a key is a Go map key, so two tuples that
// encode the same would silently answer each other's lookups, which is the
// quietest kind of wrong a dict can be.
//
// The cases are the shapes a concatenating encoder gets wrong -- the same
// elements regrouped, an empty tuple against no tuple at all, and a string
// that spells the bracket the encoding uses.
func TestTupleKeysAreExact(t *testing.T) {
	tup := func(items ...value.Value) value.Value { return value.NewTuple(items...) }
	one, two := value.Int(1), value.Int(2)

	keys := []value.Value{
		tup(),
		tup(tup()),
		tup(tup(), tup()),
		tup(one),
		tup(one, two),
		tup(tup(one), two),
		tup(one, tup(two)),
		tup(tup(one, two)),
		tup(tup(one), tup(two)),
		tup(value.String("[")),
		tup(value.String("]")),
		tup(value.String("[]")),
		tup(value.String(""), value.String("")),
		tup(value.String("[1]")),
		tup(value.Bytes([]byte("["))),
		tup(one, value.String("2")),
		tup(value.String("1"), two),
	}

	d := value.NewDict()
	dict, _ := d.Dict()
	for i, k := range keys {
		if err := dict.Set(k, value.Int(int64(i)), value.DefaultPythonVersion); err != nil {
			t.Fatalf("set %s: %v", value.Repr(k), err)
		}
	}
	if dict.Len() != len(keys) {
		t.Fatalf("dict holds %d of %d distinct keys: two of them collide",
			dict.Len(), len(keys))
	}
	for i, k := range keys {
		got, ok, err := dict.Get(k, value.DefaultPythonVersion)
		if err != nil || !ok {
			t.Fatalf("get %s: ok=%v err=%v", value.Repr(k), ok, err)
		}
		if n, _ := got.Int64(); n != int64(i) {
			t.Errorf("%s answered with entry %d, want %d", value.Repr(k), n, i)
		}
	}
}

// TestEqualTuplesShareAKey is the other direction: a tuple built twice is one
// key, however it was built, because Python's dict is keyed by value.
func TestEqualTuplesShareAKey(t *testing.T) {
	build := func() value.Value {
		return value.NewTuple(value.Int(1),
			value.NewTuple(value.String("a"), value.NewTuple()),
			value.Float(2.5))
	}
	d := value.NewDict()
	dict, _ := d.Dict()
	if err := dict.Set(build(), value.String("first"), value.DefaultPythonVersion); err != nil {
		t.Fatal(err)
	}
	if err := dict.Set(build(), value.String("second"), value.DefaultPythonVersion); err != nil {
		t.Fatal(err)
	}
	if dict.Len() != 1 {
		t.Fatalf("two equal tuples made %d entries, want 1", dict.Len())
	}
	got, _, _ := dict.Get(build(), value.DefaultPythonVersion)
	if value.Str(got) != "second" {
		t.Errorf("got %q, want the later value", value.Str(got))
	}
}
