// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package conformance_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/gojja2"
	"github.com/mgilbir/gojja2/conformance"
)

// The corpus grades what a template renders. It cannot grade what a template
// *spends*, and that is where every critical defect in this engine has been:
// CPython's answer to `{{ 1.5|round(2000000000) }}` is "1.5" and gojja2's was a
// dead process, which no output comparison can see.
//
// The tests below grade resource behaviour instead, and they need no oracle, so
// they run on a fresh checkout in CI. Two signals make that possible:
//
//   - a panic inside the engine now surfaces as an error wrapping ErrInternal
//     rather than unwinding the caller, so "did this crash?" is a value to
//     assert on; and
//   - a render given a tiny budget must always come back, so "did this finish?"
//     is a deadline to assert on.
//
// Run with a memory cap for the strongest version of the second one.

// tinyBudget is deliberately far below anything a real template needs, so that
// the bound is what decides the outcome rather than the size of the machine.
func tinyBudget() *gojja2.Environment {
	return gojja2.New(
		gojja2.WithMaxOutputBytes(64<<10),
		gojja2.WithMaxIterations(50_000),
		gojja2.WithExtensions("do", "loopcontrols"),
	)
}

// renderBounded compiles and renders src, reporting how it ended.
func renderBounded(env *gojja2.Environment, src string, limit time.Duration) (err error, timedOut bool) {
	done := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	go func() {
		tmpl, cerr := env.FromString(src)
		if cerr != nil {
			done <- cerr
			return
		}
		done <- tmpl.Render(ctx, io.Discard, nil)
	}()
	select {
	case e := <-done:
		return e, false
	case <-time.After(limit + 5*time.Second):
		// The context deadline should already have stopped it; reaching
		// here means the render is in a region that does not consult it.
		return nil, true
	}
}

// TestGeneratedTemplatesStayBounded renders generated templates under a tiny
// budget and requires each to come back, without the engine panicking.
//
// This is the check that would have caught the whole allocate-then-charge class
// on its own: `round`, `tojson`, `slice`, `batch`, `lipsum` and `replace` all
// rendered fine under CPython and took the process down here.
func TestGeneratedTemplatesStayBounded(t *testing.T) {
	env := tinyBudget()
	seed := uint64(20260916)
	rng := rand.New(rand.NewPCG(seed, 0x9e3779b97f4a7c15))

	const count = 3000
	for i := range count {
		input := make([]byte, 1+rng.IntN(96))
		for j := range input {
			input[j] = byte(rng.UintN(256))
		}
		src := conformance.GenerateTemplate(input)
		if strings.TrimSpace(src) == "" {
			continue
		}
		err, timedOut := renderBounded(env, src, 10*time.Second)
		if timedOut {
			t.Fatalf("case %d did not return within its deadline:\n%s", i, src)
		}
		if errors.Is(err, gojja2.ErrInternal) {
			t.Fatalf("case %d panicked inside gojja2: %v\n%s", i, err, src)
		}
	}
}

