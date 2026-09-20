// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"context"
	"strings"
	"testing"
)

// `ns.attr` parses to an NSRef, and only ever as an assignment target: the
// parser reaches that node from parseAssignTarget alone. The evaluator used to
// carry a case for reading one, which nothing could ever run -- `go tool cover`
// reported it at 0% while namespaces themselves were well covered, which is
// what gave it away.
//
// These pin the reachable halves, so that removing the unreachable one cannot
// have taken anything with it.
func TestNamespaceAssignmentAndReads(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"assign then read", "{% set ns = namespace(a=1) %}{% set ns.a = 2 %}{{ ns.a }}", "2"},
		{"read is a getattr", "{% set ns = namespace(a=1) %}{{ ns.a }}", "1"},
		{"self reference", "{% set ns = namespace(a=1) %}{% set ns.a = ns.a + 1 %}{{ ns.a }}", "2"},
		{"accumulate in a loop", "{% set ns = namespace(t=0) %}{% for i in [1,2,3] %}{% set ns.t = ns.t + i %}{% endfor %}{{ ns.t }}", "6"},
		{"missing attribute", "{% set ns = namespace() %}[{{ ns.nope }}]", "[]"},
		{"tuple target", "{% set ns = namespace() %}{% set a, ns.b = 1, 2 %}{{ a }}{{ ns.b }}", "12"},
		{"block form", "{% set ns = namespace() %}{% set ns.b %}body{% endset %}{{ ns.b }}", "body"},
		{"filtered block form", "{% set ns = namespace() %}{% set ns.b | upper %}body{% endset %}{{ ns.b }}", "BODY"},
	} {
		tmpl, err := mustEnv().FromString(tc.src)
		if err != nil {
			t.Errorf("%s: FromString: %v", tc.name, err)
			continue
		}
		got, err := tmpl.RenderString(context.Background(), nil)
		if err != nil {
			t.Errorf("%s: render: %v", tc.name, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

// And the dot form is refused outside an assignment target, which is what
// keeps the node from ever reaching the evaluator.
func TestDottedTargetOnlyParsesInSet(t *testing.T) {
	for _, src := range []string{
		"{% for ns.a in [1] %}{% endfor %}",
		"{% with ns.a = 1 %}{% endwith %}",
		"{% macro m(ns.a) %}{% endmacro %}",
	} {
		_, err := mustEnv().FromString(src)
		if err == nil {
			t.Errorf("%s: parsed, want a syntax error", src)
			continue
		}
		if !strings.Contains(err.Error(), "expected") && !strings.Contains(err.Error(), "assign") {
			t.Errorf("%s: %v", src, err)
		}
	}
}
