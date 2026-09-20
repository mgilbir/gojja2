// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"context"
	"testing"
)

// TestTemplateModuleIsItsBody: a TemplateModule's str() is what the imported
// template rendered, and its __html__ is the same string -- the body was
// produced under that template's own escaping, so handing it over as markup is
// what keeps it from being escaped twice.
//
// gojja2 threw the body away and printed the repr, so `{% import "t" as m %}
// {{ m }}` rendered "<TemplateModule 't'>" where jinja2 renders t's output.
// The module also carried no qualified name, so an attribute error named
// "TemplateModule object" where CPython names
// "jinja2.environment.TemplateModule object", and `{% from %}` reported the
// *importing* template's name -- empty for anything compiled from a string --
// instead of the one it imported from.
func TestTemplateModuleIsItsBody(t *testing.T) {
	load := map[string]string{
		"mac.txt":  `{% macro m(x) %}({{ x }}){% endmacro %}{% set ex = "E" %}`,
		"body.txt": `<b>{{ w|default("?") }}</b>`,
	}
	for _, auto := range []bool{false, true} {
		env := mustNew(WithLoader(DictLoader(load)), WithAutoescape(auto))
		for _, tc := range []struct{ src, want string }{
			// The body, not the repr -- and not escaped again, since
			// it was rendered under the same setting.
			{`{% import "body.txt" as m %}{{ m }}`, "<b>?</b>"},
			{`{% import "body.txt" as m %}{{ m|safe }}`, "<b>?</b>"},
			{`{% import "body.txt" as m %}{{ m|escape }}`, "<b>?</b>"},
			{`{% import "body.txt" as m %}{{ [m]|join("-") }}`, "<b>?</b>"},
			// A module has __html__, so it is "escaped".
			{`{% import "body.txt" as m %}{{ m is escaped }}`, "True"},
			// A macro-only module renders nothing.
			{`{% import "mac.txt" as m %}[{{ m }}]`, "[]"},
			{`{% import "mac.txt" as m %}{{ m.m(1) }}`, "(1)"},
		} {
			tmpl, err := env.FromString(tc.src)
			if err != nil {
				t.Fatalf("compile %q: %v", tc.src, err)
			}
			got, err := tmpl.RenderString(context.Background(), nil)
			if err != nil {
				t.Errorf("autoescape=%v %s: %v", auto, tc.src, err)
				continue
			}
			if got != tc.want {
				t.Errorf("autoescape=%v %s\n got %q\nwant %q", auto, tc.src, got, tc.want)
			}
		}
	}

	// `~` is not __html__: markup_join calls soft_str first, which turns a
	// non-str into a plain str, so the result is escaped as a whole.
	env := mustNew(WithLoader(DictLoader(load)), WithAutoescape(true))
	tmpl, err := env.FromString(`{% import "body.txt" as m %}{{ "" ~ m }}`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	got, err := tmpl.RenderString(context.Background(), nil)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if want := "&lt;b&gt;?&lt;/b&gt;"; got != want {
		t.Errorf("concat\n got %q\nwant %q", got, want)
	}

	// The names in the errors, and in the repr -- which pprint still uses.
	plain := mustNew(WithLoader(DictLoader(load)))
	tmpl, err = plain.FromString(`{% import "mac.txt" as m %}{{ m|pprint }}`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if got, err = tmpl.RenderString(context.Background(), nil); err != nil {
		t.Fatalf("render: %v", err)
	} else if want := "<TemplateModule 'mac.txt'>"; got != want {
		t.Errorf("pprint\n got %q\nwant %q", got, want)
	}

	for _, tc := range []struct{ src, want string }{
		{`{% import "mac.txt" as m %}{{ m.nope.x }}`,
			"'jinja2.environment.TemplateModule object' has no attribute 'nope'"},
		{`{% from "mac.txt" import nope %}{{ nope() }}`,
			"the template 'mac.txt' (imported on line 1) does not export the requested name 'nope'"},
	} {
		tmpl, err := plain.FromString(tc.src)
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

// An imported template that extends another renders through the extends chain,
// like any other render.
//
// importModule ran the template's body with execBody and stopped. A template
// that extends emits nothing from its own body -- its output comes from the
// parent, rendered afterwards with the blocks the child registered -- so an
// extending module's str() was the empty string, and no name set anywhere up
// the chain was exported.
//
// renderState is the one place that knows about the parent chain, and
// {% include %} already went through it. That is what made the bug so quiet:
// including a template and importing it disagreed about what the template
// renders, and only one of them was right.
func TestImportOfAnExtendingTemplateRendersTheChain(t *testing.T) {
	loader := DictLoader{
		"base.html":  "B[{% block x %}bx{% endblock %}]{% set fromBase = 'FB' %}",
		"mid.html":   `{% extends "base.html" %}{% block x %}mx{% endblock %}{% set fromMid = 'FM' %}`,
		"deep.html":  `{% extends "mid.html" %}{% block x %}dx{% endblock %}{% set v = 7 %}{% macro m() %}M{% endmacro %}`,
		"plain.html": "P{% set v = 9 %}{% macro m() %}N{% endmacro %}",
		"ctx.html":   `{% extends "base.html" %}{% block x %}{{ outer }}{% endblock %}`,
	}
	for _, tc := range []struct{ name, src, want string }{
		{"str of a two-level extending module",
			`[{% import "deep.html" as m %}{{ m }}]`, "[B[dx]]"},
		{"str of a one-level extending module",
			`[{% import "mid.html" as m %}{{ m }}]`, "[B[mx]]"},
		{"its own exports still work",
			`[{% import "deep.html" as m %}{{ m.v }}|{{ m.m() }}]`, "[7|M]"},
		// A name set at the top level of a template in the chain is
		// exported too, because the chain runs in the module's state.
		{"an export from the middle template",
			`[{% import "deep.html" as m %}{{ m.fromMid }}]`, "[FM]"},
		{"an export from the base template",
			`[{% import "deep.html" as m %}{{ m.fromBase }}]`, "[FB]"},
		{"as a string it has a length",
			`{% import "deep.html" as m %}{{ m|string|length }}`, "5"},
		{"from-import off an extending template",
			`[{% from "deep.html" import m %}{{ m() }}]`, "[M]"},
		// The context rules are unchanged: the chain renders with
		// whatever the import was given.
		{"with context",
			`{% set outer = "OUT" %}[{% import "ctx.html" as m with context %}{{ m }}]`, "[B[OUT]]"},
		{"without context",
			`{% set outer = "OUT" %}[{% import "ctx.html" as m %}{{ m }}]`, "[B[]]"},
		// A plain template was always right and stays right.
		{"a plain module",
			`[{% import "plain.html" as m %}{{ m }}]`, "[P]"},
		{"a plain module's exports",
			`[{% import "plain.html" as m %}{{ m.v }}|{{ m.m() }}]`, "[9|N]"},
		// Importing and including a template now agree about what it
		// renders, which is the invariant that was broken.
		{"include agrees with import",
			`[{% include "deep.html" %}]`, "[B[dx]]"},
	} {
		env := mustNew(WithLoader(loader))
		tmpl, err := env.FromString(tc.src)
		if err != nil {
			t.Errorf("%s: compile: %v", tc.name, err)
			continue
		}
		got, err := tmpl.RenderString(context.Background(), nil)
		if err != nil || got != tc.want {
			t.Errorf("%s = %q, %v; want %q", tc.name, got, err, tc.want)
		}
	}
}
