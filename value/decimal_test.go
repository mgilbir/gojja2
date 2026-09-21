// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package value

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"unicode"
)

// Every code point's decimal value, checked against CPython's.
//
// int() and float() do not read ASCII digits. They transform every character
// carrying a decimal value into the ASCII digit of that value first, so
// int("٤٢") is 42. gojja2 read ASCII only, in all three places it
// prepared numeric text, and `{{ "٤٢"|int }}` answered the filter's
// default of 0 -- a wrong number, with no error to say so.
//
// The table is generated from the pinned CPython rather than taken from
// unicode.Nd, because Go is on a later Unicode: its Nd has twenty code points
// CPython 3.11 does not, and accepting them would be a divergence in the other
// direction. This recomputes the generator's digest over all 1,114,112 code
// points, so a run that moves, gains a member or loses one fails the build.
func TestDecimalValuesMatchCPython(t *testing.T) {
	h := sha256.New()
	var line strings.Builder
	n := 0
	for cp := range 0x110000 {
		v := DecimalValue(rune(cp))
		if v >= 0 {
			n++
		}
		line.Reset()
		fmt.Fprintf(&line, "%d\t%d\n", cp, v)
		h.Write([]byte(line.String()))
	}
	if n != decimalDigitCount {
		t.Errorf("found %d decimal code points, the table says %d", n, decimalDigitCount)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != decimalDigitDigest {
		t.Errorf("decimal digest = %s, want %s\n"+
			"A code point's decimal value differs from CPython's. Regenerate "+
			"with `make decimal` and read the diff: it is telling you either "+
			"that Unicode moved or that the pinned CPython did.",
			got, decimalDigitDigest)
	}
}

// TestDecimalIsNarrowerThanDigit: decimal is the narrowest of Python's three
// numeric predicates, and taking the wrong one would quietly make int() accept
// characters CPython refuses. SUPERSCRIPT TWO is isdigit, VULGAR FRACTION ONE
// HALF and ROMAN NUMERAL FIVE are isnumeric, and int() takes none of them.
func TestDecimalIsNarrowerThanDigit(t *testing.T) {
	for r, want := range map[rune]int{
		'0': 0, '9': 9,
		'٤':          4,  // ARABIC-INDIC DIGIT FOUR
		'۲':          2,  // EXTENDED ARABIC-INDIC DIGIT TWO
		'๔':          4,  // THAI DIGIT FOUR
		'９':          9,  // FULLWIDTH DIGIT NINE
		'\U0001d7dc': 4,  // MATHEMATICAL DOUBLE-STRUCK DIGIT FOUR
		'²':          -1, // SUPERSCRIPT TWO -- isdigit, not isdecimal
		'½':          -1, // VULGAR FRACTION ONE HALF -- isnumeric only
		'Ⅴ':          -1, // ROMAN NUMERAL FIVE -- isnumeric only
		'一':          -1, // CJK ideograph one -- isnumeric only
		'a':          -1,
		' ':          -1,
		// Kawi and Nag Mundari arrived in Unicode 15, which the pinned
		// CPython now has -- so they are digits here. They were -1 when
		// the pin was 3.11, and that is the point: the table follows
		// the interpreter rather than Go's own tables, in whichever
		// direction the two happen to differ.
		'\U00011f50': 0, // KAWI DIGIT ZERO
		'\U0001e4f0': 0, // NAG MUNDARI DIGIT ZERO
	} {
		if got := DecimalValue(r); got != want {
			t.Errorf("DecimalValue(%U) = %d, want %d", r, got, want)
		}
	}
}

// TestPythonSpaceIsGoSpace: the transform folds whitespace to an ASCII space
// as well as digits to ASCII digits, and gojja2 uses Go's unicode.IsSpace for
// it. That is only correct while the two agree, which nothing would otherwise
// notice: every one of these skipped by one side and not the other would be an
// int() that accepts a string CPython rejects, or the reverse.
//
// They do agree, exactly, and this is the list -- recorded from CPython by
// asking which code points int() ignores around a number.
func TestPythonSpaceIsGoSpace(t *testing.T) {
	cpython := []rune{
		0x0009, 0x000A, 0x000B, 0x000C, 0x000D, 0x0020, 0x0085, 0x00A0,
		0x1680, 0x2000, 0x2001, 0x2002, 0x2003, 0x2004, 0x2005, 0x2006,
		0x2007, 0x2008, 0x2009, 0x200A, 0x2028, 0x2029, 0x202F, 0x205F,
		0x3000,
	}
	want := make(map[rune]bool, len(cpython))
	for _, r := range cpython {
		want[r] = true
	}
	for cp := range 0x110000 {
		r := rune(cp)
		if unicode.IsSpace(r) != want[r] {
			t.Errorf("unicode.IsSpace(%U) = %t, CPython's int() says %t",
				r, unicode.IsSpace(r), want[r])
		}
	}
}

// TestDecimalASCII covers the transform itself, including that it leaves an
// all-ASCII subject alone rather than rebuilding it.
func TestDecimalASCII(t *testing.T) {
	for name, tc := range map[string]struct{ in, want string }{
		"ascii is returned as it came": {"42", "42"},
		"arabic-indic":                 {"٤٢", "42"},
		"scripts may be mixed":         {"٤2", "42"},
		"digits keep their place":      {"2٤", "24"},
		"underscores survive":          {"٤_٢", "4_2"},
		"signs survive":                {"-٤٢", "-42"},
		"unicode space becomes ascii":  {" ٤٢　", " 42 "},
		"non-digits are left alone":    {"٤²", "4²"},
		"letters are left alone":       {"café", "café"},
		"empty":                        {"", ""},
	} {
		t.Run(name, func(t *testing.T) {
			if got := DecimalASCII(tc.in, DefaultPythonVersion); got != tc.want {
				t.Errorf("DecimalASCII(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
	// Idempotent, which is what lets both int() and float() apply it
	// without either having to know whether the other already did.
	for _, s := range []string{"42", "٤٢", "٤_٢", " ٤"} {
		if once, twice := DecimalASCII(s, DefaultPythonVersion), DecimalASCII(DecimalASCII(s, DefaultPythonVersion), DefaultPythonVersion); once != twice {
			t.Errorf("DecimalASCII is not idempotent for %q: %q then %q", s, once, twice)
		}
	}
}