// sizedConstructs are the places a template hands the engine a number that
// sizes an allocation. The generator reaches the grammar, not these magnitudes,
// so they are enumerated: this is the shape of every critical finding, and the
// list is the one that has to keep passing.
var sizedConstructs = []string{
	`{{ "a".center(%[1]s) }}`,
	`{{ "a".ljust(%[1]s) }}`,
	`{{ "a".rjust(%[1]s) }}`,
	`{{ "1".zfill(%[1]s) }}`,
	`{{ "a"|center(%[1]s) }}`,
	`{{ "a"|indent(%[1]s) }}`,
	`{{ "a"|truncate(%[1]s) }}`,
	`{{ "a b c"|wordwrap(%[1]s) }}`,
	`{{ [1]|tojson(%[1]s) }}`,
	`{{ [[1]]|tojson(%[1]s) }}`,
	`{{ {"a": [1]}|tojson(%[1]s) }}`,
	`{{ 1.5|round(%[1]s) }}`,
	`{{ 1.5|round(%[1]s, "ceil") }}`,
	`{{ []|slice(%[1]s)|length }}`,
	`{{ [1,2]|slice(%[1]s)|length }}`,
	`{{ [1]|batch(%[1]s)|length }}`,
	`{{ [1]|batch(%[1]s, 0)|length }}`,
	`{{ lipsum(%[1]s) }}`,
	`{{ lipsum(1, true, 0, %[1]s) }}`,
	`{{ lipsum(1, true, %[1]s, 0) }}`,
	`{{ "10"|int(0, %[1]s) }}`,
	`{{ "abc"|replace("b", "x" * 100, %[1]s) }}`,
	`{{ "a" * %[1]s }}`,
	`{{ [1] * %[1]s }}`,
	`{{ range(%[1]s)|length }}`,
	`{{ "abcdef"[%[1]s:] }}`,
	`{{ "abc".split("b", %[1]s)|length }}`,
	`{{ [1,2,3].pop(%[1]s) }}`,
	`{{ 10 ** %[1]s }}`,
	`{{ "a"|urlize(%[1]s) }}`,
}

// extremeSizes are the magnitudes that broke things: the boundaries of int64,
// the point where a product overflows, zero, and negatives.
var extremeSizes = []string{
	"9223372036854775807", "9223372036854775806", "4611686018427387904",
	"2147483648", "2147483647", "2000000000", "1000000000", "100000000",
	"65537", "65536", "1", "0", "-1", "-65536", "-2147483648",
	"-9223372036854775807", "-9223372036854775808",
}

// TestSizedConstructsStayBounded is the generative form of the critical
// findings: every construct that takes a size, crossed with every magnitude
// that has ever broken one.
func TestSizedConstructsStayBounded(t *testing.T) {
	env := tinyBudget()
	for _, shape := range sizedConstructs {
		for _, size := range extremeSizes {
			src := fmt.Sprintf(shape, size)
			err, timedOut := renderBounded(env, src, 10*time.Second)
			if timedOut {
				t.Errorf("did not return within its deadline: %s", src)
				continue
			}
			if errors.Is(err, gojja2.ErrInternal) {
				t.Errorf("panicked inside gojja2: %s\n  %v", src, err)
			}
		}
	}
}

// pastTheCeiling are sizes comfortably above the hard allocation ceiling, which
// must be refused whether or not a budget is configured.
//
// Comfortably, rather than by one: a size that lands exactly on the ceiling is
// an allocation the ceiling permits, and a caller who asked for no limits is
// entitled to it. Sizes here are past it even after the off-by-one adjustments
// the individual filters make.
var pastTheCeiling = []string{
	"9223372036854775807", "4611686018427387904", "3000000000",
	"-9223372036854775808", "-2147483648",
}

// TestSizedConstructsRefusedWithoutABudget is the same sweep with the bounds
// removed, which is the configuration where a crash is most likely.
//
// It sweeps only the two ends deliberately. A zero or negative budget means
// "unbounded", and the hard ceiling is all that is left: past it an allocation
// must be refused, and well below it one must succeed. The band in between --
// a few hundred megabytes to two gigabytes -- is an allocation a caller who
// asked for no limits is entitled to, so exercising it would measure the
// machine rather than the engine.
func TestSizedConstructsRefusedWithoutABudget(t *testing.T) {
	env := gojja2.New(gojja2.WithoutLimits())
	for _, shape := range sizedConstructs {
		for _, size := range append(append([]string{}, pastTheCeiling...), "1", "0", "-1") {
			src := fmt.Sprintf(shape, size)
			err, timedOut := renderBounded(env, src, 10*time.Second)
			if timedOut {
				t.Errorf("did not return within its deadline: %s", src)
				continue
			}
			if errors.Is(err, gojja2.ErrInternal) {
				t.Errorf("panicked inside gojja2: %s\n  %v", src, err)
			}
		}
	}
}
