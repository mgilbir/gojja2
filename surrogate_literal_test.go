// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"context"
	"testing"
)

// TestLoneSurrogateLiteral pins what a surrogate escape in a string literal
// becomes, which no corpus case can: CPython's answer is a string holding a lone
// surrogate, and the corpus's reference files are UTF-8, which cannot hold one
// either -- gen_syntax.py fails to write the parse tree of such a case.
//
// A Go string is UTF-8 too, so the lexer writes U+FFFD for every surrogate
// escape, `\u` or `\U`, paired or not. CPython does not join a pair either,
// so the length agrees; equality, |tojson and every encoder do not, and
// docs/divergences.md says so.
//
// The escapes are spelled with a backslash constant rather than in the source,
// so that no tool between an editor and the compiler can decode a pair into the
// character it encodes -- which is the thing under test.
func TestLoneSurrogateLiteral(t *testing.T) {
	const bs = "\\"
	u := func(h string) string { return bs + "u" + h }
	U := func(h string) string { return bs + "U" + h }
	for _, tc := range []struct{ src, want string }{
		{"{{ '" + u("d800") + "' == '" + u("fffd") + "' }}", "True"},
		{"{{ '" + u("d800") + "'|length }}", "1"},
		{"{{ '" + u("d800") + "'|tojson }}", `"` + bs + `ufffd"`},
		// The eight-digit form is taken too, as CPython takes it; it was
		// refused as an illegal character.
		{"{{ '" + U("0000dfff") + "' == '" + u("fffd") + "' }}", "True"},
		{"{{ '" + U("0000d83d") + U("0000de00") + "'|length }}", "2"},
		// A pair is two characters on CPython as well, not the one it
		// would encode in UTF-16.
		{"{{ '" + u("d83d") + u("de00") + "'|length }}|{{ '" + u("d83d") + u("de00") +
			"' == '" + U("0001f600") + "' }}", "2|False"},
		{"{{ '" + u("de00") + u("d83d") + "'|length }}", "2"},
	} {
		tmpl, err := mustEnv().FromString(tc.src)
		if err != nil {
			t.Fatalf("compile %q: %v", tc.src, err)
		}
		got, err := tmpl.RenderString(context.Background(), nil)
		if err != nil {
			t.Fatalf("%s: %v", tc.src, err)
		}
		if got != tc.want {
			t.Errorf("%s = %q, want %q", tc.src, got, tc.want)
		}
	}
}
