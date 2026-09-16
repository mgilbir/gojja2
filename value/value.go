// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

// Package value implements the value model templates see.
//
// The model is Python's, not Go's, because CPython's jinja2 is the spec:
// integers are arbitrary precision, strings are sequences of code points,
// dicts preserve insertion order and hash 1, 1.0 and True to the same slot,
// and truthiness follows __bool__/__len__. Where Go and Python disagree,
// Python wins.
package value

import (
	"math"
	"math/big"
)

// Kind is the Python type of a Value.
type Kind uint8

const (
	// KindUndefined is jinja2's Undefined: renders as "", is falsey, and
	// errors on most other use. It is not a Python type of its own.
	KindUndefined Kind = iota
	KindNone
	KindBool
	KindInt
	KindFloat
	KindString
	KindBytes
	KindList
	KindTuple
	KindDict
	// KindObject is a Go value exposed to the template.
	KindObject
	// KindFunc is anything callable: filters, tests, macros, globals.
	KindFunc
)

// typeNames are the Python type names used in error messages such as
// "unsupported operand type(s) for +: 'int' and 'str'".
var typeNames = [...]string{
	KindUndefined: "undefined",
	KindNone:      "NoneType",
	KindBool:      "bool",
	KindInt:       "int",
	KindFloat:     "float",
	KindString:    "str",
	KindBytes:     "bytes",
	KindList:      "list",
	KindTuple:     "tuple",
	KindDict:      "dict",
	KindObject:    "object",
	KindFunc:      "function",
}

func (k Kind) String() string { return typeNames[k] }

// Value is a template value.
//
// The payload is split across typed fields rather than a single `any` so that
// ints, floats, bools and strings -- overwhelmingly the common cases -- cost
// no allocation. kind and safe share one word of padding, so Value is four
// words wide.
type Value struct {
	kind Kind
	// safe marks a string as markupsafe.Markup: already escaped, and
	// exempt from autoescaping. It is a flag rather than a Kind because
	// Markup subclasses str and must behave as one everywhere.
	safe bool
	// num carries KindBool (0/1), KindInt (int64 bits, when obj is nil) and
	// KindFloat (math.Float64bits).
	num uint64
	// str carries KindString and KindBytes.
	str string
	// obj carries everything else: *big.Int for ints too wide for num,
	// *Seq, *Dict, *Undefined, Object, Func.
	obj any
}

// --- constructors ------------------------------------------------------------

// None is Python's None.
var None = Value{kind: KindNone}

// True and False are the Python bools.
var (
	True  = Value{kind: KindBool, num: 1}
	False = Value{kind: KindBool}
)

// Undefined is the bare undefined value, as produced by a missing name.
var Undefined = Value{kind: KindUndefined, obj: &undefinedInfo{}}

// Bool returns Python's True or False.
func Bool(b bool) Value {
	if b {
		return True
	}
	return False
}

// Int returns a Python int.
func Int(i int64) Value { return Value{kind: KindInt, num: uint64(i)} }

// Uint returns a Python int, widening to arbitrary precision when i exceeds
// the signed 64-bit fast path.
func Uint(u uint64) Value {
	if u <= math.MaxInt64 {
		return Int(int64(u))
	}
	return BigInt(new(big.Int).SetUint64(u))
}

// BigInt returns a Python int of arbitrary precision, narrowing to the fast
// path when it fits so that equal ints always compare equal.
func BigInt(i *big.Int) Value {
	if i.IsInt64() {
		return Int(i.Int64())
	}
	return Value{kind: KindInt, obj: i}
}

// Float returns a Python float.
func Float(f float64) Value { return Value{kind: KindFloat, num: math.Float64bits(f)} }

// String returns a Python str.
func String(s string) Value { return Value{kind: KindString, str: s} }

// Safe returns a markupsafe.Markup: a str that autoescaping leaves alone.
func Safe(s string) Value { return Value{kind: KindString, str: s, safe: true} }

// Bytes returns a Python bytes.
func Bytes(b []byte) Value { return Value{kind: KindBytes, str: string(b)} }

// --- kind predicates ---------------------------------------------------------

// Kind returns the value's Python type.
func (v Value) Kind() Kind { return v.kind }

