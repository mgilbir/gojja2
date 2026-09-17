// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"context"
	"errors"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/gojja2"
	"github.com/mgilbir/gojja2/errs"
)

// renderWith is the shape every test here uses: render src and return the
// error, discarding output.
func renderWith(t *testing.T, ctx context.Context, env *gojja2.Environment, src string) error {
	t.Helper()
	tmpl, err := env.FromString(src)
	if err != nil {
		t.Fatalf("compile %q: %v", src, err)
	}
	return tmpl.Render(ctx, io.Discard, nil)
}

// TestIterationBudget covers the two ways a template can ask for unbounded
// work without any recursion: a loop over a huge range, and a filter that
// materialises one. The second reaches no {% for %} at all, so the loop
// counter alone would not see it.
func TestIterationBudget(t *testing.T) {
	env := gojja2.New(gojja2.WithMaxIterations(1000))
	for _, src := range []string{
		`{% for i in range(10000000000) %}{% endfor %}`,
		`{% for i in range(10000000000) if i %}{% endfor %}`,
		`{{ range(10000000000)|list|length }}`,
		`{{ range(10000000000)|sort|first }}`,
		`{% for a in range(100) %}{% for b in range(100) %}{% endfor %}{% endfor %}`,
	} {
		err := renderWith(t, context.Background(), env, src)
		if !errors.Is(err, gojja2.ErrTooManyIterations) {
			t.Errorf("%s: got %v, want ErrTooManyIterations", src, err)
		}
	}
}

// TestIterationBudgetAllowsRealTemplates guards the other direction: the bound
// must not fire on work a template legitimately does.
func TestIterationBudgetAllowsRealTemplates(t *testing.T) {
	env := gojja2.New(gojja2.WithMaxIterations(1000))
	out, err := mustRender(t, env, `{% for i in range(999) %}{{ i }},{% endfor %}`)
	if err != nil {
		t.Fatalf("999 iterations should be under a 1000 bound: %v", err)
	}
	if !strings.HasSuffix(out, "998,") {
		t.Fatalf("truncated output: %q", out[max(0, len(out)-20):])
	}
}

// TestBudgetCrossesEveryTemplateBoundary is the test that would have caught
// the bug the recursion counter had: a budget stored on the State restarts at
// every template boundary, because each of them builds a fresh State. Neither
// template in any pair here exceeds the bound on its own.
//
// Every construct that can reach another template is listed, and the list is
// the point: {% import %} built its State by hand and left the budget nil, so
// an imported template ran with no bound and no context while the importing
// one was bounded. A table that names every boundary is what stops the next
// one from being added without its budget.
func TestBudgetCrossesEveryTemplateBoundary(t *testing.T) {
	loader := gojja2.DictLoader(map[string]string{
		"inner.txt":  `{% for j in range(6) %}x{% endfor %}`,
		"module.txt": `{% for j in range(6) %}{% endfor %}{% macro m() %}{% endmacro %}`,
		"parent.txt": `{% for j in range(6) %}{% endfor %}`,
	})
	for name, src := range map[string]string{
		"include":     `{% for i in range(6) %}{% include "inner.txt" %}{% endfor %}`,
		"import":      `{% for i in range(6) %}{% import "module.txt" as m %}{% endfor %}`,
		"from import": `{% for i in range(6) %}{% from "module.txt" import m %}{% endfor %}`,
		"extends":     `{% for i in range(6) %}{% endfor %}{% extends "parent.txt" %}`,
	} {
		t.Run(name, func(t *testing.T) {
			env := gojja2.New(gojja2.WithMaxIterations(10), gojja2.WithLoader(loader))
			err := renderWith(t, context.Background(), env, src)
			if !errors.Is(err, gojja2.ErrTooManyIterations) {
				t.Fatalf("got %v, want ErrTooManyIterations across the %s boundary", err, name)
			}
		})
	}
}

