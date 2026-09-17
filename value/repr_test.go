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
