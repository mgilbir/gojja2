// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import "testing"

// TestCallerCrossesANestedMacro pins that a nested macro is not a boundary for
// the search that decides whether a macro accepts a caller.
//
// jinja2's UndeclaredNameVisitor stops at a block and nothing else -- "it will
// not stop at closure frames" -- so `caller` mentioned inside a nested macro
// makes the enclosing one accept a caller too, and {% call %} on the outer
// macro works instead of refusing the argument it was handed.
func TestCallerCrossesANestedMacro(t *testing.T) {
	env := mustNew()
	for _, tc := range []struct{ src, want string }{
		{`{% macro mm(x) %}{% macro nn() %}{{ caller() }}{% endmacro %}{% call nn() %}I{% endcall %}{% endmacro %}{% call mm(1) %}B{% endcall %}`, "I"},
		{`{% macro mm(x) %}{% macro nn() %}{{ caller() }}{% endmacro %}{% endmacro %}{% call mm(1) %}B{% endcall %}`, ""},
		// The macro's own caller still works, and one that mentions it
		// nowhere still refuses.
		{`{% macro mm(x) %}{{ caller() }}{% endmacro %}{% call mm(1) %}B{% endcall %}`, "B"},
	} {
		got, err := renderVars(t, env, tc.src, nil)
		if err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s = %q, want %q", tc.src, got, tc.want)
		}
	}
	if _, err := renderVars(t, env, `{% macro mm(x) %}no caller{% endmacro %}{% call mm(1) %}B{% endcall %}`, nil); err == nil {
		t.Error("a macro that never mentions caller accepted one")
	}
}
