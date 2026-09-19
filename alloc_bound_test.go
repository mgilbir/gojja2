// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/mgilbir/gojja2"
)

// What a render allocates stays in proportion to what it was given and what it
// produced.
//
// The budget bounds the output and the iterations; nothing bounded the memory
// that goes through the render on the way there, and two defects found by hand
// were exactly that shape -- str.startswith expanded its whole receiver into a
// []rune to answer a question about three characters, and |wordcount built a
// slice holding every word of its input to count them. Both were invisible to
// every bound in the engine, because neither showed up in the output.
//
// So this measures it. The corpus is 800-odd templates covering the grammar,
// and each is rendered with its allocation counted against the size of the
// template plus the size of what it rendered.
//
// The factor is measured, not chosen: across the corpus the median is 1.4, the
// 99th percentile 2.9 and the worst 4.9, the same to one decimal place on every
// run. Thirty-two is six times the worst observed.
//
// What this catches is a render that starts allocating something large per
// call, whatever the template -- a buffer sized once, a table built eagerly, a
// receiver expanded when it did not need to be. What it does not catch is a
// regression that scales with an input the corpus does not have: these
// templates are a hundred bytes each, so four bytes of allocation per byte of a
// thirteen-megabyte *argument* passes here and is caught by
// TestBoundedMethodsDoNotCopyTheSubject instead. The two are complementary and
// neither is the other's substitute.
const allocFactor = 32

func TestRenderAllocationStaysInProportion(t *testing.T) {
	env := gojja2.New(
		gojja2.WithMaxOutputBytes(1<<20),
		gojja2.WithMaxIterations(200_000),
		gojja2.WithLoader(gojja2.DictLoader(map[string]string{
			"inner":  `[{% block b %}inner{% endblock %}]`,
			"parent": `{% block b %}parent{% endblock %}`,
		})),
	)
	vars := map[string]any{
		"s": "hello", "n": 42, "f": 1.5, "b": true, "z": nil,
		"list": []any{1, 2, 3}, "dict": map[string]any{"a": 1, "b": 2},
		"users": []any{map[string]any{"name": "ada", "age": 36}},
		"html":  "<b>&amp;</b>",
	}

	// The corpus only. A loop is a different regime -- what it allocates
	// follows the iterations, not the output, and a nested one over 5,000
	// items allocates 123 times its own size while doing nothing wrong --
	// so mixing the two would force a bound too loose to catch anything.
	var cases []string
	root := filepath.Join("testdata", "corpus")
	if err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if _, src, ok := strings.Cut(string(body), "\n---\n"); ok {
			cases = append(cases, src)
		}
		return nil
	}); err != nil {
		t.Fatalf("reading the corpus: %v", err)
	}

	var checked int
	var ratios []float64
	var worstRatio float64
	var worstSrc string
	for _, src := range cases {
		tmpl, err := env.FromString(src)
		if err != nil {
			continue // a case about compile errors has nothing to render
		}
		var out strings.Builder
		if err := tmpl.Render(context.Background(), &out, vars); err != nil {
			continue // a case about render errors, likewise
		}

		// TotalAlloc counts every byte ever asked for, so a collection
		// running or not cannot change the answer. The template is
		// rendered once first, to leave nothing lazy behind it.
		var before, after runtime.MemStats
		const runs = 3
		runtime.ReadMemStats(&before)
		for range runs {
			if err := tmpl.Render(context.Background(), io.Discard, vars); err != nil {
				break
			}
		}
		runtime.ReadMemStats(&after)
		per := (after.TotalAlloc - before.TotalAlloc) / runs

		// 4KiB of slack, because a render that produces nothing still
		// sets up a state, a scope and a buffer.
		base := uint64(len(src) + out.Len() + 4096)
		if r := float64(per) / float64(base); r > worstRatio {
			worstRatio, worstSrc = r, src
		}
		ratios = append(ratios, float64(per)/float64(base))
		if per > allocFactor*base {
			t.Errorf("rendering %d bytes of template into %d bytes of output "+
				"allocated %d, more than %d times their size:\n  %q",
				len(src), out.Len(), per, allocFactor, truncate(src, 200))
		}
		checked++
	}
	sort.Float64s(ratios)
	q := func(f float64) float64 { return ratios[int(float64(len(ratios)-1)*f)] }
	// Logged rather than asserted on: the numbers are what the factor above
	// was chosen from, and seeing them move is how anyone would know to
	// revisit it.
	t.Logf("allocation over %d templates: median %.1f, p90 %.1f, p99 %.1f, "+
		"max %.1f (bar is %d)\n  worst: %q",
		len(ratios), q(.5), q(.9), q(.99), ratios[len(ratios)-1],
		allocFactor, truncate(worstSrc, 70))
	if checked < 400 {
		t.Fatalf("only %d templates were measured; the corpus is not being "+
			"read and this test is proving nothing", checked)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
