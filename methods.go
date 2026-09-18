// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"errors"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/mgilbir/gojja2/errs"
	"github.com/mgilbir/gojja2/value"
)

// jinja2 exposes real Python objects, so templates reach their methods
// directly: `d.items()`, `s.upper()`, `l.append(x)`. These are the ones
// templates actually use; anything missing shows up as an undefined attribute
// rather than as silently wrong output.

// Every method takes the render state.
//
// It used to be only the handful that walk a caller-supplied iterable, looked
// up from a table of their own -- which meant the question "can this method be
// bounded?" was answered per method, by whoever added it. A method that sizes
// an allocation from one of its arguments needs the budget just as much as one
// that walks a sequence, and several of them were not getting it. s is nil when
// the lookup comes from constant folding, which State.Step and State.Charge
// both handle.

// builtinMethod resolves a method on a built-in type, returning it bound.
func builtinMethod(s *State, recv value.Value, name string) (value.Value, bool) {
	var table map[string]func(*State, value.Value, *value.CallArgs) (value.Value, error)
	switch recv.Kind() {
	case value.KindString:
		table = stringMethods
	case value.KindBytes:
		table = bytesMethods
	case value.KindDict:
		table = dictMethods
	case value.KindList:
		table = listMethods
	case value.KindTuple:
		table = tupleMethods
	case value.KindObject:
		// A tuple subclass inherits tuple's methods, so `g.index(x)`
		// works on a |groupby pair. The receiver is unwrapped with it:
		// the methods read a sequence, and the object itself is not one
		// they know. An attribute the object defines has already won by
		// the time this is reached, which is the Python order -- the
		// subclass's own names shadow the base's.
		if tv, ok := recv.Interface().(value.TupleView); ok {
			table, recv = tupleMethods, tv.AsTuple()
			break
		}
		return value.Undefined, false
	default:
		return value.Undefined, false
	}
	fn, ok := table[name]
	if !ok {
		return value.Undefined, false
	}
	typeName := recv.TypeName()
	return Func(name, func(callState *State, args *value.CallArgs) (value.Value, error) {
		// Prefer the state of the call over the state of the lookup:
		// they are the same render, but a bound method can outlive the
		// expression that produced it.
		if callState == nil {
			callState = s
		}
		if err := checkMethodArity(typeName, name, args); err != nil {
			return value.Undefined, err
		}
		return fn(callState, recv, args)
	}), true
}

// checkMethodArity reports what CPython would say about the *shape* of this
// call, before the method itself looks at anything.
//
// The filters and tests got this treatment first; the methods never did, and
// every one of them accepted whatever it was given. `[1].append("a", 1)` bound
// the extra argument to nothing and answered None, where CPython says
// "list.append() takes exactly one argument (2 given)".
//
// A method gojja2 adds that CPython has not got is left alone: there is no
// signature to bind against and nothing to reproduce.
func checkMethodArity(typeName, name string, args *value.CallArgs) error {
	sig, known := methodSignatures[typeName+"."+name]
	if !known {
		return nil
	}
	// The keyword check comes first, as it does in CPython: a call that is
	// both the wrong length and carrying a bad keyword reports the keyword.
	if !sig.anyKw {
		for _, kw := range args.Kwargs {
			if slices.Contains(sig.kwNames, kw.Name) {
				continue
			}
			if strings.Contains(sig.kwMessage, "%s") {
				return errs.New(errs.TypeError, sig.kwMessage, kw.Name)
			}
			return errs.New(errs.TypeError, "%s", sig.kwMessage)
		}
	}
	n := len(args.Pos)
	switch {
	case n < sig.minArgs:
		return errs.New(errs.TypeError, sig.fewMessage, n)
	case sig.maxArgs >= 0 && n > sig.maxArgs:
		return errs.New(errs.TypeError, sig.manyMessage, n)
	}
	return nil
}

// arg reads a positional or named argument.
func arg(args *value.CallArgs, i int, name string) (value.Value, bool) {
	if v, ok := args.Arg(i); ok {
		return v, true
	}
	if name != "" {
		return args.Kwarg(name)
	}
	return value.Undefined, false
}

// bareStr is the check CPython's string searches and partitions make on their
// first argument. Its message names neither the method nor the parameter --
// "must be str, not int" is the whole of it -- because CPython raises it from a
// helper shared by all of them.
//
// The argument is always there: the arity check runs first and every one of
// these takes at least one.
// clinicStrArg is the numbered form the argument parser generates for a method
// that declares more than one str: "replace() argument 2 must be str, not int".
func clinicStrArg(args *value.CallArgs, i int, method string, position int) (string, error) {
	v, _ := args.Arg(i)
	if v.Kind() != value.KindString {
		return "", errs.New(errs.TypeError,
			"%s() argument %d must be str, not %s", method, position, clinicTypeName(v))
	}
	return v.AsString(), nil
}

func bareStr(v value.Value) (string, error) {
	if v.Kind() != value.KindString {
		return "", errs.New(errs.TypeError, "must be str, not %s", v.TypeName())
	}
	return v.AsString(), nil
}

func intArg(args *value.CallArgs, i int, name string, def int, t cIntType) (int, error) {
	v, ok := arg(args, i, name)
	if !ok || v.IsNone() {
		return def, nil
	}
	return indexOf(v, t)
}

// indexArg is intArg for an argument whose Python default is not None. Only
// *absence* is the default there: an explicit None reaches the C function and
// is refused, which is why `{{ "x"|center(none) }}` is a TypeError and not a
// width of 80.
func indexArg(args *value.CallArgs, i int, name string, def int, t cIntType) (int, error) {
	v, ok := arg(args, i, name)
	if !ok {
		return def, nil
	}
	return indexOf(v, t)
}

// clinicTypeName is the type name CPython's own argument parser prints, which
// is "None" for the None singleton and the type's name for everything else.
// Only the messages that parser generates use it; an older hand-written check
// says "NoneType" like any other type, which is why this is not simply
// TypeName.
func clinicTypeName(v value.Value) string {
	if v.IsNone() {
		return "None"
	}
	return v.TypeName()
}

// cIntType is the C type CPython's argument parser converts an integer
// argument to.
//
// It is not an implementation detail: a template can observe both halves of
// it. The range decides which values are refused -- a C int argument gives up
// at 2**31, long before anything runs out of memory -- and the name is what
// the OverflowError reports.
type cIntType struct {
	name     string
	min, max int64
}

var (
	// cSSizeT is Py_ssize_t, which every length, width, index and count
	// converts to. It is the common case.
	cSSizeT = cIntType{"ssize_t", math.MinInt64, math.MaxInt64}
	// cInt is a plain C int. Only three arguments use it: expandtabs'
	// tabsize, and the two declared `bool(accept={int})` in Argument
	// Clinic -- splitlines' keepends and sorted's reverse -- which are
	// integers rather than truth tests and so carry a range.
	cInt = cIntType{"int", math.MinInt32, math.MaxInt32}
)

// indexOf is Python's __index__ protocol: the conversion every argument used
// as an integer goes through, and the complaint it makes.
func indexOf(v value.Value, t cIntType) (int, error) {
	n, fits := v.Int64()
	if !fits {
		// Int64 reports failure for two unrelated reasons: the value is
		// not an integer at all, or it is an integer too wide for
		// int64. Reporting the second as the first said "'int' object
		// cannot be interpreted as an integer", which contradicts
		// itself -- the object is an int, and the complaint is about
		// how large it is. IsInteger is what tells them apart.
		if v.IsInteger() {
			return 0, overflowsC(t)
		}
		// This is the __index__ protocol's own complaint, which is what
		// CPython raises wherever an argument is used as an integer:
		// str.center, str.zfill, |round, |replace's count, lipsum and
		// the rest all report it in these words. gojja2 had a wording
		// of its own -- "expected an integer, not str" -- that no
		// Python ever produces.
		return 0, errs.New(errs.TypeError,
			"'%s' object cannot be interpreted as an integer", v.TypeName())
	}
	if n < t.min || n > t.max {
		return 0, overflowsC(t)
	}
	return int(n), nil
}

// overflowsC is CPython's wording for an integer outside a C type's range. It
// says "too large" in both directions, including for a negative that is too
// small, which is what CPython does.
func overflowsC(t cIntType) error {
	return errs.New(errs.OverflowError,
		"Python int too large to convert to C %s", t.name)
}

// bytesMethods is what a bytes value answers. There was no table at all, so
// `{{ "é".encode().decode() }}` could not round-trip.
var bytesMethods = map[string]func(*State, value.Value, *value.CallArgs) (value.Value, error){
	"decode": methodDecode,
}

// --- string methods ----------------------------------------------------------

// stringMethods is populated in init rather than in its declaration: format
// reaches back into attribute lookup, which reads this table, and Go rejects
// the initialisation cycle that would create.
var stringMethods map[string]func(*State, value.Value, *value.CallArgs) (value.Value, error)

