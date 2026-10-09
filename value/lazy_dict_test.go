// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package value

import (
	"fmt"
	"math"
	"reflect"
	"slices"
	"testing"
)

// A dict FromGoLazy has not filled yet is a delegation to the one FromGoBudget
// would have built, so every method has to answer as that one does -- the reads
// it answers from the Go map and the ones that fill it first alike. Each
// operation runs on a fresh unfilled dict and a fresh eager one, and both the
// answer and the dict afterwards must match.
func TestAnUnfilledDictAnswersAsAFilledOne(t *testing.T) {
	src := func() map[string]any {
		return map[string]any{
			"a": 1, "b": "x", "n": nil, "nan": math.NaN(),
			"c": []any{1, 2}, "d": map[string]any{"e": 2}, "i8": int8(3),
		}
	}
	py := DefaultPythonVersion
	show := func(v Value, ok bool) string { return fmt.Sprintf("%s/%v", Repr(v), ok) }
	showErr := func(err error) string {
		if err != nil {
			return err.Error()
		}
		return "<nil>"
	}
	ops := map[string]func(d *Dict) string{
		"Len":     func(d *Dict) string { return fmt.Sprint(d.Len()) },
		"Entries": func(d *Dict) string { return fmt.Sprint(len(d.Entries())) },
		"Keys":    func(d *Dict) string { return Repr(NewList(d.Keys()...)) },
		"Values":  func(d *Dict) string { return Repr(NewList(d.Values()...)) },
		"Get": func(d *Dict) string {
			out := ""
			for _, k := range []Value{String("a"), String("b"), String("n"), String("c"),
				String("d"), String("i8"), String("zz"), Safe("a"), Int(1), None} {
				v, ok, err := d.Get(k, py)
				out += show(v, ok) + showErr(err) + ";"
			}
			_, _, err := d.Get(NewList(), py)
			return out + showErr(err)
		},
		"GetString": func(d *Dict) string {
			out := ""
			for _, k := range []string{"a", "b", "n", "c", "d", "i8", "zz", ""} {
				out += show(d.GetString(k)) + ";"
			}
			return out
		},
		"GetKnown": func(d *Dict) string {
			return show(d.GetKnown(String("b"))) + show(d.GetKnown(Int(1)))
		},
		"SetKnown": func(d *Dict) string { d.SetKnown(String("a"), Int(9)); return "" },
		"Set": func(d *Dict) string {
			return showErr(d.Set(String("z"), Int(1), py)) +
				showErr(d.Set(Int(4), String("four"), py)) +
				showErr(d.Set(NewList(), None, py))
		},
		"Reserve":     func(d *Dict) string { d.Reserve(20); return fmt.Sprint(d.Len()) },
		"SetString":   func(d *Dict) string { d.SetString("b", String("y")); return "" },
		"DeleteKnown": func(d *Dict) string { return fmt.Sprint(d.DeleteKnown(String("c"))) },
		"Delete": func(d *Dict) string {
			ok, err := d.Delete(String("a"), py)
			miss, err2 := d.Delete(String("zz"), py)
			return fmt.Sprint(ok, miss) + showErr(err) + showErr(err2)
		},
		"Clone": func(d *Dict) string { return Repr(d.Clone()) },
	}

	// Every exported method has to be in the sweep, so a method added
	// later is a decision about laziness rather than a hole in it.
	typ := reflect.TypeFor[*Dict]()
	for i := range typ.NumMethod() {
		if name := typ.Method(i).Name; ops[name] == nil {
			t.Errorf("Dict.%s is not swept: add it here, and to load's "+
				"callers in dict.go if it reads the entries", name)
		}
	}

	names := make([]string, 0, len(ops))
	for name := range ops {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		op := ops[name]
		lazyV, err := FromGoLazy(src(), nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		eagerV, err := FromGoBudget(src(), nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		lazy, _ := lazyV.Dict()
		eager, _ := eagerV.Dict()
		if lazy.pending == nil {
			t.Fatalf("FromGoLazy filled the dict before anything read it")
		}
		gotL, gotE := op(lazy), op(eager)
		if gotL != gotE {
			t.Errorf("%s: unfilled dict answered %q, filled one %q", name, gotL, gotE)
		}
		// The NaN's repr is the same either way; what the dicts hold
		// afterwards has to be too.
		if rl, re := Repr(lazyV), Repr(eagerV); rl != re {
			t.Errorf("%s: afterwards the unfilled dict is %s, the filled one %s", name, rl, re)
		}
	}
}

// The reads a loop over records makes are the ones answered without filling
// the dict, which is the point of it -- and a NaN or a container is not one of
// them, because each must be the same object every time it is read.
func TestScalarReadsLeaveTheDictUnfilled(t *testing.T) {
	src := map[string]any{"a": 1, "s": "x", "nan": math.NaN(), "c": []any{1}}
	cases := []struct {
		name   string
		read   func(d *Dict)
		filled bool
	}{
		{"Len", func(d *Dict) { d.Len() }, false},
		{"GetString of an int", func(d *Dict) { d.GetString("a") }, false},
		{"GetString of a str", func(d *Dict) { d.GetString("s") }, false},
		{"GetString of a missing key", func(d *Dict) { d.GetString("zz") }, false},
		{"Get by str", func(d *Dict) { _, _, _ = d.Get(String("a"), DefaultPythonVersion) }, false},
		{"GetString of a NaN", func(d *Dict) { d.GetString("nan") }, true},
		{"GetString of a list", func(d *Dict) { d.GetString("c") }, true},
		{"Get by int", func(d *Dict) { _, _, _ = d.Get(Int(1), DefaultPythonVersion) }, true},
	}
	for _, tc := range cases {
		v, _ := FromGoLazy(src, nil, nil)
		d, _ := v.Dict()
		tc.read(d)
		if filled := d.pending == nil; filled != tc.filled {
			t.Errorf("%s: filled = %v, want %v", tc.name, filled, tc.filled)
		}
	}
}

// Comparing and printing reach the entries without going through a Dict
// method of their own, so they are checked separately.
func TestAnUnfilledDictComparesAndPrints(t *testing.T) {
	src := map[string]any{"a": 1, "c": []any{map[string]any{"x": "y"}}}
	other := map[string]any{"a": 2, "c": []any{map[string]any{"x": "y"}}}
	py := DefaultPythonVersion
	for _, tc := range []struct {
		name       string
		a, b       func() Value
		wantEquals bool
	}{
		{"unfilled == filled", lazyOf(src), eagerOf(src), true},
		{"filled == unfilled", eagerOf(src), lazyOf(src), true},
		{"unfilled == unfilled", lazyOf(src), lazyOf(src), true},
		{"unfilled != filled", lazyOf(src), eagerOf(other), false},
		{"filled != unfilled", eagerOf(other), lazyOf(src), false},
		{"unfilled != unfilled", lazyOf(other), lazyOf(src), false},
	} {
		if eq, err := EqualBoolErr(tc.a(), tc.b(), py); err != nil || eq != tc.wantEquals {
			t.Errorf("%s: %v, %v; want %v", tc.name, eq, err, tc.wantEquals)
		}
	}
	c, _ := FromGoLazy(src, nil, nil)
	if got, want := Repr(c), Repr(FromGo(src)); got != want {
		t.Errorf("repr of an unfilled dict = %s, want %s", got, want)
	}
}

func lazyOf(m map[string]any) func() Value {
	return func() Value {
		v, err := FromGoLazy(m, nil, nil)
		if err != nil {
			panic(err)
		}
		return v
	}
}

func eagerOf(m map[string]any) func() Value {
	return func() Value { return FromGo(m) }
}
