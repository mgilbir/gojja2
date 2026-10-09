// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/mgilbir/gojja2"
)

// Template.ReferencedTemplates is jinja2's meta.find_referenced_templates. Each
// want is list(meta.find_referenced_templates(env.parse(src))) under jinja2
// 3.1.6, recorded verbatim as the oracle printed it in JSON -- null is the None
// it yields for a name decided at render time. One case per capped subprocess.
func TestReferencedTemplatesAgainstJinja2(t *testing.T) {
	env := mustEnv()
	for _, tc := range []struct{ src, want string }{
		{`{% extends 'layout.html' %}`, `["layout.html"]`},
		{`{% include 'a.html' %}{% import 'm.html' as m %}{% from 'f.html' import x, y as z %}`, `["a.html", "m.html", "f.html"]`},
		{`{% include helper %}`, `[null]`},
		{`{% extends layout %}`, `[null]`},
		{`{% include ['a.html', 'b.html'] %}`, `["a.html", "b.html"]`},
		{`{% include ('a.html', 'b.html') %}`, `["a.html", "b.html"]`},
		{`{% include ['a.html', b, 'c.html'] %}`, `["a.html", null, "c.html"]`},
		{`{% include ['a.html', 1, none, 'c.html'] %}`, `["a.html", "c.html"]`},
		{`{% include [-1, 'a.html'] %}`, `[null, "a.html"]`},
		{`{% include [] %}`, `[]`},
		{`{% extends ['a.html', 'b.html'] %}`, `["a.html", "b.html"]`},
		{`{% import ['a.html', x] as m %}`, `["a.html", null]`},
		{`{% from ('a.html',) import q %}`, `["a.html"]`},
		{`{% include 'a.html' ignore missing %}{% include 'b.html' with context %}{% include 'c.html' without context %}{% include x ignore missing with context %}`, `["a.html", "b.html", "c.html", null]`},
		{`{% include 'a.html' if c else 'b.html' %}`, `[null]`},
		{`{% include 'a' ~ 'b' %}`, `[null]`},
		{`{% include 'a' 'b.html' %}`, `["ab.html"]`},
		{`{% include 'a' + 'b' %}`, `[null]`},
		{`{% include 42 %}`, `[null]`},
		{`{% include none %}`, `[null]`},
		{`{% include true %}`, `[null]`},
		{`{% include 1.5 %}`, `[null]`},
		{`{% include 99999999999999999999999 %}`, `[null]`},
		{`{% extends 42 %}`, `[null]`},
		{`{% include ['a', ['b', 'c']] %}`, `["a", null]`},
		{`{% include ('a.html') %}`, `["a.html"]`},
		{`{% include ['x' ~ y, 'z'] %}`, `[null, "z"]`},
		{`{% include names|first %}`, `[null]`},
		{`{% include 'a.html'|upper %}`, `[null]`},
		{`{% include {'a': 1} %}`, `[null]`},
		{`{% block b %}{% include 'in_block.html' %}{% endblock %}{% macro m() %}{% include 'in_macro.html' %}{% endmacro %}{% for i in x %}{% include 'in_for.html' %}{% else %}{% include 'in_else.html' %}{% endfor %}`, `["in_block.html", "in_macro.html", "in_for.html", "in_else.html"]`},
		{`{% if a %}{% include 'if.html' %}{% elif b %}{% include 'elif.html' %}{% else %}{% include 'else.html' %}{% endif %}{% include 'after.html' %}`, `["if.html", "elif.html", "else.html", "after.html"]`},
		{`{% set s %}{% include 'set.html' %}{% endset %}{% filter upper %}{% include 'filter.html' %}{% endfilter %}{% with %}{% include 'with.html' %}{% endwith %}{% autoescape true %}{% include 'ae.html' %}{% endautoescape %}{% call m() %}{% include 'call.html' %}{% endcall %}`, `["set.html", "filter.html", "with.html", "ae.html", "call.html"]`},
		{`{% macro outer() %}{% block inner %}{% include 'deep.html' %}{% endblock %}{% endmacro %}{% include 'top.html' %}`, `["deep.html", "top.html"]`},
		{`no references at all {{ x }}`, `[]`},
		{`{% include 'a.html' %}{% include 'a.html' %}`, `["a.html", "a.html"]`},
		{`{% for t in ['a.html'] %}{% include t %}{% endfor %}`, `[null]`},
		{`{% include [] + ['a'] %}`, `[null]`},
		{`{% include ['a.html', ('b.html', 'c.html')] %}`, `["a.html", null]`},
		{`{% import 'm.html' as m with context %}{% from 'f.html' import x without context %}`, `["m.html", "f.html"]`},
		{`{% include 'café.html' %}`, `["caf\u00e9.html"]`},
		{`{% include "" %}`, `[""]`},
		{`{% include ['', 'a'] %}`, `["", "a"]`},
		{`{% include [true, 'a'] %}`, `["a"]`},
	} {
		var want []any
		if err := json.Unmarshal([]byte(tc.want), &want); err != nil {
			t.Fatalf("bad want %s: %v", tc.want, err)
		}
		tmpl, err := env.FromString(tc.src)
		if err != nil {
			t.Errorf("FromString(%q): %v", tc.src, err)
			continue
		}
		got := []any{}
		for _, ref := range tmpl.ReferencedTemplates() {
			if ref.Dynamic {
				if ref.Name != "" {
					t.Errorf("%s: a dynamic reference carries the name %q", tc.src, ref.Name)
				}
				got = append(got, nil)
				continue
			}
			got = append(got, ref.Name)
		}
		if !reflect.DeepEqual(got, want) {
			gotJSON, _ := json.Marshal(got)
			t.Errorf("%s\n got %s\nwant %s", tc.src, gotJSON, tc.want)
		}
	}
}

// Each reference says which line its tag is on, which jinja2's generator does
// not -- it yields names only -- so this is graded by hand.
func TestReferencedTemplatesLines(t *testing.T) {
	tmpl, err := mustEnv().FromString("{% extends 'base' %}\n\n{% block b %}\n{% include [x, 'a'] %}{% endblock %}")
	if err != nil {
		t.Fatal(err)
	}
	want := []gojja2.TemplateReference{
		{Name: "base", Line: 1},
		{Dynamic: true, Line: 4},
		{Name: "a", Line: 4},
	}
	if got := tmpl.ReferencedTemplates(); !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}
