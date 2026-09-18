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
		env := New(WithLoader(DictLoader(load)), WithAutoescape(auto))
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
	env := New(WithLoader(DictLoader(load)), WithAutoescape(true))
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
	plain := New(WithLoader(DictLoader(load)))
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
