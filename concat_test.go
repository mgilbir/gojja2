// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/gojja2"
)

// `~` is a sized allocation and is charged like one.
//
// It was the only one in the engine that asked nobody. `*` next door has been
// charged all along -- the two sit in the same expression grammar and do the
// same thing to memory -- so this was an oversight rather than a policy, and
// the gap was wide: `{% set x = b ~ b %}` over an eight-megabyte argument
// allocated sixteen with the output bound set to a kilobyte, and the same line
// in a loop allocated until the machine gave up. Nothing was written, so no
// bound in the engine saw any of it.
func TestConcatIsChargedLikeRepetition(t *testing.T) {
	big := strings.Repeat("x", 4<<20)
	vars := map[string]any{"b": big}

	// Neither of these writes more than "ok", so only the charge for the
	// value they build can refuse them.
	for name, src := range map[string]string{
		"concat, result discarded": `{% set x = b ~ b %}ok`,
		"concat in a loop":         `{% for i in range(8) %}{% set x = b ~ b %}{% endfor %}ok`,
		"concat, printed":          `{{ (b ~ b)|length }}`,
		// The comparison that makes the point: repetition was always
		// refused here, and concatenation now agrees with it.
		"repeat, result discarded": `{% set x = b * 2 %}ok`,
	} {
		t.Run(name, func(t *testing.T) {
			env := gojja2.New(
				gojja2.WithMaxOutputBytes(1024),
				gojja2.WithMaxIterations(1000),
			)
			tmpl, err := env.FromString(src)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			out, err := tmpl.RenderString(context.Background(), vars)
			if err == nil {
				t.Fatalf("built %d MiB of value against a 1KiB bound and "+
					"returned %q", len(big)*2>>20, out)
			}
			if !errors.Is(err, gojja2.ErrOutputTooLarge) {
				t.Errorf("error = %v, want ErrOutputTooLarge", err)
			}
		})
	}
}

// Charging it did not make it useless: the sizes a template really writes
// still go through.
func TestConcatStillWorks(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{`{{ "a" ~ "b" ~ 1 }}`, "ab1"},
		{`{{ "a" ~ none }}`, "aNone"},
		{`{{ [1] ~ 2 }}`, "[1]2"},
		{`{% set s = "x" %}{{ s ~ s ~ s }}`, "xxx"},
		{`{{ ("a" ~ "b")|length }}`, "2"},
	} {
		tmpl, err := gojja2.New().FromString(tc.src)
		if err != nil {
			t.Errorf("%s: compile: %v", tc.src, err)
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
}

// Folding a chain of `~` costs the length of the result, not its square.
//
// `~` is one node over a flat list of operands, and the folder walked it
// pairwise -- copying the whole accumulated string once per operand. That is
// quadratic in their number, bounded only by the 64KiB a folded constant may
// reach, which is about two gigabytes of copying. FromString takes no context,
// so nothing could stop it: 64,000 operands took 385 milliseconds to compile
// where the same chain over a name took 31.
//
// The same chain over a *name* is the control. It has the same tokens, the same
// tree and the same number of operands, and folds nothing -- so whatever the
// constant chain costs beyond it is the folding, and the comparison needs no
// threshold in milliseconds. That matters: measured as an absolute ratio across
// two input sizes, this passed at 4.2 on a quiet eight-core machine and failed
// at 8.9 on a two-core builder, which said nothing about the folder and
// everything about the builder. Both halves here slow down together.
func TestFoldingAChainOfConcatsIsLinear(t *testing.T) {
	compile := func(src string) time.Duration {
		t.Helper()
		best := time.Duration(1<<63 - 1)
		for range 5 {
			start := time.Now()
			if _, err := gojja2.New().FromString(src); err != nil {
				t.Fatalf("compile: %v", err)
			}
			best = min(best, time.Since(start))
		}
		return best
	}

	const n = 32_000
	tail := strings.Repeat(" ~ 'a'", n)
	folded := compile("{{ 'a'" + tail + " }}")
	control := compile("{{ x" + tail + " }}")
	if control < 200*time.Microsecond {
		t.Skipf("%d operands parse in %s here, too fast to compare against", n, control)
	}
	// Four times the control. Folding pairwise measured six times it at
	// this size and twelve at twice it; joining in one pass measures about
	// one and a half, because the join is a fraction of the parse.
	if ratio := float64(folded) / float64(control); ratio > 4 {
		t.Errorf("folding %d constant operands took %s against %s to parse the "+
			"same chain over a name, a factor of %.1f: the chain is being "+
			"accumulated pairwise rather than joined", n, folded, control, ratio)
	}
}

// Folding a chain must not build a result it is going to throw away.
//
// A folded constant may reach 64KiB; past that the fold is declined and the
// work moves to render time, where the caller's budget applies. But the chain
// was joined in full before anyone asked how big it had become, so a template
// whose operands are a kilobyte each built megabytes at compile time and then
// discarded them -- inside FromString, which takes no context and cannot be
// stopped.
//
// Charging each operand as it is joined is what stops it at the limit instead
// of at the end. Measured in bytes rather than seconds, because the work is an
// allocation and a fast machine does it just as wastefully.
func TestFoldingAChainStopsAtTheConstantLimit(t *testing.T) {
	// Five thousand operands of a kilobyte each: five megabytes joined, of
	// which at most 64KiB could ever be kept.
	unit := "'" + strings.Repeat("a", 1024) + "'"
	src := "{{ " + unit + strings.Repeat(" ~ "+unit, 5000) + " }}"

	compile := func() {
		if _, err := gojja2.New().FromString(src); err != nil {
			t.Fatalf("compile: %v", err)
		}
	}
	compile() // warm anything lazy

	var before, after runtime.MemStats
	const runs = 3
	runtime.GC()
	runtime.ReadMemStats(&before)
	for range runs {
		compile()
	}
	runtime.ReadMemStats(&after)
	per := (after.TotalAlloc - before.TotalAlloc) / runs

	// The source itself is five megabytes, and parsing it allocates in
	// proportion; what must not happen is joining another five on top. Ten
	// times the constant limit is far above what stopping costs and far
	// below what finishing does.
	if limit := uint64(len(src)) + 10*64<<10; per > limit {
		t.Errorf("compiling a chain whose join is %d bytes allocated %d, "+
			"more than the %d a fold stopped at the constant limit "+
			"would need: the chain is being joined in full before "+
			"its size is looked at", 5000*1024, per, limit)
	}
}
