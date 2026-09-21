// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package value

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// DecimalValue is the value int() and float() read a code point as, or -1 for
// one they do not read as a digit at all.
//
// Decimal is the narrowest of Python's three numeric predicates, and that is
// the point here rather than an accident: SUPERSCRIPT TWO satisfies isdigit,
// VULGAR FRACTION ONE HALF and ROMAN NUMERAL FIVE satisfy isnumeric, and int()
// refuses all three. Only a decimal digit converts.
func DecimalValue(r rune) int {
	if r < 0x80 {
		if r >= '0' && r <= '9' {
			return int(r - '0')
		}
		return -1
	}
	// The runs are sorted, so the candidate is the last one starting at or
	// below r, and it holds r only if r is within ten of its start.
	i := sort.Search(len(decimalRuns), func(i int) bool { return decimalRuns[i] > r })
	if i == 0 {
		return -1
	}
	if d := r - decimalRuns[i-1]; d < 10 {
		return int(d)
	}
	return -1
}

// DecimalASCII is CPython's _PyUnicode_TransformDecimalAndSpaceToASCII, which
// int() and float() run over their argument before parsing it: every character
// carrying a decimal value becomes the ASCII digit of that value, every
// whitespace character becomes a space, and everything else is left alone for
// the parse to accept or reject.
//
// It is why int("٤٢") is 42, why int("٤" + "2") is also 42 --
// the scripts may be mixed -- and why the underscore and sign rules then apply
// to the transformed text, so int("٤_٢") is 42 as well.
//
// Both places gojja2 prepares numeric text call this, and they call the same
// one on purpose. They used to read ASCII digits each in their own way, and
// both were wrong in the same direction: `{{ "٤٢"|int }}` answered
// the filter's default of 0, which is a wrong number rather than an error.
//
// Go's unicode.IsSpace is exactly CPython's set here -- the same twenty-five
// code points, checked in decimal_test.go -- so the whitespace half needs no
// table of its own.
func DecimalASCII(s string) string {
	// Almost every subject is ASCII already, and rewriting one that needs
	// no rewriting would cost an allocation per int() in a loop.
	if isASCIIText(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r < 0x80:
			b.WriteByte(byte(r))
		case DecimalValue(r) >= 0:
			b.WriteByte(byte('0' + DecimalValue(r)))
		case unicode.IsSpace(r):
			b.WriteByte(' ')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// isASCIIText reports whether s needs no transforming, which is the common
// case and worth not allocating for.
func isASCIIText(s string) bool {
	for i := range len(s) {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}
