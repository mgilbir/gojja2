// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"context"
	"fmt"
	"log"

	"github.com/mgilbir/gojja2"
	"github.com/mgilbir/gojja2/value"
)

func ExampleEnvironment_FromString() {
	ctx := context.Background()
	env := gojja2.New()
	tmpl, err := env.FromString("Hello {{ name|title }}! You have {{ items|length }} item(s).")
	if err != nil {
		log.Fatal(err)
	}
	out, err := tmpl.RenderString(ctx, map[string]any{
		"name":  "ada lovelace",
		"items": []int{1, 2, 3},
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(out)
	// Output: Hello Ada Lovelace! You have 3 item(s).
}

// Go structs are exposed to templates by field name, or by their json tag.
func ExampleTemplate_Render_structs() {
	ctx := context.Background()
	type user struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}
	env := gojja2.New()
	tmpl, _ := env.FromString(
		`{% for u in users|sort(attribute="age") %}{{ u.name }}({{ u.age }}) {% endfor %}`)
	out, err := tmpl.RenderString(ctx, map[string]any{
		"users": []user{{"Bo", 25}, {"Ana", 30}},
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(out)
	// Output: Bo(25) Ana(30)
}

// Template inheritance needs a loader, so the child can find its parent.
func ExampleWithLoader() {
	ctx := context.Background()
	env := gojja2.New(gojja2.WithLoader(gojja2.DictLoader{
		"base.html": `<title>{% block title %}Untitled{% endblock %}</title>`,
		"page.html": `{% extends "base.html" %}{% block title %}{{ super() }} - Home{% endblock %}`,
	}))
	tmpl, err := env.GetTemplate("page.html")
	if err != nil {
		log.Fatal(err)
	}
	out, _ := tmpl.RenderString(ctx, nil)
	fmt.Println(out)
	// Output: <title>Untitled - Home</title>
}

// Autoescaping follows markupsafe: values are escaped, template markup is not,
// and |safe opts a value out.
func ExampleWithAutoescape() {
	ctx := context.Background()
	env := gojja2.New(gojja2.WithAutoescape(true))
	tmpl, _ := env.FromString(`<p>{{ comment }}</p><p>{{ trusted|safe }}</p>`)
	out, _ := tmpl.RenderString(ctx, map[string]any{
		"comment": `<script>alert("x")</script>`,
		"trusted": "<em>ok</em>",
	})
	fmt.Println(out)
	// Output: <p>&lt;script&gt;alert(&#34;x&#34;)&lt;/script&gt;</p><p><em>ok</em></p>
}

// Integer dict keys work, which is the point of modelling Python's dict rather
// than stringifying keys.
func ExampleTemplate_Render_integerDictKeys() {
	ctx := context.Background()
	env := gojja2.New()
	tmpl, _ := env.FromString(`{% set d = {1: "one", 2: "two",} %}{{ d[1] }}/{{ d[2] }}/{{ d }}`)
	out, _ := tmpl.RenderString(ctx, nil)
	fmt.Println(out)
	// Output: one/two/{1: 'one', 2: 'two'}
}

// Errors carry the Python exception class jinja2 would have raised, so they
// can be matched with errors.Is.
func ExampleEnvironment_errors() {
	ctx := context.Background()
	env := gojja2.New(gojja2.WithUndefined(value.UndefinedStrict))
	tmpl, _ := env.FromString("{{ missing }}")
	_, err := tmpl.RenderString(ctx, nil)
	fmt.Println(err)
	// Output: 'missing' is undefined
}
