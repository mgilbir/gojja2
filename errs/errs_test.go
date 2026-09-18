// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package errs_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/mgilbir/gojja2/errs"
)

// KindOf has to see through wrapping, because the errors it classifies come
// from callers as often as from this package.
//
// It was a bare type assertion, `err.(*Error)`, which is false for every error
// that has been wrapped even once. Everything downstream reads a Kind, and
// Kind.DerivesFrom answers false for the zero Kind whatever it is asked, so a
// wrapped error was silently classified as "not any of these" -- which is the
// answer that makes a fallback path give up.
func TestKindOfSeesThroughWrapping(t *testing.T) {
	inner := errs.New(errs.TemplateNotFound, "missing.html")
	for _, tc := range []struct {
		name string
		err  error
		want errs.Kind
	}{
		{"an *Error", inner, errs.TemplateNotFound},
		{"an *Error wrapped once", fmt.Errorf("loading: %w", inner), errs.TemplateNotFound},
		{"an *Error wrapped twice",
			fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", inner)), errs.TemplateNotFound},
		// The exported sentinel is a bare Kind, not an *Error. A custom
		// Loader returning gojja2.ErrNotFound -- the most obvious
		// reading of the documented contract -- hands over exactly this.
		{"a bare Kind", errs.TemplateNotFound, errs.TemplateNotFound},
		{"a wrapped Kind", fmt.Errorf("loading: %w", errs.TemplateNotFound), errs.TemplateNotFound},
		{"an unrelated error", errors.New("io"), errs.Kind(0)},
		{"nil", nil, errs.Kind(0)},
	} {
		if got := errs.KindOf(tc.err); got != tc.want {
			t.Errorf("%s: KindOf = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// A bare Kind used as an error answers errors.Is by the class hierarchy, the
// same way an *Error does. Without this, `except TemplateNotFound` catches a
// TemplatesNotFound carried by an *Error and misses the identical one carried
// by the sentinel.
func TestBareKindIsRespectsTheHierarchy(t *testing.T) {
	for _, tc := range []struct {
		err    error
		target errs.Kind
		want   bool
	}{
		{errs.TemplateNotFound, errs.TemplateNotFound, true},
		{errs.TemplatesNotFound, errs.TemplateNotFound, true},
		{errs.TemplateNotFound, errs.TemplatesNotFound, false},
		{errs.TemplateNotFound, errs.TemplateError, true},
		{errs.TypeError, errs.TemplateNotFound, false},
		{fmt.Errorf("x: %w", errs.TemplatesNotFound), errs.TemplateNotFound, true},
	} {
		if got := errors.Is(tc.err, tc.target); got != tc.want {
			t.Errorf("errors.Is(%v, %v) = %v, want %v", tc.err, tc.target, got, tc.want)
		}
	}
}
