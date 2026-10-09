// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/mgilbir/gojja2/errs"
	"github.com/mgilbir/gojja2/value"
)

// Environment.overlay in jinja2 3.1.6, as the oracle answers it, and what each
// test below is grading against:
//
//   - the overlay's cache is copy_cache(parent.cache): empty, same capacity,
//     and replaced by create_cache(n) when cache_size is given. A template
//     the parent has loaded is compiled again by the overlay, and its
//     .environment is the overlay;
//   - an include, an extends and a pass_environment filter folded at compile
//     time all see the overlay's settings, not the parent's;
//   - filters, tests, globals and policies are the parent's dict objects, so
//     a write through either is seen by both. gojja2 copies on the overlay's
//     first write instead (docs/divergences.md);
//   - the overlay is checked like a new environment: colliding delimiters and
//     a bad newline_sequence are refused.

// overlaySources is the loader every overlay test starts from.
func overlaySources() DictLoader {
	return DictLoader{
		"fold.html":     `{{ '<i>' }}|{{ x }}`,
		"inc.html":      `{{ x }}|{% include 'leaf.html' %}`,
		"leaf.html":     `[{{ x }}]`,
		"child.html":    `{% extends 'base.html' %}{% block b %}<{{ g }}>{% endblock %}`,
		"base.html":     `B{{ g }}{% block b %}{% endblock %}`,
		"imp.html":      `{% import 'macros.html' as m %}{{ m.show() }}`,
		"macros.html":   `{% macro show() %}M{{ g }}{% endmacro %}`,
		"global.html":   `{{ g }}|{% include 'gleaf.html' %}`,
		"gleaf.html":    `({{ g }})`,
		"loop.html":     `{% for i in range(n) %}.{% endfor %}{% include 'loopleaf.html' %}`,
		"loopleaf.html": `{% for i in range(n) %}:{% endfor %}`,
	}
}

