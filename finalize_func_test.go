// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mgilbir/gojja2"
	"github.com/mgilbir/gojja2/errs"
	"github.com/mgilbir/gojja2/value"
)

// WithFinalizeFunc is jinja2's finalize decorated with @pass_context (or
// @pass_eval_context, which the compiler treats the same way), and
// WithFinalize is the undecorated one. Every want below is what jinja2 3.1.6
// rendered for the same template, recorded verbatim from the oracle -- one case
// per capped subprocess -- with the Python finalize spelled as the Go one here
// is: "angle" is `"<%r>" % (v,)`, "upper" is `str(v).upper()`, "ctx" is
// `"%s[%s]" % (v, context.resolve("suffix"))`, and "raise" raises
// `Boom("finalize refused %r" % (v,))`.
//
// The cases that matter most are the constant prints. jinja2 folds a constant
// print at compile time only when it can call the finalize there, which it
// cannot for a decorated one, so `{{ 'a' }}` under autoescaping is finalized
// *after* escaping by a plain finalize and *before* it by a context one.

// errBoom is the Go side of the oracle's Boom exception.
var errBoom = errors.New("boom")

type boomError struct{ msg string }

func (b *boomError) Error() string { return b.msg }
func (b *boomError) Unwrap() error { return errBoom }

func finalizeByName(name string) gojja2.FinalizeFunc {
	switch name {
	case "angle":
		return func(_ *gojja2.State, v value.Value) (value.Value, error) { return angle(v), nil }
	case "upper":
		return func(_ *gojja2.State, v value.Value) (value.Value, error) {
			return value.String(strings.ToUpper(value.Str(v))), nil
		}
	case "raise":
		return func(_ *gojja2.State, v value.Value) (value.Value, error) {
			return value.Undefined, &boomError{"finalize refused " + value.Repr(v)}
		}
	case "raise_none":
		return func(_ *gojja2.State, v value.Value) (value.Value, error) {
			if v.IsNone() {
				return value.Undefined, &boomError{"none printed"}
			}
			return v, nil
		}
	case "ctx":
		return func(s *gojja2.State, v value.Value) (value.Value, error) {
			suffix, _ := s.Resolve("suffix")
			return value.String(value.Str(v) + "[" + value.Str(suffix) + "]"), nil
		}
	}
	panic("unknown finalize " + name)
}

// outcome is a render's result in the oracle's spelling: the text, or the
// exception class and message.
func outcome(out string, err error) string {
	if err == nil {
		return out
	}
	var boom *boomError
	if errors.As(err, &boom) {
		return "Boom: " + err.Error()
	}
	return errs.KindOf(err).String() + ": " + err.Error()
}

