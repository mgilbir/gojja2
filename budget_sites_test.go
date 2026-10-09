// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"strings"
	"testing"

	"github.com/mgilbir/gojja2"
	"github.com/mgilbir/gojja2/errs"
	"github.com/mgilbir/gojja2/value"
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
//
// The same rule catches three subtler maskings, each of which made a case here
// pass for the wrong reason until `make mutate` said so:
//
//   - a *named* filter or test is a call, and a call charges a step, so
//     `range(2000)|map("string")` measures the call and not map's own walk.
//     Ask by attribute instead.
//   - reading a context value converts it, and converting a container is
//     charged per element, so a bound below that cost is what refuses. The
//     dict-view case puts its bound *above* the conversion for that reason.
//   - a walk that materialises its source and then walks it again costs the
//     same twice. The loop cases break on the first pass so the second walk
//     costs one.
//
// methods.go's pad is not in the table: no *budget* can tell its charge from
// the ones repeatString makes anyway, because whatever the halves cost
// together they cost separately too. It is measured, by the gate a budget is
// not -- see TestPadChargesTheWholeCentreBeforeBuildingEitherHalf below.
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
	wide := map[string]any{}
	for i := range 2000 {
		wide[fmt.Sprintf("k%d", i)] = i
	}
	short := make([]any, 2000)
	for i := range short {
		short[i] = "x"
	}
	// A wide dictionary three long keys down. pprint wraps it one entry per
	// line, and every line is indented past the keys above it: the indent
	// adds up to far more than the repr the same walk is charged for.
	entries := map[string]any{}
	for i := range 200 {
		entries[fmt.Sprintf("e%d", i)] = 1
	}
	indented := any(entries)
	for range 3 {
		indented = map[string]any{strings.Repeat("k", 100): indented}
	}
	// Sixty single-entry dicts, each keyed by a hundred characters.
	var chain any = "x"
	for range 60 {
		chain = map[string]any{strings.Repeat("k", 100): chain}
	}
	// Three of those keys, then bytes long enough to wrap a literal per line.
	var wrapped any = []byte(strings.Repeat("a", 8000))
	for range 3 {
		wrapped = map[string]any{strings.Repeat("k", 100): wrapped}
	}
	vars := map[string]any{
		"indented": indented,
		"chain":    chain,
		"wrapped":  wrapped,
		"s":        strings.Repeat("a", 1<<16),
		"b":        []byte(strings.Repeat("a", 1<<16)),
		// Two thousand one-character strings: a walk long enough to
		// exhaust the iteration bound while the bytes they add up to
		// stay well inside the output bound, so a per-item step is the
		// only thing that can refuse.
		"short": short,
		"pairs": map[string]any{"a": "1"},
		// Two thousand keys, so a *view* of it is a walk long enough to
		// exhaust the iteration bound. A dict itself is copied without a
		// per-item step; only the view takes the guarded walk.
		"wide": wide,
		// Two thousand characters: long enough to exhaust the iteration
		// bound and short enough that the bytes stay inside the output
		// one, which is what leaves a per-item step as the only thing
		// that can refuse.
		"s2k": strings.Repeat("a", 2000),
		// The same length with nothing in it that the one-key sets below
		// hold, so a walk over it never finds what it is looking for and
		// cannot stop early.
		"z2k": strings.Repeat("z", 2000),
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
		// A host object that behaves as a list of a million items, so
		// `in` scans it through the Sequence interface with no
		// conversion charged ahead of the scan.
		"hostSeq":  value.FromObject(millionInts{}),
		"hostIter": value.FromObject(millionIter{}),
		// An exponent from the context, so `2 ** e` is computed at
		// render time: 20,001 bits, about 2.5KB, under a 4KiB bound
		// once and over it twice.
		"e20k": 20000,
	}
	for name, tc := range map[string]struct {
		src      string
		want     error
		outBytes int64 // 4096 unless set
		iters    int64 // 1000 unless set
		// exts are the extensions the case needs. Only loopcontrols,
		// and only so that a loop body can `{% break %}` on its first
		// pass: a walk that materialises its source and then walks it
		// again costs the same twice, so the bound cannot tell the two
		// apart unless the second walk stops at one.
		exts []string
	}{
		// alloc.go, repeatStringN: reached on its own only through
		// tojson's indent, whose unit is repeated once per level and
		// once per element, so a template-chosen one sizes the whole
		// document. Every other caller charges the same bytes again
		// itself, so this is the shape that measures it.
		"tojson indent": {`{% set v = deep|tojson(indent=10) %}`,
			gojja2.ErrOutputTooLarge, 0, 0, nil},
		// value/strformat.go, formatFloat; value/pyformat.go, floatBody,
		// integerDigits and padDigits: a precision is a size the template
		// chose, and strconv, fmt and strings.Repeat allocate it. Uncharged,
		// `{:.99999999999999f}` killed the process with a fatal out of
		// memory that no recover catches.
		"format float precision": {`{% set v = '{:.100000f}'.format(f) %}`, gojja2.ErrOutputTooLarge, 0, 0, nil},
		// value/strformat.go, pad: a width is a fill of that many runes,
		// charged before it is built.
		"format width":            {`{% set v = '{:>100000}'.format(x) %}`, gojja2.ErrOutputTooLarge, 0, 0, nil},
		"percent float precision": {`{% set v = '%.100000f' % f %}`, gojja2.ErrOutputTooLarge, 0, 0, nil},
		"percent int precision":   {`{% set v = '%.100000d' % n1 %}`, gojja2.ErrOutputTooLarge, 0, 0, nil},
		"markup percent int precision": {`{% set v = ('%.100000d'|safe) % n1 %}`,
			gojja2.ErrOutputTooLarge, 0, 0, nil},
		// methods.go, pad
		"str center": {`{% set v = x.center(2097152) %}`, gojja2.ErrOutputTooLarge, 0, 0, nil},
		"str ljust":  {`{% set v = x.ljust(2097152) %}`, gojja2.ErrOutputTooLarge, 0, 0, nil},
		// methods.go, methodTranslate
		"str translate": {`{% set v = s.translate({97: "xx"}) %}`, gojja2.ErrOutputTooLarge, 0, 0, nil},
		// bytes_methods.go, pad
		"bytes center": {`{% set v = b.center(2097152) %}`, gojja2.ErrOutputTooLarge, 0, 0, nil},
		// bytes_methods.go, bytesExpandtabs
		"bytes expandtabs": {`{% set v = b.expandtabs() %}`, gojja2.ErrOutputTooLarge, 0, 0, nil},
		// bytes_methods.go, bytesHex
		"bytes hex": {`{% set v = b.hex() %}`, gojja2.ErrOutputTooLarge, 0, 0, nil},
		// bytes_methods.go, bytesTranslate
		"bytes translate": {`{% set v = b.translate(none) %}`, gojja2.ErrOutputTooLarge, 0, 0, nil},
		// bytes_methods.go, bytesReplace
		"bytes replace": {`{% set v = b.replace("a".encode(), "b".encode()) %}`,
			gojja2.ErrOutputTooLarge, 0, 0, nil},
		// numbers.go, intToBytes
		"int to_bytes": {`{% set v = n1.to_bytes(2097152, "big") %}`, gojja2.ErrOutputTooLarge, 0, 0, nil},
		// filters.go, filterRound
		// A float carries no decimal past ~1080 places and the
		// precision is clamped there, so this is the one site whose
		// charge cannot exceed a four-kilobyte budget.
		"round precision": {`{% set v = f|round(1000) %}`, gojja2.ErrOutputTooLarge,
			512, 0, nil},
		// filters_web.go, writeJSONString
		"tojson string":       {`{% set v = s|tojson %}`, gojja2.ErrOutputTooLarge, 0, 0, nil},
		"tojson short string": {`{% set v = s4k|tojson %}`, gojja2.ErrOutputTooLarge, 1000, 0, nil},

		// The rest bound a *walk* rather than a size, so they are asked
		// for two thousand items whose bytes stay well inside the
		// output bound: only a per-item step can refuse. The source is
		// a range() and not a list from the context, because converting
		// a context list is charged as it goes and would be what
		// refused instead.

		// bytes_methods.go, bytesList -- every bytes method that splits
		"bytes split": {`{% set v = b.split("a".encode()) %}`,
			gojja2.ErrTooManyIterations, 0, 0, nil},
		// methods.go, splitMethod
		"str split": {`{% set v = s.split("a") %}`, gojja2.ErrTooManyIterations, 0, 0, nil},
		// bytes_methods.go, bytesJoin -- charged per item as it walks
		"bytes join": {`{% set v = "".encode().join([b, b]) %}`,
			gojja2.ErrOutputTooLarge, 0, 0, nil},
		// methods.go, methodJoin: the byte charge and the step are two
		// sites in one walk, so they need two different shapes.
		"str join bytes": {`{% set v = "".join([s, s]) %}`, gojja2.ErrOutputTooLarge, 0, 0, nil},
		// Joining a *string* walks its characters, and nothing charges
		// for that walk, so join's own step is the only one that can
		// refuse. Handing it `range(2000)|map("string")` instead -- as
		// this case did -- pays two thousand steps building the list
		// first, and taking join's step out changed nothing.
		"str join steps": {`{% set v = "".join(s2k) %}`, gojja2.ErrTooManyIterations, 0, 1000, nil},
		// filters_seq.go, filterJoin
		"join filter steps": {`{% set v = range(2000)|join %}`, gojja2.ErrTooManyIterations, 0, 1000, nil},
		// filters_seq.go, filterBatch
		"batch": {`{% set v = range(2000)|batch(2) %}`, gojja2.ErrTooManyIterations, 0, 1000, nil},
		// filters_web.go, filterURLEncode. The bound sits *between* what
		// building the pairs costs (4,000 for `range(2000)|batch(2)`)
		// and what urlencode's own walk adds (1,000 more), because every
		// shape that hands it pairs has already paid for them. The case
		// this replaced asked for two thousand pairs under a bound of a
		// thousand, so `map` refused before urlencode ran at all -- it
		// passed, and it passed for the wrong reason. `make mutate` is
		// what said so: taking urlencode's charge out changed nothing.
		"urlencode": {`{% set v = range(2000)|batch(2)|urlencode %}`,
			gojja2.ErrTooManyIterations, 0, 4500, nil},
		// filters_seq.go, filterFirst: one item is one step, so what
		// this measures is that asking costs anything at all.
		"first costs a step": {`{% set a = range(9)|first %}{% set b = range(9)|first %}`,
			gojja2.ErrTooManyIterations, 0, 1, nil},
		// runtime.go, makeLoopSource: a loop materialises its source
		// before it runs, and the walk that does it is charged
		// separately from the loop's own. Two thousand items cost two
		// thousand of each, so a bound between one and two of them
		// tells the materialising walk from the loop.
		//
		// That was not enough, and `make mutate` said so: with the body
		// running four thousand times, taking the materialising step out
		// only moved which walk reached the bound. The body breaks on
		// its first pass so that the loop's own walk costs one, and then
		// the materialising walk is the only thing that can refuse.
		"loop source": {`{% for x in s4k %}{% endfor %}`, gojja2.ErrTooManyIterations, 0, 2500, nil},
		"loop source alone": {`{% for x in s4k %}{% break %}{% endfor %}`,
			gojja2.ErrTooManyIterations, 0, 2500, []string{"loopcontrols"}},
		// The other half of makeLoopSource: a *size-guarded* source --
		// a dict view -- takes a different walk from the general one,
		// and only a view reaches it, because a dict itself is copied
		// without a per-item step.
		// The other half of makeLoopSource: a *size-guarded* source --
		// a dict view -- takes a different walk from the general one,
		// and only a view reaches it, because a dict itself is copied
		// without a per-item step.
		//
		// The bound is above two thousand on purpose. Reading `wide`
		// converts a two-thousand-entry Go map, and that conversion is
		// charged per entry, so a bound below it is what refuses and
		// the walk under test is never reached. Three thousand sits
		// between the conversion alone and the conversion plus the
		// walk, which is the only place the two can be told apart.
		"loop source view": {`{% for x in wide.keys() %}{% break %}{% endfor %}`,
			gojja2.ErrTooManyIterations, 0, 3000, []string{"loopcontrols"}},
		// dictview.go, isdisjoint: the walk is over the *argument*, so
		// it is a range -- which costs nothing to walk -- and the view
		// is the one-key dict. Nothing matches, so it walks all of it.
		"keys view isdisjoint": {`{% set v = pairs.keys().isdisjoint(range(2000)) %}`,
			gojja2.ErrTooManyIterations, 0, 1000, nil},
		// filters_seq.go, filterMap's own walk. By attribute, not by
		// filter name: naming a filter makes each item a *call*, and a
		// call charges a step of its own, which is what refused when
		// this asked for map("string").
		"map steps": {`{% set v = range(2000)|map(attribute="real") %}`,
			gojja2.ErrTooManyIterations, 0, 1000, nil},
		// set_methods.go, setArgument: a set method takes any iterable,
		// and walking a string is charged by nothing else. The receiver
		// is a one-element set so that building it costs next to
		// nothing.
		"set method argument": {`{% set v = (pairs.keys() - []).union(s2k) %}`,
			gojja2.ErrTooManyIterations, 0, 1000, nil},
		// set_methods.go, setIntersectionArgument: intersection and the
		// subset tests take any iterable, and it stops early only once
		// every member has been found -- so an argument that holds none
		// of them is walked to the end.
		"set intersection argument": {`{% set v = (pairs.keys() - []).intersection(z2k) %}`,
			gojja2.ErrTooManyIterations, 0, 1000, nil},
		// set_methods.go, setScan: isdisjoint stops at the first member
		// it finds, and this argument has none.
		"set scan argument": {`{% set v = (pairs.keys() - []).isdisjoint(z2k) %}`,
			gojja2.ErrTooManyIterations, 0, 1000, nil},
		// filters.go, pformatItems: the separator carries the indent and is
		// written once per entry, so a wide container indented far by the
		// keys above it costs entries * indent. The bound is above the repr
		// and the one charge for the pad, and below the separators.
		"pprint separators": {`{% set v = indented|pprint %}`, gojja2.ErrOutputTooLarge, 20000, 100000, nil},
		// filters.go, pprintIndent: one entry per container, so the pad is
		// built and never repeated. The repr charged at every level is about
		// as large as the indents, and the bound sits between the reprs and
		// the reprs plus the indents.
		"pprint indent": {`{% set v = chain|pprint %}`, gojja2.ErrOutputTooLarge, 220000, 100000, nil},
		// filters.go, pformatBytes: the same separator, for a bytes that
		// wraps into a literal per line.
		"pprint bytes separators": {`{% set v = wrapped|pprint %}`, gojja2.ErrOutputTooLarge, 20000, 100000, nil},
		// dictview.go, mappingProxy.pairs: a proxy over something that
		// is not a dict is read through the wrapped object's own
		// items(), and that walk is the charge. A proxy over a proxy
		// over a dict of two thousand keys takes it. Reading `wide` from
		// the context costs two thousand steps by itself, so the bound
		// sits between that and the pairs walk on top of it -- at a
		// thousand the conversion refused first and taking the charge
		// out changed nothing.
		"proxy pairs": {`{% set C = pairs.keys().mapping.__class__ %}{% set v = C(C(wide))|items %}`,
			gojja2.ErrTooManyIterations, 0, 3000, nil},

		// The value package charges through functions rather than a
		// State, which make mutate did not see until 2026-10-09; these
		// are the sites that then survived. Each list is built in the
		// template from a lazy range(), at a cost of one step an item,
		// so the bound sits between building it and walking it again.

		// call.go, unpack: a list in hand is charged its length.
		"unpack a list": {`{% set xs = range(600)|list %}{% set a, b = xs %}`,
			gojja2.ErrTooManyIterations, 0, 0, nil},
		// value/compare.go, Contains over a list or tuple.
		"in a list": {`{% set xs = range(600)|list %}{% set v = -1 in xs %}`,
			gojja2.ErrTooManyIterations, 0, 0, nil},
		// value/compare.go, Contains over a Sequence object. A range
		// answers arithmetically and a |groupby pair holds two, so only a
		// host object reaches the scan with any length.
		"in a host sequence": {`{% set v = -1 in hostSeq %}`, gojja2.ErrTooManyIterations, 0, 0, nil},
		// dictview.go: a values view is searched element by element, and
		// was searched by a walk of its own that charged nothing, so `in`
		// over d.values() in a loop did len(d) comparisons per charged
		// step. It now takes containsIterated below. Converting wide
		// costs two thousand steps and the walk two thousand more.
		"in a values view": {`{% set v = -1 in wide.values() %}`, gojja2.ErrTooManyIterations, 0, 3000, nil},
		// value/compare.go, containsIterated: an object that can only be
		// iterated is searched by walking it. A dict view answers `in`
		// itself, so again only a host object reaches the walk.
		"in a host iterable": {`{% set v = -1 in hostIter %}`, gojja2.ErrTooManyIterations, 0, 0, nil},
		// value/ops.go, Add and Sub on integers too wide for int64: the
		// result is charged its bytes, as `*` and `**` charge theirs.
		// Measured: `2 ** e20k` costs 5,000 bytes by itself and each
		// operation on it 2,501 more, so the bound sits between.
		"wide int add": {`{% set x = 2 ** e20k %}{% set v = x + x %}`, gojja2.ErrOutputTooLarge, 6000, 0, nil},
		"wide int sub": {`{% set x = 2 ** e20k %}{% set v = x - (0 - x) %}`, gojja2.ErrOutputTooLarge, 6000, 0, nil},
		// value/set.go, NewSet: a view subtracted from builds a set of it.
		"set of a view": {`{% set v = wide.keys() - [] %}`, gojja2.ErrTooManyIterations, 0, 3000, nil},
		// value/ops.go, set - set: the second difference walks the first.
		// Measured: 10,003 steps with the charge, two thousand fewer
		// without it.
		"set minus set": {`{% set a = wide.keys() - [] %}{% set v = a - (pairs.keys() - []) %}`,
			gojja2.ErrTooManyIterations, 0, 9000, nil},
		// value/set.go, Set.Add: each new element is an item.
		"set add": {`{% set s = pairs.keys() - [] %}{% for i in range(600) %}{% set _ = s.add(i) %}{% endfor %}`,
			gojja2.ErrTooManyIterations, 0, 0, nil},
		// value/set.go, setReverseDifference and setDifference walk the
		// other operand into a slice or a set before anything else
		// refuses. With the charge gone the walk over a billion items is
		// stopped by nothing, which is what the charge is for.
		"range minus a view": {`{% set v = range(10 ** 9) - pairs.keys() %}`, gojja2.ErrTooManyIterations, 0, 0, nil},
		"view minus a range": {`{% set v = pairs.keys() - range(10 ** 9) %}`, gojja2.ErrTooManyIterations, 0, 0, nil},
	} {
		t.Run(name, func(t *testing.T) {
			out, iters := tc.outBytes, tc.iters
			if out == 0 {
				out = 4096
			}
			if iters == 0 {
				iters = 1000
			}
			opts := []gojja2.Option{
				gojja2.WithMaxOutputBytes(out),
				gojja2.WithMaxIterations(iters),
			}
			if len(tc.exts) > 0 {
				opts = append(opts, gojja2.WithExtensions(tc.exts...))
			}
			env := mustEnv(opts...)
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

// TestPadChargesTheWholeCentreBeforeBuildingEitherHalf measures methods.go's
// pad, the charge the table above cannot reach.
//
// A budget cannot tell it from the charges `repeatString` makes anyway: two
// halves cost together exactly what they cost apart, so whichever refuses, one
// of them does. ChargeBytes has a second gate that does not work that way. It
// refuses anything over maxAllocBytes outright, *before* the budget is
// consulted, and that ceiling is per charge rather than per render -- which is
// the whole reason pad adds the halves up before building either. Charge them
// separately and each one is legal while their sum is not.
//
// So: a width a little over twice the ceiling. With the charge the render is
// refused with an OverflowError naming the sum, and nothing is built. Without
// it, the first half passes the ceiling and the budget is what refuses, one
// gate later and a gigabyte and a half further down the road. The two errors
// are what tells the versions apart, so this asserts the kind and not just that
// something failed.
func TestPadChargesTheWholeCentreBeforeBuildingEitherHalf(t *testing.T) {
	// Between the ceiling and twice it, so that the sum is over and each
	// half is under -- 5,000,000,000 would put *both* halves over on their
	// own and the mutated build would refuse for the wrong reason. The
	// receiver comes from the context rather than a literal, which would
	// fold.
	const width = 3000000000
	env := mustEnv(gojja2.WithMaxOutputBytes(4096))
	tmpl, err := env.FromString(fmt.Sprintf(`{%% set v = x.center(%d) %%}`, width))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	var sb strings.Builder
	err = tmpl.Render(context.Background(), &sb, map[string]any{"x": "x"})
	if kind := errs.KindOf(err); kind != errs.OverflowError {
		t.Errorf("center(%d): got %v (%v), want an OverflowError: the sum of "+
			"the two halves is over the allocation ceiling even though "+
			"neither half is", width, kind, err)
	}
	if sb.Len() != 0 {
		t.Errorf("center(%d) wrote %d bytes; the result is bound to a name, "+
			"so nothing should reach the output", width, sb.Len())
	}
}

// millionIter is a host object that can only be iterated: a million integers.
type millionIter struct{}

func (millionIter) GetAttr(string) (value.Value, bool) { return value.Undefined, false }
func (millionIter) Iterate() iter.Seq[value.Value] {
	return func(yield func(value.Value) bool) {
		for i := range 1_000_000 {
			if !yield(value.Int(int64(i))) {
				return
			}
		}
	}
}

// millionInts is a host object that is a sequence of a million integers.
type millionInts struct{}

func (millionInts) GetAttr(string) (value.Value, bool) { return value.Undefined, false }
func (millionInts) Len() int                           { return 1_000_000 }
func (millionInts) GetIndex(i int) (value.Value, bool) {
	return value.Int(int64(i)), i >= 0 && i < 1_000_000
}
