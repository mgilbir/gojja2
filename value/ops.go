// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package value

import (
	"math"
	"math/big"
	"strings"

	"github.com/mgilbir/gojja2/errs"
)

// maxPowBits caps the width of an integer produced by **.
//
// CPython has no such limit and will happily try to materialise 2**(1<<40).
// Refusing is a deliberate divergence: an unbounded exponent in a template is
// a denial-of-service vector, and no real template needs a 128 KiB integer.

// IsTrue is Python truthiness.
//
// Only StrictUndefined can fail here; every other value has an answer. NaN is
// true, matching Python, because float.__bool__ is `self != 0.0`.
func IsTrue(v Value) (bool, error) {
	switch v.kind {
	case KindUndefined:
		if v.undef().behavior == UndefinedStrict {
			return false, v.UndefinedError()
		}
		return false, nil
	case KindNone:
		return false, nil
	case KindBool:
		return v.num != 0, nil
	case KindInt:
		if b, ok := v.obj.(*big.Int); ok {
			return b.Sign() != 0, nil
		}
		return v.num != 0, nil
	case KindFloat:
		return v.AsFloat() != 0, nil
	case KindString, KindBytes:
		return v.str != "", nil
	case KindList, KindTuple:
		s, _ := v.Seq()
		return s.Len() != 0, nil
	case KindDict:
		d, _ := v.Dict()
		return d.Len() != 0, nil
	case KindObject:
		switch o := v.obj.(type) {
		case Booler:
			return o.IsTrue(), nil
		case Mapping:
			return o.Len() != 0, nil
		case Sequence:
			return o.Len() != 0, nil
		}
		return true, nil
	}
	return true, nil
}

// Len is Python's len(). Undefined has length 0 rather than failing, which is
// what makes `{{ nope|length }}` render 0.
func Len(v Value) (int, error) {
	switch v.kind {
	case KindUndefined:
		if v.undef().behavior == UndefinedStrict {
			return 0, v.UndefinedError()
		}
		return 0, nil
	case KindString, KindBytes:
		return runeLen(v.str, v.kind == KindBytes), nil
	case KindList, KindTuple:
		s, _ := v.Seq()
		return s.Len(), nil
	case KindDict:
		d, _ := v.Dict()
		return d.Len(), nil
	case KindObject:
		switch o := v.obj.(type) {
		case Mapping:
			return o.Len(), nil
		case Sequence:
			return o.Len(), nil
		case Sized:
			// Checked last: a Sequence is Sized too, and answers
			// above as the sequence it is.
			return o.Len(), nil
		}
	}
	return 0, errs.New(errs.TypeError, "object of type '%s' has no len()", v.TypeName())
}

// LenValue is len() as a template sees it.
//
// CPython's len() returns a Py_ssize_t, so a length past that is an
// OverflowError rather than a large integer: len(range(-2**63, 0)) raises,
// while len(range(0, 2**63-1)) is fine. An object that can be longer than an
// int says so through BigLener, and this is where that length is either
// reported exactly or refused the way CPython refuses it.
//
// Len itself saturates instead, because indexing and iteration need a usable
// number and a loop over such a range is stopped by the render budget long
// before the count matters.
func LenValue(v Value) (Value, error) {
	if v.kind == KindObject {
		if b, ok := v.obj.(BigLener); ok {
			n := b.BigLen()
			if !n.IsInt64() {
				return Undefined, errs.New(errs.OverflowError,
					"Python int too large to convert to C ssize_t")
			}
			return BigInt(n), nil
		}
	}
	n, err := Len(v)
	if err != nil {
		return Undefined, err
	}
	return Int(int64(n)), nil
}

// --- numeric coercion --------------------------------------------------------

// bothNumbers reports whether an arithmetic op should take the numeric path.
func bothNumbers(a, b Value) bool { return a.IsNumber() && b.IsNumber() }

