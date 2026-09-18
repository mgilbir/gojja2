// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"runtime/debug"

	"github.com/mgilbir/gojja2"
	"github.com/mgilbir/gojja2/errs"
	"github.com/mgilbir/gojja2/internal/parser"
)

// The guards collected here all defend one property: no template may exhaust
// the goroutine stack. They are together because they used to be apart -- the
// bound existed for equality and not for ordering, for a macro call and not
// for a block -- and a reader checking whether a new recursive descent is
// covered should have one list to check against.
//
// A Go stack overflow is a fatal error rather than a panic, so catchPanic
// cannot turn it into a render error: a miss here is a dead process, not a
// failed render. That is why every case asserts CPython's RecursionError
// rather than merely asserting that the render returned.

// cyclicLists builds two distinct lists that point at each other, using only
// what a default environment offers: `{% set _ = l.append(x) %}` needs no
// extension. Two *distinct* cycles are the hard case -- a structure compared
// against itself short-circuits on identity and terminates either way.
const cyclicLists = `{% set a = [] %}{% set b = [] %}` +
	`{% set _ = a.append(b) %}{% set _ = b.append(a) %}`

func wantRecursionError(t *testing.T, src string, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: rendered without error, want a RecursionError", src)
	}
	if !errors.Is(err, errs.RecursionError) {
		t.Fatalf("%s: got %T %v, want a RecursionError", src, err, err)
	}
	if !strings.Contains(err.Error(), "maximum recursion depth exceeded") {
		t.Errorf("%s: got %q, want CPython's recursion wording", src, err)
	}
}

// TestCyclicOrderingRaises covers every operator and filter that reaches
// value.compare. Equality was already bounded; ordering was not, and CPython
// raises the same RecursionError for both.
func TestCyclicOrderingRaises(t *testing.T) {
	for name, expr := range map[string]string{
		"lt":      `{{ a < b }}`,
		"lteq":    `{{ a <= b }}`,
		"gt":      `{{ a > b }}`,
		"gteq":    `{{ a >= b }}`,
		"sort":    `{{ [a, b]|sort }}`,
		"min":     `{{ [a, b]|min }}`,
		"max":     `{{ [a, b]|max }}`,
		"groupby": `{{ [{"k": a}, {"k": b}]|groupby("k") }}`,
		"eq":      `{{ a == b }}`,
	} {
		t.Run(name, func(t *testing.T) {
			env := gojja2.New()
			err := renderWith(t, context.Background(), env, cyclicLists+expr)
			wantRecursionError(t, expr, err)
		})
	}
}

// TestDeepNonCyclicOrderingRaises is the same bound reached without a cycle:
// CPython raises RecursionError comparing structures nested past its limit,
// whether or not they close on themselves.
func TestDeepNonCyclicOrderingRaises(t *testing.T) {
	// A namespace, because a {% set %} inside a loop body does not survive
	// the iteration: the nesting has to accumulate somewhere the loop's
	// fresh scope does not discard.
	const build = `{% set ns = namespace(a=[0], b=[1]) %}` +
		`{% for i in range(4000) %}{% set ns.a = [ns.a] %}{% set ns.b = [ns.b] %}{% endfor %}`
	env := gojja2.New()
	err := renderWith(t, context.Background(), env, build+`{{ ns.a < ns.b }}`)
	wantRecursionError(t, "deeply nested <", err)
}

// TestSelfReferentialBlockRaises: rendering a block is entering another
// template function, so it is bounded by the same counter as include, extends
// and a macro call. Without that, `{{ self.x() }}` inside block x recursed
// until the process died.
//
// Only a *call* renders. `{{ self.x }}` prints the reference and recurses
// nowhere, which is why every case here writes the parentheses.
func TestSelfReferentialBlockRaises(t *testing.T) {
	for name, src := range map[string]string{
		"called":     `{% block x %}{{ self.x() }}{% endblock %}`,
		"mutual":     `{% block a %}{{ self.b() }}{% endblock %}{% block b %}{{ self.a() }}{% endblock %}`,
		"via macro":  `{% macro m() %}{{ self.x() }}{% endmacro %}{% block x %}{{ m() }}{% endblock %}`,
		"via filter": `{% block x %}{{ self.x()|upper }}{% endblock %}`,
		"in a set":   `{% block x %}{% set t = self.x() %}{{ t }}{% endblock %}`,
	} {
		t.Run(name, func(t *testing.T) {
			env := gojja2.New()
			err := renderWith(t, context.Background(), env, src)
			wantRecursionError(t, src, err)
		})
	}
}

