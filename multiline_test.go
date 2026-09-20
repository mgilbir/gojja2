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
)

// A tag's expression may span lines -- `{% if a\n and b %}` -- and so may the
// delimiters around it. These render identically to jinja2; what differs is
// which line an error inside such a tag is attributed to, which is pinned
// below and recorded in docs/divergences.md.

func TestMultiLineTagsRender(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"{% if true\n   and true %}X{% endif %}", "X"},
		{"{% if false\n   or true %}X{% endif %}", "X"},
		{"{% if\n  true %}X{% endif %}", "X"},
		{"{% if true %}X{%\n endif %}", "X"},
		{"{%\nif true\n%}X{%\nendif\n%}", "X"},
		{"{% if (1 +\n 2) == 3 %}X{% endif %}", "X"},
		{"{% if [1,\n2,\n3]|length == 3 %}X{% endif %}", "X"},
		{"{% if {'a':\n1}['a'] %}X{% endif %}", "X"},
		{"{% if true\n%}X{% elif false\n%}Y{% else\n%}Z{% endif %}", "X"},
		{"{% if a\n   is defined %}X{% else %}Y{% endif %}", "Y"},
		{"{% if 'a'\n   in ['a'] %}X{% endif %}", "X"},
		{"{% for i in [1,\n2] %}{{ i }}{% endfor %}", "12"},
		{"{% set x =\n  5 %}{{ x }}", "5"},
		{"{% macro m(a,\n b) %}{{ a }}{{ b }}{% endmacro %}{{ m(1,\n2) }}", "12"},
		{"{{ 1 +\n 2 }}", "3"},
		{"{{\n  'x'\n}}", "x"},
		{"{% filter\n upper %}y{% endfilter %}", "Y"},
		{"{% with x =\n 1 %}{{ x }}{% endwith %}", "1"},
	} {
		tmpl, err := gojja2.New().FromString(tc.src)
		if err != nil {
			t.Errorf("%q: FromString: %v", tc.src, err)
			continue
		}
		got, err := tmpl.RenderString(context.Background(), nil)
		if err != nil {
			t.Errorf("%q: render: %v", tc.src, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.src, got, tc.want)
		}
	}
}

// An error is reported on the line of the token that failed. jinja2 reports
// the line its code generator attributed the surrounding statement to, which
// for a multi-line tag is usually where the tag opened -- so these differ only
// when a tag spans lines. See docs/divergences.md.
func TestErrorLineInsideAMultiLineTag(t *testing.T) {
	for _, tc := range []struct {
		src      string
		wantLine int
	}{
		{"{% if true\n   and x|nope %}X{% endif %}", 2},
		{"{% if true\n   and\n   x|nope %}X{% endif %}", 3},
		{"{% if x|nope\n   and true %}X{% endif %}", 1},
		{"A\n{% if true\n   and x|nope %}X{% endif %}", 3},
		{"A\nB\nC\n{% if true\n\n\n   and x|nope %}X{% endif %}", 7},
		{"{% if true\n   and 1/0 %}X{% endif %}", 2},
		{"{% set x =\n  1/0 %}{{ x }}", 2},
		{"{{ 1 +\n   1/0 }}", 2},
		{"{% with x =\n  1/0 %}{% endwith %}", 2},
		// An error in the body, rather than in the tag, agrees with jinja2.
		{"line1\n{% if true\n and true %}\n{{ boom() }}\n{% endif %}", 4},
		{"a\nb\n{% for i in [1]\n %}\n{{ 1/0 }}\n{% endfor %}", 5},
	} {
		tmpl, err := gojja2.New().FromString(tc.src)
		if err == nil {
			_, err = tmpl.RenderString(context.Background(), nil)
		}
		if err == nil {
			t.Errorf("%q: rendered, want an error", tc.src)
			continue
		}
		var e *errs.Error
		if !errors.As(err, &e) {
			t.Errorf("%q: %v is not a template error", tc.src, err)
			continue
		}
		if e.Line != tc.wantLine {
			t.Errorf("%q:\n reported line %d, want %d\n (%s)",
				tc.src, e.Line, tc.wantLine, strings.SplitN(e.Msg, "\n", 2)[0])
		}
	}
}
