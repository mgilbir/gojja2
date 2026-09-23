// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mgilbir/gojja2"
)

// One charge site, one template that reaches it and nothing else.
//
// `make mutate` takes each budget charge out in turn and asks what notices.
// Twenty-two of the thirty-eight survived the suite as it stood -- not because
// the bound did not hold, but because the error still arrived from somewhere
// else: a second charge further on, or the write that put the result in the
// output. A charge that only ever fires after another one has already refused
// is not measured by anything.
//
// So every case here binds its result to a name instead of printing it. Nothing
// downstream can then charge it, and the site under test is the only thing
// standing between the template and the allocation it asked for. Take the
// charge out and the render succeeds, which is what the mutation reports.
func TestEachBudgetChargeRefusesOnItsOwn(t *testing.T) {
	// Every receiver comes from the context, never from a literal: an
	// expression whose operands are all constant is folded at compile time
	// and the charge is never reached at all. `{{ "x" * 2097152 }}` and
	// `{{ (1).to_bytes(2097152, "big") }}` both looked like passing cases
	// that way, and the second passed or failed depending on which other
	// test had run first.

	// Sixty levels, so tojson's indent unit is repeated sixty times over:
	// the pad is what grows, not the document.
	deep := any("x")
	for range 60 {
		deep = map[string]any{"k": deep}
	}
	short := make([]any, 2000)
	for i := range short {
		short[i] = "x"
	}
	vars := map[string]any{
		"s": strings.Repeat("a", 1<<16),
		"b": []byte(strings.Repeat("a", 1<<16)),
		// Two thousand one-character strings: a walk long enough to
		// exhaust the iteration bound while the bytes they add up to
		// stay well inside the output bound, so a per-item step is the
		// only thing that can refuse.
		"short": short,
		"pairs": map[string]any{"a": "1"},
		// Shorter than one charge block, so the escaping walk never
		// reaches its in-loop charge and the tail charge is the only
		// one that can refuse.
		"s4k":  strings.Repeat("a", 4000),
		"deep": deep,
		// Not a literal: a constant round() folds at compile time and
		// never reaches the filter at all.
		"f":  1.5,
		"x":  "x",
		"n1": 1,
	}
	for name, tc := range map[string]struct {
		src      string
		want     error
		outBytes int64 // 4096 unless set
		iters    int64 // 1000 unless set
	}{
		// alloc.go, repeatStringN: reached on its own only through
		// tojson's indent, whose unit is repeated once per level and
		// once per element, so a template-chosen one sizes the whole
		// document. Every other caller charges the same bytes again
		// itself, so this is the shape that measures it.
		"tojson indent": {`{% set v = deep|tojson(indent=10) %}`,
			gojja2.ErrOutputTooLarge, 0, 0},
		// methods.go, pad
		"str center": {`{% set v = x.center(2097152) %}`, gojja2.ErrOutputTooLarge, 0, 0},
		"str ljust":  {`{% set v = x.ljust(2097152) %}`, gojja2.ErrOutputTooLarge, 0, 0},
		// methods.go, methodTranslate
		"str translate": {`{% set v = s.translate({97: "xx"}) %}`, gojja2.ErrOutputTooLarge, 0, 0},
		// bytes_methods.go, pad
		"bytes center": {`{% set v = b.center(2097152) %}`, gojja2.ErrOutputTooLarge, 0, 0},
		// bytes_methods.go, bytesExpandtabs
		"bytes expandtabs": {`{% set v = b.expandtabs() %}`, gojja2.ErrOutputTooLarge, 0, 0},
		// bytes_methods.go, bytesHex
		"bytes hex": {`{% set v = b.hex() %}`, gojja2.ErrOutputTooLarge, 0, 0},
		// bytes_methods.go, bytesTranslate
		"bytes translate": {`{% set v = b.translate(none) %}`, gojja2.ErrOutputTooLarge, 0, 0},
		// bytes_methods.go, bytesReplace
		"bytes replace": {`{% set v = b.replace("a".encode(), "b".encode()) %}`,
			gojja2.ErrOutputTooLarge, 0, 0},
		// numbers.go, intToBytes
		"int to_bytes": {`{% set v = n1.to_bytes(2097152, "big") %}`, gojja2.ErrOutputTooLarge, 0, 0},
		// filters.go, filterRound
		// A float carries no decimal past ~1080 places and the
		// precision is clamped there, so this is the one site whose
		// charge cannot exceed a four-kilobyte budget.
		"round precision": {`{% set v = f|round(1000) %}`, gojja2.ErrOutputTooLarge,
			512, 0},
		// filters_web.go, writeJSONString
		"tojson string":       {`{% set v = s|tojson %}`, gojja2.ErrOutputTooLarge, 0, 0},
		"tojson short string": {`{% set v = s4k|tojson %}`, gojja2.ErrOutputTooLarge, 1000, 0},

		// The rest bound a *walk* rather than a size, so they are asked
		// for two thousand items whose bytes stay well inside the
		// output bound: only a per-item step can refuse. The source is
		// a range() and not a list from the context, because converting
		// a context list is charged as it goes and would be what
		// refused instead.

		// bytes_methods.go, bytesList -- every bytes method that splits
		"bytes split": {`{% set v = b.split("a".encode()) %}`,
			gojja2.ErrTooManyIterations, 0, 0},
		// methods.go, splitMethod
		"str split": {`{% set v = s.split("a") %}`, gojja2.ErrTooManyIterations, 0, 0},
		// bytes_methods.go, bytesJoin -- charged per item as it walks
		"bytes join": {`{% set v = "".encode().join([b, b]) %}`,
			gojja2.ErrOutputTooLarge, 0, 0},
		// methods.go, methodJoin: the byte charge and the step are two
		// sites in one walk, so they need two different shapes.
		"str join bytes": {`{% set v = "".join([s, s]) %}`, gojja2.ErrOutputTooLarge, 0, 0},
		"str join steps": {`{% set v = "".join(range(2000)|map("string")) %}`, gojja2.ErrTooManyIterations,
			0, 1000},
		// filters_seq.go, filterJoin
		"join filter steps": {`{% set v = range(2000)|join %}`, gojja2.ErrTooManyIterations, 0, 1000},
		// filters_seq.go, filterBatch
		"batch": {`{% set v = range(2000)|batch(2) %}`, gojja2.ErrTooManyIterations, 0, 1000},
		// filters_web.go, filterURLEncode
		"urlencode": {`{% set v = range(2000)|map("string")|list|batch(2)|urlencode %}`, gojja2.ErrTooManyIterations, 0, 1000},
		// filters_seq.go, filterFirst: one item is one step, so what
		// this measures is that asking costs anything at all.
		"first costs a step": {`{% set a = range(9)|first %}{% set b = range(9)|first %}`,
			gojja2.ErrTooManyIterations, 0, 1},
		// runtime.go, makeLoopSource: a loop materialises its source
		// before it runs, and the walk that does it is charged
		// separately from the loop's own. Two thousand items cost two
		// thousand of each, so a bound between one and two of them
		// tells the materialising walk from the loop.
		"loop source": {`{% for x in s4k %}{% endfor %}`, gojja2.ErrTooManyIterations, 0, 2500},
	} {
		t.Run(name, func(t *testing.T) {
			out, iters := tc.outBytes, tc.iters
			if out == 0 {
				out = 4096
			}
			if iters == 0 {
				iters = 1000
			}
			env := mustEnv(
				gojja2.WithMaxOutputBytes(out),
				gojja2.WithMaxIterations(iters),
			)
			tmpl, err := env.FromString(tc.src)
			if err != nil {
				t.Fatalf("compile %q: %v", tc.src, err)
			}
			var sb strings.Builder
			err = tmpl.Render(context.Background(), &sb, vars)
			if !errors.Is(err, tc.want) {
				t.Errorf("%s: got %v, want %v", tc.src, err, tc.want)
			}
			if sb.Len() != 0 {
				t.Errorf("%s: wrote %d bytes; the result is bound to a "+
					"name, so nothing should reach the output and no "+
					"write can be what refused", tc.src, sb.Len())
			}
		})
	}
}