func newOverlayParent(t testing.TB, opts ...Option) *Environment {
	t.Helper()
	env, err := New(append([]Option{WithLoader(overlaySources())}, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	env.AddGlobal("g", value.String("P"))
	return env
}

func renderNamed(env *Environment, name string, vars map[string]any) (string, error) {
	tmpl, err := env.GetTemplate(name)
	if err != nil {
		return "", err
	}
	return tmpl.RenderString(context.Background(), vars)
}

// overlayCase is one Option given to an overlay, and what rendering through the
// overlay must then produce. Every exported With* constructor has a case in
// either compileOverlayCases or runtimeOverlayCases;
// TestOverlayCasesNameEveryOption fails otherwise.
type overlayCase struct {
	option string
	opt    Option
	// sources is added to the loader. name is the template rendered.
	sources map[string]string
	name    string
	vars    map[string]any
	// parent is what the parent renders, and want what the overlay must.
	// A want beginning "error:" is an error containing the rest, at
	// GetTemplate or at render.
	parent, want string
	// after, if set, checks what rendering cannot show.
	after func(t *testing.T)
}

// compileOverlayCases change something compiling depends on. The parent
// compiles and caches the template first, so an overlay that reused the
// parent's tree would print the parent's fold -- which is what each case is
// built to show.
func compileOverlayCases() []overlayCase {
	var reported []string
	return []overlayCase{
		// Reporting happens while compiling, so an overlay with a report
		// of its own has to compile to make it.
		{option: "WithUnsupportedReport", opt: WithUnsupportedReport(func(u Unsupported) {
			reported = append(reported, u.Construct)
		}),
			name: "t", sources: map[string]string{"t": `{{ s.encode("ascii", "namereplace") }}`},
			vars: map[string]any{"s": "a"}, parent: "b'a'", want: "b'a'",
			after: func(t *testing.T) {
				if len(reported) != 1 {
					t.Errorf("the overlay's report saw %q; want one finding", reported)
				}
			}},
		{option: "WithAutoescape", opt: WithAutoescape(true), name: "fold.html",
			vars: map[string]any{"x": "<b>"}, parent: "<i>|<b>", want: "&lt;i&gt;|&lt;b&gt;"},
		{option: "WithAutoescapeFunc", opt: WithAutoescapeFunc(SelectAutoescape("html")),
			name: "inc.html", sources: map[string]string{"inc.html": `{{ '<i>' }}{{ x }}|{% include 'leaf.html' %}`},
			vars: map[string]any{"x": "<b>"}, parent: "<i><b>|[<b>]", want: "&lt;i&gt;&lt;b&gt;|[&lt;b&gt;]"},
		{option: "WithAutoescapeExtensions", opt: WithAutoescapeExtensions("html"), name: "fold.html",
			vars: map[string]any{"x": "<b>"}, parent: "<i>|<b>", want: "&lt;i&gt;|&lt;b&gt;"},
		{option: "WithAutoescapeSelection", opt: WithAutoescapeSelection(SelectAutoescapeConfig{Enabled: []string{"html"}}),
			name: "fold.html", vars: map[string]any{"x": "<b>"}, parent: "<i>|<b>", want: "&lt;i&gt;|&lt;b&gt;"},
		{option: "WithFinalize", opt: WithFinalize(func(v value.Value) value.Value { return value.String("F" + value.Str(v)) }),
			name: "t", sources: map[string]string{"t": `{{ 1 }}|{{ x }}`}, vars: map[string]any{"x": 2},
			parent: "1|2", want: "F1|F2"},
		// A finalize that sees the render keeps a constant print out of
		// the fold, so a reused tree would print the parent's folded 1.
		{option: "WithFinalizeFunc", opt: WithFinalizeFunc(func(_ *State, v value.Value) (value.Value, error) {
			return value.String("F" + value.Str(v)), nil
		}),
			name: "t", sources: map[string]string{"t": `{{ 1 }}|{{ x }}`}, vars: map[string]any{"x": 2},
			parent: "1|2", want: "F1|F2"},
		// A StrictUndefined asked for its text while folding stops the
		// compile; the default one folds to the empty string.
		{option: "WithUndefined", opt: WithUndefined(value.UndefinedStrict),
			name: "t", sources: map[string]string{"t": `{{ (0).a ~ 1 }}`},
			parent: "1", want: "error:has no attribute 'a'"},
		{option: "WithVariableDelimiters", opt: WithVariableDelimiters("[[", "]]"),
			name: "t", sources: map[string]string{"t": `[[ 1 ]]{{ 2 }}`}, parent: "[[ 1 ]]2", want: "1{{ 2 }}"},
		{option: "WithBlockDelimiters", opt: WithBlockDelimiters("<%", "%>"),
			name: "t", sources: map[string]string{"t": `<% if 1 %>y<% endif %>{% if 1 %}n{% endif %}`},
			parent: "<% if 1 %>y<% endif %>n", want: "y{% if 1 %}n{% endif %}"},
		{option: "WithCommentDelimiters", opt: WithCommentDelimiters("<#", "#>"),
			name: "t", sources: map[string]string{"t": `<# c #>{# d #}`}, parent: "<# c #>", want: "{# d #}"},
		{option: "WithLineStatementPrefix", opt: WithLineStatementPrefix("%%"),
			name: "t", sources: map[string]string{"t": "%% if 1\ny\n%% endif\n"},
			parent: "%% if 1\ny\n%% endif", want: "y\n"},
		{option: "WithLineCommentPrefix", opt: WithLineCommentPrefix("##"),
			name: "t", sources: map[string]string{"t": "a ## c\nb"}, parent: "a ## c\nb", want: "a\nb"},
		{option: "WithTrimBlocks", opt: WithTrimBlocks(true),
			name: "t", sources: map[string]string{"t": "{% if 1 %}\ny{% endif %}"}, parent: "\ny", want: "y"},
		{option: "WithLstripBlocks", opt: WithLstripBlocks(true),
			name: "t", sources: map[string]string{"t": "  {% if 1 %}y{% endif %}"}, parent: "  y", want: "y"},
		{option: "WithKeepTrailingNewline", opt: WithKeepTrailingNewline(true),
			name: "t", sources: map[string]string{"t": "y\n"}, parent: "y", want: "y\n"},
		{option: "WithNewlineSequence", opt: WithNewlineSequence("\r\n"),
			name: "t", sources: map[string]string{"t": "a\nb"}, parent: "a\nb", want: "a\r\nb"},
		// The parent cannot compile it at all.
		{option: "WithExtensions", opt: WithExtensions("do"),
			name: "t", sources: map[string]string{"t": `{% do x.append(1) %}{{ x }}`}, vars: map[string]any{"x": []any{}},
			parent: "error:unknown tag 'do'", want: "[1]"},
		{option: "WithLoader", opt: WithLoader(DictLoader{"fold.html": "other {{ x }}"}),
			name: "fold.html", vars: map[string]any{"x": 1}, parent: "<i>|1", want: "other 1"},
		{option: "WithPolicies", opt: WithPolicies(Policies{URLizeRel: "noopener"}),
			name: "t", sources: map[string]string{"t": `{{ 'aaaaaaaaaa'|truncate(5) }}`},
			parent: "aaaaaaaaaa", want: "aa..."},
		// jinja2's sort passes reverse to list.sort, whose argument clinic
		// took an int too wide for a C int only from 3.12 on. The fold
		// either succeeds or is left for the render to refuse.
		{option: "WithPythonVersion", opt: WithPythonVersion(value.Python311),
			name: "t", sources: map[string]string{"t": `{{ [3,1]|sort(reverse=2147483648) }}`},
			parent: "[3, 1]", want: "error:Python int too large to convert to C int"},
		{option: "WithMaxIntBits", opt: WithMaxIntBits(64),
			name: "t", sources: map[string]string{"t": `{{ 2 ** 100 }}`},
			parent: "1267650600228229401496703205376", want: "error:over the 64 bit limit"},
		{option: "WithUnsupportedLeniency", opt: WithUnsupportedLeniency(RefuseUnsupported),
			name: "t", sources: map[string]string{"t": `{{ s.encode("ascii", "namereplace") }}`},
			vars: map[string]any{"s": "a"}, parent: "b'a'", want: "error:namereplace"},
	}
}

// runtimeOverlayCases change nothing compiling reads, so the overlay reuses the
// parent's compiled tree -- and must still render with its own setting, which
// is what want shows.
func runtimeOverlayCases() []overlayCase {
	return []overlayCase{
		// The include counts against the overlay's bound too: 3 + 3 > 4.
		{option: "WithMaxIterations", opt: WithMaxIterations(4), name: "loop.html",
			vars: map[string]any{"n": 3}, parent: "...:::", want: "error:exceeded 4 loop iterations"},
		{option: "WithMaxOutputBytes", opt: WithMaxOutputBytes(4), name: "loop.html",
			vars: map[string]any{"n": 3}, parent: "...:::", want: "error:output"},
		{option: "WithoutLimits", opt: WithoutLimits(), name: "loop.html",
			vars: map[string]any{"n": 3}, parent: "...:::", want: "...:::"},
		// Two includes deep is one too many.
		{option: "WithMaxRecursion", opt: WithMaxRecursion(1), name: "deep",
			sources: map[string]string{"deep": `{% include 'inc.html' %}`},
			vars:    map[string]any{"x": 1}, parent: "1|[1]", want: "error:recursion"},
		{option: "WithMethodPolicy", opt: WithMethodPolicy(value.AllMethods), name: "t",
			sources: map[string]string{"t": `{{ h.Echo is defined }}{% include 'mleaf' %}`},
			vars:    map[string]any{"h": overlayHost{}}, parent: "False|False", want: "True|True"},
		{option: "WithCacheSize", opt: WithCacheSize(3), name: "inc.html",
			vars: map[string]any{"x": 1}, parent: "1|[1]", want: "1|[1]"},
		// The join is applied while rendering, through the overlay, to a
		// name the reused tree holds as written.
		{option: "WithJoinPath", opt: WithJoinPath(func(name, _ string) string { return "j/" + name }),
			name: "jp", sources: map[string]string{"jp": `{% include 'x' %}`, "x": "plain", "j/x": "joined"},
			parent: "plain", want: "joined"},
		{option: "WithDelimiterLeniency", opt: WithDelimiterLeniency(MatchJinja2Delimiters), name: "inc.html",
			vars: map[string]any{"x": 1}, parent: "1|[1]", want: "1|[1]"},
	}
}

type overlayHost struct{}

func (overlayHost) Echo(s string) string { return s }

func extraSources(c overlayCase) DictLoader {
	src := overlaySources()
	src["mleaf"] = `|{{ h.Echo is defined }}`
	for k, v := range c.sources {
		src[k] = v
	}
	return src
}

func checkRender(t *testing.T, who string, env *Environment, c overlayCase, want string) {
	t.Helper()
	got, err := renderNamed(env, c.name, c.vars)
	if msg, isErr := strings.CutPrefix(want, "error:"); isErr {
		if err == nil || !strings.Contains(err.Error(), msg) {
			t.Errorf("%s: got %q, %v; want an error containing %q", who, got, err, msg)
		}
		return
	}
	if err != nil || got != want {
		t.Errorf("%s: got %q, %v; want %q", who, got, err, want)
	}
}

// TestOverlayCompilesWhatItChanged gives an overlay each option that changes
// how a template compiles, after the parent has compiled and cached the
// template, and checks the overlay renders its own compile and leaves the
// parent's alone. jinja2 compiles again in every overlay; gojja2 must at least
// whenever it would show.
func TestOverlayCompilesWhatItChanged(t *testing.T) {
	for _, c := range compileOverlayCases() {
		t.Run(c.option, func(t *testing.T) {
			parent := newOverlayParent(t, WithLoader(extraSources(c)))
			checkRender(t, "parent before", parent, c, c.parent)
			ov, err := parent.Overlay(c.opt)
			if err != nil {
				t.Fatal(err)
			}
			if ov.overlay.sharesCompiled {
				t.Errorf("%s: overlay shares the parent's compiled templates", c.option)
			}
			checkRender(t, "overlay", ov, c, c.want)
			checkRender(t, "parent after", parent, c, c.parent)
			if c.after != nil {
				c.after(t)
			}
		})
	}
}

// TestOverlayReusesCompiledTemplates is the other half: an overlay that changes
// only what is read while rendering reuses the parent's tree -- for the
// template and the ones it includes -- and still renders with its own setting.
func TestOverlayReusesCompiledTemplates(t *testing.T) {
	for _, c := range runtimeOverlayCases() {
		t.Run(c.option, func(t *testing.T) {
			parent := newOverlayParent(t, WithLoader(extraSources(c)))
			checkRender(t, "parent before", parent, c, c.parent)
			ov, err := parent.Overlay(c.opt)
			if err != nil {
				t.Fatal(err)
			}
			if !ov.overlay.sharesCompiled {
				t.Fatalf("%s: overlay does not share the parent's compiled templates", c.option)
			}
			checkRender(t, "overlay", ov, c, c.want)
			checkRender(t, "parent after", parent, c, c.parent)

			pt, _ := parent.GetTemplate(c.name)
			ot, err := ov.GetTemplate(c.name)
			if err != nil {
				t.Fatal(err)
			}
			if ot == pt || ot.env != ov {
				t.Errorf("overlay template is the parent's, or not bound to the overlay")
			}
			if ot.tree != pt.tree {
				t.Errorf("overlay compiled %s again instead of reusing the parent's tree", c.name)
			}
			if again, _ := ov.GetTemplate(c.name); again != ot {
				t.Errorf("overlay did not cache its bound template")
			}
		})
	}
}

// TestOverlayCasesNameEveryOption keeps the two tables above complete: an
// Option added to the package must be given a case in one of them, which is
// the decision of whether an overlay may share compiled templates across it.
func TestOverlayCasesNameEveryOption(t *testing.T) {
	covered := map[string]bool{}
	for _, c := range append(compileOverlayCases(), runtimeOverlayCases()...) {
		covered[c.option] = true
	}
	for _, name := range packageFuncsReturning(t, "Option") {
		if !covered[name] {
			t.Errorf("%s has no overlay case: add it to compileOverlayCases if it changes "+
				"how a template compiles, and to runtimeOverlayCases if not", name)
		}
	}
}

// packageFuncsReturning lists the package's exported functions whose only
// result is the named type.
func packageFuncsReturning(t *testing.T, typ string) []string {
	t.Helper()
	var out []string
	forEachPackageFunc(t, func(fn *ast.FuncDecl) {
		if fn.Recv != nil || !fn.Name.IsExported() || fn.Type.Results == nil ||
			len(fn.Type.Results.List) != 1 {
			return
		}
		if id, ok := fn.Type.Results.List[0].Type.(*ast.Ident); ok && id.Name == typ {
			out = append(out, fn.Name.Name)
		}
	})
	if len(out) < 20 {
		t.Fatalf("found only %d functions returning %s; the scan is broken", len(out), typ)
	}
	return out
}

func forEachPackageFunc(t *testing.T, visit func(*ast.FuncDecl)) {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, path, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		if f.Name.Name != "gojja2" {
			continue
		}
		for _, decl := range f.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok {
				visit(fn)
			}
		}
	}
}

