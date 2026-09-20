// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"context"
	"testing"
)

// Where center puts the odd character when the padding does not divide evenly.
//
// It goes on the *left* when the margin and the width are both odd, and on the
// right otherwise. CPython computes it as `marg / 2 + (marg & width & 1)`,
// which is a quirk of the C rather than a rule anyone would derive -- and the
// comment here said the opposite, so `{{ "ab".center(5) }}` was " ab  " where
// CPython has "  ab ".
//
// It went unnoticed because every existing case used an even margin, where the
// two rules agree.
func TestCenterPutsTheOddCharacterOnTheRightSide(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		// margin odd, width odd -> the extra goes left
		{`{{ "ab".center(5) }}`, "  ab "},
		{`{{ "ab".center(7) }}`, "   ab  "},
		{`{{ "abcd".center(7) }}`, "  abcd "},
		// margin odd, width even -> the extra goes right
		{`{{ "abc".center(6) }}`, " abc  "},
		{`{{ "a".center(4) }}`, " a  "},
		// margin even -> the two rules agree
		{`{{ "ab".center(6) }}`, "  ab  "},
		{`{{ "abc".center(7) }}`, "  abc  "},
		// no padding at all
		{`{{ "abc".center(3) }}`, "abc"},
		{`{{ "abc".center(1) }}`, "abc"},
		// the filter shares the same code
		{`{{ "ab"|center(5) }}`, "  ab "},
		{`{{ "abc"|center(6) }}`, " abc  "},
		// and a fill character does not change where the odd one goes
		{`{{ "ab".center(5, "*") }}`, "**ab*"},
		{`{{ "abc".center(6, "*") }}`, "*abc**"},
		// ljust and rjust have no halves to divide and are unaffected
		{`{{ "ab".ljust(5) }}`, "ab   "},
		{`{{ "ab".rjust(5) }}`, "   ab"},
	} {
		tmpl, err := mustNew().FromString(tc.src)
		if err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		got, err := tmpl.RenderString(context.Background(), nil)
		if err != nil || got != tc.want {
			t.Errorf("%s = %q, %v; want %q", tc.src, got, err, tc.want)
		}
	}
}

// The split itself, over every combination of parities, so the formula is
// pinned rather than inferred from a handful of renders.
func TestCenterSplitParities(t *testing.T) {
	for _, tc := range []struct {
		missing, width, left, right int
	}{
		{3, 5, 2, 1}, // both odd: extra left
		{5, 7, 3, 2}, // both odd
		{3, 6, 1, 2}, // margin odd, width even: extra right
		{1, 4, 0, 1}, // margin odd, width even
		{4, 6, 2, 2}, // margin even
		{2, 5, 1, 1}, // margin even
		{0, 3, 0, 0}, // nothing to divide
	} {
		left, right := centerSplit(tc.missing, tc.width)
		if left != tc.left || right != tc.right {
			t.Errorf("centerSplit(%d, %d) = %d, %d; want %d, %d",
				tc.missing, tc.width, left, right, tc.left, tc.right)
		}
		if left+right != tc.missing {
			t.Errorf("centerSplit(%d, %d) loses padding: %d+%d", tc.missing, tc.width, left, right)
		}
	}
}
