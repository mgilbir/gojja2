// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"context"
	"fmt"

	"github.com/mgilbir/gojja2"
	"github.com/mgilbir/gojja2/value"
)

// One base environment holds the loader, the filters and the compiled
// templates; each tenant gets an overlay with its own globals and settings.
func ExampleEnvironment_Overlay() {
	base, err := gojja2.New(gojja2.WithLoader(gojja2.DictLoader{
		"page.html":   `{{ "<b>" }} {{ brand }}: {% include "footer.html" %}`,
		"footer.html": `{{ brand|upper }}`,
	}))
	if err != nil {
		panic(err)
	}
	base.AddGlobal("brand", value.String("base"))

	// Changes only a limit and a global, so it reuses the base's compiled
	// templates.
	acme, err := base.Overlay(gojja2.WithMaxIterations(10_000))
	if err != nil {
		panic(err)
	}
	acme.AddGlobal("brand", value.String("acme"))

	// Changes how templates compile, so it compiles its own.
	escaped, err := base.Overlay(gojja2.WithAutoescape(true))
	if err != nil {
		panic(err)
	}

	for _, env := range []*gojja2.Environment{base, acme, escaped} {
		tmpl, err := env.GetTemplate("page.html")
		if err != nil {
			panic(err)
		}
		out, err := tmpl.RenderString(context.Background(), nil)
		if err != nil {
			panic(err)
		}
		fmt.Println(out)
	}
	// Output:
	// <b> base: BASE
	// <b> acme: ACME
	// &lt;b&gt; base: BASE
}
