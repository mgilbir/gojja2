// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"strings"
	"testing"

	"github.com/mgilbir/gojja2/errs"
	"github.com/mgilbir/gojja2/value"
)

// pair is a host object standing for a tuple subclass, the shape
// value.TupleView exists for. |groupby yields one of these internally; this is
// the same contract offered to a caller, and the rules below are the tuple
// type's, not groupby's.
type pair struct{ a, b value.Value }

func (p pair) AsTuple() value.Value { return value.NewTuple(p.a, p.b) }
func (p pair) Len() int             { return 2 }
func (p pair) TypeName() string     { return "Pair" }
func (p pair) GetAttr(string) (value.Value, bool) {
	return value.Undefined, false
}

func (p pair) GetIndex(i int) (value.Value, bool) {
	switch i {
	case 0:
		return p.a, true
	case 1:
		return p.b, true
	}
	return value.Undefined, false
}

func (p pair) Repr() string {
	return "(" + value.Repr(p.a) + ", " + value.Repr(p.b) + ")"
}

// TestTupleSubclassBehavesAsTuple pins the rule that a TupleView is the tuple
// it stands for wherever the *tuple type* decides what happens, and is named as
// itself everywhere else.
//
// Every expectation here was taken from CPython running the same expression
// against `class Pair(tuple)`, so what the table asserts is jinja2's behaviour
// and not this implementation's.
func TestTupleSubclassBehavesAsTuple(t *testing.T) {
	vars := map[string]any{"p": pair{value.Int(1), value.Int(2)}}

	for _, tc := range []struct{ expr, want string }{
		// Concatenation: tuple.__add__ runs, from either side.
		{"p + (3,)", "(1, 2, 3)"},
		{"(3,) + p", "(3, 1, 2)"},
		{"p + p", "(1, 2, 1, 2)"},
		// Repetition: __mul__ and __rmul__ alike.
		{"p * 2", "(1, 2, 1, 2)"},
		{"2 * p", "(1, 2, 1, 2)"},
		{"p * 0", "()"},
		// Slicing yields a tuple, not a list.
		{"p[:1]", "(1,)"},
		{"p[::-1]", "(2, 1)"},
		{"p[1:]", "(2,)"},
		{"p[0]", "1"},
		// tuple's own methods are inherited.
		{"p.index(2)", "1"},
		{"p.count(1)", "1"},
		// A tuple subclass *is* the argument tuple of %.
		{`"%s-%s" % p`, "1-2"},
		// Ordering compares pair by pair.
		{"p < (1, 3)", "True"},
		{"(1, 3) < p", "False"},
		{"p == (1, 2)", "True"},
		// And it reaches the sequence filters as the tuple it is.
		{"p|list", "[1, 2]"},
		{"p|length", "2"},
		{"p|sum", "3"},
		{"[p]|sum(start=())", "(1, 2)"},
		{"1 in p", "True"},
		{`p|join(",")`, "1,2"},
		{"p", "(1, 2)"},
		// Equality follows the same rule, in both positions -- and with
		// it everything built on equality: hashing into a dict, `in`,
		// |unique, and equality of containers holding one.
		{"(1, 2) == p", "True"},
		{"p != (1, 2)", "False"},
		{"p == [1, 2]", "False"},
		{`{(1,2): "x"}[p]`, "x"},
		{`p in {(1,2): "x"}`, "True"},
		{`{p: "y"}[(1,2)]`, "y"},
		{"p in [(1,2)]", "True"},
		{"[(1,2)].index(p)", "0"},
		{"p in ((1,2),)", "True"},
		{"[p, (1,2)]|unique|list", "[(1, 2)]"},
		{`{"k": p} == {"k": (1,2)}`, "True"},
	} {
		got, err := renderVars(t, New(), "{{ "+tc.expr+" }}", vars)
		if err != nil {
			t.Errorf("%s: %v", tc.expr, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s = %q, want %q", tc.expr, got, tc.want)
		}
	}
}

// TestTupleSubclassErrorsNameTheSubclass is the other half of the rule: the
// operation is tuple's, but the type reported is the one the template has --
// except where CPython's own message hardcodes the base class.
func TestTupleSubclassErrorsNameTheSubclass(t *testing.T) {
	vars := map[string]any{"p": pair{value.Int(1), value.Int(2)}}

	for _, tc := range []struct{ expr, want string }{
		// tuple_concat hardcodes "tuple" for the receiver and names the
		// operand by its actual class; list_concat does the same, which
		// is where "Pair" surfaces.
		{`p + "s"`, `can only concatenate tuple (not "str") to tuple`},
		{"p + [1]", `can only concatenate tuple (not "list") to tuple`},
		{"[1] + p", `can only concatenate list (not "Pair") to list`},
		{`p|indent(2)`, `can only concatenate tuple (not "str") to tuple`},
		// The repetition message names whichever operand is not the
		// sequence, by its own class.
		{`p * "s"`, `can't multiply sequence by non-int of type 'str'`},
		{`"s" * p`, `can't multiply sequence by non-int of type 'Pair'`},
		{"p * 2.5", `can't multiply sequence by non-int of type 'float'`},
		// No tuple slot applies, so the generic operand error reports
		// the subclass under its own name.
		{"p - 1", `unsupported operand type(s) for -: 'Pair' and 'int'`},
		{"1 + p", `unsupported operand type(s) for +: 'int' and 'Pair'`},
		{`"s" < p`, `'<' not supported between instances of 'str' and 'Pair'`},
		// The right operand's type derives from the left's, so Python
		// runs the reflected comparison: the operator is swapped and the
		// elements meet in the other order.
		{`(1, "a") < p`, `'>' not supported between instances of 'int' and 'str'`},
		// Two placeholders' worth of arguments, one placeholder.
		{`"%s" % p`, "not all arguments converted during string formatting"},
	} {
		_, err := renderVars(t, New(), "{{ "+tc.expr+" }}", vars)
		if err == nil {
			t.Errorf("%s: no error, want %q", tc.expr, tc.want)
			continue
		}
		if !errs.KindOf(err).DerivesFrom(errs.TypeError) {
			t.Errorf("%s: kind %v, want TypeError", tc.expr, errs.KindOf(err))
		}
		if got := err.Error(); !strings.Contains(got, tc.want) {
			t.Errorf("%s = %q, want %q", tc.expr, got, tc.want)
		}
	}
}
