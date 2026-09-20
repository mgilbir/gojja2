// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mgilbir/gojja2"
)

// SelectAutoescape matches by suffix and has nowhere to report a mistake, so
// an argument that is not an extension never matches -- and never matching
// means never escaping. WithAutoescapeExtensions checks the argument, because
// this is the one setting whose failure mode is cross-site scripting.

func renderPage(t *testing.T, opt gojja2.Option) (string, error) {
	t.Helper()
	env, err := gojja2.New(
		gojja2.WithLoader(gojja2.DictLoader{"page.html": "{{ v }}"}), opt)
	if err != nil {
		return "", err
	}
	tmpl, err := env.GetTemplate("page.html")
	if err != nil {
		t.Fatalf("GetTemplate: %v", err)
	}
	out, err := tmpl.RenderString(context.Background(), map[string]any{"v": "<script>"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	return out, nil
}

func TestAutoescapeExtensionsRefusesWhatIsNotAnExtension(t *testing.T) {
	for _, tc := range []struct{ name, ext, want string }{
		{"glob", "*.html", "looks like a glob"},
		{"glob star only", "*", "looks like a glob"},
		{"character class", "[ht]ml", "looks like a glob"},
		{"question mark", "htm?", "looks like a glob"},
		{"path", "templates/*.html", "looks like a glob"},
		{"path no glob", "templates/index.html", "looks like a path"},
		{"windows path", `templates\index.html`, "looks like a path"},
		{"empty", "", "is empty"},
		{"dot only", ".", "is empty"},
		{"dots only", "...", "is empty"},
		{"whitespace", "ht ml", "contains whitespace"},
	} {
		_, err := gojja2.New(gojja2.WithAutoescapeExtensions(tc.ext))
		if err == nil {
			t.Errorf("%s: WithAutoescapeExtensions(%q) was accepted; it would silently "+
				"leave those templates unescaped", tc.name, tc.ext)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v\n  does not mention %q", tc.name, err, tc.want)
		}
	}
}

func TestAutoescapeExtensionsEscapesWhatItShould(t *testing.T) {
	for _, tc := range []struct {
		name    string
		exts    []string
		escaped bool
	}{
		{"bare", []string{"html"}, true},
		{"leading dot", []string{".html"}, true},
		{"uppercase", []string{"HTML"}, true},
		{"several", []string{"xml", "html"}, true},
		{"double extension", []string{".tar.gz"}, false},
		{"none given uses the defaults", nil, true},
		// A plausible-looking typo cannot be told from a real suffix,
		// so it is accepted and simply does not match. That is the
		// residual hazard, and it is the same one jinja2 has.
		{"plausible typo", []string{"hmtl"}, false},
	} {
		out, err := renderPage(t, gojja2.WithAutoescapeExtensions(tc.exts...))
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		got := out == "&lt;script&gt;"
		if got != tc.escaped {
			t.Errorf("%s: rendered %q, escaped=%v want %v", tc.name, out, got, tc.escaped)
		}
	}
}

// The knob: the same argument the default refuses is taken literally under
// AcceptAnyExtension, and then behaves exactly as jinja2 does -- matching
// nothing, and so escaping nothing.
func TestAutoescapeLeniencyMatchesJinja2WhenAsked(t *testing.T) {
	out, err := renderPage(t, gojja2.WithAutoescapeSelection(gojja2.SelectAutoescapeConfig{
		Enabled:  []string{"*.html"},
		Leniency: gojja2.AcceptAnyExtension,
	}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if out != "<script>" {
		t.Errorf("got %q, want the unescaped text jinja2 produces for a glob", out)
	}
	// And a good extension still works under the lenient setting.
	out, err = renderPage(t, gojja2.WithAutoescapeSelection(gojja2.SelectAutoescapeConfig{
		Enabled:  []string{".html"},
		Leniency: gojja2.AcceptAnyExtension,
	}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if out != "&lt;script&gt;" {
		t.Errorf("got %q, want it escaped", out)
	}
}

// The zero value of the config is the strict one, so a caller who says nothing
// about leniency gets the checking.
func TestAutoescapeSelectionDefaultsToStrict(t *testing.T) {
	_, err := gojja2.New(gojja2.WithAutoescapeSelection(gojja2.SelectAutoescapeConfig{
		Enabled: []string{"*.html"},
	}))
	if err == nil {
		t.Fatal("the zero-value config accepted a glob; the default must be strict")
	}
	if got := gojja2.RefuseImpossibleExtensions.String(); got != "RefuseImpossibleExtensions" {
		t.Errorf("zero value names itself %q", got)
	}
}

// A Disabled entry is checked too. It errs toward escaping more, which is
// safe, but it is still not what the caller wrote.
func TestAutoescapeSelectionChecksDisabledToo(t *testing.T) {
	_, err := gojja2.New(gojja2.WithAutoescapeSelection(gojja2.SelectAutoescapeConfig{
		Enabled:  []string{".html"},
		Disabled: []string{"*.txt"},
	}))
	if err == nil {
		t.Fatal("a glob in Disabled was accepted")
	}
	if !strings.Contains(err.Error(), "Disabled") {
		t.Errorf("%v does not say which list it came from", err)
	}
}

// The unchecked spelling still behaves as jinja2's select_autoescape does,
// including the part that makes the checked one worth having.
func TestSelectAutoescapeIsUnchanged(t *testing.T) {
	out, err := renderPage(t, gojja2.WithAutoescapeFunc(gojja2.SelectAutoescape("*.html")))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if out != "<script>" {
		t.Errorf("got %q, want the unescaped text jinja2 also produces", out)
	}
}
