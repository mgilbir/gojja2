// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package syntax_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgilbir/gojja2"
	"github.com/mgilbir/gojja2/syntax"
)

// Everything else about the canonical form is checked by comparing it to
// jinja2's. That cannot catch a field neither side writes down: two emitters
// that both forget `ignore missing` agree perfectly and are both wrong, and no
// amount of agreement will say so.
//
// What says so is discrimination. For each attribute the vocabulary carries,
// two templates that differ only in it must not encode the same -- and the pairs
// below are chosen so that the difference is one a render can tell.
func TestEveryAttributeIsCarried(t *testing.T) {
	sources := map[string]string{
		"inc":  "[included]",
		"mod":  "{% macro a() %}A{% endmacro %}{% macro b() %}B{% endmacro %}",
		"base": "{% block x %}X{% endblock %}",
	}
	for _, tc := range []struct{ attr, a, b string }{
		{"attr", `{{ o.x }}`, `{{ o.y }}`},
		{"name (variable)", `{{ a }}`, `{{ b }}`},
		{"name (filter)", `{{ a|upper }}`, `{{ a|lower }}`},
		{"name (test)", `{{ a is odd }}`, `{{ a is even }}`},
		{"name (block)", `{% block x %}q{% endblock %}`, `{% block y %}q{% endblock %}`},
		{"name (macro)", `{% macro m() %}q{% endmacro %}`, `{% macro n() %}q{% endmacro %}`},
		{"name (keyword)", `{{ range(1, stop=2) }}`, `{{ range(1, step=2) }}`},
		{"op (binary)", `{{ a + b }}`, `{{ a - b }}`},
		{"op (unary)", `{{ -a }}`, `{{ not a }}`},
		{"op (comparison)", `{{ a < b }}`, `{{ a > b }}`},
		{"value (number)", `{{ 1 }}`, `{{ 2 }}`},
		{"value (string)", `{{ 'a' }}`, `{{ 'b' }}`},
		{"value (text)", `x`, `y`},
		{"store", `{% set a = 1 %}{{ a }}`, `{{ a }}{% set a = 1 %}`},
		{"recursive", `{% for i in xs %}q{% endfor %}`, `{% for i in xs recursive %}q{% endfor %}`},
		{"scoped", `{% block x %}q{% endblock %}`, `{% block x scoped %}q{% endblock %}`},
		{"target", `{% import 'mod' as p %}`, `{% import 'mod' as q %}`},
		{"with_context (import)", `{% import 'mod' as p %}`, `{% import 'mod' as p with context %}`},
		{"with_context (include)", `{% include 'inc' %}`, `{% include 'inc' without context %}`},
		{"ignore_missing", `{% include 'inc' %}`, `{% include 'inc' ignore missing %}`},
		{"names (imported)", `{% from 'mod' import a %}`, `{% from 'mod' import b %}`},
		{"names (alias)", `{% from 'mod' import a %}`, `{% from 'mod' import a as c %}`},
	} {
		t.Run(tc.attr, func(t *testing.T) {
			a, b := encodeOne(t, sources, tc.a), encodeOne(t, sources, tc.b)
			if a == b {
				t.Errorf("these encode identically, so the vocabulary does not "+
					"carry %s:\n  %s\n  %s\n  both: %s", tc.attr, tc.a, tc.b, a)
			}
		})
	}
}

func encodeOne(t *testing.T, sources map[string]string, src string) string {
	t.Helper()
	all := make(map[string]string, len(sources)+1)
	for k, v := range sources {
		all[k] = v
	}
	all["t"] = src
	env, err := gojja2.New(gojja2.WithLoader(gojja2.DictLoader(all)))
	if err != nil {
		t.Fatal(err)
	}
	tmpl, err := env.GetTemplate("t")
	if err != nil {
		t.Fatalf("compile %q: %v", src, err)
	}
	raw, err := syntax.Canonical(tmpl.Syntax().Root)
	if err != nil {
		t.Fatalf("encode %q: %v", src, err)
	}
	return string(raw)
}

// An attribute the corpus never exercises is one the comparison against jinja2
// never checks, so the pairs above would be the only thing standing behind it.
// This requires each one to appear in a committed case as well.
func TestEveryAttributeAppearsInTheCorpus(t *testing.T) {
	root := repoRootFor(t)
	raw, err := os.ReadFile(filepath.Join(root, "testdata/syntax.jsonl"))
	if err != nil {
		t.Skipf("no committed trees: %v", err)
	}
	all := string(raw)
	for _, attr := range []string{
		"attr", "ignore_missing", "name", "names", "op", "recursive",
		"required", "scoped", "store", "target", "value", "with_context",
	} {
		if !strings.Contains(all, `\"`+attr+`\":`) {
			t.Errorf("no committed case carries the %q attribute, so nothing "+
				"compares it against jinja2", attr)
		}
	}
}

func repoRootFor(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}
