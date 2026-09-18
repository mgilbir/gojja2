// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"github.com/mgilbir/gojja2/value"
	"math"
	"strings"

	"github.com/mgilbir/gojja2/errs"
)

// The allocation a template asks for has to be charged before it is made.
//
// Nearly every resource defect in this engine has had one shape: a filter, a
// method or a global reads an integer out of the template, uses it to size an
// allocation, and charges the budget afterwards -- or not at all. Charging
// afterwards is useless, because by then the memory is committed. `round`
// formatted a two-billion-digit decimal, `tojson` built a two-gigabyte
// indent, `slice` allocated a hundred million lists, and each did it with the
// output budget set to four kilobytes.
//
// The helpers below are the way to size anything from a template-chosen
// number. They answer two different questions, and both have to be asked:
//
//   - the render budget is what the caller asked for, and
//   - the hard ceiling is what the process survives when the caller asked for
//     nothing, because a zero or negative budget means "unbounded" and an
//     unbounded budget must still not mean "allocate 2**63 bytes".
//
// The ceiling matches the one value.repeat already applies to `*`, so the two
// halves of the engine refuse the same sizes.
const maxAllocBytes = math.MaxInt32

// ChargeBytes reserves n bytes of allocation against the render's output
// budget, before the allocation is made.
//
// A nil State, or one without a budget, still gets the hard ceiling: constant
// folding runs that way, and it allocates just as much.
func (s *State) ChargeBytes(n int64) error {
	if n <= 0 {
		return nil
	}
	if n > maxAllocBytes {
		return errs.New(errs.OverflowError,
			"result would be %d bytes, over the %d byte limit", n, int64(maxAllocBytes))
	}
	if s == nil || s.budget == nil {
		return nil
	}
	return s.budget.account(int(n))
}

// IntBitLimit is value.IntBitLimiter: the ceiling on the width of an integer
// this render may compute.
//
// A State with no budget -- constant folding runs that way before any render
// exists -- answers the package default, which is what the value package would
// have used anyway.
func (s *State) IntBitLimit() int64 {
	if s == nil || s.budget == nil {
		return value.MaxIntBits
	}
	return s.budget.maxIntBits
}

// ChargeItems reserves n elements against the render's iteration budget,
// before the slice holding them is allocated.
func (s *State) ChargeItems(n int64) error {
	if n <= 0 {
		return nil
	}
	if n > maxAllocBytes {
		return errs.New(errs.OverflowError,
			"result would hold %d elements, over the %d element limit",
			n, int64(maxAllocBytes))
	}
	if s == nil || s.budget == nil {
		return nil
	}
	return s.budget.chargeSteps(int(n))
}

// repeatString is strings.Repeat with the result charged first.
//
// count is what the template asked for and is not trusted: a negative count
// repeats nothing, as Python's `"x" * -1` does, rather than panicking the way
// strings.Repeat does.
func (s *State) repeatString(unit string, count int) (string, error) {
	return s.repeatStringN(unit, int64(count))
}

// repeatStringN is repeatString for a count that has already been computed in
// int64, and must be charged at its true size.
//
// Taking an int here would mean the caller clamping first, and clamping an
// enormous count down to the ceiling turns "refuse this" into "allocate the
// largest thing allowed" -- the same way a wrapped negative reads as a tiny
// allocation. The charge has to see the number the template actually asked for.
func (s *State) repeatStringN(unit string, count int64) (string, error) {
	if count <= 0 || unit == "" {
		return "", nil
	}
	if err := s.ChargeBytes(saturatingMulInt(int64(len(unit)), count)); err != nil {
		return "", err
	}
	return strings.Repeat(unit, int(count)), nil
}

// saturatingMulInt multiplies without wrapping, so a product that overflows
// reads as "enormous" rather than as a small or negative number -- which is
// exactly how an unbounded allocation gets waved through a size check.
func saturatingMulInt(a, b int64) int64 {
	if a == 0 || b == 0 {
		return 0
	}
	if a < 0 || b < 0 {
		return 0
	}
	if a > math.MaxInt64/b {
		return math.MaxInt64
	}
	return a * b
}
