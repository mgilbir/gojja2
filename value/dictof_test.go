// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package value_test

import (
	"errors"
	"testing"

	"github.com/mgilbir/gojja2/errs"
	"github.com/mgilbir/gojja2/value"
)

// DictOf reports what it cannot build rather than panicking. A caller building
// a dict from data it did not write cannot know in advance that the argument
// count is even or that every key is hashable.
func TestDictOfReportsWhatItCannotBuild(t *testing.T) {
	for _, tc := range []struct {
		name string
		kv   []value.Value
		want string
	}{
		{"odd count", []value.Value{value.String("a")}, "DictOf needs an even number of arguments, got 1"},
		{"odd count, three", []value.Value{
			value.String("a"), value.Int(1), value.String("b")}, "DictOf needs an even number of arguments, got 3"},
		// Worded for the pinned interpreter: before 3.14 the refusal named
		// only the unhashable type, and DictOf takes the version for
		// exactly that reason.
		{"list key", []value.Value{
			value.NewList(value.Int(1)), value.Int(1)}, unhashableWant("list", "list", value.AsDictKey)},
		{"dict key", []value.Value{value.NewDict(), value.Int(1)}, unhashableWant("dict", "dict", value.AsDictKey)},
	} {
		got, err := value.DictOf(value.DefaultPythonVersion, tc.kv...)
		if err == nil {
			t.Errorf("%s: DictOf succeeded, want an error", tc.name)
			continue
		}
		if err.Error() != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, err.Error(), tc.want)
		}
		if !got.IsUndefined() {
			t.Errorf("%s: returned a value alongside its error", tc.name)
		}
		if !errors.Is(err, errs.TypeError) {
			t.Errorf("%s: %v is not a TypeError", tc.name, err)
		}
	}
}

func TestDictOfBuildsWhatItCan(t *testing.T) {
	d, err := value.DictOf(value.DefaultPythonVersion, value.String("b"), value.Int(2), value.String("a"), value.Int(1))
	if err != nil {
		t.Fatalf("DictOf: %v", err)
	}
	// Insertion order, as a Python dict keeps.
	if got := value.Repr(d); got != "{'b': 2, 'a': 1}" {
		t.Errorf("got %s, want {'b': 2, 'a': 1}", got)
	}
	empty, err := value.DictOf(value.DefaultPythonVersion)
	if err != nil {
		t.Fatalf("DictOf(): %v", err)
	}
	if got := value.Repr(empty); got != "{}" {
		t.Errorf("got %s, want {}", got)
	}
}

// unhashableWant is the refusal DictOf gives for a key that cannot be hashed,
// as the pinned interpreter words it. Built from the production helper rather
// than written out, so a bump to DefaultPythonVersion does not leave this
// asserting some other interpreter's sentence.
func unhashableWant(outer, inner string, use value.HashUse) string {
	var v value.Value
	switch inner {
	case "list":
		v = value.NewList(value.Int(1))
	case "dict":
		v = value.NewDict()
	default:
		panic("unhashableWant: no sample value for " + inner)
	}
	return value.ErrUnhashable(outer, v, value.DefaultPythonVersion, use).Error()
}
