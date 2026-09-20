// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"context"
	"strings"
	"testing"
)

// TestRandomIsChoice: do_random is `random.choice(seq)`, which is `len(seq)`
// and then `seq[i]` -- not an iteration. gojja2 materialised the value first,
// which changed three answers: a value with no length was refused as
// non-iterable rather than as something len() cannot take; a mapping was
// walked by key and answered one of them, where CPython subscripts it by the
// *index* and finds nothing unless that integer is a key; and only the empty
// case happened to line up.
func TestRandomIsChoice(t *testing.T) {
	env := mustNew()
	for _, tc := range []struct{ src, want string }{
		{`{{ 1|random }}`, "object of type 'int' has no len()"},
		{`{{ 1.5|random }}`, "object of type 'float' has no len()"},
		{`{{ none|random }}`, "object of type 'NoneType' has no len()"},
		{`{{ true|random }}`, "object of type 'bool' has no len()"},
		// A mapping is indexed, so a key that is not the index is not
		// found -- and a missing key is a KeyError, not an undefined.
		{`{{ {"a":1}|random }}`, "0"},
	} {
		tmpl, err := env.FromString(tc.src)
		if err != nil {
			t.Fatalf("compile %q: %v", tc.src, err)
		}
		out, err := tmpl.RenderString(context.Background(), nil)
		if err == nil {
			t.Errorf("%s: rendered %q, want %q", tc.src, out, tc.want)
			continue
		}
		if err.Error() != tc.want {
			t.Errorf("%s\n got %q\nwant %q", tc.src, err.Error(), tc.want)
		}
	}

	for _, tc := range []struct{ src, want string }{
		// An empty anything is the undefined jinja2 substitutes for
		// random.choice's IndexError -- including an undefined input,
		// which has length 0.
		{`[{{ []|random }}][{{ {}|random }}][{{ ""|random }}][{{ nope|random }}]`, "[][][][]"},
		// A mapping whose keys are the indices answers by index.
		{`{{ {0:"z"}|random }}`, "z"},
		// And a one-element sequence has only one answer.
		{`{{ [7]|random }}|{{ "q"|random }}|{{ (9,)|random }}`, "7|q|9"},
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
			t.Errorf("%s\n got %q\nwant %q", tc.src, got, tc.want)
		}
	}

	// Over many draws every element of a list comes up, which is what says
	// the index is random and not a constant.
	tmpl, err := env.FromString(`{{ ["a","b","c"]|random }}`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	seen := map[string]bool{}
	for range 200 {
		got, err := tmpl.RenderString(context.Background(), nil)
		if err != nil {
			t.Fatalf("render: %v", err)
		}
		seen[got] = true
	}
	for _, want := range []string{"a", "b", "c"} {
		if !seen[want] {
			t.Errorf("200 draws never produced %q (saw %v)", want, seenList(seen))
		}
	}
}

func seenList(m map[string]bool) string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return strings.Join(out, ",")
}
