// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"testing"

	"github.com/mgilbir/gojja2/errs"
)

// The operators CPython answers by running out of memory or time. No oracle
// can grade them -- the differential records those as resource errors, which
// is why `make ungraded` lists their messages as reached by no corpus case --
// so what gojja2 does instead is pinned here: an OverflowError, before
// anything is allocated, and still the answer where CPython has one without
// looking at the size.
func TestOperatorsBoundedWhereCPythonExhaustsTheMachine(t *testing.T) {
	env := mustEnv()
	for _, tc := range []struct {
		name, src, want, wantErr string
	}{
		{"exponent past int64", `{% set n = 2**70 %}{{ 7 ** n }}`, "", "exponent too large"},
		{"negative base too", `{% set n = 2**70 %}{{ (-7) ** n }}`, "", "exponent too large"},
		{"one needs no exponent", `{% set n = 2**70 %}{{ 1 ** n }}`, "1", ""},
		{"minus one reads the parity", `{% set n = 2**70 + 1 %}{{ (-1) ** n }}`, "-1", ""},
		{"string repeated past the ceiling", `{% set n = 2**62 %}{{ 'ab' * n }}`, "", "repeated string is too long"},
		{"sequence repeated past the ceiling", `{% set n = 2**62 %}{{ [1, 2] * n }}`, "", "repeated sequence is too long"},
		{"empty unit repeats freely", `{% set n = 2**62 %}[{{ '' * n }}][{{ [] * n }}]`, "[][[]]", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmpl, err := env.FromString(tc.src)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			out, err := tmpl.RenderString(t.Context(), nil)
			if tc.wantErr == "" {
				if err != nil || out != tc.want {
					t.Fatalf("rendered %q, %v; want %q", out, err, tc.want)
				}
				return
			}
			if errs.KindOf(err) != errs.OverflowError || err.Error() != tc.wantErr {
				t.Fatalf("err = %v (%v); want OverflowError %q", err, errs.KindOf(err), tc.wantErr)
			}
		})
	}
}
