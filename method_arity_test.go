// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"context"
	"testing"
)

// TestMethodArity: the filters and tests were bound against CPython's
// signatures; the methods on str, list, dict and tuple never were, and every
// one of them accepted whatever it was given. `[1].append("a", 1)` bound the
// extra argument to nothing and answered None, and a keyword was dropped in
// silence -- some seven hundred calls across the shapes a template can write.
//
// The wordings cannot be derived from a signature: a method that takes nothing,
// one that takes exactly one, and one with an optional second all word it
// differently, and the names in them are sometimes qualified and sometimes not.
// They are probed out of CPython; see tools/oracle/gen_methods.py.
func TestMethodArity(t *testing.T) {
	env := New()
	for _, tc := range []struct{ src, want string }{
		// Takes nothing at all.
		{`{{ "ab".upper(1) }}`, "str.upper() takes no arguments (1 given)"},
		{`{{ "ab".isalpha(1, 2) }}`, "str.isalpha() takes no arguments (2 given)"},
		{`{{ [1].clear(none) }}`, "list.clear() takes no arguments (1 given)"},
		{`{{ {"a":1}.keys(1) }}`, "dict.keys() takes no arguments (1 given)"},
		// Takes exactly one, and says so in both directions.
		{`{{ [1].append() }}`, "list.append() takes exactly one argument (0 given)"},
		{`{{ [1].append("a", 1) }}`, "list.append() takes exactly one argument (2 given)"},
		// Takes exactly two.
		{`{{ [1].insert(0) }}`, "insert expected 2 arguments, got 1"},
		{`{{ [1].insert(0, 1, 2) }}`, "insert expected 2 arguments, got 3"},
		// A range, worded differently at each end.
		{`{{ "ab".center() }}`, "center expected at least 1 argument, got 0"},
		{`{{ "ab".center(1, "x", 2) }}`, "center expected at most 2 arguments, got 3"},
		{`{{ "ab".count() }}`, "count() takes at least 1 argument (0 given)"},
		{`{{ "ab".count("a", 1, 2, 3) }}`, "count() takes at most 3 arguments (4 given)"},
		{`{{ {"a":1}.get() }}`, "get expected at least 1 argument, got 0"},
		{`{{ {"a":1}.get("a", 1, 2) }}`, "get expected at most 2 arguments, got 3"},
		// A keyword the method does not take, named or not.
		{`{{ "ab".upper(zz=1) }}`, "str.upper() takes no keyword arguments"},
		{`{{ [1].append(zz=1) }}`, "list.append() takes no keyword arguments"},
		{`{{ "a,b".split(zz=1) }}`, "'zz' is an invalid keyword argument for split()"},
		{`{{ "ab".encode(zz=1) }}`, "'zz' is an invalid keyword argument for encode()"},
		// The keyword is reported ahead of the count.
		{`{{ "ab".upper(1, zz=1) }}`, "str.upper() takes no keyword arguments"},
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

	for _, tc := range []struct{ src, want string }{
		// The calls that are the right shape still work, keywords the
		// method really takes included.
		{`{{ "ab".upper() }}`, "AB"},
		{`{{ "a,b".split(sep=",") }}`, "['a', 'b']"},
		{`{{ "a,b,c".split(",", maxsplit=1) }}`, "['a', 'b,c']"},
		{`{{ "ab".center(6, "-") }}`, "--ab--"},
		{`{{ {"a":1}.get("a", 2) }}|{{ {"a":1}.get("z", 2) }}`, "1|2"},
		// *args and **kwargs take anything.
		{`{{ "{0}{k}".format(1, k=2) }}`, "12"},
		{`{{ "{0}{1}{2}".format(1, 2, 3) }}`, "123"},
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

// TestMethodNoneIsNotADefault: a method's optional argument has a default for
// being *absent*, not for being None. CPython hands an explicit None to the
// same check every other value goes through, and every one of these refuses it.
//
// gojja2's intArg took None for "use the default" everywhere, so
// `{{ "ab".zfill(none) }}` padded to width 0 and `{{ [1].pop(none) }}` popped
// the last element -- each a plausible answer to a call CPython rejects.
func TestMethodNoneIsNotADefault(t *testing.T) {
	env := New()
	const index = "'NoneType' object cannot be interpreted as an integer"
	for _, tc := range []struct{ src, want string }{
		{`{{ "ab".expandtabs(none) }}`, index},
		{`{{ "a,b".split("a", none) }}`, index},
		{`{{ "a,b".rsplit("a", none) }}`, index},
		{`{{ "ab".replace("a", "b", none) }}`, index},
		{`{{ "ab".zfill(none) }}`, index},
		{`{{ "ab".center(none) }}`, index},
		{`{{ "ab".ljust(none) }}`, index},
		{`{{ "ab".rjust(none) }}`, index},
		{`{{ "ab".splitlines(none) }}`, index},
		{`{{ [1].insert(none, 1) }}`, index},
		{`{{ [1].pop(none) }}`, index},
		// keepends is declared as an integer, so anything that is not
		// one is refused rather than read for its truth.
		{`{{ "ab".splitlines(1.5) }}`, "'float' object cannot be interpreted as an integer"},
		{`{{ "ab".splitlines("x") }}`, "'str' object cannot be interpreted as an integer"},
		// The fill character's check is hand-written in CPython, so it
		// names the type plainly rather than as the parser would.
		{`{{ "ab".center(4, none) }}`,
			"The fill character must be a unicode character, not NoneType"},
		// str.encode's arguments are declared str, and the parser's own
		// message calls the None singleton "None".
		{`{{ "ab".encode(none) }}`, "encode() argument 'encoding' must be str, not None"},
		{`{{ "ab".encode("utf-8", none) }}`, "encode() argument 'errors' must be str, not None"},
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

	// Where None really is the default, it still is: a string search's
	// bounds are declared as optional, and say so in their own message.
	for _, tc := range []struct{ src, want string }{
		{`{{ "ab".count("a", none) }}|{{ "ab".find("a", none) }}`, "1|0"},
		{`{{ "ab".startswith("a", none) }}|{{ "ab".endswith("b", 0, none) }}`, "True|True"},
		{`{{ "ab".splitlines(1) }}|{{ "ab".splitlines(true) }}`, "['ab']|['ab']"},
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

// TestSeqIndexWindow: list.index and tuple.index take a start and a stop and
// search only between them, answering an index into the whole list. gojja2 read
// both and then ignored them, so `[1,2,1].index(1, 1)` answered 0 where CPython
// answers 2, and `[1,2,1].index(1, 1, 2)` answered 0 where CPython raises.
//
// Unlike a string search's, these bounds have no None form: list.index declares
// them as indices outright, so the message has no "or None" in it.
func TestSeqIndexWindow(t *testing.T) {
	env := New()
	for _, tc := range []struct{ src, want string }{
		{`{{ [1,2,1].index(1) }}|{{ [1,2,1].index(1, 1) }}`, "0|2"},
		{`{{ [1,2,1].index(1, 1, 3) }}|{{ [1,2,1].index(1, -1) }}`, "2|2"},
		{`{{ [1,2,1].index(1, 0, -1) }}`, "0"},
		{`{{ (1,2,1).index(1, 1) }}`, "2"},
		// Out of range clamps rather than failing.
		{`{{ [1,2,1].index(1, 0, 2**70) }}`, "0"},
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
	const slice = "slice indices must be integers or have an __index__ method"
	for _, tc := range []struct{ src, want string }{
		{`{{ [1,2,1].index(1, 1, 2) }}`, "1 is not in list"},
		{`{{ [1,2,1].index(1, 2**70) }}`, "1 is not in list"},
		{`{{ (1,2,1).index(1, 1, 2) }}`, "tuple.index(x): x not in tuple"},
		{`{{ [1,2,1].index(1, none) }}`, slice},
		{`{{ [1,2,1].index(1, 1.5) }}`, slice},
		{`{{ [1,2,1].index(1, 0, none) }}`, slice},
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
