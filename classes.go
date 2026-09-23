// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"slices"
	"strings"

	"github.com/mgilbir/gojja2/errs"
	"github.com/mgilbir/gojja2/value"
)

// Every value answers `__class__` with a type object, as it does in Python.
//
// A type object here has a name, a repr, equality, and the constructor its class
// carries: `{{ n.__class__('42') }}` is 42, exactly as in Python, because what
// it builds is an ordinary value and leads nowhere further.
//
// It exposes no other structure, and in particular there is no `__mro__`,
// `__subclasses__`, `__globals__` or `__builtins__` behind it -- the chain
// those lead to in CPython has no counterpart in Go, and building a decoy of it
// so a sandbox-escape test passes would be worse than not having one. That is
// the line: a constructor is a value, and the rest is a route out.
// See docs/divergences.md.

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

// classProbes is one value of each class a type object can name, used to ask
// whether the class has a method without having an instance of it.
//
// Python's class attributes are the methods themselves, unbound: dict.items is
// a value, and dict.items(d) is d.items(). There is no table of "the methods of
// dict" here to read -- builtinMethod picks one from the receiver's kind and
// numericAttr answers for numbers -- so the way to ask is to look the name up
// on a value that *is* one, and then require the caller's self to be one too.
var classProbes = map[string]value.Value{
	"str":   value.String(""),
	"bytes": value.Bytes(nil),
	"dict":  value.NewDict(),
	"list":  value.NewList(),
	"tuple": value.NewTuple(),
	"int":   value.Int(0),
	"float": value.Float(0),
	"bool":  value.Bool(false),
}

// unboundMethod is `T.m`: the method with self supplied at the call.
//
//	{{ d.__class__.items }}      <method 'items' of 'dict' objects>
//	{{ d.__class__.items(d) }}   dict_items([('a', 1)])
//	{{ d.__class__.items() }}    unbound method dict.items() needs an argument
//	{{ d.__class__.items(lst) }} descriptor 'items' for 'dict' objects does not
//	                             apply to a 'list' object
//
// Everything past the first argument is the method's own, so an arity error
// comes from the method rather than from here.
func (c *classObject) unboundMethod(name string) (value.Value, bool) {
	probe, ok := classProbes[c.qualified]
	if !ok {
		return value.Undefined, false
	}
	bound, ok := lookupAttr(nil, probe, name)
	if !ok {
		return value.Undefined, false
	}
	if classLevelNames[name] {
		// Not a descriptor: the probe supplied a receiver the callable
		// ignores, so this is the same callable an instance answers.
		return bound, true
	}
	return value.FromObject(&unboundMethodObject{
		class: methodOwner(c.qualified), name: name,
	}), true
}

// classLevelNames are the names CPython declares classmethod or staticmethod on
// the classes probed above. They take no instance -- `bytes.fromhex('01')` is
// exactly `b”.fromhex('01')` -- so reaching one through a type object must not
// make it eat its first argument as a receiver. `str.maketrans` showed why:
// treated as a descriptor it consumed the source string and then complained
// about the argument that was left.
//
// Consulted only after the probe found the name on that class, so a name here
// cannot shadow another class's instance method of the same spelling.
var classLevelNames = map[string]bool{
	"fromhex":    true, // bytes, float
	"fromkeys":   true, // dict
	"from_bytes": true, // int
	"maketrans":  true, // str
}

// methodOwner names the class a descriptor belongs to, which is not always the
// class it was reached through. bool defines no methods of its own, so
// `bool.bit_length` *is* `int.bit_length` and says `'int' objects`. That is the
// only inheritance among the probed classes, and it is also why an int is an
// acceptable receiver for a descriptor reached through bool, and a bool for one
// reached through int.
func methodOwner(class string) string {
	if class == "bool" {
		return "int"
	}
	return class
}

// selfMatches reports whether a receiver is an instance of the descriptor's
// class, which for int includes bool.
func selfMatches(self value.Value, class string) bool {
	name := value.QualifiedTypeName(self)
	return name == class || (class == "int" && name == "bool")
}

