// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"context"
	"testing"
)

// evil is a value that renders harmlessly only when it is escaped.
const evil = `<script>alert(1)</script>`

const evilEscaped = `&lt;script&gt;alert(1)&lt;/script&gt;`

func renderWith(t *testing.T, env *Environment, name, source string) string {
	t.Helper()
	var (
		tmpl *Template
		err  error
	)
	if name == "" {
		tmpl, err = env.FromString(source)
	} else {
		tmpl, err = env.FromNamedString(name, source)
	}
	if err != nil {
		t.Fatalf("compile %q: %v", name, err)
	}
	out, err := tmpl.RenderString(context.Background(), map[string]any{"evil": evil})
	if err != nil {
		t.Fatalf("render %q: %v", name, err)
	}
	return out
}

// TestSelectAutoescapeEscapesStringTemplates pins jinja2's default_for_string.
// A template compiled from a string has no name to match, and jinja2 escapes it
// rather than falling through to the default.
func TestSelectAutoescapeEscapesStringTemplates(t *testing.T) {
	env := New(WithAutoescapeFunc(SelectAutoescape(".html")))
	if got := renderWith(t, env, "", `{{ evil }}`); got != evilEscaped {
		t.Errorf("FromString template was not escaped:\n got %q\nwant %q", got, evilEscaped)
	}
	if got := renderWith(t, env, "page.html", `{{ evil }}`); got != evilEscaped {
		t.Errorf("named .html template was not escaped:\n got %q\nwant %q", got, evilEscaped)
	}
	// A named template that matches nothing must still NOT be escaped: the
	// point of the fix is to tell that case apart from the unnamed one.
	if got := renderWith(t, env, "page.txt", `{{ evil }}`); got != evil {
		t.Errorf("named .txt template should not be escaped:\n got %q\nwant %q", got, evil)
	}
}

// TestSelectAutoescapeIsCaseInsensitive pins the property jinja2's docstring
// calls out as a security property: an extension list spelled in a different
// case must not silently turn escaping off.
func TestSelectAutoescapeIsCaseInsensitive(t *testing.T) {
	cases := []struct {
		ext  string
		name string
	}{
		{".HTML", "page.HTML"},
		{".HTML", "page.html"},
		{".html", "page.HTML"},
		{".Html", "page.hTmL"},
		{"html", "page.html"}, // a leading dot is optional, as in jinja2
		{"HTML", "page.HTML"},
	}
	for _, tc := range cases {
		env := New(WithAutoescapeFunc(SelectAutoescape(tc.ext)))
		if got := renderWith(t, env, tc.name, `{{ evil }}`); got != evilEscaped {
			t.Errorf("SelectAutoescape(%q) on %q did not escape:\n got %q\nwant %q",
				tc.ext, tc.name, got, evilEscaped)
		}
	}
}

// TestSelectAutoescapeMatchesOnExtensionBoundary pins that matching is on a
// whole extension and not on any trailing substring, as jinja2's normalisation
// to ".ext" enforces.
func TestSelectAutoescapeMatchesOnExtensionBoundary(t *testing.T) {
	env := New(WithAutoescapeFunc(SelectAutoescape("tml")))
	if got := renderWith(t, env, "page.html", `{{ evil }}`); got != evil {
		t.Errorf(`SelectAutoescape("tml") must not match "page.html":`+"\n got %q\nwant %q",
			got, evil)
	}
	if got := renderWith(t, env, "page.tml", `{{ evil }}`); got != evilEscaped {
		t.Errorf(`SelectAutoescape("tml") must match "page.tml":`+"\n got %q\nwant %q",
			got, evilEscaped)
	}
}

// TestSelectAutoescapeDefaults pins that the zero SelectAutoescapeConfig
// reproduces jinja2's own defaults.
func TestSelectAutoescapeDefaults(t *testing.T) {
	env := New(WithAutoescapeFunc(SelectAutoescapeWith(SelectAutoescapeConfig{})))
	for _, name := range []string{"page.html", "page.htm", "page.xml", "page.xhtml", "PAGE.HTML"} {
		if got := renderWith(t, env, name, `{{ evil }}`); got != evilEscaped {
			t.Errorf("default config should escape %q, got %q", name, got)
		}
	}
	if got := renderWith(t, env, "page.txt", `{{ evil }}`); got != evil {
		t.Errorf("default config should not escape page.txt, got %q", got)
	}
	if got := renderWith(t, env, "", `{{ evil }}`); got != evilEscaped {
		t.Errorf("default config should escape a string template, got %q", got)
	}
}

