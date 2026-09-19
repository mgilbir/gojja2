// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/gojja2"
)

// workload is one filter driven over a sequence of the caller's length.
//
// kind names the sequence to build, so each case builds only what it uses:
// sizing every shape for the largest case would spend most of the test making
// dictionaries that most of it never looks at.
type workload struct {
	kind string // ints, strs, dicts or mapping
	src  string
	// n is chosen so that this filter's own work dominates the render.
	//
	// It is fixed rather than searched for. Reaching the sequence --
	// converting the argument, then materialising it -- already yields, so
	// the deadline has to fall past that to say anything about the filter,
	// and deriving the size from the difference between two timed renders
	// turned out to measure the garbage collector: the subtraction
	// amplifies the noise in both, and the deadline it produced sometimes
	// landed past the end of the render it was meant to interrupt.
	n int
}

// keyed is an element whose attribute costs a reflective field read, and whose
// conversion costs nothing until then.
type keyed struct {
	K int
	V int
}

// The elements are distinct wherever the filter's cost depends on it. |unique
// over a sequence that is mostly repeats finishes as soon as the set stops
// growing, and would be graded on work it never did.
func buildSeq(kind string, n int) map[string]any {
	switch kind {
	case "ints":
		v := make([]any, n)
		for i := range v {
			v[i] = i
		}
		return map[string]any{kind: v}
	case "strs":
		v := make([]any, n)
		for i := range v {
			v[i] = fmt.Sprintf("s%07d", i)
		}
		return map[string]any{kind: v}
	case "dicts":
		v := make([]any, n)
		for i := range v {
			v[i] = map[string]any{"k": i, "v": i}
		}
		return map[string]any{kind: v}
	case "structs":
		// A struct is wrapped lazily, so converting the argument is
		// cheap while reading a field is reflection. That puts the cost
		// in the filter rather than in reaching the sequence, which is
		// what a workload for an attribute lookup has to do: over
		// dictionaries the conversion dominates, the deadline expires
		// before the filter starts, and the filter is not what is being
		// measured.
		v := make([]any, n)
		for i := range v {
			v[i] = &keyed{K: i, V: i}
		}
		return map[string]any{kind: v}
	case "text":
		// Markup, a URL, an entity and non-ASCII, so no filter takes a
		// short path past the work it is here to be measured doing.
		//
		// Lines, too: |indent and |pprint do a pass per line, so over a
		// single long line their work is one copy however big the input
		// is, and the workload would be measuring memmove.
		return map[string]any{kind: strings.Repeat(
			"Héllo wörld, <b>this</b> &amp; a http://example.com sentence.\n", n)}
	case "nospace":
		// One enormous field. Splitting it produces a single piece, so
		// the walk that finds it is the only thing that can yield --
		// wrapping the pieces afterwards has just the one to wrap.
		//
		// There is no bytes twin here: scanning bytes for a space runs
		// at about half a nanosecond each, so even the largest subject
		// the output bound allows is under a tenth of a second, and a
		// workload big enough to grade would be most of a gigabyte.
		return map[string]any{kind: strings.Repeat("abcdefghij", n)}
	case "bytes":
		return map[string]any{kind: []byte(strings.Repeat(
			"hello world this is a sentence\n", n))}
	case "mapping":
		v := make(map[string]any, n)
		for i := range n {
			v[fmt.Sprintf("k%07d", i)] = i
		}
		return map[string]any{kind: v}
	}
	panic("unknown sequence kind " + kind)
}