// TestBlockRecursionAllowsRealTemplates guards the other direction. A bound
// that fires on ordinary inheritance would be worse than no bound at all, so
// the shapes real templates use are pinned here: nested blocks, a super()
// chain, and a block rendered through self by name.
func TestBlockRecursionAllowsRealTemplates(t *testing.T) {
	loader := gojja2.DictLoader(map[string]string{
		"base.html": `[{% block outer %}o{% block inner %}i{% endblock %}{% endblock %}]`,
		"mid.html":  `{% extends "base.html" %}{% block inner %}m{{ super() }}{% endblock %}`,
	})
	for name, tc := range map[string]struct{ src, want string }{
		"nested blocks":  {`{% block a %}A{% block b %}B{% endblock %}{% endblock %}`, "AB"},
		"self by name":   {`{% block a %}A{% endblock %}-{{ self.a() }}`, "A-A"},
		"super chain":    {`{% extends "mid.html" %}{% block inner %}c{{ super() }}{% endblock %}`, "[ocmi]"},
		"sibling blocks": {`{% block a %}A{% endblock %}{% block b %}{{ self.a() }}B{% endblock %}`, "AAB"},
	} {
		t.Run(name, func(t *testing.T) {
			env := gojja2.New(gojja2.WithLoader(loader))
			got, err := mustRender(t, env, tc.src)
			if err != nil {
				t.Fatalf("%s: %v", tc.src, err)
			}
			if got != tc.want {
				t.Errorf("%s = %q, want %q", tc.src, got, tc.want)
			}
		})
	}
}

// TestBlockRecursionRespectsTheConfiguredLimit pins that blocks are counted by
// the same knob as every other template boundary, rather than by a bound of
// their own that a caller cannot reach.
func TestBlockRecursionRespectsTheConfiguredLimit(t *testing.T) {
	var deep strings.Builder
	const levels = 40
	for i := range levels {
		deep.WriteString(`{% block b`)
		deep.WriteString(string(rune('a' + i%26)))
		deep.WriteString(string(rune('a' + i/26)))
		deep.WriteString(` %}`)
	}
	deep.WriteString("x")
	for i := levels - 1; i >= 0; i-- {
		_ = i
		deep.WriteString(`{% endblock %}`)
	}
	src := deep.String()

	if _, err := mustRender(t, gojja2.New(gojja2.WithMaxRecursion(levels+10)), src); err != nil {
		t.Fatalf("%d nested blocks under a limit of %d: %v", levels, levels+10, err)
	}
	err := renderWith(t, context.Background(),
		gojja2.New(gojja2.WithMaxRecursion(levels/2)), src)
	wantRecursionError(t, "nested blocks past the limit", err)
	var e *errs.Error
	if errors.As(err, &e) && e.Limit != levels/2 {
		t.Errorf("Limit = %d, want the configured %d", e.Limit, levels/2)
	}
}

// --- compile-time nesting ----------------------------------------------------