func init() {
	stringMethods = map[string]func(*State, value.Value, *value.CallArgs) (value.Value, error){
		"upper": func(_ *State, r value.Value, _ *value.CallArgs) (value.Value, error) {
			return value.String(pyUpperString(r.AsString())), nil
		},
		"lower": func(_ *State, r value.Value, _ *value.CallArgs) (value.Value, error) {
			return value.String(pyLowerString(r.AsString())), nil
		},
		"title": func(_ *State, r value.Value, _ *value.CallArgs) (value.Value, error) {
			return value.String(pyTitleString(r.AsString())), nil
		},
		"capitalize": func(_ *State, r value.Value, _ *value.CallArgs) (value.Value, error) {
			return value.String(pyCapitalizeString(r.AsString())), nil
		},
		"swapcase": func(_ *State, r value.Value, _ *value.CallArgs) (value.Value, error) {
			return value.String(pySwapcaseString(r.AsString())), nil
		},
		"casefold": func(_ *State, r value.Value, _ *value.CallArgs) (value.Value, error) {
			return value.String(pyCasefold(r.AsString())), nil
		},

		"strip":  trimMethod("strip", strings.Trim, strings.TrimFunc),
		"lstrip": trimMethod("lstrip", strings.TrimLeft, strings.TrimLeftFunc),
		"rstrip": trimMethod("rstrip", strings.TrimRight, strings.TrimRightFunc),

		"split":      splitMethod(false),
		"rsplit":     splitMethod(true),
		"splitlines": methodSplitlines,
		"join":       methodJoin,
		"replace":    methodReplace,
		"startswith": affixMethod("startswith", strings.HasPrefix),
		"endswith":   affixMethod("endswith", strings.HasSuffix),
		"count":      methodStrCount,
		"find":       findMethod(strings.Index),
		"rfind":      findMethod(strings.LastIndex),
		"index":      indexMethod(strings.Index, "index"),
		"rindex":     indexMethod(strings.LastIndex, "rindex"),
		"format":     methodFormat,
		"format_map": methodFormatMap,
		"zfill":      methodZfill,
		"ljust":      padMethod(padLeftAligned),
		"rjust":      padMethod(padRightAligned),
		"center":     padMethod(padCentered),
		"encode":     methodEncode,

		// Python's three numeric predicates are three different sets,
		// and only isdecimal is a general category. Reading isdigit as
		// Nd made `{{ "\u00b2".isdigit() }}` False where CPython says
		// True, and isalnum inherited it. strclass.go carries what the
		// wider two accept beyond Nd; see tools/oracle/gen_strclass.py.
		"isdecimal": classifyMethod(unicode.IsDigit),
		"isdigit":   classifyMethod(pyIsDigit),
		"isnumeric": classifyMethod(pyIsNumeric),
		"isalpha":   classifyMethod(unicode.IsLetter),
		// str.isalnum is the union of the four, not letters and Nd.
		"isalnum": classifyMethod(func(r rune) bool {
			return unicode.IsLetter(r) || pyIsNumeric(r)
		}),
		"isspace": classifyMethod(unicode.IsSpace),
		"isupper": stringPredicate(isUpperString),
		"islower": stringPredicate(isLowerString),
		// isascii and isprintable are the two that answer True for the
		// empty string, so neither can go through classifyMethod.
		"isascii":      methodIsASCII,
		"isprintable":  methodIsPrintable,
		"istitle":      methodIsTitle,
		"isidentifier": methodIsIdentifier,

		"expandtabs":   methodExpandtabs,
		"maketrans":    methodMaketrans,
		"translate":    methodTranslate,
		"partition":    partitionMethod(false),
		"rpartition":   partitionMethod(true),
		"removeprefix": affixCutMethod("removeprefix", strings.HasPrefix, func(s, a string) string { return s[len(a):] }),
		"removesuffix": affixCutMethod("removesuffix", strings.HasSuffix, func(s, a string) string { return s[:len(s)-len(a)] }),
	}
}

// methodMaketrans is str.maketrans, which only ever builds the table another
// call translates with. It is a static method in Python, so the string it is
// reached through has no say in the result.
//
// One argument is a mapping whose keys are single characters or ordinals; two
// are equal-length strings paired off; a third names characters to delete. The
// table it returns is always keyed by ordinal, which is why translate can look
// a character up without knowing how the table was written.
func methodMaketrans(_ *State, _ value.Value, args *value.CallArgs) (value.Value, error) {
	out := value.NewDict()
	d, _ := out.Dict()
	switch len(args.Pos) {
	case 1:
		src, ok := args.Pos[0].Dict()
		if !ok {
			return value.Undefined, errs.New(errs.TypeError,
				"if you give only one argument to maketrans it must be a dict")
		}
		for _, e := range src.Entries() {
			key, err := transKey(e.Key)
			if err != nil {
				return value.Undefined, err
			}
			if err := d.Set(key, e.Value); err != nil {
				return value.Undefined, err
			}
		}
	case 2, 3:
		// The parser converts the second and third arguments, and only
		// then does the body look at the first -- so a call with two
		// wrong arguments reports the *second*, and the first has a
		// complaint of its own rather than the parser's.
		for i := 1; i < len(args.Pos); i++ {
			if !args.Pos[i].IsString() {
				return value.Undefined, errs.New(errs.TypeError,
					"maketrans() argument %d must be str, not %s",
					i+1, clinicTypeName(args.Pos[i]))
			}
		}
		if !args.Pos[0].IsString() {
			return value.Undefined, errs.New(errs.TypeError,
				"first maketrans argument must be a string if there is a second argument")
		}
		from, to := []rune(value.Str(args.Pos[0])), []rune(value.Str(args.Pos[1]))
		if len(from) != len(to) {
			return value.Undefined, errs.New(errs.ValueError,
				"the first two maketrans arguments must have equal length")
		}
		for i, c := range from {
			if err := d.Set(value.Int(int64(c)), value.Int(int64(to[i]))); err != nil {
				return value.Undefined, err
			}
		}
		if len(args.Pos) == 3 {
			if !args.Pos[2].IsString() {
				return value.Undefined, errs.New(errs.TypeError,
					"third argument to maketrans must be a string")
			}
			for _, c := range value.Str(args.Pos[2]) {
				if err := d.Set(value.Int(int64(c)), value.None); err != nil {
					return value.Undefined, err
				}
			}
		}
	default:
		return value.Undefined, errs.New(errs.TypeError,
			"maketrans() takes 1, 2 or 3 arguments (%d given)", len(args.Pos))
	}
	return out, nil
}

// transKey turns a maketrans key into the ordinal the table is keyed by: a
// one-character string becomes its code point, an integer is already one.
func transKey(k value.Value) (value.Value, error) {
	if k.IsString() {
		rs := []rune(value.Str(k))
		if len(rs) != 1 {
			return value.Undefined, errs.New(errs.ValueError,
				"string keys in translate table must be of length 1")
		}
		return value.Int(int64(rs[0])), nil
	}
	if k.IsInteger() {
		return k, nil
	}
	return value.Undefined, errs.New(errs.TypeError,
		"keys in translate table must be strings or integers")
}

// translateLookup is `table[ord(c)]` with LookupError meaning "leave this
// character alone", which is what str.translate does per character.
func translateLookup(table value.Value, c rune) (value.Value, bool, error) {
	key := value.Int(int64(c))
	switch table.Kind() {
	case value.KindDict:
		d, _ := table.Dict()
		return d.Get(key)
	case value.KindString, value.KindBytes:
		if ch, in := value.StrIndex(table.AsString(), int(c)); in {
			return value.String(ch), true, nil
		}
		return value.Undefined, false, nil
	case value.KindList, value.KindTuple:
		seq, _ := table.Seq()
		if int(c) < seq.Len() {
			return seq.At(int(c)), true, nil
		}
		return value.Undefined, false, nil
	}
	if v, ok := lookupItem(table, key); ok {
		return v, true, nil
	}
	if table.Kind() == value.KindObject {
		return value.Undefined, false, nil
	}
	return value.Undefined, false, errs.New(errs.TypeError,
		"'%s' object is not subscriptable", table.TypeName())
}

