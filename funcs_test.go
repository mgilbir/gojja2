// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mgilbir/gojja2"
	"github.com/mgilbir/gojja2/errs"
	"github.com/mgilbir/gojja2/value"
)

// funcEnv registers Go functions shaped like four Python ones:
//
//	def tw(value, count, suffix): ...
//	def one(value): ...
//	def va(value, sep, *rest): ...
//	def mo(value, of): return value % of == 0   (a test)
func funcEnv() *gojja2.Environment {
	env := mustEnv()
	env.AddFilter("tw", gojja2.FilterFunc("tw", func(v string, count int, suffix string) string {
		w := strings.Fields(v)
		return strings.Join(w[:min(count, len(w))], " ") + suffix
	}, "count", "suffix"))
	env.AddFilter("one", gojja2.FilterFunc("one", strings.ToUpper))
	env.AddFilter("va", gojja2.FilterFunc("va", func(v any, sep string, rest ...any) string {
		parts := []string{fmt.Sprint(v)}
		for _, r := range rest {
			parts = append(parts, fmt.Sprint(r))
		}
		return strings.Join(parts, sep)
	}, "sep"))
	env.AddTest("mo", gojja2.TestFunc("mo", func(v, of int) bool { return v%of == 0 }, "of"))
	return env
}

// TestFilterFuncBindsLikePython renders each template with the Go functions
// above and expects what CPython jinja2 rendered, or raised, with the Python
// functions they stand for registered under the same names.
func TestFilterFuncBindsLikePython(t *testing.T) {
	env := funcEnv()
	for _, tc := range []struct{ src, want, err string }{
		{"{{ s|tw(2, \"...\") }}", "a b...", ""},
		{"{{ s|tw(2, suffix=\"!\") }}", "a b!", ""},
		{"{{ s|tw(count=1, suffix=\"?\") }}", "a?", ""},
		{"{{ s|tw(2) }}", "", "tw() missing 1 required positional argument: 'suffix'"},
		{"{{ s|tw() }}", "", "tw() missing 2 required positional arguments: 'count' and 'suffix'"},
		{"{{ s|tw(1,2,3) }}", "", "tw() takes 3 positional arguments but 4 were given"},
		{"{{ s|tw(2, \"x\", suffix=\"y\") }}", "", "tw() got multiple values for argument 'suffix'"},
		{"{{ s|tw(2, \"x\", width=3) }}", "", "tw() got an unexpected keyword argument 'width'"},
		{"{{ s|tw(2, \"x\", value=3) }}", "", "tw() got multiple values for argument 'value'"},
		{"{{ s|one }}", "A B C D", ""},
		{"{{ s|one(1) }}", "", "one() takes 1 positional argument but 2 were given"},
		{"{{ s|one(x=1) }}", "", "one() got an unexpected keyword argument 'x'"},
		{"{{ s|va(\"-\") }}", "a b c d", ""},
		{"{{ s|va(\"-\", 1, 2) }}", "a b c d-1-2", ""},
		{"{{ s|va }}", "", "va() missing 1 required positional argument: 'sep'"},
		{"{{ s|va(sep=\"+\") }}", "a b c d", ""},
		{"{{ s|va(1, sep=\"+\") }}", "", "va() got multiple values for argument 'sep'"},
		{"{{ n is mo 3 }}", "True", ""},
		{"{{ n is mo(4) }}", "False", ""},
		{"{{ n is mo }}", "", "mo() missing 1 required positional argument: 'of'"},
		{"{{ n is mo(of=3) }}", "True", ""},
		{"{{ [1,2,3,4,6]|select(\"mo\", 2)|list }}", "[2, 4, 6]", ""},
		{"{{ [1,2]|map(\"tw\")|list }}", "", "tw() missing 2 required positional arguments: 'count' and 'suffix'"},
	} {
		tmpl, err := env.FromString(tc.src)
		if err != nil {
			t.Fatalf("%s: %v", tc.src, err)
		}
		got, err := tmpl.RenderString(context.Background(), map[string]any{"s": "a b c d", "n": 9})
		if tc.err == "" {
			if err != nil || got != tc.want {
				t.Errorf("%s = %q, %v; want %q", tc.src, got, err, tc.want)
			}
			continue
		}
		var e *errs.Error
		if !errors.As(err, &e) || e.Kind != errs.TypeError || e.Msg != tc.err {
			t.Errorf("%s: %v; want TypeError %q", tc.src, err, tc.err)
		}
	}
}

