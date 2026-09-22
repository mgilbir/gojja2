// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"strings"

	"github.com/mgilbir/gojja2/errs"
	"github.com/mgilbir/gojja2/value"
)

// Every value answers `__class__` with a type object, as it does in Python.
//
// This is a statement about gojja2's own value model and nothing more: a name,
// a repr, and equality. It exposes no further structure, and in particular
// there is no `__subclasses__`, `__globals__` or `__builtins__` behind it --
// the chain those lead to in CPython has no counterpart in Go, and building a
// decoy of it so a sandbox-escape test passes would be worse than not having
// one. See docs/divergences.md.

// classObject is a Python type object.
type classObject struct {
	// qualified is the module-qualified name, as repr shows it.
	qualified string
}

// classOf returns the type object for a value.
func classOf(v value.Value) value.Value {
	return value.FromObject(&classObject{qualified: value.QualifiedTypeName(v)})
}

// name is the bare class name, which is what __name__ reports.
func (c *classObject) name() string {
	if i := strings.LastIndexByte(c.qualified, '.'); i >= 0 {
		return c.qualified[i+1:]
	}
	return c.qualified
}

func (c *classObject) GetAttr(name string) (value.Value, bool) {
	switch name {
	case "__name__", "__qualname__":
		return value.String(c.name()), true
	case "__module__":
		if i := strings.LastIndexByte(c.qualified, '.'); i >= 0 {
			return value.String(c.qualified[:i]), true
		}
		return value.String("builtins"), true
	}
	return value.Undefined, false
}

// Call refuses to construct a value from its type object. A type is callable in
// Python, and `is callable` must say so, but a type object here is inert on
// purpose: the same decision that leaves `__mro__` and `__subclasses__` out.
//
// CPython *would* build one -- `{{ n.__class__() }}` is `0` for an int -- so
// this is a divergence, recorded in docs/divergences.md and graded by
// divergence/class_call_*.
func (c *classObject) Call(*value.CallArgs) (value.Value, error) {
	return value.Undefined, errs.New(errs.TypeError,
		"cannot instantiate %s from a template", c.qualified)
}

// Equals compares type objects by the class they name.
func (c *classObject) Equals(other value.Value) (bool, bool) {
	o, ok := other.Interface().(*classObject)
	if !ok {
		return false, false
	}
	return c.qualified == o.qualified, true
}

func (c *classObject) Repr() string { return "<class '" + c.qualified + "'>" }

func (c *classObject) TypeName() string { return "type" }

func (c *classObject) HashKey() (string, bool) { return "class:" + c.qualified, true }

// notSubscriptable is the TypeError for a value that takes no index at all.
//
// CPython words it differently for a type object -- `type 'float' is not
// subscriptable` rather than `'float' object is not subscriptable` -- and a
// template reaches a type object through `__class__`, so the two wordings are
// both reachable from the same expression:
//
//	{{ (1.5)[1:] }}            'float' object is not subscriptable
//	{{ (1.5).__class__[1:] }}  type 'float' is not subscriptable
func notSubscriptable(v value.Value) error {
	if c, ok := v.Interface().(*classObject); ok {
		return errs.New(errs.TypeError, "type '%s' is not subscriptable", c.name())
	}
	return errs.New(errs.TypeError,
		"'%s' object is not subscriptable", v.TypeName())
}

// noAttribute is the AttributeError for a value that does not carry a name.
//
// CPython words it differently for a type object -- `type object 'bool' has no
// attribute 'items'` rather than `'bool' object has no attribute 'items'` --
// and a template reaches a type object through `__class__`, so both wordings
// are reachable from the same expression. Found by the render differential:
// `{{ true.__class__|dictsort }}` is the shortest spelling.
func noAttribute(v value.Value, name string) error {
	if c, ok := v.Interface().(*classObject); ok {
		return errs.New(errs.AttributeError,
			"type object '%s' has no attribute '%s'", c.name(), name)
	}
	return errs.New(errs.AttributeError,
		"'%s' object has no attribute '%s'", v.TypeName(), name)
}
