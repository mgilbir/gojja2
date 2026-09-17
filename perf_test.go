// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/gojja2/value"
)

// TestURLEncodeIsLinear pins that percent-encoding does not blow up on the
// bytes below 0x10, which include \n and \t and so occur in ordinary text.
//
// It used to rebuild the whole accumulated buffer twice per such byte, to patch
// in a leading zero. That is quadratic: 40,000 newlines took 0.256s where the
// same length of text one byte higher took 0.001s, and a megabyte would have
// taken minutes.
//
// The assertion is a deadline rather than a ratio, because a ratio is flaky on
// a loaded machine and the margin here is four orders of magnitude: linear
// finishes in milliseconds, quadratic cannot finish in ten seconds.
func TestURLEncodeIsLinear(t *testing.T) {
	const n = 1_000_000
	env := New(WithoutLimits())
	tmpl, err := env.FromString(`{{ text|urlencode|length }}`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	start := time.Now()
	out, err := tmpl.RenderString(ctx, map[string]any{"text": strings.Repeat("\n", n)})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("urlencode of %d newlines did not finish in 10s (%v): %v", n, elapsed, err)
	}
	if out != "3000000" {
		t.Errorf("got %q, want %q", out, "3000000")
	}
	t.Logf("%d newlines encoded in %v", n, elapsed)
}

