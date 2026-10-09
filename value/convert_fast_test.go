// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package value_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/mgilbir/gojja2/value"
)

// The eager walk sorts every map[string]any's keys in one scratch slice that a
// nested map extends and gives back. A nested map that gave back too much
// would let its sibling overwrite the keys its parent is still reading, so
// this nests three levels of maps too big for the stack array and checks
// every key comes out where it belongs.
func TestNestedMapsShareTheKeyScratch(t *testing.T) {
	const width = 6
	var build func(depth int, path string) map[string]any
	build = func(depth int, path string) map[string]any {
		m := make(map[string]any, width)
		for i := range width {
			k := fmt.Sprintf("%s%c", path, 'a'+i)
			if depth == 0 {
				m[k] = k
			} else {
				m[k] = build(depth-1, k)
			}
		}
		return m
	}
	var repr func(depth int, path string) string
	repr = func(depth int, path string) string {
		parts := make([]string, width)
		for i := range width {
			k := fmt.Sprintf("%s%c", path, 'a'+i)
			v := "'" + k + "'"
			if depth > 0 {
				v = repr(depth-1, k)
			}
			parts[i] = "'" + k + "': " + v
		}
		return "{" + strings.Join(parts, ", ") + "}"
	}
	if got, want := value.Repr(value.FromGo(build(2, ""))), repr(2, ""); got != want {
		t.Errorf("FromGo =\n  %s\nwant\n  %s", got, want)
	}
}

// A []any is identified by its data pointer and length without being boxed,
// and that has to keep meaning what identify means: the same slice twice is
// one list, and two slices that merely look alike -- or share a backing array
// at different lengths -- are two.
func TestAnySliceIdentity(t *testing.T) {
	same := []any{1}
	backing := []any{1, 2, 3}
	v := value.FromGo([]any{same, same, []any{1}, backing[:1], backing[:2], backing[:1]})
	s, _ := v.Seq()
	at := func(i int) value.Value { return s.At(i) }
	for _, tc := range []struct {
		name string
		i, j int
		same bool
	}{
		{"one slice twice", 0, 1, true},
		{"an equal slice of its own", 0, 2, false},
		{"one array at two lengths", 3, 4, false},
		{"one array at one length twice", 3, 5, true},
	} {
		if got := value.SameObject(at(tc.i), at(tc.j)); got != tc.same {
			t.Errorf("%s: same object = %v, want %v", tc.name, got, tc.same)
		}
	}
}

// objLabel is a named string that is an Object, which the typed-element fast
// path must not mistake for a plain string.
type objLabel string

func (o objLabel) GetAttr(name string) (value.Value, bool) {
	if name == "upper" {
		return value.String(strings.ToUpper(string(o))), true
	}
	return value.Undefined, false
}

// Elements of a basic kind are converted by their kind without being boxed,
// which is only right when the type has no methods: a named scalar that is an
// Object has to arrive as one, in a slice and as a map key or value alike.
func TestTypedScalarElementsKeepTheirObjects(t *testing.T) {
	list := value.FromGo([]objLabel{"a", "b"})
	s, _ := list.Seq()
	if got := s.At(0).Kind(); got != value.KindObject {
		t.Errorf("[]objLabel element kind = %v, want an object", got)
	}
	m := value.FromGo(map[objLabel]objLabel{"k": "v"})
	d, _ := m.Dict()
	for _, e := range d.Entries() {
		if e.Key.Kind() != value.KindObject || e.Value.Kind() != value.KindObject {
			t.Errorf("map[objLabel]objLabel entry = %v: %v, want objects",
				e.Key.Kind(), e.Value.Kind())
		}
	}
	// And the plain kinds convert as fromAny would convert them, keys
	// sorted numerically where they are numbers.
	got := value.Repr(value.FromGo(map[int]float32{10: 1.5, 9: -2, 100: 0}))
	if want := "{9: -2.0, 10: 1.5, 100: 0.0}"; got != want {
		t.Errorf("map[int]float32 = %s, want %s", got, want)
	}
	got = value.Repr(value.FromGo([]uint8x{1, 2}))
	if want := "[1, 2]"; got != want {
		t.Errorf("[]uint8x = %s, want %s", got, want)
	}
	strs := []string{"b", "a"}
	if got := value.Repr(value.FromGo(strs)); got != "['b', 'a']" {
		t.Errorf("[]string = %s", got)
	}
	if !slices.Equal(strs, []string{"b", "a"}) {
		t.Errorf("converting a []string changed it: %v", strs)
	}
}

// uint8x is a named integer with no methods, which takes the fast path.
type uint8x uint16
