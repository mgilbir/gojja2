// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package value_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/gojja2/value"
)

// Repeating an empty sequence costs nothing, however large the count.
//
// It used to cost the count. `"" * n` is the empty string for every n, but the
// loop that built it ran n times copying nothing: seventeen seconds for 1e10,
// and for the counts a template can actually write, longer than the machine
// will be switched on. Nothing stopped it -- the budget is charged the size of
// the result, which is zero, and the loop consults no context -- and because
// the expression is constant it ran inside FromString, where the caller's
// deadline does not apply at all.
//
// The counts here are chosen so that the old code could not have passed this
// test by being fast: at a billion iterations a second, 2**62 takes 146 years.
func TestRepeatingNothingIsFree(t *testing.T) {
	for _, tc := range []struct {
		name string
		unit value.Value
		want string
	}{
		{"string", value.String(""), "''"},
		{"markup", value.Safe(""), "Markup('')"},
		{"bytes", value.Bytes(nil), `b''`},
		{"list", value.NewList(), "[]"},
		{"tuple", value.NewTuple(), "()"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			done := make(chan struct{})
			var got value.Value
			var err error
			go func() {
				defer close(done)
				got, err = value.Mul(tc.unit, value.Int(1<<62), nil)
			}()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("repeating an empty sequence 2**62 times did not " +
					"finish; it is counting the repetitions rather than " +
					"the result")
			}
			if err != nil {
				t.Fatalf("multiply: %v", err)
			}
			if r := value.Repr(got); r != tc.want {
				t.Errorf("got %s, want %s", r, tc.want)
			}
		})
	}
}

// The counts that do produce something still produce exactly what they did.
func TestRepeatingSomethingIsUnchanged(t *testing.T) {
	for _, tc := range []struct {
		unit value.Value
		n    int64
		want string
	}{
		{value.String("ab"), 3, "'ababab'"},
		{value.String("ab"), 1, "'ab'"},
		{value.String("ab"), 0, "''"},
		{value.String("ab"), -2, "''"},
		{value.Bytes([]byte("hi")), 2, `b'hihi'`},
		{value.NewList(value.Int(1), value.Int(2)), 2, "[1, 2, 1, 2]"},
		{value.NewList(value.Int(1)), 0, "[]"},
		{value.NewTuple(value.Int(1)), 3, "(1, 1, 1)"},
		{value.NewTuple(value.Int(1)), -1, "()"},
	} {
		got, err := value.Mul(tc.unit, value.Int(tc.n), nil)
		if err != nil {
			t.Errorf("%s * %d: %v", value.Repr(tc.unit), tc.n, err)
			continue
		}
		if r := value.Repr(got); r != tc.want {
			t.Errorf("%s * %d = %s, want %s", value.Repr(tc.unit), tc.n, r, tc.want)
		}
	}
}

// Markup survives repetition, and an empty one must not lose its safety on the
// way through the early return.
func TestRepeatingKeepsMarkup(t *testing.T) {
	for _, tc := range []struct {
		unit value.Value
		n    int64
	}{
		{value.Safe("<b>"), 2},
		{value.Safe(""), 1 << 40},
	} {
		got, err := value.Mul(tc.unit, value.Int(tc.n), nil)
		if err != nil {
			t.Fatalf("multiply: %v", err)
		}
		if !got.IsSafe() {
			t.Errorf("repeating Markup %d times gave a plain string", tc.n)
		}
	}
}

// Building a repr is charged and can be stopped.
//
// A repr is as long as the value it describes, so it is work set by the
// caller's data. It used to be one uninterruptible pass, and for a Markup
// string it is the whole of what |pprint outputs: 90% of that filter's time
// with a deadline an eighth of it, and the deadline arrived after the work
// rather than during.
func TestReprIsChargedAndCanStop(t *testing.T) {
	long := value.String(strings.Repeat("xyz ", 200_000))
	deep := value.NewList(long, long, long)

	// A list of integers is the case the per-element charge exists for:
	// writing an integer charges nothing of its own, so without it a
	// million-element list is one uninterruptible walk.
	ints := make([]value.Value, 200_000)
	for i := range ints {
		ints[i] = value.Int(int64(i))
	}

	for name, v := range map[string]value.Value{
		"string":       long,
		"markup":       value.Safe(strings.Repeat("xyz ", 200_000)),
		"list":         deep,
		"list of ints": value.NewList(ints...),
	} {
		t.Run(name, func(t *testing.T) {
			b := &countRepr{}
			if _, err := value.ReprBudget(v, b); err != nil {
				t.Fatalf("repr: %v", err)
			}
			// Bytes for the text of a repr, items for the elements
			// of a container: which one depends on the value, and
			// what matters is that something was charged.
			if b.calls == 0 {
				t.Error("building the repr charged nothing")
			}

			// A budget that refuses stops the walk rather than
			// reporting the refusal once it has finished.
			r := &countRepr{stopAfter: 1}
			if _, err := value.ReprBudget(v, r); !errors.Is(err, errStopRepr) {
				t.Fatalf("repr = %v, want the budget's refusal", err)
			}
			if r.calls > 8 {
				t.Errorf("the walk charged %d times after being refused "+
					"on the first; it built the whole repr and "+
					"reported the refusal at the end", r.calls)
			}
		})
	}

	// Without a budget it is what it always was.
	if got := value.Repr(value.String("hi")); got != "'hi'" {
		t.Errorf("Repr = %s, want 'hi'", got)
	}
	if got, err := value.ReprBudget(value.String("hi"), nil); err != nil || got != "'hi'" {
		t.Errorf("ReprBudget with no budget = %s, %v; want 'hi'", got, err)
	}
}

var errStopRepr = errors.New("stop repr")

type countRepr struct {
	bytes     int64
	calls     int
	stopAfter int
}

func (c *countRepr) ChargeBytes(n int64) error {
	c.bytes += n
	c.calls++
	if c.stopAfter > 0 && c.calls >= c.stopAfter {
		return errStopRepr
	}
	return nil
}

func (c *countRepr) ChargeItems(n int64) error {
	c.calls++
	if c.stopAfter > 0 && c.calls >= c.stopAfter {
		return errStopRepr
	}
	return nil
}