// unboundMethodObject is what `T.m` evaluates to, and it is a value in its own
// right: it prints, it is defined, and it is callable.
type unboundMethodObject struct{ class, name string }

func (m *unboundMethodObject) GetAttr(string) (value.Value, bool) {
	return value.Undefined, false
}

func (m *unboundMethodObject) TypeName() string { return "method_descriptor" }

func (m *unboundMethodObject) Repr() string {
	return "<method '" + m.name + "' of '" + m.class + "' objects>"
}

func (m *unboundMethodObject) callWith(s *State, args *value.CallArgs) (value.Value, error) {
	if len(args.Pos) == 0 {
		return value.Undefined, errs.New(errs.TypeError,
			"unbound method %s.%s() needs an argument", m.class, m.name)
	}
	// The descriptor belongs to one class, so a receiver of any other is
	// refused before the method runs -- which matters where the other class
	// has a method of the same name, as list and tuple both do for `count`.
	// The lookup cannot then fail, since the probe that built this
	// descriptor found the name on that very class; the one branch covers
	// both rather than carrying a second, unreachable copy of the message.
	self := args.Pos[0]
	bound, ok := lookupAttr(s, self, m.name)
	if !ok || !selfMatches(self, m.class) {
		return value.Undefined, errs.New(errs.TypeError,
			"descriptor '%s' for '%s' objects doesn't apply to a '%s' object",
			m.name, m.class, self.TypeName())
	}
	rest := &value.CallArgs{Pos: args.Pos[1:], Kwargs: args.Kwargs}
	if fn, ok := bound.Interface().(statefulCaller); ok {
		return fn.callWith(s, rest)
	}
	if fn, ok := bound.Interface().(value.Caller); ok {
		return fn.Call(rest)
	}
	return value.Undefined, errs.New(errs.TypeError,
		"'%s' object is not callable", bound.TypeName())
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
	// A class carries its own methods, unbound.
	return c.unboundMethod(name)
}

// callWith constructs a value from its type object, as calling a class does in
// Python: `{{ n.__class__() }}` is `0` and `{{ n.__class__('5') }}` is `5`.
//
// It takes the render rather than satisfying value.Caller, because three of
// these size their result from an argument the template chose -- list(), tuple()
// and bytes(n) -- and a constructor that cannot charge the budget is a way to
// allocate without limit outside any {% for %}. See statefulCaller.
//
// A class with no counterpart to build gets CPython's refusal for a type that
// cannot be instantiated: a dict view says so, and so does the type object for
// a Go value, which is the honest answer rather than a decoy.
func (c *classObject) callWith(s *State, args *value.CallArgs) (value.Value, error) {
	if fn, ok := classConstructors[c.qualified]; ok {
		return fn(s, args)
	}
	return value.Undefined, errs.New(errs.TypeError,
		"cannot create '%s' instances", c.name())
}

// classConstructors is what each reachable class builds.
//
// Keyed by the qualified name because that is what distinguishes the classes a
// template can reach: `str` and `markupsafe.Markup` are two entries, and a
// value converted from Go carries its Go type here and matches none of them.
var classConstructors map[string]func(*State, *value.CallArgs) (value.Value, error)

func init() {
	classConstructors = map[string]func(*State, *value.CallArgs) (value.Value, error){
		"int":                    constructInt,
		"float":                  constructFloat,
		"str":                    constructStr,
		"markupsafe.Markup":      constructMarkup,
		"bool":                   constructBool,
		"NoneType":               constructNone,
		"list":                   constructList,
		"tuple":                  constructTuple,
		"bytes":                  constructBytes,
		"dict":                   globalDict,
		"range":                  globalRange,
		"jinja2.utils.Namespace": globalNamespace,
		"jinja2.utils.Cycler":    globalCycler,
		"jinja2.utils.Joiner":    globalJoiner,
	}
	// An undefined's class builds another undefined. Which class it was
	// decides how the result behaves, and the class name is the only thing
	// that reaches the constructor, so the behaviour is bound here.
	for _, u := range []struct {
		name     string
		behavior value.UndefinedBehavior
	}{
		{"Undefined", value.UndefinedDefault},
		{"ChainableUndefined", value.UndefinedChainable},
		{"DebugUndefined", value.UndefinedDebug},
		{"StrictUndefined", value.UndefinedStrict},
	} {
		behavior := u.behavior
		classConstructors["jinja2.runtime."+u.name] =
			func(_ *State, args *value.CallArgs) (value.Value, error) {
				return constructUndefined(behavior, args)
			}
	}
}

