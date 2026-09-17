// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"testing"

	"github.com/mgilbir/gojja2/value"
)

// TestFoldableMatchesHasSafeRepr pins which constants may stand in for the
// expression that produced them.
//
// jinja2's has_safe_repr looks inside a container -- a list is foldable only
// when every element is, a dict only when every key and value are -- and the
// types it accepts are exact. A tuple subclass is not a tuple for this
// purpose, which is what makes a list of |groupby pairs unfoldable.
func TestFoldableMatchesHasSafeRepr(t *testing.T) {
	group := value.FromObject(&groupObject{key: value.String("a"), items: value.NewList()})
	for _, tc := range []struct {
		name string
		v    value.Value
		want bool
	}{
		{"none", value.None, true},
		{"int", value.Int(1), true},
		{"float", value.Float(1.5), true},
		{"string", value.String("a"), true},
		{"markup", value.Safe("a"), true},
		{"empty list", value.NewList(), true},
		{"nested literals", value.NewList(value.NewTuple(value.Int(1), value.String("x"))), true},
		{"range", value.FromObject(&rangeObject{stop: 3, step: 1}), true},
		// Not literals Python could write back.
		{"bytes", value.Bytes([]byte("x")), false},
		{"undefined", value.Undefined, false},
		{"a group tuple", group, false},
		// ... and a container is judged by what it holds.
		{"list of group tuples", value.NewList(group), false},
		{"dict with a group tuple value", dictOf(t, value.String("k"), group), false},
		{"list holding a list holding one", value.NewList(value.NewList(group)), false},
	} {
		if got := foldable(tc.v); got != tc.want {
			t.Errorf("foldable(%s) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func dictOf(t *testing.T, k, v value.Value) value.Value {
	t.Helper()
	d := value.NewDict()
	dict, _ := d.Dict()
	if err := dict.Set(k, v); err != nil {
		t.Fatalf("set: %v", err)
	}
	return d
}

// TestFoldingDoesNotSwallowAnError is the behaviour that rule protects: an
// unfoldable branch means the condition beside it runs, and running it raises.
func TestFoldingDoesNotSwallowAnError(t *testing.T) {
	src := `{% with w = blank if (-1)[-2:] else d|batch(2)|list|groupby('city')|list %}{% endwith %}`
	_, err := renderVars(t, New(), src, map[string]any{"blank": "", "d": map[string]any{"1": "a"}})
	if err == nil {
		t.Fatal("rendered; want the TypeError CPython raises for a slice of an int")
	}
	if got, want := err.Error(), "'int' object is not subscriptable"; got != want {
		t.Errorf("error = %q, want %q", got, want)
	}
}
