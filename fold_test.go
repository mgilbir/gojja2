// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"strings"
	"testing"

	"github.com/mgilbir/gojja2/value"
)

// TestFoldableMatchesHasSafeRepr pins which constants may stand in for the
// expression that produced them.
//
// jinja2's has_safe_repr looks inside a container -- a list is foldable only
// when every element is, a dict only when every key and value are -- and the
// types it accepts are exact. A tuple subclass is not a tuple for this
// purpose, which is what makes a list of |groupby pairs unfoldable.
func TestFoldableMatchesHasSafeRepr(t *testing.T) {
	group := value.FromObject(&groupObject{key: value.String("a"), items: value.NewList()})
	for _, tc := range []struct {
		name string
		v    value.Value
		want bool
	}{
		{"none", value.None, true},
		{"int", value.Int(1), true},
		{"float", value.Float(1.5), true},
		{"string", value.String("a"), true},
		{"markup", value.Safe("a"), true},
		{"empty list", value.NewList(), true},
		{"nested literals", value.NewList(value.NewTuple(value.Int(1), value.String("x"))), true},
		{"range", value.FromObject(&rangeObject{stop: 3, step: 1}), true},
		// Not literals Python could write back.
		{"bytes", value.Bytes([]byte("x")), false},
		{"undefined", value.Undefined, false},
		{"a group tuple", group, false},
		// ... and a container is judged by what it holds.
		{"list of group tuples", value.NewList(group), false},
		{"dict with a group tuple value", dictOf(t, value.String("k"), group), false},
		{"list holding a list holding one", value.NewList(value.NewList(group)), false},
	} {
		if got := foldable(tc.v); got != tc.want {
			t.Errorf("foldable(%s) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func dictOf(t *testing.T, k, v value.Value) value.Value {
	t.Helper()
	d := value.NewDict()
	dict, _ := d.Dict()
	if err := dict.Set(k, v); err != nil {
		t.Fatalf("set: %v", err)
	}
	return d
}

// TestFoldingDoesNotSwallowAnError is the behaviour that rule protects: an
// unfoldable branch means the condition beside it runs, and running it raises.
func TestFoldingDoesNotSwallowAnError(t *testing.T) {
	src := `{% with w = blank if (-1)[-2:] else d|batch(2)|list|groupby('city')|list %}{% endwith %}`
	_, err := renderVars(t, mustNew(), src, map[string]any{"blank": "", "d": map[string]any{"1": "a"}})
	if err == nil {
		t.Fatal("rendered; want the TypeError CPython raises for a slice of an int")
	}
	if got, want := err.Error(), "'int' object is not subscriptable"; got != want {
		t.Errorf("error = %q, want %q", got, want)
	}
}

// TestFrameLocalsAliasTheEnclosingFrame pins where a frame's own names start
// from.
//
// A name an enclosing *frame* owns is aliased in at entry, so a macro body
// sees what the template had assigned when it was called. A name nothing above
// owns starts undefined, which is what makes a loop inside the frame read
// nothing until the assignment runs.
//
// A {% block %} body nests inside no frame at all: jinja2 compiles it as a
// standalone function resolving against the context, so a value passed in does
// not survive the block owning the name.
//
// Expectations from CPython jinja2 3.1.6.
func TestFrameLocalsAliasTheEnclosingFrame(t *testing.T) {
	vars := map[string]any{"m": 10}
	for _, tc := range []struct{ src, want string }{
		// The block owns m, so the loop inside it reads nothing --
		// even though m was passed in as 10.
		{`{% block a %}{% for i in [1] %}[{{ m }}]{% endfor %}{% set m = 1 %}[{{ m }}]{% endblock %}`, "[][1]"},
		// It only owns it if it assigns it.
		{`{% block a %}{% for i in [1] %}[{{ m }}]{% endfor %}{% endblock %}`, "[10]"},
		// A macro nests inside the frame that defined it, so it starts
		// from that frame's value rather than from the context's.
		{`{% set m = 1 %}{% macro q() %}{% for i in [1] %}[{{ m }}]{% endfor %}{% set m = 2 %}{% endmacro %}{{ q() }}`, "[1]"},
		// With nothing to alias, undefined.
		{`{% macro q() %}{% for i in [1] %}[{{ m }}]{% endfor %}{% set m = 2 %}{% endmacro %}{{ q() }}`, "[]"},
		// A load before the store keeps resolving from the context, at
		// any level.
		{`{% block a %}[{{ m }}]{% set m = 1 %}{% endblock %}`, "[10]"},
		{`[{{ m }}]{% set m = 1 %}`, "[10]"},
	} {
		got, err := renderVars(t, mustNew(), tc.src, vars)
		if err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s\n  = %q\n want %q", tc.src, got, tc.want)
		}
	}
}

// TestDynamicArgsFold pins that `*` and `**` fold with everything else.
//
// jinja2's args_as_const builds the argument list the way Python builds one --
// list.extend and dict.update -- rather than the way a call site checks one,
// and the difference shows. dict.update takes an iterable of pairs and lets a
// later name replace an earlier one; a call in the same shape refuses both. So
// a constant expression folds where the same expression over a name raises,
// and a subscript standing beside it is resolved away instead of running.
//
// Expectations from CPython jinja2 3.1.6.
func TestDynamicArgsFold(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		// The whole print folds, so the unsubscriptable receiver is
		// never subscripted for real.
		{`{{ (false)[1:]|join(*["-"]) }}`, ""},
		{`{{ (0o17)[1:]|center(*[4]) }}`, "    "},
		{`{{ ({})[1:]|default(*[1]) }}`, "1"},
		{`{{ (0o17)[1:] is eq(*[2]) }}`, "False"},
		// ... including inside a tag, which has no output folder of
		// its own to fall back on.
		{`{% if (0o17)[1:]|join(*["-"]) %}Y{% endif %}`, ""},
		// dict.update, not a call: pairs are accepted and a repeat
		// replaces rather than colliding.
		{`{{ [1,2]|join(**{"d": "-"}) }}`, "1-2"},
		{`{{ [1,2]|join(**["db"]) }}`, "1b2"},
		{`{{ [1,2]|join(d="-", **{"d": "+"}) }}`, "1+2"},
		// A star form that is not constant is left alone, and the call
		// then happens for real.
		{`{% set l = [1,2] %}{{ l|join(*["-"]) }}`, "1-2"},
	} {
		got, err := renderVars(t, mustNew(), tc.src, nil)
		if err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s\n  = %q\n want %q", tc.src, got, tc.want)
		}
	}
}

// TestDynamicArgsThatCannotFold pins the other side: when building the list
// would raise, jinja2 abandons the fold and the call happens at run time,
// where the star form is checked as a call site checks it.
func TestDynamicArgsThatCannotFold(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{`{{ [1,2]|join(*5) }}`, "Value after * must be an iterable, not int"},
		// The same pair-shaped update, over a name: folding it would
		// answer where a real call raises. CPython names the callee in
		// this message and gojja2 names Context.call, which is a
		// separate divergence -- the suffix is what this test is for.
		{`{% set l = [1,2] %}{{ l|join(**["db"]) }}`,
			"argument after ** must be a mapping, not list"},
	} {
		_, err := renderVars(t, mustNew(), tc.src, nil)
		if err == nil {
			t.Errorf("%s: rendered; want %q", tc.src, tc.want)
			continue
		}
		if got := err.Error(); !strings.HasSuffix(got, tc.want) {
			t.Errorf("%s\n  = %q\n want suffix %q", tc.src, got, tc.want)
		}
	}
}
