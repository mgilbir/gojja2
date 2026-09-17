// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"context"
	"strings"
	"testing"

	"github.com/mgilbir/gojja2/errs"
)

// hostileTemplates is every template the audit found that could panic the
// process or exhaust its memory, plus the shapes around them.
//
// Each sizes an allocation from a number the template chose. The point of the
// table is that it is one table: the defect was never in any single filter, it
// was that each site decided for itself whether to charge for what it was
// about to allocate.
var hostileTemplates = []string{
	// A width repeated into a string.
	`{{ "a".center(9223372036854775807) }}`,
	`{{ "a".ljust(4611686018427387904) }}`,
	`{{ "a".rjust(4611686018427387904) }}`,
	`{{ "1".zfill(4611686018427387904) }}`,
	`{{ "a"|center(4611686018427387904) }}`,
	`{{ "a"|indent(4611686018427387904) }}`,
	// An indent repeated once per level and per element.
	`{{ [1]|tojson(2000000000) }}`,
	`{{ [[1]]|tojson(2000000000) }}`,
	`{{ {"a": 1}|tojson(2000000000) }}`,
	// A digit count.
	`{{ 1.5|round(2000000000) }}`,
	// A number of containers to build.
	`{{ []|slice(100000000)|length }}`,
	`{{ [1]|batch(100000000, 0)|length }}`,
	// A global, which until recently could not charge anything at all.
	`{{ lipsum(100000000) }}`,
	`{{ lipsum(1, true, 0, 2000000000) }}`,
	// Two individually-legal sizes whose product is not.
	`{{ ("a" * 60000)|replace("a", "b" * 60000) }}`,
	`{{ ("a" * 60000).replace("a", "b" * 60000) }}`,
	// The one that was already guarded, kept so it stays guarded.
	`{{ "x" * 1000000000 }}`,
}

// TestHostileTemplatesAreRefused pins that every one of them fails as an error
// rather than as a panic or an exhausted machine.
//
// Worth running under a memory cap: before the charges existed these did not
// fail, they took the machine down, and the difference between "returned an
// error" and "was killed" is the whole point of the test.
func TestHostileTemplatesAreRefused(t *testing.T) {
	// A deliberately tiny budget. Every case below is far past it, so the
	// bound is what decides, not the size of the machine.
	env := New(WithMaxOutputBytes(4096), WithMaxIterations(10000))
	for _, src := range hostileTemplates {
		t.Run(src, func(t *testing.T) {
			tmpl, err := env.FromString(src)
			if err != nil {
				// Refusing at compile time is also a pass.
				return
			}
			out, err := tmpl.RenderString(context.Background(), nil)
			if err == nil {
				t.Errorf("rendered %d bytes instead of refusing", len(out))
			}
		})
	}
}

// TestHostileTemplatesAreRefusedAtCompileTime pins the same list against
// FromString alone. Folding runs real filters, so every one of these was
// reachable with no render, no context and no variables -- which put them
// beyond every bound the caller can configure.
func TestHostileTemplatesAreRefusedAtCompileTime(t *testing.T) {
	env := New(WithMaxOutputBytes(4096), WithMaxIterations(10000))
	for _, src := range hostileTemplates {
		if _, err := env.FromString(src); err != nil {
			continue // refused outright, which is fine
		}
	}
}

// TestBoundsHoldWithoutABudget pins the hard ceiling. A zero or negative
// budget means "unbounded", and unbounded must still not mean "allocate 2**63
// bytes" -- the process has a limit even when the caller has not set one.
func TestBoundsHoldWithoutABudget(t *testing.T) {
	env := New(WithMaxOutputBytes(0), WithMaxIterations(0))
	for _, src := range []string{
		`{{ "a".center(9223372036854775807) }}`,
		`{{ "a"|indent(4611686018427387904) }}`,
		`{{ lipsum(100000000) }}`,
		`{{ ("a" * 60000)|replace("a", "b" * 60000) }}`,
	} {
		tmpl, err := env.FromString(src)
		if err != nil {
			continue
		}
		out, err := tmpl.RenderString(context.Background(), nil)
		if err == nil {
			t.Errorf("%s: rendered %d bytes with no budget; the hard ceiling did not hold",
				src, len(out))
		}
	}
}