// TestOnlyAddWritesARegistry is what makes copy-on-write sufficient: an overlay
// starts out holding its parent's registry maps, so any code that writes one
// other than through AddFilter, AddTest and AddGlobal -- an Option setting a
// global, say -- would write the parent's.
func TestOnlyAddWritesARegistry(t *testing.T) {
	registries := map[string]bool{
		"filters": true, "tests": true, "globals": true, "stockFilters": true, "stockTests": true,
	}
	allowed := map[string]bool{
		"AddFilter": true, "AddTest": true, "AddGlobal": true,
		"ownFilters": true, "ownTests": true, "ownGlobals": true,
		// New makes the maps; nothing holds them yet.
		"New": true,
	}
	isRegistry := func(e ast.Expr) bool {
		if ix, ok := e.(*ast.IndexExpr); ok {
			e = ix.X
		}
		sel, ok := e.(*ast.SelectorExpr)
		return ok && registries[sel.Sel.Name]
	}
	writers := 0
	forEachPackageFunc(t, func(fn *ast.FuncDecl) {
		ast.Inspect(fn, func(n ast.Node) bool {
			var hit bool
			switch n := n.(type) {
			case *ast.AssignStmt:
				hit = slices.ContainsFunc(n.Lhs, isRegistry)
			case *ast.CallExpr:
				if id, ok := n.Fun.(*ast.Ident); ok && (id.Name == "delete" || id.Name == "clear") {
					hit = isRegistry(n.Args[0])
				}
			}
			if hit {
				writers++
				if !allowed[fn.Name.Name] {
					t.Errorf("%s writes a filter, test or global registry; do it through "+
						"AddFilter, AddTest or AddGlobal so an overlay copies first", fn.Name.Name)
				}
			}
			return true
		})
	})
	if writers < 6 {
		t.Fatalf("found %d registry writes; the scan is broken", writers)
	}
}

