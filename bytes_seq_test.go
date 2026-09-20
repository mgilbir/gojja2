// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"context"
	"testing"
)

// A bytes is a sequence of integers, and the two places that did not know it.
//
// Indexing one already gave the byte value -- b"ab"[0] is 97, not "a" -- and
// iteration, len, comparison, concatenation, repetition and every sequence
// filter were right too. Slicing was not implemented at all, so `{{ x[0] }}`
// answered and `{{ x[0:1] }}` said the object was not subscriptable. And
// membership refused an integer, where CPython reads it as the byte value to
// look for.
func TestBytesSlicing(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{`{{ "ab".encode()[0:1] }}`, `b'a'`},
		{`{{ "ab".encode()[1:] }}`, `b'b'`},
		{`{{ "ab".encode()[:] }}`, `b'ab'`},
		{`{{ "ab".encode()[::-1] }}`, `b'ba'`},
		{`{{ "abcdef".encode()[1:5:2] }}`, `b'bd'`},
		{`{{ "ab".encode()[5:] }}`, `b''`},
		{`{{ "ab".encode()[-1:] }}`, `b'b'`},
		// Positions are bytes, not code points: this character is two
		// bytes, and slicing one of them gives one byte back.
		{`{{ "\u00e9".encode()|length }}`, "2"},
		{`{{ "\u00e9".encode()[0:1]|length }}`, "1"},
		// Indexing still gives the integer, which is what made the gap
		// easy to miss.
		{`{{ "ab".encode()[0] }}`, "97"},
	} {
		tmpl, err := mustNew().FromString(tc.src)
		if err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		got, err := tmpl.RenderString(context.Background(), nil)
		if err != nil || got != tc.want {
			t.Errorf("%s = %q, %v; want %q", tc.src, got, err, tc.want)
		}
	}
}

// `x in b"..."` with an integer asks whether that byte value occurs. Out of a
// byte's range it is refused rather than answered False, because the question
// is malformed rather than unsatisfied.
func TestBytesMembershipTakesAnInteger(t *testing.T) {
	for _, tc := range []struct{ src, want, wantErr string }{
		{`{{ 97 in "ab".encode() }}`, "True", ""},
		{`{{ 98 in "ab".encode() }}`, "True", ""},
		{`{{ 0 in "ab".encode() }}`, "False", ""},
		{`{{ 255 in "ab".encode() }}`, "False", ""},
		{`{{ 97 not in "ab".encode() }}`, "False", ""},
		// A bool is an int here as everywhere in Python.
		{`{{ true in "ab".encode() }}`, "False", ""},
		{`{{ false in "ab".encode() }}`, "False", ""},
		// Outside a byte's range, in either direction or too wide to fit.
		{`{{ 256 in "ab".encode() }}`, "", "byte must be in range(0, 256)"},
		{`{{ -1 in "ab".encode() }}`, "", "byte must be in range(0, 256)"},
		{`{{ 2**70 in "ab".encode() }}`, "", "byte must be in range(0, 256)"},
		// A bytes on the left is still a substring search, and anything
		// else is still refused.
		{`{{ "a".encode() in "ab".encode() }}`, "True", ""},
		{`{{ "c".encode() in "ab".encode() }}`, "False", ""},
		{`{{ "a" in "ab".encode() }}`, "", "a bytes-like object is required, not 'str'"},
		{`{{ 1.5 in "ab".encode() }}`, "", "a bytes-like object is required, not 'float'"},
		{`{{ none in "ab".encode() }}`, "", "a bytes-like object is required, not 'NoneType'"},
	} {
		tmpl, err := mustNew().FromString(tc.src)
		if err != nil {
			t.Errorf("%s: compile: %v", tc.src, err)
			continue
		}
		got, err := tmpl.RenderString(context.Background(), nil)
		if tc.wantErr != "" {
			if err == nil || err.Error() != tc.wantErr {
				t.Errorf("%s: got %q %v, want error %q", tc.src, got, err, tc.wantErr)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("%s = %q, %v; want %q", tc.src, got, err, tc.want)
		}
	}
}
