// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package value

import (
	"math"

	"github.com/mgilbir/gojja2/errs"
)

// MaxAllocBytes is the hard ceiling on anything a template sizes for itself.
//
// It exists because a zero or negative render budget means "unbounded", and
// unbounded must still not mean "allocate 2**63 bytes". Every sized allocation
// in the engine refuses at the same number, so `"x" * n` and `"%ns" % x` answer
// a hostile n the same way.
const MaxAllocBytes = math.MaxInt32

// Budget is what an operation charges an allocation against before making it.
//
// It lives here, rather than the charging being done by callers, because
// charging afterwards is useless and charging at the call site is something
// every new call site has to remember. `%` is the case that proves it: `*` was
// charged by its two callers and `%` by neither, so a 42-byte template
// OOM-killed FromString while an equivalent one with `*` was refused.
//
// A render's State satisfies this. So does the constant folder's, which is how
// compile time comes under a bound at all -- there is no render then, and
// nothing a caller configured applies, but folding still executes real
// operations on real sizes.
type Budget interface {
	// ChargeBytes reserves n bytes, before they are allocated.
	ChargeBytes(n int64) error
	// ChargeItems reserves n elements, before the slice holding them is.
	ChargeItems(n int64) error
}

// chargeBytes reserves n bytes against b, which may be nil.
//
// The ceiling applies whether or not there is a budget: nil means "nobody is
// counting", not "anything goes". Constant folding outside a template, and any
// caller reaching the value package directly, still cannot ask for two
// gigabytes.
func chargeBytes(b Budget, n int64) error {
	if n <= 0 {
		return nil
	}
	if n > MaxAllocBytes {
		return errs.New(errs.OverflowError,
			"result would be %d bytes, over the %d byte limit", n, int64(MaxAllocBytes))
	}
	if b == nil {
		return nil
	}
	return b.ChargeBytes(n)
}

// chargeItems reserves n elements against b, which may be nil.
func chargeItems(b Budget, n int64) error {
	if n <= 0 {
		return nil
	}
	if n > MaxAllocBytes {
		return errs.New(errs.OverflowError,
			"result would hold %d elements, over the %d element limit",
			n, int64(MaxAllocBytes))
	}
	if b == nil {
		return nil
	}
	return b.ChargeItems(n)
}