// TestSelectAutoescapeDisabledAndDefault covers jinja2's documented
// "escape everything except .txt" configuration, which the previous API could
// not express at all.
func TestSelectAutoescapeDisabledAndDefault(t *testing.T) {
	env := New(WithAutoescapeFunc(SelectAutoescapeWith(SelectAutoescapeConfig{
		Disabled: []string{"txt"},
		Default:  true,
	})))
	if got := renderWith(t, env, "page.txt", `{{ evil }}`); got != evil {
		t.Errorf("disabled extension should not escape, got %q", got)
	}
	for _, name := range []string{"page.rst", "page.md", "page.html"} {
		if got := renderWith(t, env, name, `{{ evil }}`); got != evilEscaped {
			t.Errorf("Default:true should escape %q, got %q", name, got)
		}
	}
	if got := renderWith(t, env, "", `{{ evil }}`); got != evilEscaped {
		t.Errorf("string template should escape by default, got %q", got)
	}
}

// TestSelectAutoescapeDisableForString pins the one explicit opt-out.
func TestSelectAutoescapeDisableForString(t *testing.T) {
	env := New(WithAutoescapeFunc(SelectAutoescapeWith(SelectAutoescapeConfig{
		Enabled:          []string{"html"},
		DisableForString: true,
	})))
	if got := renderWith(t, env, "", `{{ evil }}`); got != evil {
		t.Errorf("DisableForString should not escape a string template, got %q", got)
	}
	if got := renderWith(t, env, "page.html", `{{ evil }}`); got != evilEscaped {
		t.Errorf("DisableForString must not affect named templates, got %q", got)
	}
}

// TestAutoescapeFuncSeesFromString pins that a hand-written policy can tell the
// two cases apart, which is the whole reason the parameter exists.
func TestAutoescapeFuncSeesFromString(t *testing.T) {
	var sawFromString, sawNamed bool
	env := New(WithAutoescapeFunc(func(name string, fromString bool) bool {
		if fromString {
			sawFromString = true
			if name != "" {
				t.Errorf("fromString template should have an empty name, got %q", name)
			}
		} else {
			sawNamed = true
		}
		return fromString
	}))
	if got := renderWith(t, env, "", `{{ evil }}`); got != evilEscaped {
		t.Errorf("policy asked to escape string templates did not, got %q", got)
	}
	if got := renderWith(t, env, "page.html", `{{ evil }}`); got != evil {
		t.Errorf("policy asked not to escape named templates did, got %q", got)
	}
	if !sawFromString || !sawNamed {
		t.Errorf("policy was not consulted for both cases: fromString=%v named=%v",
			sawFromString, sawNamed)
	}
}

// TestWithAutoescapeAppliesToStringTemplates pins that the blanket switch still
// covers both kinds.
func TestWithAutoescapeAppliesToStringTemplates(t *testing.T) {
	on := New(WithAutoescape(true))
	if got := renderWith(t, on, "", `{{ evil }}`); got != evilEscaped {
		t.Errorf("WithAutoescape(true) should escape a string template, got %q", got)
	}
	if got := renderWith(t, on, "page.txt", `{{ evil }}`); got != evilEscaped {
		t.Errorf("WithAutoescape(true) should escape any named template, got %q", got)
	}
	off := New(WithAutoescape(false))
	if got := renderWith(t, off, "", `{{ evil }}`); got != evil {
		t.Errorf("WithAutoescape(false) should not escape, got %q", got)
	}
}

// TestSelectAutoescapeEmptyExtension pins what an extension that is nothing
// but dots means.
//
// jinja2 builds each pattern as "." + the extension with its dots stripped, so
// an empty one becomes ".", which selects a name ending in a dot. Dropping it
// instead made SelectAutoescape("") escape nothing at all -- the wrong
// direction for the one setting whose failure mode is cross-site scripting,
// and the direction this package treats as a bug everywhere else.
func TestSelectAutoescapeEmptyExtension(t *testing.T) {
	for _, ext := range []string{"", ".", "..."} {
		fn := SelectAutoescape(ext)
		if !fn("page.", false) {
			t.Errorf("SelectAutoescape(%q) must select a name ending in a dot", ext)
		}
		if fn("page.html", false) {
			t.Errorf("SelectAutoescape(%q) must not select page.html", ext)
		}
		if fn("page", false) {
			t.Errorf("SelectAutoescape(%q) must not select an extensionless name", ext)
		}
		// A template from a string still escapes: it has no name to
		// decide by, and default_for_string is on.
		if !fn("", true) {
			t.Errorf("SelectAutoescape(%q) must escape a string template", ext)
		}
	}

	// An explicit list that includes an empty entry keeps the others.
	fn := SelectAutoescapeWith(SelectAutoescapeConfig{Enabled: []string{"", "html"}})
	for name, want := range map[string]bool{"page.": true, "page.html": true, "page.txt": false} {
		if got := fn(name, false); got != want {
			t.Errorf(`SelectAutoescapeWith({"", "html"})(%q) = %v, want %v`, name, got, want)
		}
	}
}