// methodTranslate is str.translate: every character is looked up by ordinal and
// replaced by a string, by another ordinal, or by nothing at all when the entry
// is None. A character the table does not mention is left exactly as it was,
// which is why a missing key is not an error.
func methodTranslate(s *State, r value.Value, args *value.CallArgs) (value.Value, error) {
	table, ok := arg(args, 0, "table")
	if !ok {
		return value.Undefined, errs.New(errs.TypeError,
			"translate() takes exactly one argument (0 given)")
	}
	// str.translate is `table[ord(c)]` per character, catching LookupError
	// -- so the table is not converted and not even looked at for an empty
	// string. Anything subscriptable by an integer will do: a str, a list
	// and a tuple all answer for the code points they are long enough to
	// index and leave the rest alone, where requiring a dict refused them
	// outright.
	var b strings.Builder
	for _, c := range r.AsString() {
		repl, found, err := translateLookup(table, c)
		if err != nil {
			return value.Undefined, err
		}
		switch {
		case !found:
			b.WriteRune(c)
		case repl.IsNone():
			// Deleted.
		case repl.IsString():
			if err := s.ChargeBytes(int64(len(value.Str(repl)))); err != nil {
				return value.Undefined, err
			}
			b.WriteString(value.Str(repl))
		case repl.IsInteger():
			n, fits := repl.Int64()
			if !fits || n < 0 || n > unicode.MaxRune {
				return value.Undefined, errs.New(errs.ValueError,
					"character mapping must be in range(0x110000)")
			}
			b.WriteRune(rune(n))
		default:
			return value.Undefined, errs.New(errs.TypeError,
				"character mapping must return integer, None or str")
		}
	}
	if r.IsSafe() {
		return value.Safe(b.String()), nil
	}
	return value.String(b.String()), nil
}

// pyIsDigit is str.isdigit: Nd plus Numeric_Type=Digit.
func pyIsDigit(r rune) bool {
	return unicode.IsDigit(r) || unicode.Is(digitExtra, r)
}

// pyIsNumeric is str.isnumeric: anything carrying a numeric value, which
// reaches past the number categories into CJK ideographs like U+4E00.
func pyIsNumeric(r rune) bool {
	return unicode.IsDigit(r) || unicode.Is(unicode.Nl, r) ||
		unicode.Is(unicode.No, r) || unicode.Is(numericExtra, r)
}

// methodIsASCII is str.isascii, which is True for the empty string: it asks
// whether anything is out of range, and nothing is.
func methodIsASCII(_ *State, r value.Value, _ *value.CallArgs) (value.Value, error) {
	for _, c := range r.AsString() {
		if c > unicode.MaxASCII {
			return value.False, nil
		}
	}
	return value.True, nil
}

// methodIsPrintable is str.isprintable: nothing in Cc, Cf, Cs, Co, Cn, Zl, Zp
// or Zs -- except U+0020, which is the one separator Python calls printable.
// Empty is True, for the same reason isascii is.
func methodIsPrintable(_ *State, r value.Value, _ *value.CallArgs) (value.Value, error) {
	for _, c := range r.AsString() {
		if c == ' ' {
			continue
		}
		if unicode.In(c, unicode.Cc, unicode.Cf, unicode.Cs, unicode.Co,
			unicode.Zl, unicode.Zp, unicode.Zs) || !unicode.IsGraphic(c) {
			return value.False, nil
		}
	}
	return value.True, nil
}

// methodIsTitle is str.istitle: every uppercase letter follows an uncased
// character and every lowercase one follows a cased character, with at least
// one cased character present. Titlecase counts as upper here, which is what
// makes a digraph like U+01C8 titlecase rather than a failure.
func methodIsTitle(_ *State, r value.Value, _ *value.CallArgs) (value.Value, error) {
	return value.Bool(isTitleString(r.AsString())), nil
}

// methodIsIdentifier is str.isidentifier, which asks the same question the
// parser does: XID_Start followed by XID_Continue, with underscore allowed in
// either position. It says nothing about keywords -- "class".isidentifier() is
// True.
func methodIsIdentifier(_ *State, r value.Value, _ *value.CallArgs) (value.Value, error) {
	s := r.AsString()
	if s == "" {
		return value.False, nil
	}
	for i, c := range s {
		if i == 0 {
			if !isXIDStart(c) {
				return value.False, nil
			}
			continue
		}
		if !isXIDContinue(c) {
			return value.False, nil
		}
	}
	return value.True, nil
}

func isXIDStart(c rune) bool {
	return c == '_' || unicode.IsLetter(c) || unicode.Is(unicode.Nl, c)
}

func isXIDContinue(c rune) bool {
	return isXIDStart(c) || unicode.IsDigit(c) ||
		unicode.In(c, unicode.Mn, unicode.Mc, unicode.Pc)
}

// methodExpandtabs is str.expandtabs: each tab advances to the next multiple
// of tabsize, and the column resets at every line break rather than counting
// from the start of the string.
func methodExpandtabs(s *State, r value.Value, args *value.CallArgs) (value.Value, error) {
	size, err := indexArg(args, 0, "tabsize", 8, cInt)
	if err != nil {
		return value.Undefined, err
	}
	var b strings.Builder
	col := 0
	for _, c := range r.AsString() {
		switch c {
		case '\t':
			// A tabsize of zero or less deletes the tab rather than
			// padding to it, which is what Python's loop does.
			if size > 0 {
				pad := size - col%size
				if err := s.ChargeBytes(int64(pad)); err != nil {
					return value.Undefined, err
				}
				b.WriteString(strings.Repeat(" ", pad))
				col += pad
			}
		case '\n', '\r':
			b.WriteRune(c)
			col = 0
		default:
			b.WriteRune(c)
			col++
		}
	}
	if r.IsSafe() {
		return value.Safe(b.String()), nil
	}
	return value.String(b.String()), nil
}

// partitionMethod is str.partition and str.rpartition, which always answer a
// 3-tuple: the separator sits in the middle when it was found, and the two
// empty strings go on whichever side the search came from when it was not.
func partitionMethod(fromRight bool) func(*State, value.Value, *value.CallArgs) (value.Value, error) {
	return func(_ *State, r value.Value, args *value.CallArgs) (value.Value, error) {
		v, _ := arg(args, 0, "sep")
		sep, err := bareStr(v)
		if err != nil {
			return value.Undefined, err
		}
		if sep == "" {
			return value.Undefined, errs.New(errs.ValueError, "empty separator")
		}
		s := r.AsString()
		i := strings.Index(s, sep)
		if fromRight {
			i = strings.LastIndex(s, sep)
		}
		wrap := value.String
		if r.IsSafe() {
			wrap = value.Safe
		}
		if i < 0 {
			// Not found: partition keeps the text on the left,
			// rpartition on the right.
			if fromRight {
				return value.NewTuple(wrap(""), wrap(""), wrap(s)), nil
			}
			return value.NewTuple(wrap(s), wrap(""), wrap("")), nil
		}
		return value.NewTuple(wrap(s[:i]), wrap(sep), wrap(s[i+len(sep):])), nil
	}
}

// affixCutMethod is str.removeprefix and str.removesuffix, which return the
// string unchanged when the affix is absent -- and an empty affix is always
// present, so it is not an error the way partition's is.
func affixCutMethod(name string, has func(string, string) bool, cut func(string, string) string) func(*State, value.Value, *value.CallArgs) (value.Value, error) {
	return func(_ *State, r value.Value, args *value.CallArgs) (value.Value, error) {
		// Each names itself, and the message comes from the argument
		// parser, which calls the None singleton "None".
		v, _ := arg(args, 0, "affix")
		if v.Kind() != value.KindString {
			return value.Undefined, errs.New(errs.TypeError,
				"%s() argument must be str, not %s", name, clinicTypeName(v))
		}
		affix := v.AsString()
		s := r.AsString()
		if has(s, affix) {
			s = cut(s, affix)
		}
		if r.IsSafe() {
			return value.Safe(s), nil
		}
		return value.String(s), nil
	}
}

func trimMethod(name string, withCutset func(string, string) string, withFunc func(string, func(rune) bool) string) func(*State, value.Value, *value.CallArgs) (value.Value, error) {
	return func(_ *State, r value.Value, args *value.CallArgs) (value.Value, error) {
		if v, ok := arg(args, 0, "chars"); ok && !v.IsNone() {
			// Hand-written in CPython, so it names the method and
			// not the type it was given.
			if v.Kind() != value.KindString {
				return value.Undefined, errs.New(errs.TypeError,
					"%s arg must be None or str", name)
			}
			return value.String(withCutset(r.AsString(), v.AsString())), nil
		}
		return value.String(withFunc(r.AsString(), unicode.IsSpace)), nil
	}
}

// splitMethod implements str.split and str.rsplit.
//
// Splitting on no separator is not splitting on " ": Python collapses runs of
// whitespace and drops leading and trailing empties, which is why
// `" a  b ".split()` has two elements and `" a  b ".split(" ")` has five.
func splitMethod(fromRight bool) func(*State, value.Value, *value.CallArgs) (value.Value, error) {
	return func(_ *State, r value.Value, args *value.CallArgs) (value.Value, error) {
		limit, err := indexArg(args, 1, "maxsplit", -1, cSSizeT)
		if err != nil {
			return value.Undefined, err
		}
		sep, hasSep := arg(args, 0, "sep")

		var parts []string
		if !hasSep || sep.IsNone() {
			parts = strings.FieldsFunc(r.AsString(), unicode.IsSpace)
			if limit >= 0 && len(parts) > limit+1 {
				parts = rejoinTail(r.AsString(), parts, limit, fromRight)
			}
		} else {
			if sep.Kind() != value.KindString {
				return value.Undefined, errs.New(errs.TypeError,
					"must be str or None, not %s", sep.TypeName())
			}
			if sep.AsString() == "" {
				return value.Undefined, errs.New(errs.ValueError, "empty separator")
			}
			n := -1
			if limit >= 0 {
				n = limit + 1
			}
			if fromRight && n > 0 {
				parts = splitRightN(r.AsString(), sep.AsString(), n)
			} else {
				parts = strings.SplitN(r.AsString(), sep.AsString(), n)
			}
		}

		items := make([]value.Value, len(parts))
		for i, p := range parts {
			items[i] = value.String(p)
		}
		return value.NewList(items...), nil
	}
}