func TestFinalizeFuncAgainstJinja2(t *testing.T) {
	for _, tc := range []struct {
		fn         string
		autoescape bool
		src        string
		ctx        map[string]any
		templates  map[string]string // src names the one to render when set
		want       string
	}{
		// A constant print is not folded, so the finalize sees the
		// value and escaping comes after. @pass_eval_context gives the
		// same answer as @pass_context in every one of these.
		{"angle", true, `{{ 'a' }}`, nil, nil, `&lt;&#39;a&#39;&gt;`},
		{"angle", false, `{% autoescape true %}{{ 'a' }}{% endautoescape %}`, nil, nil, `&lt;&#39;a&#39;&gt;`},
		{"upper", true, `{{ '<i>' }}|{% set v = '<i>' %}{{ v }}`, nil, nil, `&lt;I&gt;|&lt;I&gt;`},
		{"upper", true, `{{ '<i>'|safe }}`, nil, nil, `&lt;I&gt;`},
		// The print fold is what turns a failed constant subscript into
		// an undefined. Without it the subscript runs for real.
		{"angle", false, `{{ 0[1:] }}`, nil, nil, `TypeError: 'int' object is not subscriptable`},
		{"angle", false, `{{ 'a'|upper }}{{ 1 + 2 }}{{ none }}`, nil, nil, `<'A'><3><None>`},
		{"angle", true, `{{ '<b>' ~ x }}`, map[string]any{"x": "&"}, nil, `&lt;&#39;&lt;b&gt;&amp;&#39;&gt;`},
		// The context: what context.resolve sees, which is the render
		// arguments and top-level assignments but not a loop variable.
		{"ctx", false, `{{ x }} {{ 'lit' }} {{ 7 }}`, map[string]any{"x": "X", "suffix": "S"}, nil, `X[S] lit[S] 7[S]`},
		{"ctx", false, `{% set suffix = 'local' %}{{ 'lit' }}`, map[string]any{"suffix": "S"}, nil, `lit[local]`},
		{"ctx", false, `{% for suffix in ['loop'] %}{{ 'lit' }}{% endfor %}`, map[string]any{"suffix": "S"}, nil, `lit[S]`},
		{"ctx", false, `{{ 'lit' }}`, nil, nil, `lit[]`},
		{"ctx", false, "page", map[string]any{"suffix": "S"},
			map[string]string{"page": `{% include 'inc' %}`, "inc": `{{ 'i' }}`}, `i[S]`},
		{"ctx", false, "page", map[string]any{"suffix": "S"},
			map[string]string{"page": `{% set suffix = 'P' %}{% include 'inc' without context %}`, "inc": `{{ 'i' }}`}, `i[]`},
		{"ctx", false, "page", map[string]any{"suffix": "S"},
			map[string]string{"page": `{% set suffix = 'P' %}{% include 'inc' %}`, "inc": `{{ 'i' }}`}, `i[P]`},
		// Failing: a constant print fails at render time, not compile
		// time, and a print that never runs never calls it.
		{"raise", false, `a{{ x }}b`, map[string]any{"x": 1}, nil, `Boom: finalize refused 1`},
		{"raise", false, `a{{ 1 }}b`, nil, nil, `Boom: finalize refused 1`},
		{"raise", false, `plain text only {% if false %}{{ x }}{% endif %}`, nil, nil, `plain text only `},
		{"raise", false, `{% for i in [1,2] %}{{ i }}{% endfor %}`, nil, nil, `Boom: finalize refused 1`},
		{"raise_none", true, `{{ none }}`, nil, nil, `Boom: none printed`},
		// Only print tags are finalized; captured output is not.
		{"angle", false, `{% macro m() %}{{ 1 }}{% endmacro %}{{ m() }}`, nil, nil, `<'<1>'>`},
		{"angle", false, `{% filter upper %}{{ 'x' }}plain{% endfilter %}`, nil, nil, `<'X'>PLAIN`},
		{"angle", false, `{% set s %}{{ 'x' }}{% endset %}{{ s }}`, nil, nil, `<"<'x'>">`},
	} {
		t.Run(tc.src, func(t *testing.T) {
			opts := []gojja2.Option{gojja2.WithFinalizeFunc(finalizeByName(tc.fn)),
				gojja2.WithAutoescape(tc.autoescape)}
			if tc.templates != nil {
				opts = append(opts, gojja2.WithLoader(gojja2.DictLoader(tc.templates)))
			}
			env := mustEnv(opts...)
			var tmpl *gojja2.Template
			var err error
			if tc.templates != nil {
				tmpl, err = env.GetTemplate(tc.src)
			} else {
				tmpl, err = env.FromString(tc.src)
			}
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			out, err := tmpl.RenderString(context.Background(), tc.ctx)
			if got := outcome(out, err); got != tc.want {
				t.Errorf("%s with %s\n got %q\nwant %q", tc.src, tc.fn, got, tc.want)
			}
		})
	}
}

