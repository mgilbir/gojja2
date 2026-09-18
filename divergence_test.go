// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"context"
	"math/big"
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

// TestBlockReferencePrintsAsAnObject: jinja2's BlockReference defines no
// __str__ and no __repr__, so printing one prints the object. gojja2 rendered
// the block instead, which made `{{ self.body }}` produce the block's output
// where jinja2 produces "<jinja2.runtime.BlockReference object at 0x...>" --
// and made `{% block x %}{{ self.x }}{% endblock %}` a RecursionError where
// jinja2 prints one line. Only a call renders.
func TestBlockReferencePrintsAsAnObject(t *testing.T) {
	out := mustRender(t, `[{{ self.b }}]{% block b %}hi{% endblock %}`)
	const want = "<jinja2.runtime.BlockReference object at 0x"
	if !strings.HasPrefix(out, "["+want) || !strings.HasSuffix(out, ">]hi") {
		t.Errorf("got %q, want [%s...>]hi", out, want)
	}
	// Printing it does not render it, so a block that prints itself
	// terminates.
	if got := mustRender(t, `{% block x %}[{{ self.x }}]{% endblock %}`); !strings.HasPrefix(got, "[<") {
		t.Errorf("a self-printing block did not terminate cleanly: %q", got)
	}
}

// TestBlockReferenceErrorIsNotSwallowed pins that an error raised while
// rendering a block through `self` reaches the caller. The block is parked
// inside a false branch so that only the `self` reference renders it.
func TestBlockReferenceErrorIsNotSwallowed(t *testing.T) {
	const src = `[{{ self.b() }}]{% if false %}{% block b %}{{ 1/0 }}{% endblock %}{% endif %}`
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

// TestBlockReferenceErrorThroughFilter pins the same through a filter, which
// calls value.Str itself, well away from the print path.
func TestBlockReferenceErrorThroughFilter(t *testing.T) {
	const src = `{{ self.b()|upper }}{% if false %}{% block b %}{{ 1/0 }}{% endblock %}{% endif %}`
	if _, err := render(t, src); err == nil {
		t.Fatal("expected the block's error to surface through a filter, got nil")
	}
}

// TestBlockReferenceStillRenders guards the fix against over-reach: a block
// reached through self that does NOT fail must still render normally.
func TestBlockReferenceStillRenders(t *testing.T) {
	if got := mustRender(t, `[{{ self.b() }}]{% block b %}hi{% endblock %}`); got != "[hi]hi" {
		t.Errorf("got %q, want %q", got, "[hi]hi")
	}
	// And `super` is a property on the reference, which is what
	// `{{ self.body.super() }}` reaches -- undefined past the end of the
	// chain, saying so only when it is used.
	if got := mustRender(t, `{% block b %}hi{% endblock %}[{{ self.b.super }}]`); got != "hi[]" {
		t.Errorf("got %q, want %q", got, "hi[]")
	}
	if _, err := render(t, `{% block b %}hi{% endblock %}{{ self.b.super() }}`); err == nil ||
		err.Error() != "there is no parent block called 'b'." {
		t.Errorf("super past the end of the chain: got %v", err)
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
	// The wide bounds are in here too: a range whose bounds do not fit an
	// int64 takes the other representation, and the invariant is the same
	// one. Construction goes through newRange because that is what computes
	// the length -- reaching past it and assigning the fields left the
	// length nil, which is its own kind of wrong answer.
	bounds := []string{
		"-1180591620717411303424", "-9223372036854775808", "-1", "0", "1",
		"9223372036854775807", "1180591620717411303424",
	}
	steps := []string{
		"-1180591620717411303424", "-9223372036854775808", "-3", "-1", "1", "3",
		"9223372036854775807", "1180591620717411303424",
	}
	for _, start := range bounds {
		for _, stop := range bounds {
			for _, step := range steps {
				r := newRange(
					mustParseBig(t, start),
					mustParseBig(t, stop),
					mustParseBig(t, step),
				)
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

// mustParseBig reads an exact bound, however wide.
func mustParseBig(t *testing.T, s string) *big.Int {
	t.Helper()
	b, ok := new(big.Int).SetString(s, 10)
	if !ok {
		t.Fatalf("bad bound %q", s)
	}
	return b
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
	}
	for _, tc := range cases {
		if got := mustRender(t, tc.src); got != tc.want {
			t.Errorf("%s = %q, want %q", tc.src, got, tc.want)
		}
	}
	// An explicit None is not the default. Only an argument that was not
	// written is; a None reaches the check like any other value, and the
	// check is hand-written in CPython, so it names the type plainly.
	if _, err := render(t, `{{ "a".center(10, none) }}`); err == nil ||
		err.Error() != "The fill character must be a unicode character, not NoneType" {
		t.Errorf("center(10, none): got %v", err)
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
// TestRepeatCountIndexOverflow: sequence repetition asks its count for
// __index__ before it repeats anything, so a count outside Py_ssize_t raises
// an OverflowError there -- whatever the count's sign, and however short the
// sequence is. gojja2 answered the "can't multiply sequence by non-int"
// TypeError instead, because a bignum arrives as an int whose Int64 does not
// fit and so was taken for a non-integer.
func TestRepeatCountIndexOverflow(t *testing.T) {
	const want = "cannot fit 'int' into an index-sized integer"
	for _, expr := range []string{
		`["-"] * 10000000000000000000000`,
		`10000000000000000000000 * ["-"]`,
		`"-" * 10000000000000000000000`,
		`(1, 2) * 10000000000000000000000`,
		// Markup multiplies through __index__ too, and overflows there
		// rather than reporting the count as uninterpretable.
		`"x"|safe * 10000000000000000000000`,
		// A negative count would have produced an empty sequence had it
		// fit; __index__ refuses before the sign is ever consulted.
		`["-"] * -10000000000000000000000`,
		// One past int64 is already past the index.
		`["-"] * 9223372036854775808`,
	} {
		out, err := render(t, "{{ "+expr+" }}")
		if err == nil {
			t.Errorf("%s = %q, want an OverflowError", expr, out)
			continue
		}
		if kind := errs.KindOf(err); kind != errs.OverflowError {
			t.Errorf("%s: got %v (%v), want OverflowError", expr, kind, err)
		}
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %q, want %q", expr, err, want)
		}
	}

	// The overflow must not swallow the case it sits next to: a count of
	// the wrong *type* still gets sequence.__mul__'s own message, and a
	// count that fits is still simply repeated.
	for _, tc := range []struct{ expr, want string }{
		{`["-"] * "z"`, "can't multiply sequence by non-int of type 'str'"},
		{`["-"] * 2.5`, "can't multiply sequence by non-int of type 'float'"},
		{`["-"] * none`, "can't multiply sequence by non-int of type 'NoneType'"},
	} {
		_, err := render(t, "{{ "+tc.expr+" }}")
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want %q", tc.expr, err, tc.want)
		}
	}
	if got := mustRender(t, `{{ ["-"] * 3 }}`); got != "['-', '-', '-']" {
		t.Errorf(`["-"] * 3 = %s`, got)
	}
	if got := mustRender(t, `{{ ["-"] * -3 }}`); got != "[]" {
		t.Errorf(`["-"] * -3 = %s`, got)
	}
}