// noKeywords is the refusal for a constructor that binds positionally only.
// Each wording below is CPython's for that class, measured rather than assumed:
// they are not all alike, and `bytes` counts its arguments in a third way again.
func noKeywords(name string, args *value.CallArgs) error {
	if len(args.Kwargs) == 0 {
		return nil
	}
	return errs.New(errs.TypeError, "%s() takes no keyword arguments", name)
}

// atMostOne is the arity of a one-argument constructor.
func atMostOne(name string, args *value.CallArgs) error {
	if err := noKeywords(name, args); err != nil {
		return err
	}
	if len(args.Pos) > 1 {
		return errs.New(errs.TypeError,
			"%s expected at most 1 argument, got %d", name, len(args.Pos))
	}
	return nil
}

// clinicKeyword and clinicArity are the two wordings CPython 3.13 changed for
// the classes whose signature the argument clinic generates. The classes with a
// hand-written signature (float, bool, list, tuple, NoneType) kept theirs.
//
// The two did not move together: the keyword wording changed for int, str *and*
// bytes, while the arity wording changed for int and str only. Measured across
// 3.11 to 3.14 rather than assumed -- assuming they matched put bytes a version
// ahead of itself.
//
// value.KeywordMessageNamesTheCallee and value.ArityMessageIsExpected already
// describe the same change for methods; these are the constructor spellings.
func clinicKeyword(py value.PythonVersion, name, kw string) error {
	if py.KeywordMessageNamesTheCallee() {
		return errs.New(errs.TypeError,
			"%s() got an unexpected keyword argument '%s'", name, kw)
	}
	return errs.New(errs.TypeError,
		"'%s' is an invalid keyword argument for %s()", kw, name)
}

func clinicArity(py value.PythonVersion, name string, most, given int) error {
	plural := "s"
	if most == 1 {
		plural = ""
	}
	if py.ArityMessageIsExpected() {
		return errs.New(errs.TypeError,
			"%s expected at most %d argument%s, got %d", name, most, plural, given)
	}
	return errs.New(errs.TypeError,
		"%s() takes at most %d argument%s (%d given)", name, most, plural, given)
}

func constructInt(s *State, args *value.CallArgs) (value.Value, error) {
	var subject, base value.Value
	haveSubject, haveBase := false, false
	if len(args.Pos) > 0 {
		subject, haveSubject = args.Pos[0], true
	}
	if len(args.Pos) > 1 {
		base, haveBase = args.Pos[1], true
	}
	for _, kw := range args.Kwargs {
		if kw.Name != "base" {
			return value.Undefined, clinicKeyword(s.PythonVersion(), "int", kw.Name)
		}
		base, haveBase = kw.Value, true
	}
	if len(args.Pos) > 2 {
		return value.Undefined, clinicArity(s.PythonVersion(), "int", 2, len(args.Pos))
	}
	if !haveSubject {
		if haveBase {
			return value.Undefined, errs.New(errs.TypeError,
				"int() missing string argument")
		}
		return value.Int(0), nil
	}
	if !haveBase {
		return value.ConstructInt(subject, 10, false, s.PythonVersion())
	}
	b, ok := base.BigInt()
	if !ok || !b.IsInt64() {
		return value.Undefined, errs.New(errs.TypeError,
			"'%s' object cannot be interpreted as an integer", base.TypeName())
	}
	n := b.Int64()
	if n != 0 && (n < 2 || n > 36) {
		return value.Undefined, errs.New(errs.ValueError,
			"int() base must be >= 2 and <= 36, or 0")
	}
	return value.ConstructInt(subject, int(n), true, s.PythonVersion())
}

