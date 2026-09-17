// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"context"
	"strings"
	"testing"

	"github.com/mgilbir/gojja2/errs"
)

// TestEncodeDecodeCodecs: str.encode ignored both of its arguments and always
// answered UTF-8, so `{{ "é".encode("latin-1") }}` gave the two UTF-8 bytes
// rather than the one latin-1 byte and `{{ "€".encode("ascii") }}` gave bytes
// at all rather than raising. bytes had no methods, so nothing could decode.
func TestEncodeDecodeCodecs(t *testing.T) {
	env := New()
	for _, tc := range []struct{ src, want string }{
		// The codec is honoured, and its aliases are the same codec.
		{`{{ "é".encode() }}`, `b'\xc3\xa9'`},
		{`{{ "é".encode("utf-8") }}|{{ "é".encode("UTF-8") }}|{{ "é".encode("u8") }}`,
			`b'\xc3\xa9'|b'\xc3\xa9'|b'\xc3\xa9'`},
		{`{{ "é".encode("latin-1") }}|{{ "é".encode("latin1") }}|{{ "é".encode("iso-8859-1") }}`,
			`b'\xe9'|b'\xe9'|b'\xe9'`},
		{`{{ "abc".encode("ascii") }}`, `b'abc'`},
		// Every error handler, on a character the codec cannot hold.
		{`{{ "é".encode("ascii", "ignore") }}`, `b''`},
		{`{{ "é".encode("ascii", "replace") }}`, `b'?'`},
		{`{{ "é".encode("ascii", "xmlcharrefreplace") }}`, `b'&#233;'`},
		{`{{ "é".encode("ascii", "backslashreplace") }}`, `b'\\xe9'`},
		{`{{ "€".encode("ascii", "xmlcharrefreplace") }}`, `b'&#8364;'`},
		{`{{ "€".encode("ascii", "backslashreplace") }}`, `b'\\u20ac'`},
		{`{{ "€".encode("latin-1", "ignore") }}`, `b''`},
		// latin-1 holds é, so a handler never runs for it.
		{`{{ "é".encode("latin-1", "ignore") }}`, `b'\xe9'`},
		// decode round-trips, and latin-1 decodes anything at all.
		{`{{ "é".encode().decode() }}`, `é`},
		{`{{ "é".encode("latin-1").decode("latin-1") }}`, `é`},
		{`{{ "abc".encode().decode("ascii")|upper }}`, `ABC`},
		{`{{ "é".encode().decode("latin-1") }}`, `Ã©`},
		// The bytes are unchanged by how they are described.
		{`{{ "héllo wörld".encode()|length }}|{{ "héllo wörld"|length }}`, `13|11`},
	} {
		tmpl, err := env.FromString(tc.src)
		if err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		got, err := tmpl.RenderString(context.Background(), nil)
		if err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s = %q, want %q", tc.src, got, tc.want)
		}
	}

	for _, tc := range []struct {
		src  string
		kind errs.Kind
		want string
	}{
		// The position is counted in characters, as CPython counts it.
		{`{{ "é".encode("ascii") }}`, errs.UnicodeEncodeError,
			`'ascii' codec can't encode character '\xe9' in position 0: ordinal not in range(128)`},
		{`{{ "aéb".encode("ascii") }}`, errs.UnicodeEncodeError,
			`'ascii' codec can't encode character '\xe9' in position 1: ordinal not in range(128)`},
		{`{{ "€".encode("latin-1") }}`, errs.UnicodeEncodeError,
			`'latin-1' codec can't encode character '\u20ac' in position 0: ordinal not in range(256)`},
		{`{{ "😀".encode("ascii") }}`, errs.UnicodeEncodeError,
			`'ascii' codec can't encode character '\U0001f600' in position 0: ordinal not in range(128)`},
		{`{{ "é".encode().decode("ascii") }}`, errs.UnicodeDecodeError,
			`'ascii' codec can't decode byte 0xc3 in position 0: ordinal not in range(128)`},
		// A codec with a character table is refused rather than silently
		// answered in some other encoding; see docs/divergences.md.
		{`{{ "é".encode("cp1252") }}`, errs.LookupError, "unknown encoding: cp1252"},
		{`{{ "é".encode("bogus") }}`, errs.LookupError, "unknown encoding: bogus"},
	} {
		tmpl, err := env.FromString(tc.src)
		if err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		_, err = tmpl.RenderString(context.Background(), nil)
		if err == nil {
			t.Errorf("%s: rendered; want %v", tc.src, tc.kind)
			continue
		}
		if kind := errs.KindOf(err); kind != tc.kind {
			t.Errorf("%s: got %v, want %v", tc.src, kind, tc.kind)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %q, want %q", tc.src, err, tc.want)
		}
	}

	// Python derives UnicodeEncodeError from ValueError, so a template that
	// catches the one catches the other.
	if !errs.UnicodeEncodeError.DerivesFrom(errs.ValueError) {
		t.Error("UnicodeEncodeError should derive from ValueError")
	}
}
