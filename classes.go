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

// Call reports that gojja2 cannot construct a value from its type object. A
// type is callable in Python, and `is callable` must say so, but there is
// nothing sensible to build here.
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
