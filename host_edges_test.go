// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/gojja2/errs"
	"github.com/mgilbir/gojja2/value"
)

// adder is a host type with a method that takes an argument, which is only
// reachable under a widened method policy.
type adder struct{ Base int }

func (a adder) Add(n int) int      { return a.Base + n }
func (a adder) Byte(n uint8) uint8 { return n }
func (a adder) Scale(x float32) string {
	return strings.TrimSpace(strconv.FormatFloat(float64(x), 'g', -1, 32))
}
func (a adder) Half(x float64) float64 { return x / 2 }

// TestHostMethodArgumentsAreChecked: under AllMethods a template chooses the
// arguments a host method is called with, so a wrong count or a wrong type has
// to be a TypeError naming the method rather than a reflect panic. Go has no
// CPython counterpart to compare with; the wording is gojja2's own.
func TestHostMethodArgumentsAreChecked(t *testing.T) {
	env := mustNew(WithMethodPolicy(value.AllMethods))
	vars := map[string]any{"a": adder{Base: 10}}
	// A template's integer is an int64 and its float a float64; a method that
	// takes any other numeric type is still callable when nothing is lost.
	for _, tc := range []struct{ src, want string }{
		{`{{ a.Add(5) }}`, "15"},
		{`{{ a.Byte(255) }}`, "255"},
		{`{{ a.Scale(1.5) }}`, "1.5"},
		{`{{ a.Scale(2) }}`, "2"},
		{`{{ a.Half(3) }}`, "1.5"},
	} {
		if got := mustRenderVars(t, env, tc.src, vars); got != tc.want {
			t.Errorf("%s = %q, want %q", tc.src, got, tc.want)
		}
	}
	for _, tc := range []struct{ src, want string }{
		{`{{ a.Add() }}`, "Add() takes 1 arguments, got 0"},
		{`{{ a.Add(1, 2) }}`, "Add() takes 1 arguments, got 2"},
		{`{{ a.Add(n=1) }}`, "Add() takes 1 arguments, got 0"},
		{`{{ a.Add("x") }}`, "Add(): argument 1 must be int, not str"},
		{`{{ a.Add(none) }}`, "Add(): argument 1 must be int, not NoneType"},
		{`{{ a.Add(true) }}`, "Add(): argument 1 must be int, not bool"},
		{`{{ a.Add(1.5) }}`, "Add(): argument 1 must be int, not float"},
		{`{{ a.Byte(300) }}`, "Byte(): argument 1 does not fit in uint8"},
		{`{{ a.Byte(-1) }}`, "Byte(): argument 1 does not fit in uint8"},
		{`{{ a.Add(2 ** 70) }}`, "Add(): argument 1 does not fit in int"},
	} {
		_, err := renderVars(t, env, tc.src, vars)
		if !errors.Is(err, errs.TypeError) {
			t.Errorf("%s: err = %v, want a TypeError", tc.src, err)
			continue
		}
		if got := err.Error(); got != tc.want {
			t.Errorf("%s\n  got  %q\n  want %q", tc.src, got, tc.want)
		}
	}
}

// TestStructIsNotSubscriptedByAnythingButAName: a struct's fields are
// attributes, and `obj['Name']` reads one the way jinja2's getitem falls back
// to getattr. A key that is not a name has no field to be, and answers as any
// missing item does.
func TestStructIsNotSubscriptedByAnythingButAName(t *testing.T) {
	vars := map[string]any{"a": &ptrAccount{Name: "x"}}
	for _, tc := range []struct{ src, want string }{
		{`{{ a['Name'] }}|{{ a.Name }}`, "x|x"},
		{`[{{ a[0] }}]|{{ a[0] is defined }}`, "[]|False"},
		{`[{{ a[none] }}]`, "[]"},
	} {
		if got := mustRenderVars(t, mustNew(), tc.src, vars); got != tc.want {
			t.Errorf("%s = %q, want %q", tc.src, got, tc.want)
		}
	}
}

// TestTimeZoneNamesThatAreOffsets: a zone Go names by its offset -- "+0530" --
// has no name Python would hold, so the repr drops it rather than quoting it.
func TestTimeZoneNamesThatAreOffsets(t *testing.T) {
	for _, tc := range []struct {
		name string
		zone *time.Location
		want string
	}{
		{"+0530", time.FixedZone("+0530", 5*3600+1800), "datetime.timezone(datetime.timedelta(seconds=19800))"},
		{"-05", time.FixedZone("-05", -5*3600), "datetime.timezone(datetime.timedelta(days=-1, seconds=68400))"},
	} {
		at := time.Date(2026, 9, 19, 13, 45, 30, 0, tc.zone)
		got := mustRenderVars(t, mustNew(), `{{ [t] }}`, map[string]any{"t": at})
		if !strings.Contains(got, "tzinfo="+tc.want+")") {
			t.Errorf("zone %q rendered %s, want tzinfo=%s", tc.name, got, tc.want)
		}
	}
}

// hostSeq is a host type that presents itself as a sequence and takes slices
// no further than the generic rule; hostSlicer answers a slice itself.
type hostSeq []int

func (hostSeq) GetAttr(string) (value.Value, bool) { return value.Undefined, false }
func (h hostSeq) Len() int                         { return len(h) }
func (h hostSeq) GetIndex(i int) (value.Value, bool) {
	if i < 0 || i >= len(h) {
		return value.Undefined, false
	}
	return value.Int(int64(h[i])), true
}

type hostSlicer struct{ hostSeq }

func (h hostSlicer) Slice(start, stop, step *int) (value.Value, error) {
	get := func(p *int) string {
		if p == nil {
			return "-"
		}
		return strconv.Itoa(*p)
	}
	return value.String("slice(" + get(start) + "," + get(stop) + "," + get(step) + ")"), nil
}

// TestHostSequencesAreSliced: an object the host presents as a Sequence slices
// like a list, by the generic rule, and one that also answers Slice is asked
// instead -- with its bounds as ints, absent ones as nil.
func TestHostSequencesAreSliced(t *testing.T) {
	env := mustNew()
	vars := map[string]any{
		"s": value.FromObject(hostSeq{10, 20, 30, 40}),
		"c": value.FromObject(hostSlicer{hostSeq{1, 2, 3}}),
	}
	for _, tc := range []struct{ src, want string }{
		{`{{ s[1:3] }}|{{ s[::-1] }}|{{ s[:0] }}|{{ s[::2] }}`, "[20, 30]|[40, 30, 20, 10]|[]|[10, 30]"},
		{`{{ c[1:] }}|{{ c[:2:-1] }}|{{ c[::] }}`, "slice(1,-,-)|slice(-,2,-1)|slice(-,-,-)"},
	} {
		if got := mustRenderVars(t, env, tc.src, vars); got != tc.want {
			t.Errorf("%s\n  got  %q\n  want %q", tc.src, got, tc.want)
		}
	}
	// A step of zero is refused by the generic rule, and a bound that is not an
	// integer by the bounds -- neither reaches the host.
	for _, src := range []string{`{{ s[::0] }}`, `{{ s['a':] }}`, `{{ c['a':] }}`, `{{ c[::0] }}`} {
		if out, err := renderVars(t, env, src, vars); err == nil && strings.Contains(out, "slice(") {
			t.Errorf("%s reached the host with %q", src, out)
		}
	}
}
