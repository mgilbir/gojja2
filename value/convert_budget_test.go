// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package value_test

import (
	"errors"
	"strconv"
	"testing"

	"github.com/mgilbir/gojja2/value"
)

// countingBudget records what a conversion charges and how often it yields,
// and can refuse after a set number of either.
//
// A render's budget only consults the context every few thousand units, so from
// outside the engine a walk that yields per element and one that yields once
// per container look alike until the argument is large enough for the
// difference to show up as milliseconds. Counting here says it exactly.
type countingBudget struct {
	items  int64
	bytes  int64
	polls  int
	stopAt int // refuse the nth poll; zero never refuses
}

var errStop = errors.New("stop")

func (b *countingBudget) ChargeBytes(n int64) error { b.bytes += n; return nil }
func (b *countingBudget) ChargeItems(n int64) error { b.items += n; return nil }

func (b *countingBudget) Poll() error {
	b.polls++
	if b.stopAt > 0 && b.polls >= b.stopAt {
		return errStop
	}
	return nil
}

// Every element of a container is charged once, and the walk yields once per
// element.
//
// The charge is what makes the bound real; the yield is what makes the walk
// interruptible. They are separate properties and were separately missing: a
// container charged for its whole length up front would satisfy the first and
// still run to the end of a million-element argument without looking at the
// clock once.
func TestConversionChargesAndYieldsPerElement(t *testing.T) {
	const n = 1000
	ints := make([]any, n)
	strs := make([]string, n)
	anyMap := make(map[string]any, n)
	typedMap := make(map[string]int, n)
	for i := range ints {
		ints[i] = i
		strs[i] = strconv.Itoa(i)
		anyMap[strconv.Itoa(i)] = i
		typedMap[strconv.Itoa(i)] = i
	}

	for name, arg := range map[string]any{
		"[]any":          ints,
		"[]string":       strs,
		"map[string]any": anyMap,
		"map[string]int": typedMap,
	} {
		t.Run(name, func(t *testing.T) {
			b := &countingBudget{}
			if _, err := value.FromGoBudget(arg, nil, b); err != nil {
				t.Fatalf("convert: %v", err)
			}
			if b.items != n {
				t.Errorf("charged %d elements, want %d", b.items, n)
			}
			if b.polls < n {
				t.Errorf("yielded %d times for %d elements; the walk has "+
					"stretches no deadline can interrupt", b.polls, n)
			}
		})
	}
}

// A refused walk stops where it was refused, rather than finishing and
// reporting the refusal afterwards.
func TestConversionStopsAtTheRefusal(t *testing.T) {
	const n = 100_000
	items := make([]any, n)
	for i := range items {
		items[i] = i
	}
	b := &countingBudget{stopAt: 10}
	if _, err := value.FromGoBudget(items, nil, b); !errors.Is(err, errStop) {
		t.Fatalf("convert = %v, want the budget's refusal", err)
	}
	// Ten polls in, not a hundred thousand.
	if b.polls > 100 {
		t.Errorf("the walk polled %d times after being refused on the 10th; "+
			"it converted the argument in full and reported the refusal "+
			"only at the end", b.polls)
	}
}

// Charging happens before the memory is committed, so a budget that refuses is
// refusing an allocation that has not been made yet.
func TestConversionChargesBeforeAllocating(t *testing.T) {
	const n = 50_000
	items := make([]any, n)
	for i := range items {
		items[i] = i
	}
	b := &refuseItems{}
	if _, err := value.FromGoBudget(items, nil, b); !errors.Is(err, errStop) {
		t.Fatalf("convert = %v, want the budget's refusal", err)
	}
	if b.asked != n {
		t.Errorf("the budget was asked for %d elements, want the whole %d "+
			"before any of them were built", b.asked, n)
	}
}

// refuseItems refuses the first element charge it is given.
type refuseItems struct{ asked int64 }

func (b *refuseItems) ChargeBytes(int64) error { return nil }
func (b *refuseItems) ChargeItems(n int64) error {
	b.asked = n
	return errStop
}

// A budget that cannot be polled is still a valid budget: Poller is optional,
// and the walk has to charge and complete without it.
func TestConversionWithoutAPoller(t *testing.T) {
	items := []any{1, 2, 3}
	b := &chargeOnly{}
	v, err := value.FromGoBudget(items, nil, b)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if got := value.Repr(v); got != "[1, 2, 3]" {
		t.Errorf("converted to %s, want [1, 2, 3]", got)
	}
	if b.items != 3 {
		t.Errorf("charged %d elements, want 3", b.items)
	}
}

type chargeOnly struct{ items int64 }

func (b *chargeOnly) ChargeBytes(int64) error   { return nil }
func (b *chargeOnly) ChargeItems(n int64) error { b.items += n; return nil }

// A nil budget converts as it always did.
func TestConversionWithoutABudget(t *testing.T) {
	v, err := value.FromGoBudget([]any{1, 2}, nil, nil)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if got := value.Repr(v); got != "[1, 2]" {
		t.Errorf("converted to %s, want [1, 2]", got)
	}
}