// eitherFloat reports whether the result of a numeric op must be a float.
func eitherFloat(a, b Value) bool { return a.kind == KindFloat || b.kind == KindFloat }

// undefinedOperand reports the error an undefined raises when it is computed
// with. jinja2's Undefined routes every arithmetic dunder to its own failure,
// so `0 + nope` names the missing variable rather than complaining about int
// and Undefined -- which is the difference between a useful message and a
// puzzle.
func undefinedOperand(vs ...Value) error {
	for _, v := range vs {
		if v.kind == KindUndefined {
			return v.UndefinedError()
		}
	}
	return nil
}

func binTypeError(op string, a, b Value) error {
	return errs.New(errs.TypeError, "unsupported operand type(s) for %s: '%s' and '%s'",
		op, a.TypeName(), b.TypeName())
}

// addInt64 adds with overflow detection so the big.Int path is only taken when
// it is actually needed.
func addInt64(a, b int64) (int64, bool) {
	c := a + b
	return c, (a^c)&(b^c) >= 0
}

func subInt64(a, b int64) (int64, bool) {
	c := a - b
	return c, (a^b)&(a^c) >= 0
}

func mulInt64(a, b int64) (int64, bool) {
	if a == 0 || b == 0 {
		return 0, true
	}
	if (a == -1 && b == math.MinInt64) || (b == -1 && a == math.MinInt64) {
		return 0, false
	}
	c := a * b
	return c, c/b == a
}

// --- arithmetic --------------------------------------------------------------

// Add implements `+`: numeric addition, string concatenation, and sequence
// concatenation between two lists or two tuples.
func Add(a, b Value, budget Budget) (Value, error) {
	if err := undefinedOperand(a, b); err != nil {
		return Undefined, err
	}
	// The left operand decides which concatenation runs, and a tuple
	// subclass runs tuple's: `g + (1,)` is a tuple, and `g + "s"` fails
	// the way tuple fails, reporting itself as "tuple". Only the operation
	// sees the unwrapped value -- b keeps the name the template gave it,
	// and the generic error below keeps both.
	lhs := AsTupleIfPossible(a)
	switch {
	case bothNumbers(a, b):
		if eitherFloat(a, b) {
			x, _ := a.Float64()
			y, _ := b.Float64()
			return Float(x + y), nil
		}
		x, xok := a.Int64()
		y, yok := b.Int64()
		if xok && yok {
			if c, ok := addInt64(x, y); ok {
				return Int(c), nil
			}
		}
		bx, _ := a.BigInt()
		by, _ := b.BigInt()
		if err := chargeIntBits(budget, "+", sumBits(bx, by)); err != nil {
			return Undefined, err
		}
		return BigInt(new(big.Int).Add(bx, by)), nil

	case a.kind == KindString:
		if b.kind != KindString {
			// Markup.__add__ returns NotImplemented for a
			// non-string, which falls through to Python's generic
			// operand error; str.__add__ has a message of its own.
			if a.safe {
				return Undefined, binTypeError("+", a, b)
			}
			return Undefined, errs.New(errs.TypeError,
				"can only concatenate str (not \"%s\") to str", b.TypeName())
		}
		// Markup absorbs the other side: it escapes it and stays
		// Markup, in either order. That is the point of it -- joining
		// trusted markup to untrusted text must neither untrust the
		// result nor trust the text.
		if a.safe || b.safe {
			return Safe(markupText(a) + markupText(b)), nil
		}
		return String(a.str + b.str), nil

	case a.kind == KindBytes:
		if b.kind != KindBytes {
			return Undefined, errs.New(errs.TypeError,
				"can't concat %s to bytes", b.TypeName())
		}
		return Bytes([]byte(a.str + b.str)), nil

	case lhs.kind == KindList || lhs.kind == KindTuple:
		// tuple.__add__ accepts any tuple, a subclass included.
		// list.__add__ does not: it requires an actual list, so a tuple
		// subclass is refused there and named as itself.
		rhs := b
		if lhs.kind == KindTuple {
			rhs = AsTupleIfPossible(b)
		}
		if lhs.kind != rhs.kind {
			return Undefined, errs.New(errs.TypeError,
				"can only concatenate %s (not \"%s\") to %s",
				lhs.TypeName(), b.TypeName(), lhs.TypeName())
		}
		as, _ := lhs.Seq()
		bs, _ := rhs.Seq()
		items := make([]Value, 0, as.Len()+bs.Len())
		items = append(items, as.items...)
		items = append(items, bs.items...)
		if lhs.kind == KindTuple {
			return NewTuple(items...), nil
		}
		return NewList(items...), nil
	}
	return Undefined, binTypeError("+", a, b)
}

