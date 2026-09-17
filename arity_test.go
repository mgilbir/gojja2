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

// TestBuiltinArityMessagesAreCPythonsOwn pins the wordings a C function uses,
// which cannot be read off its signature.
//
// jinja2 registers Python's own functions for several names, and they do not
// all speak alike: abs, len and callable word a wrong count as "takes exactly
// one argument (N given)", while the operator.* comparisons behind eq, lt and
// their aliases say "eq expected 2 arguments, got N" -- and name themselves
// "_operator.eq" when refusing a keyword. Assuming the first wording for all of
// them was wrong for 12 test names.
//
// A C function also reports too few the same way it reports too many, where a
// Python function has a "missing required positional argument" of its own --
// which is why `{{ 1 is eq }}` is in the table next to `test_sameas`.
//
// Every expectation is CPython jinja2 3.1.6's, and the wordings in arity.go are
// probed out of the same place rather than written here twice.
func TestBuiltinArityMessagesAreCPythonsOwn(t *testing.T) {
	env := New()
	for _, tc := range []struct{ src, want string }{
		{"{{ 1 is eq(1,2) }}", "eq expected 2 arguments, got 3"},
		{"{{ 1 is eq }}", "eq expected 2 arguments, got 1"},
		{"{{ 1 is lt(1,2) }}", "lt expected 2 arguments, got 3"},
		{"{{ 1 is equalto(1,2) }}", "eq expected 2 arguments, got 3"},
		{"{{ 1 is eq(zz=1) }}", "_operator.eq() takes no keyword arguments"},
		{"{{ 1 is callable(1) }}", "callable() takes exactly one argument (2 given)"},
		{"{{ 1 is callable(zz=1) }}", "callable() takes no keyword arguments"},
		{"{{ [1]|abs(1) }}", "abs() takes exactly one argument (2 given)"},
		{"{{ [1]|length(1) }}", "len() takes exactly one argument (2 given)"},
		{"{{ [1]|count(1) }}", "len() takes exactly one argument (2 given)"},
		// The Python-function wordings, for contrast: these are the
		// ones derived from the signature.
		{"{{ 1 is sameas(1,2) }}", "test_sameas() takes 2 positional arguments but 3 were given"},
		{"{{ 1 is divisibleby(1,2) }}", "test_divisibleby() takes 2 positional arguments but 3 were given"},
	} {
		tmpl, err := env.FromString(tc.src)
		if err != nil {
			t.Errorf("%s: compile: %v", tc.src, err)
			continue
		}
		err = tmpl.Render(context.Background(), io.Discard, nil)
		if err == nil {
			t.Errorf("%s: no error, want %q", tc.src, tc.want)
			continue
		}
		if got := err.Error(); got != tc.want {
			t.Errorf("%s = %q, want %q", tc.src, got, tc.want)
		}
	}
}

// TestDynamicKwargsNameTheCallee pins which function an unpacking error names.
//
// CPython has two ways of spelling a function in a TypeError: the code object
// being bound, which is what a wrong argument count reports, and the object
// being called, which is module and qualified name. A `**` that cannot be
// merged is refused at the call site, so it uses the second -- and jinja2
// calls a filter directly while calling everything else through Context.call,
// so the two are visibly different from a template.
//
// Expectations from CPython jinja2 3.1.6.
func TestDynamicKwargsNameTheCallee(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{`{{ lst|join(**5) }}`,
			"jinja2.filters.do_join() argument after ** must be a mapping, not int"},
		{`{{ lst is odd(**5) }}`,
			"jinja2.tests.test_odd() argument after ** must be a mapping, not int"},
		{`{{ lst|abs(**5) }}`, "abs() argument after ** must be a mapping, not int"},
		{`{{ lst is eq(**5) }}`, "_operator.eq() argument after ** must be a mapping, not int"},
		{`{% macro mm(x) %}{% endmacro %}{{ mm(**5) }}`,
			"jinja2.runtime.Context.call() argument after ** must be a mapping, not int"},
		{`{{ range(**5) }}`,
			"jinja2.runtime.Context.call() argument after ** must be a mapping, not int"},
		// A name given twice is refused by the merge, which words it
		// with "keyword argument" -- the binding, reached without the
		// unpacking, says only "argument".
		{`{{ lst|join(d="-", **{"d": "+"}) }}`,
			"jinja2.filters.do_join() got multiple values for keyword argument 'd'"},
		{`{{ lst|join("-", d="+") }}`,
			"sync_do_join() got multiple values for argument 'd'"},
		{`{% macro mm(x) %}{% endmacro %}{{ mm(x=1, **{"x": 2}) }}`,
			"jinja2.runtime.Context.call() got multiple values for keyword argument 'x'"},
		// The key check names nothing at all, not even the type.
		{`{{ lst|join(**{1: "-"}) }}`, "keywords must be strings"},
		// The star form names nothing either: jinja2 always passes
		// something before it, so CPython builds that list on its own.
		{`{{ lst|join(*5) }}`, "Value after * must be an iterable, not int"},
	} {
		_, err := renderVars(t, New(), tc.src, map[string]any{"lst": []any{1, 2}})
		if err == nil {
			t.Errorf("%s: rendered; want %q", tc.src, tc.want)
			continue
		}
		if got := err.Error(); got != tc.want {
			t.Errorf("%s\n  = %q\n want %q", tc.src, got, tc.want)
		}
	}
}

// TestTestArgumentsTakeTheirName pins that a test's argument can be given by
// name, the way a filter's can.
//
// jinja2's tests are ordinary Python functions, so `{{ 4 is divisibleby(2) }}`
// and `{{ 4 is divisibleby(num=2) }}` are the same call -- and the tested
// value is the first parameter, so naming *it* collides with the value
// already bound there. The names are jinja2's own, which is what arity.go
// already checks a wrong one against.
//
// The comparison tests are the exception: they are operator.eq and friends,
// C functions that take no keyword at all.
//
// Expectations from CPython jinja2 3.1.6.
func TestTestArgumentsTakeTheirName(t *testing.T) {
	vars := map[string]any{"lst": []any{1, 2}}
	for _, tc := range []struct{ src, want string }{
		{`{{ 4 is divisibleby(num=2) }}`, "True"},
		{`{{ 4 is divisibleby(**{"num": 2}) }}`, "True"},
		{`{{ 2 is sameas(other=2) }}`, "True"},
		{`{{ "a" is in(seq="ab") }}`, "True"},
		{`{{ lst is in(seq=[1,2]) }}`, "False"},
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
	for _, tc := range []struct{ src, want string }{
		{`{{ "a" is in(seq="ab", value="a") }}`,
			"test_in() got multiple values for argument 'value'"},
		{`{{ 4 is divisibleby(nummm=2) }}`,
			"test_divisibleby() got an unexpected keyword argument 'nummm'"},
		{`{{ 2 is eq(b=2) }}`, "_operator.eq() takes no keyword arguments"},
	} {
		_, err := renderVars(t, New(), tc.src, vars)
		if err == nil {
			t.Errorf("%s: rendered; want %q", tc.src, tc.want)
			continue
		}
		if got := err.Error(); got != tc.want {
			t.Errorf("%s\n  = %q\n want %q", tc.src, got, tc.want)
		}
	}
}
