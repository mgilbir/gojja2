// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/mgilbir/gojja2"
	"github.com/mgilbir/gojja2/value"
)

// sorted(Environment().filters) and sorted(Environment().tests) under jinja2
// 3.1.6, recorded verbatim from the oracle.
var (
	jinja2FilterNames = strings.Fields(`abs attr batch capitalize center count d default dictsort e escape
		filesizeformat first float forceescape format groupby indent int items join last length list
		lower map max min pprint random reject rejectattr replace reverse round safe select selectattr
		slice sort string striptags sum title tojson trim truncate unique upper urlencode urlize
		wordcount wordwrap xmlattr`)
	jinja2TestNames = strings.Fields(`!= < <= == > >= boolean callable defined divisibleby eq equalto
		escaped even false filter float ge greaterthan gt in integer iterable le lessthan lower lt
		mapping ne none number odd sameas sequence string test true undefined upper`)
)

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// A fresh environment has jinja2's registries, name for name.
func TestRegistriesMatchJinja2(t *testing.T) {
	env := mustEnv()
	if got := sortedKeys(env.Filters()); !reflect.DeepEqual(got, jinja2FilterNames) {
		t.Errorf("Filters()\n got %q\nwant %q", got, jinja2FilterNames)
	}
	if got := sortedKeys(env.Tests()); !reflect.DeepEqual(got, jinja2TestNames) {
		t.Errorf("Tests()\n got %q\nwant %q", got, jinja2TestNames)
	}
}

// Filters and Tests hand back copies, as Globals does: what is registered shows
// in them, and writing to them changes nothing a template sees.
func TestRegistryAccessorsAreCopies(t *testing.T) {
	env := mustEnv()
	env.AddFilter("shout", func(_ *gojja2.State, v value.Value, _ *value.CallArgs) (value.Value, error) {
		return value.String(strings.ToUpper(value.Str(v)) + "!"), nil
	})
	env.AddTest("short", func(_ *gojja2.State, v value.Value, _ *value.CallArgs) (bool, error) {
		return len(value.Str(v)) < 3, nil
	})

	filters, tests := env.Filters(), env.Tests()
	shout, ok := filters["shout"]
	if !ok {
		t.Fatal("Filters() is missing a filter added with AddFilter")
	}
	if v, err := shout(nil, value.String("hi"), nil); err != nil || value.Str(v) != "HI!" {
		t.Errorf("the returned filter gave %v, %v", v, err)
	}
	if _, ok := tests["short"]; !ok {
		t.Fatal("Tests() is missing a test added with AddTest")
	}

	delete(filters, "upper")
	delete(filters, "shout")
	delete(tests, "short")
	filters["nope"] = shout
	tests["nope"] = tests["odd"]

	tmpl, err := env.FromString(`{{ x|upper }}{{ x|shout }}{{ x is short }}`)
	if err != nil {
		t.Fatalf("deleting from the copies reached the environment: %v", err)
	}
	if out, err := tmpl.RenderString(context.Background(), map[string]any{"x": "ab"}); err != nil || out != "ABAB!True" {
		t.Errorf("got %q, %v", out, err)
	}
	if _, err := env.FromString(`{{ x|nope }}`); err == nil {
		t.Error("a filter added to the copy is visible to templates")
	}
	if _, err := env.FromString(`{{ x is nope }}`); err == nil {
		t.Error("a test added to the copy is visible to templates")
	}
	if _, ok := env.Filters()["nope"]; ok {
		t.Error("a later Filters() shows a write to an earlier copy")
	}
}
