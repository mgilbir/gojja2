// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"context"
	"strings"
	"testing"
)

// TestGlobalArity: the globals are ordinary Python callables, and a call to one
// is bound the way Python binds it. range is a C function that refuses keywords
// outright and words too few and too many differently; lipsum is a plain
// function; cycler and joiner are classes, so the call binds against __init__
// with self counted among the positional arguments -- which is why
// `joiner('-','x')` says three were given where two were written.
//
// gojja2 checked none of this. Extra arguments to joiner and cycler were
// dropped in silence, a keyword to any of them was ignored, and range reported
// one wording of its own for every wrong count.
func TestGlobalArity(t *testing.T) {
	env := New()
	for _, tc := range []struct{ src, want string }{
		// range: a keyword beats a wrong count, and a wrong count beats
		// an argument that is not an integer.
		{`{{ range() }}`, "range expected at least 1 argument, got 0"},
		{`{{ range(1,2,3,4) }}`, "range expected at most 3 arguments, got 4"},
		{`{{ range(1,2,3,4,5) }}`, "range expected at most 3 arguments, got 5"},
		{`{{ range(a=1) }}`, "range() takes no keyword arguments"},
		{`{{ range(1,a=2) }}`, "range() takes no keyword arguments"},
		{`{{ range(1,2,3,4,a=1) }}`, "range() takes no keyword arguments"},
		{`{{ range("x",1,2,3) }}`, "range expected at most 3 arguments, got 4"},
		{`{{ range("x") }}`, "'str' object cannot be interpreted as an integer"},

		// lipsum: an unexpected keyword beats a keyword a positional
		// already filled, which beats too many positionals.
		{`{{ lipsum(1,2,3,4,5) }}`,
			"generate_lorem_ipsum() takes from 0 to 4 positional arguments but 5 were given"},
		{`{{ lipsum(a=1) }}`, "generate_lorem_ipsum() got an unexpected keyword argument 'a'"},
		{`{{ lipsum(1,n=2) }}`, "generate_lorem_ipsum() got multiple values for argument 'n'"},
		{`{{ lipsum(1,2,3,4,5,a=1) }}`,
			"generate_lorem_ipsum() got an unexpected keyword argument 'a'"},
		{`{{ lipsum(1,2,3,4,5,n=6) }}`,
			"generate_lorem_ipsum() got multiple values for argument 'n'"},

		// cycler takes *items, so only a keyword is wrong -- and it is
		// wrong before the body's empty-cycle check runs.
		{`{{ cycler(a=1) }}`, "Cycler.__init__() got an unexpected keyword argument 'a'"},
		{`{{ cycler(1,a=2) }}`, "Cycler.__init__() got an unexpected keyword argument 'a'"},
		{`{{ cycler() }}`, "at least one item has to be provided"},
		{`{% set c = cycler(1,2) %}{{ c.next(1) }}`,
			"Cycler.next() takes 1 positional argument but 2 were given"},
		{`{% set c = cycler(1,2) %}{{ c.reset(1) }}`,
			"Cycler.reset() takes 1 positional argument but 2 were given"},
		{`{% set c = cycler(1,2) %}{{ c.next(a=1) }}`,
			"Cycler.next() got an unexpected keyword argument 'a'"},
		// And a Cycler is not callable at all.
		{`{% set c = cycler(1,2) %}{{ c() }}`, "'Cycler' object is not callable"},

		// joiner takes one separator, and self is counted.
		{`{{ joiner("-","x") }}`,
			"Joiner.__init__() takes from 1 to 2 positional arguments but 3 were given"},
		{`{{ joiner(1,2,3) }}`,
			"Joiner.__init__() takes from 1 to 2 positional arguments but 4 were given"},
		{`{{ joiner(a=1) }}`, "Joiner.__init__() got an unexpected keyword argument 'a'"},
		{`{{ joiner(1,sep=2) }}`, "Joiner.__init__() got multiple values for argument 'sep'"},
		{`{% set j = joiner("-") %}{{ j(1) }}`,
			"Joiner.__call__() takes 1 positional argument but 2 were given"},
		{`{% set j = joiner("-") %}{{ j(x=1) }}`,
			"Joiner.__call__() got an unexpected keyword argument 'x'"},
	} {
		tmpl, err := env.FromString(tc.src)
		if err != nil {
			t.Fatalf("compile %q: %v", tc.src, err)
		}
		out, err := tmpl.RenderString(context.Background(), nil)
		if err == nil {
			t.Errorf("%s: rendered %q, want %q", tc.src, out, tc.want)
			continue
		}
		if err.Error() != tc.want {
			t.Errorf("%s\n got %q\nwant %q", tc.src, err.Error(), tc.want)
		}
	}

	// The calls that are in fact correct still work, and a joiner hands
	// back the separator it was given rather than a string made of it.
	for _, tc := range []struct{ src, want string }{
		{`{% set j = joiner() %}{{ j() }}|{{ j() }}`, "|, "},
		{`{% set j = joiner("-") %}{{ j() }}|{{ j() }}`, "|-"},
		{`{% set j = joiner(1) %}{{ j() }}|{{ j() }}|{{ j() }}`, "|1|1"},
		{`{% set j = joiner(none) %}{{ j() }}|{{ j() }}`, "|None"},
		{`{% set j = joiner([1,2]) %}{{ j() }}|{{ j() }}`, "|[1, 2]"},
		{`{% set j = joiner(false) %}{{ j() }}|{{ j() }}`, "|False"},
		{`{% set c = cycler(1,2) %}{{ c.next() }}{{ c.next() }}{{ c.next() }}`, "121"},
		{`{{ range(3)|list }}`, "[0, 1, 2]"},
		{`{{ range(1,2,3)|list }}`, "[1]"},
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

	// lipsum's words are random, so only its acceptance is checked here.
	for _, src := range []string{
		`{{ lipsum(1) }}`, `{{ lipsum(1,false) }}`, `{{ lipsum(n=1,html=false,min=1,max=2) }}`,
		`{{ lipsum(1,2,3,4) }}`,
	} {
		tmpl, err := env.FromString(src)
		if err != nil {
			t.Fatalf("compile %q: %v", src, err)
		}
		got, err := tmpl.RenderString(context.Background(), nil)
		if err != nil {
			t.Errorf("%s: %v", src, err)
		} else if strings.TrimSpace(got) == "" {
			t.Errorf("%s: rendered nothing", src)
		}
	}
}
