// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"unicode"
)

// Every code point's case answer, checked against CPython's.
//
// The generated tables record only where Python's full case mapping differs
// from Go's simple one, which is safe only while the two still agree
// everywhere else. They do today -- Go is on Unicode 15.0.0 and CPython 3.11
// on 14.0.0, and no single-rune mapping differs -- but nothing makes that stay
// true. A Go release is free to move a mapping, and the failure would be
// silent and per-character.
//
// So the generator writes down a digest over every code point's upper, lower,
// title, casefold, islower, isupper and istitle, and this recomputes it from
// gojja2's own functions. It is a total check: 1,112,064 code points, every
// operation, one constant. If it fails after a toolchain upgrade, regenerate
// with `make casemap` and read the diff -- it is telling you Unicode moved.
func TestCaseMappingMatchesCPython(t *testing.T) {
	h := sha256.New()
	var line strings.Builder
	for cp := 0; cp < 0x110000; cp++ {
		if cp >= 0xD800 && cp < 0xE000 {
			// A lone surrogate has no UTF-8 encoding and a Go string
			// cannot hold one; the generator skips them too.
			continue
		}
		r := rune(cp)
		s := string(r)
		line.Reset()
		fmt.Fprintf(&line, "%d\t%s\t%s\t%s\t%s\t%d%d%d\n",
			cp, pyUpperString(s), pyLowerString(s), mustTitle(t, s), pyCasefold(s),
			b2i(isLowerString(s)), b2i(isUpperString(s)), b2i(isTitleString(s)))
		h.Write([]byte(line.String()))
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != caseMapDigest {
		t.Errorf("case mapping digest = %s, want %s\n"+
			"Go is on Unicode %s. Regenerate with `make casemap` and read the diff:\n"+
			"a mapping moved, and the tables record only the differences.",
			got, caseMapDigest, unicode.Version)
	}
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// The characters that motivated this, spelled out so a reader sees what "full
// case mapping" means without decoding a digest.
func TestFullCaseMappingExpands(t *testing.T) {
	for _, tc := range []struct{ in, op, want string }{
		{"ß", "upper", "SS"},    // LATIN SMALL LETTER SHARP S
		{"ß", "title", "Ss"},    // titlecase differs from uppercase
		{"ß", "casefold", "ss"}, //
		{"ﬃ", "upper", "FFI"},   // LATIN SMALL LIGATURE FFI
		{"İ", "lower", "i̇"},    // I WITH DOT ABOVE keeps its dot
		{"ΐ", "upper", "Ϊ́"},
		{"ς", "casefold", "σ"}, // final sigma folds onto sigma
		{"µ", "casefold", "μ"}, // MICRO SIGN folds onto Greek mu
		{"ſ", "casefold", "s"}, // LATIN SMALL LETTER LONG S
	} {
		var got string
		switch tc.op {
		case "upper":
			got = pyUpperString(tc.in)
		case "lower":
			got = pyLowerString(tc.in)
		case "title":
			got = mustTitle(t, tc.in)
		case "casefold":
			got = pyCasefold(tc.in)
		}
		if got != tc.want {
			t.Errorf("%q.%s() = %q, want %q", tc.in, tc.op, got, tc.want)
		}
	}
}

// A word boundary in title() is an uncased character, not a non-letter. A
// digit is uncased, so it ends a word and the next letter is titlecased --
// which reading the boundary as "letter or digit" got backwards.
func TestTitleWordBoundaryIsCasedness(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"a1b", "A1B"},
		{"hello world's", "Hello World'S"},
		{"x-ray", "X-Ray"},
		{"2nd place", "2Nd Place"},
	} {
		if got := mustTitle(t, tc.in); got != tc.want {
			t.Errorf("%q.title() = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Every entry point that can reach a case operation goes through casing.go.
//
// The digest above proves the functions are right; this proves they are the
// ones being called. The two halves are separate failures: the mappings were
// wrong everywhere, and even with them right a call site left on
// strings.ToUpper would be wrong in exactly one place and nowhere else.
func TestCaseOperationsAreWiredEverywhere(t *testing.T) {
	const sharp = "ß"  // ß: upper "SS", title "Ss", casefold "ss"
	const ligFFI = "ﬃ" // ﬃ: upper "FFI"
	const ordFem = "ª" // ª: cased and lowercase, but not Ll
	const romanI = "Ⅰ" // Ⅰ: cased and uppercase, but not Lu

	for _, tc := range []struct{ src, want string }{
		// str methods
		{`{{ "` + sharp + `".upper() }}`, "SS"},
		{`{{ "` + sharp + `".title() }}`, "Ss"},
		{`{{ "` + sharp + `".capitalize() }}`, "Ss"},
		{`{{ "` + sharp + `".swapcase() }}`, "SS"},
		{`{{ "` + sharp + `".casefold() }}`, "ss"},
		{`{{ "` + ligFFI + `".upper() }}`, "FFI"},
		{`{{ "İ".lower() }}`, "i̇"},
		{`{{ "ς".casefold() }}`, "σ"},
		// str predicates, on characters cased only by Other_Lowercase /
		// Other_Uppercase -- the ones Go's categories miss.
		{`{{ "` + ordFem + `".islower() }}`, "True"},
		{`{{ "` + ordFem + `".isupper() }}`, "False"},
		{`{{ "` + romanI + `".isupper() }}`, "True"},
		{`{{ "` + romanI + `".istitle() }}`, "True"},
		{`{{ "ͅ".islower() }}`, "True"},
		// filters
		{`{{ "` + sharp + `"|upper }}`, "SS"},
		{`{{ "` + sharp + `"|capitalize }}`, "Ss"},
		// jinja2's |title is not str.title(). The filter builds each word as
		// `item[0].upper() + item[1:].lower()`, so the first character takes
		// the *uppercase* mapping; the method takes the titlecase one. For ß
		// those differ -- "SS" against "Ss" -- and each is CPython's answer to
		// its own question.
		{`{{ "` + sharp + `"|title }}`, "SS"},
		{`{{ "` + sharp + `".title() }}`, "Ss"},
		{`{{ "` + sharp + `x y` + sharp + `"|title }}`, "SSx Y" + sharp},
		{`{{ "` + sharp + `x y` + sharp + `".title() }}`, "Ssx Y" + sharp},
		{`{{ "İ"|lower }}`, "i̇"},
		// tests
		{`{{ "` + ordFem + `" is lower }}`, "True"},
		{`{{ "` + romanI + `" is upper }}`, "True"},
		// title's word boundary is casedness, in the method and the filter
		{`{{ "a1b".title() }}`, "A1B"},
		{`{{ "x-ray"|title }}`, "X-Ray"},
	} {
		tmpl, err := New().FromString(tc.src)
		if err != nil {
			t.Errorf("%s: compile: %v", tc.src, err)
			continue
		}
		got, err := tmpl.RenderString(context.Background(), nil)
		if err != nil || got != tc.want {
			t.Errorf("%s = %q, %v; want %q", tc.src, got, err, tc.want)
		}
	}
}

// mustTitle is str.title() with no render to charge it to, which is what every
// call here is: a nil State polls nothing and cannot refuse.
func mustTitle(t *testing.T, s string) string {
	t.Helper()
	out, err := pyTitleString(nil, s)
	if err != nil {
		t.Fatalf("title(%q): %v", s, err)
	}
	return out
}
