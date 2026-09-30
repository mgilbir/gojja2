// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package value_test

import (
	"errors"
	"math"
	"math/big"
	"strings"
	"testing"

	"github.com/mgilbir/gojja2/value"
)

// FromGo is the bridge every render argument crosses, and the cases below are
// the Go types a host can hand it that no template can construct: every integer
// width, named types the fast paths do not name, pointers, and containers that
// reach themselves through a type the fast path does not know. None of it is a
// CPython question, so the corpus cannot hold it.

type (
	flag  bool
	count int32
	size  uint16
	ratio float32
	label string
	blob  []byte
	ring  []any
	table map[string]any
)

func TestFromGoConvertsEveryScalarWidth(t *testing.T) {
	five := 5
	for _, tc := range []struct {
		name string
		in   any
		want string
	}{
		{"int8", int8(-8), "-8"},
		{"int16", int16(-16), "-16"},
		{"int32", int32(-32), "-32"},
		{"uint", uint(1), "1"},
		{"uint8", uint8(8), "8"},
		{"uint16", uint16(16), "16"},
		{"uint32", uint32(32), "32"},
		{"uint64 past int64", uint64(math.MaxUint64), "18446744073709551615"},
		{"uintptr", uintptr(7), "7"},
		{"float32", float32(1.5), "1.5"},
		{"big.Int", new(big.Int).Lsh(big.NewInt(1), 70), "1180591620717411303424"},
		{"nil big.Int", (*big.Int)(nil), "None"},
		{"named bool", flag(true), "True"},
		{"named int", count(-3), "-3"},
		{"named uint", size(9), "9"},
		{"named float", ratio(0.5), "0.5"},
		{"named string", label("x"), "'x'"},
		{"named bytes", blob("ab"), "b'ab'"},
		{"pointer to int", &five, "5"},
		{"nil pointer to int", (*int)(nil), "None"},
		{"nil func", (func())(nil), "None"},
		{"typed slice", []string{"a", "b"}, "['a', 'b']"},
		{"array", [3]int{1, 2, 3}, "[1, 2, 3]"},
		{"typed map", map[int]string{2: "b", 10: "a", 1: "c"}, "{1: 'c', 2: 'b', 10: 'a'}"},
		{"map keyed by any, mixed", map[any]int{"a": 1, 2: 2}, "{2: 2, 'a': 1}"},
		{"empty named slice", ring{}, "[]"},
	} {
		if got := value.Repr(value.FromGo(tc.in)); got != tc.want {
			t.Errorf("%s: FromGo = %s, want %s", tc.name, got, tc.want)
		}
	}
}

// Two keys that print the same are ordered by nothing but the sort, and both
// have to survive it.
func TestFromGoKeepsKeysThatRenderAlike(t *testing.T) {
	v := value.FromGo(map[any]string{1: "int", "1": "str"})
	d, ok := v.Dict()
	if !ok || d.Len() != 2 {
		t.Fatalf("FromGo = %s, want a dict of both keys", value.Repr(v))
	}
}

func TestFromGoSharesACycleThroughAnyContainerType(t *testing.T) {
	r := ring{nil}
	r[0] = r
	if got := value.Repr(value.FromGo(r)); got != "[[...]]" {
		t.Errorf("named slice cycle = %s, want [[...]]", got)
	}
	tbl := table{}
	tbl["self"] = tbl
	if got := value.Repr(value.FromGo(tbl)); got != "{'self': {...}}" {
		t.Errorf("named map cycle = %s, want {'self': {...}}", got)
	}
	sm := map[string]any{}
	sm["self"] = sm
	if got := value.Repr(value.FromGo(sm)); got != "{'self': {...}}" {
		t.Errorf("map[string]any cycle = %s, want {'self': {...}}", got)
	}
	l := make([]any, 1)
	l[0] = l
	if got := value.Repr(value.FromGo(l)); got != "[[...]]" {
		t.Errorf("[]any cycle = %s, want [[...]]", got)
	}
	// And a container met twice without a cycle is the same value both times.
	shared := ring{1}
	pair := value.FromGo([]any{shared, shared})
	if got := value.Repr(pair); got != "[[1], [1]]" {
		t.Errorf("shared = %s, want [[1], [1]]", got)
	}
}

