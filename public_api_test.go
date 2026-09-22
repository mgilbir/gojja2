// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/mgilbir/gojja2"
	"github.com/mgilbir/gojja2/errs"
	"github.com/mgilbir/gojja2/value"
)

// The exported surface a coverage run found at 0%.
//
// Nothing here had ever been called by a test, which for a conformance project
// is the same as never having been checked against CPython: the corpus grades
// what a *template* can reach, and an environment method or a State accessor is
// reached from Go. The answers below are CPython's, taken from
// jinja2.Environment.select_template rather than from reading this code.

func TestSelectTemplateMatchesJinja2(t *testing.T) {
	env, err := gojja2.New(gojja2.WithLoader(gojja2.DictLoader(map[string]string{
		"real.txt":  "REAL",
		"other.txt": "OTHER",
	})))
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	for _, tc := range []struct {
		name    string
		names   []string
		want    string // rendered output, or "" when it must fail
		wantErr string
	}{
		{"empty", nil, "", "Tried to select from an empty list of templates."},
		{"empty slice", []string{}, "", "Tried to select from an empty list of templates."},
		{"one missing", []string{"nope.txt"}, "",
			"none of the templates given were found: nope.txt"},
		{"two missing", []string{"nope.txt", "gone.txt"}, "",
			"none of the templates given were found: nope.txt, gone.txt"},
		{"first hits", []string{"real.txt", "other.txt"}, "REAL", ""},
		{"second hits", []string{"nope.txt", "real.txt"}, "REAL", ""},
		{"repeated miss", []string{"nope.txt", "nope.txt", "real.txt"}, "REAL", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmpl, err := env.SelectTemplate(tc.names)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("selected %q, want error %q", tmpl.Name(), tc.wantErr)
				}
				if got := err.Error(); got != tc.wantErr {
					t.Errorf("err = %q\nwant %q", got, tc.wantErr)
				}
				if !errors.Is(err, errs.TemplatesNotFound) {
					t.Errorf("err is not TemplatesNotFound: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("select: %v", err)
			}
			out, err := tmpl.RenderString(t.Context(), nil)
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			if out != tc.want {
				t.Errorf("rendered %q, want %q", out, tc.want)
			}
			// The chosen template keeps its own name, not the list's.
			if tmpl.Name() != "real.txt" {
				t.Errorf("Name() = %q, want %q", tmpl.Name(), "real.txt")
			}
		})
	}
}

// What a filter's State can see, graded against jinja2's own answer.
//
// State.Resolve is the counterpart of `context.resolve` on a `@pass_context`
// filter, and the two agree on all four questions below -- including the one
// that is easy to get wrong in either direction: a loop variable is a *local*
// and is not in the context, so neither engine can resolve it.
//
// These are the extension API, which docs/extending.md tells an author to reach
// for, and every one of them was at 0% coverage.
func TestStateResolveMatchesJinja2(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		// jinja2: '[who=world/T]'
		{"render argument", `{{ who|r('who') }}`, "[who=world/T]"},
		// jinja2: '[s1=S/T]'
		{"top-level set", `{% set s1 = 'S' %}{{ s1|r('s1') }}`, "[s1=S/T]"},
		// jinja2: '[i=/F]' -- a loop variable is a local, not context.
		{"loop variable", `{% for i in [1] %}{{ i|r('i') }}{% endfor %}`, "[i=/F]"},
		// jinja2: '[nope=/F]'
		{"unset name", `{{ who|r('nope') }}`, "[nope=/F]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, err := gojja2.New()
			if err != nil {
				t.Fatalf("new: %v", err)
			}
			env.AddFilter("r", func(s *gojja2.State, _ value.Value, a *value.CallArgs) (value.Value, error) {
				n, _ := a.Arg(0)
				v, found := s.Resolve(value.Str(n))
				return value.String("[" + value.Str(n) + "=" + value.Str(v) + "/" + foundText(found) + "]"), nil
			})
			tmpl, err := env.FromString(tc.src)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			out, err := tmpl.RenderString(t.Context(), map[string]any{"who": "world"})
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			if out != tc.want {
				t.Errorf("got %q, want %q", out, tc.want)
			}
		})
	}
}

