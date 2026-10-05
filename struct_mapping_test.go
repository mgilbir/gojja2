// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"errors"
	"testing"

	"github.com/mgilbir/gojja2/errs"
)

// A Go struct handed to a template answers as a mapping of its fields, and a
// missing one is a KeyError naming the key -- as the dict a JSON-shaped
// context would have been. The corpus cannot reach this: CPython has no struct,
// and every Mapping a template can build is a dict or a proxy over one, which
// answer through other arms. These are the arms `make ungraded` listed as
// reached by nothing, and a Go context is the way to them.
func TestStructContextIsAMappingWhoseMissAreKeyErrors(t *testing.T) {
	type S struct{ A int }
	env := mustEnv()
	for _, tc := range []struct {
		name, src, want string
	}{
		{"percent hit", `{{ '%(A)s' % s }}`, "1"},
		{"format hit", `{{ '{0[A]}'.format(s) }}`, "1"},
		{"percent miss", `{{ '%(zz)s' % s }}`, ""},
		{"format miss", `{{ '{0[zz]}'.format(s) }}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmpl, err := env.FromString(tc.src)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			out, err := tmpl.RenderString(t.Context(), map[string]any{"s": S{A: 1}})
			if tc.want != "" {
				if err != nil || out != tc.want {
					t.Fatalf("rendered %q, %v; want %q", out, err, tc.want)
				}
				return
			}
			if !errors.Is(err, errs.KeyError) || err.Error() != "'zz'" {
				t.Fatalf("err = %v; want KeyError 'zz'", err)
			}
		})
	}
}
