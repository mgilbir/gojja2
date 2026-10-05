// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"testing"

	"github.com/mgilbir/gojja2"
	"github.com/mgilbir/gojja2/value"
)

// Environment.PythonVersion and Environment.Policies are how a host program
// reads back what it configured, and whole-suite coverage put both at 0%: the
// engine consults the fields directly, so nothing exercised the accessors. A
// caller would have been the first to find out.

func TestEnvironmentPythonVersionRoundTrips(t *testing.T) {
	env, err := gojja2.New()
	if err != nil {
		t.Fatal(err)
	}
	// The default is the pinned interpreter, which is what the committed
	// goldens are generated from.
	if got := env.PythonVersion(); got != value.DefaultPythonVersion {
		t.Errorf("PythonVersion() = %v on a fresh environment, want the pin %v",
			got, value.DefaultPythonVersion)
	}
	for _, want := range []value.PythonVersion{
		value.Python311, value.Python312, value.Python313, value.Python314,
	} {
		env, err := gojja2.New(gojja2.WithPythonVersion(want))
		if err != nil {
			t.Fatalf("WithPythonVersion(%v): %v", want, err)
		}
		if got := env.PythonVersion(); got != want {
			t.Errorf("PythonVersion() = %v, want %v", got, want)
		}
	}
}

func TestEnvironmentPoliciesRoundTrip(t *testing.T) {
	env, err := gojja2.New()
	if err != nil {
		t.Fatal(err)
	}
	// jinja2's own defaults, which is what the filters are graded against.
	def := env.Policies()
	if def.URLizeRel != "noopener" {
		t.Errorf("default URLizeRel = %q, want %q", def.URLizeRel, "noopener")
	}
	if def.TruncateLeeway != 5 {
		t.Errorf("default TruncateLeeway = %d, want 5", def.TruncateLeeway)
	}
	if def.URLizeTarget != "" {
		t.Errorf("default URLizeTarget = %q, want empty", def.URLizeTarget)
	}

	want := gojja2.Policies{URLizeRel: "nofollow", URLizeTarget: "_blank", TruncateLeeway: 0}
	env, err = gojja2.New(gojja2.WithPolicies(want))
	if err != nil {
		t.Fatal(err)
	}
	if got := env.Policies(); got != want {
		t.Errorf("Policies() = %+v, want %+v", got, want)
	}
	// And the policies are the ones the filters read, not just the ones the
	// accessor reports: a leeway of zero makes |truncate shorten a string it
	// would otherwise leave alone.
	tmpl, err := env.FromString(`{{ "abcdefghijkl"|truncate(9) }}`)
	if err != nil {
		t.Fatal(err)
	}
	out, err := tmpl.RenderString(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if out != "abcdef..." {
		t.Errorf("truncate under a zero leeway = %q, want %q", out, "abcdef...")
	}
}
