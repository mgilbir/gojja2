// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"context"
	"errors"
	"testing"

	"github.com/mgilbir/gojja2/internal/ast"
	"github.com/mgilbir/gojja2/internal/lexer"
	"github.com/mgilbir/gojja2/internal/parser"
	"github.com/mgilbir/gojja2/value"
)

// A budget that has refused once refuses everything after it.
//
// The three ways a render runs out -- time, iterations, output -- are all
// one-way: a cancelled context stays cancelled and the two counters only
// climb. So the first refusal is the final answer, and repeating it is what
// makes it safe for one caller to drop one. The charges below would each
// succeed on their own, which is the point: what they report is the earlier
// refusal, not a fresh judgement about themselves.
func TestSpentBudgetStaysSpent(t *testing.T) {
	t.Run("iterations", func(t *testing.T) {
		b := &budget{maxSteps: 1}
		if err := b.chargeSteps(5); !errors.Is(err, ErrTooManyIterations) {
			t.Fatalf("first charge = %v, want ErrTooManyIterations", err)
		}
		// maxOutput is zero here, so this write is unbounded and would
		// otherwise be free.
		if err := b.account(1); !errors.Is(err, ErrTooManyIterations) {
			t.Errorf("account after refusal = %v, want the refusal", err)
		}
		if err := b.tick(); !errors.Is(err, ErrTooManyIterations) {
			t.Errorf("tick after refusal = %v, want the refusal", err)
		}
		if err := b.step(); !errors.Is(err, ErrTooManyIterations) {
			t.Errorf("step after refusal = %v, want the refusal", err)
		}
	})

	t.Run("output", func(t *testing.T) {
		b := &budget{maxOutput: 1}
		if err := b.account(5); !errors.Is(err, ErrOutputTooLarge) {
			t.Fatalf("first write = %v, want ErrOutputTooLarge", err)
		}
		// maxSteps is zero, so iterating is unbounded.
		if err := b.step(); !errors.Is(err, ErrOutputTooLarge) {
			t.Errorf("step after refusal = %v, want the refusal", err)
		}
	})

	t.Run("cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		b := &budget{done: ctx.Done(), ctx: ctx}
		// Reach the interval so the context is actually consulted.
		if err := b.chargeSteps(checkInterval); !errors.Is(err, context.Canceled) {
			t.Fatalf("charge past the check interval = %v, want context.Canceled", err)
		}
		// One step is far short of the next check, so without
		// stickiness this would report nothing at all.
		if err := b.step(); !errors.Is(err, context.Canceled) {
			t.Errorf("step after cancellation = %v, want context.Canceled", err)
		}
	})

	// The refusal that is repeated is the *first* one. A render stopped by
	// its deadline should say so, even if the work already in flight would
	// also have overrun the output bound a moment later -- otherwise the
	// reported cause depends on which check happened to run next.
	t.Run("the first cause wins", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		b := &budget{done: ctx.Done(), ctx: ctx, maxOutput: 1}
		if err := b.chargeSteps(checkInterval); !errors.Is(err, context.Canceled) {
			t.Fatalf("first charge = %v, want context.Canceled", err)
		}
		if err := b.account(5); !errors.Is(err, context.Canceled) {
			t.Errorf("write after cancellation = %v, want context.Canceled "+
				"(the output bound is a later cause, not the reason "+
				"the render stopped)", err)
		}
		if err := b.step(); !errors.Is(err, context.Canceled) {
			t.Errorf("step after cancellation = %v, want context.Canceled", err)
		}
	})

	t.Run("resetAllowance clears it", func(t *testing.T) {
		b := &budget{maxSteps: 1}
		if err := b.chargeSteps(5); err == nil {
			t.Fatal("charge should have been refused")
		}
		b.resetAllowance()
		if err := b.step(); err != nil {
			t.Errorf("step after reset = %v, want nil", err)
		}
	})
}

// A fold that runs out of allowance must not stop the folds after it.
//
// The allowance is spent per fold attempt precisely so that whether an
// expression folds does not depend on what preceded it in the file -- folding
// is observable, so making it positional would make the same tag render
// differently depending on its neighbours. A refusal that stuck to the shared
// folder budget would do exactly that.
//
// This drives the folder directly rather than through a render, because the
// premise has to be checked rather than assumed: a "heavy" expression that
// quietly folded within its allowance would make the test prove nothing, and
// from outside a render there is no way to tell that apart from success.
func TestARefusedFoldDoesNotPoisonLaterFolds(t *testing.T) {
	// Over maxFoldedConst, so folding it is refused for output.
	heavy := foldExpr(t, `"x" * 100000`)
	// Trivially foldable, and its value says so.
	cheap := foldExpr(t, `2 + 3`)

	c := newConstEvaluator(mustNew(), "t", true)
	if _, ok := c.tryConstEval(heavy); ok {
		t.Fatal("the heavy expression folded, so it never exhausts the " +
			"allowance and this test cannot detect poisoning")
	}
	if c.st.budget.failed == nil {
		t.Fatal("the refused fold did not record a failure, so there is " +
			"nothing for a later fold to be poisoned by")
	}
	got, ok := c.tryConstEval(cheap)
	if !ok {
		t.Fatal("a trivially foldable expression stopped folding after " +
			"a refused fold preceded it")
	}
	if s := value.Repr(got); s != "5" {
		t.Errorf("folded value = %s, want 5", s)
	}
}

// foldExpr parses `{{ expr }}` and returns the expression inside it.
func foldExpr(t *testing.T, expr string) ast.Expr {
	t.Helper()
	tree, err := parser.Parse(lexer.DefaultSyntax(), parser.Options{}, "{{ "+expr+" }}", "t")
	if err != nil {
		t.Fatalf("parse %s: %v", expr, err)
	}
	out, ok := tree.Body[0].(*ast.Output)
	if !ok || len(out.Nodes) != 1 {
		t.Fatalf("parse %s: want one output expression, got %T", expr, tree.Body[0])
	}
	return out.Nodes[0]
}

// A render must not report success after a charge was refused.
//
// Every path inside this package propagates a refusal, but State.Resolve is a
// published signature with nowhere to put one, and an extension is not a path
// this package controls. The global below drops its refusal the way a careless
// one would; the render still has to fail, because the alternative is handing
// back output that the budget already said could not be produced.
func TestRenderFailsAfterADroppedRefusal(t *testing.T) {
	env := mustNew(WithMaxIterations(100))
	env.AddGlobal("sloppy", Func("sloppy", func(s *State, _ *value.CallArgs) (value.Value, error) {
		_ = s.Step(1000) // refused, and dropped
		return value.String("output the budget refused"), nil
	}))
	// The call's result is discarded, so nothing is written afterwards and
	// no later charge happens to re-report the refusal. Printing it would
	// have made the output write raise instead, which tests the write
	// rather than the backstop.
	tmpl, err := env.FromString(`{% set _ = sloppy() %}`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	out, err := tmpl.RenderString(context.Background(), nil)
	if err == nil {
		t.Fatalf("render succeeded with %q after the budget refused a charge", out)
	}
	if !errors.Is(err, ErrTooManyIterations) {
		t.Errorf("render error = %v, want ErrTooManyIterations", err)
	}
}
