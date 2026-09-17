// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"context"
	"strings"
	"testing"

	"github.com/mgilbir/gojja2/errs"
)

// TestIsFilterAndIsTestHashTheirValue: jinja2's `is filter` and `is test` are
// `value in env.filters` and `value in env.tests`, and a dict membership test
// hashes the value before anything looks at whether it could be a name.
//
// So an unhashable value raises rather than answering: `{{ [1] is filter }}` is
// "unhashable type: 'list'". gojja2 answered False for everything that was not
// a string, which made a question nothing could answer look answered.
func TestIsFilterAndIsTestHashTheirValue(t *testing.T) {
	env := New()
	for _, tc := range []struct{ src, want string }{
		{`{{ "upper" is filter }}|{{ "bogus" is filter }}`, "True|False"},
		// A name can be one and not the other.
		{`{{ "odd" is test }}|{{ "odd" is filter }}`, "True|False"},
		// Hashable but no name can equal it: still False, as `in` says.
		{`{{ 1 is filter }}|{{ none is test }}|{{ true is filter }}`, "False|False|False"},
		{`{{ (1,2) is filter }}|{{ "" is test }}`, "False|False"},
		{`{{ nope is filter }}`, "False"},
	} {
		tmpl, err := env.FromString(tc.src)
		if err != nil {
			t.Fatalf("compile %q: %v", tc.src, err)
		}
		got, err := tmpl.RenderString(context.Background(), nil)
		if err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s = %q, want %q", tc.src, got, tc.want)
		}
	}

	for _, tc := range []struct{ src, want string }{
		{`{{ [1] is filter }}`, "unhashable type: 'list'"},
		{`{{ [] is test }}`, "unhashable type: 'list'"},
		{`{{ {} is filter }}`, "unhashable type: 'dict'"},
		// A tuple is hashable only when everything in it is, which is
		// the same rule |attr's name goes through.
		{`{{ (1,[2]) is test }}`, "unhashable type: 'list'"},
	} {
		tmpl, err := env.FromString(tc.src)
		if err != nil {
			t.Fatalf("compile %q: %v", tc.src, err)
		}
		_, err = tmpl.RenderString(context.Background(), nil)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want %q", tc.src, err, tc.want)
			continue
		}
		if kind := errs.KindOf(err); kind != errs.TypeError {
			t.Errorf("%s: got %v, want TypeError", tc.src, kind)
		}
	}
}
