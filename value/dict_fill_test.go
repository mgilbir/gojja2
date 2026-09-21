// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package value

import (
	"fmt"
	"testing"
)

// Filling a dict from a Go map skips the "is this key already here?" probe,
// because a map cannot hand over the same key twice. These pin what that
// shortcut assumes: every key arrives, none is duplicated, the order is
// sorted, and each one is reachable by lookup rather than only present in the
// entry list.
func TestFromGoMapKeepsEveryKeyOnce(t *testing.T) {
	m := map[string]any{}
	for i := range 64 {
		m[fmt.Sprintf("k%02d", i)] = i
	}
	d, ok := FromGo(m).Dict()
	if !ok {
		t.Fatal("not a dict")
	}
	if got := len(d.Keys()); got != len(m) {
		t.Fatalf("got %d entries, want %d", got, len(m))
	}
	prev := ""
	for _, k := range d.Keys() {
		name := k.AsString()
		if name <= prev {
			t.Fatalf("keys out of order: %q then %q", prev, name)
		}
		prev = name
		v, found, err := d.Get(k, DefaultPythonVersion)
		if err != nil || !found {
			t.Fatalf("Get(%q): found=%v err=%v", name, found, err)
		}
		want, _ := m[name].(int)
		if n, _ := v.Int64(); n != int64(want) {
			t.Errorf("%q = %v, want %d", name, n, want)
		}
	}
}

// An empty map reserves nothing, so the index is never built. setFresh must
// still not be reached with a nil map behind it.
func TestFromGoEmptyMap(t *testing.T) {
	d, ok := FromGo(map[string]any{}).Dict()
	if !ok {
		t.Fatal("not a dict")
	}
	if got := len(d.Keys()); got != 0 {
		t.Errorf("got %d entries, want 0", got)
	}
	// And it is still usable afterwards.
	d.SetString("a", Int(1))
	if got := len(d.Keys()); got != 1 {
		t.Errorf("after SetString: got %d entries, want 1", got)
	}
}

// setFresh builds the index itself when nothing reserved one, rather than
// panicking on a write to a nil map.
func TestSetFreshWithoutReserve(t *testing.T) {
	d, _ := NewDict().Dict()
	if err := d.setFresh(String("a"), Int(1)); err != nil {
		t.Fatalf("setFresh: %v", err)
	}
	v, found, err := d.Get(String("a"), DefaultPythonVersion)
	if err != nil || !found {
		t.Fatalf("Get: found=%v err=%v", found, err)
	}
	if n, _ := v.Int64(); n != 1 {
		t.Errorf("got %v, want 1", n)
	}
}

// The index is split in two -- strings by themselves, everything else by
// hashKey -- so these pin what that split assumes.

// A bytes key carries its bytes in the same field a string key uses. They must
// not collide: CPython keeps {b'a': 1, 'a': 2} as two entries.
func TestBytesKeyIsNotAStringKey(t *testing.T) {
	d, _ := NewDict().Dict()
	if err := d.Set(Bytes([]byte("a")), Int(1), DefaultPythonVersion); err != nil {
		t.Fatal(err)
	}
	if err := d.Set(String("a"), Int(2), DefaultPythonVersion); err != nil {
		t.Fatal(err)
	}
	if got := len(d.Keys()); got != 2 {
		t.Fatalf("got %d entries, want 2", got)
	}
	if v, ok, _ := d.Get(Bytes([]byte("a")), DefaultPythonVersion); !ok {
		t.Error("bytes key missing")
	} else if n, _ := v.Int64(); n != 1 {
		t.Errorf("bytes key = %d, want 1", n)
	}
	if v, ok, _ := d.Get(String("a"), DefaultPythonVersion); !ok {
		t.Error("string key missing")
	} else if n, _ := v.Int64(); n != 2 {
		t.Errorf("string key = %d, want 2", n)
	}
}

