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
	// keyRepr is set instead of name when the subscript that failed was
	// not a string: an out-of-range index, a key the container does not
	// take at all, or a slice. jinja2 keys both the message and
	// DebugUndefined's rendering off whether the name is a str, so the two
	// cannot be one field. It holds the rendered repr rather than the
	// value, because that is the only thing either form does with it --
	// and because a slice has no Value here to hold.
	keyRepr string
	hasKey  bool
	// nameRepr overrides how the name is written, and is set only by an
	// Undefined a *template* constructed through `__class__`. jinja2
	// formats the name with !r, so one built with no name at all says
	// "None is undefined" and prints as "{{ None }}" under DebugUndefined.
	// Every undefined the engine builds for itself has a real name, and
	// for those the quoting is the same either way -- which is why this
	// stayed unnoticed until a template could call the class.
	nameRepr string
	// excRefusal is the error that replaces this undefined's own, set only
	// when a template constructed the Undefined with a fourth argument that
	// cannot be called. jinja2 raises with `exc(message)`, so a non-callable
	// one fails before the message is ever used.
	//
	// It arrives already built, because what counts as callable is a
	// question about macros and globals that this package cannot answer.
	excRefusal error
}

// UndefinedConstructed returns the undefined jinja2's Undefined(hint, obj,
// name) builds, which is what a template gets from `{{ nope.__class__(...) }}`.
//
// It picks between the same three messages every other undefined here uses, so
// this is a binding rather than a fourth form. jinja2's rule, in order: a
// *truthy* hint is the whole message; with no obj the name stands alone; with an
// obj, a string name is an attribute and anything else is an element.
//
// What is new is that every argument may be absent, and jinja2 writes the name
// with !r -- so a nameless one is "None is undefined" rather than the "” is
// undefined" an empty name would give. Nothing the engine builds for itself is
// nameless, which is why that only mattered once a template could call the
// class.
func UndefinedConstructed(hint Value, obj Value, hasObj bool, name Value,
	excRefusal error) Value {
	info := &undefinedInfo{excRefusal: excRefusal}
	switch {
	case isTruthyHint(hint):
		info.hint = Str(hint)
	case !hasObj:
		// Two forms of the same name, because jinja2 writes it two
		// ways: the error quotes it with !r and DebugUndefined's
		// __str__ prints it raw, so `Undefined(name='zz')` is "'zz' is
		// undefined" and "{{ zz }}".
		info.name, info.nameRepr = Str(name), Repr(name)
	case name.IsString():
		info.owner, info.name = ObjectTypeRepr(obj), Str(name)
	default:
		info.owner, info.keyRepr, info.hasKey = ObjectTypeRepr(obj), Repr(name), true
	}
	return Value{kind: KindUndefined, obj: info}
}

// isTruthyHint reports whether a hint replaces the message. jinja2 tests the
// hint for truth rather than for presence, so Undefined(hint=None) falls
// through to the name form and says "None is undefined".
func isTruthyHint(hint Value) bool {
	ok, err := IsTrue(hint)
	return err == nil && ok
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
	return UndefinedElement(owner, Int(int64(i)))
}

// UndefinedElement returns the undefined produced by a subscript whose key is
// not one the container takes at all: `{{ list[none] }}`. jinja2's getitem
// catches the TypeError and hands back an undefined, and the repr of the key
// is what it names -- "list object has no element None".
func UndefinedElement(owner, key Value) Value {
	return Value{kind: KindUndefined, obj: &undefinedInfo{
		keyRepr: Repr(key), hasKey: true, owner: ObjectTypeRepr(owner),
	}}
}

// UndefinedSlice returns the undefined produced by a slice the container will
// not take: `{{ "ab"[1:"x"] }}`. The key is a Python slice object, which has
// no Value here, so it is rendered as Python reprs one -- every bound present,
// an omitted one as None.
func UndefinedSlice(owner, start, stop, step Value) Value {
	return Value{kind: KindUndefined, obj: &undefinedInfo{
		keyRepr: "slice(" + Repr(start) + ", " + Repr(stop) + ", " + Repr(step) + ")",
		hasKey:  true, owner: ObjectTypeRepr(owner),
	}}
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
	// jinja2 raises with `self._undefined_exception(message)`, and a
	// template can replace that class through the fourth argument of
	// Undefined(...). Calling something that is not callable fails before
	// the message is ever used, so this comes first.
	//
	// A *callable* exc is not reproduced: jinja2 calls it at the raise, with
	// side effects and all, and this has no evaluator here to call it with.
	// Calling it at construction instead would run it for an undefined that
	// is never used, which is worse than not calling it. Recorded in
	// docs/divergences.md.
	if info.excRefusal != nil {
		return info.excRefusal
	}
	switch {
	case info.hint != "":
		return errs.New(errs.UndefinedError, "%s", info.hint)
	case info.owner == "":
		if info.nameRepr != "" {
			return errs.New(errs.UndefinedError, "%s is undefined", info.nameRepr)
		}
		return errs.New(errs.UndefinedError, "'%s' is undefined", info.name)
	case info.hasKey:
		return errs.New(errs.UndefinedError, "%s has no element %s", info.owner, info.keyRepr)
	default:
		return errs.New(errs.UndefinedError, "'%s' has no attribute '%s'", info.owner, info.name)
	}
}