// markupText renders one side of a Markup concatenation, escaping it if it is
// not already trusted.
func markupText(v Value) string {
	if v.safe {
		return v.str
	}
	return EscapeHTML(v.str)
}

// Sub implements `-`, which is numeric only.
func Sub(a, b Value, budget Budget) (Value, error) {
	if err := undefinedOperand(a, b); err != nil {
		return Undefined, err
	}
	if !bothNumbers(a, b) {
		return Undefined, binTypeError("-", a, b)
	}
	if eitherFloat(a, b) {
		x, _ := a.Float64()
		y, _ := b.Float64()
		return Float(x - y), nil
	}
	x, xok := a.Int64()
	y, yok := b.Int64()
	if xok && yok {
		if c, ok := subInt64(x, y); ok {
			return Int(c), nil
		}
	}
	bx, _ := a.BigInt()
	by, _ := b.BigInt()
	if err := chargeIntBits(budget, "-", sumBits(bx, by)); err != nil {
		return Undefined, err
	}
	return BigInt(new(big.Int).Sub(bx, by)), nil
}

// Mul implements `*`: numeric multiplication, and repetition of a str, list or
// tuple by an integer count. A non-positive count yields an empty result.
//
// A repetition is charged against budget before it is made, not after: the cap
// repeat() applies is on one result, and a repetition just under it is still
// gigabytes. Charging here rather than at the call site is what makes the two
// callers -- the evaluator and the constant folder -- obey the same rule
// without each having to remember it.
func Mul(a, b Value, budget Budget) (Value, error) {
	// A Markup on the left settles the operation before an undefined on
	// the right can raise its own error. markupsafe's Markup.__mul__ asks
	// the other operand for __index__ and lets that TypeError out, where
	// str.__mul__ returns NotImplemented and hands the undefined a turn to
	// raise instead -- so `{{ x|safe * nope }}` reports the index and
	// `{{ x * nope }}` reports the undefined.
	markupIndexes := a.safe && a.kind == KindString && b.kind == KindUndefined
	if !markupIndexes {
		if err := undefinedOperand(a, b); err != nil {
			return Undefined, err
		}
	}
	if bothNumbers(a, b) {
		if eitherFloat(a, b) {
			x, _ := a.Float64()
			y, _ := b.Float64()
			return Float(x * y), nil
		}
		x, xok := a.Int64()
		y, yok := b.Int64()
		if xok && yok {
			if c, ok := mulInt64(x, y); ok {
				return Int(c), nil
			}
		}
		bx, _ := a.BigInt()
		by, _ := b.BigInt()
		// A product is as wide as its operands together, which is what
		// makes repeated multiplication the fastest way to build an
		// integer too large to hold.
		if err := chargeIntBits(budget, "*",
			int64(bx.BitLen())+int64(by.BitLen())); err != nil {
			return Undefined, err
		}
		return BigInt(new(big.Int).Mul(bx, by)), nil
	}
	// A tuple subclass repeats as the tuple it stands for, and does so from
	// either side: `g * 2` is tuple.__mul__ and `2 * g` is tuple.__rmul__.
	// The unwrapped pair decides what happens; the originals are what the
	// errors below name.
	ua, ub := AsTupleIfPossible(a), AsTupleIfPossible(b)
	// Repetition is commutative in Python: "ab" * 2 and 2 * "ab" agree.
	if seq, n, ok := repeatOperands(ua, ub); ok {
		if err := chargeRepeat(seq, n, budget); err != nil {
			return Undefined, err
		}
		return repeat(seq, n)
	}
	// A count too wide to be an index fails before any repetition is
	// attempted: CPython asks it for __index__, and a Python int outside
	// Py_ssize_t refuses there. That happens whatever the count's sign and
	// however short the sequence is, so it comes ahead of the non-int
	// message below -- `[] * (10**22)` is an OverflowError, not a sequence
	// multiplied by a non-int, and `[] * (-10**22)` is the same error
	// rather than the empty list the negative count would have produced.
	if indexOverflows(ua, ub) {
		return Undefined, errs.New(errs.OverflowError,
			"cannot fit 'int' into an index-sized integer")
	}
	// Once one operand is a sequence, the failure comes from
	// sequence.__mul__ and names only the other operand's type. Markup
	// coerces through __index__ instead, so it reports the other operand
	// as not interpretable as an integer.
	if otherIsB, ok := sequenceRepetitionFailed(ua, ub); ok {
		other := a
		if otherIsB {
			other = b
		}
		if a.safe || b.safe {
			// Markup multiplies through __index__, so the operand
			// named is the one that is not the Markup.
			nonMarkup := a
			if a.safe {
				nonMarkup = b
			}
			return Undefined, errs.New(errs.TypeError,
				"'%s' object cannot be interpreted as an integer", nonMarkup.TypeName())
		}
		return Undefined, errs.New(errs.TypeError,
			"can't multiply sequence by non-int of type '%s'", other.TypeName())
	}
	return Undefined, binTypeError("*", a, b)
}