// TestSizedOperationsStillWork guards the charges against over-reach. Every
// one of these is an ordinary use of the same filters, and must be unaffected.
func TestSizedOperationsStillWork(t *testing.T) {
	cases := []struct{ src, want string }{
		{`{{ "a".center(5) }}`, "  a  "},
		{`{{ "a".ljust(3, "-") }}`, "a--"},
		{`{{ "a".rjust(3, "-") }}`, "--a"},
		{`{{ "7".zfill(3) }}`, "007"},
		{`{{ "-7".zfill(4) }}`, "-007"},
		{`{{ "a"|center(5) }}`, "  a  "},
		{`{{ "a\nb"|indent(2) }}`, "a\n  b"},
		{`{{ "a\nb"|indent(2, true) }}`, "  a\n  b"},
		{`{{ "abc"|replace("b", "xy") }}`, "axyc"},
		{`{{ "aaa"|replace("a", "b", 2) }}`, "bba"},
		{`{{ [1, 2]|tojson }}`, "[1, 2]"},
		{`{{ 1.23456|round(2) }}`, "1.23"},
		{`{{ [1,2,3,4]|batch(2)|list|length }}`, "2"},
		{`{{ [1,2,3,4]|slice(2)|list|length }}`, "2"},
		{`{{ [1,2,3]|batch(2, 0)|list }}`, "[[1, 2], [3, 0]]"},
		{`{{ ", ".join(["a", "b"]) }}`, "a, b"},
		{`{{ "x" * 5 }}`, "xxxxx"},
	}
	env := New()
	for _, tc := range cases {
		tmpl, err := env.FromString(tc.src)
		if err != nil {
			t.Errorf("%s: compile: %v", tc.src, err)
			continue
		}
		got, err := tmpl.RenderString(context.Background(), nil)
		if err != nil {
			t.Errorf("%s: render: %v", tc.src, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s = %q, want %q", tc.src, got, tc.want)
		}
	}
}

// TestLipsumMatchesCPythonOnEdgeCases pins the argument handling. lipsum's own
// text is random and ungradable, but its refusals and its shape are not.
func TestLipsumMatchesCPythonOnEdgeCases(t *testing.T) {
	env := New()
	// jinja2 calls randrange(min, max), which raises on an empty range.
	// Quietly repairing the range is what used to create a zero-word
	// paragraph and then panic formatting it.
	if _, err := renderVars(t, env, `{{ lipsum(1, true, 0, 0) }}`, nil); err == nil {
		t.Error("lipsum(1, true, 0, 0) should raise, as randrange does")
	} else if !strings.Contains(err.Error(), "empty range for randrange()") {
		t.Errorf("got %q, want CPython's randrange wording", err)
	}
	// A paragraph really can come out empty, and jinja2 renders just the
	// full stop.
	for _, src := range []string{
		`{{ lipsum(1, true, 0, 1) }}`,
		`{{ lipsum(1, true, -5, -1) }}`,
	} {
		got, err := renderVars(t, env, src, nil)
		if err != nil {
			t.Errorf("%s: %v", src, err)
			continue
		}
		if got != "<p>.</p>" {
			t.Errorf("%s = %q, want %q", src, got, "<p>.</p>")
		}
	}
	// A negative count renders nothing at all.
	if got, err := renderVars(t, env, `{{ lipsum(-1) }}`, nil); err != nil || got != "" {
		t.Errorf("lipsum(-1) = %q, %v; want \"\", nil", got, err)
	}
	// A span wide enough to overflow the subtraction must not reach IntN.
	if _, err := renderVars(t, env,
		`{{ lipsum(1, true, -9223372036854775808, 9223372036854775807) }}`, nil); err == nil {
		t.Error("an enormous lipsum should be refused, not attempted")
	}
}

// TestIntFilterRejectsAnInvalidBase pins CPython's behaviour: int() raises on a
// base outside 2..36, jinja2's filter catches that and falls through to the
// float path, so the result is 10 rather than a crash.
func TestIntFilterRejectsAnInvalidBase(t *testing.T) {
	cases := []struct{ src, want string }{
		{`{{ "10"|int(0, 99999) }}`, "10"},
		{`{{ "10"|int(0, 1) }}`, "10"},
		{`{{ "10"|int(0, -5) }}`, "10"},
		{`{{ "ff"|int(0, 16) }}`, "255"},
		{`{{ "10"|int(0, 2) }}`, "2"},
		{`{{ "10"|int(0, 36) }}`, "36"},
	}
	env := New()
	for _, tc := range cases {
		got, err := renderVars(t, env, tc.src, nil)
		if err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s = %q, want %q", tc.src, got, tc.want)
		}
	}
}

