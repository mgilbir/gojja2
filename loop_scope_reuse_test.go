// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"context"
	"testing"
)

// A loop body whose scope cannot outlive the iteration is run in one reused
// frame rather than a fresh one per pass. A macro is the exception: it closes
// over the scope it was written in, so a body containing one -- at any depth --
// must keep getting a new frame.
//
// These are the cases that tell the two apart. Every "captures" case below
// renders the last iteration's value for *every* captured macro if the reuse
// is applied unconditionally.

func renderLoop(t *testing.T, src string) string {
	t.Helper()
	tmpl, err := mustEnv().FromString(src)
	if err != nil {
		t.Fatalf("FromString(%q): %v", src, err)
	}
	out, err := tmpl.RenderString(context.Background(), nil)
	if err != nil {
		t.Fatalf("render(%q): %v", src, err)
	}
	return out
}

func TestAMacroInALoopKeepsItsOwnIteration(t *testing.T) {
	const collect = "{% set ns = namespace(f=[]) %}"
	const call = "{% for g in ns.f %}[{{ g() }}]{% endfor %}"
	for _, tc := range []struct{ name, body, want string }{
		{
			"directly in the body",
			"{% for i in [1,2,3] %}{% macro m() %}{{ i }}{% endmacro %}{{ ns.f.append(m) }}{% endfor %}",
			"NoneNoneNone[1][2][3]",
		},
		{
			"inside a nested loop",
			"{% for i in [1,2] %}{% for j in [1,2] %}{% macro m() %}{{ i }}{{ j }}{% endmacro %}{{ ns.f.append(m) }}{% endfor %}{% endfor %}",
			"NoneNoneNoneNone[11][12][21][22]",
		},
		{
			"under an if",
			"{% for i in [1,2] %}{% if true %}{% macro m() %}{{ i }}{% endmacro %}{{ ns.f.append(m) }}{% endif %}{% endfor %}",
			"NoneNone[1][2]",
		},
		{
			"inside a with",
			"{% for i in [1,2] %}{% with %}{% macro m() %}{{ i }}{% endmacro %}{{ ns.f.append(m) }}{% endwith %}{% endfor %}",
			"NoneNone[1][2]",
		},
		{
			"inside a filter block",
			"{% for i in [1,2] %}{% filter lower %}{% macro m() %}{{ i }}{% endmacro %}{{ ns.f.append(m) }}{% endfilter %}{% endfor %}",
			"nonenone[1][2]",
		},
		{
			"inside a set block",
			"{% for i in [1,2] %}{% set unused %}{% macro m() %}{{ i }}{% endmacro %}{{ ns.f.append(m) }}{% endset %}{% endfor %}",
			"[1][2]",
		},
	} {
		if got := renderLoop(t, collect+tc.body+call); got != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, got, tc.want)
		}
	}
}

// The bodies that cannot capture are the ones that get the reused frame, so
// these pin that reuse changes nothing about them.
func TestALoopBodyWithoutCaptureIsUnchanged(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"set is per iteration", "{% for i in [1,2,3] %}{% set x = i * 2 %}{{ x }}{% endfor %}", "246"},
		{"set does not leak forward", "{% for i in [1,2,3] %}{{ y|default('-') }}{% set y = i %}{% endfor %}", "---"},
		{"nested loops", "{% for i in [1,2] %}{% for j in [1,2] %}{{ i }}{{ j }},{% endfor %}{% endfor %}", "11,12,21,22,"},
		{"inner loop shadows", "{% for i in [1,2] %}{% for i in [8,9] %}{{ i }}{% endfor %}-{{ i }};{% endfor %}", "89-1;89-2;"},
		{"loop attributes", "{% for i in [1,2,3] %}{{ loop.index }}{{ loop.first }}{{ loop.last }},{% endfor %}", "1TrueFalse,2FalseFalse,3FalseTrue,"},
		{"namespace accumulates", "{% set ns = namespace(t=0) %}{% for i in [1,2,3] %}{% set ns.t = ns.t + i %}{% endfor %}{{ ns.t }}", "6"},
		{"scoped block", "{% for i in [1,2] %}{% block b scoped %}<{{ i }}>{% endblock %}{% endfor %}", "<1><2>"},
		{"loop else", "{% for i in [] %}x{% else %}none{% endfor %}", "none"},
		{"filtered loop", "{% for i in [1,2,3] if i > 1 %}{{ i }}{{ loop.index }},{% endfor %}", "21,32,"},
		{"macro called inside", "{% for i in [1,2,3] %}{% macro m() %}{{ i }}{% endmacro %}{{ m() }}{% endfor %}", "123"},
	} {
		if got := renderLoop(t, tc.src); got != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, got, tc.want)
		}
	}
}
