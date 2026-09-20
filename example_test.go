// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/mgilbir/gojja2"
	"github.com/mgilbir/gojja2/errs"
	"github.com/mgilbir/gojja2/value"
)

func ExampleEnvironment_FromString() {
	ctx := context.Background()
	env := mustEnv()
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
	env := mustEnv()
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
	env := mustEnv(gojja2.WithLoader(gojja2.DictLoader{
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
	env := mustEnv(gojja2.WithAutoescape(true))
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
	env := mustEnv()
	tmpl, _ := env.FromString(`{% set d = {1: "one", 2: "two",} %}{{ d[1] }}/{{ d[2] }}/{{ d }}`)
	out, _ := tmpl.RenderString(ctx, nil)
	fmt.Println(out)
	// Output: one/two/{1: 'one', 2: 'two'}
}

// Errors carry the Python exception class jinja2 would have raised, so they
// can be matched with errors.Is.
func ExampleEnvironment_errors() {
	ctx := context.Background()
	env := mustEnv(gojja2.WithUndefined(value.UndefinedStrict))
	tmpl, _ := env.FromString("{{ missing }}")
	_, err := tmpl.RenderString(ctx, nil)
	fmt.Println(err)
	// Output: 'missing' is undefined
}

// bannerFilter is a filter that allocates a size the *template* chooses, so it
// charges the render's budget before allocating rather than after. See
// docs/extending.md.
func bannerFilter(s *gojja2.State, v value.Value, args *value.CallArgs) (value.Value, error) {
	width := int64(40)
	if w, ok := args.Arg(0); ok {
		n, ok := w.Int64()
		if !ok {
			return value.Undefined, errs.New(errs.TypeError,
				"banner() width must be an integer, not %s", w.TypeName())
		}
		width = n
	}
	// Charge first. Charging afterwards charges for memory already gone.
	if err := s.ChargeBytes(width); err != nil {
		return value.Undefined, err
	}
	return value.String(strings.Repeat("=", int(width))), nil
}

// A filter of your own is a Go func, registered by name.
func ExampleEnvironment_AddFilter() {
	ctx := context.Background()
	env := mustEnv()
	env.AddFilter("banner", bannerFilter)

	tmpl, err := env.FromString(`{{ "" | banner(10) }}`)
	if err != nil {
		log.Fatal(err)
	}
	out, err := tmpl.RenderString(ctx, nil)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(out)

	// The same filter under a budget the template's chosen width would blow:
	// refused before a byte is allocated.
	small := mustEnv(gojja2.WithMaxOutputBytes(64))
	small.AddFilter("banner", bannerFilter)
	tmpl, _ = small.FromString(`{{ "" | banner(1000000) }}`)
	_, err = tmpl.RenderString(ctx, nil)
	fmt.Println(err)

	// Output:
	// ==========
	// render wrote more than 64 bytes of output
}

// tempC is a host type exposed to templates as an Object rather than through
// reflection, so it can decide its own attributes, its own repr and its own
// escaped form. See docs/extending.md.
type tempC float64

func (t tempC) GetAttr(name string) (value.Value, bool) {
	switch name {
	case "celsius":
		return value.Float(float64(t)), true
	case "fahrenheit":
		return value.Float(float64(t)*9/5 + 32), true
	}
	return value.Undefined, false
}

// Str is what `{{ t }}` prints; Repr is what it prints inside a container.
func (t tempC) Str() string  { return fmt.Sprintf("%.1f°C", float64(t)) }
func (t tempC) Repr() string { return fmt.Sprintf("tempC(%g)", float64(t)) }

// HTML is Python's __html__: autoescape asks for this instead of escaping Str.
func (t tempC) HTML() string { return fmt.Sprintf("<b>%.1f&deg;C</b>", float64(t)) }

// A Go type can present itself to templates directly, instead of being
// reflected over, by implementing value.Object.
func Example_customObject() {
	ctx := context.Background()
	env := mustEnv(gojja2.WithAutoescape(true))
	tmpl, err := env.FromString(
		`{{ t.celsius }} = {{ t.fahrenheit }}` + "\n" +
			`printed: {{ t|string }}` + "\n" +
			`in a list: {{ [t] }}` + "\n" +
			`escaped: {{ t }}` + "\n" +
			`missing attribute is undefined: {{ t.kelvin|default("n/a") }}`)
	if err != nil {
		log.Fatal(err)
	}
	// RenderValues takes values that are already template values, skipping the
	// conversion from Go -- which is how an Object reaches a render.
	var out strings.Builder
	if err := tmpl.RenderValues(ctx, &out, map[string]value.Value{
		"t": value.FromObject(tempC(21.5)),
	}); err != nil {
		log.Fatal(err)
	}
	fmt.Println(out.String())
	// Output:
	// 21.5 = 70.7
	// printed: 21.5°C
	// in a list: [tempC(21.5)]
	// escaped: <b>21.5&deg;C</b>
	// missing attribute is undefined: n/a
}

// A global is a value, and gojja2.Func wraps a Go function as one. This one
// does sustained work without writing output, so it polls the context: without
// that, it is a region a cancelled render cannot interrupt.
func ExampleFunc() {
	env := mustEnv()
	env.AddGlobal("countdown", gojja2.Func("countdown",
		func(s *gojja2.State, args *value.CallArgs) (value.Value, error) {
			n, _ := args.Arg(0)
			steps, ok := n.Int64()
			if !ok {
				return value.Undefined, errs.New(errs.TypeError, "countdown() wants an integer")
			}
			total := 0
			for i := int64(0); i < steps; i++ {
				// Charge the work, and let a cancelled render stop here.
				if err := s.Step(1); err != nil {
					return value.Undefined, err
				}
				total++
			}
			return value.Int(int64(total)), nil
		}))

	tmpl, err := env.FromString(`{{ countdown(5) }}`)
	if err != nil {
		log.Fatal(err)
	}
	out, err := tmpl.RenderString(context.Background(), nil)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(out)

	// A render whose context is already cancelled stops at the first poll.
	dead, cancel := context.WithCancel(context.Background())
	cancel()
	tmpl, _ = env.FromString(`{{ countdown(1000000) }}`)
	_, err = tmpl.RenderString(dead, nil)
	fmt.Println(err)

	// Output:
	// 5
	// render stopped: context canceled
}
