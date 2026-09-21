// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"sort"
	"strings"
	"testing"
)

// What Variables answers, case by case.
//
// conformance/nameflow_test.go grades the whole corpus against the independent
// implementation in tools/oracle/nameflow.py, which is what makes the answers
// trustworthy. This is the other half: it says in one place what the answers
// are supposed to mean, so a change of intent shows up as an argument about
// this table rather than as a silent drift the corpus happens to allow.
//
// Notation: "o" the value can be printed, "f" it can change the output without
// being printed, "-" neither, "?" no reliable negative.
func TestVariables(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		// The distinction this exists for.
		{`{{ a }}`, "a:o"},
		{`{% if flag %}hello{% endif %}`, "flag:f"},
		{`{% if flag %}{{ flag }}{% endif %}`, "flag:of"},

		// Dataflow: a value that reaches the output without being named
		// at an output position.
		{`{% set y = x %}{{ y }}`, "x:o"},
		{`{% set a = b %}{% set c = a %}{{ c }}`, "b:o"},
		{`{% set t %}{{ a }}{% endset %}{{ t }}`, "a:o"},
		{`{% with q = p %}{{ q }}{% endwith %}`, "p:o"},
		{`{% macro m(v) %}{{ v }}{% endmacro %}{{ m(q) }}`, "q:o"},

		// A conditional's test steers, its branches print -- even when the
		// whole expression is what gets printed.
		{`{{ "yes" if flag else "no" }}`, "flag:f"},
		{`{{ a if flag else b }}`, "a:o b:o flag:f"},
		{`{% set label = "yes" if flag else "no" %}{{ label }}`, "flag:f"},

		// A loop's sequence does both: its length decides how much is
		// rendered, and its elements reach the output through the target.
		{`{% for x in items %}{{ x }}{% endfor %}`, "items:of"},
		{`{% for x in items %}fixed{% endfor %}`, "items:f"},
		{`{% for x in xs %}{{ loop.index }}{% endfor %}`, "xs:of"},

		// The useful negative: read, but provably unable to change anything.
		{`{% set unused = secret %}done`, "secret:-"},

		// jinja2's first-mention rule decides whether a name is the
		// caller's at all. Both of these mention x; only the first reads it.
		{`{{ x }}{% set x = 1 %}`, "x:o"},
		{`{% for i in [1] %}{{ x }}{% endfor %}{% set x = 1 %}`, ""},
		// An assignment that may not run does not claim the name.
		{`{% if a %}{% set z = 1 %}{% endif %}{{ z }}`, "a:f z:o"},

		// Names the template is given rather than asked for.
		{`{{ range(3)|list }}`, ""},
		{`{% macro m() %}{{ caller() }}{% endmacro %}`, ""},

		// Where the analysis cannot see, it says so instead of guessing.
		{`{{ data[key] }}`, "data:o? key:o"},
		{`{% set ns = namespace(v=0) %}{% for i in xs %}{% set ns.v = i %}{% endfor %}{{ ns.v }}`, "xs:of?"},
		{`{% include "other.html" %}{{ a }}`, "a:o?"},
	} {
		env := mustNew()
		tmpl, err := env.FromString(tc.src)
		if err != nil {
			t.Errorf("%s: compile: %v", tc.src, err)
			continue
		}
		if got := formatVariables(tmpl.Variables()); got != tc.want {
			t.Errorf("%s\n got %q\nwant %q", tc.src, got, tc.want)
		}
	}
}

// Variables must not depend on the constant folder having run, or the answer
// becomes a fact about the optimizer rather than about the template.
func TestVariablesIgnoresFolding(t *testing.T) {
	env := mustNew()
	for _, tc := range []struct{ src, want string }{
		// The key folds to a constant, but it is not a key anything could
		// be looked up by, so the container is still not understood.
		{`{{ xs[[]] }}`, "xs:o?"},
		// A branch the folder can prove is never taken still says what the
		// template would do if it were.
		{`{% if false %}{{ secret }}{% endif %}`, "secret:o"},
		{`{{ "a" ~ "b" }}{{ v }}`, "v:o"},
	} {
		tmpl, err := env.FromString(tc.src)
		if err != nil {
			t.Fatalf("%s: compile: %v", tc.src, err)
		}
		if got := formatVariables(tmpl.Variables()); got != tc.want {
			t.Errorf("%s\n got %q\nwant %q", tc.src, got, tc.want)
		}
	}
}

func formatVariables(vs []Variable) string {
	out := make([]string, 0, len(vs))
	for _, v := range vs {
		s := ""
		if v.Output {
			s += "o"
		}
		if v.Flow {
			s += "f"
		}
		if s == "" {
			s = "-"
		}
		if v.Unknown {
			s += "?"
		}
		out = append(out, v.Name+":"+s)
	}
	sort.Strings(out)
	return strings.Join(out, " ")
}
