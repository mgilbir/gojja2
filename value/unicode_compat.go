// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package value

import "unicode"

// UnicodeOverrides is one non-pinned interpreter's Unicode answers, where they
// differ from the pinned one's.
//
// CPython carries its own Unicode, so which characters are printable, which
// are digits and how each one cases are all decided by the interpreter rather
// than by jinja2. Across 3.11 to 3.14 there are 10,311 code points the four do
// not all agree about, and WithPythonVersion would mean less than it says if a
// render reproduced one interpreter's rules and another's tables.
//
// The committed tables are the pinned interpreter's, and this records only the
// difference. Almost all of it is isprintable, and almost all of that is
// simply which characters had been assigned yet: they arrive in blocks, so ten
// thousand code points store as a few dozen ranges. See
// tools/oracle/gen_unicode_matrix.py.
type UnicodeOverrides struct {
	// printable is where isprintable differs, which is what repr escapes by.
	printable *unicode.RangeTable
	// upper, lower, title and fold are the mappings that differ, by rune.
	upper, lower, title, fold map[rune]string
	// digit, numeric and decimal are where the numeric predicates differ.
	digit, numeric, decimal *unicode.RangeTable
	// isLower, isUpper and isTitle are where the case predicates differ.
	isLower, isUpper, isTitle *unicode.RangeTable
	// isAlpha is where str.isalpha differs, which follows the same rule:
	// Go and the interpreter are on different Unicode releases, and either
	// can be the one that knows a character.
	isAlpha *unicode.RangeTable
}

// unicodeFor is the overrides to apply for one interpreter, or nil for the
// default -- which is the common case, and the one that costs nothing.
//
// It is looked up once per operation rather than once per rune. `{{ x|upper }}`
// over a ten-kilobyte string does one map lookup and then walks, which is what
// keeps the version out of the inner loop.
func UnicodeFor(py PythonVersion) *UnicodeOverrides {
	if py == DefaultPythonVersion {
		return nil
	}
	return unicodeOther[py]
}

// The accessors below all take the resolved overrides rather than the version,
// so a caller that has hoisted the lookup out of its loop cannot accidentally
// put it back.

// PrintableWith is str.isprintable for one interpreter.
func (u *UnicodeOverrides) PrintableWith(r rune) bool {
	p := PrintableDefault(r)
	if u != nil && unicode.Is(u.printable, r) {
		return !p
	}
	return p
}

// flipIn reports a predicate's answer for one interpreter: the default's,
// inverted where this version is recorded as disagreeing.
func flipIn(t *unicode.RangeTable, r rune, def bool) bool {
	if t != nil && unicode.Is(t, r) {
		return !def
	}
	return def
}

// Upper, Lower, Title and Fold are the mappings this interpreter words
// differently, or nil where there are none.
func (u *UnicodeOverrides) Upper() map[rune]string { return u.upper }
func (u *UnicodeOverrides) Lower() map[rune]string { return u.lower }
func (u *UnicodeOverrides) Title() map[rune]string { return u.title }
func (u *UnicodeOverrides) Fold() map[rune]string  { return u.fold }

// IsLower, IsUpper and IsTitle answer the case predicates for this
// interpreter, given the default's answer.
func (u *UnicodeOverrides) IsLower(r rune, def bool) bool {
	if u == nil {
		return def
	}
	return flipIn(u.isLower, r, def)
}

func (u *UnicodeOverrides) IsUpper(r rune, def bool) bool {
	if u == nil {
		return def
	}
	return flipIn(u.isUpper, r, def)
}

func (u *UnicodeOverrides) IsTitle(r rune, def bool) bool {
	if u == nil {
		return def
	}
	return flipIn(u.isTitle, r, def)
}

// IsDigit, IsNumeric and IsDecimal answer the numeric predicates for this
// interpreter, given the default's answer.
func (u *UnicodeOverrides) IsDigit(r rune, def bool) bool {
	if u == nil {
		return def
	}
	return flipIn(u.digit, r, def)
}

func (u *UnicodeOverrides) IsNumeric(r rune, def bool) bool {
	if u == nil {
		return def
	}
	return flipIn(u.numeric, r, def)
}

// DecimalValueFor is DecimalValue for one interpreter: -1 where this version
// does not read the code point as a digit, and the default's value otherwise.
//
// The overrides record only membership, not the value, because a code point
// that is a decimal digit in two interpreters always has the same value in
// both -- it is the assignment that moved, not the meaning.
func (u *UnicodeOverrides) DecimalValueFor(r rune) int {
	v := DecimalValue(r)
	if u != nil && unicode.Is(u.decimal, r) {
		if v >= 0 {
			return -1
		}
		return -1
	}
	return v
}

// IsAlpha answers str.isalpha for this interpreter, given the default's answer.
func (u *UnicodeOverrides) IsAlpha(r rune, def bool) bool {
	if u == nil {
		return def
	}
	return flipIn(u.isAlpha, r, def)
}

// PrintableDefault is str.isprintable for the pinned interpreter.
//
// Go's own tables are a different Unicode release, so asking IsGraphic alone
// answers for characters CPython has not been told about and misses ones it
// has -- 5,812 code points at the moment. printableFixDefault is where the two
// disagree.
func PrintableDefault(r rune) bool {
	return flipIn(printableFixDefault, r, printable(r))
}

// AlphaDefault is str.isalpha for the pinned interpreter, corrected against
// Go's tables for the same reason.
func AlphaDefault(r rune, goSays bool) bool {
	return flipIn(alphaFixDefault, r, goSays)
}
