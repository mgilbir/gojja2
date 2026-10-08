// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package value

import (
	"unicode"
	"unicode/utf8"
)

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
	digit, numeric *unicode.RangeTable
	// decimal carries the *value*, not just the membership: a version newer
	// than the pin assigns decimal digits the pin does not know, and there
	// is nowhere else to read their value from. -1 means this version reads
	// no decimal value at all.
	decimal map[rune]int
	// isLower and isUpper are where the case predicates differ. There is no
	// isTitle beside them: str.istitle of a single character is
	// "isupper or Lt", and Lt has not moved across the four releases
	// modelled here -- so composing isUpper with Go's Lt is exact on all
	// 1,112,064 code points for every interpreter, and the delta table for
	// it had no caller. See pyIsCased and isTitleString in casing.go.
	isLower, isUpper *unicode.RangeTable
	// isAlpha is where str.isalpha differs, which follows the same rule:
	// Go and the interpreter are on different Unicode releases, and either
	// can be the one that knows a character.
	isAlpha *unicode.RangeTable
	// xidStart and xidContinue are where str.isidentifier's two halves
	// differ: which characters may begin an identifier and which may
	// continue one. They follow the same rule as the rest -- a code point
	// listed here answers the opposite of the pin's.
	xidStart, xidContinue *unicode.RangeTable
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

// DecimalValueFor is DecimalValue for one interpreter: the value this version
// reads the code point as, and -1 where it reads none.
//
// The override carries the value. It used to record membership alone and answer
// -1 for every code point in it, which is right for a version *older* than the
// pin -- those only ever lose assignments -- and wrong for a newer one, where 80
// code points that 3.14 reads as digits answered as though they were not digits
// at all. `{{ "\U00010d40"|int }}` was the shape that showed it.
func (u *UnicodeOverrides) DecimalValueFor(r rune) int {
	if u != nil {
		if v, ok := u.decimal[r]; ok {
			return v
		}
	}
	return DecimalValue(r)
}

// IsAlpha answers str.isalpha for this interpreter, given the default's answer.
func (u *UnicodeOverrides) IsAlpha(r rune, def bool) bool {
	if u == nil {
		return def
	}
	return flipIn(u.isAlpha, r, def)
}

// PrintableDefault is str.isprintable for the pinned interpreter, read from
// gojja2's own table.
//
// It used to be a correction to unicode.IsGraphic, which made the answer depend
// on the Unicode release of whichever Go compiled the binary: the correction is
// only true against the tables it was generated from. The whole table costs
// about nine times the ranges and owes nothing to the toolchain.
func PrintableDefault(r rune) bool {
	return unicode.Is(printableDefault, r)
}

// AlphaDefault is str.isalpha for the pinned interpreter, read the same way.
func AlphaDefault(r rune) bool {
	return unicode.Is(alphaDefault, r)
}

// XIDStartDefault and XIDContinueDefault are str.isidentifier's two halves for
// the pinned interpreter: which characters may begin an identifier and which may
// continue one.
//
// These were `unicode.IsLetter(c) || unicode.Is(unicode.Nl, c) || c == '_'` and
// that plus digits and Mn/Mc/Pc, which is the *rule* CPython's grammar states
// and not the table CPython carries -- it disagreed with the pin about 8,975
// code points for the first and 9,168 for the second. Like alphaDefault they are
// CPython's own answers and owe nothing to the Unicode release Go carries.
func XIDStartDefault(r rune) bool    { return unicode.Is(xidStartDefault, r) }
func XIDContinueDefault(r rune) bool { return unicode.Is(xidContinueDefault, r) }

// IsXIDStart and IsXIDContinue answer str.isidentifier's two halves for this
// interpreter, given the pin's answer.
func (u *UnicodeOverrides) IsXIDStart(r rune, def bool) bool {
	if u == nil {
		return def
	}
	return flipIn(u.xidStart, r, def)
}

func (u *UnicodeOverrides) IsXIDContinue(r rune, def bool) bool {
	if u == nil {
		return def
	}
	return flipIn(u.xidContinue, r, def)
}

// NameClass reports whether r may appear in a name as jinja2's lexer matches
// one: `jinja2._identifier.pattern` is `[\w<extra>]+`, so the class is Python's
// `\w` under this interpreter plus 2,231 code points frozen into that module at
// jinja2's release.
//
// It is deliberately wider than an identifier. The lexer matches a maximal run
// of this and *then* asks [IsIdentifier] about the run, which is what gives
// "Invalid character in identifier" for `a²` -- part of the match, not an
// identifier -- against "unexpected char" for `a࢘`, which is neither.
func NameClass(r rune, py PythonVersion) bool {
	if r < utf8.RuneSelf && !asciiOverridden {
		return asciiName[r]
	}
	in := unicode.Is(nameClassDefault, r)
	if t := nameClassOther[py]; t != nil && unicode.Is(t, r) {
		return !in
	}
	return in
}

// IsIdentifier is str.isidentifier for one interpreter: the first character may
// begin an identifier and every later one may continue it, both read from the
// tables CPython carries rather than from the rule its grammar states. The empty
// string is not an identifier.
func IsIdentifier(s string, py PythonVersion) bool {
	if s == "" {
		return false
	}
	if !asciiOverridden {
		i := 0
		for ; i < len(s) && s[i] < utf8.RuneSelf; i++ {
			ok := asciiXIDContinue[s[i]]
			if i == 0 {
				ok = asciiXIDStart[s[i]]
			}
			// An ASCII character answers the same here as in the
			// walk below, so a refusal is final wherever the rest
			// of the string goes.
			if !ok {
				return false
			}
		}
		if i == len(s) {
			return true
		}
	}
	// One lookup for the whole string, as every other classifier does.
	u := UnicodeFor(py)
	for i, r := range s {
		if i == 0 {
			if !u.IsXIDStart(r, XIDStartDefault(r)) {
				return false
			}
			continue
		}
		if !u.IsXIDContinue(r, XIDContinueDefault(r)) {
			return false
		}
	}
	return true
}

// asciiName, asciiXIDStart and asciiXIDContinue are NameClass and
// IsIdentifier's two halves below utf8.RuneSelf, read once from the same tables
// the slow paths consult. The lexer asks about every character of every name,
// and walking a range table and a version map for `u` or `_` was about a
// seventh of compiling a template.
//
// They are read from the tables and not written out, because a character
// class is the interpreter's and not ours to state. And they are only consulted
// while asciiOverridden is false: should a regenerated table ever give some
// interpreter a different answer for an ASCII character, the fast path turns
// itself off rather than answering for the pinned one.
var (
	asciiName, asciiXIDStart, asciiXIDContinue = asciiClasses()
	asciiOverridden                            = asciiHasOverride()
)

func asciiClasses() (name, start, cont [utf8.RuneSelf]bool) {
	for r := range rune(utf8.RuneSelf) {
		name[r] = unicode.Is(nameClassDefault, r)
		start[r] = XIDStartDefault(r)
		cont[r] = XIDContinueDefault(r)
	}
	return name, start, cont
}

func asciiHasOverride() bool {
	for r := range rune(utf8.RuneSelf) {
		for _, t := range nameClassOther {
			if t != nil && unicode.Is(t, r) {
				return true
			}
		}
		for _, u := range unicodeOther {
			if u == nil {
				continue
			}
			for _, t := range []*unicode.RangeTable{u.xidStart, u.xidContinue} {
				if t != nil && unicode.Is(t, r) {
					return true
				}
			}
		}
	}
	return false
}
