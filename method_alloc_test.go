// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"context"
	"io"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/gojja2"
)

// Asking a question about the ends of a string must not cost the string.
//
// str.startswith, endswith, count, find, rfind, index and rindex all take
// optional [start[, end]] bounds in code points, and every one of them worked
// those bounds out by expanding the whole receiver into a []rune and copying
// the span back out -- four bytes of allocation for every byte of the subject,
// plus a copy, whether or not a bound was passed. `s.startswith("zzz")` over
// 13MB took 63 milliseconds and about 52MB to answer a question about three
// characters.
//
// value.StrSlice had already learned this: "building one costs eight bytes for
// every byte of the string, which is how slicing a hundred megabytes to a
// single character came to allocate eight hundred". The methods had not.
func TestBoundedMethodsDoNotCopyTheSubject(t *testing.T) {
	const n = 1 << 22 // 4MiB, so a per-byte cost is unmistakable
	subject := strings.Repeat("a", n)
	vars := map[string]any{"s": subject}

	for name, src := range map[string]string{
		"startswith": `{{ s.startswith("zzz") }}`,
		"endswith":   `{{ s.endswith("zzz") }}`,
		"count":      `{{ s.count("zzz") }}`,
		"find":       `{{ s.find("zzz") }}`,
		"rfind":      `{{ s.rfind("zzz") }}`,
		// With bounds given the span has to be found, but finding it is
		// a walk with a cursor and not a table of every offset.
		"startswith, bounded": `{{ s.startswith("a", 1, 3) }}`,
		"count, bounded":      `{{ s.count("a", 1, 3) }}`,
	} {
		t.Run(name, func(t *testing.T) {
			tmpl, err := gojja2.New(gojja2.WithoutLimits()).FromString(src)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			// TotalAlloc counts every byte ever allocated, so it is
			// unaffected by whether a collection happens to run: the
			// question here is what the render asks for, not what
			// survives.
			render := func() {
				if err := tmpl.Render(context.Background(), io.Discard, vars); err != nil {
					t.Fatalf("render: %v", err)
				}
			}
			render() // warm the compiled template and the argument conversion
			const runs = 5
			var before, after runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&before)
			for range runs {
				render()
			}
			runtime.ReadMemStats(&after)
			perOp := (after.TotalAlloc - before.TotalAlloc) / runs

			// A tenth of the subject. The defect allocated four times
			// it; answering the question itself needs a few hundred
			// bytes.
			if limit := uint64(n / 10); perOp > limit {
				t.Errorf("%s allocated %d bytes per render over a %d byte "+
					"subject; it is expanding the receiver rather than "+
					"looking at the part it was asked about", src, perOp, n)
			}
		})
	}
}

// Asking about the ends of a string must not walk it either.
//
// With no bounds given the whole subject is selected, so there is nothing to
// work out and no reason to count its code points. The allocation test above
// cannot see this: counting runes allocates nothing, it only takes as long as
// the string is. So this compares against an operation that really is
// proportional -- upper() over the same subject -- and requires the question
// about three characters to be a small fraction of it.
func TestUnboundedMethodsDoNotWalkTheSubject(t *testing.T) {
	// Multi-byte, because counting code points of ASCII is nearly free and
	// would understate what the walk costs.
	const n = 1 << 21
	vars := map[string]any{"s": strings.Repeat("é", n)}
	env := gojja2.New(gojja2.WithoutLimits())

	timeOf := func(src string) time.Duration {
		t.Helper()
		tmpl, err := env.FromString(src)
		if err != nil {
			t.Fatalf("compile %s: %v", src, err)
		}
		best := time.Duration(1<<63 - 1)
		for range 5 {
			start := time.Now()
			if err := tmpl.Render(context.Background(), io.Discard, vars); err != nil {
				t.Fatalf("render %s: %v", src, err)
			}
			best = min(best, time.Since(start))
		}
		return best
	}

	// A walk of the subject, to calibrate what proportional costs here.
	walk := timeOf(`{{ (s.upper()) and 1 or 1 }}`)
	if walk < time.Millisecond {
		t.Skipf("a walk of %d bytes takes %s here, too fast to compare against", n, walk)
	}
	// A fiftieth of the walk. Measured, not guessed: answering these takes
	// about two microseconds against sixty-four milliseconds to walk the
	// same subject, and counting its code points instead takes sixteen.
	// Two per cent sits an order of magnitude below the defect and three
	// above what the answer really costs.
	for _, src := range []string{
		`{{ s.startswith("zzz") }}`,
		`{{ s.endswith("zzz") }}`,
		`{{ s.find("zzz") }}`,
	} {
		if got, limit := timeOf(src), walk/50; got > limit {
			t.Errorf("%s took %s, more than the %s allowed against %s to walk "+
				"the same %d code points: it is counting the code points of "+
				"a subject it was not asked about",
				src, got, limit, walk, n)
		}
	}
}
