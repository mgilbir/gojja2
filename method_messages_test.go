// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"context"
	"testing"
)

// TestMethodArgumentMessages: what CPython says about an argument it does not
// like, and when it says it. gojja2 had a wording of its own for most of these,
// and checked several of them in the wrong order.
//
// The wordings are not interchangeable. A string search raises from a helper
// shared by all of them, so its message names neither the method nor the
// parameter; startswith names itself; strip names itself and not the type it
// was given; replace and maketrans number their arguments; and the messages the
// argument parser generates call the None singleton "None" where a hand-written
// check says "NoneType".
func TestMethodArgumentMessages(t *testing.T) {
	env := mustNew()
	for _, tc := range []struct{ src, want string }{
		// The searches and partitions: no method, no parameter.
		{`{{ "ab".count(1) }}`, "count() argument 1 must be str, not int"},
		{`{{ "ab".find(none) }}`, "find() argument 1 must be str, not None"},
		{`{{ "ab".index([]) }}`, "index() argument 1 must be str, not list"},
		{`{{ "ab".rfind(1.5) }}`, "rfind() argument 1 must be str, not float"},
		{`{{ "ab".rindex(true) }}`, "rindex() argument 1 must be str, not bool"},
		{`{{ "ab".partition({}) }}`, "must be str, not dict"},
		{`{{ "ab".rpartition(1) }}`, "must be str, not int"},
		// And the bounds are converted first, while the call is parsed.
		{`{{ "ab".count(1, 1.5) }}`,
			"count() argument 1 must be str, not int"},
		{`{{ "ab".find(1, 1.5) }}`,
			"find() argument 1 must be str, not int"},

		// startswith and endswith name themselves.
		{`{{ "ab".startswith(1) }}`,
			"startswith first arg must be str or a tuple of str, not int"},
		{`{{ "ab".endswith(none) }}`,
			"endswith first arg must be str or a tuple of str, not NoneType"},

		// The strip family names itself and not the type.
		{`{{ "ab".strip(1) }}`, "strip arg must be None or str"},
		{`{{ "ab".lstrip([]) }}`, "lstrip arg must be None or str"},
		{`{{ "ab".rstrip(1.5) }}`, "rstrip arg must be None or str"},

		// removeprefix and removesuffix name themselves, and the
		// parser's message calls None "None".
		{`{{ "ab".removeprefix(none) }}`, "removeprefix() argument must be str, not None"},
		{`{{ "ab".removesuffix(none) }}`, "removesuffix() argument must be str, not None"},
		{`{{ "ab".removesuffix(1) }}`, "removesuffix() argument must be str, not int"},

		// replace numbers its arguments.
		{`{{ "ab".replace(1, none) }}`, "replace() argument 1 must be str, not int"},
		{`{{ "ab".replace("a", 1) }}`, "replace() argument 2 must be str, not int"},
		{`{{ "ab".replace("a", none) }}`, "replace() argument 2 must be str, not None"},

		// maketrans numbers its arguments too -- and converts the
		// second and third before the body looks at the first, so a
		// call with two wrong arguments reports the second.
		{`{{ "ab".maketrans("a", 1) }}`, "maketrans() argument 2 must be str, not int"},
		{`{{ "ab".maketrans(1, none) }}`, "maketrans() argument 2 must be str, not None"},
		{`{{ "ab".maketrans("a", "b", 1) }}`, "maketrans() argument 3 must be str, not int"},
		{`{{ "ab".maketrans(1, "b") }}`,
			"first maketrans argument must be a string if there is a second argument"},

		// join checks by asking for an iterator, so it says nothing
		// about the type.
		{`{{ "-".join(1) }}`, "can only join an iterable"},
		{`{{ "-".join(none) }}`, "can only join an iterable"},

		// encode converts both arguments before it looks the codec up.
		{`{{ "ab".encode("nosuch", 1) }}`, "encode() argument 'errors' must be str, not int"},
		{`{{ "ab".encode("nosuch") }}`, "unknown encoding: nosuch"},
	} {
		tmpl, err := env.FromString(tc.src)
		if err != nil {
			t.Fatalf("compile %q: %v", tc.src, err)
		}
		out, err := tmpl.RenderString(context.Background(), nil)
		if err == nil {
			t.Errorf("%s: rendered %q, want %q", tc.src, out, tc.want)
			continue
		}
		if err.Error() != tc.want {
			t.Errorf("%s\n got %q\nwant %q", tc.src, err.Error(), tc.want)
		}
	}
}

// TestFormatMapAndTranslateAreLazy: neither converts the argument it is handed.
//
// str.translate is `table[ord(c)]` per character, catching LookupError, so an
// empty string never touches the table and anything subscriptable by an integer
// will do -- a str, a list and a tuple all answer for the code points they are
// long enough to index. str.format_map subscripts its mapping once per *named*
// field, so a string with no fields never touches it and one that is not a
// mapping fails as a subscript of that value would.
//
// gojja2 required a dict up front in both, which refused calls CPython answers
// and answered with a complaint CPython never makes.
func TestFormatMapAndTranslateAreLazy(t *testing.T) {
	env := mustNew()
	for _, tc := range []struct{ src, want string }{
		// Nothing to translate, so the table is never consulted.
		{`[{{ ""|string|trim }}{{ "".translate(1) }}]`, "[]"},
		{`[{{ "".translate(none) }}]`, "[]"},
		// A str, a list or a tuple indexes by code point and leaves
		// anything past its end alone.
		{`{{ "a".translate("xyz") }}|{{ "a".translate(["z"]) }}`, "a|a"},
		{`{{ "a".translate({97: "Z"}) }}|{{ "a".translate({"a": "Z"}) }}`, "Z|a"},
		{`{{ "ab".translate({98: "Z"}) }}|{{ "a".translate({97: none}) }}`, "aZ|"},
		// No fields, so format_map never subscripts.
		{`{{ "ab".format_map(1) }}|{{ "ab".format_map(none) }}`, "ab|ab"},
		{`{{ "{a}".format_map({"a": 1}) }}`, "1"},
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

	for _, tc := range []struct{ src, want string }{
		// A table that cannot be subscripted fails on the first
		// character, not before it.
		{`{{ "a".translate(1) }}`, "'int' object is not subscriptable"},
		{`{{ "a".translate(none) }}`, "'NoneType' object is not subscriptable"},
		// format_map has no positional arguments to number.
		{`{{ "{0}".format_map({"a": 1}) }}`, "Format string contains positional fields"},
		{`{{ "{}".format_map({}) }}`, "Format string contains positional fields"},
		// And a named field subscripts whatever it was handed, which
		// is a different complaint for each kind.
		{`{{ "{a}".format_map([1]) }}`, "list indices must be integers or slices, not str"},
		{`{{ "{a}".format_map("x") }}`, "string indices must be integers, not 'str'"},
		{`{{ "x{a}".format_map(none) }}`, "'NoneType' object is not subscriptable"},
		{`{{ "{a}".format_map({}) }}`, "'a'"},
	} {
		tmpl, err := env.FromString(tc.src)
		if err != nil {
			t.Fatalf("compile %q: %v", tc.src, err)
		}
		out, err := tmpl.RenderString(context.Background(), nil)
		if err == nil {
			t.Errorf("%s: rendered %q, want %q", tc.src, out, tc.want)
			continue
		}
		if err.Error() != tc.want {
			t.Errorf("%s\n got %q\nwant %q", tc.src, err.Error(), tc.want)
		}
	}
}