func constructFloat(s *State, args *value.CallArgs) (value.Value, error) {
	if err := atMostOne("float", args); err != nil {
		return value.Undefined, err
	}
	if len(args.Pos) == 0 {
		return value.Float(0), nil
	}
	return value.ConstructFloat(args.Pos[0], s.PythonVersion())
}

// constructStr is str(object) and str(bytes, encoding[, errors]).
//
// The encoding form is bytes.decode() under a different name, so it is the same
// call: a codec gojja2 does not implement diverges in exactly one place rather
// than two, and the handler wordings are already graded there.
func constructStr(s *State, args *value.CallArgs) (value.Value, error) {
	pos, err := bindConversionArgs(s.PythonVersion(), "str", "object", args, 3)
	if err != nil {
		return value.Undefined, err
	}
	if len(pos) == 0 {
		return value.String(""), nil
	}
	if len(pos) == 1 {
		// strictStr rather than value.Str: a StrictUndefined refuses to
		// become a string, and that refusal is what the template sees.
		text, err := strictStr(pos[0])
		if err != nil {
			return value.Undefined, err
		}
		return value.String(text), nil
	}
	// CPython checks the encoding's own type before it looks at the subject.
	if !pos[1].IsString() {
		return value.Undefined, errs.New(errs.TypeError,
			"str() argument 'encoding' must be str, not %s", pos[1].TypeName())
	}
	switch {
	case pos[0].Kind() == value.KindBytes:
		return methodDecode(s, pos[0], &value.CallArgs{Pos: pos[1:]})
	case pos[0].IsString():
		return value.Undefined, errs.New(errs.TypeError,
			"decoding str is not supported")
	}
	return value.Undefined, errs.New(errs.TypeError,
		"decoding to str: need a bytes-like object, %s found", pos[0].TypeName())
}

// constructMarkup is str() with the result marked safe. Markup does not escape
// what it is handed -- that is the whole point of it -- so `Markup('<b>')`
// stays a tag.
//
// Markup.__new__ wraps str's conversion in its own binding, so the arity and
// keyword refusals are Markup's while anything past them is str's: `Markup(1,
// 2)` is `str() argument 'encoding' must be str, not int`, and `Markup(base=1)`
// is an unexpected keyword -- markupsafe names that parameter `object`, after
// str's, and `base` only in the pure-Python fallback the C build replaces.
func constructMarkup(s *State, args *value.CallArgs) (value.Value, error) {
	if len(args.Pos) > 3 {
		return value.Undefined, errs.New(errs.TypeError,
			"Markup.__new__() takes from 1 to 4 positional arguments "+
				"but %d were given", len(args.Pos)+1)
	}
	for _, kw := range args.Kwargs {
		if !slices.Contains([]string{"object", "encoding", "errors"}, kw.Name) {
			return value.Undefined, errs.New(errs.TypeError,
				"Markup.__new__() got an unexpected keyword argument '%s'",
				kw.Name)
		}
	}
	built, err := constructStr(s, args)
	if err != nil {
		return value.Undefined, err
	}
	return value.Safe(value.Str(built)), nil
}

func constructBool(_ *State, args *value.CallArgs) (value.Value, error) {
	if err := atMostOne("bool", args); err != nil {
		return value.Undefined, err
	}
	if len(args.Pos) == 0 {
		return value.Bool(false), nil
	}
	ok, err := value.IsTrue(args.Pos[0])
	if err != nil {
		return value.Undefined, err
	}
	return value.Bool(ok), nil
}

func constructNone(_ *State, args *value.CallArgs) (value.Value, error) {
	if len(args.Pos) > 0 || len(args.Kwargs) > 0 {
		return value.Undefined, errs.New(errs.TypeError,
			"NoneType takes no arguments")
	}
	return value.None, nil
}