// TestJoinIsMarkupOnlyWhenMarkupIsInvolved pins do_join's coercion rule.
//
// Autoescaping alone does not make a join Markup: jinja2 coerces only when
// there is markup to preserve -- a safe delimiter, or a safe item -- and
// otherwise joins the str()s into a plain string, leaving the escaping to the
// output. Escaping eagerly is invisible in `{{ xs|join(",") }}` and wrong for
// every other use of the result.
//
// Expectations taken from CPython jinja2 3.1.6 with autoescape on.
func TestJoinIsMarkupOnlyWhenMarkupIsInvolved(t *testing.T) {
	env := New(WithAutoescape(true))
	vars := map[string]any{"a": "a'", "b": "<i>", "sep": "&"}

	// |safe on the pprint keeps the repr readable here: without it the
	// output escapes the quotes and ampersands of the repr itself.
	for _, tc := range []struct{ expr, want string }{
		// Nothing safe: a plain string, unescaped, so its length is
		// what a reader would count.
		{`(xs|join(sep))|pprint|safe`, `"a'&<i>"`},
		{`(xs|join(sep)) is escaped`, "False"},
		{`xs|join(sep)|length`, "6"},
		// ... and it still reaches the page escaped, which is why the
		// bug was invisible here.
		{`xs|join(sep)`, "a&#39;&amp;&lt;i&gt;"},
		// A safe item coerces the join: the delimiter is escaped with
		// it, and the safe item is left alone.
		{`([a, b|safe]|join(sep))|pprint|safe`, `Markup('a&#39;&amp;<i>')`},
		{`([a, b|safe]|join(sep)) is escaped`, "True"},
		// A safe delimiter coerces it too, and stays raw itself.
		{`([a, b]|join(sep|safe))|pprint|safe`, `Markup('a&#39;&&lt;i&gt;')`},
	} {
		v := map[string]any{"xs": []any{"a'", "<i>"}}
		for k, val := range vars {
			v[k] = val
		}
		got, err := renderVars(t, env, "{{ "+tc.expr+" }}", v)
		if err != nil {
			t.Errorf("%s: %v", tc.expr, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s = %q, want %q", tc.expr, got, tc.want)
		}
	}
}

// TestReplaceEscapesWhatJinja2Escapes pins do_replace's autoescaping rule,
// which is finer than escaping everything.
//
// `old` is matched verbatim -- escaping it first made `|replace("&", "+")`
// hunt for `&amp;` in text it had just escaped itself, so it found the `&` of
// every *other* entity and rewrote the middle of them. `new` is escaped only
// when the subject it replaces into is Markup, and a plain subject with plain
// arguments stays a plain string for the output to escape once.
//
// Expectations from CPython jinja2 3.1.6 with autoescape on.
func TestReplaceEscapesWhatJinja2Escapes(t *testing.T) {
	env := New(WithAutoescape(true))
	vars := map[string]any{"s": "a&<b", "o": "&", "n": "<i>"}

	for _, tc := range []struct{ expr, want string }{
		// Nothing safe: a plain string, and `old` found the real `&`.
		{`(s|replace("&", "+"))|pprint|safe`, `'a+<b'`},
		{`(s|replace("&", "+")) is escaped`, "False"},
		// A Markup subject: `new` is escaped into it, `old` is not.
		{`(s|safe|replace(o, n))|pprint|safe`, `Markup('a&lt;i&gt;<b')`},
		// A Markup `old` forces the subject to be escaped first, and
		// then matches against the escaped text -- which is why this
		// finds the `&` of `&lt;` as well.
		{`(s|replace(o|safe, n))|pprint|safe`, `Markup('a&lt;i&gt;amp;&lt;i&gt;lt;b')`},
		// A Markup `new` against a plain subject does the same, and is
		// then inserted without being escaped again -- so the `a` of
		// `&amp;` is a match for `old` like any other.
		{`(s|replace("a", n|safe))|pprint|safe`, `Markup('<i>&<i>mp;&lt;b')`},
		// Both Markup: nothing is escaped at all.
		{`(s|safe|replace("a", n|safe))|pprint|safe`, `Markup('<i>&<b')`},
	} {
		got, err := renderVars(t, env, "{{ "+tc.expr+" }}", vars)
		if err != nil {
			t.Errorf("%s: %v", tc.expr, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s = %q, want %q", tc.expr, got, tc.want)
		}
	}
}

// TestAutoescapeScope pins which escaping applies where, which is not one
// setting per template.
//
// jinja2 keeps two. Text escapes by the setting where it was *written*: a
// macro body by where the macro was defined, and a {% block %} body by the
// template's own setting, because each block is compiled against a fresh eval
// context and does not see an {% autoescape %} it sits inside. A filter
// escapes by where it is *called*, because it is handed the context's eval
// context, which {% autoescape %} moves for the dynamic extent of its body --
// so a filter inside a macro or a block follows the caller. Whether a macro's
// or a block's result is trusted is decided at the call for the same reason.
//
// Constant folding follows the writing side, and stops entirely where the
// setting is not known until the render -- `{% autoescape x %}` with a
// variable -- which jinja2 calls a volatile eval context.
//
// Every expectation is CPython jinja2 3.1.6's, rendered against the same
// template. |pprint|safe reports the value's type without the repr itself
// being escaped on the way out.
func TestAutoescapeScope(t *testing.T) {
	vars := map[string]any{"s": "a", "mk": "&", "sep": "&", "v": false, "v2": true}

	for _, tc := range []struct {
		env  bool
		src  string
		want string
	}{
		// The block reaches the filter, in both directions.
		{true, `{% autoescape false %}{{ ([s, mk|safe]|join(sep))|pprint|safe }}{% endautoescape %}`, `'a&&'`},
		{true, `{% autoescape false %}{{ ([s, mk|safe]|join(sep)) is escaped }}{% endautoescape %}`, "False"},
		{false, `{% autoescape true %}{{ ([s, mk|safe]|join(sep))|pprint|safe }}{% endautoescape %}`, `Markup('a&amp;&')`},
		// A setting that is not known until the render reaches it too,
		// and nothing inside is folded.
		{true, `{% autoescape v %}{{ ([s, mk|safe]|join(sep))|pprint|safe }}{% endautoescape %}`, `'a&&'`},
		{false, `{% autoescape v2 %}{{ ([s, mk|safe]|join(sep))|pprint|safe }}{% endautoescape %}`, `Markup('a&amp;&')`},
		// A constant folded inside the block folds under the block.
		{true, `{% autoescape false %}{{ (['a', '&'|safe]|join('&'))|pprint|safe }}{% endautoescape %}`, `'a&&'`},
		// A {% block %} body does not: it is compiled against the
		// environment's setting, so the folded half and the run-time
		// half of the same block disagree, in jinja2 as here.
		{false, `{% autoescape true %}{% block b %}{{ (['a','&'|safe]|join('&'))|pprint|safe }}{% endblock %}{% endautoescape %}`, `'a&&'`},
		{false, `{% autoescape true %}{% block c %}{{ ([s, mk|safe]|join(sep))|pprint|safe }}{% endblock %}{% endautoescape %}`, `Markup('a&amp;&')`},
		// And the text it prints escapes by the template's setting, not
		// by the block it sits in -- which is why this stays raw.
		{false, `{% autoescape true %}{% block d %}{{ mk }}{% endblock %}{% endautoescape %}`, "&"},
		// A macro prints by where it was written...
		{true, `{% macro m(x) %}{{ [x, mk|safe]|join(sep) }}{% endmacro %}{% autoescape false %}{{ m(s) }}{% endautoescape %}`, "a&amp;&amp;"},
		// ... while its filters follow the call, so one call can use
		// both settings: the join below is unescaped and the text
		// printing it is escaped.
		{true, `{% macro m(x) %}{{ ([x, mk|safe]|join(sep))|pprint|safe }}{% endmacro %}{% autoescape false %}{{ m(s) }}{% endautoescape %}`, `'a&&'`},
		// Whether the result is trusted is the call's decision.
		{false, `{% macro m(x) %}{{ [x, mk|safe]|join(sep) }}{% endmacro %}{% autoescape true %}[{{ m(s) is escaped }}][{{ m(s) }}]{% endautoescape %}`, "[True][a&amp;&]"},
	} {
		got, err := renderVars(t, New(WithAutoescape(tc.env)), tc.src, vars)
		if err != nil {
			t.Errorf("env=%v %s: %v", tc.env, tc.src, err)
			continue
		}
		if got != tc.want {
			t.Errorf("env=%v %s\n  = %q\n want %q", tc.env, tc.src, got, tc.want)
		}
	}
}

// TestConcatIsMarkupOnlyWhenMarkupIsInvolved pins markup_join, which is what
// `~` compiles to.
//
// It walks the operands and only switches to joining as Markup once it meets
// one that already is; with none it concatenates the str()s into a plain
// string, to be escaped at output like any other value. Escaping regardless
// was invisible in `{{ a ~ b }}` and wrong for every other use of the result.
//
// Expectations from CPython jinja2 3.1.6 with autoescape on.
func TestConcatIsMarkupOnlyWhenMarkupIsInvolved(t *testing.T) {
	env := New(WithAutoescape(true))
	vars := map[string]any{"sv": "a<b", "n": 5, "mk": "<i>"}

	for _, tc := range []struct{ expr, want string }{
		// Nothing safe: a plain string, so its length is what a reader
		// would count and its type is what an error would name.
		{`(sv ~ n)|pprint|safe`, `'a<b5'`},
		{`(sv ~ n) is escaped`, "False"},
		{`(sv ~ n)|length`, "4"},
		{`(n ~ n)|pprint|safe`, `'55'`},
		// ... and it still reaches the page escaped.
		{`sv ~ n`, "a&lt;b5"},
		// One safe operand escapes every other one, in either position
		// -- markup_join rejoins the whole sequence when it finds one.
		{`(sv ~ mk|safe)|pprint|safe`, `Markup('a&lt;b<i>')`},
		{`(mk|safe ~ sv)|pprint|safe`, `Markup('<i>a&lt;b')`},
	} {
		got, err := renderVars(t, env, "{{ "+tc.expr+" }}", vars)
		if err != nil {
			t.Errorf("%s: %v", tc.expr, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s = %q, want %q", tc.expr, got, tc.want)
		}
	}
}

// TestVolatileAutoescapeFoldsAndDefers pins what a {% autoescape %} with a
// non-constant argument does to what is inside it.
//
// The block leaves the escaping unknowable until the render -- jinja2 calls
// that a volatile eval context -- and the two halves of one block then
// disagree. A constant print is still folded, and folded with the setting the
// block was supposed to replace, because a volatile context carries no value
// of its own. A filter is not folded at all: Filter.as_const refuses outright,
// so it runs at the render under the block's own setting.
//
// Expectations from CPython jinja2 3.1.6.
func TestVolatileAutoescapeFoldsAndDefers(t *testing.T) {
	vars := map[string]any{"yes": true, "blank": ""}
	for _, tc := range []struct {
		env  bool
		src  string
		want string
	}{
		// Environment off, block on: the constant keeps the
		// environment's setting, the filter follows the block.
		{false, `{% autoescape yes %}{{ {'a': 1} }}|{{ '<x>'|upper }}{% endautoescape %}`,
			`{'a': 1}|&lt;X&gt;`},
		// Environment on, block off: the mirror image.
		{true, `{% autoescape blank %}{{ {'a': 1} }}|{{ '<x>'|upper }}{% endautoescape %}`,
			`{&#39;a&#39;: 1}|<X>`},
		// A constant argument is not volatile, so both halves follow
		// the block and agree.
		{false, `{% autoescape true %}{{ {'a': 1} }}|{{ '<x>'|upper }}{% endautoescape %}`,
			`{&#39;a&#39;: 1}|&lt;X&gt;`},
	} {
		got, err := renderVars(t, New(WithAutoescape(tc.env)), tc.src, vars)
		if err != nil {
			t.Errorf("env=%v %s: %v", tc.env, tc.src, err)
			continue
		}
		if got != tc.want {
			t.Errorf("env=%v %s\n  = %q\n want %q", tc.env, tc.src, got, tc.want)
		}
	}
}

// TestAutoescapeIsAScope pins that {% autoescape %} scopes names, which is
// what jinja2 compiling it as a Scope means.
//
// What the body assigns does not reach the frame outside it -- so a context
// value that was shadowed inside is visible again after -- and a name that
// frame assigns *later* is already its local when the body reads it, which
// makes that read undefined rather than a fall-through to the context.
func TestAutoescapeIsAScope(t *testing.T) {
	vars := map[string]any{"q": 7}
	for _, tc := range []struct{ src, want string }{
		{`{% autoescape true %}{% set q = 1 %}{% endautoescape %}[{{ q }}]`, "[7]"},
		{`{% autoescape true %}[{{ q }}]{% endautoescape %}{% set q = 1 %}[{{ q }}]`, "[][1]"},
		// Inside the block the assignment is visible, as in any scope.
		{`{% autoescape true %}{% set q = 1 %}[{{ q }}]{% endautoescape %}`, "[1]"},
	} {
		got, err := renderVars(t, New(), tc.src, vars)
		if err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s = %q, want %q", tc.src, got, tc.want)
		}
	}
}

// TestVolatileConcatNeverEscapes pins an upstream quirk that a template can
// see.
//
// jinja2 picks the join for `~` with
//
//	(markup_join if context.eval_ctx.volatile else str_join)
//
// inside a volatile context -- and an eval context is only ever volatile at
// *compile* time, so the attribute the generated code reads is always False.
// `~` there concatenates str()s, escaping nothing and returning a plain
// string, which the output then escapes as a whole. One line further out, the
// same expression answers Markup.
//
// Volatility is lexical and never resets, so a nested constant block, a loop
// body and a macro written inside are all still inside it.
//
// Expectations from CPython jinja2 3.1.6.
func TestVolatileConcatNeverEscapes(t *testing.T) {
	vars := map[string]any{"yes": true, "mk": "<i>", "s": "a&b"}
	for _, tc := range []struct{ src, want string }{
		{`{% autoescape yes %}{{ (mk|safe) ~ s }}{% endautoescape %}`, "&lt;i&gt;a&amp;b"},
		{`{% autoescape yes %}{{ ((mk|safe) ~ s) is escaped }}{% endautoescape %}`, "False"},
		{`{% autoescape yes %}{{ ((mk|safe) ~ s)|length }}{% endautoescape %}`, "6"},
		// A literal argument is not volatile: Markup, as everywhere
		// else.
		{`{% autoescape true %}{{ (mk|safe) ~ s }}{% endautoescape %}`, "<i>a&amp;b"},
		// Nesting a constant block inside a volatile one does not
		// clear it.
		{`{% autoescape yes %}{% autoescape true %}{{ (mk|safe) ~ s }}{% endautoescape %}{% endautoescape %}`,
			"&lt;i&gt;a&amp;b"},
		// Nor does a loop body or a macro written inside.
		{`{% autoescape yes %}{% for i in [1] %}{{ (mk|safe) ~ s }}{% endfor %}{% endautoescape %}`,
			"&lt;i&gt;a&amp;b"},
		{`{% autoescape yes %}{% macro q() %}{{ (mk|safe) ~ s }}{% endmacro %}{{ q() }}{% endautoescape %}`,
			"&lt;i&gt;a&amp;b"},
	} {
		got, err := renderVars(t, New(), tc.src, vars)
		if err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s\n  = %q\n want %q", tc.src, got, tc.want)
		}
	}
}

// TestMarkupTimesUndefined pins which of two errors comes out.
//
// markupsafe's Markup.__mul__ asks the other operand for __index__ and lets
// that TypeError out. str.__mul__ returns NotImplemented instead, which hands
// the undefined its turn to raise. So the same expression reports two
// different things depending on whether the left side is safe.
func TestMarkupTimesUndefined(t *testing.T) {
	for _, tc := range []struct{ expr, want string }{
		{`(s|safe) * nope`, "'Undefined' object cannot be interpreted as an integer"},
		{`s * nope`, "'nope' is undefined"},
		{`nope * (s|safe)`, "'nope' is undefined"},
		{`(s|safe) * (0|attr('q'))`, "'Undefined' object cannot be interpreted as an integer"},
	} {
		_, err := renderVars(t, New(), "{{ "+tc.expr+" }}", map[string]any{"s": "a"})
		if err == nil {
			t.Errorf("%s: no error, want %q", tc.expr, tc.want)
			continue
		}
		if got := err.Error(); got != tc.want {
			t.Errorf("%s = %q, want %q", tc.expr, got, tc.want)
		}
	}
}
