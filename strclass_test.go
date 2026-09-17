// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"context"
	"strings"
	"testing"
)

// TestStringMethodsMatchCPython covers the thirteen str methods that were
// missing entirely -- a template calling one got `'str object' has no
// attribute` where CPython answers -- and the two numeric predicates that were
// present but wrong.
//
// isdigit was Nd, which is str.isdecimal. Python has three numeric predicates
// and they are three different sets, so `{{ "²".isdigit() }}` was False where
// CPython says True, and isalnum inherited it.
func TestStringMethodsMatchCPython(t *testing.T) {
	env := New()
	for _, tc := range []struct{ src, want string }{
		// --- the three numeric predicates are three different sets ---
		{`{{ "²".isdecimal() }}|{{ "²".isdigit() }}|{{ "²".isnumeric() }}`, "False|True|True"},
		{`{{ "½".isdecimal() }}|{{ "½".isdigit() }}|{{ "½".isnumeric() }}`, "False|False|True"},
		{`{{ "一".isdecimal() }}|{{ "一".isdigit() }}|{{ "一".isnumeric() }}`, "False|False|True"},
		{`{{ "7".isdecimal() }}|{{ "7".isdigit() }}|{{ "7".isnumeric() }}`, "True|True|True"},
		{`{{ "٧".isdecimal() }}|{{ "٧".isdigit() }}|{{ "٧".isnumeric() }}`, "True|True|True"},
		// isalnum is the union of all four, not letters and Nd.
		{`{{ "²".isalnum() }}|{{ "½".isalnum() }}|{{ "a".isalnum() }}|{{ "-".isalnum() }}`,
			"True|True|True|False"},
		// --- the two that are True for the empty string ---
		{`{{ "".isascii() }}|{{ "".isprintable() }}`, "True|True"},
		{`{{ "".isdigit() }}|{{ "".istitle() }}|{{ "".isidentifier() }}|{{ "".isnumeric() }}`,
			"False|False|False|False"},
		{`{{ "abc".isascii() }}|{{ "é".isascii() }}`, "True|False"},
		// A space is the one separator Python calls printable.
		{`{{ " ".isprintable() }}|{{ "\t".isprintable() }}|{{ "a b".isprintable() }}`,
			"True|False|True"},
		// --- istitle ---
		{`{{ "Hello World".istitle() }}|{{ "Hello world".istitle() }}`, "True|False"},
		{`{{ "HELLO".istitle() }}|{{ "It'S".istitle() }}|{{ "123".istitle() }}`,
			"False|True|False"},
		// --- isidentifier says nothing about keywords ---
		{`{{ "class".isidentifier() }}|{{ "_x".isidentifier() }}|{{ "x1".isidentifier() }}`,
			"True|True|True"},
		{`{{ "1x".isidentifier() }}|{{ "a-b".isidentifier() }}|{{ "a b".isidentifier() }}`,
			"False|False|False"},
		// --- expandtabs: column resets at a line break ---
		{`{{ "a\tb"|e }}`, "a\tb"},
		{`[{{ "a\tb".expandtabs() }}]`, "[a       b]"},
		{`[{{ "a\tb".expandtabs(4) }}]`, "[a   b]"},
		{`[{{ "ab\tcd".expandtabs(4) }}]`, "[ab  cd]"},
		{`[{{ "a\tb\nc\td".expandtabs(4) }}]`, "[a   b\nc   d]"},
		// A tabsize of zero deletes the tab rather than padding to it.
		{`[{{ "a\tb".expandtabs(0) }}]`, "[ab]"},
		// --- partition always answers a 3-tuple ---
		{`{{ "a-b-c".partition("-") }}`, "('a', '-', 'b-c')"},
		{`{{ "a-b-c".rpartition("-") }}`, "('a-b', '-', 'c')"},
		// Where the empties go is the only difference when it is absent.
		{`{{ "abc".partition("-") }}`, "('abc', '', '')"},
		{`{{ "abc".rpartition("-") }}`, "('', '', 'abc')"},
		// --- removeprefix/removesuffix leave a missing affix alone ---
		{`{{ "abc".removeprefix("ab") }}|{{ "abc".removeprefix("zz") }}`, "c|abc"},
		{`{{ "abc".removesuffix("bc") }}|{{ "abc".removesuffix("zz") }}`, "a|abc"},
		{`{{ "abc".removeprefix("") }}`, "abc"},
		// --- maketrans/translate ---
		{`{{ "abc".maketrans("ab", "xy") }}`, "{97: 120, 98: 121}"},
		{`{{ "abc".translate("abc".maketrans("ab", "xy")) }}`, "xyc"},
		{`{{ "abc".translate({97: "X"}) }}`, "Xbc"},
		{`{{ "abc".translate({97: 88}) }}`, "Xbc"},
		// None deletes; a character the table omits is left alone.
		{`{{ "abc".translate({97: none}) }}|{{ "abc".translate({}) }}`, "bc|abc"},
		{`{{ "abc".maketrans({"a": "z"}) }}`, "{97: 'z'}"},
		{`{{ "abc".translate("abc".maketrans("a", "x", "b")) }}`, "xc"},
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

	// partition needs a separator to sit between the halves; removeprefix
	// does not, so an empty one is an error for one and not the other.
	for _, tc := range []struct{ src, want string }{
		{`{{ "abc".partition("") }}`, "empty separator"},
		{`{{ "abc".rpartition("") }}`, "empty separator"},
		{`{{ "abc".maketrans("ab", "x") }}`, "must have equal length"},
		{`{{ "abc".translate({"a": "X"}) }}`, ""},
	} {
		tmpl, err := env.FromString(tc.src)
		if err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		_, err = tmpl.RenderString(context.Background(), nil)
		if tc.want == "" {
			continue // only that it does not panic
		}
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want %q", tc.src, err, tc.want)
		}
	}
}

