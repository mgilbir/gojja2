// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/mgilbir/gojja2"
)

// A Go map reaches a template in sorted key order, every time.
//
// Go randomises map iteration deliberately, and a Python dict preserves
// insertion order -- which a Go map has none of. Sorting is the only answer
// that makes `{% for k, v in m|items %}` render the same document twice, and
// it is documented as a divergence from CPython for exactly that reason.
//
// There are two conversions, and only one of them is reachable from JSON, which
// is what the data fuzzer generates: map[string]any has a fast path of its own
// and every other map type goes through reflection. Deleting the sort from the
// reflect path passed every test in this repository until this one.
func TestGoMapsConvertInKeyOrder(t *testing.T) {
	const n = 24 // wide enough that a random order is essentially never sorted
	anyMap := make(map[string]any, n)
	intMap := make(map[string]int, n)
	runeMap := make(map[rune]int, n)
	for i := range n {
		k := fmt.Sprintf("k%02d", n-i) // inserted in descending order
		anyMap[k] = i
		intMap[k] = i
		runeMap[rune('a'+n-i)] = i
	}

	for name, m := range map[string]any{
		"map[string]any": anyMap,
		"map[string]int": intMap,
		"map[rune]int":   runeMap,
	} {
		t.Run(name, func(t *testing.T) {
			tmpl, err := gojja2.New().FromString(
				`{% for k, v in m|items %}{{ k }}={{ v }};{% endfor %}`)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			first, err := tmpl.RenderString(context.Background(), map[string]any{"m": m})
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			// Rendered many times, because an unsorted order can agree
			// with a sorted one by chance -- just not repeatedly.
			for range 20 {
				again, err := tmpl.RenderString(context.Background(), map[string]any{"m": m})
				if err != nil {
					t.Fatalf("render: %v", err)
				}
				if again != first {
					t.Fatalf("two renders of the same map disagree:\n  %q\n  %q",
						first, again)
				}
			}
			// And in ascending key order, not merely a consistent one.
			var keys []string
			for _, pair := range strings.Split(strings.TrimSuffix(first, ";"), ";") {
				if pair == "" {
					continue
				}
				keys = append(keys, strings.SplitN(pair, "=", 2)[0])
			}
			// Compared as numbers where they are numbers: a rune key
			// renders as the integer it is, and "100" sorts before
			// "97" as text while 100 does not before 97.
			for i := 1; i < len(keys); i++ {
				if !inOrder(keys[i-1], keys[i]) {
					t.Errorf("keys are stable but not sorted: %v", keys)
					break
				}
			}
		})
	}
}

// inOrder reports a <= b, numerically when both are numbers.
func inOrder(a, b string) bool {
	x, errA := strconv.Atoi(a)
	y, errB := strconv.Atoi(b)
	if errA == nil && errB == nil {
		return x <= y
	}
	return a <= b
}