// TestOverlayHostCodeFoldIsNotShared covers the template-level condition. A
// host filter or test is handed the compiling State, so the fold it took part
// in can depend on which environment compiled -- jinja2's pass_environment
// filter, folded in an overlay, sees the overlay. The overlay here changes only
// a limit, and must still compile such a template itself.
func TestOverlayHostCodeFoldIsNotShared(t *testing.T) {
	which := func(s *State) string {
		if s.Env().LinkedTo() != nil {
			return "overlay"
		}
		return "parent"
	}
	for _, c := range []struct{ kind, src string }{
		{"filter", `{{ 'x'|who }}`},
		{"test", `{{ 'x' is who }}`},
	} {
		t.Run(c.kind, func(t *testing.T) {
			parent := newOverlayParent(t, WithLoader(DictLoader{"t": c.src}))
			var seen []string
			parent.AddFilter("who", func(s *State, _ value.Value, _ *value.CallArgs) (value.Value, error) {
				return value.String(which(s)), nil
			})
			parent.AddTest("who", func(s *State, _ value.Value, _ *value.CallArgs) (bool, error) {
				seen = append(seen, which(s))
				return true, nil
			})
			if _, err := renderNamed(parent, "t", nil); err != nil {
				t.Fatal(err)
			}
			ov, err := parent.Overlay(WithMaxIterations(100))
			if err != nil {
				t.Fatal(err)
			}
			got, err := renderNamed(ov, "t", nil)
			if err != nil {
				t.Fatal(err)
			}
			if c.kind == "filter" && got != "overlay" {
				t.Errorf("host filter folded for the overlay saw the %s", got)
			}
			if c.kind == "test" && !slices.Equal(seen, []string{"parent", "overlay"}) {
				t.Errorf("host test folded under %v; want the parent's compile then the overlay's", seen)
			}
		})
	}
}

