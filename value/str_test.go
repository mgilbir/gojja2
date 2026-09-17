// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package value_test

import (
	"runtime"
	"strings"
	"testing"

	"github.com/mgilbir/gojja2/value"
)

func at(n int) *int { return &n }

// TestSlicingCostsTheResultNotTheInput pins that a slice is paid for in the
// size of what it returns.
//
// Python indexes a str by code point, and the first way to get that is a table
// of every rune's byte offset -- eight bytes for every byte of the string. It
// is invisible until the string is large, and then it is not: taking a single
// character out of a hundred-megabyte string allocated eight hundred megabytes,
// none of it charged against the render budget, which had been told 256 MiB was
// the limit. The render was OOM-killed at a 700 MB ceiling that the same
// template without the slice survived comfortably.
//
// The threshold is one byte per byte of input, which a table exceeds eightfold
// on the very first call while a cursor never comes near it.
func TestSlicingCostsTheResultNotTheInput(t *testing.T) {
	// Multi-byte, so the ASCII fast path does not hide a regression.
	const runes = 1 << 20
	s := strings.Repeat("é", runes)

	for name, take := range map[string]func(){
		"slice one character":      func() { _, _ = value.StrSlice(s, at(0), at(1), nil) },
		"slice from the end":       func() { _, _ = value.StrSlice(s, at(-2), nil, nil) },
		"index one character":      func() { _, _ = value.StrIndex(s, 1) },
		"index from the end":       func() { _, _ = value.StrIndex(s, -1) },
		"slice a stride, one item": func() { _, _ = value.StrSlice(s, at(0), at(2), at(2)) },
	} {
		t.Run(name, func(t *testing.T) {
			const calls = 8
			var before, after runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&before)
			for range calls {
				take()
			}
			runtime.ReadMemStats(&after)

			grew := after.TotalAlloc - before.TotalAlloc
			if limit := uint64(len(s)); grew > limit {
				t.Errorf("%d calls allocated %d bytes over a %d-byte string; "+
					"a per-rune table would, a cursor would not (limit %d)",
					calls, grew, len(s), limit)
			}
		})
	}
}

// TestSliceSpanMatchesPythonIndices pins the resolution itself, which used to
// be written out twice -- once to produce an index list and once to produce
// raw bounds -- in two functions that had to agree.
func TestSliceSpanMatchesPythonIndices(t *testing.T) {
	for _, tc := range []struct {
		length              int
		start, stop, step   *int
		begin, stride, want int
	}{
		{5, nil, nil, nil, 0, 1, 5},
		{5, at(1), at(3), nil, 1, 1, 2},
		{5, at(-2), nil, nil, 3, 1, 2},
		{5, at(1), at(99), nil, 1, 1, 4},
		{5, at(99), nil, nil, 5, 1, 0},
		{5, nil, nil, at(2), 0, 2, 3},
		{5, nil, nil, at(-1), 4, -1, 5},
		{5, nil, nil, at(-2), 4, -2, 3},
		{5, at(3), at(0), at(-1), 3, -1, 3},
		{5, at(0), at(0), nil, 0, 1, 0},
		{0, nil, nil, nil, 0, 1, 0},
		{0, nil, nil, at(-1), -1, -1, 0},
		{7, at(1), at(6), at(3), 1, 3, 2},
	} {
		begin, stride, count, err := value.SliceSpan(tc.length, tc.start, tc.stop, tc.step)
		if err != nil {
			t.Errorf("len %d: %v", tc.length, err)
			continue
		}
		if begin != tc.begin || stride != tc.stride || count != tc.want {
			t.Errorf("SliceSpan(%d, %v, %v, %v) = (%d, %d, %d), want (%d, %d, %d)",
				tc.length, deref(tc.start), deref(tc.stop), deref(tc.step),
				begin, stride, count, tc.begin, tc.stride, tc.want)
		}
	}
	if _, _, _, err := value.SliceSpan(5, nil, nil, at(0)); err == nil {
		t.Error("a zero step must be refused")
	}
}

func deref(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}
