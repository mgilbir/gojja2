// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"context"
	"errors"
	"testing"
)

// A range holds its bounds exactly, however large they are.
//
// They were narrowed to an int64 when the range was built, so `range(2**70)`
// was refused outright -- with "'int' object cannot be interpreted as an
// integer", which is the wrong complaint as well as the wrong answer. CPython
// builds the range happily: it holds Python ints, and a template can observe
// them exactly even though the range is far too long to walk. It can print it,
// decide membership by arithmetic, index it, slice it, and ask for the first
// element, none of which needs the length to be reachable.
//
// The length has always been arbitrary precision -- `range(-2**63, 2**63-1)`
// holds 2**64-1 elements -- so it was only the bounds that were narrow.
//
// Every expectation below is CPython's own answer, taken from the oracle.
func TestRangeBoundsAreExact(t *testing.T) {
	for _, tc := range []struct{ src, want, wantErr string }{
		{`{{ range(1180591620717411303424) }}`, "range(0, 1180591620717411303424)", ""},
		{`{{ range(2,1180591620717411303424,3) }}`, "range(2, 1180591620717411303424, 3)", ""},
		{`{{ range(1180591620717411303424,1180591620717411303424) }}`, "range(1180591620717411303424, 1180591620717411303424)", ""},
		{`{{ range(-1180591620717411303424,0) }}`, "range(-1180591620717411303424, 0)", ""},
		{`{{ range(1180591620717411303424,0,-1) }}`, "range(1180591620717411303424, 0, -1)", ""},
		{`{{ range(1180591620717411303424)|first }}`, "0", ""},
		{`{{ range(2,1180591620717411303424,3)|first }}`, "2", ""},
		{`{{ range(1180591620717411303424,1180591620717411303424+10)|first }}`, "1180591620717411303424", ""},
		{`{{ 3 in range(1180591620717411303424) }}`, "True", ""},
		{`{{ 1180591620717411303424 in range(1180591620717411303424) }}`, "False", ""},
		{`{{ 1180591620717411303424 in range(1180591620717411303424+1) }}`, "True", ""},
		{`{{ -1 in range(1180591620717411303424) }}`, "False", ""},
		{`{{ range(1180591620717411303424)|length }}`, "", "Python int too large to convert to C ssize_t"},
		{`{{ range(1180591620717411303424)[0] }}`, "0", ""},
		{`{{ range(1180591620717411303424)[5] }}`, "5", ""},
		{`{{ range(1180591620717411303424)[1:3] }}`, "range(1, 3)", ""},
		{`{{ range(1180591620717411303424).start }}|{{ range(1180591620717411303424).stop }}|{{ range(1180591620717411303424).step }}`, "0|1180591620717411303424|1", ""},
		{`{{ range(1180591620717411303424) == range(1180591620717411303424) }}`, "True", ""},
		{`{{ range(1180591620717411303424) == range(3) }}`, "False", ""},
		{`{{ range(1180591620717411303424,1180591620717411303424+3)|list }}`, "[1180591620717411303424, 1180591620717411303425, 1180591620717411303426]", ""},
		{`{{ range(0,1180591620717411303424,1180591620717411303424) }}`, "range(0, 1180591620717411303424, 1180591620717411303424)", ""},
		{`{{ range(1.5) }}`, "", "'float' object cannot be interpreted as an integer"},
		{`{{ range(1180591620717411303424,1,0) }}`, "", "range() arg 3 must not be zero"},
	} {
		tmpl, err := New().FromString(tc.src)
		if err != nil {
			t.Errorf("%s: compile: %v", tc.src, err)
			continue
		}
		got, err := tmpl.RenderString(context.Background(), nil)
		if tc.wantErr != "" {
			if err == nil || err.Error() != tc.wantErr {
				t.Errorf("%s:\n  got  %q %v\n  want error %q", tc.src, got, err, tc.wantErr)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("%s = %q, %v; want %q", tc.src, got, err, tc.want)
		}
	}
}

// A wide range is still bounded when walked: the budget stops the loop, rather
// than the range refusing to exist. That is the whole point of keeping the
// bounds exact -- the refusal belongs to the walk, not to the construction.
func TestWideRangeIsWalkedUnderTheBudget(t *testing.T) {
	env := New(WithMaxIterations(1000))
	tmpl, err := env.FromString(`{% for i in range(1180591620717411303424) %}x{% endfor %}`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if _, err := tmpl.RenderString(context.Background(), nil); err == nil {
		t.Error("rendered, want the iteration budget to stop it")
	} else if !errorsIsTooManyIterations(err) {
		t.Errorf("got %v, want ErrTooManyIterations", err)
	}
}

func errorsIsTooManyIterations(err error) bool {
	return errors.Is(err, ErrTooManyIterations)
}
