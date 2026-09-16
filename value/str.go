// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package value

import (
	"strings"
	"unicode/utf8"

	"github.com/mgilbir/gojja2/errs"
)

// Python strings are sequences of code points, so len, indexing and slicing
// all count runes rather than bytes. Go strings are UTF-8, so the ASCII case
// -- overwhelmingly the common one -- is detected and handled in O(1)/O(n)
// byte space, and only genuinely multi-byte text pays for a rune walk.

func runeLen(s string, isBytes bool) int {
	if isBytes {
		return len(s)
	}
	return utf8.RuneCountInString(s)
}

// StrLen is Python's len() for a str.
func StrLen(s string) int { return utf8.RuneCountInString(s) }

// runeOffsets returns the byte offset of every rune in s, plus len(s) as a
// final sentinel, so a rune index range maps to a byte range.
func runeOffsets(s string) []int {
	offsets := make([]int, 0, len(s)+1)
	for i := range s {
		offsets = append(offsets, i)
	}
	return append(offsets, len(s))
}

// StrIndex returns the one-character string at code-point index i, which may
// be negative to count from the end.
func StrIndex(s string, i int) (string, bool) {
	if len(s) == utf8.RuneCountInString(s) { // all ASCII
		if i < 0 {
			i += len(s)
		}
		if i < 0 || i >= len(s) {
			return "", false
		}
		return s[i : i+1], true
	}
	offsets := runeOffsets(s)
	n := len(offsets) - 1
	if i < 0 {
		i += n
	}
	if i < 0 || i >= n {
		return "", false
	}
	return s[offsets[i]:offsets[i+1]], true
}

// StrSlice applies a Python slice to a str. A nil bound means "omitted".
func StrSlice(s string, start, stop, step *int) (string, error) {
	offsets := runeOffsets(s)
	n := len(offsets) - 1
	idx, err := SliceIndices(n, start, stop, step)
	if err != nil {
		return "", err
	}
	// A forward, unit-step slice is contiguous, so it can be taken whole.
	if len(idx) > 0 && idx[len(idx)-1]-idx[0] == len(idx)-1 {
		return s[offsets[idx[0]]:offsets[idx[len(idx)-1]+1]], nil
	}
	var b strings.Builder
	for _, i := range idx {
		b.WriteString(s[offsets[i]:offsets[i+1]])
	}
	return b.String(), nil
}

// SliceIndices resolves a Python slice against a sequence of the given length
// and returns the indices it selects, in order.
//
// This is slice.indices() plus the walk: omitted bounds default by direction,
// negative bounds count from the end, and out-of-range bounds clamp instead of
// failing -- which is why `"abc"[1:99]` is "bc" and not an error.
func SliceIndices(length int, start, stop, step *int) ([]int, error) {
	st := 1
	if step != nil {
		st = *step
	}
	if st == 0 {
		return nil, errs.New(errs.ValueError, "slice step cannot be zero")
	}

	var lower, upper int
	if st > 0 {
		lower, upper = 0, length
	} else {
		lower, upper = -1, length-1
	}

	clamp := func(v int) int {
		if v < 0 {
			v += length
			if v < lower {
				return lower
			}
			return v
		}
		if v > upper {
			return upper
		}
		return v
	}

	begin := lower
	if st < 0 {
		begin = upper
	}
	if start != nil {
		begin = clamp(*start)
	}

	end := upper
	if st < 0 {
		end = lower
	}
	if stop != nil {
		end = clamp(*stop)
	}

	var out []int
	if st > 0 {
		for i := begin; i < end; i += st {
			out = append(out, i)
		}
	} else {
		for i := begin; i > end; i += st {
			out = append(out, i)
		}
	}
	return out, nil
}