// rejoinTail re-merges the parts beyond a whitespace split's maxsplit, keeping
// the original spacing of the remainder.
func rejoinTail(src string, parts []string, limit int, fromRight bool) []string {
	if fromRight {
		keep := parts[len(parts)-limit:]
		head := strings.TrimRightFunc(src, unicode.IsSpace)
		for _, p := range keep {
			head = head[:strings.LastIndex(head, p)]
		}
		return append([]string{strings.TrimRightFunc(head, unicode.IsSpace)}, keep...)
	}
	keep := parts[:limit]
	rest := strings.TrimLeftFunc(src, unicode.IsSpace)
	for _, p := range keep {
		rest = rest[strings.Index(rest, p)+len(p):]
	}
	return append(keep, strings.TrimLeftFunc(rest, unicode.IsSpace))
}

func splitRightN(s, sep string, n int) []string {
	all := strings.Split(s, sep)
	if len(all) <= n {
		return all
	}
	head := strings.Join(all[:len(all)-n+1], sep)
	return append([]string{head}, all[len(all)-n+1:]...)
}

func methodSplitlines(_ *State, r value.Value, args *value.CallArgs) (value.Value, error) {
	// keepends is declared `bool(accept={int})`, so it is an integer and
	// not a truth test: `splitlines(none)` is refused where reading it for
	// truth quietly kept nothing.
	keepEnds := false
	if v, ok := arg(args, 0, "keepends"); ok {
		n, err := indexOf(v, cInt)
		if err != nil {
			return value.Undefined, err
		}
		keepEnds = n != 0
	}
	lines := splitLines(r.AsString(), keepEnds)
	items := make([]value.Value, len(lines))
	for i, line := range lines {
		items[i] = value.String(line)
	}
	return value.NewList(items...), nil
}

func methodJoin(st *State, r value.Value, args *value.CallArgs) (value.Value, error) {
	v, ok := arg(args, 0, "iterable")
	if !ok {
		return value.Undefined, errs.New(errs.TypeError, "join() takes exactly one argument")
	}
	seq, err := value.Iterate(v)
	if err != nil {
		// str.join says this and nothing about the type, because it
		// checks by asking for an iterator rather than by type.
		return value.Undefined, errs.New(errs.TypeError, "can only join an iterable")
	}
	var parts []string
	sep := r.AsString()
	for item := range seq {
		if item.Kind() != value.KindString {
			return value.Undefined, errs.New(errs.TypeError,
				"sequence item %d: expected str instance, %s found",
				len(parts), item.TypeName())
		}
		// Charged as the walk goes: the whole sequence is materialised
		// before the join happens, so a bound checked afterwards would
		// never be reached.
		if err := st.Step(1); err != nil {
			return value.Undefined, err
		}
		if err := st.ChargeBytes(int64(len(item.AsString())) + int64(len(sep))); err != nil {
			return value.Undefined, err
		}
		parts = append(parts, item.AsString())
	}
	return value.String(strings.Join(parts, sep)), nil
}

func methodReplace(st *State, r value.Value, args *value.CallArgs) (value.Value, error) {
	// replace numbers its arguments, and the parser's message calls the
	// None singleton "None".
	old, err := clinicStrArg(args, 0, "replace", 1)
	if err != nil {
		return value.Undefined, err
	}
	new, err := clinicStrArg(args, 1, "replace", 2)
	if err != nil {
		return value.Undefined, err
	}
	count, err := indexArg(args, 2, "count", -1, cSSizeT)
	if err != nil {
		return value.Undefined, err
	}
	src := r.AsString()
	if err := chargeReplace(st, src, old, new, count); err != nil {
		return value.Undefined, err
	}
	return value.String(strings.Replace(src, old, new, count)), nil
}

// chargeReplace charges what a replacement is about to produce.
//
// Each operand may be individually legal and individually charged, and their
// product still enormous: `("a" * 60000)|replace("a", "b" * 60000)` is two
// sixty-kilobyte strings and a three-and-a-half gigabyte result. Every guard
// that existed looked at one operand at a time, so nothing looked at this.
func chargeReplace(st *State, src, old, new string, count int) error {
	if len(new) <= len(old) {
		return nil
	}
	hits := int64(strings.Count(src, old))
	if count >= 0 && int64(count) < hits {
		hits = int64(count)
	}
	growth := saturatingMulInt(hits, int64(len(new)-len(old)))
	return st.ChargeBytes(int64(len(src)) + growth)
}

// affixMethod implements startswith and endswith, which accept a tuple of
// candidates as well as a single string.
func affixMethod(name string, match func(string, string) bool) func(*State, value.Value, *value.CallArgs) (value.Value, error) {
	return func(_ *State, r value.Value, args *value.CallArgs) (value.Value, error) {
		v, ok := arg(args, 0, "prefix")
		if !ok {
			return value.Undefined, errs.New(errs.TypeError, "missing required argument")
		}
		within, _, err := strSliceBounds(r.AsString(), args, 1)
		if err != nil {
			return value.Undefined, err
		}
		if s, ok := v.Seq(); ok && v.Kind() == value.KindTuple {
			for _, cand := range s.Items() {
				if cand.Kind() == value.KindString && match(within, cand.AsString()) {
					return value.True, nil
				}
			}
			return value.False, nil
		}
		if v.Kind() != value.KindString {
			return value.Undefined, errs.New(errs.TypeError,
				"%s first arg must be str or a tuple of str, not %s",
				name, v.TypeName())
		}
		return value.Bool(match(within, v.AsString())), nil
	}
}

// strSliceBounds reads the optional start and end that every string search
// takes -- count, find, rfind, index, rindex, startswith, endswith -- and
// returns the slice they select, with the character offset of its start so a
// found position can be reported against the whole string.
//
// These were ignored outright, so `{{ "Hello World".find("o", 1, 2) }}` was 4
// where Python says -1: not a wording difference but a wrong answer. The bounds
// are slice indices, counted in characters and clamped the way a slice clamps,
// and anything that is not an integer or None is refused in the words CPython
// uses for a slice.
func strSliceBounds(r string, args *value.CallArgs, first int) (string, int, error) {
	runes := []rune(r)
	n := len(runes)
	read := func(i, def int) (int, error) {
		v, ok := args.Arg(i)
		if !ok || v.IsNone() {
			return def, nil
		}
		// The same conversion the subscript path uses, so that the two
		// cannot drift apart again: an integer too wide for the machine
		// saturates and is clamped below, rather than being refused as
		// though it were not an integer at all.
		idx, err := sliceIndexOf(v)
		if err != nil {
			return 0, err
		}
		// A slice index counts from the end when negative, and is
		// clamped rather than refused when out of range. Adding n to a
		// saturated MinInt cannot wrap: it only moves toward zero.
		if idx < 0 {
			idx += n
			if idx < 0 {
				idx = 0
			}
		}
		if idx > n {
			idx = n
		}
		return idx, nil
	}
	start, err := read(first, 0)
	if err != nil {
		return "", 0, err
	}
	end, err := read(first+1, n)
	if err != nil {
		return "", 0, err
	}
	if end < start {
		end = start
	}
	return string(runes[start:end]), start, nil
}

func methodStrCount(_ *State, r value.Value, args *value.CallArgs) (value.Value, error) {
	// The bounds are converted while the call is parsed, so they are
	// refused before the substring is looked at: `"ab".count(1, 1.5)` is
	// about the 1.5.
	within, _, err := strSliceBounds(r.AsString(), args, 1)
	if err != nil {
		return value.Undefined, err
	}
	v, _ := arg(args, 0, "sub")
	sub, err := bareStr(v)
	if err != nil {
		return value.Undefined, err
	}
	return value.Int(int64(strings.Count(within, sub))), nil
}

// findMethod returns a code-point index, or -1, the way str.find does.
func findMethod(search func(string, string) int) func(*State, value.Value, *value.CallArgs) (value.Value, error) {
	return func(_ *State, r value.Value, args *value.CallArgs) (value.Value, error) {
		within, offset, err := strSliceBounds(r.AsString(), args, 1)
		if err != nil {
			return value.Undefined, err
		}
		v, _ := arg(args, 0, "sub")
		sub, err := bareStr(v)
		if err != nil {
			return value.Undefined, err
		}
		at := search(within, sub)
		if at < 0 {
			return value.Int(-1), nil
		}
		// The answer is an index into the whole string, so the slice's
		// own start goes back on.
		return value.Int(int64(offset + value.StrLen(within[:at]))), nil
	}
}

