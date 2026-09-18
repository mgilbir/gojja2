// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"context"
	"testing"
)

// d.keys(), d.values() and d.items() are views, not lists.
//
// They were lists, which a template can tell apart in five ways: the repr, the
// `is sequence` test, indexing, json.dumps, and -- the one that is not about
// appearances -- that a view tracks the dict it came from.
//
// This is not the lazy-sequence divergence in docs/divergences.md. That is
// about jinja2's map, select and items *filters*, which return generators
// there and lists here deliberately. A view is a different thing from a
// generator: it has a length, it can be walked twice, and it is live.
func TestDictMethodsReturnViews(t *testing.T) {
	const d = `{% set d = {'b': 2, 'a': 1} %}`
	for _, tc := range []struct{ src, want string }{
		{d + `{{ d.keys() }}`, `dict_keys(['b', 'a'])`},
		{d + `{{ d.values() }}`, `dict_values([2, 1])`},
		{d + `{{ d.items() }}`, `dict_items([('b', 2), ('a', 1)])`},
		{d + `{{ d.keys()|string }}`, `dict_keys(['b', 'a'])`},
		// A view has a length and membership, and is iterable...
		{d + `{{ d.keys()|length }}`, "2"},
		{d + `{{ 'a' in d.keys() }}`, "True"},
		{d + `{{ 'z' in d.keys() }}`, "False"},
		{d + `{{ d.keys()|list }}`, `['b', 'a']`},
		{d + `{% for k in d.keys() %}{{ k }};{% endfor %}`, "b;a;"},
		{d + `{% for k, v in d.items() %}{{ k }}={{ v }};{% endfor %}`, "b=2;a=1;"},
		// ...but is not a sequence, so it does not index.
		{d + `{{ d.keys() is sequence }}`, "False"},
		{d + `{{ d.keys() is iterable }}`, "True"},
		{d + `{{ d.keys()[0] }}`, ""},
		// It still flows through the sequence filters, which walk it.
		{d + `{{ d.keys()|sort }}`, `['a', 'b']`},
		{d + `{{ d.keys()|first }}`, "b"},
		{d + `{{ d.keys()|join(',') }}`, "b,a"},
		{d + `{{ d.values()|sum }}`, "3"},
		{d + `{{ d.keys()|map('upper')|list }}`, `['B', 'A']`},
	} {
		checkView(t, tc.src, tc.want)
	}
}

// A view is live: it shows what the dict holds now, not what it held when the
// view was taken. A list cannot do that, which is the difference that is not
// merely cosmetic.
func TestDictViewTracksTheDict(t *testing.T) {
	checkView(t,
		`{% set d = {'b': 2, 'a': 1} %}{% set k = d.keys() %}`+
			`{{ d.update({'c': 3}) }}{{ k|list }}|{{ k|length }}`,
		"None['b', 'a', 'c']|3")
}

// json.dumps refuses a view, as it refuses anything it has no type for.
func TestDictViewIsNotJSONSerializable(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{`{{ {'a': 1}.keys()|tojson }}`, "Object of type dict_keys is not JSON serializable"},
		{`{{ {'a': 1}.values()|tojson }}`, "Object of type dict_values is not JSON serializable"},
		{`{{ {'a': 1}.items()|tojson }}`, "Object of type dict_items is not JSON serializable"},
	} {
		tmpl, err := New().FromString(tc.src)
		if err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		if _, err := tmpl.RenderString(context.Background(), nil); err == nil || err.Error() != tc.want {
			t.Errorf("%s: got %v, want %q", tc.src, err, tc.want)
		}
	}
}

// Keys and items compare as sets; a values view has no __eq__ in Python at
// all, so two of them are equal only by being the same object.
func TestDictViewEquality(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{`{% set d = {'a': 1} %}{{ d.keys() == d.keys() }}`, "True"},
		{`{% set d = {'a': 1} %}{{ d.items() == d.items() }}`, "True"},
		{`{% set d = {'a': 1} %}{{ d.values() == d.values() }}`, "False"},
		{`{% set d = {'a': 1} %}{% set k = d.keys() %}{{ k == k }}`, "True"},
		{`{{ {'a': 1}.keys() == {'a': 2}.keys() }}`, "True"},
		{`{{ {'a': 1}.keys() == {'b': 1}.keys() }}`, "False"},
		{`{{ {'a': 1}.items() == {'a': 2}.items() }}`, "False"},
		{`{% set d = {'a': 1} %}{{ d.keys() == d.values() }}`, "False"},
	} {
		checkView(t, tc.src, tc.want)
	}
}

func checkView(t *testing.T, src, want string) {
	t.Helper()
	tmpl, err := New().FromString(src)
	if err != nil {
		t.Errorf("%s: compile: %v", src, err)
		return
	}
	got, err := tmpl.RenderString(context.Background(), nil)
	if err != nil || got != want {
		t.Errorf("%s = %q, %v; want %q", src, got, err, want)
	}
}
