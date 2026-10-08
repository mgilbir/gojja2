// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"context"
	"errors"
	"testing"

	"github.com/mgilbir/gojja2/errs"
)

// TestAddChain holds `a + b + c ...` to what it did before a run of plain
// strings was joined in one go. The outputs and messages are CPython jinja2's,
// asked of the oracle; the lines are gojja2's own from before the change, since
// jinja2 reports none here, and each addition still owns its line.
func TestAddChain(t *testing.T) {
	for _, tc := range []struct {
		src  string
		vars map[string]any
		want string
		err  string
		line int
	}{
		// A Markup operand escapes the other side, so it ends a run.
		{src: "{{ 'a' + x|safe + '<' }}", vars: map[string]any{"x": "b"}, want: "ab&lt;"},
		{src: "{{ '<' + y + x|safe + '<' + y }}", vars: map[string]any{"x": ">", "y": "&"}, want: "&lt;&amp;>&lt;&amp;"},
		// Order is kept, through more operands than the run's buffer holds.
		{src: "{{ a + b + c + d + a + b + c + d + a + b + c + d }}",
			vars: map[string]any{"a": "1", "b": "2", "c": "3", "d": "4"}, want: "123412341234"},
		{src: "{{ l + l + [3] }}|{{ t + t }}", vars: map[string]any{"l": []int{1}, "t": []int{2}}, want: "[1, 1, 3]|[2, 2]"},
		// Operands are still evaluated in order, once each, around the
		// additions between them.
		{src: "{% set ns = namespace(l=[]) %}{{ y + y ~ ns.l.append(1) ~ y + y + ns.l|length|string }}",
			vars: map[string]any{"y": "a"}, want: "aaNoneaa1"},
		// Each failing addition is reported on its own node's line, after
		// a run of strings before it.
		{src: "{{ y\n + y\n + 1\n + y }}", vars: map[string]any{"y": "a"},
			err: `can only concatenate str (not "int") to str`, line: 3},
		{src: "{{ y\n + y\n + y\n + n\n + y }}", vars: map[string]any{"y": "a", "n": 1},
			err: `can only concatenate str (not "int") to str`, line: 4},
		{src: "{{ n\n + n\n + y\n + y }}", vars: map[string]any{"y": "a", "n": 1},
			err: "unsupported operand type(s) for +: 'int' and 'str'", line: 3},
		{src: "{{ y + y +\n nope + y }}", vars: map[string]any{"y": "a"}, err: "'nope' is undefined", line: 1},
	} {
		out, err := mustEnv().FromString(tc.src)
		if err != nil {
			t.Fatalf("%q: compile: %v", tc.src, err)
		}
		got, err := out.RenderString(context.Background(), tc.vars)
		if tc.err == "" {
			if err != nil || got != tc.want {
				t.Errorf("%q = %q, %v; want %q", tc.src, got, err, tc.want)
			}
			continue
		}
		var e *errs.Error
		if !errors.As(err, &e) {
			t.Errorf("%q: got %q, %v; want the error %q", tc.src, got, err, tc.err)
			continue
		}
		if e.Msg != tc.err || e.Line != tc.line {
			t.Errorf("%q: error %q on line %d; want %q on line %d", tc.src, e.Msg, e.Line, tc.err, tc.line)
		}
	}
}
