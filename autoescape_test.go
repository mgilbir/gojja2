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
