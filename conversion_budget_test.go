// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/mgilbir/gojja2"
)

// convHolder reaches a conversion the two lazy ways: through a struct field and
// through a method's result.
type convHolder struct {
	Items []any
	Maps  map[string]any
}

func (h *convHolder) Big() []any { return h.Items }

// convArgs builds one argument of each shape that reaches the converter.
func convArgs(n int) map[string]any {
	anySlice := make([]any, n)
	typed := make([]int, n)
	goMap := make(map[string]any, n)
	// map[string]any has a fast path of its own, so a map of any other type
	// is the only thing that reaches the reflect walk -- and the reflect
	// walk sorts its keys, which converts two of them per comparison.
	typedMap := make(map[string]int, n)
	for i := range anySlice {
		anySlice[i] = i
		typed[i] = i
		goMap[strconv.Itoa(i)] = i
		typedMap[strconv.Itoa(i)] = i
	}
	return map[string]any{
		"anySlice": anySlice,
		"typed":    typed,
		"goMap":    goMap,
		"typedMap": typedMap,
		"nested":   map[string]any{"inner": anySlice},
		"holder":   &convHolder{Items: anySlice, Maps: goMap},
	}
}

// Every expression here is O(1) *after* the conversion. That is the point:
// |length does not iterate, so nothing downstream ever charges or polls, and
// the conversion is the only thing that can notice a spent budget. An
// expression that looped afterwards would pass whether or not conversion was
// charged, and would have proved nothing.
var convCases = map[string]string{
	"render argument, []any":     `{{ anySlice|length }}`,
	"render argument, typed":     `{{ typed|length }}`,
	"render argument, Go map":    `{{ goMap|length }}`,
	"render argument, typed map": `{{ typedMap|length }}`,
	"nested in a dict":           `{{ nested.inner|length }}`,
	"struct field":               `{{ holder.Items|length }}`,
	"struct field, map":          `{{ holder.Maps|length }}`,
	"method result":              `{{ holder.Big()|length }}`,
	"include with context":       `{% include "inner" with context %}`,
	"argument used twice":        `{{ anySlice|length }}{{ anySlice|length }}`,
}

func convEnv(opts ...gojja2.Option) *gojja2.Environment {
	opts = append(opts, gojja2.WithLoader(gojja2.DictLoader(map[string]string{
		"inner": `{{ anySlice|length }}`,
	})))
	return mustEnv(opts...)
}

// Converting a render argument is work proportional to the argument, and it
// happens before the template does anything a bound would notice. It was
// neither charged nor interruptible, so a cancelled context did not stop it --
// and because these expressions never iterate, nothing after the conversion
// consulted the context either. The render ran for about a second over a
// million-element argument and then reported *success*.
func TestConversionStopsWhenCancelled(t *testing.T) {
	args := convArgs(200_000)
	for name, src := range convCases {
		t.Run(name, func(t *testing.T) {
			tmpl, err := convEnv().FromString(src)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel() // dead before the render starts
			out, err := tmpl.RenderString(ctx, args)
			if err == nil {
				t.Fatalf("render returned success with %q against a "+
					"cancelled context", out)
			}
			if !errors.Is(err, context.Canceled) {
				t.Errorf("error = %v, want context.Canceled", err)
			}
		})
	}
}

// The same walk has to be counted, not just interruptible. A caller who set a
// bound of a few thousand units got a conversion of any size for free, so the
// bound described the template's loops and nothing else.
func TestConversionIsCharged(t *testing.T) {
	args := convArgs(50_000)
	for name, src := range convCases {
		t.Run(name, func(t *testing.T) {
			tmpl, err := convEnv(gojja2.WithMaxIterations(1000)).FromString(src)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			out, err := tmpl.RenderString(context.Background(), args)
			if err == nil {
				t.Fatalf("render returned success with %q, though converting "+
					"the argument is 50,000 units against a bound of 1,000", out)
			}
			if !errors.Is(err, gojja2.ErrTooManyIterations) {
				t.Errorf("error = %v, want ErrTooManyIterations", err)
			}
		})
	}
}

