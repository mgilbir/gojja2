// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"github.com/mgilbir/gojja2/errs"
	"github.com/mgilbir/gojja2/value"
)

// Every Python object has __str__, __repr__ and __format__, and jinja2's
// default Environment.getattr is plain getattr(), so a template can call them:
// `{{ (5).__format__("03") }}` is "005" and `{{ nope.__repr__() }}` is
// "Undefined". They are str(), repr() and format() under another spelling, so
// what they answer is already reproduced; what is new here is reaching them as
// attributes.
//
// The bound object is three different things in CPython, and a template can
// tell them apart by `__class__.__name__`, `__qualname__` and `__module__`:
//
//	a slot of a C type      method-wrapper              (5).__str__
//	__format__ of anything  builtin_function_or_method  (5).__format__
//	a def in a Python class method                      nope.__str__
//
// and a slot wrapper's qualified name is the class that *defines* the slot,
// not the receiver's: int inherits object's __str__, so `(5).__str__` is
// object.__str__ while `(5).__repr__` is int.__repr__. __format__ is named
// after the receiver, bool included.
//
// Not answered, and left as they were: a namespace, whose __getattribute__
// refuses every name but its own fields, so CPython has no such attribute on
// one either; and type objects and the descriptors reached through them, whose
// dunders are slot wrappers of the class -- the class-level side recorded in
// docs/divergences.md under Python object introspection.
var valueDunders = map[string]bool{
	"__str__":    true,
	"__repr__":   true,
	"__format__": true,
}

// dunderSource is where one of a receiver's __str__ or __repr__ comes from:
// a C slot, named by the class defining it, or a function written in Python,
// named by its qualified class and its own name.
type dunderSource struct {
	slot    string // "object", "int", ...: a method-wrapper
	pyClass string // "jinja2.runtime.Undefined": a bound method
	pyName  string // the function's own name, when it is not the attribute's
}

// dunderSources answers, for a receiver gojja2 reproduces a Python class for,
// where its __str__ and its __repr__ come from. ok is false for a receiver
// that has no such attribute.
func dunderSources(recv value.Value) (str, repr dunderSource, ok bool) {
	slot := func(class string) dunderSource { return dunderSource{slot: class} }
	py := func(class string) dunderSource { return dunderSource{pyClass: class} }
	object := slot("object")
	switch recv.Kind() {
	case value.KindString:
		if recv.IsSafe() {
			return slot("str"), py("markupsafe.Markup"), true
		}
		return slot("str"), slot("str"), true
	case value.KindBytes:
		return slot("bytes"), slot("bytes"), true
	case value.KindUndefined:
		// Every class's __repr__ is Undefined's, "Undefined". __str__ is
		// Undefined's too, except DebugUndefined's own and
		// StrictUndefined's, which is the shared refusal assigned to a
		// dozen names and keeps the name it was defined under.
		repr := py("jinja2.runtime.Undefined")
		switch recv.UndefinedBehavior() {
		case value.UndefinedDebug:
			return py("jinja2.runtime.DebugUndefined"), repr, true
		case value.UndefinedStrict:
			return dunderSource{pyClass: "jinja2.runtime.Undefined",
				pyName: "_fail_with_undefined_error"}, repr, true
		}
		return py("jinja2.runtime.Undefined"), repr, true
	case value.KindObject:
		switch o := recv.Interface().(type) {
		case *namespaceObject, *classObject, *descriptorObject, *unboundMethodObject:
			return dunderSource{}, dunderSource{}, false
		case *builtinFunc:
			if o.class != "" {
				return dunderSource{}, dunderSource{}, false
			}
			return object, slot(o.TypeName()), true
		case *loopObject:
			return object, py("jinja2.runtime.LoopContext"), true
		case *macroObject:
			return object, py("jinja2.runtime.Macro"), true
		case *templateReference:
			return object, py("jinja2.runtime.TemplateReference"), true
		case *moduleObject:
			return py("jinja2.environment.TemplateModule"),
				py("jinja2.environment.TemplateModule"), true
		case *groupObject:
			return py("jinja2.filters._GroupTuple"), py("jinja2.filters._GroupTuple"), true
		case *mappingProxy:
			return slot("mappingproxy"), slot("mappingproxy"), true
		case *rangeObject, *value.Set, *dictView:
			return object, slot(recv.TypeName()), true
		}
		// Cycler, Joiner and BlockReference define neither, and a host
		// object stands for a Python object that need not either.
		return object, object, true
	}
	// int, float, bool, None, list, tuple, dict: each has a repr slot of
	// its own and inherits object's __str__, which calls it.
	return object, slot(recv.TypeName()), true
}