// sequenceWorkloads makes each filter walk a sequence of the caller's length.
//
// Each has to do enough work *after* the sequence is materialised to be graded
// on it: materialising charges per item and so yields already, which is why a
// filter could be wholly uninterruptible past that point and still look fine.
var sequenceWorkloads = map[string]workload{
	"unique":               {"ints", `{{ ints|unique|list|length }}`, 300000},
	"unique, strings":      {"strs", `{{ strs|unique|list|length }}`, 300000},
	"unique, case-folded":  {"strs", `{{ strs|unique(case_sensitive=false)|list|length }}`, 300000},
	"unique, by attribute": {"dicts", `{{ dicts|unique(attribute='k')|list|length }}`, 300000},
	"unique, struct field": {"structs", `{{ structs|unique(attribute='K')|list|length }}`, 400000},
	"min":                  {"ints", `{{ ints|min }}`, 500000},
	"max":                  {"ints", `{{ ints|max }}`, 500000},
	"min, by attribute":    {"dicts", `{{ dicts|min(attribute='k') }}`, 500000},
	"sort":                 {"ints", `{{ ints|sort|length }}`, 250000},
	"sort, by attribute":   {"dicts", `{{ dicts|sort(attribute='k')|length }}`, 100000},
	"sort, case-folded":    {"strs", `{{ strs|sort(case_sensitive=false)|length }}`, 150000},
	"groupby":              {"dicts", `{{ dicts|groupby('k')|length }}`, 100000},
	"dictsort":             {"mapping", `{{ mapping|dictsort|length }}`, 100000},
	"map, by attribute":    {"dicts", `{{ dicts|map(attribute='k')|list|length }}`, 150000},
	"map, by filter":       {"ints", `{{ ints|map('string')|list|length }}`, 250000},
	"select":               {"ints", `{{ ints|select|list|length }}`, 500000},
	"reject":               {"ints", `{{ ints|reject|list|length }}`, 500000},
	"selectattr":           {"dicts", `{{ dicts|selectattr('k')|list|length }}`, 150000},
	"rejectattr":           {"dicts", `{{ dicts|rejectattr('k')|list|length }}`, 150000},
	"join":                 {"ints", `{{ ints|join(',')|length }}`, 500000},
	"sum":                  {"ints", `{{ ints|sum }}`, 500000},
	"list":                 {"ints", `{{ ints|list|length }}`, 500000},
	"reverse":              {"ints", `{{ ints|reverse|list|length }}`, 500000},
	"batch":                {"ints", `{{ ints|batch(3)|list|length }}`, 250000},
	"slice":                {"ints", `{{ ints|slice(3)|list|length }}`, 1000000},
	"items":                {"mapping", `{{ mapping|items|list|length }}`, 100000},
	"random":               {"ints", `{{ ints|random }}`, 500000},
}

// A filter that walks a caller-sized sequence has to stop when the deadline
// passes, not when the walk ends.
//
// The render budget cannot express this on its own. Charging is what bounds
// memory, and a filter that has already materialised its input is not
// allocating any more, so it has nothing left to charge -- yet the comparisons,
// the hashing and the attribute lookups still take time proportional to the
// input. |min and |max yielded between items and |unique did not, and nothing
// failed, because materialising yields often enough to hide a wholly
// uninterruptible filter behind it. 300,000 distinct items held a render 200ms
// past a 39ms deadline.
func TestSequenceFiltersYieldToTheDeadline(t *testing.T) {
	for name, w := range sequenceWorkloads {
		t.Run(name, func(t *testing.T) {
			assertYieldsToDeadline(t, name, w)
		})
	}
}

// deadlineShare is where the deadline falls, as a fraction of the whole render.
const deadlineShare = 8

// overrunShare is how far past the deadline a render may go before the filter
// is judged not to be yielding.
//
// It is loose on purpose. A filter that does not yield runs to the end of its
// work, which showed up at 95% of the whole render and above; one that yields
// stops at an eighth. Anything between is not a close call worth failing a
// build over, and a threshold set close to the deadline would fail on a
// collection pause rather than on a defect. This test errs towards saying
// nothing rather than towards saying something wrong.
const overrunShare = 0.6

// tightBar overrides overrunShare for workloads where both sides have been
// measured and the loose bar cannot tell them apart.
//
// The splits are the case. A filter that never yields runs to 95% of the render
// and beyond, which is what overrunShare is set for; a split that stops yielding
// runs to 39%, because finding the pieces is a small part of the work and
// wrapping them -- which yields either way -- is most of it. Every workload here
// stops at 13% when it is right, so 30% is more than twice the correct answer
// and well under the defect.
//
// Keyed by the workload's own name, passed in rather than read back out of
// the subtest, which has its spaces rewritten as underscores.
var tightBar = map[string]float64{
	"str.split":        0.30,
	"str.rsplit":       0.30,
	"str.split on sep": 0.30,
	"bytes.split":      0.30,
	"bytes.rsplit":     0.30,
}

// deadlineRuns is how many times the deadline is measured, the best standing
// for the filter.
//
// A garbage collection or a descheduled goroutine can only ever make a render
// look slower than the filter is, so the shortest of several runs describes the
// filter rather than the machine. Without this |reverse failed about one run in
// three: its work after materialising is a swap loop, so its whole margin is
// smaller than a collection pause.
const deadlineRuns = 3

