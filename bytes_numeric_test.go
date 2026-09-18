// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"context"
	"testing"
)

// Python's float() and int() take a bytes exactly as they take a str.
//
// Both filters asked IsString, so a bytes fell through to the filter's
// *default* -- `{{ "1.5".encode()|float }}` answered 0.0 rather than 1.5. A
// wrong number rather than an error, and silent, which is the worst shape a
// conformance defect can have.
func TestNumericFiltersReadBytes(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{`{{ "1.5".encode()|float }}`, "1.5"},
		{`{{ " 1.5 ".encode()|float }}`, "1.5"},
		{`{{ "inf".encode()|float }}`, "inf"},
		{`{{ "15".encode()|int }}`, "15"},
		{`{{ " 15 ".encode()|int }}`, "15"},
		{`{{ "-15".encode()|int }}`, "-15"},
		{`{{ "1_0".encode()|int }}`, "10"},
		// int() falls back to int(float(value)), so a decimal reads.
		{`{{ "1.5".encode()|int }}`, "1"},
		{`{{ "1000".encode()|filesizeformat }}`, "1.0 kB"},
		// A bytes that is not a number still takes the default.
		{`{{ "a".encode()|float }}`, "0.0"},
		{`{{ "a".encode()|float(9) }}`, "9"},
		{`{{ "a".encode()|int }}`, "0"},
		{`{{ "".encode()|int }}`, "0"},
		{`{{ "inf".encode()|int }}`, "0"},
		// str behaviour is untouched.
		{`{{ "1.5"|float }}`, "1.5"},
		{`{{ "a"|float(9) }}`, "9"},
	} {
		checkNum(t, tc.src, tc.want)
	}
}

// do_int passes the base only for a str -- it tests `isinstance(value, str)` --
// so a bytes goes to the bare int(value), which is base ten whatever was asked
// for.
func TestIntFilterBaseIsForStringsOnly(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{`{{ "ff"|int(0, 16) }}`, "255"},
		{`{{ "0x1f"|int(0, 0) }}`, "31"},
		// The same calls on a bytes ignore the base, so "ff" is not a
		// base-ten number and the default comes back.
		{`{{ "ff".encode()|int(0, 16) }}`, "0"},
		{`{{ "0x1f".encode()|int(0, 0) }}`, "0"},
		{`{{ "15".encode()|int(0, 16) }}`, "15"},
		// A base that is not usable is swallowed for either type.
		{`{{ "10"|int(0, 99999) }}`, "10"},
		{`{{ "10".encode()|int(0, 99999) }}`, "10"},
	} {
		checkNum(t, tc.src, tc.want)
	}
}

// filesizeformat's failure is float()'s, so it names the value with its repr.
func TestFilesizeformatRefusalWording(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{`{{ "a".encode()|filesizeformat }}`, "could not convert string to float: b'a'"},
		{`{{ "a"|filesizeformat }}`, "could not convert string to float: 'a'"},
		{`{{ [1]|filesizeformat }}`, "float() argument must be a string or a real number, not 'list'"},
	} {
		tmpl, err := New().FromString(tc.src)
		if err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		if _, err := tmpl.RenderString(context.Background(), nil); err == nil || err.Error() != tc.want {
			t.Errorf("%s: got %v, want %q", tc.src, err, tc.want)
		}
	}
}

// wordwrap is the one filter a bytes gets *past* the attribute lookup of, so
// its failure is textwrap's rather than a missing splitlines -- and an empty
// bytes never reaches textwrap at all.
func TestWordwrapOnBytes(t *testing.T) {
	const pattern = "cannot use a string pattern on a bytes-like object"
	for _, tc := range []struct{ src, want, wantErr string }{
		{`{{ "".encode()|wordwrap }}`, "", ""},
		{`{{ "a".encode()|wordwrap }}`, "", pattern},
		{`{{ "a b".encode()|wordwrap(1) }}`, "", pattern},
		// Everything else still fails at the attribute, as before.
		{`{{ 1|wordwrap }}`, "", "'int' object has no attribute 'splitlines'"},
		{`{{ [1]|wordwrap }}`, "", "'list' object has no attribute 'splitlines'"},
		{`{{ none|wordwrap }}`, "", "'NoneType' object has no attribute 'splitlines'"},
	} {
		tmpl, err := New().FromString(tc.src)
		if err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		got, err := tmpl.RenderString(context.Background(), nil)
		if tc.wantErr != "" {
			if err == nil || err.Error() != tc.wantErr {
				t.Errorf("%s: got %q %v, want %q", tc.src, got, err, tc.wantErr)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("%s = %q, %v; want %q", tc.src, got, err, tc.want)
		}
	}
}

// json.dumps builds the indent before it discovers it cannot serialise the
// value, so an unusable indent is reported ahead of an unserialisable bytes.
// Only a str short-circuits before the indent is touched at all.
func TestToJSONIndentIsResolvedBeforeBytesIsRefused(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{`{{ "a".encode()|tojson(1.5) }}`, "can't multiply sequence by non-int of type 'float'"},
		{`{{ "a".encode()|tojson([1]) }}`, "can't multiply sequence by non-int of type 'list'"},
		{`{{ "a".encode()|tojson }}`, "Object of type bytes is not JSON serializable"},
	} {
		tmpl, err := New().FromString(tc.src)
		if err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		if _, err := tmpl.RenderString(context.Background(), nil); err == nil || err.Error() != tc.want {
			t.Errorf("%s: got %v, want %q", tc.src, err, tc.want)
		}
	}
	// A str is answered before the indent is looked at, however bad it is.
	checkNum(t, `{{ "s"|tojson(1.5) }}`, `"s"`)
}

func checkNum(t *testing.T, src, want string) {
	t.Helper()
	tmpl, err := New().FromString(src)
	if err != nil {
		t.Errorf("%s: compile: %v", src, err)
		return
	}
	got, err := tmpl.RenderString(context.Background(), nil)
	if err != nil || got != want {
		t.Errorf("%s = %q, %v; want %q", src, got, err, want)
	}
}
