// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"context"
	"io"
	"sort"
	"strings"
	"testing"
)

// TestRegisteredNamesMatchJinja2 pins that gojja2 implements exactly the
// filters and tests jinja2 does.
//
// The signatures in arity.go are read out of the pinned jinja2, so a name in
// one list and not the other means either a filter this engine does not have
// or one jinja2 does not -- and in the second case the arity of the extra one
// is checked against nothing. Both are worth knowing about the moment they
// happen rather than the next time somebody compares the two by hand.
func TestRegisteredNamesMatchJinja2(t *testing.T) {
	env := New()
	for _, tc := range []struct {
		what  string
		mine  []string
		jinja []string
	}{
		{"filter", keysOf(env.filters), keysOfSig(filterSignatures)},
		{"test", keysOf(env.tests), keysOfSig(testSignatures)},
	} {
		missing, extra := difference(tc.jinja, tc.mine), difference(tc.mine, tc.jinja)
		if len(missing) > 0 {
			t.Errorf("%s(s) jinja2 has and gojja2 does not: %s", tc.what, strings.Join(missing, ", "))
		}
		if len(extra) > 0 {
			t.Errorf("%s(s) gojja2 has and jinja2 does not, so nothing checks their arity: %s",
				tc.what, strings.Join(extra, ", "))
		}
	}
}

// TestArityIsCheckedForEveryFilter walks every filter and test and gives it
// seven arguments too many and an unknown keyword, asserting that each one
// refuses rather than ignoring them or reading them as the next parameter.
//
// The table is the point. Individual cases would cover whichever filters
// somebody thought of; this covers the ones nobody did, which is where all 85
// of the divergences were. What each one should refuse comes from its own
// signature -- a filter declared *args has no arity to exceed, and one
// declared **kwargs takes any keyword -- so the exemptions are jinja2's rather
// than a list kept here.
func TestArityIsCheckedForEveryFilter(t *testing.T) {
	env := New()
	for _, tc := range []struct {
		kind string
		sigs map[string]signature
		call func(name, args string) string
	}{
		{"filter", filterSignatures, func(n, a string) string { return "{{ v|" + n + "(" + a + ") }}" }},
		{"test", testSignatures, func(n, a string) string { return "{{ v is " + n + "(" + a + ") }}" }},
	} {
		for _, name := range keysOfSig(tc.sigs) {
			sig := tc.sigs[name]
			t.Run(tc.kind+"/"+name, func(t *testing.T) {
				if sig.total >= 0 {
					assertRefuses(t, env, tc.call(name, "1,2,3,4,5,6,7"), "too many positional arguments")
				}
				if !sig.varKw {
					assertRefuses(t, env, tc.call(name, "zzzz=1"), "an unknown keyword")
				}
			})
		}
	}
}

func assertRefuses(t *testing.T, env *Environment, src, why string) {
	t.Helper()
	tmpl, err := env.FromString(src)
	if err != nil {
		// A compile-time refusal is a refusal.
		return
	}
	err = tmpl.Render(context.Background(), io.Discard, map[string]any{"v": "x"})
	if err == nil {
		t.Errorf("%s accepted %s", src, why)
		return
	}
	if !strings.Contains(err.Error(), "argument") {
		t.Errorf("%s: got %q, want an argument error", src, err)
	}
}

func keysOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func keysOfSig(m map[string]signature) []string { return keysOf(m) }

// difference returns the members of a that are not in b.
func difference(a, b []string) []string {
	in := make(map[string]bool, len(b))
	for _, s := range b {
		in[s] = true
	}
	var out []string
	for _, s := range a {
		if !in[s] {
			out = append(out, s)
		}
	}
	return out
}