// TestFilterFuncTypes covers what Python has no counterpart for: the Go types.
func TestFilterFuncTypes(t *testing.T) {
	type point struct{ X, Y int }
	env := funcEnv()
	env.AddFilter("narrow", gojja2.FilterFunc("narrow", func(b uint8) uint8 { return b + 1 }))
	env.AddFilter("raw", gojja2.FilterFunc("raw", func(v value.Value, w value.Value) value.Value {
		return value.String(v.TypeName() + "/" + w.TypeName())
	}, "w"))
	env.AddFilter("point", gojja2.FilterFunc("point", func(x, y int) point { return point{x, y} }, "y"))
	env.AddFilter("fails", gojja2.FilterFunc("fails", func(string) (string, error) {
		return "", errs.New(errs.ValueError, "no good")
	}))
	env.AddFilter("boom", gojja2.FilterFunc("boom", func(xs []any) any { return xs[5] }))
	env.AddFilter("where", gojja2.FilterFunc("where", func(s *gojja2.State, v string) string {
		return v + "@" + s.Name()
	}))
	for _, tc := range []struct{ src, want, err string }{
		// A number converts to a narrower type when nothing is lost.
		{src: "{{ 41|narrow }}", want: "42"},
		{src: "{{ 300|narrow }}", err: "narrow(): argument 1 does not fit in uint8"},
		// A typed parameter is a check Python would not make: CPython
		// renders ['1', '2'] for a tw that calls str() on its value.
		{src: `{{ [1,2]|map("tw", 1, "")|list }}`, err: "tw(): argument 1 must be string, not int"},
		{src: "{{ s|tw('x', '') }}", err: "tw(): argument 2 must be int, not str"},
		// value.Value passes through unconverted, either way.
		{src: "{{ none|raw(nope) }}", want: "NoneType/Undefined"},
		// A struct result converts as a render argument does.
		{src: "{{ (1|point(2)).Y }}", want: "2"},
		{src: "{{ s|fails }}", err: "no good"},
		{src: "{{ [1]|boom }}", err: "boom() panicked: runtime error: index out of range [5] with length 1"},
		{src: "{{ s|where }}", want: "a b c d@t.txt"},
	} {
		tmpl, err := env.FromNamedString("t.txt", tc.src)
		if err != nil {
			t.Fatalf("%s: %v", tc.src, err)
		}
		got, err := tmpl.RenderString(context.Background(), map[string]any{"s": "a b c d"})
		if tc.err == "" {
			if err != nil || got != tc.want {
				t.Errorf("%s = %q, %v; want %q", tc.src, got, err, tc.want)
			}
			continue
		}
		var e *errs.Error
		if !errors.As(err, &e) || e.Msg != tc.err {
			t.Errorf("%s: %v; want %q", tc.src, err, tc.err)
		}
	}
}

// TestFilterFuncRefusesABadFunction checks the adapter panics at setup, with
// the reason, for every shape it cannot bind.
func TestFilterFuncRefusesABadFunction(t *testing.T) {
	for name, fn := range map[string]func(){
		"not a function":      func() { gojja2.FilterFunc("f", 3) },
		"nil function":        func() { gojja2.FilterFunc("f", (func(int) int)(nil)) },
		"no value":            func() { gojja2.FilterFunc("f", func() int { return 0 }) },
		"only a state":        func() { gojja2.FilterFunc("f", func(*gojja2.State) int { return 0 }) },
		"only a variadic":     func() { gojja2.FilterFunc("f", func(...int) int { return 0 }) },
		"too few names":       func() { gojja2.FilterFunc("f", func(a, b int) int { return 0 }) },
		"too many names":      func() { gojja2.FilterFunc("f", func(a int) int { return 0 }, "b") },
		"repeated name":       func() { gojja2.FilterFunc("f", func(a, b, c int) int { return 0 }, "b", "b") },
		"empty name":          func() { gojja2.FilterFunc("f", func(a, b int) int { return 0 }, "") },
		"no result":           func() { gojja2.FilterFunc("f", func(int) {}) },
		"second not an error": func() { gojja2.FilterFunc("f", func(int) (int, int) { return 0, 0 }) },
		"test not a bool":     func() { gojja2.TestFunc("f", func(int) int { return 0 }) },
	} {
		func() {
			defer func() {
				r := recover()
				if s, _ := r.(string); !strings.HasPrefix(s, "gojja2.") {
					t.Errorf("%s: recovered %v, want the adapter's own panic", name, r)
				}
			}()
			fn()
		}()
	}
}
