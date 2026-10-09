// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package value

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"
)

// renderPoller is a budget that polls the way a render's does: it reads its
// "context" once every checkEvery units of work, not on every poll, and that
// context says stop from the read numbered stopAt (0 never says so).
//
// The polling has to be modelled, not assumed. An earlier version of these
// tests counted every Poll as a read of the context, which a render's is not,
// and passed a sort whose yields counted as one unit each -- a sort no render
// could ever stop inside. The Windows race run is what showed it.
type renderPoller struct {
	since, reads, stopAt int
}

const checkEvery = 4096

func (p *renderPoller) ChargeBytes(int64) error { return nil }
func (p *renderPoller) ChargeItems(int64) error { return nil }
func (p *renderPoller) Poll() error             { return p.PollWork(1) }
func (p *renderPoller) PollWork(n int) error {
	p.since += n
	if p.since < checkEvery {
		return nil
	}
	p.since = 0
	p.reads++
	if p.stopAt > 0 && p.reads >= p.stopAt {
		return errors.New("stop")
	}
	return nil
}

// TestSortKeysSorts holds sortKeys to slices.Sort at every size that changes
// what it does: one run, a run exactly, a run and one more, an odd number of
// runs, and many.
func TestSortKeysSorts(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for _, n := range []int{0, 1, 2, sortRun - 1, sortRun, sortRun + 1, 2 * sortRun, 3*sortRun + 7, 5*sortRun - 1, 200_000} {
		keys := make([]string, n)
		for i := range keys {
			keys[i] = fmt.Sprintf("k%x", r.Uint64())
		}
		want := slices.Clone(keys)
		slices.Sort(want)
		got := (&converter{b: &renderPoller{}}).sortKeys(keys)
		if !slices.Equal(got, want) {
			t.Errorf("n=%d: not sorted, or not the same keys", n)
		}
	}
}

// TestSortKeysYields: a large sort gives the render a chance to stop -- a real
// read of its context -- after every run it sorts or merges, and stops when
// told to.
func TestSortKeysYields(t *testing.T) {
	keys := make([]string, 200_000)
	for i := range keys {
		keys[i] = fmt.Sprint(len(keys) - i)
	}
	p := &renderPoller{}
	(&converter{b: p}).sortKeys(slices.Clone(keys))
	if runs := len(keys) / sortRun; p.reads < runs*2 {
		t.Errorf("sorting %d keys gave the render %d chances to stop; want at least %d, one per run sorted and merged",
			len(keys), p.reads, runs*2)
	}
	stopped := &renderPoller{stopAt: 3}
	if got := (&converter{b: stopped}).sortKeys(slices.Clone(keys)); got != nil {
		t.Errorf("told to stop at the third read, the sort finished")
	}
	if stopped.reads != 3 {
		t.Errorf("read the context %d times after being told to stop at 3", stopped.reads)
	}
}

// TestFillYieldsBeforeSortingEverything: a fill told to stop at its first
// chance stops having gathered one run of keys, not all of them. Before the
// sort yielded, a fill collected and sorted every key of the map before its
// first chance to stop, which on a slow machine was most of a deadline -- the
// Windows race run's overrun.
func TestFillYieldsBeforeSortingEverything(t *testing.T) {
	m := make(map[string]any, 200_000)
	for i := range 200_000 {
		m[fmt.Sprint(i)] = i
	}
	c := &converter{b: &renderPoller{stopAt: 1}}
	d := &Dict{}
	c.fillEntries(d, m)
	if c.err == nil {
		t.Fatal("the fill finished although told to stop at its first chance")
	}
	if n := len(c.keys); n > sortRun {
		t.Errorf("it gathered %d keys before its first chance to stop; want at most %d", n, sortRun)
	}
	if len(d.entries) != 0 {
		t.Errorf("it filled %d entries", len(d.entries))
	}
}

// TestFillYieldsInsideTheSort stops the fill at its first chance after
// gathering the keys, which is inside the sort: the keys are then sorted only
// as far as one run. A sort with no yield in it would have finished first.
func TestFillYieldsInsideTheSort(t *testing.T) {
	const n = 200_000
	m := make(map[string]any, n)
	for i := range n {
		m[fmt.Sprint(i)] = i
	}
	gathering := n / sortRun // the reads while the keys are collected
	c := &converter{b: &renderPoller{stopAt: gathering + 1}}
	c.fillEntries(&Dict{}, m)
	if c.err == nil {
		t.Fatal("the fill finished although told to stop")
	}
	if len(c.keys) != n {
		t.Fatalf("stopped having gathered %d of %d keys; the yields while gathering are not where this test expects", len(c.keys), n)
	}
	if slices.IsSorted(c.keys) {
		t.Error("stopped at the first chance after gathering, and every key was already sorted: the sort does not yield")
	}
}