// isSequenceKind reports whether v participates in the sequence repetition
// protocol: str, bytes, list and tuple.
func isSequenceKind(v Value) bool {
	switch v.kind {
	case KindString, KindBytes, KindList, KindTuple:
		return true
	}
	return false
}

// sequenceRepetitionFailed reports whether the sequence-repetition message
// applies -- one operand is a sequence and the repetition did not happen -- and
// which operand it names as the non-int: the one that is not the sequence.
//
// It decides on the unwrapped operands so that a tuple subclass counts as the
// sequence, while the caller names the originals: `"s" * g` is a sequence
// repeated by a _GroupTuple, and that is the name CPython prints.
func sequenceRepetitionFailed(a, b Value) (otherIsB, ok bool) {
	switch {
	case isSequenceKind(a):
		return true, true
	case isSequenceKind(b):
		return false, true
	}
	return false, false
}

// indexOverflows reports whether a sequence is being repeated by an integer
// too wide to be an index. Not fitting an int64 is exactly the condition
// __index__ refuses on, and a bigint reaches here as a KindInt whose Int64
// does not fit -- which is why repeatOperands turned it down.
func indexOverflows(a, b Value) bool {
	seq, n := a, b
	if !isSequenceKind(seq) {
		seq, n = b, a
	}
	if !isSequenceKind(seq) || !n.IsInteger() {
		return false
	}
	_, fits := n.Int64()
	return !fits
}

func repeatOperands(a, b Value) (seq Value, n int64, ok bool) {
	if a.IsInteger() {
		a, b = b, a
	}
	if !b.IsInteger() {
		return Undefined, 0, false
	}
	switch a.kind {
	case KindString, KindBytes, KindList, KindTuple:
	default:
		return Undefined, 0, false
	}
	count, fits := b.Int64()
	if !fits {
		// A repetition count that does not fit in an int64 could never
		// be allocated anyway; Mul turns it into the OverflowError
		// __index__ raises rather than attempting the repetition.
		return Undefined, 0, false
	}
	return a, count, true
}

