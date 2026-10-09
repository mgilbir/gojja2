// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/mgilbir/gojja2/value"
)

// A render converts the records inside its arguments lazily (value.FromGoLazy):
// a map[string]any becomes a dict that is filled from the map only when
// something needs more than its length or a scalar field. These pin what that
// must not change.

// One Go map is one value however it is reached, before and after the dict is
// filled, and a change made through one reference is seen through the others
// -- but never by the caller.
func TestLazyRecordsKeepIdentity(t *testing.T) {
	newArgs := func() (map[string]any, map[string]any) {
		shared := map[string]any{"x": 1}
		cyc := map[string]any{"k": "v"}
		cyc["self"] = cyc
		return shared, map[string]any{
			"pair":  []any{shared, shared},
			"outer": map[string]any{"a": shared, "b": []any{shared}},
			"cyc":   cyc,
		}
	}
	cases := []struct{ name, src, want string }{
		{"a list holding one map twice", `{{ pair[0] is sameas pair[1] }}`, "True"},
		{"one map in a dict and in a list inside it", `{{ outer.a is sameas outer.b[0] }}`, "True"},
		{"a map that holds itself", `{{ cyc.self is sameas cyc }}{{ cyc.self.self.k }}`, "Truev"},
		// Read a scalar first, so the dict is still unfilled when the
		// other reference is taken; then mutate through one and read
		// through the other.
		{"a write through one reference", `{{ pair[1].x }}{% set _ = pair[0].update({'x': 2, 'z': 3}) %}{{ pair[1].x }}{{ pair[1].z }}`, "123"},
		{"a write before the other side is filled", `{% set _ = outer.a.update({'z': 9}) %}{{ outer.b[0].z }}`, "9"},
		{"a field set on an unfilled record", `{% set r = pair[0] %}{% set _ = r.update({'x': 5}) %}{{ r.x }}{{ pair[1]|dictsort }}`, "5[('x', 5)]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			shared, args := newArgs()
			got, err := renderVars(t, mustNew(), tc.src, args)
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
			if !reflect.DeepEqual(shared, map[string]any{"x": 1}) {
				t.Errorf("the render wrote to the caller's map: %v", shared)
			}
		})
	}
}

// What a template never fills is never built, so it is never charged: ten
// records each holding a thousand tags fit a bound of a thousand units when
// the template reads only the records' ids, and do not when it reads a tag.
// This is the observable difference between the render path and a full
// conversion, and what says the render path is the lazy one.
func TestRenderChargesOnlyWhatItFills(t *testing.T) {
	args := func() map[string]any {
		rows := make([]any, 10)
		for i := range rows {
			rows[i] = map[string]any{"id": i, "tags": make([]any, 1000)}
		}
		return map[string]any{"rows": rows}
	}
	env := mustNew(WithMaxIterations(1000))
	got, err := renderVars(t, env, `{{ rows|length }}:{{ rows[3].id }}`, args())
	if err != nil {
		t.Fatalf("reading ids: %v", err)
	}
	if got != "10:3" {
		t.Errorf("got %q, want 10:3", got)
	}
	if _, err := renderVars(t, env, `{% for r in rows %}{{ r.tags|length }}{% endfor %}`,
		args()); !errors.Is(err, ErrTooManyIterations) {
		t.Errorf("reading every record's tags: %v, want ErrTooManyIterations", err)
	}
}

// A NaN is the one scalar whose identity a template can see, so reading it
// twice out of one record has to give one object -- which a read answered from
// the Go map without filling the dict would not.
func TestLazyRecordsKeepANaNsIdentity(t *testing.T) {
	args := map[string]any{"rows": []any{map[string]any{"n": math.NaN(), "s": "x"}}}
	got, err := renderVars(t, mustNew(),
		`{{ rows[0].n is sameas rows[0].n }}{{ rows[0]['n'] is sameas rows[0].n }}`, args)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if got != "TrueTrue" {
		t.Errorf("got %q, want TrueTrue", got)
	}
}

// Filling a record is charged to the render, and a refusal there has nowhere
// to go but the budget. The template can trip over the record it left short
// before the render ends -- here `tags` is empty, so `tags[0]` is undefined and
// calling a method on it raises -- and the render must still report the
// refusal, not the UndefinedError it caused.
func TestARefusedFillReportsTheRefusal(t *testing.T) {
	users := make([]any, 10)
	for i := range users {
		tags := make([]any, 1000)
		for j := range tags {
			tags[j] = "t" + strconv.Itoa(j)
		}
		users[i] = map[string]any{"name": "u", "tags": tags}
	}
	env := mustNew(WithMaxIterations(500))
	// Converting `users` charges its ten records and their two keys, which
	// fits; filling a record converts its thousand tags, which does not.
	got, err := renderVars(t, env,
		`{% for u in users %}{{ u.tags[0].upper() }}{% endfor %}`,
		map[string]any{"users": users})
	if err == nil {
		t.Fatalf("render succeeded with %q", got)
	}
	if !errors.Is(err, ErrTooManyIterations) {
		t.Errorf("error = %v, want ErrTooManyIterations", err)
	}
}