// TestOverlayRegistriesAreCopiedOnWrite pins the divergence: in jinja2 an
// overlay's filters, tests and globals are its parent's dicts, so adding one
// to the overlay adds it to the parent. Here the overlay copies first.
func TestOverlayRegistriesAreCopiedOnWrite(t *testing.T) {
	parent := newOverlayParent(t)
	ov, err := parent.Overlay()
	if err != nil {
		t.Fatal(err)
	}
	// Before the overlay writes, a registration on the parent is seen
	// through it, as in jinja2.
	parent.AddFilter("early", func(*State, value.Value, *value.CallArgs) (value.Value, error) {
		return value.String("e"), nil
	})
	ov.AddFilter("mine", func(*State, value.Value, *value.CallArgs) (value.Value, error) {
		return value.String("m"), nil
	})
	ov.AddTest("mine", func(*State, value.Value, *value.CallArgs) (bool, error) { return true, nil })
	ov.AddGlobal("g", value.String("OV"))
	parent.AddFilter("late", func(*State, value.Value, *value.CallArgs) (value.Value, error) {
		return value.String("l"), nil
	})

	src := `{{ 'early' is filter }} {{ 'mine' is filter }} {{ 'mine' is test }} {{ g }} {{ 'late' is filter }}`
	for _, c := range []struct {
		env  *Environment
		want string
	}{
		{parent, "True False False P True"},
		{ov, "True True True OV False"},
	} {
		tmpl, err := c.env.FromString(src)
		if err != nil {
			t.Fatal(err)
		}
		got, err := tmpl.RenderString(context.Background(), nil)
		if err != nil || got != c.want {
			t.Errorf("got %q, %v; want %q", got, err, c.want)
		}
	}
	if _, ok := parent.Globals()["g"]; !ok || value.Str(parent.Globals()["g"]) != "P" {
		t.Errorf("the overlay's global replaced the parent's")
	}
}

// TestOverlayOwnFilterStopsSharing: `{{ 'a'|upper }}` is folded when the
// parent compiles it, so an overlay that replaced upper must compile again.
func TestOverlayOwnFilterStopsSharing(t *testing.T) {
	for _, c := range []struct {
		kind, src string
		add       func(*Environment)
	}{
		{"filter", `{{ 'a'|upper }}`, func(e *Environment) {
			e.AddFilter("upper", func(*State, value.Value, *value.CallArgs) (value.Value, error) {
				return value.String("mine"), nil
			})
		}},
		{"test", `{{ 'mine' if 2 is odd else 'stock' }}`, func(e *Environment) {
			e.AddTest("odd", func(*State, value.Value, *value.CallArgs) (bool, error) { return true, nil })
		}},
	} {
		t.Run(c.kind, func(t *testing.T) {
			parent := newOverlayParent(t, WithLoader(DictLoader{"t": c.src}))
			if got, _ := renderNamed(parent, "t", nil); got == "mine" {
				t.Fatalf("parent rendered %q", got)
			}
			ov, err := parent.Overlay()
			if err != nil {
				t.Fatal(err)
			}
			c.add(ov)
			if got, err := renderNamed(ov, "t", nil); err != nil || got != "mine" {
				t.Errorf("overlay with its own %s rendered %q, %v; want %q", c.kind, got, err, "mine")
			}
			if got, _ := renderNamed(parent, "t", nil); got == "mine" {
				t.Errorf("the overlay's %s reached the parent", c.kind)
			}
		})
	}
}