// TypeName is the Python type name, as it appears in error messages.
func (v Value) TypeName() string {
	if v.kind == KindObject {
		if o, ok := v.obj.(interface{ TypeName() string }); ok {
			return o.TypeName()
		}
	}
	return v.kind.String()
}

// IsUndefined reports whether v is jinja2 Undefined.
func (v Value) IsUndefined() bool { return v.kind == KindUndefined }

// IsNone reports whether v is Python None.
func (v Value) IsNone() bool { return v.kind == KindNone }

// IsSafe reports whether v is a Markup string, exempt from autoescaping.
func (v Value) IsSafe() bool { return v.safe }

// IsString reports whether v is a str, Markup included.
func (v Value) IsString() bool { return v.kind == KindString }

// IsNumber reports whether v is an int, float or bool. Python treats bool as
// an int subclass, so it is a number in every arithmetic context.
func (v Value) IsNumber() bool {
	switch v.kind {
	case KindInt, KindFloat, KindBool:
		return true
	}
	return false
}

// IsInteger reports whether v is an int or bool (an int subclass).
func (v Value) IsInteger() bool { return v.kind == KindInt || v.kind == KindBool }

// IsSequence reports whether v is an ordered sequence: list, tuple or str.
func (v Value) IsSequence() bool {
	switch v.kind {
	case KindList, KindTuple, KindString, KindBytes:
		return true
	}
	return false
}

// IsMapping reports whether v behaves as a dict.
func (v Value) IsMapping() bool {
	if v.kind == KindDict {
		return true
	}
	_, ok := v.obj.(Mapping)
	return v.kind == KindObject && ok
}

// --- scalar accessors --------------------------------------------------------

// AsBool returns the raw bool of a KindBool value. Use IsTrue for truthiness.
func (v Value) AsBool() bool { return v.num != 0 }

// AsFloat returns the raw float64 of a KindFloat value.
func (v Value) AsFloat() float64 { return math.Float64frombits(v.num) }

// AsString returns the raw text of a str or bytes value.
func (v Value) AsString() string { return v.str }

// Int64 returns the value as an int64, reporting whether it is an integer that
// fits. bool counts as an integer, matching Python.
func (v Value) Int64() (int64, bool) {
	switch v.kind {
	case KindBool:
		return int64(v.num), true
	case KindInt:
		if v.obj != nil {
			return 0, false // arbitrary precision, does not fit
		}
		return int64(v.num), true
	}
	return 0, false
}

// BigInt returns an integer value as a *big.Int. The result is freshly
// allocated for the fast path and must not be mutated for the wide path.
func (v Value) BigInt() (*big.Int, bool) {
	switch v.kind {
	case KindBool:
		return big.NewInt(int64(v.num)), true
	case KindInt:
		if b, ok := v.obj.(*big.Int); ok {
			return b, true
		}
		return big.NewInt(int64(v.num)), true
	}
	return nil, false
}

// Float64 returns the value as a float64, as Python's float() would for int,
// float and bool. Wide ints lose precision exactly as they do in CPython.
func (v Value) Float64() (float64, bool) {
	switch v.kind {
	case KindBool:
		return float64(v.num), true
	case KindFloat:
		return v.AsFloat(), true
	case KindInt:
		if b, ok := v.obj.(*big.Int); ok {
			f, _ := new(big.Float).SetInt(b).Float64()
			return f, true
		}
		return float64(int64(v.num)), true
	}
	return 0, false
}

// --- container payloads ------------------------------------------------------

// Seq returns the backing sequence of a list or tuple.
func (v Value) Seq() (*Seq, bool) {
	if v.kind == KindList || v.kind == KindTuple {
		return v.obj.(*Seq), true
	}
	return nil, false
}

// Dict returns the backing mapping of a dict.
func (v Value) Dict() (*Dict, bool) {
	if v.kind == KindDict {
		return v.obj.(*Dict), true
	}
	return nil, false
}

// Object returns the Go value behind an object.
func (v Value) Object() (Object, bool) {
	if v.kind == KindObject {
		return v.obj.(Object), true
	}
	return nil, false
}

// Interface returns the payload behind the Value for callers that need to type
// assert on it, such as the runtime reaching for a Func or an Undefined.
func (v Value) Interface() any { return v.obj }
