// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"unicode"
)

// The hand-written scans agree with the standard library ones they replaced.
//
// str.split had to be written out to become interruptible: finding the pieces
// was a single call nothing could pause, and the pieces are as many as the
// subject has words. Writing out a function the standard library already has is
// how a subtle difference gets introduced -- an empty field kept where one was
// dropped, a separator counted twice, a trailing piece lost -- so the test is
// not a table of cases but the original, run beside it.
//
// The inputs cover what the tables usually miss: empty strings, a subject that
// is all separators, separators at both ends, runs of them, a separator longer
// than one byte, and multi-byte space that is a space to Unicode and not to
// ASCII.
func TestSplitScansMatchTheStandard(t *testing.T) {
	subjects := []string{
		"", " ", "  ", "a", " a", "a ", " a ", "a b", "a  b", "  a  b  ",
		"\t\n\v\f\r a   b  ", "aXb", "XaX", "XX", "aXXb",
		"abXXcd", " ", "é ö", "  ", "a b",
		strings.Repeat("a b ", 50), strings.Repeat(" ", 20),
		"no-separators-at-all", "XendsX",
	}
	// And a few thousand random ones, because the cases above are the ones
	// I thought of.
	rng := rand.New(rand.NewPCG(20260919, 0x5eed))
	alphabet := []rune{'a', 'b', 'X', ' ', '\t', '\n', ' ', 'é'}
	for range 3000 {
		var b strings.Builder
		for range rng.IntN(24) {
			b.WriteRune(alphabet[rng.IntN(len(alphabet))])
		}
		subjects = append(subjects, b.String())
	}

	for _, s := range subjects {
		got, err := fieldsYielding(nil, s)
		if err != nil {
			t.Fatalf("fieldsYielding(%q): %v", s, err)
		}
		if want := strings.FieldsFunc(s, unicode.IsSpace); !slices.Equal(got, want) {
			t.Fatalf("fieldsYielding(%q)\n  = %q\n want %q", s, got, want)
		}

		for _, sep := range []string{"X", "XX", " ", "a", "zz"} {
			for _, n := range []int{-1, 0, 1, 2, 3, 5, 100} {
				got, err := splitNYielding(nil, s, sep, n)
				if err != nil {
					t.Fatalf("splitNYielding(%q, %q, %d): %v", s, sep, n, err)
				}
				want := strings.SplitN(s, sep, n)
				if !slices.Equal(got, want) {
					t.Fatalf("splitNYielding(%q, %q, %d)\n  = %q\n want %q",
						s, sep, n, got, want)
				}
			}
		}
	}
}

// splitRightN keeps the whole head in one piece, which is what rsplit's
// maxsplit means, and still agrees with the old spelling.
func TestSplitRightNMatchesTheOldSpelling(t *testing.T) {
	old := func(s, sep string, n int) []string {
		all := strings.Split(s, sep)
		if len(all) <= n {
			return all
		}
		head := strings.Join(all[:len(all)-n+1], sep)
		return append([]string{head}, all[len(all)-n+1:]...)
	}
	for _, s := range []string{
		"", "X", "aXb", "aXbXc", "aXbXcXd", "XaXbX", "XX", "abc",
		strings.Repeat("aX", 30),
	} {
		for _, sep := range []string{"X", "XX", "a"} {
			for _, n := range []int{1, 2, 3, 10} {
				got, err := splitRightN(nil, s, sep, n)
				if err != nil {
					t.Fatalf("splitRightN(%q, %q, %d): %v", s, sep, n, err)
				}
				if want := old(s, sep, n); !slices.Equal(got, want) {
					t.Fatalf("splitRightN(%q, %q, %d)\n  = %q\n want %q",
						s, sep, n, got, want)
				}
			}
		}
	}
}