// TestStringSearchSliceBounds: count, find, rfind, index, rindex, startswith
// and endswith all take an optional start and end, which select the slice they
// look at. gojja2 ignored them outright, so this was not a wording difference
// but a wrong answer:
//
//	{{ "Hello World".find("o", 1, 2) }}   -1  ->  4
//	{{ "Hello World".count("o", 1, 2) }}   0  ->  2
//	{{ "Hello World".startswith("H", 5) }} False -> True
//
// They are slice indices: counted in characters, negative from the end, and
// clamped rather than refused when out of range.
func TestStringSearchSliceBounds(t *testing.T) {
	env := New()
	for _, tc := range []struct{ src, want string }{
		// The window really is a window.
		{`{{ "Hello World".find("o",1,2) }}|{{ "Hello World".count("o",1,2) }}`, `-1|0`},
		{`{{ "Hello World".find("o",5) }}|{{ "Hello World".count("o",5) }}`, `7|1`},
		// A found index is reported against the whole string, not the slice.
		{`{{ "Hello World".find("o") }}|{{ "Hello World".find("o",5,8) }}`, `4|7`},
		{`{{ "Hello World".rfind("o") }}|{{ "Hello World".rfind("o",0,6) }}`, `7|4`},
		// Negative bounds count from the end.
		{`{{ "Hello World".count("o",-3) }}|{{ "Hello World".find("o",-7,-4) }}`, `0|4`},
		// None means "the default end", as in a slice.
		{`{{ "Hello World".count("o",none,none) }}|{{ "Hello World".count("o",1,none) }}`, `2|2`},
		// Out of range clamps, and a reversed window is empty.
		{`{{ "Hello World".count("o",100) }}|{{ "Hello World".count("o",-100) }}`, `0|2`},
		{`{{ "Hello World".count("o",5,1) }}|{{ "Hello World".count("o",0,0) }}`, `0|0`},
		// The affix tests take the same window.
		{`{{ "Hello World".startswith("H") }}|{{ "Hello World".startswith("H",1) }}`, `True|False`},
		{`{{ "Hello World".startswith("e",1) }}|{{ "Hello World".endswith("o",0,5) }}`, `True|True`},
		// Counted in characters, not bytes.
		{`{{ "héllo wörld".find("l",1,4) }}|{{ "héllo wörld".count("l",1) }}`, `2|3`},
		{`{{ "héllo wörld".startswith("h",0,1) }}`, `True`},
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

	// index and rindex raise where find and rfind answer -1, and the bound
	// is what decides it.
	for _, tc := range []struct{ src, want string }{
		{`{{ "Hello".index("o",0,2) }}`, "substring not found"},
		{`{{ "Hello".rindex("o",0,2) }}`, "substring not found"},
		// A bound that is not an integer is refused as a slice index is.
		{`{{ "Hello".count("o","x") }}`,
			"slice indices must be integers or None or have an __index__ method"},
		{`{{ "Hello".find("o",1,"y") }}`,
			"slice indices must be integers or None or have an __index__ method"},
		{`{{ "Hello".startswith("H","x") }}`,
			"slice indices must be integers or None or have an __index__ method"},
	} {
		tmpl, err := env.FromString(tc.src)
		if err != nil {
			t.Fatalf("compile %q: %v", tc.src, err)
		}
		_, err = tmpl.RenderString(context.Background(), nil)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want %q", tc.src, err, tc.want)
		}
	}
}
