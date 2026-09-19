// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"context"
	"testing"

	"github.com/mgilbir/gojja2"
	"github.com/mgilbir/gojja2/value"
)

// jinja2 applies finalize from visit_Output and nowhere else, so it runs on
// what a print tag evaluates and never on text a construct *captured*. gojja2
// ran it on the output of {% filter %} and {% call %} blocks too, which
// applied it twice to anything printed inside one -- and once to a block that
// printed nothing at all.
//
// The corpus cannot reach this: the oracle takes no finalize setting, because
// it would have to be the same function on both sides.

// angle is the Go spelling of `lambda v: "<%r>" % (v,)`, so a value that has
// been through it says so, and one that has been through it twice says that.
func angle(v value.Value) value.Value {
	return value.String("<" + value.Repr(v) + ">")
}

func renderFinalized(t *testing.T, src string, opts ...gojja2.Option) string {
	t.Helper()
	opts = append([]gojja2.Option{gojja2.WithFinalize(angle)}, opts...)
	tmpl, err := gojja2.New(opts...).FromString(src)
	if err != nil {
		t.Fatalf("FromString(%q): %v", src, err)
	}
	out, err := tmpl.RenderString(context.Background(), map[string]any{"x": "a"})
	if err != nil {
		t.Fatalf("render(%q): %v", src, err)
	}
	return out
}

func TestFinalizeRunsOnPrintsOnly(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		// A print tag, which is the one place it runs.
		{"{{ x }}", `<'a'>`},
		{"{{ 1 }}", `<1>`},
		// Literal template data never goes through it.
		{"plain", "plain"},
		// A filter block hands over what it captured. The print inside
		// is finalized; the block's own result is not.
		{"{% filter upper %}plain{% endfilter %}", "PLAIN"},
		{"{% filter upper %}{{ x }}{% endfilter %}", `<'A'>`},
		{"{% filter upper %}{{ x }}plain{% endfilter %}", `<'A'>PLAIN`},
		{"{% filter lower|upper %}{{ x }}{% endfilter %}", `<'A'>`},
		{"{% filter upper %}{% filter lower %}{{ x }}{% endfilter %}{% endfilter %}", `<'A'>`},
		// A call block likewise: the prints inside are finalized, the
		// assembled result is not.
		{"{% macro w() %}[{{ caller() }}]{% endmacro %}{% call w() %}{{ x }}{% endcall %}",
			`[<"<'a'>">]`},
		// A macro *call* is a print, so its result is finalized -- once
		// here and once for the print inside the body.
		{"{% macro m() %}{{ 1 }}{% endmacro %}{{ m() }}", `<'<1>'>`},
		{"{% macro m() %}plain{% endmacro %}{{ m() }}", `<'plain'>`},
		// A set block captures; the later print finalizes.
		{"{% set s %}{{ x }}{% endset %}{{ s }}", `<"<'a'>">`},
		{"{% set s %}plain{% endset %}{{ s }}", `<'plain'>`},
		// Constructs that only contain prints finalize once.
		{"{% for i in [1] %}{{ i }}{% endfor %}", `<1>`},
		{"{% with y = 1 %}{{ y }}{% endwith %}", `<1>`},
		{"{% block b %}{{ 3 }}{% endblock %}", `<3>`},
	} {
		if got := renderFinalized(t, tc.src); got != tc.want {
			t.Errorf("%s\n got %q\nwant %q", tc.src, got, tc.want)
		}
	}
}

// Without a finalize hook the same templates are unaffected, so the split
// cannot have moved anything else.
func TestBlockOutputUnchangedWithoutFinalize(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"{% filter upper %}{{ x }}plain{% endfilter %}", "APLAIN"},
		{"{% macro w() %}[{{ caller() }}]{% endmacro %}{% call w() %}{{ x }}{% endcall %}", "[a]"},
		{"{% set s %}{{ x }}{% endset %}{{ s }}", "a"},
	} {
		tmpl, err := gojja2.New().FromString(tc.src)
		if err != nil {
			t.Fatalf("FromString(%q): %v", tc.src, err)
		}
		got, err := tmpl.RenderString(context.Background(), map[string]any{"x": "a"})
		if err != nil {
			t.Fatalf("render(%q): %v", tc.src, err)
		}
		if got != tc.want {
			t.Errorf("%s\n got %q\nwant %q", tc.src, got, tc.want)
		}
	}
}
