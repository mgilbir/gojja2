// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"fmt"

	"github.com/mgilbir/gojja2/value"
)

// The messages CPython rewords between releases, as the pinned interpreter
// words them.
//
// These tests grade the default environment, so their expectations move when
// the pin does. Writing the wording out again in each one meant that a bump
// rewrote thirty string literals by hand, which is both tedious and the exact
// way a wrong expectation gets pasted in and then believed.
//
// Asking the same rule the production code asks is not circular here: the rule
// is only *which* of two fixed sentences applies, and both sentences are still
// written out below. What proves them right is testdata/golden, which is CPython
// itself answering, and TestEveryPythonVersion, which grades all four.

// The two uses a key can be put to, named here so a test does not have to
// import value just to say which one it means.
const (
	asDictKey    = value.AsDictKey
	asSetElement = value.AsSetElement
)

// wantUnhashable is the refusal for a value that cannot be a dict key or a set
// element. Before 3.14 it named only the unhashable type; 3.14 also names what
// the key was being used as, and for a tuple containing a list those differ.
func wantUnhashable(outer, inner string, use value.HashUse) string {
	if DefaultPythonVersion.UnhashableNamesTheUse() {
		return fmt.Sprintf("cannot use '%s' as %s (unhashable type: '%s')",
			outer, string(use), inner)
	}
	return fmt.Sprintf("unhashable type: '%s'", inner)
}

// wantZeroDivision is ZeroDivisionError's sentence. Before 3.14 it named the
// operand kinds; 3.14 collapsed every case to one wording.
func wantZeroDivision(older string) string {
	if DefaultPythonVersion.UnifiedDivisionByZero() {
		return "division by zero"
	}
	return older
}

// wantNotInList is list.index's refusal. Before 3.14 it repeated the value it
// had been looking for; 3.14 stopped naming it.
func wantNotInList(repr string) string {
	if DefaultPythonVersion.IndexMessageIsGeneric() {
		return "list.index(x): x not in list"
	}
	return repr + " is not in list"
}
