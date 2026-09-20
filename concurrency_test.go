// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/mgilbir/gojja2"
	"github.com/mgilbir/gojja2/value"
)

// counter is a host object a template can reach the methods of. It is the one
// thing the bridge cannot copy away, and the tests below say so deliberately.
type counter struct{ n int }

func (c *counter) Bump() int { c.n++; return c.n }

func renderStr(t *testing.T, tmpl *gojja2.Template, vars map[string]any) string {
	t.Helper()
	out, err := tmpl.RenderString(context.Background(), vars)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	return out
}

// TestRenderDoesNotMutateCallerData: a render must not write back into the
// map the caller handed it, however deep the value sits.
//
// This is a deliberate divergence from jinja2, which passes the caller's real
// objects and lets a template append to them -- see docs/divergences.md. A
// template is often the least trusted part of a program, and a filter that
// mutates in place (`|sort` used to, `do_indent` still does to a list) would
// otherwise reach into the caller's state.
func TestRenderDoesNotMutateCallerData(t *testing.T) {
	env := mustEnv(gojja2.WithExtensions("do"))
	for _, tc := range []struct {
		name, src string
		vars      func() (map[string]any, func() string)
		want      string // what the caller's own value must still be
	}{
		{"slice", `{% do xs.append(9) %}{{ xs }}`, func() (map[string]any, func() string) {
			xs := []any{1}
			return map[string]any{"xs": xs}, func() string { return fmt.Sprint(xs) }
		}, "[1]"},
		{"map", `{% do d.update({"b": 2}) %}{{ d }}`, func() (map[string]any, func() string) {
			d := map[string]any{"a": 1}
			return map[string]any{"d": d}, func() string { return fmt.Sprint(d) }
		}, "map[a:1]"},
		{"nested slice", `{% do d.inner.append(9) %}{{ d }}`, func() (map[string]any, func() string) {
			d := map[string]any{"inner": []any{1}}
			return map[string]any{"d": d}, func() string { return fmt.Sprint(d) }
		}, "map[inner:[1]]"},
		{"sort in place", `{{ xs|sort }}`, func() (map[string]any, func() string) {
			xs := []any{3, 1, 2}
			return map[string]any{"xs": xs}, func() string { return fmt.Sprint(xs) }
		}, "[3 1 2]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmpl, err := env.FromString(tc.src)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			vars, caller := tc.vars()
			first := renderStr(t, tmpl, vars)
			if got := caller(); got != tc.want {
				t.Errorf("caller's value became %s, want %s", got, tc.want)
			}
			// And the second render must see the same input as the
			// first, which is the property that matters in a server.
			if second := renderStr(t, tmpl, vars); second != first {
				t.Errorf("second render = %q, first = %q", second, first)
			}
		})
	}
}

// TestContextVariableIsOneValuePerRender: a render argument is converted on
// first use, so it has to be remembered. Converting it again on the second
// mention would hand out a second copy, and a change made through the first --
// which is legal, the copy is the render's own -- would vanish.
func TestContextVariableIsOneValuePerRender(t *testing.T) {
	env := mustEnv(gojja2.WithExtensions("do"))
	for _, tc := range []struct{ name, src, want string }{
		{"append is visible later in the render",
			`{% do xs.append(9) %}{{ xs }}`, "[1, 9]"},
		{"through a nested container",
			`{% do d.inner.append(9) %}{{ d.inner }}`, "[1, 9]"},
		{"two mentions are the same object",
			`{% do xs.append(9) %}{{ xs|length }}{{ xs|length }}`, "22"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmpl, err := env.FromString(tc.src)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			got := renderStr(t, tmpl, map[string]any{
				"xs": []any{1},
				"d":  map[string]any{"inner": []any{1}},
			})
			if got != tc.want {
				t.Errorf("%s = %q, want %q", tc.src, got, tc.want)
			}
		})
	}
}