// TestOverlayKeepsStockSignatures: replacing a stock filter on the overlay
// takes the name off the overlay's stock list, not the parent's, whose calls
// are still checked against jinja2's signature.
func TestOverlayKeepsStockSignatures(t *testing.T) {
	parent := newOverlayParent(t)
	ov, err := parent.Overlay()
	if err != nil {
		t.Fatal(err)
	}
	ov.AddFilter("upper", func(*State, value.Value, *value.CallArgs) (value.Value, error) {
		return value.String("mine"), nil
	})
	src := `{{ x|upper(1, 2) }}`
	tmpl, err := parent.FromString(src)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tmpl.RenderString(context.Background(), map[string]any{"x": "a"}); err == nil ||
		!strings.Contains(err.Error(), "positional argument") {
		t.Errorf("parent's upper lost its signature check: %v", err)
	}
	tmpl, err = ov.FromString(src)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := tmpl.RenderString(context.Background(), map[string]any{"x": "a"}); err != nil || got != "mine" {
		t.Errorf("overlay's upper = %q, %v; want %q", got, err, "mine")
	}
}

// TestOverlayTemplatesUseItsGlobals: what a template reaches by include,
// extends and import comes through the overlay, and so sees its globals.
func TestOverlayTemplatesUseItsGlobals(t *testing.T) {
	parent := newOverlayParent(t)
	ov, err := parent.Overlay()
	if err != nil {
		t.Fatal(err)
	}
	ov.AddGlobal("g", value.String("OV"))
	for _, c := range []struct{ name, parent, overlay string }{
		{"global.html", "P|(P)", "OV|(OV)"},
		{"child.html", "BP<P>", "BOV<OV>"},
		{"imp.html", "MP", "MOV"},
	} {
		// Twice, in both orders, so neither cache decides the answer.
		for range 2 {
			if got, err := renderNamed(parent, c.name, nil); err != nil || got != c.parent {
				t.Errorf("parent %s = %q, %v; want %q", c.name, got, err, c.parent)
			}
			if got, err := renderNamed(ov, c.name, nil); err != nil || got != c.overlay {
				t.Errorf("overlay %s = %q, %v; want %q", c.name, got, err, c.overlay)
			}
		}
	}
	if !ov.overlay.sharesCompiled {
		t.Errorf("adding a global stopped the overlay sharing compiled templates")
	}
}

// TestOverlayCache: jinja2's copy_cache -- the overlay starts empty, at the
// parent's size, and clearing one leaves the other.
func TestOverlayCache(t *testing.T) {
	parent := newOverlayParent(t, WithCacheSize(7))
	if _, err := parent.GetTemplate("leaf.html"); err != nil {
		t.Fatal(err)
	}
	ov, err := parent.Overlay()
	if err != nil {
		t.Fatal(err)
	}
	if ov.cache == parent.cache || ov.cache.limit != 7 || len(ov.cache.entries) != 0 {
		t.Errorf("overlay cache: shared=%v limit=%d entries=%d; want its own, 7, 0",
			ov.cache == parent.cache, ov.cache.limit, len(ov.cache.entries))
	}
	sized, err := parent.Overlay(WithCacheSize(2))
	if err != nil {
		t.Fatal(err)
	}
	if sized.cache.limit != 2 {
		t.Errorf("WithCacheSize on an overlay gave limit %d", sized.cache.limit)
	}
	if _, err := ov.GetTemplate("leaf.html"); err != nil {
		t.Fatal(err)
	}
	ov.ClearCache()
	if _, ok := parent.cache.get("leaf.html"); !ok {
		t.Errorf("clearing the overlay's cache cleared the parent's")
	}
}

// TestOverlayIsCheckedLikeNew: the overlay's own configuration is validated,
// and a refused one changes nothing.
func TestOverlayIsCheckedLikeNew(t *testing.T) {
	parent := newOverlayParent(t)
	for name, opt := range map[string]Option{
		"colliding delimiters": WithBlockDelimiters("{{", "}}"),
		"bad newline":          WithNewlineSequence("x"),
		"unknown extension":    WithExtensions("nope"),
	} {
		ov, err := parent.Overlay(opt)
		if err == nil || ov != nil || !errors.Is(err, errs.TemplateError) {
			t.Errorf("%s: Overlay = %v, %v; want a TemplateError and no environment", name, ov, err)
		}
	}
	if got, err := renderNamed(parent, "leaf.html", map[string]any{"x": 1}); err != nil || got != "[1]" {
		t.Errorf("parent after refused overlays: %q, %v", got, err)
	}
}