// repeatSize reports what repeating seq n times would allocate: bytes for a
// string, elements for a sequence.
func repeatSize(seq Value, n int64) (size int64, isBytes bool) {
	if n <= 0 {
		return 0, seq.kind == KindString || seq.kind == KindBytes
	}
	switch seq.kind {
	case KindString, KindBytes:
		return saturatingMul(int64(len(seq.str)), n), true
	default:
		s, _ := seq.Seq()
		return saturatingMul(int64(s.Len()), n), false
	}
}

// saturatingMul multiplies without wrapping. `"xx" * 9000000000000000000`
// overflows int64, and a wrapped negative would read as a tiny allocation and
// wave through the very thing the caller is trying to bound.
func saturatingMul(a, n int64) int64 {
	if a == 0 || n == 0 {
		return 0
	}
	if a > math.MaxInt64/n {
		return math.MaxInt64
	}
	return a * n
}

// chargeRepeat reserves what a repetition is about to allocate: bytes for a
// string against the output budget, elements for a sequence against the
// iteration budget, so each lands on the bound that measures the same unit.
//
// The hard ceiling is left to repeat(), which refuses a result past it in
// CPython's own words -- "repeated string is too long". Reporting the ceiling
// from here instead would replace that message with one of ours for the only
// case CPython also rejects.
func chargeRepeat(seq Value, n int64, budget Budget) error {
	size, isBytes := repeatSize(seq, n)
	if size > MaxAllocBytes {
		// repeat() refuses this outright and says so in CPython's
		// words; reporting the ceiling from here would replace that
		// message with one of ours.
		return nil
	}
	if isBytes {
		return chargeBytes(budget, size)
	}
	return chargeItems(budget, size)
}

// repeat builds `v * n`, and does it in the size of the result rather than in
// the count.
//
// Those differ exactly when the unit is empty, and that is the whole defect:
// `"" * 10000000000` is the empty string, but copying nothing ten billion times
// to establish that took seventeen seconds. Nothing stopped it. The budget is
// charged the size of the result, which is zero, so there was nothing to
// charge; the loop consults no context, so no deadline reached it; and the
// expression is constant, so the work happened in FromString, where the
// caller's deadline does not apply at all. Twenty-two characters of template.
//
// State.repeatStringN, which is this operation on the engine's side, has always
// returned early on an empty unit.
func repeat(v Value, n int64) (Value, error) {
	if n < 0 {
		n = 0
	}
	switch v.kind {
	case KindString, KindBytes:
		size := saturatingMul(int64(len(v.str)), n)
		if size > MaxAllocBytes {
			return Undefined, errs.New(errs.OverflowError, "repeated string is too long")
		}
		out := Value{kind: v.kind, safe: v.safe}
		if size > 0 {
			// n fits an int here: the unit is at least one byte, so
			// the size ceiling has already bounded the count.
			out.str = strings.Repeat(v.str, int(n))
		}
		return out, nil
	default:
		s, _ := v.Seq()
		total := saturatingMul(int64(s.Len()), n)
		if total > MaxAllocBytes {
			return Undefined, errs.New(errs.OverflowError, "repeated sequence is too long")
		}
		var items []Value
		if total > 0 {
			items = make([]Value, 0, total)
			for int64(len(items)) < total {
				items = append(items, s.items...)
			}
		}
		if v.kind == KindTuple {
			return NewTuple(items...), nil
		}
		return NewList(items...), nil
	}
}

// floatOperand coerces a numeric operand to float64 for a mixed-type
// operation. A wide integer that is outside float64's range cannot be
// converted, which is an error in Python rather than an infinity.
func floatOperand(v Value) (float64, error) {
	f, ok := v.Float64()
	if !ok {
		return 0, errs.New(errs.TypeError, "must be real number, not %s", v.TypeName())
	}
	if math.IsInf(f, 0) && v.IsInteger() {
		return 0, errs.New(errs.OverflowError, "int too large to convert to float")
	}
	return f, nil
}