// Both indexes number positions in one shared entry list, so a delete has to
// renumber both or a later lookup reads the wrong entry.
func TestDeleteRenumbersBothIndexes(t *testing.T) {
	d, _ := NewDict().Dict()
	// Interleaved so that removing one leaves stale numbers in the other.
	for _, kv := range []struct {
		k Value
		v int64
	}{
		{String("a"), 1}, {Int(2), 2}, {String("b"), 3},
		{Int(4), 4}, {String("c"), 5}, {Bytes([]byte("d")), 6},
	} {
		if err := d.Set(kv.k, Int(kv.v), DefaultPythonVersion); err != nil {
			t.Fatal(err)
		}
	}
	// Remove one from each index, from the middle.
	if ok, err := d.Delete(String("a"), DefaultPythonVersion); err != nil || !ok {
		t.Fatalf("delete a: ok=%v err=%v", ok, err)
	}
	if ok, err := d.Delete(Int(2), DefaultPythonVersion); err != nil || !ok {
		t.Fatalf("delete 2: ok=%v err=%v", ok, err)
	}
	want := map[string]int64{"b": 3, "c": 5}
	for k, v := range want {
		got, ok, err := d.Get(String(k), DefaultPythonVersion)
		if err != nil || !ok {
			t.Fatalf("Get(%q): ok=%v err=%v", k, ok, err)
		}
		if n, _ := got.Int64(); n != v {
			t.Errorf("%q = %d, want %d", k, n, v)
		}
	}
	if got, ok, _ := d.Get(Int(4), DefaultPythonVersion); !ok {
		t.Error("int key 4 missing")
	} else if n, _ := got.Int64(); n != 4 {
		t.Errorf("4 = %d, want 4", n)
	}
	if got, ok, _ := d.Get(Bytes([]byte("d")), DefaultPythonVersion); !ok {
		t.Error("bytes key missing")
	} else if n, _ := got.Int64(); n != 6 {
		t.Errorf("bytes = %d, want 6", n)
	}
	if got := len(d.Keys()); got != 4 {
		t.Errorf("got %d entries, want 4", got)
	}
}

// Clone has to copy both indexes, and the copy must be independent.
func TestCloneCopiesBothIndexes(t *testing.T) {
	d, _ := NewDict().Dict()
	_ = d.Set(String("s"), Int(1), DefaultPythonVersion)
	_ = d.Set(Int(9), Int(2), DefaultPythonVersion)
	c, _ := d.Clone().Dict()
	_ = c.Set(String("s"), Int(10), DefaultPythonVersion)
	_ = c.Set(Int(9), Int(20), DefaultPythonVersion)
	// Both writes must *replace*. An index the clone did not copy makes
	// the key look absent, and the entry is appended instead -- which the
	// value assertions below would not notice.
	if got := len(c.Keys()); got != 2 {
		t.Fatalf("clone has %d entries after replacing both keys, want 2", got)
	}
	if got := len(d.Keys()); got != 2 {
		t.Fatalf("original has %d entries, want 2", got)
	}
	if v, _, _ := d.Get(String("s"), DefaultPythonVersion); func() int64 { n, _ := v.Int64(); return n }() != 1 {
		t.Error("clone wrote through to the original's string key")
	}
	if v, _, _ := d.Get(Int(9), DefaultPythonVersion); func() int64 { n, _ := v.Int64(); return n }() != 2 {
		t.Error("clone wrote through to the original's int key")
	}
	if v, _, _ := c.Get(String("s"), DefaultPythonVersion); func() int64 { n, _ := v.Int64(); return n }() != 10 {
		t.Error("clone lost its own string key")
	}
	if v, _, _ := c.Get(Int(9), DefaultPythonVersion); func() int64 { n, _ := v.Int64(); return n }() != 20 {
		t.Error("clone lost its own int key")
	}
}

// Keys that are equal in Python stay one entry, across the split.
func TestNumericKeyIdentityAcrossTheSplit(t *testing.T) {
	d, _ := NewDict().Dict()
	_ = d.Set(Int(1), String("x"), DefaultPythonVersion)
	_ = d.Set(Bool(true), String("y"), DefaultPythonVersion)
	_ = d.Set(Float(1.0), String("z"), DefaultPythonVersion)
	if got := len(d.Keys()); got != 1 {
		t.Fatalf("got %d entries, want 1 -- 1, True and 1.0 are one key", got)
	}
	v, _, _ := d.Get(Int(1), DefaultPythonVersion)
	if v.AsString() != "z" {
		t.Errorf("got %q, want the last assignment", v.AsString())
	}
}

// A dict below smallDict entries carries no string index and scans instead, so
// every operation has two implementations that must agree. These drive a dict
// across the threshold and check it behaves the same on both sides of it.

