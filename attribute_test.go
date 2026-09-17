// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import "testing"

// TestAttributeIsAnItemLookup pins what a filter's `attribute=` means.
//
// jinja2 resolves every part of it through Environment.getitem, which reaches
// the item first and falls back to the attribute only for a string key. So a
// dict's own "items" wins over its method -- gojja2 reached the method, which
// is a wrong answer for a perfectly ordinary template over JSON data.
//
// The specification is not always a string either: None is no lookup at all,
// and anything else is a single key used as it stands, which is why `false`
// reads element 0 and a float reports "no element 2.5" rather than looking for
// an attribute spelled "2.5".
//
// Expectations from CPython jinja2 3.1.6.
func TestAttributeIsAnItemLookup(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		// The item, not the method of the same name.
		{`{{ [{"items": 1}]|map(attribute="items")|list }}`, "[1]"},
		{`{{ [{"items": 1}]|join(attribute="items") }}`, "1"},
		{`{{ [{"items": 1}]|sum(attribute="items") }}`, "1"},
		{`{{ [{"items": 1}]|groupby("items")|list }}`, "[(1, [{'items': 1}])]"},
		{`{{ [{"items": 1}, {"items": 0}]|sort(attribute="items")|list }}`,
			"[{'items': 0}, {'items': 1}]"},
		// None is no lookup: the item is its own key.
		{`{{ "ab cd"|groupby(attribute=none) }}`,
			"[(' ', [' ']), ('a', ['a']), ('b', ['b']), ('c', ['c']), ('d', ['d'])]"},
		// false is 0, and indexes.
		{`{{ "ab cd"|max(attribute=false) }}`, "d"},
		{`{{ "ab cd"|join(attribute=false) }}`, "ab cd"},
		// A dotted string still walks, and a run of digits still
		// indexes -- by str.isdigit, so a sign or a space is a name.
		{`{{ [{"a": {"b": 2}}]|map(attribute="a.b")|list }}`, "[2]"},
		{`{{ ["ab"]|map(attribute="1")|list }}`, "['b']"},
		{`{{ ["ab"]|map(attribute="١")|list }}`, "['b']"},
		{`{{ [[1,2]]|map(attribute="-1")|list }}`, "[Undefined]"},
		{`{{ ["ab"]|map(attribute=" 1")|list }}`, "[Undefined]"},
	} {
		got, err := renderVars(t, New(), tc.src, nil)
		if err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s\n  = %q\n want %q", tc.src, got, tc.want)
		}
	}
	// A key that is not a string reports the key, not a name made from it.
	for _, tc := range []struct{ src, want string }{
		{`{{ "ab cd"|max(attribute=2.5) }}`, "str object has no element 2.5"},
		{`{{ "ab cd"|max(attribute=true) }}`, "str object has no element True"},
		{`{{ "ab cd"|max(attribute=[1]) }}`, "str object has no element [1]"},
	} {
		_, err := renderVars(t, New(), tc.src, nil)
		if err == nil {
			t.Errorf("%s: rendered; want %q", tc.src, tc.want)
			continue
		}
		if got := err.Error(); got != tc.want {
			t.Errorf("%s\n  = %q\n want %q", tc.src, got, tc.want)
		}
	}
}

// TestAttrNameMustBeAString pins that |attr refuses a name that is not one.
//
// do_attr starts with inspect.getattr_static, which looks the name up in the
// type's dictionaries. So an unhashable name is refused by that lookup before
// anything checks it is a string, and a hashable one that is not a string is
// refused by the check after it. Neither reaches the object, so both answer
// the same whatever is being asked.
//
// Expectations from CPython jinja2 3.1.6.
func TestAttrNameMustBeAString(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{`{{ "ab"|attr(name=true) }}`, "attribute name must be string, not 'bool'"},
		{`{{ "ab"|attr(name=none) }}`, "attribute name must be string, not 'NoneType'"},
		{`{{ "ab"|attr(name=2.5) }}`, "attribute name must be string, not 'float'"},
		{`{{ "ab"|attr(name=3) }}`, "attribute name must be string, not 'int'"},
		{`{{ "ab"|attr(name=[1]) }}`, "unhashable type: 'list'"},
		{`{{ "ab"|attr(name={}) }}`, "unhashable type: 'dict'"},
		// A tuple hashes only if what it holds does, and the refusal
		// names what actually stopped it.
		{`{{ "ab"|attr(name=(1,[2])) }}`, "unhashable type: 'list'"},
	} {
		_, err := renderVars(t, New(), tc.src, nil)
		if err == nil {
			t.Errorf("%s: rendered; want %q", tc.src, tc.want)
			continue
		}
		if got := err.Error(); got != tc.want {
			t.Errorf("%s\n  = %q\n want %q", tc.src, got, tc.want)
		}
	}
}
