// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/mgilbir/gojja2/value"
)

// A scope holds its first bindings in slots and the rest in a map, and nothing
// outside it can tell which. The boundary is the whole risk: a name written on
// one side and read on the other, or written twice and landing in both.
func TestScopeSlotsAndMapAgree(t *testing.T) {
	// Enough names to overflow whatever inlineVars is set to, several times.
	names := make([]string, inlineVars*3+1)
	for i := range names {
		names[i] = fmt.Sprintf("n%d", i)
	}

	s := newScope(nil)
	for i, name := range names {
		s.set(name, value.Int(int64(i)))
	}
	for i, name := range names {
		v, ok := s.get(name)
		if !ok {
			t.Fatalf("%s was not found after being set", name)
		}
		if got, _ := v.Int64(); got != int64(i) {
			t.Errorf("%s = %d, want %d", name, got, i)
		}
	}

	// Rebinding must replace, on either side of the boundary, and must not
	// leave the name in both places.
	for i, name := range names {
		s.set(name, value.Int(int64(100+i)))
	}
	seen := map[string]int{}
	s.each(func(k string, v value.Value) { seen[k]++ })
	for i, name := range names {
		if seen[name] != 1 {
			t.Errorf("%s appears %d times; a rebinding left it in the "+
				"slots and the map at once", name, seen[name])
		}
		v, _ := s.get(name)
		if got, _ := v.Int64(); got != int64(100+i) {
			t.Errorf("%s = %d after rebinding, want %d", name, got, 100+i)
		}
	}
	if len(seen) != len(names) {
		t.Errorf("each yielded %d names, want %d", len(seen), len(names))
	}
}

// A scope built around a map that already exists -- which is how the
// environment globals are reached -- still binds and reads correctly.
func TestScopeOverExistingMap(t *testing.T) {
	s := &scope{vars: map[string]value.Value{"a": value.Int(1), "b": value.Int(2)}}
	if v, ok := s.get("a"); !ok {
		t.Fatal("a was not found in the map the scope was built around")
	} else if got, _ := v.Int64(); got != 1 {
		t.Errorf("a = %d, want 1", got)
	}
	// Rebinding an existing key must reach the map, not shadow it in a slot
	// and leave two answers behind.
	s.set("a", value.Int(9))
	if v, _ := s.get("a"); func() int64 { n, _ := v.Int64(); return n }() != 9 {
		t.Error("rebinding a name from the map did not take")
	}
	var got []string
	s.each(func(k string, v value.Value) { got = append(got, k) })
	slices.Sort(got)
	if strings.Join(got, ",") != "a,b" {
		t.Errorf("each yielded %v, want [a b]", got)
	}
	// A new name goes to the slots and must still be visible to each.
	s.set("c", value.Int(3))
	got = got[:0]
	s.each(func(k string, v value.Value) { got = append(got, k) })
	slices.Sort(got)
	if strings.Join(got, ",") != "a,b,c" {
		t.Errorf("each yielded %v after a new binding, want [a b c]", got)
	}
}

// The same boundary reached the way a template reaches it.
//
// Each of these binds more names in one frame than a scope holds in line, so
// the frame is half slots and half map, and every one of them reads a name back
// across that split.
func TestFramesBindingPastTheSlots(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		// A `with` binding well past the slot count.
		{`{% with a=1, b=2, c=3, d=4, e=5 %}{{ a }}{{ b }}{{ c }}{{ d }}{{ e }}{% endwith %}`, "12345"},
		// Rebinding across the boundary: the later set must replace.
		{`{% with a=1, b=2, c=3 %}{% set a = 9 %}{% set c = 7 %}{{ a }}{{ b }}{{ c }}{% endwith %}`, "927"},
		// A loop frame that binds past the slots, loop variable included.
		{`{% for i in [1] %}{% set p = 1 %}{% set q = 2 %}{% set r = 3 %}` +
			`{{ i }}{{ p }}{{ q }}{{ r }}{{ loop.index }}{% endfor %}`, "11231"},
		// Unpacking binds several at once.
		{`{% for a, b, c in [[1,2,3],[4,5,6]] %}{{ a }}{{ b }}{{ c }};{% endfor %}`, "123;456;"},
		// A macro with more parameters than slots.
		{`{% macro m(a, b, c, d, e) %}{{ a }}{{ b }}{{ c }}{{ d }}{{ e }}{% endmacro %}{{ m(1,2,3,4,5) }}`, "12345"},
		// Shadowing an outer name from inside a frame that has overflowed.
		{`{% set x = 1 %}{% with a=1, b=2, c=3, x=9 %}{{ x }}{% endwith %}{{ x }}`, "91"},
	} {
		tmpl, err := New().FromString(tc.src)
		if err != nil {
			t.Errorf("%s: compile: %v", tc.src, err)
			continue
		}
		got, err := tmpl.RenderString(context.Background(), nil)
		if err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s\n  = %q\n want %q", tc.src, got, tc.want)
		}
	}
}

// Handing a whole frame somewhere else has to hand over both halves of it.
//
// A scoped block copies its enclosing frame's bindings, and an include with
// context flattens the chain. Either one reading only the map would drop
// whatever was still in the slots -- which is nearly everything.
func TestWholeFramesCrossBoundaries(t *testing.T) {
	env := New(WithLoader(DictLoader(map[string]string{
		"inc":    `{{ a }}{{ b }}{{ c }}{{ d }}`,
		"parent": `{% block b scoped %}{% endblock %}`,
	})))
	for _, tc := range []struct{ src, want string }{
		{`{% with a=1, b=2, c=3, d=4 %}{% include "inc" with context %}{% endwith %}`, "1234"},
		{`{% extends "parent" %}{% block b %}{{ x }}{{ y }}{{ z }}{% endblock %}`, ""},
	} {
		tmpl, err := env.FromString(tc.src)
		if err != nil {
			t.Errorf("%s: compile: %v", tc.src, err)
			continue
		}
		got, err := tmpl.RenderString(context.Background(),
			map[string]any{"x": "", "y": "", "z": ""})
		if err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s\n  = %q\n want %q", tc.src, got, tc.want)
		}
	}
}

// An imported template's exports are read out of its context scope directly,
// so they have to be found wherever that scope put them.
func TestModuleExportsCrossTheBoundary(t *testing.T) {
	env := New(WithLoader(DictLoader(map[string]string{
		"mod": `{% set a = 1 %}{% set b = 2 %}{% set c = 3 %}{% set d = 4 %}`,
	})))
	tmpl, err := env.FromString(`{% import "mod" as m %}{{ m.a }}{{ m.b }}{{ m.c }}{{ m.d }}`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	got, err := tmpl.RenderString(context.Background(), nil)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if want := "1234"; got != want {
		t.Errorf("= %q, want %q", got, want)
	}
}
