// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"strings"
	"unicode"
)

// Python's case operations are full case mappings over cased characters, and
// Go's are simple mappings over general categories. The two differ in both
// halves, so every case operation in the engine goes through this file.
//
// Full mapping means one character may become several: "ß" uppercases to "SS",
// "ﬁ" to "FI", "İ" lowercases to "i" plus a combining dot. strings.ToUpper left
// all 102 of those unchanged. Cased means Unicode's Cased property rather than
// Lu/Ll/Lt, so the modifier letters, the Roman numerals and the circled
// capitals count, and unicode.IsLower/IsUpper answered for 370 code points the
// way Go defines them rather than the way Python does.
//
// The tables are generated from CPython by tools/oracle/gen_casemap.py, which
// records only the differences; caseMapDigest covers every code point and the
// test recomputes it, so a Go release that moves a mapping is caught.

// pyUpperRune, pyLowerRune, pyTitleRune and pyFoldRune are the per-character
// full mappings. Where Python's answer is a single rune, Go's table already
// holds it.
func pyUpperRune(r rune) string {
	if m, ok := upperSpecial[r]; ok {
		return m
	}
	return string(unicode.ToUpper(r))
}

func pyLowerRune(r rune) string {
	if m, ok := lowerSpecial[r]; ok {
		return m
	}
	return string(unicode.ToLower(r))
}

func pyTitleRune(r rune) string {
	if m, ok := titleSpecial[r]; ok {
		return m
	}
	return string(unicode.ToTitle(r))
}

// pyFoldRune is casefold, which is not lowercase: it folds for caseless
// comparison, so the final sigma folds onto the ordinary one and the micro sign
// onto Greek mu.
func pyFoldRune(r rune) string {
	if m, ok := foldSpecial[r]; ok {
		return m
	}
	return string(unicode.ToLower(r))
}

// pyIsLower, pyIsUpper and pyIsCased are Python's notions, which rest on the
// Cased derived property rather than on the general category alone.
func pyIsLower(r rune) bool { return unicode.Is(lowerCased, r) }
func pyIsUpper(r rune) bool { return unicode.Is(upperCased, r) }
func pyIsCased(r rune) bool { return unicode.Is(anyCased, r) }

// mapRunes applies a per-character full mapping across a string.
func mapRunes(s string, f func(rune) string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		b.WriteString(f(r))
	}
	return b.String()
}

func pyUpperString(s string) string { return mapRunes(s, pyUpperRune) }
func pyLowerString(s string) string { return mapRunes(s, pyLowerRune) }
func pyCasefold(s string) string    { return mapRunes(s, pyFoldRune) }

// pyTitle is str.title: the first cased character of each word takes the
// titlecase mapping and the rest take lowercase.
//
// A word ends at an uncased character, which is not the same as a non-letter:
// "a1b".title() is "A1B", because a digit is uncased and so the b that follows
// starts a word. Reading the boundary as "letter or digit" made it "A1b".
func pyTitleString(st *State, s string) (string, error) {
	var b strings.Builder
	b.Grow(len(s))
	prevCased := false
	for _, r := range s {
		if err := st.Poll(); err != nil {
			return "", err
		}
		if prevCased {
			b.WriteString(pyLowerRune(r))
		} else {
			b.WriteString(pyTitleRune(r))
		}
		prevCased = pyIsCased(r)
	}
	return b.String(), nil
}

// The three string predicates, written the way CPython writes them.
//
// islower is not "every character is lowercase": it is "no character is
// uppercase or titlecase, and at least one is lowercase". The difference shows
// on a string mixing a lowercase letter with a titlecase one -- "aǅ".islower()
// is False in Python, and a rule that only rejected *uppercase* would say True.
func isLowerString(s string) bool {
	cased := false
	for _, r := range s {
		if pyIsUpper(r) || unicode.IsTitle(r) {
			return false
		}
		if !cased && pyIsLower(r) {
			cased = true
		}
	}
	return cased
}

func isUpperString(s string) bool {
	cased := false
	for _, r := range s {
		if pyIsLower(r) || unicode.IsTitle(r) {
			return false
		}
		if !cased && pyIsUpper(r) {
			cased = true
		}
	}
	return cased
}

// isTitleString is str.istitle: at least one cased character, and every cased
// character in the position the title mapping would have put it.
func isTitleString(s string) bool {
	cased, prevCased := false, false
	for _, r := range s {
		switch {
		case pyIsUpper(r) || unicode.IsTitle(r):
			if prevCased {
				return false
			}
			cased, prevCased = true, true
		case pyIsLower(r):
			if !prevCased {
				return false
			}
			cased, prevCased = true, true
		default:
			prevCased = false
		}
	}
	return cased
}