// assertYieldsToDeadline times the render, then requires a deadline at an
// eighth of it to stop the render well short of finishing.
func assertYieldsToDeadline(t *testing.T, name string, w workload) {
	t.Helper()
	tmpl, err := gojja2.New().FromString(w.src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	vars := buildSeq(w.kind, w.n)

	start := time.Now()
	if _, err := tmpl.RenderString(context.Background(), vars); err != nil {
		t.Fatalf("uninterrupted render: %v", err)
	}
	natural := time.Since(start)
	if natural < 40*time.Millisecond {
		t.Skipf("the whole render takes %s at %d elements, too short to "+
			"tell stopping early from finishing; raise this "+
			"workload's size", natural.Round(time.Millisecond), w.n)
	}

	t.Logf("%d elements, %s in full", w.n, natural.Round(time.Millisecond))

	deadline := natural / deadlineShare
	stopped := time.Duration(1<<63 - 1)
	for range deadlineRuns {
		ctx, cancel := context.WithTimeout(context.Background(), deadline)
		start = time.Now()
		_, err = tmpl.RenderString(ctx, vars)
		took := time.Since(start)
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("render with a %s deadline: %v, want context.DeadlineExceeded",
				deadline.Round(time.Millisecond), err)
		}
		stopped = min(stopped, took)
	}

	share := overrunShare
	if b, ok := tightBar[name]; ok {
		share = b
	}
	t.Logf("stopped at %.0f%% of the whole render, bar %.0f%%",
		100*float64(stopped)/float64(natural), 100*share)
	if limit := time.Duration(share * float64(natural)); stopped > limit {
		t.Errorf("a %s deadline stopped the render after %s, %.0f%% of the "+
			"%s it takes in full: the filter runs to the end of its "+
			"work whatever the deadline says",
			deadline.Round(time.Millisecond), stopped.Round(time.Millisecond),
			100*float64(stopped)/float64(natural), natural.Round(time.Millisecond))
	}
}

// stringWorkloads make each filter walk a string of the caller's length.
//
// A filter over one long string is the same defect as a filter over a long
// sequence, and it was the more widespread of the two: ten of these ran to the
// end of a 23MB string whatever the deadline said, |pprint by eight times over.
// Nothing about it is exotic -- the string comes from the caller, or from `"x"
// * n`, which the output bound allows up to 256MB of by default.
var stringWorkloads = map[string]workload{
	"upper":       {"text", `{{ text|upper|length }}`, 120000},
	"lower":       {"text", `{{ text|lower|length }}`, 120000},
	"title":       {"text", `{{ text|title|length }}`, 120000},
	"capitalize":  {"text", `{{ text|capitalize|length }}`, 120000},
	"striptags":   {"text", `{{ text|striptags|length }}`, 120000},
	"wordcount":   {"text", `{{ text|wordcount }}`, 300000},
	"wordwrap":    {"text", `{{ text|wordwrap(20)|length }}`, 120000},
	"escape":      {"text", `{{ text|escape|length }}`, 400000},
	"forceescape": {"text", `{{ text|forceescape|length }}`, 400000},
	"tojson":      {"text", `{{ text|tojson|length }}`, 200000},
	"pprint":      {"text", `{{ text|pprint|length }}`, 60000},
	// Markup takes its own repr, which is the whole output, and a list
	// takes the repr of what is inside it: both reach value.Repr with the
	// caller's data and neither goes near the word-splitting that the plain
	// string case does.
	"pprint, markup":    {"text", `{{ text|safe|pprint|length }}`, 200000},
	"pprint, in a list": {"text", `{{ [text]|pprint|length }}`, 100000},
	"urlize":            {"text", `{{ text|urlize|length }}`, 40000},
	"replace":           {"text", `{{ text|replace("o", "0")|length }}`, 2000000},
	"indent":            {"text", `{{ text|indent(2)|length }}`, 400000},
	"urlencode":         {"text", `{{ text|urlencode|length }}`, 200000},
}

// |string, |trim and |truncate are left out on purpose. What each does is
// bounded whatever it is given -- returning the subject, trimming its two ends,
// cutting it at a length the template chose -- so there is no walk to interrupt
// and nothing for a deadline to arrive in the middle of. Copying the result is
// all that is proportional to the input, and that is true of every filter here.