func TestOverlayLinkedTo(t *testing.T) {
	parent := newOverlayParent(t)
	ov, _ := parent.Overlay()
	ov2, _ := ov.Overlay(WithMaxIterations(9))
	if parent.LinkedTo() != nil || ov.LinkedTo() != parent || ov2.LinkedTo() != ov {
		t.Errorf("LinkedTo: %p %p %p", parent.LinkedTo(), ov.LinkedTo(), ov2.LinkedTo())
	}
	// An overlay of an overlay shares through both.
	pt, _ := parent.GetTemplate("leaf.html")
	ot, err := ov2.GetTemplate("leaf.html")
	if err != nil || ot.tree != pt.tree || ot.env != ov2 {
		t.Errorf("an overlay of an overlay did not reuse the root's tree, bound to itself: %v", err)
	}
}

// TestBoundToCopiesEveryField fails when Template grows a field boundTo has not
// decided about. Each field is either the product of compiling, and shared, or
// belongs to the binding, and is not.
func TestBoundToCopiesEveryField(t *testing.T) {
	notShared := map[string]bool{"env": true, "frameLocals": true}
	src := &Template{}
	rv := reflect.ValueOf(src).Elem()
	for i := range rv.NumField() {
		f := rv.Type().Field(i)
		if notShared[f.Name] {
			continue
		}
		// Give every shared field a non-zero value, by writing through
		// the unexported field's address.
		fv := reflect.NewAt(f.Type, rv.Field(i).Addr().UnsafePointer()).Elem()
		switch f.Type.Kind() {
		case reflect.String:
			fv.SetString("s")
		case reflect.Bool:
			fv.SetBool(true)
		case reflect.Pointer, reflect.Map:
			if f.Type.Kind() == reflect.Map {
				fv.Set(reflect.MakeMap(f.Type))
			} else {
				fv.Set(reflect.New(f.Type.Elem()))
			}
		case reflect.Slice:
			fv.Set(reflect.MakeSlice(f.Type, 1, 1))
		default:
			t.Fatalf("Template.%s is a %s; teach this test to set one", f.Name, f.Type)
		}
	}
	env := &Environment{}
	got := reflect.ValueOf(src.boundTo(env)).Elem()
	for i := range rv.NumField() {
		f := rv.Type().Field(i)
		if notShared[f.Name] {
			continue
		}
		a := reflect.NewAt(f.Type, rv.Field(i).Addr().UnsafePointer()).Elem().Interface()
		b := reflect.NewAt(f.Type, got.Field(i).Addr().UnsafePointer()).Elem().Interface()
		same := reflect.DeepEqual(a, b)
		switch f.Type.Kind() {
		case reflect.Pointer, reflect.Map, reflect.Slice:
			// The same object, not an equal one: two empty maps are
			// DeepEqual.
			same = same && reflect.ValueOf(a).Pointer() == reflect.ValueOf(b).Pointer()
		}
		if !same {
			t.Errorf("boundTo does not carry Template.%s; decide whether a template "+
				"the overlay reuses shares it", f.Name)
		}
	}
	if src.boundTo(env).env != env {
		t.Errorf("boundTo did not bind the environment")
	}
}

// TestOverlayClassifiesEveryEnvironmentField fails when Environment grows a
// field the overlay has not been told about: whether it is read while
// compiling, and so belongs in compilesAlike or setsCompileReferences.
func TestOverlayClassifiesEveryEnvironmentField(t *testing.T) {
	known := map[string]string{
		// Compared by compilesAlike.
		"syntax": "compile", "parseOpts": "compile", "undefined": "compile",
		"policies": "compile", "maxIntBits": "compile",
		// Compared through syntax.PythonVersion, which WithPythonVersion
		// sets with it.
		"pyVersion":           "compile",
		"unsupportedLeniency": "compile",
		// Asked about by setsCompileReferences.
		"loader": "compile-ref", "autoescape": "compile-ref", "finalize": "compile-ref",
		// Set only together with finalize, which WithFinalizeFunc clears.
		"finalizeFunc":      "compile-ref",
		"unsupportedReport": "compile-ref",
		// Copied on write, and checked per template by sharedTemplate.
		"filters": "registry", "tests": "registry", "globals": "registry",
		"stockFilters": "registry", "stockTests": "registry",
		// Read only while rendering, or only by validate.
		"methods": "runtime", "maxRecursion": "runtime", "maxIterations": "runtime",
		"maxOutputBytes": "runtime", "delimiterLeniency": "runtime", "joinPath": "runtime",
		"cache": "own", "overlay": "own",
	}
	typ := reflect.TypeFor[Environment]()
	for i := range typ.NumField() {
		if name := typ.Field(i).Name; known[name] == "" {
			t.Errorf("Environment.%s is new: if compiling reads it, compare it in "+
				"compilesAlike (or setsCompileReferences for a func or interface) and add "+
				"a compileOverlayCases entry; then list it here", name)
		}
	}
}

