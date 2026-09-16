// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/mgilbir/gojja2/errs"
)

// render is a bare render with no context and default limits.
func render(t *testing.T, source string) (string, error) {
	t.Helper()
	tmpl, err := New().FromString(source)
	if err != nil {
		return "", err
	}
	return tmpl.RenderString(context.Background(), nil)
}

// mustRender fails the test if the render does not succeed.
func mustRender(t *testing.T, source string) string {
	t.Helper()
	out, err := render(t, source)
	if err != nil {
		t.Fatalf("render %q: %v", source, err)
	}
	return out
}

// TestBlockReferenceErrorIsNotSwallowed pins that an error raised while
// rendering a block through `self` reaches the caller.
//
// Str() cannot return an error, so the failure used to be discarded: the block
// rendered as "" and the render reported success. The block is parked inside a
// false branch so that only the `self` reference renders it.
func TestBlockReferenceErrorIsNotSwallowed(t *testing.T) {
	const src = `[{{ self.b }}]{% if false %}{% block b %}{{ 1/0 }}{% endblock %}{% endif %}`
	out, err := render(t, src)
	if err == nil {
		t.Fatalf("expected the block's error to surface, got out=%q err=nil", out)
	}
	if kind := errs.KindOf(err); kind != errs.ZeroDivisionError {
		t.Errorf("expected ZeroDivisionError, got %v (%v)", kind, err)
	}
	if out != "" {
		t.Errorf("a failed render must not return partial output, got %q", out)
	}
}

// TestBlockReferenceErrorThroughFilter pins that the deferred error is caught
// however the block was stringified, not only by a print tag. A filter calls
// value.Str itself, well away from the print path.
func TestBlockReferenceErrorThroughFilter(t *testing.T) {
	const src = `{{ self.b|upper }}{% if false %}{% block b %}{{ 1/0 }}{% endblock %}{% endif %}`
	if _, err := render(t, src); err == nil {
		t.Fatal("expected the block's error to surface through a filter, got nil")
	}
}

// TestBlockReferenceStillRenders guards the fix against over-reach: a block
// reached through self that does NOT fail must still render normally.
func TestBlockReferenceStillRenders(t *testing.T) {
	if got := mustRender(t, `[{{ self.b }}]{% block b %}hi{% endblock %}`); got != "[hi]hi" {
		t.Errorf("got %q, want %q", got, "[hi]hi")
	}
	if got := mustRender(t, `[{{ self.b() }}]{% block b %}hi{% endblock %}`); got != "[hi]hi" {
		t.Errorf("got %q, want %q", got, "[hi]hi")
	}
}

// TestRangeLengthDoesNotOverflow pins the exact element count for ranges whose
// length does not fit in an int64. The int64 form of CPython's formula wrapped
// negative, which rendered a length of -1 and ran the loop zero times.
func TestRangeLengthDoesNotOverflow(t *testing.T) {
	cases := []struct{ expr, want string }{
		{`range(0, 9223372036854775807)|length`, "9223372036854775807"},
		{`range(10)|length`, "10"},
		{`range(0)|length`, "0"},
		{`range(10, 0, -1)|length`, "10"},
		{`range(0, 10, -1)|length`, "0"},
		{`range(0, 10, 3)|length`, "4"},
		{`range(0, 10, 2)|length`, "5"},
		{`range(-9223372036854775808, -9223372036854775807)|length`, "1"},
	}
	for _, tc := range cases {
		got := mustRender(t, "{{ "+tc.expr+" }}")
		if got != tc.want {
			t.Errorf("%s = %s, want %s", tc.expr, got, tc.want)
		}
	}

	// Past Py_ssize_t, CPython's len() raises rather than returning a
	// bignum, so len() must raise here too. It used to wrap to -1.
	tooLong := []string{
		`range(-9223372036854775808, 9223372036854775807)|length`,
		`range(-9223372036854775808, 0)|length`,
		`range(9223372036854775807, -9223372036854775808, -1)|length`,
	}
	for _, expr := range tooLong {
		out, err := render(t, "{{ "+expr+" }}")
		if err == nil {
			t.Errorf("%s = %q, want an OverflowError", expr, out)
			continue
		}
		if kind := errs.KindOf(err); kind != errs.OverflowError {
			t.Errorf("%s: got %v (%v), want OverflowError", expr, kind, err)
		}
		if !strings.Contains(err.Error(), "too large to convert to C ssize_t") {
			t.Errorf("%s: got %q, want CPython's ssize_t wording", expr, err)
		}
	}
}

// TestRangeLengthNeverNegative pins the invariant the overflow broke. A
// negative length reaches make() in anything that sizes a slice from it.
func TestRangeLengthNeverNegative(t *testing.T) {
	bounds := []string{"-9223372036854775808", "-1", "0", "1", "9223372036854775807"}
	steps := []string{"-9223372036854775808", "-3", "-1", "1", "3", "9223372036854775807"}
	for _, start := range bounds {
		for _, stop := range bounds {
			for _, step := range steps {
				r := &rangeObject{}
				r.start = mustParseInt(t, start)
				r.stop = mustParseInt(t, stop)
				r.step = mustParseInt(t, step)
				if n := r.Len(); n < 0 {
					t.Errorf("range(%s, %s, %s).Len() = %d, must never be negative",
						start, stop, step, n)
				}
				if b := r.BigLen(); b.Sign() < 0 {
					t.Errorf("range(%s, %s, %s).BigLen() = %s, must never be negative",
						start, stop, step, b)
				}
			}
		}
	}
}

