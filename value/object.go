// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package value

import "iter"

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
