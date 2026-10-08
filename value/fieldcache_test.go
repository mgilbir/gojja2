// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package value

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
)

// TestVisibleFieldsCacheIsBounded mints more struct types than the cache
// holds, from many goroutines at once, and checks two things: the cache stops
// at its cap, and a type past the cap still answers -- it is computed instead
// of remembered, not refused.
func TestVisibleFieldsCacheIsBounded(t *testing.T) {
	// The cache is the process's, and other tests may have put types in it.
	// Empty it before and after, so this test counts only its own.
	reset := func() {
		visibleFieldsCache.Clear()
		visibleFieldsCount.Store(0)
	}
	reset()
	t.Cleanup(reset)

	const types = visibleFieldsCap + 1000
	minted := make([]reflect.Type, types)
	for i := range minted {
		minted[i] = reflect.StructOf([]reflect.StructField{
			{Name: fmt.Sprintf("F%d", i), Type: reflect.TypeFor[int](), Tag: `json:"f"`},
			{Name: "G", Type: reflect.TypeFor[string]()},
		})
	}

	// Every worker asks for every type, each starting somewhere else, so
	// two of them miss on one type at once and one loses the store -- the
	// race the count has to undo.
	var wg sync.WaitGroup
	const workers = 8
	for w := range workers {
		wg.Go(func() {
			for k := range types {
				i := (k + w*types/workers) % types
				// Twice, so a type is asked for both before and
				// after it could have been stored.
				for range 2 {
					got := visibleFields(minted[i])
					if len(got) != 2 || got[0].name != "f" ||
						got[0].goName != fmt.Sprintf("F%d", i) || got[1].name != "G" {
						t.Errorf("type %d: fields = %+v", i, got)
						return
					}
				}
			}
		})
	}
	wg.Wait()

	n := 0
	visibleFieldsCache.Range(func(any, any) bool { n++; return true })
	if n > visibleFieldsCap {
		t.Errorf("cache holds %d types, over its cap of %d", n, visibleFieldsCap)
	}
	if n < visibleFieldsCap {
		t.Errorf("cache holds %d types of the %d minted; it should have filled to its cap of %d",
			n, types, visibleFieldsCap)
	}
	if c := visibleFieldsCount.Load(); c != int64(n) {
		t.Errorf("the count says %d and the cache holds %d", c, n)
	}
}
