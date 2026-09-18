// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"context"
	"testing"
)

// The bytes methods, and the three things that make them not the str methods.
//
// bytes had one method -- decode -- so a type the engine otherwise supports
// fully answered an attribute error to the other forty-one. These cover what
// differs rather than what is the same:
//
//   - Case and classification are ASCII only. A byte above 127 has no case,
//     because bytes carries no encoding to interpret it with.
//   - Positions and lengths are bytes, not code points.
//   - Arguments must be bytes-like, and several take an integer as a byte.
//
// Every expectation is CPython's own answer, taken from the oracle.
func TestBytesMethodsAreASCIIOnly(t *testing.T) {
	// The two-character receiver below is four bytes, and none of them is
	// an ASCII letter, so every case operation leaves it alone. The same
	// characters as a str uppercase to "É" and "SS".
	const nonASCII = `"éß".encode()`
	for _, tc := range []struct{ src, want string }{
		{`{{ "aBc dEf".encode().upper() }}`, `b'ABC DEF'`},
		{`{{ "aBc dEf".encode().lower() }}`, `b'abc def'`},
		{`{{ "aBc dEf".encode().title() }}`, `b'Abc Def'`},
		{`{{ "aBc dEf".encode().capitalize() }}`, `b'Abc def'`},
		{`{{ "aBc dEf".encode().swapcase() }}`, `b'AbC DeF'`},
		{`{{ ` + nonASCII + `.upper() }}`, `b'\xc3\xa9\xc3\x9f'`},
		{`{{ ` + nonASCII + `.lower() }}`, `b'\xc3\xa9\xc3\x9f'`},
		{`{{ ` + nonASCII + `.swapcase() }}`, `b'\xc3\xa9\xc3\x9f'`},
		// A digit is uncased for bytes too, so it ends a word.
		{`{{ "a1b".encode().title() }}`, `b'A1B'`},
		// Classification is ASCII only, and every predicate but isascii
		// is false for the empty bytes.
		{`{{ "abc".encode().isalpha() }}`, "True"},
		{`{{ ` + nonASCII + `.isalpha() }}`, "False"},
		{`{{ "".encode().isalpha() }}`, "False"},
		{`{{ "".encode().isascii() }}`, "True"},
		{`{{ ` + nonASCII + `.isascii() }}`, "False"},
		{`{{ "123".encode().isdigit() }}`, "True"},
		{`{{ "Ab".encode().istitle() }}`, "True"},
		{`{{ "AB".encode().istitle() }}`, "False"},
	} {
		checkBytesMethod(t, tc.src, tc.want)
	}
}

// Positions are bytes. A two-byte character pads to eight bytes of fill in a
// width of ten, not nine, and slicing counts bytes throughout.
func TestBytesMethodsCountBytes(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{`{{ "éß".encode().ljust(10, "*".encode()) }}`, `b'\xc3\xa9\xc3\x9f******'`},
		{`{{ "éß".encode().center(10) }}`, `b'   \xc3\xa9\xc3\x9f   '`},
		{`{{ "éß".encode().count("".encode()) }}`, "5"},
		{`{{ "éß".encode().replace("".encode(), "-".encode()) }}`, `b'-\xc3-\xa9-\xc3-\x9f-'`},
		{`{{ "ab".encode().center(6, "*".encode()) }}`, `b'**ab**'`},
		{`{{ "42".encode().zfill(5) }}`, `b'00042'`},
		{`{{ "-42".encode().zfill(5) }}`, `b'-0042'`},
	} {
		checkBytesMethod(t, tc.src, tc.want)
	}
}

// A search's start is resolved but not clamped up: past the end it finds
// nothing, not even the empty needle at the end of the data.
func TestBytesSearchBounds(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{`{{ "abc".encode().find("".encode(), 3) }}`, "3"},
		{`{{ "abc".encode().find("".encode(), 4) }}`, "-1"},
		{`{{ "abc".encode().rfind("".encode(), 4) }}`, "-1"},
		{`{{ "abc".encode().count("".encode(), 4) }}`, "0"},
		{`{{ "abc".encode().count("".encode()) }}`, "4"},
		{`{{ "abc".encode().count("".encode(), 1, 0) }}`, "0"},
		{`{{ "abc".encode().startswith("".encode(), 4) }}`, "False"},
		{`{{ "abc".encode().startswith("".encode(), 3) }}`, "True"},
		{`{{ "abc".encode().endswith("".encode(), 4) }}`, "False"},
		{`{{ "abc".encode().find("a".encode(), -2) }}`, "-1"},
		// An integer is a byte value here, as it is for `in`.
		{`{{ "aBc".encode().find(66) }}`, "1"},
		{`{{ "aBc".encode().count(97) }}`, "1"},
	} {
		checkBytesMethod(t, tc.src, tc.want)
	}
}

