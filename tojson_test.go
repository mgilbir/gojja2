// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"context"
	"strings"
	"testing"
)

// TestToJSONIndentZeroStillBreaksLines: json.dumps splits "no indent" from "an
// indent of zero". Only None gives the one-line form; 0 still puts every
// element on its own line, just without leading spaces, and the item separator
// loses its trailing space because the newline carries it.
//
// gojja2 defaulted the argument to 0 and treated anything below 1 as "no
// indent", so `{{ x|tojson(0) }}` came out on one line. A template asking for
// the broken-up form got the compact one, and nothing said so.
func TestToJSONIndentZero(t *testing.T) {
	env := New()
	for _, tc := range []struct{ src, want string }{
		// No argument, and an explicit None, are the compact form.
		{`{{ [1,2]|tojson }}`, `[1, 2]`},
		{`{{ [1,2]|tojson(none) }}`, `[1, 2]`},
		{`{{ [1,2]|tojson(indent=none) }}`, `[1, 2]`},
		// Zero breaks the lines without indenting them.
		{`{{ [1,2]|tojson(0) }}`, "[\n1,\n2\n]"},
		{`{{ [1,2]|tojson(indent=0) }}`, "[\n1,\n2\n]"},
		// A negative indent is zero, not "none".
		{`{{ [1,2]|tojson(-1) }}`, "[\n1,\n2\n]"},
		// And a positive one indents per level, as before.
		{`{{ [1,2]|tojson(2) }}`, "[\n  1,\n  2\n]"},
		{`{{ [[1]]|tojson(2) }}`, "[\n  [\n    1\n  ]\n]"},
		{`{{ [[1]]|tojson(0) }}`, "[\n[\n1\n]\n]"},
		// Dicts take the same treatment, keys sorted as jinja2 asks.
		{`{{ {"b":1,"a":2}|tojson(0) }}`, "{\n\"a\": 2,\n\"b\": 1\n}"},
		{`{{ {"b":1,"a":2}|tojson }}`, `{"a": 2, "b": 1}`},
		// A scalar has no lines to break.
		{`{{ 1|tojson(0) }}|{{ "x"|tojson(0) }}|{{ none|tojson(0) }}`, `1|"x"|null`},
		// Empty containers likewise.
		{`{{ []|tojson(0) }}|{{ {}|tojson(0) }}`, `[]|{}`},
	} {
		tmpl, err := env.FromString(tc.src)
		if err != nil {
			t.Fatalf("compile %q: %v", tc.src, err)
		}
		got, err := tmpl.RenderString(context.Background(), nil)
		if err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s\n got %q\nwant %q", tc.src, got, tc.want)
		}
	}
}

// TestToJSONStringIndent: json.dumps takes either an integer or a *string* for
// its indent, and a string is used literally -- `tojson("\t")` indents with
// tabs. gojja2 read the argument as an integer, so every string was refused.
//
// The argument is only looked at that closely once something needs indenting,
// which is why a str value never raises: JSONEncoder.encode returns the encoded
// string before building any indentation, while everything else goes through
// iterencode and does.
func TestToJSONStringIndent(t *testing.T) {
	env := New()
	for _, tc := range []struct{ src, want string }{
		// A string is the unit, repeated once per level.
		{`{{ [1,2]|tojson("  ") }}`, "[\n  1,\n  2\n]"},
		{`{{ [1,2]|tojson("x") }}`, "[\nx1,\nx2\n]"},
		{`{{ [1,2]|tojson("ab") }}`, "[\nab1,\nab2\n]"},
		{`{{ [1,2]|tojson("") }}`, "[\n1,\n2\n]"},
		// Nesting repeats it, so the unit shows per level.
		{`{{ [[1]]|tojson("x") }}`, "[\nx[\nxx1\nx]\n]"},
		{`{{ {"a":1,"b":{"c":2}}|tojson("x") }}`,
			"{\nx\"a\": 1,\nx\"b\": {\nxx\"c\": 2\nx}\n}"},
		// An integer is still that many spaces, and a bool is an int.
		{`{{ [1,2]|tojson(2) }}`, "[\n  1,\n  2\n]"},
		{`{{ [1,2]|tojson(true) }}`, "[\n 1,\n 2\n]"},
		// A string value never builds the indent, so a bad one is not
		// looked at.
		{`{{ "s"|tojson(1.5) }}|{{ "s"|tojson([1]) }}`, `"s"|"s"`},
	} {
		tmpl, err := env.FromString(tc.src)
		if err != nil {
			t.Fatalf("compile %q: %v", tc.src, err)
		}
		got, err := tmpl.RenderString(context.Background(), nil)
		if err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s\n got %q\nwant %q", tc.src, got, tc.want)
		}
	}

	// Anything that is neither a string nor an integer fails where the unit
	// would have been multiplied -- including for an empty container, and
	// for a bare number.
	for _, src := range []string{
		`{{ [1,2]|tojson(1.5) }}`,
		`{{ []|tojson(1.5) }}`,
		`{{ {}|tojson([1]) }}`,
		`{{ 1|tojson(1.5) }}`,
		`{{ none|tojson([1]) }}`,
	} {
		tmpl, err := env.FromString(src)
		if err != nil {
			t.Fatalf("compile %q: %v", src, err)
		}
		_, err = tmpl.RenderString(context.Background(), nil)
		if err == nil || !strings.Contains(err.Error(), "can't multiply sequence by non-int") {
			t.Errorf("%s: got %v, want the sequence-repetition error", src, err)
		}
	}
}