// nestingShapes is every way a template can build a deep tree. The ones that
// nest *iteratively* are the point: a chain is written as a loop and produces
// a tree as deep as it is long, so `not not not ...` and `x|f|f|f...` reach
// exactly as far into a recursive walk as `[[[...]]]` does. Counting call
// depth rather than tree depth saw only the bracketed forms, and a 2.5 MB
// template of `not ` was accepted by the parser and then overflowed the stack
// inside the constant folder -- at compile time, where there is no render to
// bound and no context to cancel.
var nestingShapes = map[string]func(n int) string{
	"brackets":  func(n int) string { return "{{ " + strings.Repeat("[", n) + strings.Repeat("]", n) + " }}" },
	"parens":    func(n int) string { return "{{ " + strings.Repeat("(", n) + "1" + strings.Repeat(")", n) + " }}" },
	"dict":      func(n int) string { return "{{ " + strings.Repeat("{1:", n) + "0" + strings.Repeat("}", n) + " }}" },
	"not":       func(n int) string { return "{{ " + strings.Repeat("not ", n) + "1 }}" },
	"negate":    func(n int) string { return "{{ " + strings.Repeat("-", n) + "1 }}" },
	"filter":    func(n int) string { return "{{ 1" + strings.Repeat("|abs", n) + " }}" },
	"attribute": func(n int) string { return "{{ x" + strings.Repeat(".a", n) + " }}" },
	"subscript": func(n int) string { return "{{ x" + strings.Repeat("[0]", n) + " }}" },
	"call":      func(n int) string { return "{{ x" + strings.Repeat("()", n) + " }}" },
	"add":       func(n int) string { return "{{ 0" + strings.Repeat("+x", n) + " }}" },
	"multiply":  func(n int) string { return "{{ 1" + strings.Repeat("*x", n) + " }}" },
	"power":     func(n int) string { return "{{ 1" + strings.Repeat("**x", n) + " }}" },
	"or":        func(n int) string { return "{{ 0" + strings.Repeat(" or x", n) + " }}" },
	"and":       func(n int) string { return "{{ 1" + strings.Repeat(" and x", n) + " }}" },
	"condexpr":  func(n int) string { return "{{ 1" + strings.Repeat(" if x else 1", n) + " }}" },
	"for":       func(n int) string { return strings.Repeat("{% for i in [1] %}", n) + strings.Repeat("{% endfor %}", n) },
	"if":        func(n int) string { return strings.Repeat("{% if 1 %}", n) + strings.Repeat("{% endif %}", n) },
	"filtertag": func(n int) string {
		return strings.Repeat("{% filter upper %}", n) + strings.Repeat("{% endfilter %}", n)
	},
}

// TestNestingBoundCoversEveryShape pins that the bound is reached, and reached
// cleanly, however a template chooses to nest -- and that it is reached at the
// documented depth rather than at some fraction of it that depends on how many
// layers of grammar the shape happens to pass through.
func TestNestingBoundCoversEveryShape(t *testing.T) {
	const limit = parser.MaxNestingDepth
	for name, build := range nestingShapes {
		t.Run(name, func(t *testing.T) {
			env := gojja2.New()
			// A shape whose statement or operator costs two nodes
			// lands one short; what matters is that the limit is of
			// the documented order and not half of it.
			if _, err := env.FromString(build(limit / 2)); err != nil {
				t.Fatalf("%d levels should compile, got %v", limit/2, err)
			}
			_, err := env.FromString(build(limit * 4))
			if err == nil {
				t.Fatalf("%d levels compiled; the bound did not fire", limit*4)
			}
			if !errors.Is(err, errs.TemplateSyntaxError) {
				t.Fatalf("got %T %v, want a TemplateSyntaxError", err, err)
			}
			if !strings.Contains(err.Error(), "nests deeper than") {
				t.Errorf("got %q, want the nesting message", err)
			}
		})
	}
}

// TestFlatShapesAreNotNesting is the other half of the same claim. `a ~ b ~ c`
// and `a < b < c` each build *one* node over a flat list of operands, exactly
// as jinja2's own Concat and Compare do, so they deepen the tree by one however
// long they run and everything that walks them iterates rather than recurses.
// Charging them per operand would refuse a template CPython renders.
func TestFlatShapesAreNotNesting(t *testing.T) {
	env := gojja2.New()
	for name, src := range map[string]string{
		"concat":  "{{ 0" + strings.Repeat("~1", 20000) + " }}",
		"compare": "{{ 1" + strings.Repeat(" < 2", 20000) + " }}",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := env.FromString(src); err != nil {
				t.Fatalf("a flat %s chain must not count as nesting: %v", name, err)
			}
		})
	}
}

// TestNestingBoundSurvivesTheWholeCompile guards the part that actually
// crashed. The parser was never the only thing walking this tree: the
// constant folder, the frame-local visitor, the dependency checker and the
// block collector all descend it once per level, and it was the folder that
// died. Rendering at the limit exercises the evaluator's descent too.
func TestNestingBoundSurvivesTheWholeCompile(t *testing.T) {
	const near = parser.MaxNestingDepth / 2
	for name, src := range map[string]string{
		"folded not":    "{{ " + strings.Repeat("not ", near) + "1 }}",
		"folded negate": "{{ " + strings.Repeat("-", near) + "1 }}",
		"folded filter": "{{ 1" + strings.Repeat("|abs", near) + " }}",
		"folded add":    "{{ 0" + strings.Repeat("+1", near) + " }}",
		"nested list":   "{{ " + strings.Repeat("[", near) + strings.Repeat("]", near) + " }}",
		// A name the folder cannot resolve, so the chain survives to
		// render time and the evaluator descends it for real.
		"unfolded chain": "{% set x = 1 %}{{ x" + strings.Repeat("|abs", near) + " }}",
		"unfolded add":   "{% set x = 1 %}{{ x" + strings.Repeat("+1", near) + " }}",
	} {
		t.Run(name, func(t *testing.T) {
			env := gojja2.New()
			if _, err := mustRender(t, env, src); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
		})
	}
}

