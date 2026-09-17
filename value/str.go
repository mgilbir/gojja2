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

// byteOffsetOfRune returns the byte offset of rune index i, which must be in
// [0, rune count]. It walks rather than indexing a table: building one costs
// eight bytes for every byte of the string, which is how slicing a hundred
// megabytes to a single character came to allocate eight hundred.
func byteOffsetOfRune(s string, i int) int {
	if i <= 0 {
		return 0
	}
	n := 0
	for at := range s {
		if n == i {
			return at
		}
		n++
	}
	return len(s)
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
	n := utf8.RuneCountInString(s)
	if i < 0 {
		i += n
	}
	if i < 0 || i >= n {
		return "", false
	}
	at := byteOffsetOfRune(s, i)
	_, size := utf8.DecodeRuneInString(s[at:])
	return s[at : at+size], true
}

// StrSlice applies a Python slice to a str. A nil bound means "omitted".
//
// Nothing here is proportional to the length of the input beyond the result
// itself: the walk carries a cursor rather than a table of every rune's
// offset. A forward unit-step slice does not even copy, because it is a
// contiguous span of the original.
func StrSlice(s string, start, stop, step *int) (string, error) {
	n := utf8.RuneCountInString(s)
	begin, st, count, err := SliceSpan(n, start, stop, step)
	if err != nil {
		return "", err
	}
	if count == 0 {
		return "", nil
	}

	from := byteOffsetOfRune(s, begin)
	if st == 1 {
		// Contiguous and forward: the span of the original.
		return s[from:byteOffsetOfRune(s, begin+count)], nil
	}

	var b strings.Builder
	// Reserve for the result, not for the input: sizing the buffer from
	// what is left to walk allocates in the length of the string even when
	// the slice selects one character out of it.
	b.Grow(min(count*utf8.UTFMax, len(s)))
	if st > 0 {
		rest := s[from:]
		for range count {
			_, size := utf8.DecodeRuneInString(rest)
			b.WriteString(rest[:size])
			rest = rest[size:]
			// Skip st-1 runes, or run out.
			for range st - 1 {
				if rest == "" {
					break
				}
				_, skip := utf8.DecodeRuneInString(rest)
				rest = rest[skip:]
			}
		}
		return b.String(), nil
	}

	// A negative step walks back from the starting rune, decoding from the
	// end of the prefix each time -- still a cursor, not a table.
	head := s[:from]
	for k := 0; ; k++ {
		_, size := utf8.DecodeRuneInString(s[len(head):])
		b.WriteString(s[len(head) : len(head)+size])
		if k == count-1 {
			break
		}
		for range -st {
			if head == "" {
				break
			}
			_, back := utf8.DecodeLastRuneInString(head)
			head = head[:len(head)-back]
		}
	}
	return b.String(), nil
}

// SliceBounds resolves a Python slice to the raw (start, stop, step) it
// selects, without materialising the indices.
//
// A range needs these rather than the index list: slicing a range yields
// another range, and Python keeps the slice's stop rather than deriving one
// from the last element, so range(3)[::2] is range(0, 3, 2) and not
// range(0, 4, 2).
func SliceBounds(length int, start, stop, step *int) (begin, end, st int, err error) {
	st = 1
	if step != nil {
		st = *step
	}
	if st == 0 {
		return 0, 0, 0, errs.New(errs.ValueError, "slice step cannot be zero")
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

	begin = lower
	if st < 0 {
		begin = upper
	}
	if start != nil {
		begin = clamp(*start)
	}
	end = upper
	if st < 0 {
		end = lower
	}
	if stop != nil {
		end = clamp(*stop)
	}
	return begin, end, st, nil
}

// SliceSpan resolves a Python slice and reports the indices it selects as a
// starting point, a stride and a count, so a caller can walk them without
// materialising them.
//
// This is slice.indices(): omitted bounds default by direction, negative bounds
// count from the end, and out-of-range bounds clamp instead of failing -- which
// is why `"abc"[1:99]` is "bc" and not an error. The clamping itself lives in
// SliceBounds and is not repeated here; it used to be written out twice,
// character for character, in two functions that had to agree.
func SliceSpan(length int, start, stop, step *int) (begin, stride, count int, err error) {
	begin, end, stride, err := SliceBounds(length, start, stop, step)
	if err != nil {
		return 0, 0, 0, err
	}
	span := end - begin
	if (stride > 0 && span <= 0) || (stride < 0 && span >= 0) {
		return begin, stride, 0, nil
	}
	// Round the span up, away from zero, to count the final partial step.
	if stride > 0 {
		count = (span + stride - 1) / stride
	} else {
		count = (span + stride + 1) / stride
	}
	return begin, stride, count, nil
}