// Stopping is not enough on its own: a conversion that noticed the context only
// at the start of each container would satisfy the two tests above and still
// hold the render for as long as the argument is large, because the deadline
// that matters expires *during* the walk rather than before it.
//
// So this one times the render, gives the same render a deadline at a tenth of
// that, and requires it to stop well short of the whole walk. Both halves are
// measured on the machine running them, so a slow builder moves the threshold
// with it rather than tripping over a constant.
//
// The elements are dicts rather than integers so that the walk takes long
// enough to measure. At a few milliseconds the timer and the scheduler are
// most of what is being timed, and the test says more about them than about
// the conversion.
func TestConversionStopsPromptly(t *testing.T) {
	const n = 200_000
	items := make([]any, n)
	for i := range items {
		items[i] = map[string]any{"k": i}
	}
	args := map[string]any{"anySlice": items}

	tmpl, err := convEnv().FromString(`{{ anySlice|length }}`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	start := time.Now()
	if _, err := tmpl.RenderString(context.Background(), args); err != nil {
		t.Fatalf("uninterrupted render: %v", err)
	}
	natural := time.Since(start)
	if natural < 50*time.Millisecond {
		t.Skipf("the conversion takes %s here, which is too short to "+
			"distinguish stopping early from finishing", natural)
	}

	// A second render converts afresh: the memo lives in the render's own
	// scope, so this is the same work again.
	deadline := natural / 10
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	start = time.Now()
	if _, err := tmpl.RenderString(ctx, args); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("render with a deadline: %v, want context.DeadlineExceeded", err)
	}
	stopped := time.Since(start)

	// Half the natural time sits well above the deadline plus one check
	// interval, and well below finishing the walk, which is what the defect
	// did -- it ran to the end and then reported success.
	if limit := natural / 2; stopped > limit {
		t.Errorf("a render with a %s deadline ran %s, which is %.0f%% of "+
			"the %s the whole conversion takes; the walk is not "+
			"yielding between elements",
			deadline.Round(time.Millisecond), stopped.Round(time.Millisecond),
			100*float64(stopped)/float64(natural), natural.Round(time.Millisecond))
	}
}

// A refused conversion must not reach the template as a short value.
//
// The walk returns what it had built when it was refused, which is a list of
// the wrong length. Nothing may read it: the render fails either way, because
// the budget remembers the refusal, but failing is not the same as first
// printing a length that is not the argument's. Render streams, so anything
// written before the failure is already on its way to the caller.
func TestRefusedConversionYieldsNoValue(t *testing.T) {
	args := convArgs(200_000)
	for name, src := range convCases {
		t.Run(name, func(t *testing.T) {
			tmpl, err := convEnv().FromString(src)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			var buf bytes.Buffer
			if err := tmpl.Render(ctx, &buf, args); err == nil {
				t.Fatalf("render succeeded with %q", buf.String())
			}
			if got := buf.String(); got != "" {
				t.Errorf("a refused render wrote %q; the conversion was "+
					"cut short, so any length it reports is wrong", got)
			}
		})
	}
}

// The budget crosses into a nested render, so an include cannot convert the
// context it was handed on a fresh allowance.
func TestConversionBudgetIsSharedWithIncludes(t *testing.T) {
	env := mustEnv(
		gojja2.WithMaxIterations(1000),
		gojja2.WithLoader(gojja2.DictLoader(map[string]string{
			"inner": `{{ anySlice|length }}`,
		})),
	)
	tmpl, err := env.FromString(`{% include "inner" with context %}`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	out, err := tmpl.RenderString(context.Background(), convArgs(50_000))
	if err == nil {
		t.Fatalf("the include converted %q on an allowance of its own", out)
	}
	if !errors.Is(err, gojja2.ErrTooManyIterations) {
		t.Errorf("error = %v, want ErrTooManyIterations", err)
	}
}

// A conversion small enough to fit the bound still has to work, and has to be
// charged only once however often the name is mentioned -- the memoisation that
// makes the second reference free must not be undone by charging it again.
func TestConversionWithinBudgetStillRenders(t *testing.T) {
	args := convArgs(100)
	for name, src := range convCases {
		t.Run(name, func(t *testing.T) {
			tmpl, err := convEnv(gojja2.WithMaxIterations(5000)).FromString(src)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			got, err := tmpl.RenderString(context.Background(), args)
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			if want := fmt.Sprint(100); len(got) == 0 || got == "0" {
				t.Errorf("render = %q, want it to report %s elements", got, want)
			}
		})
	}
}
