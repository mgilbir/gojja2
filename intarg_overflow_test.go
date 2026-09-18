// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"context"
	"strings"
	"testing"
)

// An integer argument too large for the C type CPython converts it to is an
// OverflowError naming that type, not a TypeError about its own type.
//
// value.Value.Int64 answers false for two different things -- a value that is
// not an integer, and an integer too wide for int64 -- and indexOf could not
// tell them apart, so it took the type-error branch for both. That produced
//
//	TypeError: 'int' object cannot be interpreted as an integer
//
// which contradicts itself: the object *is* an int, and the complaint is about
// its magnitude. value.IsInteger already distinguished the two cases; nothing
// asked it.
//
// The C type is a property of the call site, not of the engine. Most integer
// arguments -- lengths, widths, indices, counts -- convert to Py_ssize_t. Three
// convert to a plain C int, and those refuse at 2**31 rather than 2**63, which
// is a range a template can observe long before it runs out of memory.
func TestIntegerArgumentOverflowsAtItsCType(t *testing.T) {
	const wideForSSizeT = "9223372036854775808" // 2**63, past Py_ssize_t
	const wideForInt = "2147483648"             // 2**31, past C int
	const smallForInt = "-2147483649"           // -2**31-1, past C int

	for _, tc := range []struct{ name, src, want string }{
		// --- Py_ssize_t: refuses only past 2**63 ------------------------
		{"replace filter count", `{{ "aaa"|replace("a","b",N) }}`, "C ssize_t"},
		{"center filter width", `{{ "ab"|center(N) }}`, "C ssize_t"},
		{"to_bytes length", `{{ (5).to_bytes(N,"big") }}`, "C ssize_t"},
		{"split maxsplit", `{{ "a b".split(" ",N)|list }}`, "C ssize_t"},
		{"rsplit maxsplit", `{{ "a b".rsplit(" ",N)|list }}`, "C ssize_t"},
		{"replace method count", `{{ "aaa".replace("a","b",N) }}`, "C ssize_t"},
		{"zfill width", `{{ "ab".zfill(N) }}`, "C ssize_t"},
		{"ljust width", `{{ "ab".ljust(N) }}`, "C ssize_t"},
		{"rjust width", `{{ "ab".rjust(N) }}`, "C ssize_t"},
		{"center method width", `{{ "ab".center(N) }}`, "C ssize_t"},
		{"list.insert index", `{% set L=[1,2] %}{{ L.insert(N,9) }}`, "C ssize_t"},
		{"list.pop index", `{% set L=[1,2] %}{{ L.pop(N) }}`, "C ssize_t"},
		// --- C int: refuses at 2**31 ------------------------------------
		{"expandtabs tabsize", `{{ "a	b".expandtabs(N) }}`, "C int"},
		{"splitlines keepends", `{{ "a\nb".splitlines(N)|list }}`, "C int"},
		{"sort reverse", `{{ [3,1]|sort(reverse=N) }}`, "C int"},
	} {
		// Past Py_ssize_t every site overflows, whatever its C type.
		src := strings.ReplaceAll(tc.src, "N", wideForSSizeT)
		checkOverflow(t, tc.name+" @2**63", src, tc.want)

		// Past C int, only the C int sites do.
		if tc.want == "C int" {
			for _, v := range []string{wideForInt, smallForInt} {
				checkOverflow(t, tc.name+" @"+v,
					strings.ReplaceAll(tc.src, "N", v), tc.want)
			}
		}
	}
}

func checkOverflow(t *testing.T, name, src, ctype string) {
	t.Helper()
	tmpl, err := New().FromString(src)
	if err != nil {
		t.Errorf("%s: compile: %v", name, err)
		return
	}
	_, err = tmpl.RenderString(context.Background(), nil)
	if err == nil {
		t.Errorf("%s: rendered, want an OverflowError", name)
		return
	}
	want := "Python int too large to convert to " + ctype
	if err.Error() != want {
		t.Errorf("%s:\n  got  %q\n  want %q", name, err.Error(), want)
	}
}

// A value that is genuinely not an integer keeps the TypeError, which is the
// other half of what indexOf could not tell apart.
func TestNonIntegerArgumentStaysATypeError(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{`{{ "ab"|center("x") }}`, "'str' object cannot be interpreted as an integer"},
		{`{{ "ab"|center(1.5) }}`, "'float' object cannot be interpreted as an integer"},
		{`{{ "ab"|center([1]) }}`, "'list' object cannot be interpreted as an integer"},
		{`{{ "a	b".expandtabs("x") }}`, "'str' object cannot be interpreted as an integer"},
		{`{{ [3,1]|sort(reverse="x") }}`, "'str' object cannot be interpreted as an integer"},
		{`{{ "a\nb".splitlines(1.5)|list }}`, "'float' object cannot be interpreted as an integer"},
	} {
		tmpl, err := New().FromString(tc.src)
		if err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		_, err = tmpl.RenderString(context.Background(), nil)
		if err == nil || err.Error() != tc.want {
			t.Errorf("%s:\n  got  %v\n  want %q", tc.src, err, tc.want)
		}
	}
}

// Everything inside the range still works, so the bound is a bound and not a
// clamp: a maxsplit of Py_ssize_t's maximum splits everything.
func TestIntegerArgumentInRangeStillWorks(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{`{{ "a b c".split(" ",9223372036854775807)|list }}`, "['a', 'b', 'c']"},
		{`{{ "a	b".expandtabs(2147483647) }}`, ""},
		{`{{ [3,1]|sort(reverse=2147483647) }}`, "[3, 1]"},
		{`{{ "a\nb".splitlines(2147483647)|list }}`, "['a\\n', 'b']"},
		{`{{ [3,1]|sort(reverse=0) }}`, "[1, 3]"},
	} {
		tmpl, err := New().FromString(tc.src)
		if err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		got, err := tmpl.RenderString(context.Background(), nil)
		if tc.want == "" {
			if err == nil {
				t.Errorf("%s: rendered %q, want a refusal on size", tc.src, got)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("%s = %q, %v; want %q", tc.src, got, err, tc.want)
		}
	}
}
