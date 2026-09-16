// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package value

import (
	"fmt"

	"github.com/mgilbir/gojja2/errs"
)

// UndefinedBehavior selects which of jinja2's Undefined classes the
// environment hands out for a failed lookup.
type UndefinedBehavior uint8

const (
	// UndefinedDefault is jinja2.Undefined: renders as "", is falsey and
	// has length 0, but errors on arithmetic, attribute access and calls.
	UndefinedDefault UndefinedBehavior = iota
	// UndefinedChainable is jinja2.ChainableUndefined: additionally lets
	// attribute and item access keep returning undefined, so `a.b.c` on a
	// missing `a` is undefined rather than an error.
	UndefinedChainable
	// UndefinedDebug is jinja2.DebugUndefined: renders as the expression
	// that produced it, e.g. "{{ nope }}", instead of "".
	UndefinedDebug
	// UndefinedStrict is jinja2.StrictUndefined: every operation errors,
	// printing and truth-testing included.
	UndefinedStrict
)

// undefinedInfo is the payload of a KindUndefined value: enough provenance to
// reproduce jinja2's error message verbatim.
type undefinedInfo struct {
	behavior UndefinedBehavior
	// hint, when set, replaces the generated message entirely.
	hint string
	// name is the identifier or attribute that was not found.
	name string
	// owner describes the value the lookup was made on, already in
	// jinja2's object_type_repr form ("dict object", "None", ...). Empty
	// when the lookup was a bare name.
	owner string
	// index is set instead of name for a failed integer subscript.
	index    int
	hasIndex bool
}

// NewUndefined returns the undefined produced by a bare name that resolved to
// nothing: `{{ nope }}`.
func NewUndefined(name string) Value {
	return Value{kind: KindUndefined, obj: &undefinedInfo{name: name}}
}

// UndefinedAttr returns the undefined produced by a missing attribute or
// string key on owner: `{{ d.missing }}`.
func UndefinedAttr(owner Value, name string) Value {
	return Value{kind: KindUndefined, obj: &undefinedInfo{name: name, owner: ObjectTypeRepr(owner)}}
}

// UndefinedIndex returns the undefined produced by an out-of-range integer
// subscript on owner: `{{ list[5] }}`.
func UndefinedIndex(owner Value, i int) Value {
	return Value{kind: KindUndefined, obj: &undefinedInfo{index: i, hasIndex: true, owner: ObjectTypeRepr(owner)}}
}

// UndefinedHint returns an undefined with a fixed message, as produced by
// jinja2's `default` machinery and by filters that reject their input.
func UndefinedHint(format string, args ...any) Value {
	return Value{kind: KindUndefined, obj: &undefinedInfo{hint: fmt.Sprintf(format, args...)}}
}

// WithBehavior returns the same undefined under a different Undefined class.
// The environment applies this as undefined values are created.
func (v Value) WithBehavior(b UndefinedBehavior) Value {
	if v.kind != KindUndefined {
		return v
	}
	info := *v.undef()
	info.behavior = b
	return Value{kind: KindUndefined, obj: &info}
}

// UndefinedBehavior returns the Undefined class of an undefined value.
func (v Value) UndefinedBehavior() UndefinedBehavior {
	if v.kind != KindUndefined {
		return UndefinedDefault
	}
	return v.undef().behavior
}

func (v Value) undef() *undefinedInfo {
	if info, ok := v.obj.(*undefinedInfo); ok && info != nil {
		return info
	}
	return &undefinedInfo{}
}

// UndefinedError returns the error jinja2 raises when this undefined is used
// somewhere it is not tolerated. The message is reproduced exactly, including
// jinja2's inconsistent quoting: the attribute form quotes the owner, the
// element form does not.
func (v Value) UndefinedError() error {
	info := v.undef()
	switch {
	case info.hint != "":
		return errs.New(errs.UndefinedError, "%s", info.hint)
	case info.owner == "":
		return errs.New(errs.UndefinedError, "'%s' is undefined", info.name)
	case info.hasIndex:
		return errs.New(errs.UndefinedError, "%s has no element %d", info.owner, info.index)
	default:
		return errs.New(errs.UndefinedError, "'%s' has no attribute '%s'", info.owner, info.name)
	}
}

// DebugText is how DebugUndefined renders itself: the expression that failed,
// wrapped back up in print delimiters. Reports false when there is nothing
// useful to show, which is when jinja2 falls back to "".
func (v Value) DebugText() (string, bool) {
	info := v.undef()
	switch {
	case info.hint != "":
		return "", false
	case info.owner == "":
		return "{{ " + info.name + " }}", true
	case info.hasIndex:
		return "", false
	default:
		return "{{ no such element: " + info.owner + "[" + Repr(String(info.name)) + "] }}", true
	}
}

// ObjectTypeRepr describes a value the way jinja2's object_type_repr does,
// for use in undefined messages: "dict object", "str object", "None".
func ObjectTypeRepr(v Value) string {
	switch v.kind {
	case KindNone:
		return "None"
	case KindUndefined:
		return "undefined"
	default:
		return v.TypeName() + " object"
	}
}
