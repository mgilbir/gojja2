// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package value_test

import (
	"encoding/json"
	"math"
	"os"
	"testing"

	"github.com/mgilbir/gojja2/value"
)

// The corpora are produced by tools/oracle/gen_repr.py from a real CPython
// interpreter; see `make repr-corpus`. They are committed so the test runs
// without Python available.

func load[T any](t *testing.T, path string) []T {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read corpus: %v", err)
	}
	var rows []T
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatalf("parse corpus %s: %v", path, err)
	}
	if len(rows) == 0 {
		t.Fatalf("corpus %s is empty", path)
	}
	return rows
}

func TestFormatFloatMatchesCPython(t *testing.T) {
	type row struct {
		Bits uint64 `json:"bits"`
		Repr string `json:"repr"`
	}
	rows := load[row](t, "testdata/repr_float.json")
	bad := 0
	for _, r := range rows {
		f := math.Float64frombits(r.Bits)
		if got := value.FormatFloat(f); got != r.Repr {
			bad++
			if bad <= 10 {
				t.Errorf("FormatFloat(%#016x): got %s, CPython says %s", r.Bits, got, r.Repr)
			}
		}
	}
	if bad > 10 {
		t.Errorf("... and %d more of %d cases", bad-10, len(rows))
	}
	t.Logf("checked %d floats", len(rows))
}

func TestStringReprMatchesCPython(t *testing.T) {
	type row struct {
		S    string `json:"s"`
		Repr string `json:"repr"`
	}
	rows := load[row](t, "testdata/repr_str.json")
	bad := 0
	for _, r := range rows {
		if got := value.Repr(value.String(r.S)); got != r.Repr {
			bad++
			if bad <= 10 {
				t.Errorf("Repr(%q):\n  got     %s\n  CPython %s", r.S, got, r.Repr)
			}
		}
	}
	if bad > 10 {
		t.Errorf("... and %d more of %d cases", bad-10, len(rows))
	}
	t.Logf("checked %d strings", len(rows))
}

// TestContainerRepr pins the shapes CPython uses for containers, including the
// one-element tuple's trailing comma and str()'s divergence from repr().
func TestContainerRepr(t *testing.T) {
	cases := []struct {
		name string
		v    value.Value
		repr string
		str  string
	}{
		{"empty list", value.NewList(), "[]", "[]"},
		{"list", value.NewList(value.Int(1), value.String("a"), value.None), "[1, 'a', None]", "[1, 'a', None]"},
		{"empty tuple", value.NewTuple(), "()", "()"},
		{"one tuple", value.NewTuple(value.Int(1)), "(1,)", "(1,)"},
		{"tuple", value.NewTuple(value.Int(1), value.Int(2)), "(1, 2)", "(1, 2)"},
		{"empty dict", value.NewDict(), "{}", "{}"},
		{"dict", value.DictOf(value.String("a"), value.Int(1)), "{'a': 1}", "{'a': 1}"},
		{"int key dict", value.DictOf(value.Int(1), value.String("a")), "{1: 'a'}", "{1: 'a'}"},
		{"nested", value.NewList(value.NewTuple(value.Int(1), value.Int(2))), "[(1, 2)]", "[(1, 2)]"},
		{"bare string", value.String("a'b"), `"a'b"`, "a'b"},
		{"bool", value.True, "True", "True"},
		{"none", value.None, "None", "None"},
		{"undefined", value.Undefined, "Undefined", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := value.Repr(tc.v); got != tc.repr {
				t.Errorf("Repr: got %s, want %s", got, tc.repr)
			}
			if got := value.Str(tc.v); got != tc.str {
				t.Errorf("Str: got %s, want %s", got, tc.str)
			}
		})
	}
}

// TestAsciiEscapesInsideContainers pins that ascii() is about the whole
// rendered form, not only a bare string. It used to fall back to repr() for
// anything that was not a str, so ascii(['é']) rendered the character where
// CPython escapes it -- and the `%a` conversion inherited that.
func TestAsciiEscapesInsideContainers(t *testing.T) {
	for _, tc := range []struct {
		in   value.Value
		want string
	}{
		{value.String("é"), `'\xe9'`},
		{value.Safe("é"), `Markup('\xe9')`},
		{value.NewList(value.String("é")), `['\xe9']`},
		{value.NewTuple(value.String("é")), `('\xe9',)`},
		{value.NewList(value.NewTuple(value.String("é"))), `[('\xe9',)]`},
		{value.DictOf(value.String("é"), value.String("ü")), `{'\xe9': '\xfc'}`},
		{value.String("ok"), `'ok'`},
		{value.Int(7), `7`},
		{value.String("\U0001F600"), `'\U0001f600'`},
	} {
		if got := value.Ascii(tc.in); got != tc.want {
			t.Errorf("Ascii(%s) = %q, want %q", value.Repr(tc.in), got, tc.want)
		}
	}

	// repr() is unchanged: it leaves printable non-ASCII alone.
	if got := value.Repr(value.NewList(value.String("é"))); got != `['é']` {
		t.Errorf("Repr(['é']) = %q, want %q", got, `['é']`)
	}
}

