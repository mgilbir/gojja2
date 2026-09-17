// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"context"
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
