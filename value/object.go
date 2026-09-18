// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package value

import (
	"iter"
	"math/big"
)

// Object is a Go value exposed to a template.
//
// Attribute access is the only required capability; the optional interfaces
// below add indexing, length, iteration and rendering. This mirrors Python,
// where a type opts into protocols one dunder at a time.
type Object interface {
	// GetAttr resolves `obj.name`. Returning false means "no such
	// attribute", which the runtime turns into Undefined rather than an
	// error, matching jinja2's getattr fallback.
	GetAttr(name string) (Value, bool)
}

// Mapping is an Object that behaves as a dict: `obj[key]`, iteration over
// keys, and participation in filters such as dictsort and items.
type Mapping interface {
	Object
	GetItem(key Value) (Value, bool)
	Keys() []Value
	Len() int
}

// Sequence is an Object that behaves as a list: `obj[i]` by integer index.
type Sequence interface {
	Object
	Len() int
	GetIndex(i int) (Value, bool)
}

// Iterable is an Object that can be walked by a for loop. Objects that are
// only Iterable -- not Sequence -- behave like a Python generator: they have
// no length and cannot be indexed.
type Iterable interface {
	Object
	Iterate() iter.Seq[Value]
}

// Slicer lets an Object answer a slice itself, for types where the result is
// not simply a list of the selected elements.
type Slicer interface {
	Object
	Slice(start, stop, step *int) (Value, error)
}

// TupleView is an Object that stands for a Python tuple subclass, so it is
// serialised and treated as the tuple it represents. A range is a sequence but
// not a tuple, which is why this is opt-in rather than inferred from Sequence.
type TupleView interface {
	Object
	AsTuple() Value
}

// Sized is an Object that has a length but cannot be indexed: Python's __len__
// without __getitem__. len() answers for it, and `is sequence` does not, which
// is the pair jinja2's LoopContext presents.
type Sized interface {
	Object
	Len() int
}

// Equaler lets an Object decide == for itself. The second result reports
// whether it has an opinion; without one, objects compare by identity.
//
// Python types that compare by value rather than by identity need this: two
// separately built ranges over the same numbers are equal.
type Equaler interface {
	Object
	Equals(other Value) (equal bool, known bool)
}

// Reprer overrides how an Object renders. Repr is Python's repr(), used inside
// containers; Str is Python's str(), used when the value is printed on its own.
type Reprer interface {
	Repr() string
}

// Strer overrides str(obj). An Object that implements neither falls back to
// its Repr, exactly as Python's object.__str__ does.
type Strer interface {
	Str() string
}

// HTMLer is an Object that carries its own escaped form, which is Python's
// __html__. markupsafe's escape() asks for that rather than escaping str(obj),
// and jinja2's `escaped` test is exactly "has one". A str says the same thing
// by being Markup, so only an Object needs the interface.
type HTMLer interface {
	HTML() string
}

// BigLener is an Object whose length can exceed an int.
//
// Python's range can: range(-2**63, 2**63-1) holds 2**64-1 elements, more than
// any Go slice can index. Len saturates so that indexing and iteration stay
// well defined, and BigLen carries the exact count that len() has to report.
// An Object implementing this must keep the two consistent: BigLen is the
// truth, and Len is BigLen clamped into an int.
type BigLener interface {
	BigLen() *big.Int
}

// Container is an Object that answers `x in obj` itself.
//
// It exists for the types where scanning is the wrong algorithm rather than
// merely a slow one: Python's range decides membership by arithmetic, so
// `{{ 5 in range(10000000000) }}` is a division and not a walk of ten billion
// elements. The second result reports whether the object has an opinion;
// without one the generic scan runs, charged element by element.
type Container interface {
	Object
	Contains(item Value) (found, known bool)
}

// Booler overrides truthiness. Without it an Object is truthy unless it is a
// Mapping or Sequence, in which case emptiness decides -- Python's __bool__
// then __len__ fallback.
type Booler interface {
	IsTrue() bool
}

// FromObject wraps an Object as a Value.
func FromObject(o Object) Value { return Value{kind: KindObject, obj: o} }

// Kwarg is one keyword argument, kept in source order because some callables
// care -- `dict(b=1, a=2)` renders its keys in the order they were written.
type Kwarg struct {
	Name  string
	Value Value
}

// CallArgs is an argument list at a call site.
type CallArgs struct {
	Pos    []Value
	Kwargs []Kwarg
}

// Kwarg returns the named keyword argument.
func (a *CallArgs) Kwarg(name string) (Value, bool) {
	for _, kw := range a.Kwargs {
		if kw.Name == name {
			return kw.Value, true
		}
	}
	return Undefined, false
}

// Arg returns the i'th positional argument.
func (a *CallArgs) Arg(i int) (Value, bool) {
	if i < 0 || i >= len(a.Pos) {
		return Undefined, false
	}
	return a.Pos[i], true
}

// Caller is an Object that can be called from a template: a macro, a global
// function, or `caller` inside a call block.
type Caller interface {
	Object
	Call(args *CallArgs) (Value, error)
}
