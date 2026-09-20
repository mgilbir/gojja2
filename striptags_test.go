// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import "testing"

// TestStriptagsResolvesEveryCharacterReference pins that |striptags unescapes
// what Python unescapes.
//
// It ends in markupsafe's unescape, which is html.unescape, which resolves
// every HTML 5 named reference -- 2231 of them, the uppercase spellings and
// the semicolon-less forms included -- and numeric references under the
// standard's rules for the invalid ones. Eight hand-picked entities get eight
// of them right; `{{ html|upper|striptags }}` produces `&AMP;`, which was one
// of the 2,223 left behind.
//
// The table is generated from the same CPython this project grades against;
// see tools/oracle/gen_entities.py. Expectations here are CPython jinja2
// 3.1.6's.
func TestStriptagsResolvesEveryCharacterReference(t *testing.T) {
	env := mustNew()
	for _, tc := range []struct{ expr, want string }{
		// The case that found this: upper-casing an entity leaves a
		// spelling the standard still defines.
		{`'<b>a &amp; b</b>'|upper|striptags`, "A & B"},
		{`'<b>&copy;</b> &reg; &Yacute;'|striptags`, "© ® Ý"},
		// Numeric references, including the ones the standard remaps
		// rather than accepts and the ones out of range.
		{`'&#38;|&#x26;|&#X26;|&#0000038;'|striptags`, "&|&|&|&"},
		{`'&#128;'|striptags`, "€"},
		{`'&#55296;|&#1114112;'|striptags`, "�|�"},
		{`'&#13;'|striptags`, "\r"},
		// A name without its semicolon takes the longest prefix that
		// is one, and leaves the rest.
		{`'&notit;|&notit|&not|&amp|&ampx'|striptags`, "¬it;|¬it|¬|&|&x"},
		{`'&#;|&;|&'|striptags`, "&#;|&;|&"},
		// Whitespace collapses before the references resolve, so one
		// that stands for a space survives as a character.
		{`'a&nbsp;b'|striptags`, "a b"},
		{`'a &nbsp; b'|striptags`, "a   b"},
		{`'&#32;a&#32;'|striptags`, " a "},
		{`'a&Tab;b'|striptags`, "a\tb"},
	} {
		got, err := renderVars(t, env, "{{ "+tc.expr+" }}", nil)
		if err != nil {
			t.Errorf("%s: %v", tc.expr, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s = %q, want %q", tc.expr, got, tc.want)
		}
	}
}

// TestHTMLEntityTableIsComplete guards the generated table against being
// quietly emptied or truncated: it is 2231 references, and a template can
// reach any of them.
func TestHTMLEntityTableIsComplete(t *testing.T) {
	if n := len(htmlEntities); n != 2231 {
		t.Errorf("htmlEntities has %d entries, want the 2231 CPython defines", n)
	}
	for _, name := range []string{"amp;", "AMP;", "amp", "AMP", "copy;", "Yacute;", "Tab;", "nbsp;"} {
		if _, ok := htmlEntities[name]; !ok {
			t.Errorf("htmlEntities is missing %q", name)
		}
	}
	if len(invalidCharrefs) != 34 || len(invalidCodepoints) != 126 {
		t.Errorf("invalid tables are %d and %d, want 34 and 126",
			len(invalidCharrefs), len(invalidCodepoints))
	}
}
