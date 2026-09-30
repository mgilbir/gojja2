// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"context"
	"regexp"
	"testing"
)

// TestCallableReprs pins the repr of each of the three callables a template can
// reach, which no corpus case can: every one of them ends in an address, and an
// address differs between two runs of CPython itself.
//
// The shape around it is exactly Python's, and it is three shapes rather than
// one. A method of a C type is `<built-in method get of dict object at 0x...>`,
// a method of a class written in Python is `<bound method Cycler.next of <...>>`
// with the receiver's own repr inside it, and a function is `<function NAME at
// 0x...>` under the name the *function* has rather than the one the template
// reached it by. All three printed `<function NAME>` before, with no address at
// all -- which is how a template could tell gojja2 from jinja2 by printing a
// method.
func TestCallableReprs(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{`{{ d.get }}`, `^<built-in method get of dict object at 0x[0-9a-f]+>$`},
		{`{{ 'ab'.upper }}`, `^<built-in method upper of str object at 0x[0-9a-f]+>$`},
		{`{{ [1].append }}`, `^<built-in method append of list object at 0x[0-9a-f]+>$`},
		{`{{ (1).to_bytes }}`, `^<built-in method to_bytes of int object at 0x[0-9a-f]+>$`},
		{`{{ d.keys().isdisjoint }}`,
			`^<built-in method isdisjoint of dict_keys object at 0x[0-9a-f]+>$`},
		{`{{ (d.keys() - 'a').union }}`,
			`^<built-in method union of set object at 0x[0-9a-f]+>$`},
		{`{{ cycler('a').next }}`,
			`^<bound method Cycler\.next of <jinja2\.utils\.Cycler object at 0x[0-9a-f]+>>$`},
		{`{% for i in [1] %}{{ loop.cycle }}{% endfor %}`,
			`^<bound method LoopContext\.cycle of <LoopContext 1/1>>$`},
		{`{{ lipsum }}`, `^<function generate_lorem_ipsum at 0x[0-9a-f]+>$`},
		// A class is not a function and prints as one, with no address.
		{`{{ range }}`, `^<class 'range'>$`},
		{`{{ cycler }}`, `^<class 'jinja2\.utils\.Cycler'>$`},
	} {
		tmpl, err := mustEnv().FromString(tc.src)
		if err != nil {
			t.Fatalf("compile %q: %v", tc.src, err)
		}
		got, err := tmpl.RenderString(context.Background(), map[string]any{"d": map[string]any{"a": 1}})
		if err != nil {
			t.Fatalf("%s: %v", tc.src, err)
		}
		if !regexp.MustCompile(tc.want).MatchString(got) {
			t.Errorf("%s = %q, want a match for %s", tc.src, got, tc.want)
		}
	}
}