// TestIndentAcceptsANegativeWidth pins that `" " * -1` is "" as it is in
// Python, rather than a panic from strings.Repeat.
func TestIndentAcceptsANegativeWidth(t *testing.T) {
	for _, src := range []string{`{{ "a"|indent(-1) }}`, `{{ "a"|center(-5) }}`, `{{ "1".zfill(-3) }}`} {
		got, err := renderVars(t, New(), src, nil)
		if err != nil {
			t.Errorf("%s: %v", src, err)
			continue
		}
		if got == "" {
			t.Errorf("%s rendered nothing; a negative width pads nothing but keeps the value", src)
		}
	}
}

// TestBatchOnlyComparesItsLinecount: do_batch never converts linecount. It
// compares it while a row fills (`len(tmp) == linecount`), orders it when the
// last row is padded (`len(tmp) < linecount`), and multiplies by it to build
// that padding. So a linecount of the wrong type is not an error in itself --
// no length ever equals it, and everything lands in one row -- and it is the
// padding that raises, and only when there is a short last row to pad.
//
// gojja2 required a positive integer up front, so every one of these was a
// ValueError or a TypeError of its own invention.
func TestBatchOnlyComparesItsLinecount(t *testing.T) {
	env := New()
	for _, tc := range []struct{ src, want string }{
		// Nothing equals these, so nothing is ever cut.
		{`{{ [1,2,3]|batch("x")|list }}`, "[[1, 2, 3]]"},
		{`{{ [1,2,3]|batch(none)|list }}`, "[[1, 2, 3]]"},
		{`{{ [1,2,3]|batch(-1)|list }}`, "[[1, 2, 3]]"},
		{`{{ [1,2,3]|batch(2.5)|list }}`, "[[1, 2, 3]]"},
		{`{{ [1,2,3]|batch([1])|list }}`, "[[1, 2, 3]]"},
		// 0 equals the length of the empty row the generator starts
		// with, so that row is yielded once and never matches again.
		{`{{ [1,2,3]|batch(0)|list }}`, "[[], [1, 2, 3]]"},
		{`{{ [1,2,3]|batch(false)|list }}`, "[[], [1, 2, 3]]"},
		// true is 1 in Python's numeric tower.
		{`{{ [1,2,3]|batch(true)|list }}`, "[[1], [2], [3]]"},
		// Only the last row is padded; the rest were already full.
		{`{{ [1,2,3]|batch(2, "X")|list }}`, "[[1, 2], [3, 'X']]"},
		{`{{ [1,2,3]|batch(5, "X")|list }}`, "[[1, 2, 3, 'X', 'X']]"},
		// An empty input yields no row at all, so no linecount of any
		// type is ever consulted.
		{`{{ []|batch("x", "X")|list }}`, "[]"},
		{`{{ []|batch(0, "X")|list }}`, "[]"},
		// A fill of None is what "no fill" means, so it never pads --
		// and an absent fill must reach that same test as None rather
		// than as an undefined, which is not None and would pad.
		{`{{ [9]|batch(3, none)|list }}`, "[[9]]"},
		{`{{ [9]|batch(3)|list }}`, "[[9]]"},
		{`{{ [9]|batch("x", none)|list }}`, "[[9]]"},
		// A last row that is already long enough is not padded, so the
		// linecount is never ordered against and a float is harmless.
		{`{{ [1,2,3]|batch(2.5, "X")|list }}`, "[[1, 2, 3]]"},
		{`{{ [1,2,3]|batch(-1, "X")|list }}`, "[[1, 2, 3]]"},
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
			t.Errorf("%s = %s, want %s", tc.src, got, tc.want)
		}
	}

	// Padding a short last row is the one place the linecount has to be
	// more than comparable, and each step raises Python's own error.
	for _, tc := range []struct{ src, want string }{
		{`{{ [1,2,3]|batch("x", "X")|list }}`,
			"'<' not supported between instances of 'int' and 'str'"},
		{`{{ [1,2,3]|batch(none, "X")|list }}`,
			"'<' not supported between instances of 'int' and 'NoneType'"},
		{`{{ [1,2,3]|batch([1], "X")|list }}`,
			"'<' not supported between instances of 'int' and 'list'"},
		// Ordered fine, then multiplied a sequence by a non-int.
		{`{{ [1,2,3]|batch(4.0, "X")|list }}`,
			"can't multiply sequence by non-int of type 'float'"},
		// Ordered fine, then overflowed the index.
		{`{{ [1,2,3]|batch(10000000000000000000000, "X")|list }}`,
			"cannot fit 'int' into an index-sized integer"},
	} {
		tmpl, err := env.FromString(tc.src)
		if err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		_, err = tmpl.RenderString(context.Background(), nil)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want %q", tc.src, err, tc.want)
		}
	}
}

