// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package value_test

import (
	"strings"
	"testing"

	"github.com/mgilbir/gojja2/value"
)

// The exported helpers below had no test at all: coverage over the whole suite
// put each of them at 0%, which for a package a host program calls means a
// caller could be the first to find out. None of them is reachable from a
// template, so no corpus case can cover them and the differential never will.
//
// Each test asserts the documented contract rather than the implementation,
// because the contract is what a caller reads.

// TestStringDictKeepsTheGivenOrder: StringDict documents "in the order given by
// keys", which is the whole reason it exists rather than taking a Go map.
func TestStringDictKeepsTheGivenOrder(t *testing.T) {
	keys := []string{"b", "a", "C"}
	v := value.StringDict(keys, []value.Value{
		value.Int(2), value.Int(1), value.Int(3),
	})
	d, ok := v.Dict()
	if !ok {
		t.Fatal("StringDict did not build a dict")
	}
	if got := d.Keys(); len(got) != 3 ||
		value.Str(got[0]) != "b" || value.Str(got[1]) != "a" || value.Str(got[2]) != "C" {
		t.Errorf("Keys() = %v, want b, a, C in that order", got)
	}
	vals := d.Values()
	if len(vals) != 3 {
		t.Fatalf("Values() has %d entries, want 3", len(vals))
	}
	for i, want := range []int64{2, 1, 3} {
		got, _ := vals[i].Int64()
		if got != want {
			t.Errorf("Values()[%d] = %d, want %d", i, got, want)
		}
	}
}

// TestDeleteKnownRemovesAndReports: DeleteKnown is Delete for a key that cannot
// fail to hash, and its guard against one that can is a panic -- so both halves
// are asserted, the second with a recover, because a guard never seen to fire is
// not a guard.
func TestDeleteKnownRemovesAndReports(t *testing.T) {
	v := value.StringDict([]string{"a", "b"}, []value.Value{value.Int(1), value.Int(2)})
	d, _ := v.Dict()

	if !d.DeleteKnown(value.String("a")) {
		t.Error("DeleteKnown reported false for a key that was there")
	}
	if d.Len() != 1 {
		t.Errorf("Len() = %d after one delete, want 1", d.Len())
	}
	if d.DeleteKnown(value.String("a")) {
		t.Error("DeleteKnown reported true for a key already removed")
	}

	defer func() {
		r := recover()
		if r == nil {
			t.Error("DeleteKnown did not panic on an unhashable key; the guard " +
				"that documents it is decorative")
			return
		}
		if msg, ok := r.(string); !ok || !strings.Contains(msg, "unhashable") {
			t.Errorf("panicked with %v, want a message naming the unhashable key", r)
		}
	}()
	d.DeleteKnown(value.NewList(value.Int(1)))
}

// TestEscapeLeavesMarkupAlone: Escape is markupsafe.escape, which is idempotent
// -- escaping twice is what turns "&amp;" into "&amp;amp;" in a real template.
func TestEscapeLeavesMarkupAlone(t *testing.T) {
	escaped := value.Escape(value.String(`<a href="x">&</a>`))
	if !escaped.IsSafe() {
		t.Error("Escape did not return Markup")
	}
	const want = "&lt;a href=&#34;x&#34;&gt;&amp;&lt;/a&gt;"
	if got := value.Str(escaped); got != want {
		t.Errorf("Escape = %q, want %q", got, want)
	}
	if again := value.Escape(escaped); value.Str(again) != want {
		t.Errorf("Escape of Markup = %q, want it unchanged", value.Str(again))
	}
	// EscapeHTML is the text-only half, and has to agree with it.
	if got := value.EscapeHTML(`<a href="x">&</a>`); got != want {
		t.Errorf("EscapeHTML = %q, want %q", got, want)
	}
}

// TestEmptyListIsFreshEachCall: a list is mutable, so sharing one between two
// template variables would let an append through the first show in the second.
func TestEmptyListIsFreshEachCall(t *testing.T) {
	a, b := value.EmptyList(), value.EmptyList()
	sa, ok := a.Seq()
	if !ok {
		t.Fatal("EmptyList did not build a sequence")
	}
	sa.Append(value.Int(1))
	sb, _ := b.Seq()
	if sb.Len() != 0 {
		t.Errorf("appending to one EmptyList left the other with %d elements", sb.Len())
	}
	if sa.Len() != 1 {
		t.Errorf("the appended-to list has %d elements, want 1", sa.Len())
	}
}

// TestSeqSetReplaces: Set is the only way to write one element, which |sort and
// the list mutators use.
func TestSeqSetReplaces(t *testing.T) {
	v := value.NewList(value.Int(1), value.Int(2))
	s, _ := v.Seq()
	s.Set(1, value.String("x"))
	if got := value.Str(s.At(1)); got != "x" {
		t.Errorf("At(1) = %q after Set, want %q", got, "x")
	}
	if n, _ := s.At(0).Int64(); n != 1 {
		t.Errorf("Set touched element 0, which now holds %v", s.At(0))
	}
}