// The undecorated finalize against the same oracle, for the constant prints
// the context finalize answers differently: here jinja2 does fold them.
func TestFinalizeFoldsConstantPrintsAgainstJinja2(t *testing.T) {
	upper := func(v value.Value) value.Value { return value.String(strings.ToUpper(value.Str(v))) }
	for _, tc := range []struct {
		fn         func(value.Value) value.Value
		autoescape bool
		src, want  string
	}{
		{angle, true, `{{ 'a' }}`, `<Markup('a')>`},
		{angle, false, `{% autoescape true %}{{ 'a' }}{% endautoescape %}`, `<Markup('a')>`},
		{upper, true, `{{ '<i>' }}|{% set v = '<i>' %}{{ v }}`, `&LT;I&GT;|&lt;I&gt;`},
		{angle, false, `{{ 0[1:] }}`, `<Undefined>`},
	} {
		tmpl, err := mustEnv(gojja2.WithFinalize(tc.fn), gojja2.WithAutoescape(tc.autoescape)).FromString(tc.src)
		if err != nil {
			t.Fatalf("FromString(%q): %v", tc.src, err)
		}
		out, err := tmpl.RenderString(context.Background(), nil)
		if got := outcome(out, err); got != tc.want {
			t.Errorf("%s\n got %q\nwant %q", tc.src, got, tc.want)
		}
	}
}

// The error a finalize returns is the render's, unchanged, so a host can find
// its own error in it.
func TestFinalizeFuncErrorIsClassifiable(t *testing.T) {
	tmpl, err := mustEnv(gojja2.WithFinalizeFunc(finalizeByName("raise"))).FromString("\n{{ x }}")
	if err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	err = tmpl.Render(context.Background(), &buf, map[string]any{"x": 2})
	if !errors.Is(err, errBoom) {
		t.Fatalf("errors.Is(%v, errBoom) = false", err)
	}
	var boom *boomError
	if !errors.As(err, &boom) || boom.msg != "finalize refused 2" {
		t.Errorf("errors.As found %v", boom)
	}
	// An errs.Error it returns keeps its class and gains the location.
	tmpl, err = mustEnv(gojja2.WithFinalizeFunc(func(*gojja2.State, value.Value) (value.Value, error) {
		return value.Undefined, errs.New(errs.ValueError, "bad")
	})).FromNamedString("t.txt", "a\n{{ 1 }}")
	if err != nil {
		t.Fatal(err)
	}
	_, err = tmpl.RenderString(context.Background(), nil)
	var e *errs.Error
	if !errors.Is(err, errs.ValueError) || !errors.As(err, &e) || e.Line != 2 || e.Name != "t.txt" {
		t.Errorf("got %#v, want a ValueError at t.txt:2", err)
	}
}

// One finalize at a time, as jinja2 has one: the later option wins, and nil
// removes it.
func TestFinalizeOptionsReplaceEachOther(t *testing.T) {
	ctxFn := func(_ *gojja2.State, v value.Value) (value.Value, error) {
		return value.String("ctx:" + value.Str(v)), nil
	}
	plain := func(v value.Value) value.Value { return value.String("plain:" + value.Str(v)) }
	for _, tc := range []struct {
		opts []gojja2.Option
		want string
	}{
		{[]gojja2.Option{gojja2.WithFinalize(plain), gojja2.WithFinalizeFunc(ctxFn)}, "ctx:v"},
		{[]gojja2.Option{gojja2.WithFinalizeFunc(ctxFn), gojja2.WithFinalize(plain)}, "plain:v"},
		{[]gojja2.Option{gojja2.WithFinalize(plain), gojja2.WithFinalizeFunc(nil)}, "v"},
	} {
		tmpl, err := mustEnv(tc.opts...).FromString("{{ x }}")
		if err != nil {
			t.Fatal(err)
		}
		out, err := tmpl.RenderString(context.Background(), map[string]any{"x": "v"})
		if err != nil || out != tc.want {
			t.Errorf("got %q, %v; want %q", out, err, tc.want)
		}
	}
}

// A context finalize is called once per printed value, at render time, and
// never while compiling -- which a counter shows directly.
func TestFinalizeFuncNeverRunsAtCompileTime(t *testing.T) {
	calls := 0
	env := mustEnv(gojja2.WithFinalizeFunc(func(_ *gojja2.State, v value.Value) (value.Value, error) {
		calls++
		return v, nil
	}))
	tmpl, err := env.FromString(`{{ 1 }}{{ 'a' ~ 'b' }}text{{ none }}`)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("compiling called the finalize %d times", calls)
	}
	for range 2 {
		if _, err := tmpl.RenderString(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 6 {
		t.Errorf("two renders called it %d times, want 6", calls)
	}
}
