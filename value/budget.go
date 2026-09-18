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

// MaxIntBits bounds the width of any integer an expression computes.
//
// Integers here are arbitrary precision, which is what makes `{{ 2 ** 100 }}`
// answer all 31 digits. Arbitrary precision and attacker-chosen magnitudes do
// not combine: multiplication doubles the operand width, so an expression that
// squares repeatedly grows exponentially while the template that asks for it
// stays the same size.
//
// `**` carried a bound of its own long before the others did, which made the
// bound decorative -- `x ** 2` was refused past this width and `x * x` was not,
// though they compute the same number. So the limit belongs to *integers*
// rather than to one operator: every operation that can widen an integer asks
// this same question, and an operation added later inherits the answer instead
// of reopening the hole.
//
// 2**20 bits is 128 KiB, or about 315,653 decimal digits. Nothing written on
// purpose computes an integer that wide; anything that needs to is asking for
// a bignum library rather than a template engine.
const MaxIntBits = 1 << 20

// chargeIntBits refuses an integer wider than MaxIntBits and charges the bytes
// it is about to occupy against the budget.
//
// bits is an upper bound on the result's width derived from the operands, so
// the question is asked before the result exists. Deriving it afterwards would
// be charging for memory that is already committed, which is the shape nearly
// every resource defect in this engine has had.
//
// op names the operator for the message, because "the result of what" is the
// first thing anyone reading the error needs to know.
func chargeIntBits(b Budget, op string, bits int64) error {
	if bits > MaxIntBits {
		return errs.New(errs.OverflowError,
			"result of %s would be %d bits wide, over the %d bit limit",
			op, bits, int64(MaxIntBits))
	}
	// Eight bits to the byte, rounded up: the budget counts bytes, and a
	// width that rounds to zero still costs an allocation.
	return chargeBytes(b, (bits+7)/8)
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