// renderWithin runs a render on its own goroutine and gives up after wall.
//
// A render that ignores its deadline must fail the test rather than hang it:
// a bound that has gone missing is a bug to report, not a build to wait out.
// The goroutine is left running -- it is already unbounded, which is the thing
// under test -- and the process exits when the package's tests finish.
func renderWithin(t *testing.T, wall time.Duration, ctx context.Context, env *gojja2.Environment, src string) error {
	t.Helper()
	tmpl, err := env.FromString(src)
	if err != nil {
		t.Fatalf("compile %q: %v", src, err)
	}
	done := make(chan error, 1)
	go func() { done <- tmpl.Render(ctx, io.Discard, nil) }()
	select {
	case err := <-done:
		return err
	case <-time.After(wall):
		t.Fatalf("render did not stop within %v: nothing is bounding it", wall)
		return nil
	}
}

// TestCancellationCrossesEveryTemplateBoundary is the same list against the
// context rather than the iteration bound. The two are not redundant: the
// context is read *through* the budget, so a nested render with no budget
// also has no deadline, and a caller who turned the bounds off with
// WithoutLimits and is relying on a deadline would have had nothing at all.
func TestCancellationCrossesEveryTemplateBoundary(t *testing.T) {
	loader := gojja2.DictLoader(map[string]string{
		"spin.txt": `{% for j in range(1000000000) %}{% endfor %}`,
		"base.txt": `{% for j in range(1000000000) %}{% endfor %}`,
	})
	for name, src := range map[string]string{
		"include":     `{% include "spin.txt" %}`,
		"import":      `{% import "spin.txt" as m %}`,
		"from import": `{% from "spin.txt" import m %}`,
		"extends":     `{% extends "base.txt" %}`,
	} {
		t.Run(name, func(t *testing.T) {
			env := gojja2.New(gojja2.WithoutLimits(), gojja2.WithLoader(loader))
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			start := time.Now()
			err := renderWithin(t, 30*time.Second, ctx, env, src)
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("got %v after %v, want DeadlineExceeded across the %s boundary",
					err, time.Since(start), name)
			}
		})
	}
}

// TestIterationBudgetCoversCollects covers the walks that build a container
// before anything else happens, so no {% for %} pass and no filter is reached
// to charge them. Each of these allocated until the machine gave up before the
// walk itself was charged.
func TestIterationBudgetCoversCollects(t *testing.T) {
	env := gojja2.New(gojja2.WithMaxIterations(1000))
	for name, src := range map[string]string{
		"star args":     `{% macro m() %}{% endmacro %}{{ m(*range(10000000000)) }}`,
		"tuple unpack":  `{% set a, b = range(10000000000) %}`,
		"list.extend":   `{% set l = [] %}{{ l.extend(range(10000000000)) }}`,
		"nested extend": `{% set l = [] %}{% for i in range(4) %}{{ l.extend(range(500)) }}{% endfor %}`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := renderWith(t, context.Background(), env, src); !errors.Is(err, gojja2.ErrTooManyIterations) {
				t.Errorf("%s: got %v, want ErrTooManyIterations", src, err)
			}
		})
	}
}

// TestBoundMethodKeepsItsBudget: `l.extend` is resolved to a bound method that
// closes over the render it was looked up in. A lookup outside a render -- the
// optimizer's -- binds a nil one, so if such a method could ever be folded
// into the AST, every render of that template would run it unbounded. Aliasing
// it through {% set %} must not lose the budget either.
func TestBoundMethodKeepsItsBudget(t *testing.T) {
	env := gojja2.New(gojja2.WithMaxIterations(1000))
	for name, src := range map[string]string{
		"direct":           `{% set l = [] %}{{ l.extend(range(10000000000)) }}`,
		"on a literal":     `{{ [].extend(range(10000000000)) }}`,
		"aliased":          `{% set l = [] %}{% set e = l.extend %}{{ e(range(10000000000)) }}`,
		"through a filter": `{{ ([]|list).extend(range(10000000000)) }}`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := renderWith(t, context.Background(), env, src); !errors.Is(err, gojja2.ErrTooManyIterations) {
				t.Errorf("%s: got %v, want ErrTooManyIterations", src, err)
			}
		})
	}
}

