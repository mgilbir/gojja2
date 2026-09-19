// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/gojja2"
)

// The other targets vary the template and hold the data fixed. This one does
// the opposite, because the two reach different code.
//
// A render argument is walked by the Go-to-value conversion before the template
// sees it, and that walk is where the shape of the data matters: how deep it
// nests, what its keys are, how wide its numbers get. The worst defect found in
// this engine so far lived there -- converting an argument was neither charged
// nor interruptible, so `{{ big|length }}` ran for a second and returned
// success against a cancelled context -- and no fuzzer was looking at it,
// because every one of them passed the same fixed context.
//
// JSON is the generator. It is a poor fit for a fuzzer byte-wise and a good one
// here: almost every mutation still parses, so what reaches the conversion is a
// *shape* rather than a syntax error, which is the same reason the differential
// fuzzer reads its bytes as grammar decisions.
func FuzzRenderData(f *testing.F) {
	for _, seed := range []string{
		`{}`,
		`{"a": 1}`,
		`{"s": "hello", "n": 42, "f": 1.5, "b": true, "z": null}`,
		`{"list": [1, 2, 3], "dict": {"a": 1}}`,
		`{"deep": {"a": {"b": {"c": {"d": [1, [2, [3, [4]]]]}}}}}`,
		`{"users": [{"name": "ada", "age": 36}, {"name": "bob"}]}`,
		`{"big": 123456789012345678901234567890}`,
		`{"neg": -1e308, "tiny": 1e-308}`,
		`{"": "", "  ": [null, null]}`,
		`{"wide": [[[[[[[[[[1]]]]]]]]]]}`,
		`{"a": {"b": "x"}, "list": ["b", "a"], "big": 2}`,
		// Enough keys that an unsorted iteration order is visible. Go
		// randomises it per range, so two renders of a map this wide
		// disagree almost every time if the keys are not put in order.
		`{"a": {"h":1,"g":2,"f":3,"e":4,"d":5,"c":6,"b":7,"a":8}}`,
		`{"a": {"1":1,"2":2,"3":3,"4":4,"5":5,"6":6,"7":7,"8":8,"9":9}}`,
	} {
		f.Add(seed)
	}

	// Templates that reach a context every way the language offers: by
	// name, by attribute, by subscript, by iteration, by whole-context
	// operations, and by the one that hands it somewhere else.
	templates := []string{
		`{{ a }}|{{ s }}|{{ n }}`,
		`{{ a.b.c.d }}`,
		`{{ a["b"]["c"] }}`,
		`{% for k, v in (a|items) %}{{ k }}={{ v }};{% endfor %}`,
		`{% for x in list %}{{ x }};{% endfor %}`,
		`{{ list|sort|join(",") }}`,
		`{{ users|map(attribute="name")|list }}`,
		`{{ deep|tojson }}`,
		`{{ deep|pprint }}`,
		`{{ big + 1 }}{{ big * 2 }}`,
		`{{ list|unique|list }}`,
		`{% include "inner" with context %}`,
		`{{ dict(**a) }}`,
		`{{ a|length }}{{ a|string|length }}`,
	}

	f.Fuzz(func(t *testing.T, data string) {
		if len(data) > 32<<10 {
			t.Skip("oversized input")
		}
		var vars map[string]any
		if err := json.Unmarshal([]byte(data), &vars); err != nil {
			t.Skip("not a JSON object")
		}
		env := fuzzEnv()
		for _, src := range templates {
			tmpl, err := env.FromString(src)
			if err != nil {
				t.Fatalf("a fixed template stopped compiling: %s: %v", src, err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), fuzzDeadline)
			start := time.Now()
			out, err := tmpl.RenderString(ctx, vars)
			took := time.Since(start)
			cancel()

			if took > fuzzWallClock {
				t.Fatalf("%s over %q took %s against a %s deadline",
					src, data, took.Round(time.Millisecond), fuzzDeadline)
			}
			if err != nil {
				requireClassifiable(t, "render", src+" over "+data, err)
				// RenderString is all or nothing.
				if out != "" {
					t.Fatalf("%s over %q failed with %v and returned %d bytes",
						src, data, err, len(out))
				}
				continue
			}
			if int64(len(out)) > fuzzMaxOutput {
				t.Fatalf("%s over %q returned %d bytes, past the %d byte bound",
					src, data, len(out), fuzzMaxOutput)
			}
			// The same data twice is the same document. Go maps have
			// no iteration order and this engine sorts their keys
			// rather than exposing one, so a context that rendered
			// differently between runs would mean an unsorted map
			// had reached the output -- reproducible once in fifty
			// runs and never in a test.
			if hasAddress(out) || strings.Contains(src, "random") {
				continue
			}
			again, err2 := renderAgain(tmpl, vars)
			if err2 == nil && !hasAddress(again) && again != out {
				t.Fatalf("%s over %q rendered differently twice:\n  %q\n  %q",
					src, data, out, again)
			}
		}
	})
}

func renderAgain(tmpl *gojja2.Template, vars map[string]any) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), fuzzDeadline)
	defer cancel()
	return tmpl.RenderString(ctx, vars)
}
