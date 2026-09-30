// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package conformance_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mgilbir/gojja2"
)

// A comparison that runs out of stack names itself, in every interpreter.
//
// 3.12 dropped "while calling a Python object" from the *call* path's message
// -- which is what value.PythonVersion.RecursionMessageFor exists for, and what
// errors/recursion_extends grades -- but a comparison kept its own suffix:
// 3.11, 3.12 and 3.13 all say "maximum recursion depth exceeded in comparison".
// gojja2 was unifying that one too, so the message was wrong on every
// interpreter it supports.
//
// The corpus cannot hold this: 3.14 says "Stack overflow (used 8156 kB) in
// comparison", and the number is the stack *this machine* had, so a golden
// would record one run and fail on the next -- the same reason
// recursion_repr_test.go exists. docs/divergences.md records the 3.14 wording;
// this pins the sentence everywhere else.
func TestComparisonRecursionNamesItself(t *testing.T) {
	const cyclic = `{% set l = [] %}{% set m = [] %}` +
		`{% do l.append(m) %}{% do m.append(l) %}`
	for _, tc := range []struct{ name, src string }{
		{"equal", cyclic + `{{ l == m }}`},
		{"not equal", cyclic + `{{ l != m }}`},
		{"less than", cyclic + `{{ l < m }}`},
		{"sorted", cyclic + `{{ [l, m]|sort }}`},
		{"min", cyclic + `{{ [l, m]|min }}`},
		{"in", cyclic + `{{ l in [m] }}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, err := gojja2.New(gojja2.WithExtensions("do"))
			if err != nil {
				t.Fatalf("new: %v", err)
			}
			tmpl, err := env.FromString(tc.src)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			var b strings.Builder
			err = tmpl.RenderValues(context.Background(), &b, nil)
			if err == nil {
				t.Fatalf("rendered %q; CPython raises RecursionError", b.String())
			}
			// CPython's own sentence, on every version but 3.14.
			const want = "maximum recursion depth exceeded in comparison"
			if got := err.Error(); got != want {
				t.Errorf("\n  got  %q\n  want %q", got, want)
			}
		})
	}
}