func TestOutputBudget(t *testing.T) {
	env := gojja2.New(gojja2.WithMaxOutputBytes(4096))
	err := renderWith(t, context.Background(), env,
		`{% for i in range(100000) %}0123456789{% endfor %}`)
	if !errors.Is(err, gojja2.ErrOutputTooLarge) {
		t.Fatalf("got %v, want ErrOutputTooLarge", err)
	}
}

// TestOutputBudgetCountsCaptures: text captured by {% filter %} never reaches
// the writer directly, so a bound that only counted the writer would miss the
// buffer that actually holds the memory.
func TestOutputBudgetCountsCaptures(t *testing.T) {
	env := gojja2.New(gojja2.WithMaxOutputBytes(4096))
	err := renderWith(t, context.Background(), env,
		`{% filter upper %}{% for i in range(100000) %}abcdefghij{% endfor %}{% endfilter %}`)
	if !errors.Is(err, gojja2.ErrOutputTooLarge) {
		t.Fatalf("got %v, want ErrOutputTooLarge", err)
	}
}

// TestRepetitionIsCharged: `{{ "x" * 1000000000 }}` builds its result during
// evaluation, so the write that would have been charged never happens -- the
// process died first. The allocation has to be charged before it is made.
func TestRepetitionIsCharged(t *testing.T) {
	for name, tc := range map[string]struct {
		env  *gojja2.Environment
		src  string
		want error
	}{
		"string": {gojja2.New(gojja2.WithMaxOutputBytes(4096)),
			`{{ "x" * 1000000000 }}`, gojja2.ErrOutputTooLarge},
		"string, count first": {gojja2.New(gojja2.WithMaxOutputBytes(4096)),
			`{{ 1000000000 * "x" }}`, gojja2.ErrOutputTooLarge},
		"list": {gojja2.New(gojja2.WithMaxIterations(1000)),
			`{% set l = [1, 2] * 1000000000 %}`, gojja2.ErrTooManyIterations},
		"tuple": {gojja2.New(gojja2.WithMaxIterations(1000)),
			`{% set t = (1, 2) * 1000000000 %}`, gojja2.ErrTooManyIterations},
		// The count overflows int64 when multiplied by the width, so a
		// wrapped negative would read as a tiny allocation. Past the
		// hard ceiling the answer is CPython's own OverflowError rather
		// than a budget error: the repetition is refused outright, and
		// no budget large enough to matter exists.
		"overflowing count": {gojja2.New(gojja2.WithMaxOutputBytes(4096)),
			`{{ "xx" * 9000000000000000000 }}`, errs.OverflowError},
	} {
		t.Run(name, func(t *testing.T) {
			if err := renderWith(t, context.Background(), tc.env, tc.src); !errors.Is(err, tc.want) {
				t.Errorf("%s: got %v, want %v", tc.src, err, tc.want)
			}
		})
	}
}

// TestCompilingDoesNotAllocate: constant folding runs at compile time, where
// there is no render and so no budget. Each of these built its result during
// FromString, before anyone asked for a render, and took the process with it.
func TestCompilingDoesNotAllocate(t *testing.T) {
	env := gojja2.New()
	for name, src := range map[string]string{
		"repeat":         `{{ "x" * 1000000000 }}`,
		"list repeat":    `{{ ([0] * 1000000000)|length }}`,
		"doubling":       `{{ "x" * 60000 + "x" * 60000 }}`,
		"nested repeat":  `{{ ("x" * 60000) * 60000 }}`,
		"inside a block": `{% if true %}{{ "x" * 1000000000 }}{% endif %}`,
		// `%` sizes its result from a width the template wrote, which
		// the fold budget never saw: `*` was screened by its callers
		// and `%` by neither of them. The last of these OOM-killed
		// FromString at a four-gigabyte cap, from 42 bytes of template.
		"pad width":     `{{ "%1000000000s" % "x" }}`,
		"precision":     `{{ "%.1000000000f" % 1.5 }}`,
		"starred width": `{{ "%*s" % (1000000000, "x") }}`,
		"many pads":     `{{ ("%(a)10000000s" * 200) % {"a": "x"} }}`,
	} {
		t.Run(name, func(t *testing.T) {
			// Compiling must return; what it produces is the render's
			// problem, and the render is bounded.
			if _, err := env.FromString(src); err != nil {
				t.Fatalf("compile %q: %v", src, err)
			}
		})
	}
}

