// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"context"
	"testing"
)

// TestTemplateSelection: the three statements that name a template do not name
// it the same way. {% include %} compiles to get_or_select_template, while
// {% extends %}, {% import %} and {% from %} compile to get_template -- so a
// name that is not a string is a *selection* in the first and a missing
// template in the others.
//
// gojja2 refused every non-string at the door with "template name must be a
// string", a rule jinja2 does not have. Nothing that a template could write
// reached the behaviour underneath it: a number is something select_template
// cannot iterate, a None is an empty selection, and a mapping iterates its keys
// and then trips over CPython's own reporting path.
func TestTemplateSelection(t *testing.T) {
	load := map[string]string{
		"inc.txt": "<inc>",
		"mac.txt": `{% macro m(x) %}({{ x }}){% endmacro %}`,
	}
	env := mustNew(WithLoader(DictLoader(load)))

	for _, tc := range []struct{ src, want string }{
		{`{% include "inc.txt" %}`, "<inc>"},
		{`{% include ["nosuch.txt","inc.txt"] %}`, "<inc>"},
		{`{% include ("inc.txt",) %}`, "<inc>"},
		// A mapping is iterated by key, so this finds the template.
		{`{% include {"inc.txt": 1} %}`, "<inc>"},
		// ignore missing swallows the selection failures, not the type
		// ones -- see below.
		{`[{% include ["nosuch.txt"] ignore missing %}]`, "[]"},
		{`[{% include none ignore missing %}]`, "[]"},
		// A variable holding a name, and one holding a list.
		{`{% set n = "inc.txt" %}{% include n %}`, "<inc>"},
		{`{% set n = ["nosuch.txt","inc.txt"] %}{% include n %}`, "<inc>"},
		{`{% set n = "mac.txt" %}{% import n as m %}{{ m.m("q") }}`, "(q)"},
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

	for _, tc := range []struct{ src, want string }{
		// Emptiness is truthiness, and it is checked before anything
		// asks whether the value can be iterated at all.
		{`{% include none %}`, "Tried to select from an empty list of templates."},
		{`{% include [] %}`, "Tried to select from an empty list of templates."},
		{`{% include {} %}`, "Tried to select from an empty list of templates."},
		{`{% include false %}`, "Tried to select from an empty list of templates."},
		// A truthy non-iterable fails at the iteration.
		{`{% include 1 %}`, "'int' object is not iterable"},
		{`{% include 1.5 %}`, "'float' object is not iterable"},
		{`{% include true %}`, "'bool' object is not iterable"},
		// Which ignore missing does not swallow: it catches
		// TemplateNotFound, and this is a TypeError.
		{`{% include 1 ignore missing %}`, "'int' object is not iterable"},
		// Nothing found lists the candidates.
		{`{% include ["nosuch.txt","nosuch2.txt"] %}`,
			"none of the templates given were found: nosuch.txt, nosuch2.txt"},
		// A mapping whose keys are all missing trips CPython's own
		// reporting, which subscripts what it was handed.
		{`{% include {"a":1} %}`, "-1"},

		// The other three go straight to the loader, so the name is
		// reported as it is rather than checked.
		{`{% import 1 as m %}{{ m }}`, "1"},
		{`{% import none as m %}{{ m }}`, "None"},
		{`{% import 1.5 as m %}{{ m }}`, "1.5"},
		{`{% from 1 import x %}{{ x }}`, "1"},
		{`{% extends none %}`, "None"},
		{`{% extends 1 %}`, "1"},
		// A list or a dict is not even a cache key.
		{`{% import ["a"] as m %}{{ m }}`, "unhashable type: 'list'"},
		{`{% extends [] %}`, "unhashable type: 'list'"},
		{`{% extends {} %}`, "unhashable type: 'dict'"},
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
}