// methodWorkloads make each str or bytes method walk a subject of the caller's
// length.
//
// The methods are the same operations as the filters reached by another name,
// and they were audited later: `{{ s|upper }}` was made interruptible while
// `{{ s.upper() }}` was not, because the method table hands its functions no
// State at all. Six case methods, both splits, translate, expandtabs and decode
// all ran to the end of a 13MB subject whatever the deadline said.
var methodWorkloads = map[string]workload{
	"str.upper":             {"text", `{{ (text.upper()) and 1 or 1 }}`, 100000},
	"str.lower":             {"text", `{{ (text.lower()) and 1 or 1 }}`, 120000},
	"str.casefold":          {"text", `{{ (text.casefold()) and 1 or 1 }}`, 100000},
	"str.title":             {"text", `{{ (text.title()) and 1 or 1 }}`, 100000},
	"str.capitalize":        {"text", `{{ (text.capitalize()) and 1 or 1 }}`, 120000},
	"str.swapcase":          {"text", `{{ (text.swapcase()) and 1 or 1 }}`, 80000},
	"str.translate":         {"text", `{{ (text.translate({})) and 1 or 1 }}`, 50000},
	"str.expandtabs":        {"text", `{{ (text.expandtabs()) and 1 or 1 }}`, 250000},
	"bytes.decode":          {"bytes", `{{ (bytes.decode()) and 1 or 1 }}`, 400000},
	"str.split":             {"text", `{{ (text.split()) and 1 or 1 }}`, 200000},
	"str.rsplit":            {"text", `{{ (text.rsplit()) and 1 or 1 }}`, 200000},
	"str.split on sep":      {"text", `{{ (text.split("o")) and 1 or 1 }}`, 2000000},
	"bytes.split":           {"bytes", `{{ (bytes.split()) and 1 or 1 }}`, 1200000},
	"bytes.rsplit":          {"bytes", `{{ (bytes.rsplit()) and 1 or 1 }}`, 400000},
	"str.split, one field":  {"nospace", `{{ (nospace.split()) and 1 or 1 }}`, 2000000},
	"str.rsplit, one field": {"nospace", `{{ (nospace.rsplit()) and 1 or 1 }}`, 2000000},

	"bytes.expandtabs": {"bytes", `{{ (bytes.expandtabs()) and 1 or 1 }}`, 600000},
}

// A method that walks a caller-sized subject has to stop when the deadline
// passes, not when the subject ends.
func TestStringMethodsYieldToTheDeadline(t *testing.T) {
	for name, w := range methodWorkloads {
		t.Run(name, func(t *testing.T) {
			assertYieldsToDeadline(t, name, w)
		})
	}
}

// A filter that walks a caller-sized string has to stop when the deadline
// passes, not when the string ends.
func TestStringFiltersYieldToTheDeadline(t *testing.T) {
	for name, w := range stringWorkloads {
		t.Run(name, func(t *testing.T) {
			assertYieldsToDeadline(t, name, w)
		})
	}
}

// A filter that rewords "not iterable" must reword only that.
//
// |reverse and |last replace the failure with wording of their own, because
// jinja2's message names reversibility rather than iterability. Replacing every
// failure meant a render that ran out of time while walking a perfectly good
// sequence reported a type error: the deadline arrived disguised as the
// template's mistake, and errors.Is found nothing to match on.
func TestRewordedIterationErrorsKeepTheRealFailure(t *testing.T) {
	for _, tc := range []struct{ name, src, big, want string }{
		{"reverse", `{{ x|reverse|list|length }}`,
			`{{ range(4000000)|reverse|list|length }}`, "argument must be iterable"},
		{"last", `{{ x|last }}`,
			`{{ range(4000000)|last }}`, "is not reversible"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmpl, err := gojja2.New().FromString(tc.src)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}

			// A value that genuinely cannot be walked still gets the
			// filter's own wording.
			_, err = tmpl.RenderString(context.Background(), map[string]any{"x": 5})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("over an int: %v, want a message containing %q", err, tc.want)
			}

			// A sequence that is fine, under a render that is not.
			//
			// It is range() rather than a passed-in slice because a
			// slice is converted before the filter runs, and a
			// cancelled render is refused there instead -- which
			// would exercise the conversion rather than this
			// filter's handling of a refusal.
			big, err := gojja2.New().FromString(tc.big)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, err = big.RenderString(ctx, nil)
			if !errors.Is(err, context.Canceled) {
				t.Errorf("cancelled: %v, want context.Canceled -- the "+
					"filter reworded the refusal as %q", err, tc.want)
			}
		})
	}
}