// constructUndefined builds another undefined, binding jinja2's
// Undefined(hint, obj, name, exc) as jinja2 binds it.
//
// None of the four changes what the value *is* -- it is undefined either way --
// but all but the last change what it says when something uses it, and the
// class it is built from decides how it behaves: `{{ nope.__class__() }}` under
// StrictUndefined is a StrictUndefined.
func constructUndefined(behavior value.UndefinedBehavior, args *value.CallArgs) (value.Value, error) {
	if len(args.Pos) > 4 {
		return value.Undefined, errs.New(errs.TypeError,
			"Undefined.__init__() takes from 1 to 5 positional arguments "+
				"but %d were given", len(args.Pos)+1)
	}
	params := []string{"hint", "obj", "name", "exc"}
	// jinja2's defaults are None for all but obj, whose default is the
	// `missing` sentinel -- tracked by have rather than by a value, since
	// None is a legitimate obj. A zero Value here is not None but an
	// undefined, which reprs as "Undefined" and put that in the message.
	bound := []value.Value{value.None, value.None, value.None, value.None}
	have := make([]bool, 4)
	for i, v := range args.Pos {
		bound[i], have[i] = v, true
	}
	for _, kw := range args.Kwargs {
		i := slices.Index(params, kw.Name)
		if i < 0 {
			return value.Undefined, errs.New(errs.TypeError,
				"Undefined.__init__() got an unexpected keyword argument '%s'",
				kw.Name)
		}
		if have[i] {
			return value.Undefined, errs.New(errs.TypeError,
				"Undefined.__init__() got multiple values for argument '%s'",
				kw.Name)
		}
		bound[i], have[i] = kw.Value, true
	}
	// jinja2 raises with `exc(message)`, so a fourth argument that cannot be
	// called fails before the message is used. A callable one is not
	// reproduced: jinja2 calls it at the raise, and calling it here instead
	// would run it for an undefined that is never used. Recorded in
	// docs/divergences.md.
	var excRefusal error
	if have[3] && !isCallableValue(bound[3]) {
		excRefusal = errs.New(errs.TypeError, "'%s' object is not callable",
			bound[3].TypeName())
	}
	// The class is the one that was called, so a StrictUndefined builds one
	// that refuses just as it does.
	built := value.UndefinedConstructed(bound[0], bound[1], have[1], bound[2], excRefusal)
	return built.WithBehavior(behavior), nil
}

func constructSeq(s *State, name string, args *value.CallArgs) ([]value.Value, error) {
	if err := atMostOne(name, args); err != nil {
		return nil, err
	}
	if len(args.Pos) == 0 {
		return nil, nil
	}
	// materializeOr charges the walk, which is why these take the render:
	// `{{ x.__class__(range(10000000000)) }}` has to stop at the budget
	// rather than at whatever memory the process can get.
	return materializeOr(s, args.Pos[0], errs.New(errs.TypeError,
		"'%s' object is not iterable", args.Pos[0].TypeName()))
}

func constructList(s *State, args *value.CallArgs) (value.Value, error) {
	items, err := constructSeq(s, "list", args)
	if err != nil {
		return value.Undefined, err
	}
	return value.NewList(items...), nil
}

func constructTuple(s *State, args *value.CallArgs) (value.Value, error) {
	items, err := constructSeq(s, "tuple", args)
	if err != nil {
		return value.Undefined, err
	}
	return value.NewTuple(items...), nil
}