// TestFillCharMustBeOneCharacter pins CPython's two refusals. A multi-character
// fill used to be accepted, so center(10) could return nineteen characters.
func TestFillCharMustBeOneCharacter(t *testing.T) {
	cases := []struct{ src, want string }{
		{`{{ "a".center(10, "ab") }}`, "The fill character must be exactly one character long"},
		{`{{ "a".ljust(10, "ab") }}`, "The fill character must be exactly one character long"},
		{`{{ "a".rjust(10, "") }}`, "The fill character must be exactly one character long"},
		{`{{ "a".center(10, 5) }}`, "The fill character must be a unicode character, not int"},
		{`{{ "a".ljust(10, []) }}`, "The fill character must be a unicode character, not list"},
	}
	for _, tc := range cases {
		_, err := render(t, tc.src)
		if err == nil {
			t.Errorf("%s: expected an error, got none", tc.src)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s:\n got %q\nwant it to contain %q", tc.src, err.Error(), tc.want)
		}
	}
}

// TestFillCharAcceptsExactlyOne guards against over-reach, including a
// multi-byte character, which is one character and not one byte.
func TestFillCharAcceptsExactlyOne(t *testing.T) {
	cases := []struct{ src, want string }{
		{`{{ "a".center(5, "-") }}`, "--a--"},
		{`{{ "a".ljust(3, "x") }}`, "axx"},
		{`{{ "a".rjust(3, "x") }}`, "xxa"},
		{`{{ "a".center(5, "é") }}`, "ééaéé"},
		{`{{ "a".center(5) }}`, "  a  "},
		{`{{ "a".center(10, none) }}`, "    a     "},
	}
	for _, tc := range cases {
		if got := mustRender(t, tc.src); got != tc.want {
			t.Errorf("%s = %q, want %q", tc.src, got, tc.want)
		}
	}
}

// TestDictUpdateAcceptsEverythingDictDoes pins that dict.update and dict()
// accept the same shapes and refuse them identically. update() used to test
// only for a mapping and silently discard anything else, so building a dict
// from a list of pairs produced an empty dict and reported success.
func TestDictUpdateAcceptsEverythingDictDoes(t *testing.T) {
	ok := []struct{ src, want string }{
		{`{% set d = {} %}{{ d.update([("a", 1)]) }}{{ d }}`, "None{'a': 1}"},
		{`{% set d = {} %}{{ d.update({"a": 1}) }}{{ d }}`, "None{'a': 1}"},
		{`{% set d = {} %}{{ d.update(["ab", "cd"]) }}{{ d }}`, "None{'a': 'b', 'c': 'd'}"},
		{`{% set d = {"a": 0} %}{{ d.update(a=9) }}{{ d }}`, "None{'a': 9}"},
		{`{{ dict(["ab", "cd"]) }}`, "{'a': 'b', 'c': 'd'}"},
		{`{{ dict([("a", 1)]) }}`, "{'a': 1}"},
	}
	for _, tc := range ok {
		if got := mustRender(t, tc.src); got != tc.want {
			t.Errorf("%s = %q, want %q", tc.src, got, tc.want)
		}
	}

	// Both spellings must refuse the same input with the same words.
	bad := []struct{ arg, want string }{
		{`5`, "'int' object is not iterable"},
		{`"ab"`, "dictionary update sequence element #0 has length 1; 2 is required"},
		{`[5]`, "cannot convert dictionary update sequence element #0 to a sequence"},
		{`[(1, 2, 3)]`, "dictionary update sequence element #0 has length 3; 2 is required"},
		{`[("a",)]`, "dictionary update sequence element #0 has length 1; 2 is required"},
	}
	for _, tc := range bad {
		_, updateErr := render(t, `{% set d = {} %}{{ d.update(`+tc.arg+`) }}`)
		_, dictErr := render(t, `{{ dict(`+tc.arg+`) }}`)
		if updateErr == nil {
			t.Errorf("d.update(%s): expected an error, got none", tc.arg)
			continue
		}
		if !strings.Contains(updateErr.Error(), tc.want) {
			t.Errorf("d.update(%s):\n got %q\nwant it to contain %q", tc.arg, updateErr, tc.want)
		}
		if dictErr == nil || dictErr.Error() != updateErr.Error() {
			t.Errorf("dict(%s) and d.update(%s) must fail identically:\n dict:   %v\n update: %v",
				tc.arg, tc.arg, dictErr, updateErr)
		}
	}
}

// mustParseInt keeps the range table readable; the bounds are written as they
// appear in a template.
func mustParseInt(t *testing.T, s string) int64 {
	t.Helper()
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return n
}