// The refusals, which name bytes rather than str and differ from the str
// methods' wording in three places.
func TestBytesMethodRefusals(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{`{{ "ab".encode().replace("a", "X") }}`, "a bytes-like object is required, not 'str'"},
		{`{{ "ab".encode().find("a") }}`, "argument should be integer or bytes-like object, not 'str'"},
		{`{{ "ab".encode().startswith("a") }}`, "startswith first arg must be bytes or a tuple of bytes, not str"},
		// "subsection", where str says "substring".
		{`{{ "ab".encode().index("zz".encode()) }}`, "subsection not found"},
		{`{{ "ab".encode().center(6, "**".encode()) }}`, "center() argument 2 must be a byte string of length 1, not bytes"},
		{`{{ "ab".encode().center(6, "*") }}`, "center() argument 2 must be a byte string of length 1, not str"},
		{`{{ "-".encode().join(["a", "b"]) }}`, "sequence item 0: expected a bytes-like object, str found"},
		// A bytes is itself iterable and yields integers, so joining over
		// one fails at its first element rather than at the argument.
		{`{{ "-".encode().join("ab".encode()) }}`, "sequence item 0: expected a bytes-like object, int found"},
		{`{{ (0).from_bytes("a", "big") }}`, "cannot convert 'str' object to bytes"},
		{`{{ (0.0).fromhex(5) }}`, "bad argument type for built-in operation"},
		{`{{ (0.0).fromhex("zz") }}`, "invalid hexadecimal floating-point string"},
	} {
		tmpl, err := New().FromString(tc.src)
		if err != nil {
			t.Errorf("%s: compile: %v", tc.src, err)
			continue
		}
		if _, err := tmpl.RenderString(context.Background(), nil); err == nil || err.Error() != tc.want {
			t.Errorf("%s: got %v, want %q", tc.src, err, tc.want)
		}
	}
}

// hex, fromhex, maketrans/translate, and the two classmethods a template can
// only reach through an instance.
func TestBytesConversions(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{`{{ "ab".encode().hex() }}`, "6162"},
		{`{{ "".encode().hex() }}`, ""},
		{`{{ "abc".encode().hex("-") }}`, "61-62-63"},
		// A positive grouping counts from the right, so the leading group
		// is the short one.
		{`{{ "abcde".encode().hex("-", 2) }}`, "61-6263-6465"},
		{`{{ "abcde".encode().hex("-", -2) }}`, "6162-6364-65"},
		{`{{ "ab".encode().hex(sep="-") }}`, "61-62"},
		{`{{ "x".encode().fromhex("6162") }}`, `b'ab'`},
		{`{{ "x".encode().fromhex("61 62") }}`, `b'ab'`},
		{`{{ "abc".encode().translate(none, "b".encode()) }}`, `b'ac'`},
		{`{{ "abc".encode().translate("x".encode().maketrans("abc".encode(), "xyz".encode())) }}`, `b'xyz'`},
		{`{{ (0).from_bytes("a".encode(), "big") }}`, "97"},
		{`{{ (0).from_bytes("ab".encode(), "little") }}`, "25185"},
		{`{{ (0).from_bytes("ÿ".encode(), "big", signed=true) }}`, "-15425"},
		{`{{ (0.0).fromhex("0x1.8p+0") }}`, "1.5"},
		// Python's grammar: the 0x and the exponent are both optional and
		// the digits are hex either way, so "1.8" is 1.5 and not 1.8.
		{`{{ (0.0).fromhex("1.8") }}`, "1.5"},
		{`{{ (0.0).fromhex("-0x1.8p-1") }}`, "-0.75"},
		{`{{ (0.0).fromhex("  1.8  ") }}`, "1.5"},
		{`{{ (0.0).fromhex("inf") }}`, "inf"},
	} {
		checkBytesMethod(t, tc.src, tc.want)
	}
}

func checkBytesMethod(t *testing.T, src, want string) {
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
