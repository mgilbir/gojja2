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
		v, found, err := d.Get(k)
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
	v, found, err := d.Get(String("a"))
	if err != nil || !found {
		t.Fatalf("Get: found=%v err=%v", found, err)
	}
	if n, _ := v.Int64(); n != 1 {
		t.Errorf("got %v, want 1", n)
	}
}
