// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"context"
	"strings"
	"testing"

	"github.com/mgilbir/gojja2/errs"
)

// TestCollectionMethodsMatchCPython covers the three methods that were missing
// from list and dict -- a template calling one got `'list object' has no
// attribute 'sort'` -- and the messages that named the wrong type or left out
// the count CPython reports.
func TestCollectionMethodsMatchCPython(t *testing.T) {
	env := mustNew()
	for _, tc := range []struct{ src, want string }{
		// list.sort is in place and answers None, so both halves matter.
		{`{% set L = [3,1,2] %}{{ L.sort() }}|{{ L }}`, `None|[1, 2, 3]`},
		{`{% set L = [] %}{{ L.sort() }}|{{ L }}`, `None|[]`},
		{`{% set L = ['b','A','c'] %}{{ L.sort() }}|{{ L }}`, `None|['A', 'b', 'c']`},
		{`{% set L = [3,1,2] %}{{ L.sort(reverse=true) }}|{{ L }}`, `None|[3, 2, 1]`},
		{`{% set L = ['b','A','c'] %}{{ L.sort(reverse=true) }}|{{ L }}`, `None|['c', 'b', 'A']`},
		// The sort is stable and Python-ordered: 1 == True, so they keep
		// the order they were written in.
		{`{% set L = [1.5, 1, true] %}{{ L.sort() }}|{{ L }}`, `None|[1, True, 1.5]`},
		{`{% set L = [[2],[1]] %}{{ L.sort() }}|{{ L }}`, `None|[[1], [2]]`},

		// dict.popitem takes the last pair inserted, not an arbitrary one.
		{`{% set D = {'a':1,'b':2} %}{{ D.popitem() }}|{{ D }}`, `('b', 2)|{'a': 1}`},
		{`{% set D = {'a':1} %}{{ D.popitem() }}|{{ D }}`, `('a', 1)|{}`},

		// dict.fromkeys is a classmethod: the receiver contributes nothing.
		{`{% set D = {'x': 1} %}{{ D.fromkeys('ab') }}`, `{'a': None, 'b': None}`},
		{`{% set D = {'x': 1} %}{{ D.fromkeys([3,1,2], 9) }}`, `{3: 9, 1: 9, 2: 9}`},
		{`{% set D = {'x': 1} %}{{ D.fromkeys([]) }}`, `{}`},
		// Iterating a dict gives its keys, as everywhere else.
		{`{{ {'b':2,'a':1}.fromkeys({'b':2,'a':1}) }}`, `{'b': None, 'a': None}`},
	} {
		tmpl, err := env.FromString(tc.src)
		if err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
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

	for _, tc := range []struct {
		src  string
		kind errs.Kind
		want string
	}{
		// sort's arguments are keyword-only.
		{`{% set L = [1] %}{{ L.sort(1) }}`, errs.TypeError,
			"sort() takes no positional arguments"},
		{`{% set L = [1] %}{{ L.sort(bogus=1) }}`, errs.TypeError,
			"'bogus' is an invalid keyword argument for sort()"},
		{`{% set L = [1,'a'] %}{{ L.sort() }}`, errs.TypeError,
			"'<' not supported between instances of"},
		// popitem on an empty dict, and with any argument at all.
		{`{{ {}.popitem() }}`, errs.KeyError, "popitem(): dictionary is empty"},
		{`{{ {'a':1}.popitem(1) }}`, errs.TypeError,
			"dict.popitem() takes no arguments (1 given)"},
		// fromkeys wants something iterable, and hashable keys.
		{`{{ {}.fromkeys(5) }}`, errs.TypeError, "'int' object is not iterable"},
		{`{{ {}.fromkeys([[1]]) }}`, errs.TypeError, "unhashable type: 'list'"},
		{`{{ {}.fromkeys() }}`, errs.TypeError, "fromkeys expected at least 1 argument, got 0"},
		// dict.get counts its arguments, both ways.
		{`{{ {}.get() }}`, errs.TypeError, "get expected at least 1 argument, got 0"},
		{`{{ {'a':1}.get('a', 1, 2) }}`, errs.TypeError, "get expected at most 2 arguments, got 3"},
		// A tuple names itself rather than borrowing list's wording.
		{`{{ (1,2).index(99) }}`, errs.ValueError, "tuple.index(x): x not in tuple"},
		// An empty cycler is a RuntimeError in jinja2, not a TypeError.
		{`{{ cycler() }}`, errs.RuntimeError, "at least one item has to be provided"},
	} {
		tmpl, err := env.FromString(tc.src)
		if err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		_, err = tmpl.RenderString(context.Background(), nil)
		if err == nil {
			t.Errorf("%s: rendered; want %v", tc.src, tc.kind)
			continue
		}
		if kind := errs.KindOf(err); kind != tc.kind {
			t.Errorf("%s: got %v (%v), want %v", tc.src, kind, err, tc.kind)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %q, want %q", tc.src, err, tc.want)
		}
	}

	// A list still reports itself as a list, which is the other half of the
	// tuple wording being its own.
	if _, err := env.FromString(`{{ [1,2].index(99) }}`); err == nil {
		out, err := mustTmpl(t, env, `{{ [1,2].index(99) }}`)
		_ = out
		if err == nil || !strings.Contains(err.Error(), "99 is not in list") {
			t.Errorf("list.index: got %v, want \"99 is not in list\"", err)
		}
	}
}

func mustTmpl(t *testing.T, env *Environment, src string) (string, error) {
	t.Helper()
	tmpl, err := env.FromString(src)
	if err != nil {
		t.Fatalf("compile %q: %v", src, err)
	}
	return tmpl.RenderString(context.Background(), nil)
}