func indexMethod(search func(string, string) int, name string) func(*State, value.Value, *value.CallArgs) (value.Value, error) {
	find := findMethod(search)
	return func(s *State, r value.Value, args *value.CallArgs) (value.Value, error) {
		v, err := find(s, r, args)
		if err != nil {
			return value.Undefined, err
		}
		if i, _ := v.Int64(); i < 0 {
			return value.Undefined, errs.New(errs.ValueError, "substring not found")
		}
		return v, nil
	}
}

// methodFormat implements str.format for the positional and named forms
// templates use. Format specs beyond a bare field name are not supported.
//
// Formatting a Markup string escapes every substituted value and yields
// Markup, which is markupsafe's whole point: `("a{}"|safe).format("<x>")`
// renders the escaped "<x>" rather than raw markup, so marking a *template*
// safe does not mark its arguments safe.
func methodFormat(st *State, r value.Value, args *value.CallArgs) (value.Value, error) {
	return formatWith(st, r, func(name string, auto *int) (value.Value, error) {
		return resolveFieldBase(name, args, auto)
	})
}

// fieldBase resolves what the base of a replacement field names, before any
// `.attr` or `[key]` steps. str.format reads it out of the call's arguments;
// str.format_map subscripts the one mapping it was handed.
type fieldBase func(name string, auto *int) (value.Value, error)

func formatWith(st *State, r value.Value, base fieldBase) (value.Value, error) {
	var b strings.Builder
	s := r.AsString()
	safe := r.IsSafe()
	auto := 0
	for i := 0; i < len(s); {
		switch {
		case strings.HasPrefix(s[i:], "{{"):
			b.WriteByte('{')
			i += 2
		case strings.HasPrefix(s[i:], "}}"):
			b.WriteByte('}')
			i += 2
		case s[i] == '{':
			field, conv, spec, next, err := splitReplacement(s, i)
			if err != nil {
				return value.Undefined, err
			}
			i = next
			v, err := resolveFormatField(field, base, &auto)
			if err != nil {
				return value.Undefined, err
			}
			// A spec may itself hold replacement fields -- `{:{w}.{p}f}`
			// -- which are resolved against the same arguments before
			// the spec is read.
			if strings.IndexByte(spec, '{') >= 0 {
				spec, err = expandSpec(spec, base, &auto)
				if err != nil {
					return value.Undefined, err
				}
			}
			text, err := convertAndFormat(st, v, conv, spec)
			if err != nil {
				return value.Undefined, err
			}
			if safe && !v.IsSafe() {
				b.WriteString(escapeHTML(text))
			} else {
				b.WriteString(text)
			}
		case s[i] == '}':
			return value.Undefined, errs.New(errs.ValueError,
				"Single '}' encountered in format string")
		default:
			b.WriteByte(s[i])
			i++
		}
	}
	if safe {
		return value.Safe(b.String()), nil
	}
	return value.String(b.String()), nil
}

// splitReplacement reads one `{...}` field, returning its name, its conversion
// and its format spec.
//
// It cannot simply look for the next '}': a spec may hold replacement fields of
// its own, so `{:{w}}` ends at the second one. Nesting is one level deep in
// Python, which is what the depth counter here allows.
func splitReplacement(s string, start int) (field, conv, spec string, next int, err error) {
	depth, i, nested := 0, start, false
	for ; i < len(s); i++ {
		if s[i] == '{' {
			depth++
			if depth > 1 {
				nested = true
			}
			continue
		}
		if s[i] == '}' {
			depth--
			if depth == 0 {
				break
			}
		}
	}
	if depth != 0 {
		// Three different complaints, depending on how far it got: a
		// lone brace, a field that named something and never closed,
		// and a spec whose own nested field never closed.
		switch {
		case nested:
			return "", "", "", 0, errs.New(errs.ValueError,
				"unmatched '{' in format spec")
		case i > start+1:
			return "", "", "", 0, errs.New(errs.ValueError,
				"expected '}' before end of string")
		default:
			return "", "", "", 0, errs.New(errs.ValueError,
				"Single '{' encountered in format string")
		}
	}
	body := s[start+1 : i]
	next = i + 1

	// The spec starts at the first ':' that is not inside the [] of a field
	// name -- `{a[1:2]}` indexes, it does not format.
	bracket := 0
	for j := 0; j < len(body); j++ {
		switch body[j] {
		case '[':
			bracket++
		case ']':
			bracket--
		case ':':
			if bracket == 0 {
				field, spec = body[:j], body[j+1:]
				goto split
			}
		}
	}
	field = body
split:
	// The conversion sits between the name and the spec, and only there.
	if k := strings.IndexByte(field, '!'); k >= 0 {
		field, conv = field[:k], field[k+1:]
		if conv == "" {
			return "", "", "", 0, errs.New(errs.ValueError,
				"unmatched '{' in format spec")
		}
	}
	return field, conv, spec, next, nil
}

// expandSpec resolves the replacement fields inside a format spec, so the width
// and precision in `{:{w}.{p}f}` can come from the arguments.
func expandSpec(spec string, base fieldBase, auto *int) (string, error) {
	var b strings.Builder
	for i := 0; i < len(spec); {
		if spec[i] != '{' {
			b.WriteByte(spec[i])
			i++
			continue
		}
		end := strings.IndexByte(spec[i:], '}')
		if end < 0 {
			return "", errs.New(errs.ValueError, "unmatched '{' in format spec")
		}
		v, err := resolveFormatField(spec[i+1:i+end], base, auto)
		if err != nil {
			return "", err
		}
		b.WriteString(value.Str(v))
		i += end + 1
	}
	return b.String(), nil
}

// convertAndFormat applies `!r`, `!s` or `!a` and then the format spec, in that
// order -- the conversion replaces the value with its text, and the spec lays
// that text out.
func convertAndFormat(st *State, v value.Value, conv, spec string) (string, error) {
	switch conv {
	case "":
	case "s":
		v = value.String(value.Str(v))
	case "r":
		v = value.String(value.Repr(v))
	case "a":
		v = value.String(value.Ascii(v))
	default:
		return "", errs.New(errs.ValueError,
			"Unknown conversion specifier %s", conv)
	}
	return value.FormatValue(v, spec, st)
}

// methodFormatMap is str.format_map: the same substitution, with the fields
// looked up in a single mapping argument rather than in keyword arguments.
func methodFormatMap(s *State, r value.Value, args *value.CallArgs) (value.Value, error) {
	mapping, ok := arg(args, 0, "mapping")
	if !ok {
		return value.Undefined, errs.New(errs.TypeError,
			"format_map() takes exactly one argument (0 given)")
	}
	// format_map does not convert its argument. It subscripts it once per
	// *named* field, so a string with no fields never touches it at all --
	// `{{ "ab".format_map(none) }}` is "ab" -- and one that is not a
	// mapping fails as a subscript of that value would, which is not the
	// same complaint for a list, a str and a None.
	//
	// A positional field is refused before any of that: format_map has no
	// positional arguments to number.
	return formatWith(s, r, func(name string, _ *int) (value.Value, error) {
		if name == "" || isAllDigits(name) {
			return value.Undefined, errs.New(errs.ValueError,
				"Format string contains positional fields")
		}
		return fieldSubscript(mapping, name)
	})
}

// resolveFormatField resolves one replacement field.
//
// A field is a name or position followed by any number of `.attr` and `[key]`
// accessors: "{0.name}", "{user[id]}", "{0.a[1].b}". The attribute form is a
// real attribute lookup with no fall-back to items, which is why
// `"{0.foo}".format({"foo": 42})` raises rather than finding the entry.
func resolveFormatField(field string, base fieldBase, auto *int) (value.Value, error) {
	name, accessors := splitFieldName(field)

	v, err := base(name, auto)
	if err != nil {
		return value.Undefined, err
	}
	for _, a := range accessors {
		if v, err = a.apply(v); err != nil {
			return value.Undefined, err
		}
	}
	return v, nil
}

// fieldAccessor is one `.attr` or `[key]` step.
type fieldAccessor struct {
	name    string
	isIndex bool
}

func (a fieldAccessor) apply(v value.Value) (value.Value, error) {
	if a.isIndex {
		return fieldSubscript(v, a.name)
	}

	// Attribute access, with no item fall-back.
	if attr, ok := lookupAttr(nil, v, a.name); ok {
		return attr, nil
	}
	return value.Undefined, errs.New(errs.AttributeError,
		"'%s' object has no attribute '%s'", v.TypeName(), a.name)
}

