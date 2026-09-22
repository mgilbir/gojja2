// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package dataflow_test

import (
	"fmt"
	"log"
	"sort"

	"github.com/mgilbir/gojja2"
	"github.com/mgilbir/gojja2/dataflow"
)

// What a template does with each of the caller's variables: whether its value
// can be printed, whether it only steers the result, and whether the render can
// fail because of it.
func ExampleAnalyze() {
	env, err := gojja2.New()
	if err != nil {
		log.Fatal(err)
	}
	tmpl, err := env.FromString(
		`{% if admin %}{{ name|title }}{% endif %}{% set unused = note %}`)
	if err != nil {
		log.Fatal(err)
	}

	tree := tmpl.Syntax()
	effects := dataflow.Analyze(tree).Context(tree)

	names := make([]string, 0, len(effects))
	for name := range effects {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		e := effects[name]
		fmt.Printf("%-6s printed=%-5v steers=%-5v required=%v\n",
			name, e&dataflow.Printed != 0, e&dataflow.Steers != 0,
			e&dataflow.Required != 0)
	}
	// admin is required because it guards `{{ name|title }}`: a filter can
	// raise, and the condition decides whether it runs.
	//
	// Output:
	// admin  printed=false steers=true  required=true
	// name   printed=true  steers=false required=true
	// note   printed=false steers=false required=false
}
