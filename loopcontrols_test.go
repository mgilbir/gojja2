// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"errors"
	"strings"
	"testing"

	"github.com/mgilbir/gojja2/errs"
)

// jinja2 implements `{% break %}` and `{% continue %}` by emitting Python's own
// keywords, so what binds one is a `for` in the *generated function* -- and
// CPython refuses the module when nothing does. Its message names a line of
// that generated source, which has no counterpart here, so gojja2 refuses the
// same templates with the same wording and no line. docs/divergences.md records
// the difference; testdata/known_failures.txt admits it for the corpus cases
// that pin jinja2's half.
//
// Every shape below was measured against CPython jinja2 rather than reasoned
// about: which blocks are functions is not what a reader of the template would
// guess. A filter block, a `{% set %}` block, `{% with %}` and `{% autoescape %}`
// are emitted inline and a break reaches the loop through them.
func TestUnboundLoopControlIsRefused(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{`{% break %}`, "'break' outside loop"},
		{`{% continue %}`, "'continue' not properly in loop"},
		{`{% if 1 %}{% break %}{% endif %}`, "'break' outside loop"},
		// A loop's else body is emitted after the loop, not inside it.
		{`{% for x in [1] %}x{% else %}{% break %}{% endfor %}`, "'break' outside loop"},
		{`{% for x in [1] %}x{% else %}{% continue %}{% endfor %}`,
			"'continue' not properly in loop"},
		// A recursive loop puts its own `for` in a new function, and its
		// else body with it, so the enclosing loop does not reach either.
		{`{% for x in [1] %}{% for y in [1] recursive %}y{% else %}{% break %}{% endfor %}{% endfor %}`,
			"'break' outside loop"},
		// A macro, a block and a `{% call %}` body are functions.
		{`{% for x in [1] %}{% macro m() %}{% break %}{% endmacro %}{% endfor %}`,
			"'break' outside loop"},
		{`{% for x in [1] %}{% block b %}{% break %}{% endblock %}{% endfor %}`,
			"'break' outside loop"},
		{`{% for x in [1] %}{% call m() %}{% break %}{% endcall %}{% endfor %}`,
			"'break' outside loop"},
	} {
		env, err := New(WithExtensions("loopcontrols"))
		if err != nil {
			t.Fatal(err)
		}
		_, err = env.FromString(tc.src)
		if err == nil {
			t.Errorf("%s compiled; CPython refuses it with %q", tc.src, tc.want)
			continue
		}
		if got := err.Error(); got != tc.want {
			t.Errorf("%s\n  got  %q\n  want %q", tc.src, got, tc.want)
		}
		// The class is CPython's, because the refusal is CPython's.
		if !errors.Is(err, errs.SyntaxError) {
			t.Errorf("%s: refused as %v, want SyntaxError", tc.src, errs.KindOf(err))
		}
	}
}

// TestBoundLoopControlCompiles is the other half: every block a break *does*
// reach the loop through. Without it the gate above could refuse everything and
// still pass, and three of these read like function boundaries and are not.
func TestBoundLoopControlCompiles(t *testing.T) {
	for _, src := range []string{
		`{% for x in [1] %}{% break %}{% endfor %}`,
		`{% for x in [1] %}{% if x %}{% continue %}{% endif %}{% endfor %}`,
		`{% for x in [1] %}{% filter upper %}{% break %}{% endfilter %}{% endfor %}`,
		`{% for x in [1] %}{% set v %}{% break %}{% endset %}{% endfor %}`,
		`{% for x in [1] %}{% with y = x %}{% break %}{% endwith %}{% endfor %}`,
		`{% for x in [1] %}{% autoescape true %}{% break %}{% endautoescape %}{% endfor %}`,
		`{% for x in [1] recursive %}{% break %}{% endfor %}`,
		`{% macro m() %}{% for x in [1] %}{% break %}{% endfor %}{% endmacro %}`,
		`{% block b %}{% for x in [1] %}{% break %}{% endfor %}{% endblock %}`,
		// An inner loop's else body is inside the outer loop, so a break
		// there binds to the outer one.
		`{% for x in [1] %}{% for y in [] %}y{% else %}{% break %}{% endfor %}{% endfor %}`,
		// The gate is per body, not a counter that leaks: a loop that has
		// been closed does not license a later break.
		`{% for x in [1] %}{% break %}{% endfor %}{% for x in [1] %}{% break %}{% endfor %}`,
	} {
		env, err := New(WithExtensions("loopcontrols"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := env.FromString(src); err != nil {
			t.Errorf("%s was refused: %v", src, err)
		}
	}
}

// TestLoopControlDoesNotLeakPastTheLoop: the gate is what keeps the sentinel a
// break travels as from reaching a caller. Before it, `{% break %}` at the top
// level rendered an error whose whole message was "break".
func TestLoopControlSentinelNeverEscapes(t *testing.T) {
	env, err := New(WithExtensions("loopcontrols"))
	if err != nil {
		t.Fatal(err)
	}
	for _, src := range []string{`{% break %}`, `{% continue %}`} {
		if _, err := env.FromString(src); err == nil {
			t.Fatalf("%s compiled", src)
		} else if strings.TrimSpace(err.Error()) == "break" ||
			strings.TrimSpace(err.Error()) == "continue" {
			t.Errorf("%s: the loop-control sentinel reached the caller: %v", src, err)
		}
	}
}
