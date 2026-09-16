// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package value

import (
	"math"
	"math/big"
	"strings"

	"github.com/mgilbir/gojja2/errs"
)

// Equal is Python's ==.
//
// It never fails: comparing values of unrelated types is False, not an error.
// Numbers compare across int, float and bool because bool is an int subclass
// and Python's numeric tower makes 1 == 1.0 == True.
func Equal(a, b Value) bool {
	if a.IsNumber() && b.IsNumber() {
		ord, ok := compareNumbers(a, b)
		return ok && ord == 0
	}
	if a.kind != b.kind {
		// str and bytes never compare equal, and neither do list and
		// tuple -- Python keeps those distinct.
		return false
	}
	switch a.kind {
	case KindNone:
		return true
	case KindUndefined:
		// jinja2's Undefined.__eq__ compares only the class, so any two
		// undefined values of the same flavour are equal.
		return a.undef().behavior == b.undef().behavior
	case KindString, KindBytes:
		return a.str == b.str
	case KindList, KindTuple:
		as, _ := a.Seq()
		bs, _ := b.Seq()
		if as.Len() != bs.Len() {
			return false
		}
		for i := range as.items {
			if !Equal(as.items[i], bs.items[i]) {
				return false
			}
		}
		return true
	case KindDict:
		ad, _ := a.Dict()
		bd, _ := b.Dict()
		if ad.Len() != bd.Len() {
			return false
		}
		// Order is irrelevant to dict equality, only content.
		for _, e := range ad.entries {
			other, ok, err := bd.Get(e.Key)
			if err != nil || !ok || !Equal(e.Value, other) {
				return false
			}
		}
		return true
	case KindObject:
		if e, ok := a.obj.(Equaler); ok {
			if equal, known := e.Equals(b); known {
				return equal
			}
		}
		if e, ok := b.obj.(Equaler); ok {
			if equal, known := e.Equals(a); known {
				return equal
			}
		}
		return a.obj == b.obj
	case KindFunc:
		return a.obj == b.obj
	}
	return false
}

// Ordered evaluates `a op b` for op in "<", "<=", ">", ">=".
//
// Returning a bool rather than a three-way ordering is what lets NaN behave:
// every comparison involving it is false, including NaN <= NaN, which no
// -1/0/1 result can express.
func Ordered(op string, a, b Value) (bool, error) {
	ord, ok, err := compare(op, a, b)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil // unordered: NaN was involved
	}
	switch op {
	case "<":
		return ord < 0, nil
	case "<=":
		return ord <= 0, nil
	case ">":
		return ord > 0, nil
	case ">=":
		return ord >= 0, nil
	}
	return false, errs.New(errs.ValueError, "unknown comparison operator %q", op)
}

// compare returns the ordering of a and b. The second result is false when the
// two are unordered because of NaN; op is carried only for the error message.
func compare(op string, a, b Value) (int, bool, error) {
	// Undefined has no ordering: jinja2's Undefined raises on <, <=, > and
	// >= even though == is answerable. Report the undefined's own error
	// rather than a type mismatch, which is what a template author needs.
	if a.IsUndefined() {
		return 0, false, a.UndefinedError()
	}
	if b.IsUndefined() {
		return 0, false, b.UndefinedError()
	}
	if a.IsNumber() && b.IsNumber() {
		ord, ok := compareNumbers(a, b)
		return ord, ok, nil
	}
	if a.kind == b.kind {
		switch a.kind {
		case KindString, KindBytes:
			return strings.Compare(a.str, b.str), true, nil
		case KindList, KindTuple:
			as, _ := a.Seq()
			bs, _ := b.Seq()
			return compareSeq(op, as.items, bs.items)
		}
	}
	return 0, false, errs.New(errs.TypeError,
		"'%s' not supported between instances of '%s' and '%s'",
		op, a.TypeName(), b.TypeName())
}

