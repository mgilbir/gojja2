// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package lexer

import (
	"errors"
	"math/big"
	"strconv"
	"strings"

	"github.com/mgilbir/gojja2/errs"
	"github.com/mgilbir/gojja2/value"
)

// Numbers follow Python's literal grammar, underscore separators included.
// Floats are matched before integers, and a float needs either an exponent or
// a fractional part -- which is why `1.` lexes as the integer 1 followed by a
// dot, and `x.5` has no float in it at all.

func isASCIIDigit(b byte) bool  { return b >= '0' && b <= '9' }
func isBinaryDigit(b byte) bool { return b == '0' || b == '1' }
func isOctalDigit(b byte) bool  { return b >= '0' && b <= '7' }

func isHexDigit(b byte) bool {
	return isASCIIDigit(b) || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')
}

// matchDigitGroups matches `(\d+_)*\d+`: digit runs joined by single
// underscores, always ending on a digit.
func matchDigitGroups(s string, i int, isDigit func(byte) bool) (int, bool) {
	if i >= len(s) || !isDigit(s[i]) {
		return 0, false
	}
	for {
		for i < len(s) && isDigit(s[i]) {
			i++
		}
		if i+1 < len(s) && s[i] == '_' && isDigit(s[i+1]) {
			i++
			continue
		}
		return i, true
	}
}

// matchPrefixedDigits matches `(_?<digit>)+`, the body of a 0b/0o/0x literal.
func matchPrefixedDigits(s string, i int, isDigit func(byte) bool) (int, bool) {
	start := i
	for i < len(s) {
		j := i
		if s[j] == '_' {
			j++
		}
		if j >= len(s) || !isDigit(s[j]) {
			break
		}
		i = j + 1
	}
	return i, i > start
}

// matchFloat returns the byte length of a float literal at the cursor.
func (l *lexer) matchFloat() int {
	s, pos := l.src, l.pos
	// A float may not follow a dot, so the `5` in `x.5` stays an integer.
	if pos > 0 && s[pos-1] == '.' {
		return 0
	}
	intEnd, ok := matchDigitGroups(s, pos, isASCIIDigit)
	if !ok {
		return 0
	}

	// First alternative: an optional fractional part then a mandatory
	// exponent.
	mantissaEnd := intEnd
	if mantissaEnd < len(s) && s[mantissaEnd] == '.' {
		if end, ok := matchDigitGroups(s, mantissaEnd+1, isASCIIDigit); ok {
			mantissaEnd = end
		}
	}
	if mantissaEnd < len(s) && (s[mantissaEnd] == 'e' || s[mantissaEnd] == 'E') {
		expDigits := mantissaEnd + 1
		if expDigits < len(s) && (s[expDigits] == '+' || s[expDigits] == '-') {
			expDigits++
		}
		if end, ok := matchDigitGroups(s, expDigits, isASCIIDigit); ok {
			return end - pos
		}
	}

	// Second alternative: a mandatory fractional part and no exponent.
	if intEnd < len(s) && s[intEnd] == '.' {
		if end, ok := matchDigitGroups(s, intEnd+1, isASCIIDigit); ok {
			return end - pos
		}
	}
	return 0
}

// matchInteger returns the byte length of an integer literal at the cursor.
func (l *lexer) matchInteger() int {
	s, pos := l.src, l.pos
	if pos >= len(s) {
		return 0
	}

	if s[pos] == '0' && pos+1 < len(s) {
		var isDigit func(byte) bool
		switch s[pos+1] {
		case 'b', 'B':
			isDigit = isBinaryDigit
		case 'o', 'O':
			isDigit = isOctalDigit
		case 'x', 'X':
			isDigit = isHexDigit
		}
		if isDigit != nil {
			if end, ok := matchPrefixedDigits(s, pos+2, isDigit); ok {
				return end - pos
			}
		}
	}

	if s[pos] >= '1' && s[pos] <= '9' {
		i := pos + 1
		for i < len(s) {
			j := i
			if s[j] == '_' {
				j++
			}
			if j >= len(s) || !isASCIIDigit(s[j]) {
				break
			}
			i = j + 1
		}
		return i - pos
	}

	if s[pos] == '0' {
		// Decimal zero, and only zero: `0_0` is 0 but `01` is two tokens,
		// matching Python's refusal of leading-zero decimals.
		i := pos + 1
		for i < len(s) {
			j := i
			if s[j] == '_' {
				j++
			}
			if j >= len(s) || s[j] != '0' {
				break
			}
			i = j + 1
		}
		return i - pos
	}
	return 0
}

// ParseInteger converts an integer literal to a value, at arbitrary precision.
func ParseInteger(text string) (value.Value, error) {
	digits := strings.ReplaceAll(text, "_", "")
	base := 10
	if len(digits) > 2 && digits[0] == '0' {
		switch digits[1] {
		case 'b', 'B':
			base, digits = 2, digits[2:]
		case 'o', 'O':
			base, digits = 8, digits[2:]
		case 'x', 'X':
			base, digits = 16, digits[2:]
		}
	}
	n, ok := new(big.Int).SetString(digits, base)
	if !ok {
		return value.Undefined, errs.New(errs.TemplateSyntaxError,
			"invalid integer literal %q", text)
	}
	return value.BigInt(n), nil
}

// ParseFloat converts a float literal to a value.
//
// A literal outside float64's range is not a syntax error: Python reads 1e999
// as inf and 1e-999 as 0.0, and strconv reports both by returning that exact
// value alongside ErrRange. Discarding the value with the error turned a
// template CPython renders into one that would not compile.
func ParseFloat(text string) (value.Value, error) {
	f, err := strconv.ParseFloat(strings.ReplaceAll(text, "_", ""), 64)
	if err != nil && !errors.Is(err, strconv.ErrRange) {
		return value.Undefined, errs.New(errs.TemplateSyntaxError,
			"invalid float literal %q", text)
	}
	return value.Float(f), nil
}