// TestSizedAllocationsAreCharged covers every operator that sizes its result
// from a number the template wrote, against a budget far below what it asks
// for. `*` was charged by each of its two callers and `%` by neither, which is
// the shape this table exists to stop: the charge belongs to the operation, so
// a new caller cannot forget it and a new operator has to be added here.
func TestSizedAllocationsAreCharged(t *testing.T) {
	for name, tc := range map[string]struct {
		src  string
		want error
	}{
		"repeat":          {`{{ "x" * 1000000 }}`, gojja2.ErrOutputTooLarge},
		"list repeat":     {`{% set l = [1] * 1000000 %}`, gojja2.ErrTooManyIterations},
		"pad width":       {`{% set w = "%1000000s" %}{{ w % "x" }}`, gojja2.ErrOutputTooLarge},
		"pad precision":   {`{% set w = "%.1000000f" %}{{ w % 1.5 }}`, gojja2.ErrOutputTooLarge},
		"starred width":   {`{% set w = "%*s" %}{{ w % (1000000, "x") }}`, gojja2.ErrOutputTooLarge},
		"integer minimum": {`{% set w = "%.1000000d" %}{{ w % 1 }}`, gojja2.ErrOutputTooLarge},
		"through format":  {`{% set w = "%1000000s" %}{{ w|format("x") }}`, gojja2.ErrOutputTooLarge},
	} {
		t.Run(name, func(t *testing.T) {
			env := gojja2.New(
				gojja2.WithMaxOutputBytes(4096),
				gojja2.WithMaxIterations(1000),
			)
			if err := renderWith(t, context.Background(), env, tc.src); !errors.Is(err, tc.want) {
				t.Errorf("%s: got %v, want %v", tc.src, err, tc.want)
			}
		})
	}
}

// TestFoldingStillHappens guards the other direction: declining to fold a huge
// constant must not stop the optimizer folding ordinary ones.
func TestFoldingStillHappens(t *testing.T) {
	env := gojja2.New()
	out, err := mustRender(t, env, `{{ "ab" * 3 }}{{ 2 + 3 }}{{ [1] * 2 }}`)
	if err != nil {
		t.Fatal(err)
	}
	if want := "ababab5[1, 1]"; out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
}

// TestRepetitionAllowsRealTemplates: padding and separator lines are what
// repetition is actually for, and must still work.
func TestRepetitionAllowsRealTemplates(t *testing.T) {
	env := gojja2.New(gojja2.WithMaxOutputBytes(4096), gojja2.WithMaxIterations(1000))
	out, err := mustRender(t, env, `{{ "-" * 40 }}|{{ ([0] * 3)|length }}`)
	if err != nil {
		t.Fatalf("ordinary repetition must not trip a bound: %v", err)
	}
	if want := strings.Repeat("-", 40) + "|3"; out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
}

func TestContextCancellation(t *testing.T) {
	env := gojja2.New(gojja2.WithMaxIterations(0)) // only the context bounds this
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := renderWith(t, ctx, env, `{% for i in range(10000000000) %}{% endfor %}`)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
}

func TestContextDeadline(t *testing.T) {
	env := gojja2.New(gojja2.WithMaxIterations(0))
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := renderWith(t, ctx, env, `{% for i in range(10000000000) %}{% endfor %}`)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v, want context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("deadline took %v to take effect", elapsed)
	}
}