// TestOverlayConcurrent is for -race: overlays made, written to and rendered
// from many goroutines while the parent renders the same templates.
func TestOverlayConcurrent(t *testing.T) {
	parent := newOverlayParent(t)
	var wg sync.WaitGroup
	errc := make(chan error, 64)
	for i := range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			opts := []Option{WithMaxIterations(1000)}
			if i%4 == 0 {
				opts = append(opts, WithAutoescape(true))
			}
			ov, err := parent.Overlay(opts...)
			if err != nil {
				errc <- err
				return
			}
			tenant := fmt.Sprintf("t%d", i)
			ov.AddGlobal("g", value.String(tenant))
			ov.AddFilter("tag", func(*State, value.Value, *value.CallArgs) (value.Value, error) {
				return value.String(tenant), nil
			})
			for range 20 {
				got, err := renderNamed(ov, "global.html", nil)
				if err != nil || got != tenant+"|("+tenant+")" {
					errc <- fmt.Errorf("overlay %d: %q, %v", i, got, err)
					return
				}
				tmpl, err := ov.FromString(`{{ 1|tag }}`)
				if err != nil {
					errc <- err
					return
				}
				if got, err := tmpl.RenderString(context.Background(), nil); err != nil || got != tenant {
					errc <- fmt.Errorf("overlay %d filter: %q, %v", i, got, err)
					return
				}
				if got, err := renderNamed(parent, "global.html", nil); err != nil || got != "P|(P)" {
					errc <- fmt.Errorf("parent beside overlay %d: %q, %v", i, got, err)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errc)
	for err := range errc {
		t.Error(err)
	}
}

// TestOverlayPythonVersionIsInSyntax is what lets compilesAlike compare the
// Python version through syntax alone.
func TestOverlayPythonVersionIsInSyntax(t *testing.T) {
	for _, v := range []PythonVersion{value.Python311, value.Python312, value.Python313, value.Python314} {
		env, err := New(WithPythonVersion(v))
		if err != nil {
			t.Fatal(err)
		}
		if env.syntax.PythonVersion != env.pyVersion || env.pyVersion != v {
			t.Errorf("WithPythonVersion(%v): syntax has %v, environment %v", v, env.syntax.PythonVersion, env.pyVersion)
		}
	}
}

// benchPage is a page of the size a request renders: a layout, an include and
// a loop, enough that compiling it is a visible part of the cost.
var benchPage = DictLoader{
	"layout.html": `<html><head><title>{% block title %}{% endblock %}</title></head>` +
		`<body>{% include 'nav.html' %}{% block body %}{% endblock %}</body></html>`,
	"nav.html": `<ul>{% for item in nav %}<li class="{{ loop.cycle('a', 'b') }}">` +
		`{{ item|title }}</li>{% endfor %}</ul>`,
	"page.html": `{% extends 'layout.html' %}{% block title %}{{ user }}'s page{% endblock %}` +
		`{% block body %}{% for row in rows %}<p>{{ row.name|e }}: {{ row.n * 2 }}` +
		`{% if row.n is even %} even{% endif %}</p>{% endfor %}{% endblock %}`,
}

// BenchmarkPerRequestEnvironment compares three ways of giving each request
// an environment with a global of its own: building one with New, an
// Overlay that reuses the base's compiled templates, and an Overlay that has
// to compile them because it changes the autoescape policy.
func BenchmarkPerRequestEnvironment(b *testing.B) {
	vars := map[string]any{
		"nav":  []any{"home", "about", "contact"},
		"rows": []any{map[string]any{"name": "a", "n": 1}, map[string]any{"name": "b", "n": 2}},
	}
	base, err := New(WithLoader(benchPage))
	if err != nil {
		b.Fatal(err)
	}
	render := func(b *testing.B, env *Environment) {
		env.AddGlobal("user", value.String("ann"))
		tmpl, err := env.GetTemplate("page.html")
		if err != nil {
			b.Fatal(err)
		}
		if _, err := tmpl.RenderString(context.Background(), vars); err != nil {
			b.Fatal(err)
		}
	}
	render(b, base)
	b.Run("New", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			env, err := New(WithLoader(benchPage))
			if err != nil {
				b.Fatal(err)
			}
			render(b, env)
		}
	})
	b.Run("Overlay", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			env, err := base.Overlay(WithMaxIterations(10_000))
			if err != nil {
				b.Fatal(err)
			}
			render(b, env)
		}
	})
	b.Run("OverlayRecompiles", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			env, err := base.Overlay(WithAutoescape(true))
			if err != nil {
				b.Fatal(err)
			}
			render(b, env)
		}
	})
	b.Run("SharedEnvironment", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			tmpl, err := base.GetTemplate("page.html")
			if err != nil {
				b.Fatal(err)
			}
			if _, err := tmpl.RenderString(context.Background(), vars); err != nil {
				b.Fatal(err)
			}
		}
	})
}