// TestHostObjectIsShared is the other half: a pointer the caller exposed on
// purpose really is the caller's object, and a template calling its methods
// changes it. Copying that away would break every host object with state.
func TestHostObjectIsShared(t *testing.T) {
	env := mustEnv()
	tmpl, err := env.FromString(`{{ c.Bump() }}{{ c.Bump() }}`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	c := &counter{}
	if got := renderStr(t, tmpl, map[string]any{"c": c}); got != "12" {
		t.Errorf("first render = %q, want %q", got, "12")
	}
	if got := renderStr(t, tmpl, map[string]any{"c": c}); got != "34" {
		t.Errorf("second render = %q, want %q", got, "34")
	}
	if c.n != 4 {
		t.Errorf("counter = %d, want 4", c.n)
	}
}

// TestGlobalsPersistAcrossRenders pins the one place mutation is meant to
// survive, because jinja2 does the same: a global lives on the Environment.
func TestGlobalsPersistAcrossRenders(t *testing.T) {
	env := mustEnv(gojja2.WithExtensions("do"))
	env.AddGlobal("shared", value.FromGo([]any{1, 2}))
	tmpl, err := env.FromString(`{% do shared.append(9) %}{{ shared }}`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if got := renderStr(t, tmpl, nil); got != "[1, 2, 9]" {
		t.Errorf("first render = %q", got)
	}
	if got := renderStr(t, tmpl, nil); got != "[1, 2, 9, 9]" {
		t.Errorf("second render = %q, want the append to have persisted", got)
	}
}

// TestLiteralsAreRebuiltEachRender: a list written in the template is part of
// the compiled tree, which every render shares. Handing that value out
// directly would let one render's append be visible to the next.
func TestLiteralsAreRebuiltEachRender(t *testing.T) {
	env := mustEnv(gojja2.WithExtensions("do"))
	for _, tc := range []struct{ name, src, want string }{
		{"list", `{% set L = [1, 2] %}{% do L.append(9) %}{{ L }}`, "[1, 2, 9]"},
		{"dict", `{% set D = {"a": 1} %}{{ D.popitem() }}{{ D }}`, "('a', 1){}"},
		{"macro default", `{% macro m(xs=[]) %}{% do xs.append(1) %}{{ xs }}{% endmacro %}{{ m() }}{{ m() }}`, "[1][1]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmpl, err := env.FromString(tc.src)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			for i := range 3 {
				if got := renderStr(t, tmpl, nil); got != tc.want {
					t.Fatalf("render %d = %q, want %q", i+1, got, tc.want)
				}
			}
		})
	}
}