// countingWriter records how much reached it, so a test can tell streamed
// output from output handed over in one piece at the end.
type countingWriter struct {
	n      int
	writes int
}

func (c *countingWriter) Write(p []byte) (int, error) {
	c.n += len(p)
	c.writes++
	return len(p), nil
}

// TestRenderStreams: the template writes well past the buffer and then fails.
// A render that buffered the whole document would hand the writer nothing.
func TestRenderStreams(t *testing.T) {
	env := gojja2.New()
	tmpl, err := env.FromString(`{% for i in range(8192) %}x{% endfor %}{{ 1/0 }}`)
	if err != nil {
		t.Fatal(err)
	}
	var w countingWriter
	if err := tmpl.Render(context.Background(), &w, nil); err == nil {
		t.Fatal("expected the division to fail the render")
	}
	if w.n < 4096 {
		t.Fatalf("writer received %d bytes in %d writes; output was not streamed", w.n, w.writes)
	}
}

// TestRenderStringIsAllOrNothing: the string form keeps the old contract.
func TestRenderStringIsAllOrNothing(t *testing.T) {
	env := gojja2.New()
	tmpl, err := env.FromString(`before{{ 1/0 }}`)
	if err != nil {
		t.Fatal(err)
	}
	out, err := tmpl.RenderString(context.Background(), nil)
	if err == nil {
		t.Fatal("expected an error")
	}
	if out != "" {
		t.Fatalf("got partial output %q, want none", out)
	}
}

func mustRender(t *testing.T, env *gojja2.Environment, src string) (string, error) {
	t.Helper()
	tmpl, err := env.FromString(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return tmpl.RenderString(context.Background(), nil)
}

// TestMembershipIsBounded covers the generic scan that Container does not
// short-circuit. `in` over a long sequence is a walk, and a walk that consults
// neither the budget nor the context is a region nothing can interrupt.
func TestMembershipIsBounded(t *testing.T) {
	env := gojja2.New(gojja2.WithMaxIterations(1000))
	for name, src := range map[string]string{
		"list":        `{% set l = range(100000)|list %}{{ -1 in l }}`,
		"not in list": `{% set l = range(100000)|list %}{{ -1 not in l }}`,
		"is in":       `{% set l = range(100000)|list %}{{ -1 is in(l) }}`,
		"tuple":       `{% set t = range(100000)|list|first %}{{ -1 in range(100000)|list }}`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := renderWith(t, context.Background(), env, src); !errors.Is(err, gojja2.ErrTooManyIterations) {
				t.Errorf("%s: got %v, want ErrTooManyIterations", src, err)
			}
		})
	}
}

// TestSustainedFiltersStopWhenCancelled pins the promise docs/divergences.md
// makes: a filter that does sustained work without writing output polls as it
// goes, so a cancelled render stops within microseconds rather than at the end
// of whatever was running. urlencode and urlize did; the ones that sort or
// aggregate did not.
func TestSustainedFiltersStopWhenCancelled(t *testing.T) {
	for name, src := range map[string]string{
		"sort":     `{{ big|sort|length }}`,
		"min":      `{{ big|min }}`,
		"max":      `{{ big|max }}`,
		"groupby":  `{{ big|groupby("x")|length }}`,
		"dictsort": `{{ mapping|dictsort|length }}`,
	} {
		t.Run(name, func(t *testing.T) {
			env := gojja2.New(gojja2.WithoutLimits())
			tmpl, err := env.FromString(src)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			big := make([]any, 120_000)
			mapping := make(map[string]any, 120_000)
			for i := range big {
				big[i] = map[string]any{"x": i % 97}
				mapping[strconv.Itoa(i)] = i
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel() // already done before the filter starts

			done := make(chan error, 1)
			go func() {
				_, err := tmpl.RenderString(ctx, map[string]any{"big": big, "mapping": mapping})
				done <- err
			}()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("got %v, want context.Canceled", err)
				}
			case <-time.After(30 * time.Second):
				t.Fatal("the filter did not notice a cancelled context")
			}
		})
	}
}