func TestDictAcrossTheIndexThreshold(t *testing.T) {
	d, _ := NewDict().Dict()
	for n := 1; n <= smallDict*2; n++ {
		key := fmt.Sprintf("k%02d", n)
		if err := d.Set(String(key), Int(int64(n)), DefaultPythonVersion); err != nil {
			t.Fatalf("Set(%q): %v", key, err)
		}
		if got := len(d.Keys()); got != n {
			t.Fatalf("after %d inserts: %d entries", n, got)
		}
		// Everything inserted so far is still reachable, by both the
		// Value and the string accessor.
		for m := 1; m <= n; m++ {
			k := fmt.Sprintf("k%02d", m)
			v, ok, err := d.Get(String(k), DefaultPythonVersion)
			if err != nil || !ok {
				t.Fatalf("at size %d, Get(%q): ok=%v err=%v", n, k, ok, err)
			}
			if got, _ := v.Int64(); got != int64(m) {
				t.Errorf("at size %d, %q = %d, want %d", n, k, got, m)
			}
			if v, ok := d.GetString(k); !ok {
				t.Errorf("at size %d, GetString(%q) missing", n, k)
			} else if got, _ := v.Int64(); got != int64(m) {
				t.Errorf("at size %d, GetString(%q) = %d, want %d", n, k, got, m)
			}
		}
	}
}

// Replacing has to find the existing entry on both sides of the threshold,
// rather than appending a second one under the same key.
func TestDictReplaceAcrossTheThreshold(t *testing.T) {
	for _, size := range []int{1, smallDict - 1, smallDict, smallDict + 1, smallDict * 3} {
		d, _ := NewDict().Dict()
		for n := range size {
			_ = d.Set(String(fmt.Sprintf("k%02d", n)), Int(int64(n)), DefaultPythonVersion)
		}
		_ = d.Set(String("k00"), Int(999), DefaultPythonVersion)
		if got := len(d.Keys()); got != size {
			t.Errorf("size %d: replacing grew the dict to %d", size, got)
		}
		v, ok, _ := d.Get(String("k00"), DefaultPythonVersion)
		if !ok {
			t.Fatalf("size %d: k00 missing after replace", size)
		}
		if got, _ := v.Int64(); got != 999 {
			t.Errorf("size %d: k00 = %d, want 999", size, got)
		}
	}
}

// Delete works with no index, with one, and leaves the rest reachable.
func TestDictDeleteAcrossTheThreshold(t *testing.T) {
	for _, size := range []int{2, smallDict - 1, smallDict, smallDict * 2} {
		d, _ := NewDict().Dict()
		for n := range size {
			_ = d.Set(String(fmt.Sprintf("k%02d", n)), Int(int64(n)), DefaultPythonVersion)
		}
		// Remove from the middle, where renumbering matters.
		gone := fmt.Sprintf("k%02d", size/2)
		if ok, err := d.Delete(String(gone), DefaultPythonVersion); err != nil || !ok {
			t.Fatalf("size %d: Delete(%q): ok=%v err=%v", size, gone, ok, err)
		}
		if got := len(d.Keys()); got != size-1 {
			t.Errorf("size %d: %d entries after delete, want %d", size, got, size-1)
		}
		for n := range size {
			k := fmt.Sprintf("k%02d", n)
			v, ok, _ := d.Get(String(k), DefaultPythonVersion)
			if k == gone {
				if ok {
					t.Errorf("size %d: %q still present", size, k)
				}
				continue
			}
			if !ok {
				t.Fatalf("size %d: %q lost after deleting %q", size, k, gone)
			}
			if got, _ := v.Int64(); got != int64(n) {
				t.Errorf("size %d: %q = %d, want %d -- renumbering is off", size, k, got, n)
			}
		}
	}
}

// Reserving on a dict that already holds entries must index them, not replace
// the index with an empty one.
func TestReserveOnAPopulatedDictKeepsItsEntries(t *testing.T) {
	d, _ := NewDict().Dict()
	_ = d.Set(String("a"), Int(1), DefaultPythonVersion)
	_ = d.Set(String("b"), Int(2), DefaultPythonVersion)
	d.Reserve(smallDict * 4) // crosses the threshold, so an index is built
	for _, k := range []string{"a", "b"} {
		if _, ok := d.GetString(k); !ok {
			t.Errorf("%q lost when the index was built", k)
		}
	}
	_ = d.Set(String("a"), Int(9), DefaultPythonVersion)
	if got := len(d.Keys()); got != 2 {
		t.Errorf("got %d entries, want 2", got)
	}
}