// --- deep value graphs -------------------------------------------------------

// nestValue builds a value nested n deep out of open, using a namespace so the
// nesting survives the loop's scope. The depth of a value graph is chosen at
// render time and bounded only by the iteration budget, which defaults to ten
// million -- so "how deep can a template go" is not a question the parser's
// nesting bound answers.
func nestValue(n int, open, close, expr string) string {
	return "{% set ns = namespace(t=" + open + "0" + close + ") %}" +
		"{% for i in range(" + itoa(n) + ") %}" +
		"{% set ns.t = " + open + "ns.t" + close + " %}{% endfor %}" + expr
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// deepGraphStack is the stack ceiling the deep-graph tests run under.
//
// The walks they cover used to recurse once per level, so proving they no
// longer do needs a graph deeper than the stack. Millions of levels would do
// it at the default one-gigabyte ceiling and would cost seconds and gigabytes;
// lowering the ceiling reaches the same wall with a graph small enough to build
// in a fraction of a second. A recursive walk over deepGraphLevels overflows
// this by roughly a factor of two.
const (
	deepGraphStack  = 24 << 20
	deepGraphLevels = 300_000
)

// TestDeepValueGraphDoesNotExhaustTheStack covers the walks that cannot report
// a failure. Str and Repr are called from more than a hundred places and
// return no error, so they have to be total: every one of those callers --
// `{{ deep }}`, `{{ deep|upper }}`, `{{ deep|string }}`, `{{ {deep: 1} }}` --
// was a site where a deep graph ended the process rather than the render.
func TestDeepValueGraphDoesNotExhaustTheStack(t *testing.T) {
	defer debug.SetMaxStack(debug.SetMaxStack(deepGraphStack))

	for name, tc := range map[string]struct{ open, close, expr string }{
		"print a list":  {"[", "]", `{{ ns.t|length }}`},
		"repr a list":   {"[", "]", `{{ ns.t|string|length }}`},
		"upper a list":  {"[", "]", `{{ ns.t|upper|length }}`},
		"repr a tuple":  {"(", ",)", `{{ ns.t|string|length }}`},
		"repr a dict":   {`{"k":`, "}", `{{ ns.t|string|length }}`},
		"hash a tuple":  {"(", ",)", `{{ {ns.t: 1}|length }}`},
		"equal a tuple": {"(", ",)", `{{ ns.t == ns.t }}`},
	} {
		t.Run(name, func(t *testing.T) {
			env := gojja2.New()
			src := nestValue(deepGraphLevels, tc.open, tc.close, tc.expr)
			if _, err := mustRender(t, env, src); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
		})
	}
}

// TestDeepValueGraphReportsWhereItCan is the other half of the rule. A walk
// that already carries an error says what CPython says rather than being made
// total: tojson and pprint both have one, and both wall CPython's interpreter
// at a comparable depth.
func TestDeepValueGraphReportsWhereItCan(t *testing.T) {
	defer debug.SetMaxStack(debug.SetMaxStack(deepGraphStack))

	for name, tc := range map[string]struct{ expr, want string }{
		"tojson": {`{{ ns.t|tojson }}`, "maximum recursion depth exceeded while encoding a JSON object"},
		"pprint": {`{{ ns.t|pprint }}`, "maximum recursion depth exceeded while getting the repr of an object"},
	} {
		t.Run(name, func(t *testing.T) {
			env := gojja2.New()
			src := nestValue(deepGraphLevels, "[", "]", tc.expr)
			err := renderWith(t, context.Background(), env, src)
			if err == nil {
				t.Fatalf("%s rendered a %d-deep graph without error", name, deepGraphLevels)
			}
			if !errors.Is(err, errs.RecursionError) {
				t.Fatalf("%s: got %T %v, want a RecursionError", name, err, err)
			}
			if err.Error() != tc.want {
				t.Errorf("%s: got %q, want CPython's %q", name, err, tc.want)
			}
		})
	}
}

// TestShallowValueGraphsStillRender guards the other direction: the walls must
// not fire on anything a template really prints.
func TestShallowValueGraphsStillRender(t *testing.T) {
	env := gojja2.New()
	for name, tc := range map[string]struct{ src, want string }{
		"nested list":  {`{{ [[1, [2]], 3] }}`, "[[1, [2]], 3]"},
		"nested tuple": {`{{ ((1,), 2) }}`, "((1,), 2)"},
		"one tuple":    {`{{ (1,) }}`, "(1,)"},
		"empty":        {`{{ [] }}{{ () }}{{ {} }}`, "[]()" + "{}"},
		"dict":         {`{{ {"b": [1], "a": {"c": 2}} }}`, `{'b': [1], 'a': {'c': 2}}`},
		"tojson":       {`{{ {"b": [1], "a": 2}|tojson }}`, `{"a": 2, "b": [1]}`},
		"pprint":       {`{{ {"b": [1], "a": 2}|pprint }}`, `{'a': 2, 'b': [1]}`},
		"tuple key":    {`{% set d = {(1, (2, 3)): "v"} %}{{ d[(1, (2, 3))] }}`, "v"},
		"deep-ish":     {nestValue(400, "[", "]", `{{ ns.t|string|length }}`), "803"},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := mustRender(t, env, tc.src)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if got != tc.want {
				t.Errorf("%s = %q, want %q", name, got, tc.want)
			}
		})
	}
}

