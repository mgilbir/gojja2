// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/mgilbir/gojja2"
)

func includeEnv(extra ...string) *gojja2.Environment {
	sources := map[string]string{"inc": "|", "mod": `{% macro m() %}m{% endmacro %}`}
	for i := 0; i+1 < len(extra); i += 2 {
		sources[extra[i]] = extra[i+1]
	}
	return gojja2.New(gojja2.WithLoader(gojja2.DictLoader(sources)))
}

// Handing the context to an include must not undo what the template did to it.
//
// A render argument is converted from Go on first reference and memoised, so
// that the second reference is the same value as the first -- which is what
// makes a mutation stick. Handing the whole context somewhere realised every
// name again from the caller's original, replacing each memo with a fresh
// value, and every change made before the include went with it.
//
// `{{ l.append(99) }}{% include "inc" %}{{ l|length }}` printed 3. CPython
// prints 4. `without context` printed 4 all along, because it never flattens --
// which is what pointed at the flattening.
func TestIncludeKeepsMutationsToTheContext(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{`{{ l.append(99) }}{% include "inc" %}{{ l|length }}`, "None|4"},
		{`{{ l.append(99) }}{% include "inc" without context %}{{ l|length }}`, "None|4"},
		{`{% set _ = d.update({"z": 1}) %}{% include "inc" %}{{ d|length }}`, "|2"},
		{`{{ l.append(9) }}{% include "inc" %}{{ l.append(8) }}{% include "inc" %}{{ l|length }}`,
			"None|None|5"},
		// An import takes the context the same way.
		{`{{ l.append(99) }}{% import "mod" as m %}{{ l|length }}`, "None4"},
		{`{{ l.append(99) }}{% from "mod" import m %}{{ l|length }}`, "None4"},
		// And the mutation is visible *inside* the include, not only after.
		{`{{ l.append(99) }}{% include "seel" %}`, "None4"},
	} {
		env := includeEnv("seel", `{{ l|length }}`)
		tmpl, err := env.FromString(tc.src)
		if err != nil {
			t.Errorf("%s: compile: %v", tc.src, err)
			continue
		}
		got, err := tmpl.RenderString(context.Background(), map[string]any{
			"l": []any{1, 2, 3},
			"d": map[string]any{"a": 1},
		})
		if err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s\n  = %q\n want %q (CPython's answer)", tc.src, got, tc.want)
		}
	}
}

// An include costs what it renders, not what the context happens to hold.
//
// The context goes with an include by default, and realising it converted every
// render argument afresh each time. A loop with an include inside therefore did
// that once per iteration: the product is quadratic in a pattern nobody would
// think twice about writing, and 40,000 iterations over a 40,000-element
// argument took fifty-three seconds to print 40,000 characters.
//
// Measured against the same loop with no argument to convert, so the comparison
// is machine-independent: the include does the same work either way, and only
// the conversion could make one slower than the other.
func TestIncludingInALoopDoesNotReconvertTheContext(t *testing.T) {
	render := func(ctxN int) time.Duration {
		t.Helper()
		big := make([]any, ctxN)
		for i := range big {
			big[i] = i
		}
		tmpl, err := includeEnv().FromString(
			`{% for i in range(2000) %}{% include "inc" %}{% endfor %}`)
		if err != nil {
			t.Fatalf("compile: %v", err)
		}
		vars := map[string]any{"big": big}
		best := time.Duration(1<<63 - 1)
		for range 3 {
			start := time.Now()
			if err := tmpl.Render(context.Background(), io.Discard, vars); err != nil {
				t.Fatalf("render: %v", err)
			}
			best = min(best, time.Since(start))
		}
		return best
	}

	bare, loaded := render(0), render(8000)
	if bare < 500*time.Microsecond {
		t.Skipf("2,000 includes take %s here, too fast to compare against", bare)
	}
	// Four times the bare loop. Converting the argument once per include
	// measured fifty-five times it; converting it once measures about one.
	if ratio := float64(loaded) / float64(bare); ratio > 4 {
		t.Errorf("2,000 includes took %s with an 8,000-entry argument in the "+
			"context and %s with none, a factor of %.1f: the context is "+
			"being converted again for every include", loaded, bare, ratio)
	}
}

// The memo is what makes an argument convert once, so a template that mentions
// a name many times, with an include between, converts it once.
func TestContextIsConvertedOnce(t *testing.T) {
	const n = 40_000
	big := make([]any, n)
	for i := range big {
		big[i] = i
	}
	// Converting is charged per element, so a budget just over one
	// conversion admits one and refuses two.
	env := gojja2.New(
		gojja2.WithMaxIterations(n+n/2),
		gojja2.WithLoader(gojja2.DictLoader(map[string]string{"inc": "|"})),
	)
	tmpl, err := env.FromString(`{{ big|length }}{% include "inc" %}{{ big|length }}`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	got, err := tmpl.RenderString(context.Background(), map[string]any{"big": big})
	if err != nil {
		t.Fatalf("the argument was converted more than once: %v", err)
	}
	if want := fmt.Sprintf("%d|%d", n, n); got != want {
		t.Errorf("= %q, want %q", got, want)
	}
}

// Each render sees the caller's data as it was handed over, and leaves it that
// way.
//
// The memo that makes a mutation stick lives in the render's own scope, and raw
// -- the caller's map -- is never written to. Both halves matter and they pull
// against each other: a mutation has to survive an include within one render,
// and must not survive into the next one or reach the Go value behind it. The
// obvious way to make the first work is to write the converted value back where
// it came from, and that breaks the second silently.
func TestRendersDoNotLeakIntoEachOther(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{`{{ l.append(99) }}{{ l|length }}`, "None4"},
		{`{{ l.append(99) }}{% include "inc" %}{{ l|length }}`, "None|4"},
		{`{% set _ = d.update({"z": 1}) %}{{ d|length }}`, "2"},
		{`{% set _ = d.pop("a") %}{{ d|length }}`, "0"},
		{`{{ l.sort() }}{{ l|join(",") }}`, "None1,2,3"},
	} {
		tmpl, err := includeEnv().FromString(tc.src)
		if err != nil {
			t.Errorf("%s: compile: %v", tc.src, err)
			continue
		}
		// One vars map, three renders, one Go slice and one Go map
		// behind them.
		goList := []any{3, 1, 2}
		goDict := map[string]any{"a": 1}
		vars := map[string]any{"l": goList, "d": goDict}
		for i := range 3 {
			got, err := tmpl.RenderString(context.Background(), vars)
			if err != nil {
				t.Errorf("%s: render %d: %v", tc.src, i+1, err)
				break
			}
			if got != tc.want {
				t.Errorf("%s: render %d = %q, want %q -- a previous render "+
					"left something behind", tc.src, i+1, got, tc.want)
				break
			}
		}
		// And the caller's own values are as they were.
		if fmt.Sprint(goList) != "[3 1 2]" {
			t.Errorf("%s: the caller's slice became %v", tc.src, goList)
		}
		if fmt.Sprint(goDict) != "map[a:1]" {
			t.Errorf("%s: the caller's map became %v", tc.src, goDict)
		}
	}
}
