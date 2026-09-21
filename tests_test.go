// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"context"
	"strings"
	"testing"

	"github.com/mgilbir/gojja2/errs"
)

// TestIsFilterAndIsTestHashTheirValue: jinja2's `is filter` and `is test` are
// `value in env.filters` and `value in env.tests`, and a dict membership test
// hashes the value before anything looks at whether it could be a name.
//
// So an unhashable value raises rather than answering: `{{ [1] is filter }}` is
// "cannot use 'list' as a dict key (unhashable type: 'list')". gojja2 answered False for everything that was not
// a string, which made a question nothing could answer look answered.
func TestIsFilterAndIsTestHashTheirValue(t *testing.T) {
	env := mustNew()
	for _, tc := range []struct{ src, want string }{
		{`{{ "upper" is filter }}|{{ "bogus" is filter }}`, "True|False"},
		// A name can be one and not the other.
		{`{{ "odd" is test }}|{{ "odd" is filter }}`, "True|False"},
		// Hashable but no name can equal it: still False, as `in` says.
		{`{{ 1 is filter }}|{{ none is test }}|{{ true is filter }}`, "False|False|False"},
		{`{{ (1,2) is filter }}|{{ "" is test }}`, "False|False"},
		{`{{ nope is filter }}`, "False"},
	} {
		tmpl, err := env.FromString(tc.src)
		if err != nil {
			t.Fatalf("compile %q: %v", tc.src, err)
		}
		got, err := tmpl.RenderString(context.Background(), nil)
		if err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s = %q, want %q", tc.src, got, tc.want)
		}
	}

	for _, tc := range []struct{ src, want string }{
		{`{{ [1] is filter }}`, wantUnhashable("list", "list", asDictKey)},
		{`{{ [] is test }}`, wantUnhashable("list", "list", asDictKey)},
		{`{{ {} is filter }}`, wantUnhashable("dict", "dict", asDictKey)},
		// A tuple is hashable only when everything in it is, which is
		// the same rule |attr's name goes through.
		{`{{ (1,[2]) is test }}`, wantUnhashable("tuple", "list", asDictKey)},
	} {
		tmpl, err := env.FromString(tc.src)
		if err != nil {
			t.Fatalf("compile %q: %v", tc.src, err)
		}
		_, err = tmpl.RenderString(context.Background(), nil)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want %q", tc.src, err, tc.want)
			continue
		}
		if kind := errs.KindOf(err); kind != errs.TypeError {
			t.Errorf("%s: got %v, want TypeError", tc.src, kind)
		}
	}
}

// TestDivisiblebyComparesAgainstZero: jinja2 writes `value % num == 0`, which
// is a comparison and not a truth test. The two part company wherever % is not
// division: on a string it is *formatting*, so `{{ "" is divisibleby([]) }}` is
// `"" == 0` -- False -- where reading the empty result as falsey answered True.
//
// TestSameAsEmptyTuple covers the one container CPython shares.
func TestDivisiblebyComparesAgainstZero(t *testing.T) {
	env := mustNew()
	for _, tc := range []struct{ src, want string }{
		// % on a string is formatting, and no format produces "".
		{`{{ "" is divisibleby([]) }}`, "False"},
		{`{{ "" is divisibleby({}) }}`, "False"},
		{`{{ "" is divisibleby([1]) }}`, "False"},
		// A format that does produce something is no more zero.
		{`{{ "a%s" is divisibleby([1]) }}`, "False"},
		// And on numbers it is still division.
		{`{{ 4 is divisibleby(2) }}|{{ 5 is divisibleby(2) }}`, "True|False"},
		{`{{ 4.0 is divisibleby(2) }}|{{ 0 is divisibleby(3) }}`, "True|True"},
	} {
		tmpl, err := env.FromString(tc.src)
		if err != nil {
			t.Fatalf("compile %q: %v", tc.src, err)
		}
		got, err := tmpl.RenderString(context.Background(), nil)
		if err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s\n got %q\nwant %q", tc.src, got, tc.want)
		}
	}
}

// TestSameAsEmptyTuple: CPython shares one empty tuple, so `() is ()` is true
// where every other pair of separately built containers is not.
func TestSameAsEmptyTuple(t *testing.T) {
	env := mustNew()
	for _, tc := range []struct{ src, want string }{
		{`{{ () is sameas(()) }}`, "True"},
		{`{{ (1,) is sameas((1,)) }}`, "False"},
		{`{{ [] is sameas([]) }}`, "False"},
		{`{{ {} is sameas({}) }}`, "False"},
		{`{{ () is sameas((1,)) }}|{{ (1,) is sameas(()) }}`, "False|False"},
		{`{{ () is sameas([]) }}`, "False"},
	} {
		tmpl, err := env.FromString(tc.src)
		if err != nil {
			t.Fatalf("compile %q: %v", tc.src, err)
		}
		got, err := tmpl.RenderString(context.Background(), nil)
		if err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s\n got %q\nwant %q", tc.src, got, tc.want)
		}
	}
}