// fieldSubscript is the `[key]` step of a replacement field, which is a real
// `obj[key]` and not the dict-shaped lookup it used to be.
//
// The field-name parser decides the key's type by its spelling: all digits is
// an integer index, and anything else -- "-1" and " 0" included, since neither
// is all digits -- is a string key. So a negative index never reaches here, and
// `{0[-1]}` on a list is a type error rather than the last element.
func fieldSubscript(v value.Value, name string) (value.Value, error) {
	if name == "" {
		return value.Undefined, errs.New(errs.ValueError,
			"Empty attribute in format string")
	}
	var key value.Value
	if isAllDigits(name) {
		n, err := strconv.ParseInt(name, 10, 64)
		if err != nil {
			return value.Undefined, errs.New(errs.ValueError,
				"invalid index %q", name)
		}
		key = value.Int(n)
	} else {
		key = value.String(name)
	}

	switch v.Kind() {
	case value.KindDict:
		// A dict takes either kind of key and reports a miss as one.
		if item, ok := lookupItem(v, key); ok {
			return item, nil
		}
		return value.Undefined, errs.New(errs.KeyError, "%s", value.Repr(key))

	case value.KindString, value.KindBytes:
		idx, ok := key.Int64()
		if !ok {
			return value.Undefined, errs.New(errs.TypeError,
				"string indices must be integers, not '%s'", key.TypeName())
		}
		if ch, in := value.StrIndex(v.AsString(), int(idx)); in {
			return value.String(ch), nil
		}
		return value.Undefined, errs.New(errs.IndexError, "string index out of range")

	case value.KindList, value.KindTuple:
		idx, ok := key.Int64()
		if !ok {
			return value.Undefined, errs.New(errs.TypeError,
				"%s indices must be integers or slices, not %s",
				v.TypeName(), key.TypeName())
		}
		seq, _ := v.Seq()
		if idx >= 0 && idx < int64(seq.Len()) {
			return seq.At(int(idx)), nil
		}
		return value.Undefined, errs.New(errs.IndexError,
			"%s index out of range", v.TypeName())
	}

	// An object may still define __getitem__; anything else is not
	// subscriptable at all, which is a different complaint from a miss.
	if item, ok := lookupItem(v, key); ok {
		return item, nil
	}
	if v.Kind() == value.KindObject {
		return value.Undefined, errs.New(errs.KeyError, "%s", value.Repr(key))
	}
	return value.Undefined, errs.New(errs.TypeError,
		"'%s' object is not subscriptable", v.TypeName())
}

// splitFieldName separates the base of a replacement field from its accessors,
// stopping at the conversion or format spec. The scan is bracket-aware, so a
// colon inside "[a:b]" does not end the field name.
func splitFieldName(field string) (string, []fieldAccessor) {
	end := len(field)
	depth := 0
	for i := 0; i < len(field); i++ {
		switch field[i] {
		case '[':
			depth++
		case ']':
			if depth > 0 {
				depth--
			}
		case '!', ':':
			if depth == 0 {
				end = i
				i = len(field)
			}
		}
	}
	field = field[:end]

	// The base runs to the first accessor.
	base := field
	if i := strings.IndexAny(field, ".["); i >= 0 {
		base, field = field[:i], field[i:]
	} else {
		field = ""
	}

	var accessors []fieldAccessor
	for field != "" {
		switch field[0] {
		case '.':
			field = field[1:]
			next := strings.IndexAny(field, ".[")
			if next < 0 {
				next = len(field)
			}
			accessors = append(accessors, fieldAccessor{name: field[:next]})
			field = field[next:]
		case '[':
			close := strings.IndexByte(field, ']')
			if close < 0 {
				accessors = append(accessors,
					fieldAccessor{name: field[1:], isIndex: true})
				field = ""
				continue
			}
			accessors = append(accessors,
				fieldAccessor{name: field[1:close], isIndex: true})
			field = field[close+1:]
		default:
			field = ""
		}
	}
	return base, accessors
}

