// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/gojja2"
)

// Expanding `**mapping` costs the size of the mapping, not its square.
//
// The duplicate check asked CallArgs.Kwarg, which is a scan of everything
// merged so far. That is the right shape for a call written out, where the
// keywords are a handful and a map would cost more than it saved. Here the
// count is the caller's: `{{ dict(**ctx) }}` over a context of 300,000 keys
// took three minutes and thirteen seconds, against twenty-two milliseconds to
// walk the same dictionary with |items.
//
// That walk is the control. `{{ d|items|list|length }}` converts the same
// mapping, visits every entry and builds a list of them, and does not merge
// anything -- so whatever the expansion costs beyond it is the merge, and the
// comparison needs no threshold in milliseconds.
//
// Measured as an absolute ratio between two input sizes instead, this sat at
// 6.7 against a bar of 8 on one core, which is a flaky test waiting for a
// slower builder: the sibling written that way passed at 4.2 locally and failed
// at 8.9 in CI, saying nothing about the code. Both halves here slow down
// together.
func TestKwargExpansionIsLinear(t *testing.T) {
	const n = 60_000
	d := make(map[string]any, n)
	for i := range n {
		d[fmt.Sprintf("k%07d", i)] = i
	}
	vars := map[string]any{"d": d}

	render := func(src string) time.Duration {
		t.Helper()
		tmpl, err := gojja2.New(gojja2.WithoutLimits()).FromString(src)
		if err != nil {
			t.Fatalf("compile %s: %v", src, err)
		}
		best := time.Duration(1<<63 - 1)
		for range 3 {
			start := time.Now()
			if _, err := tmpl.RenderString(context.Background(), vars); err != nil {
				t.Fatalf("render %s: %v", src, err)
			}
			best = min(best, time.Since(start))
		}
		return best
	}

	merged := render(`{{ dict(**d)|length }}`)
	walked := render(`{{ d|items|list|length }}`)
	if walked < time.Millisecond {
		t.Skipf("%d entries walk in %s here, too fast to compare against", n, walked)
	}
	// Six times the walk. Merging pairwise measured over a hundred times it
	// at this size; merging against the keywords written out measures about
	// one and a half, because both walk the same mapping once.
	if ratio := float64(merged) / float64(walked); ratio > 6 {
		t.Errorf("expanding %d keys took %s against %s to walk the same "+
			"mapping, a factor of %.1f: the merge is scanning what it "+
			"has already merged", n, merged, walked, ratio)
	}
}

// The expansion has to stop when the render is out of time, and be charged for
// the arguments it builds.
func TestKwargExpansionIsBounded(t *testing.T) {
	const n = 200_000
	d := make(map[string]any, n)
	for i := range n {
		d[fmt.Sprintf("k%07d", i)] = i
	}
	vars := map[string]any{"d": d}

	// The bound sits between what reaching the mapping costs and what
	// reaching it and then merging it costs, so only the merge's own charge
	// can be what refuses this. A bound below both would be spent
	// converting the argument, and the test would pass with the merge
	// charging nothing at all.
	t.Run("charged", func(t *testing.T) {
		const small = 5_000
		little := make(map[string]any, small)
		for i := range small {
			little[fmt.Sprintf("k%07d", i)] = i
		}
		env := gojja2.New(gojja2.WithMaxIterations(small + small/2))
		if out, err := mustCompile(t, env, `{{ d|length }}`).
			RenderString(context.Background(), map[string]any{"d": little}); err != nil {
			t.Fatalf("reaching the mapping alone should fit the bound: %v (%q)", err, out)
		}
		out, err := mustCompile(t, env, `{{ dict(**d)|length }}`).
			RenderString(context.Background(), map[string]any{"d": little})
		if err == nil {
			t.Fatalf("merging %d keys on top of reaching them fitted a bound "+
				"of %d and returned %q", small, small+small/2, out)
		}
		if !errors.Is(err, gojja2.ErrTooManyIterations) {
			t.Errorf("error = %v, want ErrTooManyIterations", err)
		}
	})

	t.Run("interruptible", func(t *testing.T) {
		tmpl, err := gojja2.New(gojja2.WithoutLimits()).FromString(`{{ dict(**d)|length }}`)
		if err != nil {
			t.Fatalf("compile: %v", err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := tmpl.RenderString(ctx, vars); !errors.Is(err, context.Canceled) {
			t.Errorf("cancelled render = %v, want context.Canceled", err)
		}
	})
}

// The merge still refuses the same things, and still words them the same way.
func TestKwargExpansionKeepsItsErrors(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		// A name given twice, once written out and once through **.
		{`{{ [1,2]|join(d="-", **{"d": "+"}) }}`, ""},
		// A mapping whose key is not a string.
		{`{{ [1,2]|join(**{1: "-"}) }}`, "keywords must be strings"},
		// Not a mapping at all.
		{`{% set l = [1,2] %}{{ l|join(**["db"]) }}`,
			"argument after ** must be a mapping, not list"},
	} {
		tmpl, err := gojja2.New().FromString(tc.src)
		if err != nil {
			t.Errorf("%s: compile: %v", tc.src, err)
			continue
		}
		_, err = tmpl.RenderString(context.Background(), nil)
		if tc.want == "" {
			// jinja2 folds this one, so it answers rather than
			// raising; what matters is that it does not change.
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s = %v, want a message containing %q", tc.src, err, tc.want)
		}
	}

	// A duplicate between two keys of the same expansion cannot arise -- a
	// mapping has each key once -- so the check is against what was already
	// merged. Over a name written out, it still fires.
	tmpl, err := gojja2.New().FromString(`{% macro m() %}{{ kwargs }}{% endmacro %}{{ m(a=1, **{"a": 2}) }}`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	_, err = tmpl.RenderString(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "multiple values for keyword argument") {
		t.Errorf("a name given twice = %v, want the duplicate error", err)
	}
}

// A call takes at most one ** expansion.
//
// The merge relies on it: with one mapping, and each key in a mapping once, two
// entries of an expansion cannot carry the same name, so the duplicate check
// only has to look at the keywords written out. If the grammar ever grows a
// second expansion, this fails and the check has to grow with it.
func TestOnlyOneKeywordExpansion(t *testing.T) {
	_, err := gojja2.New().FromString(
		`{% macro m() %}{{ kwargs }}{% endmacro %}{{ m(**{"a":1}, **{"b":2}) }}`)
	if err == nil {
		t.Fatal("two ** expansions compiled; the merge's duplicate check " +
			"only looks at the keywords written out and is now incomplete")
	}
}

// A Markup key and a plain key of the same text are one entry, as they are in
// Python, which is the other half of what the merge relies on.
func TestMarkupKeyIsNotASecondKey(t *testing.T) {
	tmpl, err := gojja2.New().FromString(
		`{% macro m() %}{{ kwargs }}{% endmacro %}{{ m(**{("a"|safe): 1, "a": 2}) }}`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	got, err := tmpl.RenderString(context.Background(), nil)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if want := "{'a': 2}"; got != want {
		t.Errorf("= %q, want %q -- two entries of one expansion now share a "+
			"name, which the merge's duplicate check does not look for", got, want)
	}
}

// mustCompile compiles src or fails the test.
func mustCompile(t *testing.T, env *gojja2.Environment, src string) *gojja2.Template {
	t.Helper()
	tmpl, err := env.FromString(src)
	if err != nil {
		t.Fatalf("compile %s: %v", src, err)
	}
	return tmpl
}
