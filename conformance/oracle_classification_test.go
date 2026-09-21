// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package conformance_test

import (
	"errors"
	"testing"

	"github.com/mgilbir/gojja2/conformance"
)

// The oracle must grade what CPython decided and discard only what this
// machine decided, and OverflowError is the first kind.
//
// "Python int too large to convert to C ssize_t" is what CPython says about an
// argument, on any machine and every time. The server listed it beside
// MemoryError as a resource failure, so Comparable() rejected it and the
// fuzzer and soak threw every such case away without grading it. The batch
// tool that writes goldens never classified anything, so it recorded the same
// exception as the expected answer and graded it -- two committed goldens do.
// One exception meant the specification on one path and noise on the other.
//
// What that cost was measurable: a 36-case sweep of integer arguments reported
// 2 divergences with OverflowError listed and 23 without. Five of the hidden
// ones were real, and they had survived every soak and fuzz run to date
// because the instrument could not see them.
func TestOracleGradesWhatCPythonDecided(t *testing.T) {
	o, err := conformance.StartOracle()
	if err != nil {
		if errors.Is(err, conformance.ErrNoOracle) {
			t.Skip(err)
		}
		t.Fatalf("oracle: %v", err)
	}
	defer func() { _ = o.Close() }()

	for _, tc := range []struct {
		name string
		src  string
		// resource is whether the oracle should refuse to grade it.
		resource bool
		wantType string
	}{
		// Deterministic: CPython's answer about the argument.
		{"an integer past Py_ssize_t", `{{ "ab"|center(9223372036854775808) }}`,
			false, "OverflowError"},
		// sorted's reverse stopped being an integer in 3.12 -- Argument
		// Clinic now tests it for truth -- so a value past a C int is
		// simply true there. The case that probed it is gone rather
		// than reworded: nothing else reaches that conversion, and a
		// probe for a refusal CPython no longer makes proves nothing.
		{"a repetition count past an index", `{{ "a" * 1180591620717411303424 }}`,
			false, "OverflowError"},
		// This machine's answer: the allocation is what failed, and how much
		// room there was is not a fact about the language.
		{"an allocation past the address space", `{{ "ab"|center(2147483648) }}`,
			true, "MemoryError"},
	} {
		res, err := o.Render(conformance.OracleRequest{Name: tc.name, Source: tc.src})
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if res.OK {
			t.Errorf("%s: rendered %q, want %s", tc.name, res.Output, tc.wantType)
			continue
		}
		if res.Error.Type != tc.wantType {
			t.Errorf("%s: CPython raised %s, want %s -- the case no longer probes what it says",
				tc.name, res.Error.Type, tc.wantType)
			continue
		}
		if res.Resource != tc.resource {
			t.Errorf("%s: %s classified resource=%v, want %v",
				tc.name, tc.wantType, res.Resource, tc.resource)
		}
		if got := conformance.Comparable(res); got != !tc.resource {
			t.Errorf("%s: Comparable = %v, want %v", tc.name, got, !tc.resource)
		}
	}
}