// A filter whose input is constant is folded, and the State it is handed then
// describes no render at all.
//
// Worth pinning because it is not obvious and it is easy to write a test that
// accidentally measures it: `{{ 0|f }}` never runs at render time, so a filter
// asking about the render there sees globals and nothing else. That is what
// docs/extending.md means by a filter having to cope with no render in
// progress.
func TestAFoldedFilterSeesNoRender(t *testing.T) {
	env, err := gojja2.New()
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	env.AddGlobal("g", value.String("G"))
	env.AddFilter("r", func(s *gojja2.State, _ value.Value, a *value.CallArgs) (value.Value, error) {
		n, _ := a.Arg(0)
		v, found := s.Resolve(value.Str(n))
		return value.String("[" + value.Str(v) + "/" + foundText(found) + "]"), nil
	})
	tmpl, err := env.FromString(`{{ 0|r('who') }}{{ 0|r('g') }}`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	out, err := tmpl.RenderString(t.Context(), map[string]any{"who": "world"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	// The render argument is invisible; a global, which belongs to the
	// environment rather than the render, is not.
	if want := "[/F][G/T]"; out != want {
		t.Errorf("got %q, want %q", out, want)
	}
}

// State.Name, Autoescape, Env and Context describe the render in progress.
func TestStateDescribesTheRender(t *testing.T) {
	for _, tc := range []struct {
		name       string
		autoescape bool
		want       string
	}{
		{"plain", false, "name=page.txt ae=false env=ok ctx=ok"},
		{"autoescaping", true, "name=page.txt ae=true env=ok ctx=ok"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, err := gojja2.New(
				gojja2.WithAutoescape(tc.autoescape),
				gojja2.WithLoader(gojja2.DictLoader(map[string]string{
					"page.txt": "{{ who|describe }}",
				})),
			)
			if err != nil {
				t.Fatalf("new: %v", err)
			}
			env.AddFilter("describe", func(s *gojja2.State, _ value.Value, _ *value.CallArgs) (value.Value, error) {
				envOK, ctxOK := "no", "no"
				if s.Env() != nil {
					envOK = "ok"
				}
				if s.Context() != nil {
					ctxOK = "ok"
				}
				return value.String(strings.Join([]string{
					"name=" + s.Name(),
					"ae=" + boolText(s.Autoescape()),
					"env=" + envOK,
					"ctx=" + ctxOK,
				}, " ")), nil
			})
			tmpl, err := env.GetTemplate("page.txt")
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			out, err := tmpl.RenderString(t.Context(), map[string]any{"who": "world"})
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			if out != tc.want {
				t.Errorf("\n got %q\nwant %q", out, tc.want)
			}
		})
	}
}

// foundText transcribes "did resolve find it" the way the jinja2 probe this
// was graded against printed it, so the wants above are that probe's output.
func foundText(found bool) string {
	if found {
		return "T"
	}
	return "F"
}

func boolText(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// State.Name reports the template actually executing, not the one that started
// the render -- which is the whole reason a filter would ask.
func TestStateNameFollowsAnInclude(t *testing.T) {
	env, err := gojja2.New(gojja2.WithLoader(gojja2.DictLoader(map[string]string{
		"outer.txt": "[{{ who|here }}][{% include 'inner.txt' %}]",
		"inner.txt": "{{ who|here }}",
	})))
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	env.AddFilter("here", func(s *gojja2.State, _ value.Value, _ *value.CallArgs) (value.Value, error) {
		return value.String(s.Name()), nil
	})
	tmpl, err := env.GetTemplate("outer.txt")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	out, err := tmpl.RenderString(t.Context(), map[string]any{"who": "w"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if want := "[outer.txt][inner.txt]"; out != want {
		t.Errorf("got %q, want %q", out, want)
	}
}

// Error.Detail is what a human debugging a template is shown.
func TestErrorDetailNamesTheTemplateAndLine(t *testing.T) {
	env, err := gojja2.New(gojja2.WithLoader(gojja2.DictLoader(map[string]string{
		"broken.txt": "line one\n{{ 1/0 }}\n",
	})))
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	tmpl, err := env.GetTemplate("broken.txt")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if _, err = tmpl.RenderString(t.Context(), nil); err == nil {
		t.Fatal("a division by zero rendered")
	}
	var e *errs.Error
	if !errors.As(err, &e) {
		t.Fatalf("not an *errs.Error: %v", err)
	}
	detail := e.Detail()
	for _, want := range []string{"ZeroDivisionError", "division by zero", "broken.txt", "2"} {
		if !strings.Contains(detail, want) {
			t.Errorf("Detail() = %q, missing %q", detail, want)
		}
	}
}