// An indent too wide for an index is an OverflowError, not a differently
// indented document.
//
// resolve() narrowed the argument with `n, _ := j.raw.Int64()` and dropped the
// ok. An integer past int64 narrowed to zero, so tojson silently produced the
// *broken-up* form with no leading spaces -- a different document from the one
// the template asked for, and no error to say so. A wrong answer is worse than
// a wrong error, and this was the only place in the engine still doing it.
//
// The fix is not another size check. The argument means `" " * indent`, which
// is what json.dumps does, so the multiplication does it: an index-sized
// overflow, a non-int, the charge for the width and the empty result for a
// negative all come from the one operator rather than from a reimplementation
// of it beside it.
func TestToJSONIndentOverflows(t *testing.T) {
	const indexOverflow = "cannot fit 'int' into an index-sized integer"
	for _, tc := range []struct{ src, want string }{
		{`{{ [1,2]|tojson(indent=1180591620717411303424) }}`, indexOverflow},
		{`{{ [1,2]|tojson(indent=9223372036854775808) }}`, indexOverflow},
		{`{{ 1|tojson(indent=9223372036854775808) }}`, indexOverflow},
		// CPython asks for the index before it looks at the sign, so a
		// negative too wide is the same error and not the empty indent
		// the sign alone would have given.
		{`{{ [1,2]|tojson(indent=-1180591620717411303424) }}`, indexOverflow},
		{`{{ 1|tojson(indent=-1180591620717411303424) }}`, indexOverflow},
	} {
		tmpl, err := New().FromString(tc.src)
		if err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		got, err := tmpl.RenderString(context.Background(), nil)
		if err == nil {
			t.Errorf("%s: rendered %q, want %q", tc.src, got, tc.want)
			continue
		}
		if err.Error() != tc.want {
			t.Errorf("%s:\n  got  %q\n  want %q", tc.src, err.Error(), tc.want)
		}
	}
}

// Everything the indent already did keeps doing it, including the two shapes
// that never resolve the argument at all.
func TestToJSONIndentUnchangedElsewhere(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{`{{ [1,2]|tojson(indent=0) }}`, "[\n1,\n2\n]"},
		{`{{ [1,2]|tojson(indent=2) }}`, "[\n  1,\n  2\n]"},
		{`{{ [1,2]|tojson(indent=-1) }}`, "[\n1,\n2\n]"},
		{`{{ [1,2]|tojson(indent=none) }}`, "[1, 2]"},
		{`{{ [1,2]|tojson(indent=true) }}`, "[\n 1,\n 2\n]"},
		{`{{ [1,2]|tojson(indent=false) }}`, "[\n1,\n2\n]"},
		{`{{ [1,2]|tojson(indent="\t") }}`, "[\n\t1,\n\t2\n]"},
		// A string receiver is encoded before any indentation is built,
		// so the argument is never looked at -- however hostile it is.
		{`{{ "s"|tojson(indent=1180591620717411303424) }}`, `"s"`},
		{`{{ "s"|tojson(indent=1.5) }}`, `"s"`},
	} {
		tmpl, err := New().FromString(tc.src)
		if err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		got, err := tmpl.RenderString(context.Background(), nil)
		if err != nil || got != tc.want {
			t.Errorf("%s = %q, %v; want %q", tc.src, got, err, tc.want)
		}
	}
	// A non-integer is reported by the multiplication, as json.dumps does.
	for _, tc := range []struct{ src, want string }{
		{`{{ [1,2]|tojson(indent=1.5) }}`, "can't multiply sequence by non-int of type 'float'"},
		{`{{ 1|tojson(indent=[1]) }}`, "can't multiply sequence by non-int of type 'list'"},
	} {
		tmpl, _ := New().FromString(tc.src)
		if _, err := tmpl.RenderString(context.Background(), nil); err == nil || err.Error() != tc.want {
			t.Errorf("%s: got %v, want %q", tc.src, err, tc.want)
		}
	}
}