// TestURLEncodeMatchesCPythonOnLowBytes pins the output itself, since the fast
// path rewrote how the hex digits are produced.
func TestURLEncodeMatchesCPythonOnLowBytes(t *testing.T) {
	cases := []struct{ in, want string }{
		{"\n", "%0A"},
		{"\t", "%09"},
		{"\x00", "%00"},
		{"\x0f", "%0F"},
		{"\x10", "%10"},
		{"\u00ff", "%C3%BF"}, // a character, encoded as UTF-8, as Python does
		{"\xff", "%FF"},      // a raw byte, which stays one byte
		{"a b", "a%20b"},
		{"a/b", "a/b"},
		{"~_.-", "~_.-"},
		{"\n\t\n", "%0A%09%0A"},
	}
	env := New()
	tmpl, err := env.FromString(`{{ text|urlencode }}`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	for _, tc := range cases {
		got, err := tmpl.RenderString(context.Background(), map[string]any{"text": tc.in})
		if err != nil {
			t.Errorf("%q: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("urlencode(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestCancellationInterruptsAFilter pins that a filter doing sustained work is
// interruptible.
//
// The context is only consulted from inside the budget, which a filter that
// neither iterates a sequence nor writes output never reaches. One such call
// overran a one-second deadline by seventeen seconds, and the error it
// eventually returned was the output bound rather than the deadline. State.Poll
// is the yield point; this pins that a filter using it stops.
func TestCancellationInterruptsAFilter(t *testing.T) {
	env := New(WithoutLimits())
	started := make(chan struct{})
	env.AddFilter("spin", func(s *State, v value.Value, _ *value.CallArgs) (value.Value, error) {
		close(started)
		for i := 0; ; i++ {
			if err := s.Poll(); err != nil {
				return value.Undefined, err
			}
			if i > 1_000_000_000 {
				return value.String("never"), nil
			}
		}
	})
	tmpl, err := env.FromString(`{% for i in [1] %}{{ i|spin }}{% endfor %}`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-started
		cancel()
	}()

	done := make(chan error, 1)
	start := time.Now()
	go func() {
		_, rerr := tmpl.RenderString(ctx, nil)
		done <- rerr
	}()

	select {
	case rerr := <-done:
		if rerr == nil {
			t.Fatal("expected the cancelled render to fail")
		}
		if !errors.Is(rerr, context.Canceled) {
			t.Errorf("got %v, want it to wrap context.Canceled", rerr)
		}
		t.Logf("cancelled filter returned after %v", time.Since(start))
	case <-time.After(15 * time.Second):
		t.Fatal("a cancelled render did not stop within 15s")
	}
}

// TestPollIsSafeWithoutARender pins that the yield point is usable from
// constant folding, which has a budget but no context, and from a nil State.
func TestPollIsSafeWithoutARender(t *testing.T) {
	var nilState *State
	if err := nilState.Poll(); err != nil {
		t.Errorf("Poll on a nil State: %v", err)
	}
	env := New()
	env.AddFilter("polls", func(s *State, v value.Value, _ *value.CallArgs) (value.Value, error) {
		for range 10000 {
			if err := s.Poll(); err != nil {
				return value.Undefined, err
			}
		}
		return v, nil
	})
	// All-constant, so this runs at compile time.
	if _, err := env.FromString(`{{ "x"|polls }}`); err != nil {
		t.Errorf("compile: %v", err)
	}
}

// TestTupleHashIsLinear pins that hashing a nested tuple costs time in the
// number of nodes rather than in the square of the nesting.
//
// A tuple's key has to distinguish it from every other tuple, and the first
// way to get that -- fold each subtree into its own key and concatenate those
// -- builds n prefixes of length O(n). It was not a theoretical cost: a
// 400,000-deep tuple, which a template can build inside the default iteration
// budget, took two minutes and nineteen seconds to hash, and nothing could
// interrupt it. Writing the tree out once is exactly as discriminating and
// costs O(nodes).
//
// The assertion is a deadline rather than a ratio, because a ratio is flaky on
// a loaded machine and the margin here is three orders of magnitude: linear
// finishes in well under a second, quadratic cannot finish in thirty.
func TestTupleHashIsLinear(t *testing.T) {
	const n = 400_000
	var src strings.Builder
	src.WriteString(`{% set ns = namespace(t=(0,)) %}`)
	fmt.Fprintf(&src, `{%% for i in range(%d) %%}{%% set ns.t = (ns.t,) %%}{%% endfor %%}`, n)
	src.WriteString(`{{ {ns.t: "v"}[ns.t] }}`)

	env := New()
	tmpl, err := env.FromString(src.String())
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	start := time.Now()
	out, err := tmpl.RenderString(ctx, nil)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("hashing a %d-deep tuple did not finish in 30s (%v): %v", n, elapsed, err)
	}
	if out != "v" {
		t.Errorf("got %q, want %q: the key did not round-trip", out, "v")
	}
	t.Logf("%d-deep tuple hashed and looked up in %v", n, elapsed)
}

// TestLexingIsLinearInTheSource pins that compiling a template costs time in
// its length, not in its length times its number of tags.
//
// The lexer asks where each opening delimiter appears next, once per tag. A
// delimiter the template never uses -- and most templates use one of the three
// -- was searched for afresh every time, which means scanning everything left
// in the source on every tag:
//
//	800 KB of `{{1}}`                       8.7s
//	1.6 MB of `{{1}}`                      34.5s
//	5.0 MB with all three delimiters        0.5s
//
// Compilation takes no context and has no budget, so nothing bounded it.
//
// The assertion is a deadline rather than a ratio, because a ratio is flaky on
// a loaded machine and the margin is two orders of magnitude: linear compiles
// this in a third of a second, quadratic takes the better part of a minute.
func TestLexingIsLinearInTheSource(t *testing.T) {
	const tags = 400_000
	src := strings.Repeat("{{1}}", tags)

	env := New()
	start := time.Now()
	_, err := env.FromString(src)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if elapsed > 10*time.Second {
		t.Fatalf("compiling %d bytes of print tags took %v", len(src), elapsed)
	}
	t.Logf("%d tags (%d bytes) compiled in %v", tags, len(src), elapsed)
}

// TestRangeMembershipIsConstantTime pins that `x in range(...)` is arithmetic
// rather than a search, as it is in Python.
//
// Falling through to the generic scan made membership cost the length of the
// range. `{{ -1 in range(9223372036854775807) }}` walked toward nine quintillion
// elements, consulting neither the budget nor the context, and a three-second
// deadline was still running ninety seconds later.
func TestRangeMembershipIsConstantTime(t *testing.T) {
	env := New()
	for _, tc := range []struct {
		src  string
		want string
	}{
		{`{{ -1 in range(9223372036854775807) }}`, "False"},
		{`{{ 9223372036854775806 in range(9223372036854775807) }}`, "True"},
		{`{{ 4611686018427387904 in range(0, 9223372036854775807, 2) }}`, "True"},
		{`{{ 4611686018427387903 in range(0, 9223372036854775807, 2) }}`, "False"},
		{`{{ "x" in range(9223372036854775807) }}`, "False"},
		{`{{ 1.5 in range(9223372036854775807) }}`, "False"},
	} {
		tmpl, err := env.FromString(tc.src)
		if err != nil {
			t.Fatalf("compile: %v", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		start := time.Now()
		got, err := tmpl.RenderString(ctx, nil)
		cancel()
		if err != nil {
			t.Fatalf("%s did not finish in 10s (%v): %v", tc.src, time.Since(start), err)
		}
		if got != tc.want {
			t.Errorf("%s = %q, want %q", tc.src, got, tc.want)
		}
	}
}

// TestUniqueIsLinear pins that |unique costs the length of its input rather
// than its square.
//
// jinja2 tracks what it has seen in a set. Scanning the keys seen so far
// instead is quadratic, and it consulted neither the budget nor the context
// between comparisons: 60,000 distinct items under a three-second deadline
// were still being compared ninety seconds later.
func TestUniqueIsLinear(t *testing.T) {
	const n = 200_000
	env := New(WithoutLimits())
	tmpl, err := env.FromString(`{{ range(N)|list|unique|length }}`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	start := time.Now()
	out, err := tmpl.RenderString(ctx, map[string]any{"N": n})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("unique over %d items did not finish in 30s (%v): %v", n, elapsed, err)
	}
	if want := "200000"; out != want {
		t.Errorf("got %q, want %q", out, want)
	}
	t.Logf("%d distinct items deduplicated in %v", n, elapsed)
}
