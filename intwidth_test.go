// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"context"
	"strings"
	"testing"

	"github.com/mgilbir/gojja2/value"
)

// The width of an integer an expression computes is bounded, and the bound is
// the same wherever the integer comes from.
//
// `**` was bounded and `*` was not, which made the bound decorative: squaring
// with `x ** 2` was refused past 2**20 bits and squaring with `x * x` -- the
// same operation, one character shorter -- was not. Because multiplication
// doubles the operand width, a loop that squares through a namespace grows
// exponentially while the template stays the same size, so twelve iterations
// and four bytes of output were enough to OOM-kill the process with
// WithMaxIterations and WithMaxOutputBytes both set. Neither bound counts
// big.Int memory, which is why neither of them fired.
//
// The fix is not another special case for `*`: it is that no integer an
// expression computes may exceed value.MaxIntBits, enforced at every operator
// that can widen one, so the next operator added inherits the bound instead of
// reopening the hole.
func TestIntegerWidthIsBounded(t *testing.T) {
	// 2**524288 is the widest power of two `**` will build: estimatePowBits
	// uses the base's BitLen, which is 2 for base 2, so the estimate is
	// twice the exponent and 524288 is what fits under the limit.
	const wide = `{% set x = 2 ** 524288 %}`

	for _, tc := range []struct{ name, src string }{
		{"multiply", wide + `{{ (x * x)|string|length }}`},
		{"multiply by itself through a namespace loop",
			`{% set ns = namespace(x = 2 ** 524288) %}` +
				`{% for i in range(10) %}{% set ns.x = ns.x * ns.x %}{% endfor %}DONE`},
		{"power", wide + `{{ (x ** 2)|string|length }}`},
	} {
		env := New()
		tmpl, err := env.FromString(tc.src)
		if err != nil {
			t.Errorf("%s: compile: %v", tc.name, err)
			continue
		}
		_, err = tmpl.RenderString(context.Background(), nil)
		if err == nil {
			t.Errorf("%s: rendered, want a refusal", tc.name)
			continue
		}
		if !strings.Contains(err.Error(), "bit limit") {
			t.Errorf("%s: got %v, want a bit-limit refusal", tc.name, err)
		}
	}
}

// The ceiling is a property of the engine, not of the caller's budget: a
// caller who turned the budget off asked for no accounting, not for the
// process to be allowed to allocate until it dies.
func TestIntegerWidthBoundSurvivesWithoutLimits(t *testing.T) {
	env := New(WithoutLimits())
	tmpl, err := env.FromString(`{% set x = 2 ** 524288 %}{{ (x * x)|string|length }}`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if _, err := tmpl.RenderString(context.Background(), nil); err == nil ||
		!strings.Contains(err.Error(), "bit limit") {
		t.Errorf("got %v, want a bit-limit refusal", err)
	}
}

// Everything below the ceiling keeps answering exactly. The bound exists to
// stop a template building an integer the machine cannot hold; it must not
// cost arbitrary precision at ordinary sizes, which is the whole reason
// integers are arbitrary precision here.
func TestIntegerWidthBoundLeavesOrdinaryArithmeticExact(t *testing.T) {
	env := New()
	for _, tc := range []struct{ src, want string }{
		{`{{ 2 ** 100 }}`, "1267650600228229401496703205376"},
		{`{{ (2 ** 100) * (2 ** 100) }}`, "1606938044258990275541962092341162602522202993782792835301376"},
		{`{{ 9223372036854775807 + 1 }}`, "9223372036854775808"},
		{`{{ -9223372036854775808 - 1 }}`, "-9223372036854775809"},
		{`{{ (2 ** 64) - (2 ** 64) }}`, "0"},
		{`{{ ((2 ** 300) * (2 ** 300))|string|length }}`, "181"},
	} {
		tmpl, err := env.FromString(tc.src)
		if err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		got, err := tmpl.RenderString(context.Background(), nil)
		if err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s = %s, want %s", tc.src, got, tc.want)
		}
	}
}

// The limit is one number, so a template cannot reach a width through one
// operator that another refuses.
func TestIntegerWidthBoundIsOneNumber(t *testing.T) {
	if value.MaxIntBits != 1<<20 {
		t.Errorf("MaxIntBits = %d, want %d", value.MaxIntBits, 1<<20)
	}
}
