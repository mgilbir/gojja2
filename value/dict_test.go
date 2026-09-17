// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package value_test

import (
	"testing"

	"github.com/mgilbir/gojja2/value"
)

// TestDictReserve: reserving is a capacity hint and nothing more. It runs on a
// dict that may already hold entries -- the bridge reserves before filling, but
// a caller could reserve at any point -- so the entries and their order have to
// survive it.
func TestDictReserve(t *testing.T) {
	v := value.NewDict()
	d, _ := v.Dict()
	d.SetString("a", value.Int(1))
	d.SetString("b", value.Int(2))

	d.Reserve(64)
	d.Reserve(0)
	d.Reserve(-5)

	if got := value.Repr(v); got != "{'a': 1, 'b': 2}" {
		t.Errorf("after Reserve: %s", got)
	}
	// Still a working dict: replace, append, and look up.
	d.SetString("a", value.Int(9))
	d.SetString("c", value.Int(3))
	if got := value.Repr(v); got != "{'a': 9, 'b': 2, 'c': 3}" {
		t.Errorf("after further writes: %s", got)
	}
	if got, ok := d.GetString("b"); !ok || value.Repr(got) != "2" {
		t.Errorf("GetString(b) = %v, %v", got, ok)
	}
	if d.Len() != 3 {
		t.Errorf("Len = %d, want 3", d.Len())
	}
	// The point of reserving: a dict of known size does not pay to grow.
	// Measured against the same fill without it, since the floor includes
	// the dict itself.
	fill := func(n int, reserve bool) float64 {
		return testing.AllocsPerRun(200, func() {
			nv := value.NewDict()
			nd, _ := nv.Dict()
			if reserve {
				nd.Reserve(n)
			}
			for i := range n {
				nd.SetString(string(rune('a'+i)), value.Int(int64(i)))
			}
		})
	}
	const n = 16
	reserved, grown := fill(n, true), fill(n, false)
	if reserved >= grown {
		t.Errorf("filling a %d-entry dict took %.0f allocations reserved and "+
			"%.0f growing; reserving should take fewer", n, reserved, grown)
	}
}
