// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"context"
	"strings"
	"testing"
)

// The optional [start[, end]] bounds, graded against CPython.
//
// A window that starts past the end of the subject, or ends before it starts,
// matches nothing -- it is not the empty window at `start`. gojja2 clamped the
// end up to the start instead, which makes an inverted window empty rather than
// absent, and an empty needle then matched it. The difference is invisible to
// every other needle, which is why it went unnoticed: startswith, endswith,
// count, find, rfind, index and rindex all agreed with each other and
// disagreed with Python.
//
// The bytes methods, written later, had it right. Their rule is in
// bytesBounds and is now spelled the same way on the str side.
//
// Every expectation here is CPython 3.11's own answer, taken by running the
// expression rather than by reasoning about it. Four of them were written the
// other way first and four of them were wrong -- the engine was right and the
// test was not, which is the whole reason the oracle exists.
func TestSliceBoundsMatchCPython(t *testing.T) {
	for _, tc := range []struct{ expr, want string }{
		// An inverted window matches nothing, even an empty needle.
		{`"abcdef".startswith("", 4, 2)`, "False"},
		{`"abcdef".endswith("", 4, 2)`, "False"},
		{`"abcdef".count("", 4, 2)`, "0"},
		{`"abcdef".find("", 4, 2)`, "-1"},
		{`"abcdef".rfind("", 4, 2)`, "-1"},
		{`"abcdef".count("", -1, -3)`, "0"},
		{`"abcdef".find("", -1, -3)`, "-1"},
		// A start past the end is the same: not clamped to the end.
		{`"abcdef".startswith("", 99, 2)`, "False"},
		{`"abcdef".startswith("", 99)`, "False"},
		{`"abcdef".count("", 99)`, "0"},
		{`"abcdef".find("", 99)`, "-1"},
		// An empty window is not an inverted one, and an empty needle
		// does match it.
		{`"abcdef".startswith("", 3, 3)`, "True"},
		{`"abcdef".count("", 3, 3)`, "1"},
		{`"abcdef".find("", 3, 3)`, "3"},
		{`"abcdef".rfind("", 3, 3)`, "3"},
		{`"abcdef".find("", 6, 6)`, "6"},
		{`"abcdef".count("", 6, 6)`, "1"},
		// The ordinary cases, unchanged.
		{`"abcdef".count("", 0, 99)`, "7"},
		{`"héllo wörld".startswith("é", 1)`, "True"},
		{`"héllo wörld".startswith("w", -5)`, "True"},
		{`"héllo wörld".endswith("ö", 0, -4)`, "False"},
		{`"héllo wörld".count("l", 3, 5)`, "1"},
		{`"héllo wörld".count("l", -4)`, "1"},
		{`"héllo wörld".find("l", -3)`, "9"},
		{`"héllo wörld".rfind("l", 0, 5)`, "3"},
		{`"héllo wörld".rfind("ö", -5)`, "7"},
		{`"héllo wörld".index("l", 4)`, "9"},
		{`"héllo wörld".rindex("l", 0, 5)`, "3"},
		{`"aaa".count("aa")`, "1"},
		{`"aaa".count("a", 1, 2)`, "1"},
		{`"ααβ".find("β")`, "2"},
		{`"ααβ".count("α", 1)`, "1"},
		{`"ααβ".startswith("α", 1, 2)`, "True"},
		// A tuple of prefixes takes the same window.
		{`"abcdef".startswith(("z", ""), 4, 2)`, "False"},
		{`"abcdef".startswith(("z", "c"), 2, 4)`, "True"},
	} {
		tmpl, err := mustEnv().FromString("{{ " + tc.expr + " }}")
		if err != nil {
			t.Errorf("%s: compile: %v", tc.expr, err)
			continue
		}
		got, err := tmpl.RenderString(context.Background(), nil)
		if err != nil {
			t.Errorf("%s: %v", tc.expr, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s = %s, want %s (CPython's answer)", tc.expr, got, tc.want)
		}
	}
}

// index and rindex raise where find and rfind answer -1, and an inverted
// window is one of the places they do.
func TestInvertedBoundsRaiseForIndex(t *testing.T) {
	for _, expr := range []string{
		`"abcdef".index("", 4, 2)`,
		`"abcdef".rindex("", 4, 2)`,
		`"abcdef".index("c", 4, 2)`,
		`"abcdef".index("", 99)`,
	} {
		tmpl, err := mustEnv().FromString("{{ " + expr + " }}")
		if err != nil {
			t.Errorf("%s: compile: %v", expr, err)
			continue
		}
		_, err = tmpl.RenderString(context.Background(), nil)
		if err == nil || !strings.Contains(err.Error(), "substring not found") {
			t.Errorf("%s = %v, want a ValueError saying substring not found", expr, err)
		}
	}
}

// The bytes methods answer the same way, which is where the rule came from.
func TestBytesSliceBoundsAgree(t *testing.T) {
	vars := map[string]any{"b": []byte("abcdef"), "e": []byte("")}
	for _, tc := range []struct{ expr, want string }{
		{`b.startswith(e, 4, 2)`, "False"},
		{`b.endswith(e, 4, 2)`, "False"},
		{`b.count(e, 4, 2)`, "0"},
		{`b.find(e, 4, 2)`, "-1"},
		{`b.rfind(e, 4, 2)`, "-1"},
		{`b.count(e, 3, 3)`, "1"},
		{`b.find(e, 3, 3)`, "3"},
	} {
		tmpl, err := mustEnv().FromString("{{ " + tc.expr + " }}")
		if err != nil {
			t.Errorf("%s: compile: %v", tc.expr, err)
			continue
		}
		got, err := tmpl.RenderString(context.Background(), vars)
		if err != nil {
			t.Errorf("%s: %v", tc.expr, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s = %s, want %s", tc.expr, got, tc.want)
		}
	}
}
