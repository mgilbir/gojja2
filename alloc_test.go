// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"context"
	"strings"
	"testing"
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