// floatDivmod reproduces CPython's float_divmod, which is not the same as
// math.Floor(x/y).
//
// The difference shows up wherever the naive form loses the sign or the last
// bit: 1 // -inf is -1.0 rather than -0.0, and 0 % -2.5 is -0.0 rather than
// 0.0. CPython derives the quotient from the remainder instead of dividing
// twice, so that is what this does.
func floatDivmod(x, y float64) (floordiv, mod float64) {
	mod = math.Mod(x, y)
	div := (x - mod) / y
	if mod != 0 {
		if (y < 0) != (mod < 0) {
			mod += y
			div--
		}
	} else {
		mod = math.Copysign(0, y)
	}
	if div != 0 {
		floordiv = math.Floor(div)
		if div-floordiv > 0.5 {
			floordiv++
		}
	} else {
		floordiv = math.Copysign(0, x/y)
	}
	return floordiv, mod
}

// Div implements `/`, Python 3 true division, which always yields a float.
//
// Integer division is computed as an exact rational and rounded once, not by
// converting both sides to float first: 1 / (2**53 + 1) differs between the
// two, and CPython gives the exactly-rounded answer.
func Div(a, b Value) (Value, error) {
	if err := undefinedOperand(a, b); err != nil {
		return Undefined, err
	}
	if !bothNumbers(a, b) {
		return Undefined, binTypeError("/", a, b)
	}
	if !eitherFloat(a, b) {
		bx, _ := a.BigInt()
		by, _ := b.BigInt()
		if by.Sign() == 0 {
			return Undefined, errs.New(errs.ZeroDivisionError, "division by zero")
		}
		if bx.Sign() == 0 {
			// big.Rat has no signed zero, but 0 / -1 is -0.0.
			return Float(math.Copysign(0, float64(by.Sign()))), nil
		}
		q, _ := new(big.Rat).SetFrac(bx, by).Float64()
		return Float(q), nil
	}
	x, err := floatOperand(a)
	if err != nil {
		return Undefined, err
	}
	y, err := floatOperand(b)
	if err != nil {
		return Undefined, err
	}
	if y == 0 {
		return Undefined, errs.New(errs.ZeroDivisionError, "float division by zero")
	}
	return Float(x / y), nil
}

// FloorDiv implements `//`, which rounds toward negative infinity rather than
// toward zero as Go's integer division does.
func FloorDiv(a, b Value) (Value, error) {
	if err := undefinedOperand(a, b); err != nil {
		return Undefined, err
	}
	if !bothNumbers(a, b) {
		return Undefined, binTypeError("//", a, b)
	}
	if eitherFloat(a, b) {
		x, err := floatOperand(a)
		if err != nil {
			return Undefined, err
		}
		y, err := floatOperand(b)
		if err != nil {
			return Undefined, err
		}
		if y == 0 {
			return Undefined, errs.New(errs.ZeroDivisionError, "float floor division by zero")
		}
		q, _ := floatDivmod(x, y)
		return Float(q), nil
	}
	bx, _ := a.BigInt()
	by, _ := b.BigInt()
	if by.Sign() == 0 {
		return Undefined, errs.New(errs.ZeroDivisionError, "integer division or modulo by zero")
	}
	// big.Int.Div is Euclidean; Python floors. They differ when exactly one
	// operand is negative, so compute the truncated quotient and correct.
	q, r := new(big.Int).QuoRem(bx, by, new(big.Int))
	if r.Sign() != 0 && (r.Sign() < 0) != (by.Sign() < 0) {
		q.Sub(q, big.NewInt(1))
	}
	return BigInt(q), nil
}