// resolveFieldBase finds the argument a field names: automatic numbering when
// empty, positional when all digits, keyword otherwise.
func resolveFieldBase(name string, args *value.CallArgs, auto *int) (value.Value, error) {
	switch {
	case name == "":
		// One format string counts its own fields or names them, never
		// both: `"{0} {}"` is refused rather than guessing which the
		// author meant. auto is negative once a manual field was seen.
		if *auto < 0 {
			return value.Undefined, errs.New(errs.ValueError,
				"cannot switch from manual field specification to automatic field numbering")
		}
		v, ok := args.Arg(*auto)
		*auto++
		if !ok {
			return value.Undefined, errs.New(errs.IndexError,
				"Replacement index %d out of range for positional args tuple", *auto-1)
		}
		return v, nil
	case isAllDigits(name):
		if *auto > 0 {
			return value.Undefined, errs.New(errs.ValueError,
				"cannot switch from automatic field numbering to manual field specification")
		}
		*auto = -1
		i := 0
		for _, c := range name {
			i = i*10 + int(c-'0')
		}
		v, ok := args.Arg(i)
		if !ok {
			return value.Undefined, errs.New(errs.IndexError,
				"Replacement index %d out of range for positional args tuple", i)
		}
		return v, nil
	default:
		v, ok := args.Kwarg(name)
		if !ok {
			return value.Undefined, errs.New(errs.KeyError,
				"%s", value.Repr(value.String(name)))
		}
		return v, nil
	}
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func methodZfill(st *State, r value.Value, args *value.CallArgs) (value.Value, error) {
	width, err := indexArg(args, 0, "width", 0, cSSizeT)
	if err != nil {
		return value.Undefined, err
	}
	s := r.AsString()
	n := value.StrLen(s)
	if n >= width {
		return value.String(s), nil
	}
	sign := ""
	if strings.HasPrefix(s, "-") || strings.HasPrefix(s, "+") {
		sign, s = s[:1], s[1:]
	}
	zeros, err := st.repeatString("0", width-n)
	if err != nil {
		return value.Undefined, err
	}
	return value.String(sign + zeros + s), nil
}

type padAlign int

const (
	padLeftAligned padAlign = iota
	padRightAligned
	padCentered
)

func padMethod(align padAlign) func(*State, value.Value, *value.CallArgs) (value.Value, error) {
	return func(st *State, r value.Value, args *value.CallArgs) (value.Value, error) {
		width, err := indexArg(args, 0, "width", 0, cSSizeT)
		if err != nil {
			return value.Undefined, err
		}
		fill, err := fillCharArg(args, 1, "fillchar")
		if err != nil {
			return value.Undefined, err
		}
		return pad(st, r.AsString(), width, fill, align)
	}
}

// fillCharArg reads the fill character str.center, str.ljust and str.rjust
// take, with CPython's own refusals.
//
// Both checks are load-bearing rather than pedantic. A non-string fill used to
// be ignored, so `"a".center(10, 5)` padded with spaces where CPython raises;
// and a multi-character fill was accepted, so `"a".center(10, "ab")` returned
// nineteen characters from a call that asked for ten. Silently returning a
// string of the wrong length is worse than refusing.
func fillCharArg(args *value.CallArgs, i int, name string) (string, error) {
	v, ok := arg(args, i, name)
	if !ok {
		return " ", nil
	}
	// A None is not the default. Only an argument that was not written is,
	// and an explicit one reaches the check like any other value.
	if v.Kind() != value.KindString {
		// Hand-written in CPython rather than generated, so it names
		// the type plainly: "NoneType", not the parser's "None".
		return "", errs.New(errs.TypeError,
			"The fill character must be a unicode character, not %s", v.TypeName())
	}
	fill := v.AsString()
	if value.StrLen(fill) != 1 {
		return "", errs.New(errs.TypeError,
			"The fill character must be exactly one character long")
	}
	return fill, nil
}

func pad(st *State, s string, width int, fill string, align padAlign) (value.Value, error) {
	missing := width - value.StrLen(s)
	if missing <= 0 {
		return value.String(s), nil
	}
	// Charge the whole padding before building any of it. center splits it
	// into two halves, and charging those separately let each pass the
	// ceiling while their sum went straight past it -- the same way two
	// individually-legal operands defeated the check in replace.
	if err := st.ChargeBytes(saturatingMulInt(int64(missing), int64(len(fill)))); err != nil {
		return value.Undefined, err
	}
	switch align {
	case padLeftAligned:
		tail, err := st.repeatString(fill, missing)
		if err != nil {
			return value.Undefined, err
		}
		return value.String(s + tail), nil
	case padRightAligned:
		head, err := st.repeatString(fill, missing)
		if err != nil {
			return value.Undefined, err
		}
		return value.String(head + s), nil
	default:
		// Python's str.center puts the odd character on the right.
		left, err := st.repeatString(fill, missing/2)
		if err != nil {
			return value.Undefined, err
		}
		right, err := st.repeatString(fill, missing-missing/2)
		if err != nil {
			return value.Undefined, err
		}
		return value.String(left + s + right), nil
	}
}

func classifyMethod(pred func(rune) bool) func(*State, value.Value, *value.CallArgs) (value.Value, error) {
	return func(_ *State, r value.Value, _ *value.CallArgs) (value.Value, error) {
		s := r.AsString()
		if s == "" {
			return value.False, nil
		}
		for _, c := range s {
			if !pred(c) {
				return value.False, nil
			}
		}
		return value.True, nil
	}
}

// caseMethod implements isupper and islower: at least one cased character, and
// no character of the opposite case.
// stringPredicate wraps one of the whole-string case predicates as a method.
func stringPredicate(f func(string) bool) func(*State, value.Value, *value.CallArgs) (value.Value, error) {
	return func(_ *State, r value.Value, _ *value.CallArgs) (value.Value, error) {
		return value.Bool(f(r.AsString())), nil
	}
}

// --- dict methods ------------------------------------------------------------

var dictMethods = map[string]func(*State, value.Value, *value.CallArgs) (value.Value, error){
	"keys": func(_ *State, r value.Value, _ *value.CallArgs) (value.Value, error) {
		d, _ := r.Dict()
		return value.NewList(d.Keys()...), nil
	},
	"values": func(_ *State, r value.Value, _ *value.CallArgs) (value.Value, error) {
		d, _ := r.Dict()
		return value.NewList(d.Values()...), nil
	},
	"items":  methodDictItems,
	"get":    methodDictGet,
	"pop":    methodDictPop,
	"update": methodDictUpdate,
	"copy": func(_ *State, r value.Value, _ *value.CallArgs) (value.Value, error) {
		d, _ := r.Dict()
		return d.Clone(), nil
	},
	"clear":      methodDictClear,
	"setdefault": methodDictSetdefault,
	"popitem":    methodDictPopitem,
	"fromkeys":   methodDictFromkeys,
}

func methodDictItems(_ *State, r value.Value, _ *value.CallArgs) (value.Value, error) {
	d, _ := r.Dict()
	items := make([]value.Value, 0, d.Len())
	for _, e := range d.Entries() {
		items = append(items, value.NewTuple(e.Key, e.Value))
	}
	return value.NewList(items...), nil
}

func methodDictGet(_ *State, r value.Value, args *value.CallArgs) (value.Value, error) {
	d, _ := r.Dict()
	// dict.get is a C function: it counts its arguments rather than binding
	// them by name, so both the shortage and the excess are reported with
	// the count that was actually passed.
	if n := len(args.Pos) + len(args.Kwargs); n > 2 {
		return value.Undefined, errs.New(errs.TypeError,
			"get expected at most 2 arguments, got %d", n)
	}
	key, ok := arg(args, 0, "key")
	if !ok {
		return value.Undefined, errs.New(errs.TypeError, "get expected at least 1 argument, got 0")
	}
	v, found, err := d.Get(key)
	if err != nil {
		return value.Undefined, err
	}
	if found {
		return v, nil
	}
	if def, ok := arg(args, 1, "default"); ok {
		return def, nil
	}
	return value.None, nil
}

func methodDictPop(_ *State, r value.Value, args *value.CallArgs) (value.Value, error) {
	d, _ := r.Dict()
	key, ok := arg(args, 0, "key")
	if !ok {
		// CPython names the count it actually got, and dict.pop() is a
		// C function so the message comes from the argument clinic
		// rather than from Python.
		return value.Undefined, errs.New(errs.TypeError,
			"pop expected at least 1 argument, got %d", len(args.Pos))
	}
	v, found, err := d.Get(key)
	if err != nil {
		return value.Undefined, err
	}
	if found {
		if _, err := d.Delete(key); err != nil {
			return value.Undefined, err
		}
		return v, nil
	}
	if def, ok := arg(args, 1, "default"); ok {
		return def, nil
	}
	return value.Undefined, errs.New(errs.KeyError, "%s", value.Repr(key))
}

func methodDictSetdefault(_ *State, r value.Value, args *value.CallArgs) (value.Value, error) {
	d, _ := r.Dict()
	key, ok := arg(args, 0, "key")
	if !ok {
		return value.Undefined, errs.New(errs.TypeError, "setdefault expected at least 1 argument")
	}
	if v, found, err := d.Get(key); err != nil {
		return value.Undefined, err
	} else if found {
		return v, nil
	}
	def, _ := arg(args, 1, "default")
	if !def.IsUndefined() {
		return def, d.Set(key, def)
	}
	return value.None, d.Set(key, value.None)
}

func methodDictUpdate(_ *State, r value.Value, args *value.CallArgs) (value.Value, error) {
	d, _ := r.Dict()
	if other, ok := arg(args, 0, ""); ok {
		// Anything dict() accepts, update() accepts, and anything dict()
		// refuses it refuses the same way. This used to test only for a
		// mapping and discard everything else in silence, so
		// `d.update([("a", 1)])` left the dict empty and reported success.
		if err := updateDictFrom(d, other); err != nil {
			return value.Undefined, err
		}
	}
	for _, kw := range args.Kwargs {
		d.SetString(kw.Name, kw.Value)
	}
	return value.None, nil
}

// updateDictFrom merges src into d, accepting the three shapes Python's
// dict.update and dict() both take: a dict, any mapping, or an iterable of
// key/value pairs. It is the single implementation behind both, so the two
// cannot drift apart again.
func updateDictFrom(d *value.Dict, src value.Value) error {
	if src.IsUndefined() {
		// dict() probes for a keys() method first, and that probe is what
		// fails on an Undefined.
		return src.UndefinedError()
	}
	if sd, ok := src.Dict(); ok {
		for _, e := range sd.Entries() {
			if err := d.Set(e.Key, e.Value); err != nil {
				return err
			}
		}
		return nil
	}
	if m, ok := src.Interface().(value.Mapping); ok {
		for _, k := range m.Keys() {
			v, _ := m.GetItem(k)
			if err := d.Set(k, v); err != nil {
				return err
			}
		}
		return nil
	}
	pairs, err := value.Iterate(src)
	if err != nil {
		return errs.New(errs.TypeError, "'%s' object is not iterable", src.TypeName())
	}
	index := 0
	for pair := range pairs {
		key, val, err := unpackDictPair(pair, index)
		if err != nil {
			return err
		}
		if err := d.Set(key, val); err != nil {
			return err
		}
		index++
	}
	return nil
}

// unpackDictPair splits one element of a dict-update sequence into its key and
// value, with CPython's two refusals.
//
// The pair is *iterated* rather than required to be a list or tuple, because
// Python unpacks anything iterable of length two: `dict(["ab", "cd"])` is
// {'a': 'b', 'c': 'd'}, each pair being a two-character string. Testing for a
// sequence instead rejected that, and reported it as "has length 2; 2 is
// required" -- a message that contradicts itself, which is what gave the bug
// away.
func unpackDictPair(pair value.Value, index int) (value.Value, value.Value, error) {
	items, err := value.Iterate(pair)
	if err != nil {
		return value.Undefined, value.Undefined, errs.New(errs.TypeError,
			"cannot convert dictionary update sequence element #%d to a sequence", index)
	}
	var first, second value.Value
	n := 0
	for item := range items {
		switch n {
		case 0:
			first = item
		case 1:
			second = item
		}
		n++
	}
	if n != 2 {
		return value.Undefined, value.Undefined, errs.New(errs.ValueError,
			"dictionary update sequence element #%d has length %d; 2 is required",
			index, n)
	}
	return first, second, nil
}

func methodDictClear(_ *State, r value.Value, _ *value.CallArgs) (value.Value, error) {
	d, _ := r.Dict()
	for _, k := range d.Keys() {
		if _, err := d.Delete(k); err != nil {
			return value.Undefined, err
		}
	}
	return value.None, nil
}

// --- list and tuple methods --------------------------------------------------

var listMethods = map[string]func(*State, value.Value, *value.CallArgs) (value.Value, error){
	"append":  methodListAppend,
	"insert":  methodListInsert,
	"pop":     methodListPop,
	"remove":  methodListRemove,
	"index":   methodSeqIndex,
	"count":   methodSeqCount,
	"reverse": methodListReverse,
	"extend":  methodListExtend,
	"copy":    func(_ *State, r value.Value, _ *value.CallArgs) (value.Value, error) { return r.AsList(), nil },
	"clear":   methodListClear,
	"sort":    methodListSort,
}

func methodListClear(_ *State, r value.Value, _ *value.CallArgs) (value.Value, error) {
	s, _ := r.Seq()
	*s = *mustSeq(value.NewList())
	return value.None, nil
}

var tupleMethods = map[string]func(*State, value.Value, *value.CallArgs) (value.Value, error){
	"index": methodTupleIndex,
	"count": methodSeqCount,
}

func methodListAppend(_ *State, r value.Value, args *value.CallArgs) (value.Value, error) {
	v, ok := arg(args, 0, "")
	if !ok {
		return value.Undefined, errs.New(errs.TypeError,
			"append() takes exactly one argument (0 given)")
	}
	s, _ := r.Seq()
	s.Append(v)
	return value.None, nil
}

func methodListExtend(st *State, r value.Value, args *value.CallArgs) (value.Value, error) {
	v, ok := arg(args, 0, "")
	if !ok {
		return value.Undefined, errs.New(errs.TypeError, "extend() takes exactly one argument")
	}
	seq, err := value.Iterate(v)
	if err != nil {
		return value.Undefined, err
	}
	s, _ := r.Seq()
	for item := range seq {
		// `l.extend(range(10000000000))` grows the receiver without any
		// {% for %} to bound it, so the walk is charged here.
		if err := st.Step(1); err != nil {
			return value.Undefined, err
		}
		s.Append(item)
	}
	return value.None, nil
}

func methodListInsert(_ *State, r value.Value, args *value.CallArgs) (value.Value, error) {
	at, err := indexArg(args, 0, "", 0, cSSizeT)
	if err != nil {
		return value.Undefined, err
	}
	v, ok := arg(args, 1, "")
	if !ok {
		return value.Undefined, errs.New(errs.TypeError, "insert() takes exactly 2 arguments")
	}
	s, _ := r.Seq()
	items := s.Items()
	if at < 0 {
		at += len(items)
	}
	at = min(max(at, 0), len(items))
	items = append(items, value.None)
	copy(items[at+1:], items[at:])
	items[at] = v
	*s = *mustSeq(value.NewList(items...))
	return value.None, nil
}

func methodListPop(_ *State, r value.Value, args *value.CallArgs) (value.Value, error) {
	s, _ := r.Seq()
	items := s.Items()
	if len(items) == 0 {
		return value.Undefined, errs.New(errs.IndexError, "pop from empty list")
	}
	at, err := indexArg(args, 0, "", len(items)-1, cSSizeT)
	if err != nil {
		return value.Undefined, err
	}
	if at < 0 {
		at += len(items)
	}
	if at < 0 || at >= len(items) {
		return value.Undefined, errs.New(errs.IndexError, "pop index out of range")
	}
	out := items[at]
	*s = *mustSeq(value.NewList(append(append([]value.Value{}, items[:at]...), items[at+1:]...)...))
	return out, nil
}

func methodListRemove(_ *State, r value.Value, args *value.CallArgs) (value.Value, error) {
	v, ok := arg(args, 0, "")
	if !ok {
		return value.Undefined, errs.New(errs.TypeError, "remove() takes exactly one argument")
	}
	s, _ := r.Seq()
	for i, item := range s.Items() {
		if value.Equal(item, v) {
			items := s.Items()
			*s = *mustSeq(value.NewList(append(append([]value.Value{}, items[:i]...), items[i+1:]...)...))
			return value.None, nil
		}
	}
	return value.Undefined, errs.New(errs.ValueError, "list.remove(x): x not in list")
}

func methodListReverse(_ *State, r value.Value, _ *value.CallArgs) (value.Value, error) {
	s, _ := r.Seq()
	items := s.Items()
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
	return value.None, nil
}

// methodSeqIndex is list.index(value, start=0, stop=maxsize), which searches
// only between start and stop and answers an index into the whole list.
//
// The window was read and then ignored, so `[1,2,1].index(1, 1)` answered 0
// where CPython answers 2, and `[1,2,1].index(1, 1, 2)` answered 0 where
// CPython raises. Unlike a string search's, these bounds have no None form:
// list.index declares them as indices outright, so the message has no "or
// None" in it.
func methodSeqIndex(_ *State, r value.Value, args *value.CallArgs) (value.Value, error) {
	v, ok := arg(args, 0, "")
	if !ok {
		return value.Undefined, errs.New(errs.TypeError, "index() takes at least one argument")
	}
	s, _ := r.Seq()
	items := s.Items()
	start, end, err := seqSearchBounds(args, len(items))
	if err != nil {
		return value.Undefined, err
	}
	for i := start; i < end; i++ {
		if value.Equal(items[i], v) {
			return value.Int(int64(i)), nil
		}
	}
	return value.Undefined, errs.New(errs.ValueError, "%s is not in list", value.Repr(v))
}

// seqSearchBounds reads list.index's start and stop as slice indices: negative
// counts from the end, out of range clamps, and anything that is not a whole
// number is refused.
func seqSearchBounds(args *value.CallArgs, n int) (start, end int, err error) {
	read := func(i, def int) (int, error) {
		v, ok := args.Arg(i)
		if !ok {
			return def, nil
		}
		k, fits := v.Int64()
		if !fits {
			if _, big := v.BigInt(); big {
				// Past any index there is, in either
				// direction, and clamping is the answer.
				if b, _ := v.BigInt(); b.Sign() < 0 {
					return 0, nil
				}
				return n, nil
			}
			return 0, errs.New(errs.TypeError,
				"slice indices must be integers or have an __index__ method")
		}
		idx := int(k)
		if idx < 0 {
			idx += n
			if idx < 0 {
				idx = 0
			}
		}
		if idx > n {
			idx = n
		}
		return idx, nil
	}
	if start, err = read(1, 0); err != nil {
		return 0, 0, err
	}
	if end, err = read(2, n); err != nil {
		return 0, 0, err
	}
	if end < start {
		end = start
	}
	return start, end, nil
}

// methodTupleIndex is list.index on a tuple, which names itself when the value
// is absent rather than borrowing list's wording.
func methodTupleIndex(s *State, r value.Value, args *value.CallArgs) (value.Value, error) {
	out, err := methodSeqIndex(s, r, args)
	var e *errs.Error
	if errors.As(err, &e) && e.Kind == errs.ValueError {
		e.Msg = "tuple.index(x): x not in tuple"
	}
	return out, err
}

// methodListSort is list.sort: in place, returning None. Its arguments are
// keyword-only, so a positional one is refused rather than taken for `key`.
func methodListSort(st *State, r value.Value, args *value.CallArgs) (value.Value, error) {
	if len(args.Pos) > 0 {
		return value.Undefined, errs.New(errs.TypeError,
			"sort() takes no positional arguments")
	}
	reverse := false
	for _, kw := range args.Kwargs {
		switch kw.Name {
		case "reverse":
			b, err := value.IsTrue(kw.Value)
			if err != nil {
				return value.Undefined, err
			}
			reverse = b
		case "key":
			if !kw.Value.IsNone() {
				return value.Undefined, errs.New(errs.TypeError,
					"sort() key must be None")
			}
		default:
			return value.Undefined, errs.New(errs.TypeError,
				"'%s' is an invalid keyword argument for sort()", kw.Name)
		}
	}
	seq, _ := r.Seq()
	items := seq.Items()
	if err := stableSortBy(st, items, func(v value.Value) (value.Value, error) {
		return v, nil
	}, reverse); err != nil {
		return value.Undefined, err
	}
	// Sorting a list in place answers None, not the list -- which is why
	// `{{ L.sort() }}` renders "None" and the sorted list is in L.
	return value.None, nil
}

// methodDictPopitem is dict.popitem, which takes the *last* pair inserted:
// dicts have been ordered since 3.7, so it is a stack rather than arbitrary.
func methodDictPopitem(_ *State, r value.Value, args *value.CallArgs) (value.Value, error) {
	if len(args.Pos) > 0 || len(args.Kwargs) > 0 {
		return value.Undefined, errs.New(errs.TypeError,
			"dict.popitem() takes no arguments (%d given)", len(args.Pos)+len(args.Kwargs))
	}
	d, _ := r.Dict()
	entries := d.Entries()
	if len(entries) == 0 {
		return value.Undefined, errs.New(errs.KeyError,
			"'popitem(): dictionary is empty'")
	}
	last := entries[len(entries)-1]
	if _, err := d.Delete(last.Key); err != nil {
		return value.Undefined, err
	}
	return value.NewTuple(last.Key, last.Value), nil
}

// methodDictFromkeys is dict.fromkeys, a classmethod: the dict it is reached
// through contributes nothing, so `{{ d.fromkeys("ab") }}` is a fresh two-entry
// dict whatever d held.
func methodDictFromkeys(st *State, _ value.Value, args *value.CallArgs) (value.Value, error) {
	keys, ok := arg(args, 0, "iterable")
	if !ok {
		return value.Undefined, errs.New(errs.TypeError,
			"fromkeys expected at least 1 argument, got 0")
	}
	fill := value.None
	if v, ok := arg(args, 1, "value"); ok {
		fill = v
	}
	items, err := materialize(st, keys)
	if err != nil {
		return value.Undefined, err
	}
	out := value.NewDict()
	d, _ := out.Dict()
	for _, k := range items {
		if err := value.Hashable(k); err != nil {
			return value.Undefined, err
		}
		if err := d.Set(k, fill); err != nil {
			return value.Undefined, err
		}
	}
	return out, nil
}

func methodSeqCount(_ *State, r value.Value, args *value.CallArgs) (value.Value, error) {
	v, ok := arg(args, 0, "")
	if !ok {
		return value.Undefined, errs.New(errs.TypeError, "count() takes exactly one argument")
	}
	s, _ := r.Seq()
	n := 0
	for _, item := range s.Items() {
		if value.Equal(item, v) {
			n++
		}
	}
	return value.Int(int64(n)), nil
}

func mustSeq(v value.Value) *value.Seq {
	s, _ := v.Seq()
	return s
}