// dunderMethod resolves `recv.__str__`, `recv.__repr__` and `recv.__format__`
// to a bound callable, or reports that the name is not one of them or the
// receiver has none.
func dunderMethod(s *State, recv value.Value, name string) (value.Value, bool) {
	if !valueDunders[name] {
		return value.Undefined, false
	}
	str, repr, ok := dunderSources(recv)
	if !ok {
		return value.Undefined, false
	}
	bound := recv // a copy, so the closures below do not move recv to the heap
	typeName := recv.TypeName()

	if name == "__format__" {
		// object.__format__ or the type's own, a METH_O builtin either way:
		// keywords are refused first, then the count, then the type, and
		// each message names the receiver's class.
		return Method(name, typeName, "", bound, func(callState *State, args *value.CallArgs) (value.Value, error) {
			if callState == nil {
				callState = s
			}
			if len(args.Kwargs) > 0 {
				return value.Undefined, errs.New(errs.TypeError,
					"%s.__format__() takes no keyword arguments", typeName)
			}
			if n := len(args.Pos); n != 1 {
				return value.Undefined, errs.New(errs.TypeError,
					"%s.__format__() takes exactly one argument (%d given)", typeName, n)
			}
			spec := args.Pos[0]
			if spec.Kind() != value.KindString {
				return value.Undefined, errs.New(errs.TypeError,
					"__format__() argument must be str, not %s", clinicTypeName(spec))
			}
			text, err := value.FormatValue(bound, spec.AsString(), callState.PythonVersion(), callState)
			if err != nil {
				return value.Undefined, err
			}
			// A str, even of a Markup: str.__format__ builds a new
			// exact str, so it is escaped where it is printed.
			return value.String(text), nil
		}), true
	}

	src := str
	convert := func(st *State) (value.Value, error) {
		text, err := strictStrFor(bound, st.PythonVersion())
		if err != nil {
			return value.Undefined, err
		}
		// str.__str__ of a Markup is an exact str copy, not the Markup.
		return value.String(text), nil
	}
	if name == "__repr__" {
		src = repr
		convert = func(st *State) (value.Value, error) {
			text, err := value.ReprErr(bound, st.PythonVersion())
			if err != nil {
				return value.Undefined, err
			}
			return value.String(text), nil
		}
	}

	if src.slot != "" {
		return methodWrapper(name, src.slot, typeName, bound, func(callState *State, args *value.CallArgs) (value.Value, error) {
			if callState == nil {
				callState = s
			}
			// wrapper_call refuses keywords before the slot's own
			// wrap_unaryfunc counts the positionals.
			if len(args.Kwargs) > 0 {
				return value.Undefined, errs.New(errs.TypeError,
					"wrapper %s() takes no keyword arguments", name)
			}
			if n := len(args.Pos); n > 0 {
				return value.Undefined, errs.New(errs.TypeError,
					"expected 0 arguments, got %d", n)
			}
			return convert(callState)
		}), true
	}

	fnName := name
	if src.pyName != "" {
		fnName = src.pyName
	}
	return Method(fnName, typeName, src.pyClass, bound, func(callState *State, args *value.CallArgs) (value.Value, error) {
		if callState == nil {
			callState = s
		}
		if src.pyName == "_fail_with_undefined_error" {
			// `def _fail_with_undefined_error(self, *args, **kwargs)`:
			// any call at all is the undefined's own error.
			return value.Undefined, bound.UndefinedError()
		}
		// `def __str__(self)`: Python binds keywords before it counts
		// the positionals, so an unknown keyword is reported first.
		qual := pyClassBareName(src.pyClass) + "." + fnName
		if len(args.Kwargs) > 0 {
			return value.Undefined, errs.New(errs.TypeError,
				"%s() got an unexpected keyword argument '%s'", qual, args.Kwargs[0].Name)
		}
		if n := len(args.Pos); n > 0 {
			return value.Undefined, errs.New(errs.TypeError,
				"%s() takes 1 positional argument but %d were given", qual, n+1)
		}
		return convert(callState)
	}), true
}

// pyClassBareName is a qualified class name without its module.
func pyClassBareName(qualified string) string {
	for i := len(qualified) - 1; i >= 0; i-- {
		if qualified[i] == '.' {
			return qualified[i+1:]
		}
	}
	return qualified
}

// methodWrapper is a bound slot of a C type: `(5).__str__`, a method-wrapper
// whose qualified name is the class defining the slot, slot, and whose repr
// names the receiver's type, recv.
func methodWrapper(name, slot, recv string, self value.Value,
	fn func(s *State, args *value.CallArgs) (value.Value, error)) value.Value {
	return value.FromObject(&builtinFunc{
		name: name, recv: recv, wrapper: slot, self: self, fn: fn,
	})
}
