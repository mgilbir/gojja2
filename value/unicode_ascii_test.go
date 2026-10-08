// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package value

import (
	"testing"
	"unicode"
	"unicode/utf8"
)

var everyVersion = []PythonVersion{Python311, Python312, Python313, Python314}

// The tables walked the slow way, which is what NameClass and IsIdentifier did
// before they learned an ASCII fast path.
func nameClassByTable(r rune, py PythonVersion) bool {
	in := unicode.Is(nameClassDefault, r)
	if t := nameClassOther[py]; t != nil && unicode.Is(t, r) {
		return !in
	}
	return in
}

func isIdentifierByTable(s string, py PythonVersion) bool {
	if s == "" {
		return false
	}
	u := UnicodeFor(py)
	for i, r := range s {
		ok := u.IsXIDContinue(r, XIDContinueDefault(r))
		if i == 0 {
			ok = u.IsXIDStart(r, XIDStartDefault(r))
		}
		if !ok {
			return false
		}
	}
	return true
}

// TestASCIIFastPathsAgreeWithTheTables holds the lexer's ASCII shortcut to the
// tables it was read from, on every interpreter: every character on its own,
// every pair, and each of those leading into a name that leaves ASCII, which
// is where the fast path hands over to the slow one.
func TestASCIIFastPathsAgreeWithTheTables(t *testing.T) {
	if asciiOverridden {
		t.Fatal("some interpreter's tables give an ASCII character a different answer, " +
			"so the fast path is off and this test is checking nothing; " +
			"make the ASCII tables per-version instead")
	}
	tails := []string{"", "é", "²", "࢘", "a²"}
	for _, py := range everyVersion {
		for a := range rune(utf8.RuneSelf) {
			if got, want := NameClass(a, py), nameClassByTable(a, py); got != want {
				t.Errorf("%s: NameClass(%q) = %v, the table says %v", py, a, got, want)
			}
			for b := range rune(utf8.RuneSelf) {
				for _, tail := range tails {
					s := string(a) + string(b) + tail
					if got, want := IsIdentifier(s, py), isIdentifierByTable(s, py); got != want {
						t.Errorf("%s: IsIdentifier(%q) = %v, the table says %v", py, s, got, want)
					}
				}
			}
			for _, tail := range tails {
				s := string(a) + tail
				if got, want := IsIdentifier(s, py), isIdentifierByTable(s, py); got != want {
					t.Errorf("%s: IsIdentifier(%q) = %v, the table says %v", py, s, got, want)
				}
			}
		}
	}
}