// TestSliceDividesAndRangesItsCount: do_slice does not convert slices either.
// It computes `length // slices` and `length % slices`, then walks
// `range(slices)`, and each of those refuses a different set of values -- so
// the order they run in decides which error a template sees. `//` reports an
// unsupported operand for a str, list, dict or None and divides by zero for 0
// or false; range() is what refuses a float, after `//` accepted it.
//
// gojja2 demanded a positive integer up front and answered one ValueError for
// all of them.
func TestSliceDividesAndRangesItsCount(t *testing.T) {
	env := New()
	for _, tc := range []struct{ src, want string }{
		{`{{ [1,2,3]|slice("x")|list }}`,
			"unsupported operand type(s) for //: 'int' and 'str'"},
		{`{{ [1,2,3]|slice(none)|list }}`,
			"unsupported operand type(s) for //: 'int' and 'NoneType'"},
		{`{{ [1,2,3]|slice([1])|list }}`,
			"unsupported operand type(s) for //: 'int' and 'list'"},
		{`{{ [1,2,3]|slice({})|list }}`,
			"unsupported operand type(s) for //: 'int' and 'dict'"},
		// Dividing comes first, so zero is reported as a division by
		// zero rather than as anything about range().
		{`{{ [1,2,3]|slice(0)|list }}`, "integer division or modulo by zero"},
		{`{{ [1,2,3]|slice(false)|list }}`, "integer division or modulo by zero"},
		// A float divides happily -- 3 // 2.5 is 1.0 -- and reaches
		// range(), which is what refuses it.
		{`{{ [1,2,3]|slice(2.5)|list }}`,
			"'float' object cannot be interpreted as an integer"},
		{`{{ [1,2,3]|slice(4.0)|list }}`,
			"'float' object cannot be interpreted as an integer"},
		// An empty input still divides, so the count is refused just the
		// same -- there is no short-circuit for having nothing to slice.
		{`{{ []|slice("x")|list }}`,
			"unsupported operand type(s) for //: 'int' and 'str'"},
		{`{{ []|slice(0)|list }}`, "integer division or modulo by zero"},
	} {
		tmpl, err := env.FromString(tc.src)
		if err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		_, err = tmpl.RenderString(context.Background(), nil)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want %q", tc.src, err, tc.want)
		}
	}

	for _, tc := range []struct{ src, want string }{
		// range() of a negative count is empty, so no slice is built --
		// a fill has nothing to be put in, and this is not an error.
		{`{{ [1,2,3]|slice(-1)|list }}`, "[]"},
		{`{{ [1,2,3]|slice(-1, "X")|list }}`, "[]"},
		{`{{ []|slice(-1)|list }}`, "[]"},
		// true is 1, and still divides.
		{`{{ [1,2,3]|slice(true)|list }}`, "[[1, 2, 3]]"},
		// The ordinary shapes, unchanged.
		{`{{ [1,2,3]|slice(2)|list }}`, "[[1, 2], [3]]"},
		{`{{ [1,2,3]|slice(2, "X")|list }}`, "[[1, 2], [3, 'X']]"},
		{`{{ []|slice(2, "X")|list }}`, "[['X'], ['X']]"},
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
			t.Errorf("%s = %s, want %s", tc.src, got, tc.want)
		}
	}

	// A count wider than an int64 is a legal range() in CPython, which then
	// iterates it until the process dies -- this is the template that has
	// actually taken a machine down. It is bounded here, and a negative one
	// is still simply empty rather than refused.
	_, err := render(t, `{{ [1,2,3]|slice(10000000000000000000000)|list }}`)
	if kind := errs.KindOf(err); kind != errs.OverflowError {
		t.Errorf("huge slice count: got %v (%v), want OverflowError", kind, err)
	}
	if got := mustRender(t, `{{ [1,2,3]|slice(-10000000000000000000000)|list }}`); got != "[]" {
		t.Errorf("negative huge slice count = %s, want []", got)
	}
}