// TestAsciiEscapesAnObjectsOwnRepr pins that ascii() reaches inside an object
// that renders itself.
//
// Python's ascii() is repr() with what it produced escaped afterwards, so the
// object has no say in it. gojja2 threads the flag through the walk instead,
// which is faster and equivalent -- except that it used to stop at a value
// with a Repr of its own, leaving `{{ "%a" % [g] }}` over a |groupby pair
// escaping the list around it and not the pair inside.
func TestAsciiEscapesAnObjectsOwnRepr(t *testing.T) {
	pair := value.FromObject(reprOnly{"('é', ['ü'])"})
	for _, tc := range []struct {
		name string
		got  string
		want string
	}{
		{"ascii of the object", value.Ascii(pair), `('\xe9', ['\xfc'])`},
		{"ascii inside a list", value.Ascii(value.NewList(pair)), `[('\xe9', ['\xfc'])]`},
		{"repr is left alone", value.Repr(pair), "('é', ['ü'])"},
		{"repr inside a list", value.Repr(value.NewList(pair)), "[('é', ['ü'])]"},
		{"a backslash repr wrote is not escaped again", value.Ascii(value.FromObject(reprOnly{`'a\nb é'`})), `'a\nb \xe9'`},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", tc.name, tc.got, tc.want)
		}
	}
}

// reprOnly is an Object that renders itself and nothing else.
type reprOnly struct{ text string }

func (r reprOnly) GetAttr(string) (value.Value, bool) { return value.Undefined, false }
func (r reprOnly) Repr() string                       { return r.text }
func (r reprOnly) Str() string                        { return r.text }

// TestBytesReprEscapesPerByte: bytes.__repr__ escapes one byte at a time, and
// has no \u or \U form at all. The shared string repr ranges over runes, which
// is right for a str and wrong here -- the two bytes of "é" decode to one rune
// and printed as a single \xe9 where CPython prints \xc3\xa9, and a euro sign
// printed as €, which no bytes repr ever contains.
//
// The bytes themselves were always correct; only the text describing them was
// wrong, so nothing failed and the output was simply read as bytes that were
// never there.
func TestBytesReprEscapesPerByte(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   []byte
		want string
	}{
		// One escape per byte, not per decoded rune.
		{"latin1 range", []byte("é"), `b'\xc3\xa9'`},
		{"three-byte", []byte("€"), `b'\xe2\x82\xac'`},
		{"four-byte", []byte("😀"), `b'\xf0\x9f\x98\x80'`},
		{"mixed", []byte("héllo wörld"), `b'h\xc3\xa9llo w\xc3\xb6rld'`},
		// Raw bytes that are not valid UTF-8 at all survive as themselves
		// rather than collapsing into a replacement character.
		{"invalid utf8", []byte{0xff, 0xfe}, `b'\xff\xfe'`},
		{"lone continuation", []byte{0x80}, `b'\x80'`},
		// Printable ASCII is literal; DEL and the C0 controls are not.
		{"printable", []byte("ab cd~"), `b'ab cd~'`},
		{"nul", []byte{0x00}, `b'\x00'`},
		{"del", []byte{0x7f}, `b'\x7f'`},
		{"escape", []byte{0x1b}, `b'\x1b'`},
		// The short escapes Python prefers over \xNN.
		{"tab newline cr", []byte("\t\n\r"), `b'\t\n\r'`},
		{"backslash", []byte(`\`), `b'\\'`},
		// Quote selection is the str rule: switch to " only when there is
		// a ' and no ".
		{"empty", []byte{}, `b''`},
		{"apostrophe", []byte("it's"), `b"it's"`},
		{"quote", []byte(`say "hi"`), `b'say "hi"'`},
		{"both quotes", []byte(`it's "hi"`), `b'it\'s "hi"'`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := value.Repr(value.Bytes(tc.in)); got != tc.want {
				t.Errorf("Repr(%q) = %s, want %s", tc.in, got, tc.want)
			}
			// ascii() of bytes is the same text: a bytes repr is
			// already ASCII, so there is nothing left to escape.
			if got := value.Ascii(value.Bytes(tc.in)); got != tc.want {
				t.Errorf("Ascii(%q) = %s, want %s", tc.in, got, tc.want)
			}
		})
	}
}