// DebugText is how DebugUndefined renders itself: the expression that failed,
// wrapped back up in print delimiters. Every undefined has one -- jinja2's
// DebugUndefined.__str__ has no fallback to "" -- and which of the three forms
// it takes is decided exactly as the error message is, by whether there is a
// hint, an owner, and a key that is not a string.
func (v Value) DebugText() string {
	info := v.undef()
	switch {
	case info.hint != "":
		return "{{ undefined value printed: " + info.hint + " }}"
	case info.owner == "":
		// The name raw, never the repr: jinja2's __str__ interpolates
		// it while the error message writes it with !r, so the same
		// undefined is "{{ zz }}" here and "'zz' is undefined" there.
		return "{{ " + info.name + " }}"
	case info.hasKey:
		return "{{ no such element: " + info.owner + "[" + info.keyRepr + "] }}"
	default:
		return "{{ no such element: " + info.owner + "[" + Repr(String(info.name)) + "] }}"
	}
}

// undefinedClassNames are the Python classes behind each Undefined flavour.
var undefinedClassNames = [...]string{
	UndefinedDefault:   "Undefined",
	UndefinedChainable: "ChainableUndefined",
	UndefinedDebug:     "DebugUndefined",
	UndefinedStrict:    "StrictUndefined",
}

// ClassName is the Python class name of an undefined value, as it appears in
// messages such as "'Undefined' object cannot be interpreted as an integer".
func (v Value) ClassName() string {
	if v.kind != KindUndefined {
		return v.TypeName()
	}
	return undefinedClassNames[v.undef().behavior]
}

// QualifiedTypeName is a value's Python class name, module-qualified when the
// class does not live in builtins. It is what `__class__` reports.
func QualifiedTypeName(v Value) string {
	switch v.kind {
	case KindUndefined:
		return "jinja2.runtime." + v.ClassName()
	case KindString:
		if v.safe {
			return "markupsafe.Markup"
		}
		return "str"
	case KindObject:
		if q, ok := v.obj.(interface{ QualifiedName() string }); ok {
			return q.QualifiedName()
		}
	}
	return v.TypeName()
}

// ObjectTypeRepr describes a value the way jinja2's object_type_repr does,
// for use in undefined messages: "dict object", "str object", "None". The
// undefined classes live in jinja2.runtime rather than in builtins, so they
// are qualified.
func ObjectTypeRepr(v Value) string {
	switch v.kind {
	case KindNone:
		return "None"
	case KindUndefined:
		return "jinja2.runtime." + v.ClassName() + " object"
	case KindString:
		if v.safe {
			return "markupsafe.Markup object"
		}
	case KindObject:
		// object_type_repr qualifies a class outside builtins with its
		// module, so a Joiner is "jinja2.utils.Joiner object" here even
		// though a type error names it plain "Joiner".
		if q, ok := v.obj.(interface{ QualifiedName() string }); ok {
			return q.QualifiedName() + " object"
		}
	}
	return v.TypeName() + " object"
}

// StrictRefusal returns the error a StrictUndefined raises for an operation it
// refuses, and nil for every other value.
//
// StrictUndefined adds exactly five things to Undefined: __str__, __iter__,
// __len__, __bool__/__eq__/__ne__/__hash__ and __contains__. Everything else
// an undefined refuses -- arithmetic, calls, subscripts, ordering -- Undefined
// already refuses whatever the class, and fails at its own site. So this is
// the whole of the difference, and the operations above are the only ones that
// have to ask.
func StrictRefusal(v Value) error {
	if v.kind == KindUndefined && v.undef().behavior == UndefinedStrict {
		return v.UndefinedError()
	}
	return nil
}