// Mod implements `%`: Python modulo on numbers, whose result takes the sign of
// the divisor, and printf-style formatting on strings.
//
// budget bounds the string path, where a width the template chose sizes the
// result: it may be nil, which means nobody is counting but the hard ceiling
// still applies.
func Mod(a, b Value, budget Budget) (Value, error) {
	if a.kind == KindString {
		// `"%s" % nope` formats the undefined as "", so the operand
		// check must not run before the string path.
		return FormatPercent(a, b, budget)
	}
	if err := undefinedOperand(a, b); err != nil {
		return Undefined, err
	}
	if !bothNumbers(a, b) {
		return Undefined, binTypeError("%", a, b)
	}
	if eitherFloat(a, b) {
		x, err := floatOperand(a)
		if err != nil {
			return Undefined, err
		}
		y, err := floatOperand(b)
		if err != nil {
			return Undefined, err
		}
		if y == 0 {
			return Undefined, errs.New(errs.ZeroDivisionError, "float modulo")
		}
		_, m := floatDivmod(x, y)
		return Float(m), nil
	}
	bx, _ := a.BigInt()
	by, _ := b.BigInt()
	if by.Sign() == 0 {
		return Undefined, errs.New(errs.ZeroDivisionError, "integer modulo by zero")
	}
	r := new(big.Int).Rem(bx, by)
	if r.Sign() != 0 && (r.Sign() < 0) != (by.Sign() < 0) {
		r.Add(r, by)
	}
	return BigInt(r), nil
}

// Pow implements `**`.
//
// An integer base with a non-negative integer exponent stays exact; a negative
// exponent falls to float, as it does in Python. A negative base with a
// fractional exponent yields a complex number in Python -- a type with no
// place in a template -- so that case is rejected rather than approximated.
func Pow(a, b Value, budget Budget) (Value, error) {
	if err := undefinedOperand(a, b); err != nil {
		return Undefined, err
	}
	if !bothNumbers(a, b) {
		// pow() shares this operator's slot, so Python names both.
		return Undefined, errs.New(errs.TypeError,
			"unsupported operand type(s) for ** or pow(): '%s' and '%s'",
			a.TypeName(), b.TypeName())
	}
	if a.IsInteger() && b.IsInteger() {
		by, _ := b.BigInt()
		if by.Sign() >= 0 {
			if !by.IsInt64() {
				return Undefined, errs.New(errs.OverflowError, "exponent too large")
			}
			bx, _ := a.BigInt()
			if err := chargeIntBits(budget, "**",
				estimatePowBits(bx, by.Int64())); err != nil {
				return Undefined, err
			}
			return BigInt(new(big.Int).Exp(bx, by, nil)), nil
		}
	}
	x, err := floatOperand(a)
	if err != nil {
		return Undefined, err
	}
	y, err := floatOperand(b)
	if err != nil {
		return Undefined, err
	}
	if x == 0 && y < 0 {
		// Python reports this against the float it promoted to, so the
		// message says 0.0 even when the base was the integer 0.
		return Undefined, errs.New(errs.ZeroDivisionError,
			"0.0 cannot be raised to a negative power")
	}
	if x < 0 && !math.IsInf(y, 0) && !math.IsNaN(y) && y != math.Trunc(y) {
		return Undefined, errs.New(errs.ValueError,
			"a negative number cannot be raised to a fractional power (gojja2 has no complex type)")
	}
	return Float(powFloat(x, y)), nil
}

// estimatePowBits bounds the width of base**exp without computing it.
func estimatePowBits(base *big.Int, exp int64) int64 {
	bits := int64(base.BitLen())
	if bits <= 1 {
		return 1 // 0 and +/-1 never grow
	}
	// Saturate rather than wrap: the product is what the caller compares
	// against its own ceiling, and a wrapped one reads as "small". This
	// used to saturate at MaxIntBits, which was the ceiling as well as the
	// guard -- so raising the ceiling would have refused at the old one.
	if exp > math.MaxInt64/bits {
		return math.MaxInt64
	}
	return bits * exp
}