// constructBytes is bytes(), bytes(count), bytes(iterable of ints) and
// bytes(str, encoding[, errors]).
func constructBytes(s *State, args *value.CallArgs) (value.Value, error) {
	pos, err := bindConversionArgs(s.PythonVersion(), "bytes", "source", args, 3)
	if err != nil {
		return value.Undefined, err
	}
	if len(pos) == 0 {
		return value.Bytes(nil), nil
	}
	if len(pos) > 1 {
		if !pos[1].IsString() {
			return value.Undefined, errs.New(errs.TypeError,
				"bytes() argument 'encoding' must be str, not %s",
				pos[1].TypeName())
		}
		if !pos[0].IsString() {
			return value.Undefined, errs.New(errs.TypeError,
				"encoding without a string argument")
		}
		return methodEncode(s, pos[0], &value.CallArgs{Pos: pos[1:]})
	}
	built, done, err := value.ConstructBytes(pos[0])
	if done {
		return built, err
	}
	if !pos[0].IsInteger() {
		// An iterable of integers. The walk is charged, because its
		// length is the template's to choose.
		items, err := materializeOr(s, pos[0], errs.New(errs.TypeError,
			"cannot convert '%s' object to bytes", pos[0].TypeName()))
		if err != nil {
			return value.Undefined, err
		}
		return value.BytesFromItems(items)
	}
	// A count. Charge it before allocating, for the same reason.
	b, _ := pos[0].BigInt()
	if b.Sign() < 0 {
		return value.Undefined, errs.New(errs.ValueError, "negative count")
	}
	if !b.IsInt64() {
		return value.Undefined, errs.New(errs.OverflowError,
			"cannot fit 'int' into an index-sized integer")
	}
	if err := s.ChargeBytes(b.Int64()); err != nil {
		return value.Undefined, err
	}
	return value.Bytes(make([]byte, b.Int64())), nil
}

// bindConversionArgs binds str's and bytes's shared (object, encoding, errors)
// signature, which unlike the others does accept its parameters by keyword.
func bindConversionArgs(py value.PythonVersion, name, first string, args *value.CallArgs, most int) ([]value.Value, error) {
	names := []string{first, "encoding", "errors"}
	pos := append([]value.Value(nil), args.Pos...)
	if len(pos) > most {
		// bytes kept the older arity wording when int and str changed
		// it, so the two halves of this signature moved apart in 3.13.
		// Generalising them together is wrong, and was.
		if name == "bytes" {
			return nil, errs.New(errs.TypeError,
				"bytes() takes at most %d arguments (%d given)", most, len(pos))
		}
		return nil, clinicArity(py, name, most, len(pos))
	}
	for _, kw := range args.Kwargs {
		i := slices.Index(names, kw.Name)
		if i < 0 {
			return nil, clinicKeyword(py, name, kw.Name)
		}
		if i < len(pos) {
			return nil, errs.New(errs.TypeError,
				"argument for %s() given by name ('%s') and position (%d)",
				name, kw.Name, i+1)
		}
		for len(pos) < i {
			pos = append(pos, value.Undefined)
		}
		pos = append(pos, kw.Value)
	}
	return pos, nil
}

// Equals compares type objects by the class they name.
//
// The other side may be a class *global* rather than another classObject:
// jinja2's `dict`, `range`, `namespace`, `cycler` and `joiner` globals are the
// classes themselves, so `{{ d.__class__ == dict }}` is True there. Comparing
// by name rather than by identity is also what keeps an override honest -- a
// template that rebinds `dict` to something else compares unequal, exactly as
// it would in CPython.
func (c *classObject) Equals(other value.Value) (bool, bool) {
	name, ok := classNameOf(other)
	if !ok {
		return false, false
	}
	return c.qualified == name, true
}

// classNameOf reports the class a value stands for, for the two kinds of type
// object a template can hold: one reached through `__class__`, and one of the
// class globals.
func classNameOf(v value.Value) (string, bool) {
	switch o := v.Interface().(type) {
	case *classObject:
		return o.qualified, true
	case *builtinFunc:
		if o.class != "" {
			return o.class, true
		}
	}
	return "", false
}

// HTMLRefusal reports that this type object carries __html__ and cannot answer
// it, which is true of exactly one class: markupsafe's Markup.
//
// markupsafe defines __html__ as an ordinary method, so the class object holds
// it unbound. `hasattr` says yes -- which is what `is escaped` asks, and it
// answers True -- and calling it with no self raises. So printing the Markup
// class under autoescape fails, where printing it without autoescape is its
// repr, and `|string` is its repr either way.
//
// Found by the render differential: `{% autoescape yes %}{{ ('x'|safe).__class__ }}`.
func (c *classObject) HTMLRefusal() error {
	if c.qualified != "markupsafe.Markup" {
		return nil
	}
	return errs.New(errs.TypeError,
		"Markup.__html__() missing 1 required positional argument: 'self'")
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
