// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"context"
	"strings"
	"testing"

	"github.com/mgilbir/gojja2/value"
)

// The ceiling on a computed integer's width is the caller's to set.
//
// It is the one bound here that is also a conformance question -- CPython
// computes what the default refuses -- so a caller who wants CPython's
// arithmetic back can have it, and one who wants a tighter floor can have that
// instead.
func TestWithMaxIntBits(t *testing.T) {
	// 2**524288 is the widest power of two `**` will build under the
	// default; squaring it is two bits over.
	const square = `{% set x = 2 ** 524288 %}{{ (x * x)|string|length }}`
	const modest = `{{ (2 ** 2000) > 0 }}`

	for _, tc := range []struct {
		name    string
		opts    []Option
		src     string
		want    string
		wantErr string
	}{
		{"default refuses", nil, square, "", "over the 1048576 bit limit"},
		{"zero restores the default", []Option{WithMaxIntBits(0)}, square, "", "over the 1048576 bit limit"},
		{"raised allows it", []Option{WithMaxIntBits(4 << 20)}, square, "315653", ""},
		{"negative removes it", []Option{WithMaxIntBits(-1)}, square, "315653", ""},
		{"lowered refuses earlier", []Option{WithMaxIntBits(1024)}, modest, "", "over the 1024 bit limit"},
		{"modest is fine by default", nil, modest, "True", ""},
		// WithoutLimits is about the two *budgets*. This is a ceiling,
		// like the 2**31 caps it also leaves alone, so it survives.
		{"WithoutLimits keeps it", []Option{WithoutLimits()}, square, "", "over the 1048576 bit limit"},
	} {
		tmpl, err := mustNew(tc.opts...).FromString(tc.src)
		if err != nil {
			t.Errorf("%s: compile: %v", tc.name, err)
			continue
		}
		got, rerr := tmpl.RenderString(context.Background(), nil)
		if tc.wantErr != "" {
			if rerr == nil || !strings.Contains(rerr.Error(), tc.wantErr) {
				t.Errorf("%s: got %q %v, want an error containing %q",
					tc.name, got, rerr, tc.wantErr)
			}
			continue
		}
		if rerr != nil || got != tc.want {
			t.Errorf("%s = %q, %v; want %q", tc.name, got, rerr, tc.want)
		}
	}
}

// Removing the ceiling does not remove the floor. The width is charged against
// the output budget before it is allocated, so a squaring loop is still bounded
// by a budget the caller kept -- which is what makes "CPython's arithmetic, and
// still safe" a combination that exists.
func TestIntWidthIsStillChargedWhenTheCeilingIsGone(t *testing.T) {
	const squaringLoop = `{% set ns = namespace(x = 2 ** 500000) %}` +
		`{% for i in range(40) %}{% set ns.x = ns.x * ns.x %}{% endfor %}DONE`

	tmpl, err := mustNew(
		WithMaxIntBits(-1),
		WithMaxOutputBytes(1<<12),
		WithMaxIterations(1000),
	).FromString(squaringLoop)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if _, err := tmpl.RenderString(context.Background(), nil); err == nil {
		t.Error("the loop rendered; the output budget should have stopped it")
	}
}

// The default is the package's, and the engine and the value package agree on
// what it is.
func TestIntBitLimitDefaults(t *testing.T) {
	env := mustNew()
	if got := env.maxIntBits; got != value.MaxIntBits {
		t.Errorf("default maxIntBits = %d, want %d", got, value.MaxIntBits)
	}
	// A State with no budget -- which is how the value package sees one
	// before any render exists -- answers the package default.
	var s *State
	if got := s.IntBitLimit(); got != value.MaxIntBits {
		t.Errorf("nil State IntBitLimit = %d, want %d", got, value.MaxIntBits)
	}
}
