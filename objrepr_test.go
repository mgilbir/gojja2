// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"context"
	"regexp"
	"strings"
	"testing"
)

// TestDefaultObjectRepr: jinja2 gives most of the objects a template can reach
// a __repr__ of their own -- Namespace, LoopContext, Macro, TemplateReference,
// TemplateModule -- and three of them none at all: Cycler, Joiner and
// BlockReference fall back to Python's `<module.Qualname object at 0xADDR>`.
//
// gojja2 invented "<Cycler>" and "<Joiner>", and gave BlockReference a Str that
// rendered the block. The last one was not cosmetic: `{{ self.body }}` produced
// the block's output where jinja2 produces an object, and a block that printed
// itself recursed until it hit the depth bound.
//
// The address is this object's, as reproducible as CPython's own.
func TestDefaultObjectRepr(t *testing.T) {
	env := mustNew()
	addr := regexp.MustCompile(`^<([\w.]+) object at 0x[0-9a-f]+>$`)
	for _, tc := range []struct{ src, qualified string }{
		{`{{ cycler(1) }}`, "jinja2.utils.Cycler"},
		{`{{ joiner() }}`, "jinja2.utils.Joiner"},
		{`{% block b %}{% endblock %}{{ self.b }}`, "jinja2.runtime.BlockReference"},
	} {
		tmpl, err := env.FromString(tc.src)
		if err != nil {
			t.Fatalf("compile %q: %v", tc.src, err)
		}
		got, err := tmpl.RenderString(context.Background(), nil)
		if err != nil {
			t.Fatalf("%s: %v", tc.src, err)
		}
		m := addr.FindStringSubmatch(got)
		if m == nil {
			t.Errorf("%s = %q, want <%s object at 0x...>", tc.src, got, tc.qualified)
			continue
		}
		if m[1] != tc.qualified {
			t.Errorf("%s named %q, want %q", tc.src, m[1], tc.qualified)
		}
	}

	// Two of the same type are two different objects, so the addresses
	// differ -- a constant would satisfy the shape above and say nothing.
	tmpl, err := env.FromString(`{{ joiner() }}|{{ joiner() }}`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	got, err := tmpl.RenderString(context.Background(), nil)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if a, b, _ := strings.Cut(got, "|"); a == b {
		t.Errorf("two joiners printed the same address: %q", got)
	}

	// The types that do define one keep it, name and all.
	for _, tc := range []struct{ src, want string }{
		{`{% set n = namespace(a=1) %}{{ n }}`, `<Namespace {'a': 1}>`},
		{`{% for i in [1] %}{{ loop }}{% endfor %}`, `<LoopContext 1/1>`},
		{`{% macro m() %}{% endmacro %}{{ m }}`, `<Macro 'm'>`},
		// A template compiled from a string has no name, and Python
		// prints that as None rather than as ''.
		{`{% block b %}{% endblock %}{{ self }}`, `<TemplateReference None>`},
	} {
		tmpl, err := env.FromString(tc.src)
		if err != nil {
			t.Fatalf("compile %q: %v", tc.src, err)
		}
		got, err := tmpl.RenderString(context.Background(), nil)
		if err != nil {
			t.Fatalf("%s: %v", tc.src, err)
		}
		if got != tc.want {
			t.Errorf("%s\n got %q\nwant %q", tc.src, got, tc.want)
		}
	}

	// A loaded template does have a name.
	named := mustNew(WithLoader(DictLoader(map[string]string{
		"t.html": `{% block b %}{% endblock %}{{ self }}`,
	})))
	tmpl, err = named.GetTemplate("t.html")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got, err = tmpl.RenderString(context.Background(), nil); err != nil {
		t.Fatalf("render: %v", err)
	} else if want := `<TemplateReference 't.html'>`; got != want {
		t.Errorf("named\n got %q\nwant %q", got, want)
	}
}
