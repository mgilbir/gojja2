// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mgilbir/gojja2/internal/ast"
	"github.com/mgilbir/gojja2/value"
)

// TestCompilePanicBecomesACompileFailure pins that the optimizer cannot fail
// worse than not optimizing. Folding executes real filters at compile time,
// with no render and no budget behind it, so a filter that panics on its
// arguments used to take FromString down -- a function whose entire job is to
// report whether a template is valid.
func TestCompilePanicBecomesACompileFailure(t *testing.T) {
	env := mustNew()
	env.AddFilter("boom", func(*State, value.Value, *value.CallArgs) (value.Value, error) {
		panic("filter exploded")
	})
	// All-constant, so the optimizer will try to fold it.
	tmpl, err := env.FromString(`{{ "x"|boom }}`)
	if err != nil {
		t.Fatalf("compilation should survive a panicking filter, got %v", err)
	}
	// The expression was left for runtime, where it fails as a render error.
	out, err := tmpl.RenderString(context.Background(), nil)
	if err == nil {
		t.Fatalf("expected a render error, got %q", out)
	}
	if !errors.Is(err, ErrInternal) {
		t.Errorf("expected ErrInternal, got %v", err)
	}
	if !strings.Contains(err.Error(), "filter exploded") {
		t.Errorf("the error should name the panic, got %q", err)
	}
}

// TestRenderPanicBecomesAnError pins the backstop at the other entry point. A
// template engine renders input its caller does not control, so unwinding the
// caller's goroutine is never the right answer to a bad template.
func TestRenderPanicBecomesAnError(t *testing.T) {
	env := mustNew()
	env.AddFilter("boom", func(*State, value.Value, *value.CallArgs) (value.Value, error) {
		panic("filter exploded")
	})
	// Not constant, so it cannot be folded away: this really is the render.
	tmpl, err := env.FromString(`{% for i in [1] %}{{ i|boom }}{% endfor %}`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	out, err := tmpl.RenderString(context.Background(), nil)
	if err == nil {
		t.Fatalf("expected a render error, got %q", out)
	}
	if !errors.Is(err, ErrInternal) {
		t.Errorf("expected ErrInternal, got %v", err)
	}
}

// TestFoldAllowanceIsPerExpression pins that whether an expression folds does
// not depend on how many expressions preceded it.
//
// This matters because folding is observable: a print tag that folds to
// undefined renders "" where the unfolded form raises. An allowance spent
// across a whole template would make a long file render differently from a
// short one holding the same tag.
func TestFoldAllowanceIsPerExpression(t *testing.T) {
	// A tag whose folded form differs from its unfolded form: folded, the
	// swallowing lookup yields undefined and prints nothing; unfolded, the
	// subscript raises.
	const probe = `{{ 0[1:] }}`
	alone, err := mustNew().FromString(probe)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	aloneOut, aloneErr := alone.RenderString(context.Background(), nil)

	// The same tag, preceded by enough folding work to exhaust any
	// per-template allowance.
	var b strings.Builder
	for range 400 {
		b.WriteString(`{{ "x" * 1000 }}`)
	}
	b.WriteString(probe)
	crowded, err := mustNew().FromString(b.String())
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	crowdedOut, crowdedErr := crowded.RenderString(context.Background(), nil)

	if (aloneErr == nil) != (crowdedErr == nil) {
		t.Fatalf("folding changed with position: alone err=%v, crowded err=%v",
			aloneErr, crowdedErr)
	}
	if aloneErr == nil && !strings.HasSuffix(crowdedOut, aloneOut) {
		t.Errorf("folded tag rendered differently when preceded by other folds:\n"+
			" alone   %q\n crowded ends %q", aloneOut, crowdedOut[max(0, len(crowdedOut)-20):])
	}
}

// TestGlobalsReceiveTheRenderState pins the signature change that makes a
// global able to bound its own work. Without the render's budget a global has
// no way to charge for what it is about to do, which left the whole global
// namespace exempt from WithMaxIterations by construction.
func TestGlobalsReceiveTheRenderState(t *testing.T) {
	var sawState bool
	env := mustNew(WithMaxIterations(100))
	env.AddGlobal("probe", Func("probe", func(s *State, _ *value.CallArgs) (value.Value, error) {
		if s == nil {
			return value.Undefined, errors.New("global was called without a render state")
		}
		sawState = true
		// A global that walks a caller-controlled amount of work must be
		// able to charge for it, and to be stopped when the budget is spent.
		if err := s.Step(1000); err != nil {
			return value.Undefined, err
		}
		return value.String("unreachable"), nil
	}))
	tmpl, err := env.FromString(`{% for i in [1] %}{{ probe() }}{% endfor %}`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	out, err := tmpl.RenderString(context.Background(), nil)
	if !sawState {
		t.Fatal("the global never received a render state")
	}
	if err == nil {
		t.Fatalf("the budget should have stopped the global, got %q", out)
	}
	if !errors.Is(err, ErrTooManyIterations) {
		t.Errorf("expected ErrTooManyIterations, got %v", err)
	}
}

// TestFoldedFilterResultIsSizeChecked pins that a filter's folded result obeys
// the same size limit as a folded operator. Only the operator path checked it,
// so a filter could bake an arbitrarily large constant into the compiled
// template and keep it there for the life of the process.
func TestFoldedFilterResultIsSizeChecked(t *testing.T) {
	env := mustNew()
	tmpl, err := env.FromString(`{{ "x"|center(200000) }}`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	// However it was compiled, the render must still be correct.
	out, err := tmpl.RenderString(context.Background(), nil)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if len(out) != 200000 {
		t.Errorf("got %d bytes, want 200000", len(out))
	}
	// The oversized constant must not have been baked into the tree.
	if constantBytes(tmpl) > maxFoldedConst {
		t.Errorf("a %d-byte constant was folded into the template; the cap is %d",
			constantBytes(tmpl), maxFoldedConst)
	}
}

// constantBytes totals the literal text a compiled template carries.
func constantBytes(t *Template) int {
	total := 0
	walk := func(body []ast.Stmt) {
		for _, stmt := range body {
			if out, ok := stmt.(*ast.Output); ok {
				for _, node := range out.Nodes {
					if d, ok := node.(*ast.TemplateData); ok {
						total += len(d.Data)
					}
					if c, ok := node.(*ast.Const); ok && c.Value.IsString() {
						total += len(c.Value.AsString())
					}
				}
			}
		}
	}
	walk(t.tree.Body)
	return total
}