// sumBits bounds the width of a sum or difference: carrying out of the wider
// operand adds at most one bit.
func sumBits(x, y *big.Int) int64 {
	bits := int64(x.BitLen())
	if b := int64(y.BitLen()); b > bits {
		bits = b
	}
	return bits + 1
}

// Neg implements unary `-`.
func Neg(v Value) (Value, error) {
	if err := undefinedOperand(v); err != nil {
		return Undefined, err
	}
	switch {
	case v.kind == KindFloat:
		return Float(-v.AsFloat()), nil
	case v.IsInteger():
		if i, ok := v.Int64(); ok && i != math.MinInt64 {
			return Int(-i), nil
		}
		b, _ := v.BigInt()
		return BigInt(new(big.Int).Neg(b)), nil
	}
	return Undefined, errs.New(errs.TypeError, "bad operand type for unary -: '%s'", v.TypeName())
}

// Pos implements unary `+`, which coerces bool to int and is otherwise the
// identity on numbers.
func Pos(v Value) (Value, error) {
	if err := undefinedOperand(v); err != nil {
		return Undefined, err
	}
	switch v.kind {
	case KindFloat:
		return v, nil
	case KindBool:
		return Int(int64(v.num)), nil
	case KindInt:
		return v, nil
	}
	return Undefined, errs.New(errs.TypeError, "bad operand type for unary +: '%s'", v.TypeName())
}

// powBits is the working precision used to round x**y correctly.
const powBits = 320

// powFloat computes x**y, agreeing with CPython where math.Pow does not.
//
// CPython delegates to the platform libm, which rounds correctly; Go's pure-Go
// math.Pow can be a unit in the last place out, and that difference is visible
// in rendered output (3 ** 2.5 differs in the final digit). Any exponent that
// is a dyadic rational -- which every float with a modest fractional part is --
// can be evaluated exactly: take the base's 2**-e'th root by repeated square
// root, then raise to the remaining integer power, all at extended precision,
// and round once at the end. Exponents outside that shape fall back to
// math.Pow.
func powFloat(x, y float64) float64 {
	if r, ok := powExact(x, y); ok {
		return r
	}
	return math.Pow(x, y)
}

func powExact(x, y float64) (float64, bool) {
	switch {
	case math.IsNaN(x) || math.IsNaN(y):
		return 0, false
	case math.IsInf(x, 0) || math.IsInf(y, 0):
		return 0, false
	case x == 0 || y == 0:
		return 0, false
	}

	// Decompose y as m / 2**e with m an integer. Bounding both keeps the
	// work constant: e square roots and at most log2(m) squarings.
	const maxRootDepth, maxMantissa = 24, 1 << 16
	e, t := 0, y
	for t != math.Trunc(t) && e < maxRootDepth {
		t *= 2
		e++
	}
	if t != math.Trunc(t) || math.Abs(t) > maxMantissa {
		return 0, false
	}
	m := int64(t)
	if x < 0 && e != 0 {
		return 0, false // would be complex; Pow rejects this earlier
	}

	base := new(big.Float).SetPrec(powBits).SetFloat64(x)
	negative := base.Sign() < 0
	if negative {
		base.Neg(base)
	}
	for range e {
		base.Sqrt(base)
	}

	acc := new(big.Float).SetPrec(powBits).SetInt64(1)
	n := m
	if n < 0 {
		n = -n
	}
	for n > 0 {
		if n&1 == 1 {
			acc.Mul(acc, base)
		}
		base.Mul(base, base)
		n >>= 1
	}
	if m < 0 {
		acc.Quo(new(big.Float).SetPrec(powBits).SetInt64(1), acc)
	}
	if negative && m%2 != 0 {
		acc.Neg(acc)
	}
	f, _ := acc.Float64()
	return f, true
}
