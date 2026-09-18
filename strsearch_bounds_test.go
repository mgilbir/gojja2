// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"context"
	"testing"
)

// The start and end of a string search are slice indices, so an integer too
// wide for the machine is clamped, not refused.
//
// strSliceBounds said so in its own comment -- "clamped rather than refused
// when out of range" -- and then refused, because it read the bound with
// value.Int64 and treated "does not fit" as "is not an integer". Every search
// method took the same wrong turn, in both directions and in both positions.
//
// eval.go's sliceIndex had the right answer all along, with the right reason
// written beside it. That was the actual defect: one conversion with two
// implementations, and a template could tell which one it had reached --
// `{{ "abcde"[2**70:] }}` clamped while `{{ "abc".find("b", 2**70) }}` raised.
// There is one implementation now.
//
// Every expectation below is CPython's own answer, taken from the oracle.
func TestStringSearchBoundsClampLikeASlice(t *testing.T) {
	for _, tc := range []struct{ src, want, wantErr string }{
		{`{{ "abc".find("b",1180591620717411303424) }}`, "-1", ""},
		{`{{ "abc".find("b",-1180591620717411303424) }}`, "1", ""},
		{`{{ "abc".find("b",0,1180591620717411303424) }}`, "1", ""},
		{`{{ "abc".find("b",0,-1180591620717411303424) }}`, "-1", ""},
		{`{{ "abc".find("b",1180591620717411303424,1180591620717411303424) }}`, "-1", ""},
		{`{{ "abc".find("b",-1180591620717411303424,1180591620717411303424) }}`, "1", ""},
		{`{{ "abc".rfind("b",1180591620717411303424) }}`, "-1", ""},
		{`{{ "abc".rfind("b",-1180591620717411303424) }}`, "1", ""},
		{`{{ "abc".rfind("b",0,1180591620717411303424) }}`, "1", ""},
		{`{{ "abc".rfind("b",0,-1180591620717411303424) }}`, "-1", ""},
		{`{{ "abc".rfind("b",1180591620717411303424,1180591620717411303424) }}`, "-1", ""},
		{`{{ "abc".rfind("b",-1180591620717411303424,1180591620717411303424) }}`, "1", ""},
		{`{{ "abc".count("b",1180591620717411303424) }}`, "0", ""},
		{`{{ "abc".count("b",-1180591620717411303424) }}`, "1", ""},
		{`{{ "abc".count("b",0,1180591620717411303424) }}`, "1", ""},
		{`{{ "abc".count("b",0,-1180591620717411303424) }}`, "0", ""},
		{`{{ "abc".count("b",1180591620717411303424,1180591620717411303424) }}`, "0", ""},
		{`{{ "abc".count("b",-1180591620717411303424,1180591620717411303424) }}`, "1", ""},
		{`{{ "abc".startswith("b",1180591620717411303424) }}`, "False", ""},
		{`{{ "abc".startswith("b",-1180591620717411303424) }}`, "False", ""},
		{`{{ "abc".startswith("b",0,1180591620717411303424) }}`, "False", ""},
		{`{{ "abc".startswith("b",0,-1180591620717411303424) }}`, "False", ""},
		{`{{ "abc".startswith("b",1180591620717411303424,1180591620717411303424) }}`, "False", ""},
		{`{{ "abc".startswith("b",-1180591620717411303424,1180591620717411303424) }}`, "False", ""},
		{`{{ "abc".endswith("b",1180591620717411303424) }}`, "False", ""},
		{`{{ "abc".endswith("b",-1180591620717411303424) }}`, "False", ""},
		{`{{ "abc".endswith("b",0,1180591620717411303424) }}`, "False", ""},
		{`{{ "abc".endswith("b",0,-1180591620717411303424) }}`, "False", ""},
		{`{{ "abc".endswith("b",1180591620717411303424,1180591620717411303424) }}`, "False", ""},
		{`{{ "abc".endswith("b",-1180591620717411303424,1180591620717411303424) }}`, "False", ""},
		{`{{ "abc".index("b",1180591620717411303424) }}`, "", "substring not found"},
		{`{{ "abc".index("b",-1180591620717411303424) }}`, "1", ""},
		{`{{ "abc".index("b",0,1180591620717411303424) }}`, "1", ""},
		{`{{ "abc".index("b",0,-1180591620717411303424) }}`, "", "substring not found"},
		{`{{ "abc".index("b",1180591620717411303424,1180591620717411303424) }}`, "", "substring not found"},
		{`{{ "abc".index("b",-1180591620717411303424,1180591620717411303424) }}`, "1", ""},
		{`{{ "abc".rindex("b",1180591620717411303424) }}`, "", "substring not found"},
		{`{{ "abc".rindex("b",-1180591620717411303424) }}`, "1", ""},
		{`{{ "abc".rindex("b",0,1180591620717411303424) }}`, "1", ""},
		{`{{ "abc".rindex("b",0,-1180591620717411303424) }}`, "", "substring not found"},
		{`{{ "abc".rindex("b",1180591620717411303424,1180591620717411303424) }}`, "", "substring not found"},
		{`{{ "abc".rindex("b",-1180591620717411303424,1180591620717411303424) }}`, "1", ""},
		{`{{ "abc".find("b",1.5) }}`, "", "slice indices must be integers or None or have an __index__ method"},
		{`{{ "abc".rfind("b",1.5) }}`, "", "slice indices must be integers or None or have an __index__ method"},
		{`{{ "abc".count("b",1.5) }}`, "", "slice indices must be integers or None or have an __index__ method"},
		{`{{ "abc".startswith("b",1.5) }}`, "", "slice indices must be integers or None or have an __index__ method"},
		{`{{ "abc".endswith("b",1.5) }}`, "", "slice indices must be integers or None or have an __index__ method"},
		{`{{ "abc".index("b",1.5) }}`, "", "slice indices must be integers or None or have an __index__ method"},
		{`{{ "abc".rindex("b",1.5) }}`, "", "slice indices must be integers or None or have an __index__ method"},
	} {
		tmpl, err := New().FromString(tc.src)
		if err != nil {
			t.Errorf("%s: compile: %v", tc.src, err)
			continue
		}
		got, err := tmpl.RenderString(context.Background(), nil)
		if tc.wantErr != "" {
			if err == nil || err.Error() != tc.wantErr {
				t.Errorf("%s:\n  got  %q %v\n  want error %q", tc.src, got, err, tc.wantErr)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("%s = %q, %v; want %q", tc.src, got, err, tc.want)
		}
	}
}