// Filling a record is a walk over its fields, and a deadline has to be able to
// stop it as it stops the conversion of the argument around it.
func TestALazyFillStopsPromptly(t *testing.T) {
	const n = 200_000
	big := make(map[string]any, n)
	for i := range n {
		// Scalars, so that nothing inside a field charges: a field
		// that did would poll on its own and hide a fill that never
		// yields.
		big[strconv.Itoa(i)] = i
	}
	// The argument converts in O(1) -- it is one map, charged by its length
	// -- and `|list` fills it, which is the walk being timed.
	args := map[string]any{"big": big}
	tmpl, err := mustNew().FromString(`{{ big|list|length }}`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	start := time.Now()
	out, err := tmpl.RenderString(context.Background(), args)
	if err != nil {
		t.Fatalf("uninterrupted render: %v", err)
	}
	if out != strconv.Itoa(n) {
		t.Fatalf("render = %q, want %d", out, n)
	}
	natural := time.Since(start)
	if natural < 50*time.Millisecond {
		t.Skipf("filling the map takes %s here, which is too short to "+
			"distinguish stopping early from finishing", natural)
	}
	ctx, cancel := context.WithTimeout(context.Background(), natural/10)
	defer cancel()
	start = time.Now()
	if _, err := tmpl.RenderString(ctx, args); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("render with a deadline: %v, want context.DeadlineExceeded", err)
	}
	if stopped := time.Since(start); stopped > natural/2 {
		t.Errorf("a render with a %s deadline ran %s of the %s the fill takes; "+
			"the fill is not yielding between fields",
			(natural / 10).Round(time.Millisecond), stopped.Round(time.Millisecond),
			natural.Round(time.Millisecond))
	}
}

// An expression's result is handed to the host, where nothing bounds it any
// more, so it must come back converted in full: a lazy record filled after Eval
// returned would charge and poll a budget whose context the caller may already
// have cancelled, and come back short without an error.
func TestAnExpressionResultIsFullyConverted(t *testing.T) {
	const n = 3 * checkInterval
	rec := make(map[string]any, n)
	for i := range n {
		rec[strconv.Itoa(i)] = []any{i}
	}
	expr, err := mustNew().CompileExpression(`rec`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	v, err := expr.Eval(ctx, map[string]any{"rec": rec})
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	cancel()
	d, ok := v.Dict()
	if !ok {
		t.Fatalf("result = %s, want a dict", value.Repr(v))
	}
	if got := len(d.Entries()); got != n {
		t.Errorf("the result has %d entries once read, want %d", got, n)
	}
}

// A record read only by its scalar fields renders the same whether or not it
// was ever filled, for every scalar width fromAny has an arm for.
func TestLazyScalarReadsMatchAFilledRecord(t *testing.T) {
	rec := map[string]any{
		"none": nil, "s": "str", "t": true, "f": false,
		"i": 1, "i8": int8(-8), "i16": int16(16), "i32": int32(-32), "i64": int64(1) << 62,
		"u": uint(7), "u8": uint8(8), "u64": uint64(math.MaxUint64), "up": uintptr(9),
		"f32": float32(1.5), "f64": 2.25, "inf": math.Inf(-1), "negz": math.Copysign(0, -1),
		"b": []byte("by"), "when": time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		"v": value.String("val"),
	}
	src := ""
	for k := range rec {
		src += fmt.Sprintf("{{ r.%s|pprint }}|{{ r[%q]|pprint }};", k, k)
	}
	// The same fields once the dict has been filled -- `r|length` does not
	// fill it and `r.items()` does.
	filled := `{% set _ = r.items()|list %}` + src
	args := func() map[string]any { return map[string]any{"r": rec} }
	lazy, err := renderVars(t, mustNew(), src, args())
	if err != nil {
		t.Fatalf("lazy render: %v", err)
	}
	full, err := renderVars(t, mustNew(), filled, args())
	if err != nil {
		t.Fatalf("filled render: %v", err)
	}
	if lazy != full {
		t.Errorf("reading an unfilled record:\n  %s\nreading a filled one:\n  %s", lazy, full)
	}
}
