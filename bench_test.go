// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/mgilbir/gojja2"
)

// Benchmarks for the paths a real template spends its time in. There were
// none, so a change that made rendering twice as slow would not have shown up
// anywhere -- the suite only ever asserted that hostile templates finish.

func benchVars() map[string]any {
	users := make([]any, 0, 50)
	for i := range 50 {
		users = append(users, map[string]any{
			"name": fmt.Sprintf("user%02d", i),
			"age":  20 + i%40,
			"city": []string{"Lisbon", "Porto", "Faro"}[i%3],
			"bio":  "a short line of <em>text</em> about them",
		})
	}
	return map[string]any{
		"users": users,
		"title": "Directory",
		"n":     42,
		"text":  strings.Repeat("the quick brown fox jumps over the lazy dog ", 20),
	}
}

func benchRender(b *testing.B, src string, vars map[string]any, opts ...gojja2.Option) {
	b.Helper()
	env := mustEnv(opts...)
	tmpl, err := env.FromString(src)
	if err != nil {
		b.Fatalf("compile: %v", err)
	}
	ctx := context.Background()
	// One render outside the loop, both to fail fast and to size the buffer
	// the way a warm process would have it.
	if err := tmpl.Render(ctx, io.Discard, vars); err != nil {
		b.Fatalf("render: %v", err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := tmpl.Render(ctx, io.Discard, vars); err != nil {
			b.Fatalf("render: %v", err)
		}
	}
}

func BenchmarkCompileSmall(b *testing.B) {
	env := mustEnv()
	src := `<h1>{{ title }}</h1>{% for u in users %}<p>{{ u.name }}</p>{% endfor %}`
	b.ReportAllocs()
	for b.Loop() {
		if _, err := env.FromString(src); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCompileLarge(b *testing.B) {
	env := mustEnv()
	var sb strings.Builder
	for i := range 200 {
		fmt.Fprintf(&sb, "{%% if n > %d %%}<p>{{ users[%d].name|upper }}</p>{%% endif %%}\n", i, i%50)
	}
	src := sb.String()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := env.FromString(src); err != nil {
			b.Fatal(err)
		}
	}
}

// Text with no tags at all: the floor, and what a lexer regression shows up in.
func BenchmarkRenderPlainText(b *testing.B) {
	benchRender(b, strings.Repeat("some static markup here\n", 200), nil)
}

func BenchmarkRenderInterpolation(b *testing.B) {
	benchRender(b, strings.Repeat("{{ title }}{{ n }}", 100), benchVars())
}

func BenchmarkRenderLoop(b *testing.B) {
	benchRender(b, `{% for u in users %}{{ u.name }}:{{ u.age }};{% endfor %}`, benchVars())
}

// range is the most-used global and the only sequence that produces its
// elements by arithmetic rather than holding them, so a loop over one measures
// GetIndex directly -- which is what pays for the bounds being arbitrary
// precision.
func BenchmarkRenderRangeLoop(b *testing.B) {
	benchRender(b, `{% for i in range(1000) %}{{ i }};{% endfor %}`, nil)
}

func BenchmarkRenderRangeLoopStepped(b *testing.B) {
	benchRender(b, `{% for i in range(5, 5000, 7) %}{{ i }};{% endfor %}`, nil)
}

func BenchmarkRenderLoopWithAttributes(b *testing.B) {
	benchRender(b, `{% for u in users %}<li>{{ u.name }} ({{ u.age }}) {{ u.city }}</li>{% endfor %}`, benchVars())
}

func BenchmarkRenderAutoescape(b *testing.B) {
	benchRender(b, `{% for u in users %}<li>{{ u.bio }}</li>{% endfor %}`, benchVars(),
		gojja2.WithAutoescape(true))
}

func BenchmarkRenderFilters(b *testing.B) {
	benchRender(b, `{{ users|map(attribute='name')|join(', ') }}|`+
		`{{ users|selectattr('age', 'gt', 30)|list|length }}|`+
		`{{ users|sort(attribute='name')|first }}`, benchVars())
}

func BenchmarkRenderStringFilters(b *testing.B) {
	benchRender(b, `{{ text|upper|trim|truncate(80) }}{{ text|wordcount }}{{ text|replace('fox','cat') }}`,
		benchVars())
}

func BenchmarkRenderMacro(b *testing.B) {
	benchRender(b, `{% macro row(u) %}<tr><td>{{ u.name }}</td><td>{{ u.age }}</td></tr>{% endmacro %}`+
		`{% for u in users %}{{ row(u) }}{% endfor %}`, benchVars())
}

func BenchmarkRenderInheritance(b *testing.B) {
	env := mustEnv(gojja2.WithLoader(gojja2.DictLoader{
		"base.html": `<html>{% block head %}<title>{{ title }}</title>{% endblock %}` +
			`<body>{% block body %}empty{% endblock %}</body></html>`,
	}))
	tmpl, err := env.FromString(`{% extends "base.html" %}{% block body %}` +
		`{% for u in users %}<p>{{ u.name }}</p>{% endfor %}{% endblock %}`)
	if err != nil {
		b.Fatal(err)
	}
	vars, ctx := benchVars(), context.Background()
	b.ReportAllocs()
	for b.Loop() {
		if err := tmpl.Render(ctx, io.Discard, vars); err != nil {
			b.Fatal(err)
		}
	}
}

// The bridge: converting the caller's Go data is per-render work, so it is
// measured on its own rather than hidden inside a loop benchmark.
func BenchmarkBridgeConversion(b *testing.B) {
	benchRender(b, `{{ users|length }}`, benchVars())
}

func BenchmarkRenderToString(b *testing.B) {
	env := mustEnv()
	tmpl, err := env.FromString(`{% for u in users %}{{ u.name }}{% endfor %}`)
	if err != nil {
		b.Fatal(err)
	}
	vars, ctx := benchVars(), context.Background()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := tmpl.RenderString(ctx, vars); err != nil {
			b.Fatal(err)
		}
	}
}

// Concurrent rendering of one template, which is how a server uses it.
func BenchmarkRenderParallel(b *testing.B) {
	env := mustEnv()
	tmpl, err := env.FromString(`{% for u in users %}{{ u.name }}:{{ u.age }};{% endfor %}`)
	if err != nil {
		b.Fatal(err)
	}
	vars := benchVars()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		ctx := context.Background()
		for pb.Next() {
			if err := tmpl.Render(ctx, io.Discard, vars); err != nil {
				b.Fatal(err)
			}
		}
	})
}