// TestAsTupleAndAsListShareNoStorage: both document "sharing no storage", which
// is what stops a tuple built from a list changing when the list does.
func TestAsTupleAndAsListShareNoStorage(t *testing.T) {
	list := value.NewList(value.Int(1), value.Int(2))
	tuple := list.AsTuple()
	copied := list.AsList()

	s, _ := list.Seq()
	s.Set(0, value.String("changed"))

	ts, _ := tuple.Seq()
	if n, _ := ts.At(0).Int64(); n != 1 {
		t.Errorf("AsTuple shares storage: element 0 became %v", ts.At(0))
	}
	cs, _ := copied.Seq()
	if n, _ := cs.At(0).Int64(); n != 1 {
		t.Errorf("AsList shares storage: element 0 became %v", cs.At(0))
	}
	if tuple.Kind() != value.KindTuple {
		t.Errorf("AsTuple gave kind %v", tuple.Kind())
	}
	if copied.Kind() != value.KindList {
		t.Errorf("AsList gave kind %v", copied.Kind())
	}
	// A tuple is already one, so AsTuple may return it as it is -- and a
	// value that is no sequence at all comes back unchanged.
	if got := value.Int(7).AsTuple(); got.Kind() != value.KindInt {
		t.Errorf("AsTuple of an int gave kind %v, want it unchanged", got.Kind())
	}
	if got := value.Int(7).AsList(); got.Kind() != value.KindInt {
		t.Errorf("AsList of an int gave kind %v, want it unchanged", got.Kind())
	}
}

// TestIsSequence: list, tuple, str and bytes are ordered sequences; a dict, a
// number and None are not. bytes is in the list because Python's is a sequence
// of integers, which is also why `97 in b"ab"` is True.
func TestIsSequence(t *testing.T) {
	for _, tc := range []struct {
		name string
		v    value.Value
		want bool
	}{
		{"list", value.NewList(value.Int(1)), true},
		{"tuple", value.NewTuple(value.Int(1)), true},
		{"str", value.String("ab"), true},
		{"bytes", value.Bytes([]byte("ab")), true},
		{"dict", value.NewDict(), false},
		{"int", value.Int(1), false},
		{"float", value.Float(1.5), false},
		{"bool", value.Bool(true), false},
		{"none", value.None, false},
		{"undefined", value.Undefined, false},
	} {
		if got := tc.v.IsSequence(); got != tc.want {
			t.Errorf("IsSequence(%s) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// object is the value.Object a wrapped Go value presents, which is how a host
// program reaches an attribute: Value has no GetAttr of its own.
func object(t *testing.T, v value.Value) value.Object {
	t.Helper()
	o, ok := v.Interface().(value.Object)
	if !ok {
		t.Fatalf("%v does not present a value.Object", v)
	}
	return o
}

// exposed is a Go value with one nullary method and one that takes an argument,
// which is the distinction MethodPolicy draws.
type exposed struct{ N int }

func (e exposed) Double() int    { return e.N * 2 }
func (e exposed) Plus(k int) int { return e.N + k }
func (e exposed) String() string { return "exposed" }

// TestFromGoWithPolicy: FromGoWith is FromGo under an explicit policy, and the
// policy is what decides whether a template can choose a host method's
// arguments. NullaryMethods is the default and AllMethods the widening.
func TestFromGoWithPolicy(t *testing.T) {
	for _, tc := range []struct {
		name     string
		policy   value.MethodPolicy
		wantPlus bool
	}{
		{"nullary", value.NullaryMethods, false},
		{"all", value.AllMethods, true},
	} {
		o := object(t, value.FromGoWith(exposed{N: 21}, tc.policy))
		if _, ok := o.GetAttr("Double"); !ok {
			t.Errorf("%s: Double is not reachable; every policy exposes a nullary method", tc.name)
		}
		if _, ok := o.GetAttr("Plus"); ok != tc.wantPlus {
			t.Errorf("%s: Plus reachable = %v, want %v", tc.name, ok, tc.wantPlus)
		}
		// No case for an unexported method: reflect cannot see one at
		// all -- Type.NumMethod counts only the exported ones -- so a
		// policy has nothing to decide about it and asserting it would
		// be asserting the Go runtime.
		if _, ok := o.GetAttr("nosuch"); ok {
			t.Errorf("%s: a name the type does not have is reachable", tc.name)
		}
	}
	// A method value answers no attributes of its own, which is what stops
	// `{{ obj.Double.nope }}` reaching into reflect.
	m, ok := object(t, value.FromGoWith(exposed{N: 1}, value.NullaryMethods)).GetAttr("Double")
	if !ok {
		t.Fatal("Double is not reachable")
	}
	if _, ok := object(t, m).GetAttr("anything"); ok {
		t.Error("a method value answered an attribute")
	}
}

// TestFromGoOpaque: a Go value this package has no structure for is wrapped
// rather than dropped, so passing one into a template is visible instead of
// silently empty -- and it answers no attributes.
func TestFromGoOpaque(t *testing.T) {
	ch := make(chan int)
	v := value.FromGo(ch)
	if v.IsUndefined() {
		t.Fatal("FromGo dropped a channel to undefined")
	}
	if got := v.TypeName(); got != "chan int" {
		t.Errorf("TypeName() = %q, want %q", got, "chan int")
	}
	if got := value.Repr(v); got != "<chan int>" {
		t.Errorf("Repr() = %q, want %q", got, "<chan int>")
	}
	if _, ok := object(t, v).GetAttr("anything"); ok {
		t.Error("an opaque value answered an attribute")
	}
}