// TestConcurrentRendersDoNotInterfere renders one compiled template from many
// goroutines with different contexts. Anything a render keeps on the template
// rather than on its own state shows up here as another goroutine's output.
func TestConcurrentRendersDoNotInterfere(t *testing.T) {
	env := mustEnv(gojja2.WithExtensions("do"))
	tmpl, err := env.FromString(
		`{{ who }}|{% for i in range(8) %}{{ who }}{{ i }}{% endfor %}|` +
			`{% macro m(x) %}<{{ x }}>{% endmacro %}{{ m(who) }}|` +
			`{% set L = [] %}{% do L.append(who) %}{{ L }}`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	want := func(name string) string {
		var b strings.Builder
		b.WriteString(name + "|")
		for i := range 8 {
			fmt.Fprintf(&b, "%s%d", name, i)
		}
		fmt.Fprintf(&b, "|<%s>|['%s']", name, name)
		return b.String()
	}

	var wg sync.WaitGroup
	errCh := make(chan string, 256)
	for g := range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			name := fmt.Sprintf("g%02d", g)
			expect := want(name)
			for range 50 {
				out, err := tmpl.RenderString(context.Background(),
					map[string]any{"who": name})
				if err != nil {
					errCh <- fmt.Sprintf("%s: %v", name, err)
					return
				}
				if out != expect {
					errCh <- fmt.Sprintf("%s got %q want %q", name, out, expect)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for msg := range errCh {
		t.Error(msg)
	}
}

// TestConcurrentCompileAndRender drives one Environment from many goroutines,
// which is what a server does: the Environment is built once and shared, and
// its template cache is written by whichever request arrives first.
func TestConcurrentCompileAndRender(t *testing.T) {
	env := mustEnv(gojja2.WithLoader(gojja2.DictLoader{
		"a.txt": `{% block b %}A{% endblock %}`,
		"b.txt": `{% extends "a.txt" %}{% block b %}B{{ n }}{% endblock %}`,
	}))
	var wg sync.WaitGroup
	errCh := make(chan string, 256)
	for g := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 25 {
				// A fresh source each time exercises compiling;
				// the loader path exercises the cache.
				src := fmt.Sprintf(`{%% set v = %d %%}{{ v * 2 }}`, g)
				tmpl, err := env.FromString(src)
				if err != nil {
					errCh <- err.Error()
					return
				}
				if got, want := renderOK(tmpl), fmt.Sprint(g*2); got != want {
					errCh <- fmt.Sprintf("got %q want %q", got, want)
					return
				}
				got, err := env.GetTemplate("b.txt")
				if err != nil {
					errCh <- err.Error()
					return
				}
				out, err := got.RenderString(context.Background(),
					map[string]any{"n": i})
				if err != nil {
					errCh <- err.Error()
					return
				}
				if out != fmt.Sprintf("B%d", i) {
					errCh <- fmt.Sprintf("inherited render = %q", out)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for msg := range errCh {
		t.Error(msg)
	}
}

func renderOK(t *gojja2.Template) string {
	out, err := t.RenderString(context.Background(), nil)
	if err != nil {
		return "ERR:" + err.Error()
	}
	return out
}

// TestFrameLocalsCacheIsStableAndConcurrent: the names a frame owns are
// computed once per AST node and cached on the template, so the answer must not
// depend on which render populated the entry or how many have run.
//
// What the cache could get wrong -- handing one frame another's locals -- is
// already caught by TestFrameLocalsAliasTheEnclosingFrame and the corpus, which
// both fail if every frame is made to share one entry. What is new here is that
// the entry is written by whichever render reaches the frame first, and read by
// every render after it, including ones on other goroutines.
func TestFrameLocalsCacheIsStableAndConcurrent(t *testing.T) {
	env := mustEnv()
	const src = `` +
		`{% macro a() %}[{{ x }}{% set x = "A" %}{{ x }}]{% endmacro %}` +
		`{% macro b() %}[{{ x }}]{% endmacro %}` +
		`{% for i in [1] %}[{{ y }}{% set y = "L" %}{{ y }}]{% endfor %}` +
		`{% for i in [1] %}[{{ y }}]{% endfor %}` +
		`{{ a() }}{{ b() }}{{ a() }}{{ b() }}`
	tmpl, err := env.FromString(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	// A frame that owns a name is still handed the enclosing binding at
	// entry, so each reads the context's value before its own assignment
	// and the frame that does not assign reads it unchanged. Confirmed
	// against CPython jinja2, which renders exactly this.
	const want = `[ctxYL][ctxY][ctxXA][ctxX][ctxXA][ctxX]`
	vars := map[string]any{"x": "ctxX", "y": "ctxY"}

	// Repeated, because the first render populates the cache and every
	// render after it reads one.
	for i := range 4 {
		if got := renderStr(t, tmpl, vars); got != want {
			t.Fatalf("render %d = %q, want %q", i+1, got, want)
		}
	}

	// And concurrently, since the cache is written by whichever render
	// reaches the frame first.
	var wg sync.WaitGroup
	errCh := make(chan string, 64)
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 25 {
				out, err := tmpl.RenderString(context.Background(), vars)
				if err != nil {
					errCh <- err.Error()
					return
				}
				if out != want {
					errCh <- fmt.Sprintf("got %q want %q", out, want)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for msg := range errCh {
		t.Error(msg)
	}
}