// TestRecursionWordingIsOnlyPinnedForTwo: CPython has three recursion messages
// and picks between them by where its *own* stack ran out, so for most
// constructs the wording is not a property of the template at all. The same
// macro recursion, rendered from one extra frame, reports a different message:
//
//	N=0  maximum recursion depth exceeded
//	N=1  maximum recursion depth exceeded while calling a Python object
//	N=2  maximum recursion depth exceeded
//
// {% include %} and an {% extends %} cycle were stable at every depth tried, so
// those two are held to CPython's wording here. The rest are checked only for
// being a RecursionError, because pinning a wording for them would encode the
// stack depth of whichever harness recorded it -- which is exactly what the two
// imported MiniJinja fixtures do. docs/divergences.md records why.
func TestRecursionWordingIsOnlyPinnedForTwo(t *testing.T) {
	const (
		plain   = "maximum recursion depth exceeded"
		calling = "maximum recursion depth exceeded while calling a Python object"
		compare = "maximum recursion depth exceeded in comparison"
	)
	loader := gojja2.DictLoader{
		"self.txt":    `{% include "self.txt" %}`,
		"selfimp.txt": `{% import "selfimp.txt" as m %}{% set x = 1 %}`,
		"selfext.txt": `{% extends "selfext.txt" %}`,
	}
	for _, tc := range []struct{ name, src, want string }{
		// The two CPython pins down.
		{"include", `{% include "self.txt" %}`, calling},
		{"extends", `{% extends "selfext.txt" %}`, compare},
		// The rest: a RecursionError, wording unasserted beyond the
		// prefix every one of the three shares.
		{"macro", `{% macro m() %}{{ m() }}{% endmacro %}{{ m() }}`, plain},
		{"import", `{% import "selfimp.txt" as m %}{{ m.x }}`, plain},
		{"block reference", `{% block a %}{{ self.a() }}{% endblock %}`, plain},
		{"recursive loop", `{% for i in [1] recursive %}{{ loop([1]) }}{% endfor %}`, plain},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmpl, err := gojja2.New(gojja2.WithLoader(loader)).FromString(tc.src)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			_, err = tmpl.RenderString(context.Background(), nil)
			if err == nil {
				t.Fatal("rendered; want a RecursionError")
			}
			if kind := errs.KindOf(err); kind != errs.RecursionError {
				t.Fatalf("got %v (%v), want RecursionError", kind, err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %q, want it to contain %q", err, tc.want)
			}
			// The bound is the point, whatever the wording: it has to
			// carry the configured limit rather than bury it in text.
			var e *errs.Error
			if errors.As(err, &e) && e.Limit == 0 {
				t.Errorf("got %q with no Limit recorded", err)
			}
		})
	}
}
