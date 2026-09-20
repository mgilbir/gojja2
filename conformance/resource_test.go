// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package conformance_test

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/mgilbir/gojja2"
	"github.com/mgilbir/gojja2/conformance"
)

// TestResourceError guards the gate that keeps gojja2's own safety bounds out
// of the grading. A budget error reaches it wrapped in the engine's error
// type, never bare, so matching on the bare sentinels alone would let every
// real one through and fill a soak run with divergences that are not.
func TestResourceError(t *testing.T) {
	for name, c := range map[string]struct {
		err  error
		want bool
	}{
		"iterations": {gojja2.ErrTooManyIterations, true},
		"output":     {gojja2.ErrOutputTooLarge, true},
		"deadline":   {context.DeadlineExceeded, true},
		"canceled":   {context.Canceled, true},
		"ordinary":   {errors.New("division by zero"), false},
		"none":       {nil, false},
	} {
		if got := conformance.ResourceError(c.err); got != c.want {
			t.Errorf("%s: ResourceError(%v) = %v, want %v", name, c.err, got, c.want)
		}
	}

	for name, tc := range map[string]struct {
		env *gojja2.Environment
		src string
	}{
		"iteration budget": {mustEnv(gojja2.WithMaxIterations(10)),
			`{% for i in range(1000) %}{% endfor %}`},
		"output budget": {mustEnv(gojja2.WithMaxOutputBytes(16)),
			`{% for i in range(1000) %}xxxxxxxx{% endfor %}`},
	} {
		tmpl, err := tc.env.FromString(tc.src)
		if err != nil {
			t.Fatalf("%s: compile: %v", name, err)
		}
		err = tmpl.Render(context.Background(), io.Discard, nil)
		if !conformance.ResourceError(err) {
			t.Errorf("%s: wrapped error not recognised: %v", name, err)
		}
	}

	// An ordinary template failure must still be graded.
	tmpl, err := mustEnv().FromString(`{{ 1/0 }}`)
	if err != nil {
		t.Fatal(err)
	}
	if err := tmpl.Render(context.Background(), io.Discard, nil); conformance.ResourceError(err) {
		t.Errorf("a division by zero must be graded, not discarded: %v", err)
	}
}