// A refusal stops the walk, and what is still to be visited is not built: the
// sibling that follows a refused container is answered empty without a charge.
func TestFromGoStopsOnceRefused(t *testing.T) {
	for name, arg := range map[string]any{
		"[]any":       []any{[]any{1, 2, 3}, []any{4}, []any{}},
		"named slice": ring{ring{1, 2, 3}, ring{4}, ring{}},
		"named map":   table{"a": table{"x": 1}, "b": table{"y": 2}, "c": table{}},
		"typed map":   map[string][]int{"a": {1, 2}, "b": {3}, "c": {}},
		"typed slice": [][]int{{1, 2}, {3}, {}},
	} {
		t.Run(name, func(t *testing.T) {
			b := &countingBudget{stopAt: 2}
			if _, err := value.FromGoBudget(arg, nil, b); !errors.Is(err, errStop) {
				t.Errorf("convert = %v, want the budget's refusal", err)
			}
			b = &countingBudget{}
			if _, err := value.FromGoBudget(arg, nil, refuseAfter{b, 2}); !errors.Is(err, errStop) {
				t.Errorf("convert with a refusing charge = %v, want the budget's refusal", err)
			}
		})
	}
}

// refuseAfter lets n item charges through and refuses every one after.
type refuseAfter struct {
	*countingBudget
	n int
}

func (r refuseAfter) ChargeItems(n int64) error {
	if r.items += n; r.items > int64(r.n) {
		return errStop
	}
	return nil
}

func TestToGoConvertsBack(t *testing.T) {
	wide := new(big.Int).Lsh(big.NewInt(1), 70)
	for _, tc := range []struct {
		name string
		in   value.Value
		want any
	}{
		{"none", value.None, nil},
		{"bytes", value.Bytes([]byte("ab")), []byte("ab")},
		{"int past int64", value.BigInt(wide), wide},
		{"float", value.Float(1.5), 1.5},
	} {
		got := value.ToGo(tc.in)
		switch want := tc.want.(type) {
		case *big.Int:
			if g, ok := got.(*big.Int); !ok || g.Cmp(want) != 0 {
				t.Errorf("%s: ToGo = %v, want %v", tc.name, got, want)
			}
		case []byte:
			if g, ok := got.([]byte); !ok || string(g) != string(want) {
				t.Errorf("%s: ToGo = %v, want %v", tc.name, got, want)
			}
		default:
			if got != tc.want {
				t.Errorf("%s: ToGo = %v, want %v", tc.name, got, tc.want)
			}
		}
	}
	// A value that reaches itself is cut off rather than followed until the
	// stack gives out.
	l := value.NewList()
	seq, _ := l.Seq()
	seq.Append(l)
	depth := 0
	for cur := value.ToGo(l); ; depth++ {
		next, ok := cur.([]any)
		if !ok || len(next) == 0 {
			break
		}
		cur = next[0]
	}
	if depth < 10 || depth > 200 {
		t.Errorf("a self-referential list came back %d levels deep, want a bounded walk", depth)
	}
}

// A key the dict cannot hash is the caller's mistake, and Known says so with a
// panic rather than a silent collision.
func TestKnownLookupsPanicOnUnhashableKeys(t *testing.T) {
	d, _ := value.NewDict().Dict()
	for name, f := range map[string]func(){
		"GetKnown": func() { d.GetKnown(value.NewList()) },
		"SetKnown": func() { d.SetKnown(value.NewList(), value.None) },
	} {
		func() {
			defer func() {
				r := recover()
				msg, _ := r.(string)
				if !strings.Contains(msg, "unhashable") {
					t.Errorf("%s panicked with %v, want a message naming the unhashable key", name, r)
				}
			}()
			f()
		}()
	}
}

// bare is an Object that says nothing about how it prints.
type bare struct{}

func (bare) GetAttr(string) (value.Value, bool) { return value.Undefined, false }

// An object that offers no repr of its own is named generically, and a
// container holding one is still a container.
func TestAnObjectWithNoReprIsNamedGenerically(t *testing.T) {
	if got := value.Repr(value.FromObject(bare{})); got != "<object>" {
		t.Errorf("Repr = %s, want <object>", got)
	}
	if got := value.Repr(value.NewList(value.FromObject(bare{}))); got != "[<object>]" {
		t.Errorf("Repr of a list holding one = %s, want [<object>]", got)
	}
}