// compareSeq is Python's lexicographic sequence ordering: the first differing
// element decides, and if one runs out first the shorter sequence is smaller.
func compareSeq(op string, a, b []Value) (int, bool, error) {
	n := min(len(a), len(b))
	for i := range n {
		if Equal(a[i], b[i]) {
			continue
		}
		ord, ok, err := compare(op, a[i], b[i])
		if err != nil {
			return 0, false, err
		}
		if !ok {
			// Elements are unequal but unordered, so the sequences
			// are too.
			return 0, false, nil
		}
		return ord, true, nil
	}
	switch {
	case len(a) < len(b):
		return -1, true, nil
	case len(a) > len(b):
		return 1, true, nil
	}
	return 0, true, nil
}

// compareNumbers orders two numbers exactly, without the precision loss of
// converting both to float64.
//
// Python compares int against float by value, not by coercion, so
// 2**53 + 1 == float(2**53) is False. Anything that rounds the int first gets
// that wrong, so wide integers are compared as exact rationals.
func compareNumbers(a, b Value) (int, bool) {
	af, aIsFloat := floatOf(a)
	bf, bIsFloat := floatOf(b)

	switch {
	case aIsFloat && bIsFloat:
		if math.IsNaN(af) || math.IsNaN(bf) {
			return 0, false
		}
		return cmpFloat(af, bf), true
	case !aIsFloat && !bIsFloat:
		ai, _ := a.BigInt()
		bi, _ := b.BigInt()
		return ai.Cmp(bi), true
	}

	// Exactly one side is a float. Order the integer against it exactly and
	// flip the result if the integer was the right-hand operand, since
	// cmpIntFloat answers cmp(int, float).
	f, i, sign := bf, a, 1
	if aIsFloat {
		f, i, sign = af, b, -1
	}
	if math.IsNaN(f) {
		return 0, false
	}
	switch {
	case math.IsInf(f, 1):
		return -sign, true
	case math.IsInf(f, -1):
		return sign, true
	}
	bi, _ := i.BigInt()
	ord := new(big.Rat).SetInt(bi).Cmp(new(big.Rat).SetFloat64(f))
	return ord * sign, true
}

// floatOf reports the float value of v and whether v is a float at all.
func floatOf(v Value) (float64, bool) {
	if v.kind == KindFloat {
		return v.AsFloat(), true
	}
	return 0, false
}

func cmpFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// Contains implements `in`: substring for strings, membership for sequences,
// and key lookup for mappings.
func Contains(item, container Value) (bool, error) {
	switch container.kind {
	case KindString:
		if item.kind != KindString {
			return false, errs.New(errs.TypeError,
				"'in <string>' requires string as left operand, not %s", item.TypeName())
		}
		return strings.Contains(container.str, item.str), nil
	case KindBytes:
		if item.kind != KindBytes {
			return false, errs.New(errs.TypeError,
				"a bytes-like object is required, not '%s'", item.TypeName())
		}
		return strings.Contains(container.str, item.str), nil
	case KindList, KindTuple:
		s, _ := container.Seq()
		for _, v := range s.items {
			if Equal(item, v) {
				return true, nil
			}
		}
		return false, nil
	case KindDict:
		d, _ := container.Dict()
		if !Hashable(item) {
			return false, errs.New(errs.TypeError, "unhashable type: '%s'", item.TypeName())
		}
		_, ok, err := d.Get(item)
		return ok, err
	case KindUndefined:
		if container.undef().behavior == UndefinedStrict {
			return false, container.UndefinedError()
		}
		return false, nil
	case KindObject:
		switch o := container.obj.(type) {
		case Mapping:
			_, ok := o.GetItem(item)
			return ok, nil
		case Sequence:
			for i := range o.Len() {
				v, ok := o.GetIndex(i)
				if ok && Equal(item, v) {
					return true, nil
				}
			}
			return false, nil
		case Iterable:
			for v := range o.Iterate() {
				if Equal(item, v) {
					return true, nil
				}
			}
			return false, nil
		}
	}
	return false, errs.New(errs.TypeError, "argument of type '%s' is not iterable", container.TypeName())
}

// errTypeNotIterable is the error Python raises for `for x in <non-iterable>`.
func errTypeNotIterable(v Value) error {
	return errs.New(errs.TypeError, "'%s' object is not iterable", v.TypeName())
}
