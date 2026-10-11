// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package value

import (
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mgilbir/gojja2/errs"
)

// FormatPercent implements `"..." % args`, Python's printf-style string
// formatting, which templates reach for often enough that leaving it out would
// be a visible gap.
//
// The right operand decides how arguments are consumed: a tuple is positional,
// a mapping is consulted by name when the format uses `%(key)s`, and anything
// else -- a bare dict included -- is a single argument.
//
// The bookkeeping is CPython's own, field for field -- arglen and argidx, with
// their negative sentinels -- because what is observable depends on it: which
// conversion runs out of arguments, what a `*` may draw from, what a
// conversion after a `%(name)s` reads, what is left over at the end, and from
// 3.15 which argument an error names. A simpler "list of positionals plus a
// cursor" got the keyed case wrong on every version: `'%(a)s %s' % {'a': 1}`
// rendered the dict for the second conversion, where CPython has already
// replaced its argument source with the looked-up 1 and has nothing left.
func FormatPercent(format, args Value, budget Budget, py PythonVersion) (Value, error) {
	spec := format.str
	// PEP 461 gave bytes the same printf-style formatting, with three
	// differences: %b and %s want a bytes-like object rather than anything
	// str() accepts, %r is ascii() as %a is, and the result is bytes. The
	// spec itself is the same field, since KindBytes carries its bytes there.
	asBytes := format.kind == KindBytes
	// markupsafe wraps each argument so that it escapes as it is
	// substituted, and returns Markup. Escaping happens *before* padding,
	// so a width applies to the escaped text. A bytes is never Markup.
	escaping := format.safe && !asBytes

	// A tuple subclass *is* the argument tuple: the check CPython makes is
	// PyTuple_Check, which a subclass passes, so `"%s|%s" % g` fills two
	// placeholders from the pair rather than one from the object. It is
	// unwrapped before the mapping test below for the same reason CPython
	// excludes tuples there -- a tuple never supplies names.
	original := args
	args = AsTupleIfPossible(args)

	f := percentFormatter{py: py, asBytes: asBytes, escaping: escaping, spec: spec}
	// CPython decides between "a mapping" and "one positional argument" by
	// asking whether the right operand supports subscripting, excluding
	// tuple and str. A list therefore counts as a mapping -- which is why
	// `"" % []` is "" while `"" % 1` is a TypeError -- even though looking
	// a name up in one can only fail. The operand is still available as the
	// single positional argument either way, so `"%s" % {}` renders "{}".
	f.dict, f.mapping = isMappingArg(args, asBytes), args
	f.args, f.arglen, f.argidx = args, -1, -2
	f.argType = QualifiedTypeName(original)
	if s, ok := args.Seq(); ok && args.kind == KindTuple {
		f.items, f.arglen, f.argidx = s.items, len(s.items), 0
	} else if escaping && !f.dict {
		// markupsafe's __mod__ turns anything that is neither a tuple nor
		// subscriptable into a one-element tuple of its helper, so to
		// CPython it is the first of one positional argument -- which is
		// what a 3.15 error calls it, and what a `*` may draw from.
		f.items, f.arglen, f.argidx = []Value{args}, 1, 0
		f.argType = "tuple"
	}

	var out strings.Builder
	for i := 0; i < len(spec); {
		c := spec[i]
		if c != '%' {
			out.WriteByte(c)
			i++
			continue
		}
		start := i
		i++
		if i < len(spec) && spec[i] == '%' {
			out.WriteByte('%')
			i++
			continue
		}

		conv := conversion{py: py, bytes: asBytes, escaping: escaping, start: f.position(start)}
		var err error
		if i, err = f.parseConversion(i, &conv); err != nil {
			return Undefined, err
		}

		// The value this conversion formats: the looked-up one for a
		// `%(name)s`, otherwise the next positional argument. It is taken
		// before the verb is judged, as CPython takes it, so an unknown
		// verb with nothing left to format reports the missing argument.
		arg, err := f.nextArg(true)
		if err != nil {
			return Undefined, err
		}
		conv.label = f.label(&conv)

		conv.budget = budget
		text, err := conv.apply(arg, escaping, budget)
		if err != nil {
			return Undefined, err
		}
		out.WriteString(text)
	}

	if !f.dict && f.argidx < f.arglen {
		kind := "string"
		if asBytes {
			kind = "bytes"
		}
		if py.PercentErrorsCountArguments() {
			required, got := f.argidx, f.arglen
			if f.arglen < 0 {
				required, got = 0, 1
			}
			return Undefined, errs.New(errs.TypeError,
				"not all arguments converted during %s formatting (required %d, got %d)",
				kind, required, got)
		}
		return Undefined, errs.New(errs.TypeError,
			"not all arguments converted during %s formatting", kind)
	}
	if asBytes {
		return Bytes([]byte(out.String())), nil
	}
	if escaping {
		return Safe(out.String()), nil
	}
	return String(out.String()), nil
}

// percentFormatter is CPython's formatting context: where the arguments come
// from and how far through them it is.
//
// arglen and argidx carry CPython's sentinels. A tuple is arglen = its length
// and argidx counting up from 0. Anything else is one argument: arglen -1 and
// argidx -2, which becomes -1 once it has been taken. A `%(name)s` makes the
// looked-up value the argument source: through 3.14 that is the one-argument
// state again, so a positional conversion after it finds nothing left, and
// from 3.15 it is arglen -3, which is how the formatter tells a positional
// conversion that follows a keyed one and refuses it outright.
type percentFormatter struct {
	py       PythonVersion
	asBytes  bool
	escaping bool
	spec     string

	dict    bool   // the operand was subscriptable, so names may be looked up
	mapping Value  // what names are looked up in
	argType string // the operand's class, as "format requires a mapping" names it

	args   Value
	items  []Value
	arglen int
	argidx int
}

// nextArg is unicode_format_getnextarg. allowone is false for a `*`, which from
// 3.15 may only draw from a tuple; before that it took a lone argument too.
func (f *percentFormatter) nextArg(allowone bool) (Value, error) {
	if f.argidx < f.arglen && (allowone || f.arglen >= 0 || !f.py.PercentStarNeedsATuple()) {
		f.argidx++
		if f.arglen >= 0 {
			return f.items[f.argidx-1], nil
		}
		return f.args, nil
	}
	if f.py.PercentErrorsCountArguments() {
		got := f.arglen
		if got < 0 {
			got = 1
		}
		return Undefined, errs.New(errs.TypeError,
			"not enough arguments for format string (got %d)", got)
	}
	return Undefined, errs.New(errs.TypeError, "not enough arguments for format string")
}

// label is the "format argument ...: " that 3.15 puts in front of an error
// about one argument, naming it by key, by its 1-based place in the tuple, or
// by nothing when there was only the one. Earlier versions name none of them.
func (f *percentFormatter) label(c *conversion) string {
	if !f.py.PercentErrorsNameTheArgument() {
		return ""
	}
	switch {
	case c.hasKey:
		if f.asBytes {
			return "format argument " + Repr(Bytes([]byte(c.key))) + ": "
		}
		return "format argument " + ReprFor(String(c.key), f.py) + ": "
	case f.argidx >= 0:
		return fmt.Sprintf("format argument %d: ", f.argidx)
	}
	return "format argument: "
}

// position is where an offset into the spec falls as CPython counts it: in
// characters for a str, in bytes for a bytes.
func (f *percentFormatter) position(offset int) int {
	if f.asBytes {
		return offset
	}
	return utf8.RuneCountInString(f.spec[:offset])
}

// isMappingArg reports whether the right operand of % is subscriptable in
// CPython's sense: dict and list qualify, tuple and str explicitly do not.
func isMappingArg(v Value, asBytes bool) bool {
	switch v.kind {
	case KindDict, KindList:
		return true
	case KindBytes:
		// CPython's test is "supports subscripting", and the exceptions
		// are the format's *own* type: PyUnicode_Format names tuple and
		// str, PyBytes_Format names tuple, bytes and bytearray. So a
		// bytes counts when a str is being formatted -- which is why
		// `"0" % b""` renders "0" rather than complaining that the b""
		// was never converted -- and does not when a bytes is, where
		// `b"0" % b""` is "not all arguments converted". Looking a
		// *name* up in one still fails either way; see lookupFormatKey.
		return !asBytes
	case KindUndefined:
		// Undefined defines __getitem__, so it passes the subscript
		// check and `"x" % nope` formats rather than complaining about
		// unconverted arguments.
		return true
	case KindObject:
		switch v.Interface().(type) {
		case Mapping, Sequence:
			return true
		}
	}
	return false
}

// lookupFormatKeyAs resolves `%(name)s` against the right operand, with the
// key's own type decided by the format string's: the name parsed out of a bytes
// format is itself bytes, so `b'%(k)s' % {'k': b'v'}` raises KeyError b'k' --
// a str key does not match it -- and the complaint an operand that cannot be
// indexed by name makes names bytes too, as in "list indices must be integers
// or slices, not bytes".
func lookupFormatKeyAs(mapping Value, key string, asBytes bool) (Value, error) {
	if asBytes {
		return lookupFormatKey(mapping, Bytes([]byte(key)))
	}
	return lookupFormatKey(mapping, String(key))
}

func lookupFormatKey(mapping Value, key Value) (Value, error) {
	switch mapping.kind {
	case KindDict:
		d, _ := mapping.Dict()
		v, ok := d.GetKnown(key)
		if !ok {
			return Undefined, errs.New(errs.KeyError, "%s", Repr(key))
		}
		return v, nil
	case KindList:
		// A list is subscriptable enough to be treated as a mapping but
		// cannot actually be indexed by name.
		return Undefined, errs.New(errs.TypeError,
			"list indices must be integers or slices, not %s", key.TypeName())
	case KindBytes:
		// The same, and CPython says "byte" rather than "bytes" here.
		// Only a str format reaches this: a bytes operand is the bytes
		// format's own type, so isMappingArg refuses it there.
		return Undefined, errs.New(errs.TypeError,
			"byte indices must be integers or slices, not %s", key.TypeName())
	case KindUndefined:
		// An undefined passes the subscript check -- it defines
		// __getitem__ -- and then answers the way a subscript of it
		// would: ChainableUndefined hands back another undefined, which
		// the conversion then refuses by *type*, and every other class
		// raises its own error naming the variable that was never set.
		// Raising for all of them made `b'%(k)b' % (x|attr('nope'))`
		// under ChainableUndefined an UndefinedError where jinja2 says
		// "%b requires a bytes-like object ... not 'ChainableUndefined'".
		if mapping.UndefinedBehavior() == UndefinedChainable {
			return mapping, nil
		}
		return Undefined, mapping.UndefinedError()
	case KindObject:
		// An object that can say why a subscript failed does: a proxy over a
		// string is refused by *type*, not by a missing key.
		if sub, ok := mapping.Interface().(interface {
			Subscript(Value) (Value, error)
		}); ok {
			return sub.Subscript(key)
		}
		if m, ok := mapping.Interface().(Mapping); ok {
			v, ok := m.GetItem(key)
			if !ok {
				return Undefined, errs.New(errs.KeyError, "%s", Repr(key))
			}
			return v, nil
		}
		if _, ok := mapping.Interface().(Sequence); ok {
			// Subscriptable, so CPython does the lookup -- and the
			// complaint is the object's own, naming its type:
			// `'%(a)s' % range(3)` is "range indices must be
			// integers or slices, not str". The blanket "format
			// requires a mapping" was gojja2's answer for every one
			// of these, which is what CPython says only for
			// something it cannot subscript at all.
			return Undefined, errs.New(errs.TypeError,
				"%s indices must be integers or slices, not %s",
				mapping.TypeName(), key.TypeName())
		}
	}
	// Unreachable while this switch handles every kind isMappingArg accepts,
	// which is the invariant between the two: nothing else gets this far,
	// because a format with a `%(name)s` and a non-mapping operand is refused
	// before the lookup. It is the right answer if that ever stops being true.
	return Undefined, errs.New(errs.TypeError, "format requires a mapping")
}

// conversion is one parsed `%...` directive.
type conversion struct {
	key      string // mapping key from %(name)s
	hasKey   bool
	flags    string // any of "#0- +"
	width    int
	hasWidth bool
	prec     int
	hasPrec  bool
	verb     byte
	// verbRune is the conversion character as CPython reads it: a code
	// point for a str format and a byte for a bytes one. verb is the same
	// character when it is ASCII and 0 otherwise, which no case matches.
	verbRune rune
	// bytes is set when the format string is a bytes, which changes what
	// %b, %s, %r and %c accept. See FormatPercent.
	bytes bool
	// escaping is set for a Markup format, where every argument reaches
	// the conversion wrapped in markupsafe's helper.
	escaping bool
	// budget pays for what a precision asks for before it is built: a
	// float's digits and an integer's zero padding are both sized by a
	// number the template chose.
	budget Budget
	// start is where the conversion's '%' is and at where its verb is, both
	// counted the way CPython counts them -- characters for a str, bytes for
	// a bytes. An unknown verb names one or both.
	start, at int
	// label is the "format argument ...: " 3.15 opens an error about this
	// conversion's argument with, and "" before it.
	label string
	// py is the interpreter being reproduced, set once where the
	// conversion is parsed. Most of this family is worded differently from
	// 3.14 and again from 3.15, and a conversion is the smallest thing that
	// knows which verb it is.
	py PythonVersion
}

// parseConversion reads one directive, starting just after its '%', in the
// order unicode_format_arg_parse does: the mapping key (and its lookup), the
// flags, the width, the precision, a length modifier, the verb. The order is
// observable -- a key that is missing is a KeyError before a width that is too
// big is noticed, and a `*` draws its argument as it is parsed.
func (f *percentFormatter) parseConversion(i int, c *conversion) (int, error) {
	spec, py := f.spec, f.py
	if i < len(spec) && spec[i] == '(' {
		// Whether there is anything to look a name up in is asked before
		// the key is even read, so `'%(a' % 1` is about the mapping and
		// not about the parenthesis.
		if !f.dict {
			if py.PercentErrorsCountArguments() {
				return 0, errs.New(errs.TypeError, "format requires a mapping, not %s", f.argType)
			}
			return 0, errs.New(errs.TypeError, "format requires a mapping")
		}
		depth, keyStart := 1, i+1
		j := keyStart
		for ; j < len(spec) && depth > 0; j++ {
			switch spec[j] {
			case '(':
				depth++
			case ')':
				depth--
			}
		}
		if depth != 0 {
			if py.PercentErrorsGivePositions() {
				return 0, errs.New(errs.ValueError,
					"stray %% or incomplete format key at position %d", c.start)
			}
			return 0, errs.New(errs.ValueError, "incomplete format key")
		}
		c.key, c.hasKey = spec[keyStart:j-1], true
		i = j
		v, err := lookupFormatKeyAs(f.mapping, c.key, f.asBytes)
		if err != nil {
			return 0, err
		}
		// The looked-up value is now the whole argument source.
		f.args, f.items = v, nil
		if py.PercentKeyExcludesPositional() {
			f.arglen, f.argidx = -3, -4
		} else {
			f.arglen, f.argidx = -1, -2
		}
	} else if f.arglen < -1 {
		return 0, errs.New(errs.ValueError,
			"format requires a parenthesised mapping key at position %d", c.start)
	}

	for i < len(spec) && strings.IndexByte("#0- +", spec[i]) >= 0 {
		if !strings.ContainsRune(c.flags, rune(spec[i])) {
			c.flags += string(spec[i])
		}
		i++
	}

	if i < len(spec) && spec[i] == '*' {
		// `*` pulls the width from the argument stream *before* the
		// value. Taking the value first made `"%*s" % (5, "x")` read the
		// 5 as the string and the "x" as the width.
		w, err := f.takeStarInt(c, false)
		if err != nil {
			return 0, err
		}
		// A negative width means left-adjust, as in C.
		if w < 0 {
			c.flags, w = c.flags+"-", -w
		}
		c.width, c.hasWidth = w, true
		i++
	} else {
		start := i
		for i < len(spec) && spec[i] >= '0' && spec[i] <= '9' {
			i++
		}
		if i > start {
			w, ok := boundedDigits(spec[start:i], math.MaxInt64)
			if !ok {
				if py.PercentErrorsGivePositions() {
					return 0, errs.New(errs.ValueError, "width too big at position %d", c.start)
				}
				return 0, errs.New(errs.ValueError, "width too big")
			}
			c.width, c.hasWidth = int(w), true
		}
	}

	if i < len(spec) && spec[i] == '.' {
		i++
		if i < len(spec) && spec[i] == '*' {
			p, err := f.takeStarInt(c, true)
			if err != nil {
				return 0, err
			}
			// Python clamps a negative precision to zero rather
			// than treating it as absent, which is what C does.
			c.prec, c.hasPrec = max(p, 0), true
			i++
		} else {
			start := i
			for i < len(spec) && spec[i] >= '0' && spec[i] <= '9' {
				i++
			}
			p, ok := boundedDigits(spec[start:i], math.MaxInt32)
			if !ok {
				if py.PercentErrorsGivePositions() {
					return 0, errs.New(errs.ValueError, "precision too big at position %d", c.start)
				}
				return 0, errs.New(errs.ValueError, "precision too big")
			}
			c.prec, c.hasPrec = int(p), true
		}
	}

	// One length modifier is accepted and ignored, as in Python: `%ld` is
	// `%d`, and `%lld` is an unsupported 'l'.
	if i < len(spec) && strings.IndexByte("hlL", spec[i]) >= 0 {
		i++
	}

	if i >= len(spec) {
		if py.PercentErrorsGivePositions() {
			return 0, errs.New(errs.ValueError, "stray %% at position %d", c.start)
		}
		return 0, errs.New(errs.ValueError, "incomplete format")
	}
	c.at = f.position(i)
	size := 1
	c.verbRune = rune(spec[i])
	if !f.asBytes {
		c.verbRune, size = utf8.DecodeRuneInString(spec[i:])
	}
	if c.verbRune < utf8.RuneSelf {
		c.verb = byte(c.verbRune)
	}
	return i + size, nil
}

// boundedDigits reads a run of ASCII digits, refusing one above limit -- the
// check CPython's printf parser makes as it accumulates, PY_SSIZE_T_MAX for a
// width and INT_MAX for a precision. strconv.Atoi clamped an out-of-range run
// to MaxInt64 and reported it in an error nothing read, so a long precision
// became 9223372036854775807: fmt printed "%!(NOVERB)" for a float and %d
// tried to allocate that many zeros.
func boundedDigits(s string, limit int64) (int64, bool) {
	var n int64
	for i := 0; i < len(s); i++ {
		d := int64(s[i] - '0')
		if n > (limit-d)/10 {
			return 0, false
		}
		n = n*10 + d
	}
	return n, true
}

// takeStarInt reads the argument a `*` stands for. CPython converts a width
// with PyLong_AsSsize_t and a precision with PyLong_AsInt, so each overflows
// at its own C type -- `'%.*f' % (2**31, 1.5)` is "Python int too large to
// convert to C int" -- where a bare Int64 check called an integer that was
// merely large "not an int". 3.15 reports both as the argument being too big
// for what it was for.
//
// Under a Markup format the argument is markupsafe's helper, never an int, so
// a `*` is refused there whatever was passed: `('%*d'|safe) % (5, 3)` is
// "* wants int" in CPython.
func (f *percentFormatter) takeStarInt(c *conversion, precision bool) (int, error) {
	if f.py.PercentKeyExcludesPositional() && f.arglen < -1 {
		return 0, errs.New(errs.ValueError,
			"* cannot be used with a parenthesised mapping key at position %d", c.start)
	}
	v, err := f.nextArg(false)
	if err != nil {
		return 0, err
	}
	label := f.label(c)
	if f.escaping || !v.IsInteger() {
		if f.py.PercentErrorsNameTheArgument() {
			name := QualifiedTypeName(v)
			if f.escaping {
				name = markupHelperName
			}
			return 0, errs.New(errs.TypeError, "%s* requires int, not %s", label, name)
		}
		return 0, errs.New(errs.TypeError, "* wants int")
	}
	n, fits := v.Int64()
	if !fits {
		if f.py.PercentErrorsNameTheArgument() {
			return 0, errs.New(errs.OverflowError, "%stoo big for %s", label, map[bool]string{false: "width", true: "precision"}[precision])
		}
		if precision {
			return 0, errs.New(errs.OverflowError, "Python int too large to convert to C int")
		}
		return 0, errs.New(errs.OverflowError, "Python int too large to convert to C ssize_t")
	}
	if precision && (n > math.MaxInt32 || n < math.MinInt32) {
		if f.py.PercentErrorsNameTheArgument() {
			return 0, errs.New(errs.OverflowError, "%stoo big for precision", label)
		}
		return 0, errs.New(errs.OverflowError, "Python int too large to convert to C int")
	}
	return int(n), nil
}

// markupHelperName is the class markupsafe wraps every argument of a Markup
// format in, qualified the way CPython names a class that is not a builtin.
const markupHelperName = "markupsafe._MarkupEscapeHelper"

// Padding, sign placement and precision are applied here rather than handed to
// Go's fmt, because the two disagree in ways that reach rendered output. Go
// zero-pads `%05s` where Python pads with spaces; Go renders `%.0d` of zero as
// nothing where Python renders "0"; Go writes `+Inf` where Python writes `inf`;
// Go's `%#o` is `010` where Python's is `0o10`; and Go refuses a width past ten
// million by emitting `%!(NOVERB)` into the output -- which, since the
// expression is constant, was folded into the compiled template and rendered on
// every pass. fmt is still used for the digits of a number, where it agrees.

// formatted is one conversion's text, split where zero-fill has to go: after
// any sign and any radix prefix, and before the digits.
type formatted struct {
	prefix string // a sign, and "0x" and friends for an alternate form
	body   string
	// numeric marks a conversion the 0 flag applies to. Python ignores it
	// for %s, %r, %a and %c, which pad with spaces however it is spelled.
	numeric bool
}

func (c *conversion) has(flag byte) bool { return strings.IndexByte(c.flags, flag) >= 0 }

// sign is the leading sign Python gives a number: always for a negative one,
// and for a positive one only when asked.
func (c *conversion) sign(negative bool) string {
	switch {
	case negative:
		return "-"
	case c.has('+'):
		return "+"
	case c.has(' '):
		return " "
	}
	return ""
}

// pad lays the conversion out to its width, charging the fill before building
// it. The width comes from the template -- directly, or through `%*s` -- so it
// is exactly the kind of number that has to be paid for before it is spent.
func (c *conversion) pad(f formatted, budget Budget) (string, error) {
	if !c.hasWidth {
		return f.prefix + f.body, nil
	}
	// Width counts characters, as it does in Python: `"%5s" % "é"` is four
	// spaces and one character, not three spaces and two bytes.
	fill := int64(c.width) - int64(StrLen(f.prefix)+StrLen(f.body))
	if fill <= 0 {
		return f.prefix + f.body, nil
	}
	if err := chargeBytes(budget, fill); err != nil {
		return "", err
	}
	switch {
	case c.has('-'):
		// Left-adjust wins over zero-fill, as it does in C and Python.
		return f.prefix + f.body + strings.Repeat(" ", int(fill)), nil
	case c.has('0') && f.numeric:
		return f.prefix + strings.Repeat("0", int(fill)) + f.body, nil
	default:
		return strings.Repeat(" ", int(fill)) + f.prefix + f.body, nil
	}
}

// bytesArg formats %b and %s for a bytes format string, which take a bytes-like
// object and nothing else -- not a str, not a number, and not anything str()
// would have accepted. The message names %b whichever of the two was written.
func (c *conversion) bytesArg(v Value) (formatted, error) {
	if v.kind != KindBytes {
		if c.py.PercentErrorsNameTheArgument() {
			// %T rather than a quoted tp_name: qualified, and bare.
			return formatted{}, errs.New(errs.TypeError,
				"%s%%b requires a bytes-like object, "+
					"or an object that implements __bytes__, not %s", c.label, QualifiedTypeName(v))
		}
		return formatted{}, errs.New(errs.TypeError,
			"%%b requires a bytes-like object, "+
				"or an object that implements __bytes__, not '%s'", v.TypeName())
	}
	// A bytes is never Markup, so there is no escaping helper to apply.
	return formatted{body: c.truncate(v.str)}, nil
}

// errPercentCBytes is errPercentC for a bytes format, which has a message of its
// own -- and 3.14 appended the type to both of them, not just the str one.
func (c *conversion) errPercentCBytes(what string) error {
	if c.py.PercentErrorsNameTheArgument() {
		return errs.New(errs.TypeError,
			"%s%%c requires an integer in range(256) or a single byte, not %s", c.label, what)
	}
	if c.py.PercentCNamesTheType() {
		return errs.New(errs.TypeError,
			"%%c requires an integer in range(256) or a single byte, not %s", what)
	}
	return errs.New(errs.TypeError,
		"%%c requires an integer in range(256) or a single byte")
}

// errPercentC words %c's refusal for the chosen interpreter. Before 3.14 every
// wrong argument got the same sentence; 3.14 names what it got instead, and
// spells a wrong-length string as "a string of length N" rather than by type;
// 3.15 says "an integer" where 3.14 said "an int", and names the argument.
func (c *conversion) errPercentC(what string) error {
	if c.py.PercentErrorsNameTheArgument() {
		return errs.New(errs.TypeError,
			"%s%%c requires an integer or a unicode character, not %s", c.label, what)
	}
	if c.py.PercentCNamesTheType() {
		return errs.New(errs.TypeError,
			"%%c requires an int or a unicode character, not %s", what)
	}
	return errs.New(errs.TypeError, "%%c requires int or char")
}

// errPercentCRange is %c's refusal of an integer that is not a code point (or,
// for a bytes format, not a byte).
func (c *conversion) errPercentCRange(bound string) error {
	if c.py.PercentErrorsNameTheArgument() {
		return errs.New(errs.OverflowError, "%s%%c argument not in range(%s)", c.label, bound)
	}
	return errs.New(errs.OverflowError, "%%c arg not in range(%s)", bound)
}

// errNumberRequired is the refusal of an argument a numeric verb cannot use.
// want is "an integer" for %o, %x and %X and "a real number" for the rest;
// what is the class 3.15 names, qualified, and older the class before 3.15
// words it, by tp_name.
func (c *conversion) errNumberRequired(want, what, older string) error {
	if c.py.PercentErrorsNameTheArgument() {
		return errs.New(errs.TypeError, "%s%%%c requires %s, not %s", c.label, c.verb, want, what)
	}
	return errs.New(errs.TypeError, "%%%c format: %s is required, not %s", c.verb, want, older)
}

// errUnsupported is an unknown conversion character.
//
// Through 3.14 it is "unsupported format character 'q' (0x71) at index 1",
// naming the character's own offset -- with '?' standing in for anything
// outside printable ASCII in a str format, and, in a bytes format, the byte
// passed to PyErr_Format's %c as a *signed* char, so that anything past 0x7f is
// a negative code point and the message itself fails to format.
//
// 3.15 splits it by what the character is: a letter is "unsupported format
// %q at position 0", naming where the '%' was; anything else is taken to be a
// '%' that was never meant as one -- "stray % at position 0 or unexpected
// format character '!' at position 1" -- spelled by code point when it is not
// printable ASCII.
func (c *conversion) errUnsupported() error {
	r := c.verbRune
	if !c.py.PercentErrorsGivePositions() {
		if c.bytes {
			if r >= 0x80 {
				return errs.New(errs.OverflowError, "character argument not in range(0x110000)")
			}
			return errs.New(errs.ValueError,
				"unsupported format character '%c' (0x%x) at index %d", r, r, c.at)
		}
		shown := r
		if r < 31 || r > 126 {
			shown = '?'
		}
		return errs.New(errs.ValueError,
			"unsupported format character '%c' (0x%x) at index %d", shown, r, c.at)
	}
	isAlpha := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
	switch {
	case isAlpha:
		return errs.New(errs.ValueError,
			"unsupported format %%%c at position %d", r, c.start)
	case r == '\'':
		return errs.New(errs.ValueError,
			"stray %% at position %d or unexpected format character \"'\" at position %d",
			c.start, c.at)
	case r >= 32 && r < 127:
		return errs.New(errs.ValueError,
			"stray %% at position %d or unexpected format character '%c' at position %d",
			c.start, r, c.at)
	case c.bytes:
		return errs.New(errs.ValueError,
			"stray %% at position %d or unexpected format character with code 0x%02x at position %d",
			c.start, r, c.at)
	case UnicodeFor(c.py).PrintableWith(r):
		return errs.New(errs.ValueError,
			"stray %% at position %d or unexpected format character '%c' (U+%04X) at position %d",
			c.start, r, r, c.at)
	}
	return errs.New(errs.ValueError,
		"stray %% at position %d or unexpected format character U+%04X at position %d",
		c.start, r, c.at)
}

func (c *conversion) apply(v Value, escaping bool, budget Budget) (string, error) {
	f, err := c.convert(v, escaping)
	if err != nil {
		return "", err
	}
	return c.pad(f, budget)
}

// convert renders the value itself, without width.
func (c *conversion) convert(v Value, escaping bool) (formatted, error) {
	// A Markup format escapes whatever it substitutes, unless that value is
	// itself Markup.
	text := func(s string) string {
		if escaping && !v.safe {
			return EscapeHTML(s)
		}
		return s
	}

	// An undefined fails differently depending on which dunder the
	// conversion reaches, and the split is not guessable -- it is which
	// ones jinja2's Undefined bothers to define.
	//
	//	%s %r %a   str() and repr() answer, so these render
	//	%d %f ...  __int__ and __float__ are defined, and raise the
	//	           undefined's own error, naming the missing variable
	//	%x %o %c   go through __index__, which Undefined does not
	//	           define, so CPython raises its own TypeError instead
	//
	// A *bytes* format's float verbs are the exception through 3.14:
	// formatfloat in bytesobject.c replaced whatever the conversion raised
	// with its own "float argument required, not Undefined", so the
	// undefined's error never got out there. Its integer verbs did not do
	// that, which is why this is the float half and not the whole set. 3.15
	// lets the conversion's own error through, as the str side always had.
	if v.IsUndefined() && strings.IndexByte("diueEfFgG", c.verb) >= 0 &&
		(!c.bytes || strings.IndexByte("diu", c.verb) >= 0 || c.py.BytesFloatConvertsLikeStr()) {
		return formatted{}, v.UndefinedError()
	}

	// A Markup format string is markupsafe's __mod__, not str's. It wraps
	// each argument in a helper that defines __str__, __repr__, __int__ and
	// __float__ and nothing else, and which of those a conversion reaches
	// decides what happens:
	//
	//	%r %a      escape the repr, even of a value that is already Markup
	//	%d %f ...  go through int() and float(), which *coerce a string*
	//	           and raise ValueError rather than TypeError when it does
	//	           not parse
	//	%x %o %c   want __index__, which the helper does not define, so
	//	           they refuse every argument outright
	//
	// The last line is why this is not only a conformance detail: `%c` was
	// writing a raw character into a value the template had been told was
	// trusted, so `{{ "%c"|safe % 60 }}` emitted an unescaped "<".
	if escaping {
		if out, handled, err := c.markupConvert(v); handled {
			return out, err
		}
	}

	switch c.verb {
	// '%' is deliberately absent. CPython writes a literal percent only
	// for the two characters "%%", tested before a conversion is parsed at
	// all; once flags, a width, a precision or a mapping key intervene,
	// the '%' is a conversion character like any other, and there is no
	// case for it -- so `"%5%"` takes an argument and then reports an
	// unsupported format character. Laying out a padded "%" instead let
	// `{{ "%281%2C+2%29=x" % 2 }}`, which is what a urlencoded tuple key
	// looks like, format quietly where CPython refuses.
	case 'b':
		// Only a bytes format has %b; a str one has no case for it and
		// reports an unsupported format character, as CPython does.
		if !c.bytes {
			return formatted{}, c.errUnsupported()
		}
		return c.bytesArg(v)
	case 's':
		if c.bytes {
			// %s is an alias for %b here, and says so when it
			// refuses: CPython's message names %b either way.
			return c.bytesArg(v)
		}
		// %s is str(), which a StrictUndefined refuses. %r and %a are
		// repr() and ascii(), which it does not -- Undefined leaves
		// __repr__ alone under every class.
		if err := StrictRefusal(v); err != nil {
			return formatted{}, err
		}
		s, err := StrErr(v, c.py)
		if err != nil {
			return formatted{}, err
		}
		return formatted{body: c.truncate(text(s))}, nil
	case 'r':
		if c.bytes {
			// A bytes cannot hold repr()'s non-ASCII, so %r is
			// ascii() there -- the same answer %a gives.
			s, err := AsciiErr(v, c.py)
			if err != nil {
				return formatted{}, err
			}
			return formatted{body: c.truncate(text(s))}, nil
		}
		s, err := ReprErr(v, c.py)
		if err != nil {
			return formatted{}, err
		}
		return formatted{body: c.truncate(text(s))}, nil
	case 'a':
		s, err := AsciiErr(v, c.py)
		if err != nil {
			return formatted{}, err
		}
		return formatted{body: c.truncate(text(s))}, nil

	case 'c':
		if c.bytes {
			// A byte, or a code point that fits in one.
			if v.kind == KindBytes {
				if len(v.str) != 1 {
					// A bytes of the wrong length is named by
					// its length in 3.14, the way the padding
					// methods name a fill's: "not a bytes
					// object of length 2" rather than "not
					// bytes". Anything that is not a bytes at
					// all keeps the plain type name --
					// str.center's argument 2 splits the same
					// way, see FillCharMessageNamesTheLength.
					return formatted{}, c.errPercentCBytes(
						fmt.Sprintf("a bytes object of length %d", len(v.str)))
				}
				return formatted{body: text(v.str)}, nil
			}
			if !v.IsInteger() {
				// Qualified, as the str form above is: the two
				// `%c` messages are the pair 3.14 changed.
				return formatted{}, c.errPercentCBytes(QualifiedTypeName(v))
			}
			n, ok := v.Int64()
			if !ok || n < 0 || n > 255 {
				return formatted{}, c.errPercentCRange("256")
			}
			// One byte, not the code point's encoding: `b'%c' % 205`
			// is b'\xcd' where the str form is 'Í'. Writing the rune
			// put two bytes in for everything over 127.
			return formatted{body: text(string([]byte{byte(n)}))}, nil
		}
		// Precision is accepted and ignored, as in Python.
		if v.kind == KindString {
			if n := StrLen(v.str); n != 1 {
				return formatted{}, c.errPercentC(
					fmt.Sprintf("a string of length %d", n))
			}
			return formatted{body: text(v.str)}, nil
		}
		// Not an integer at all and an integer out of range fail
		// differently in Python, and the distinction is the useful
		// one: the first says %c took the wrong kind of thing, the
		// second says the code point does not exist.
		if !v.IsInteger() {
			// The *qualified* name, which is the only thing 3.14
			// qualifies in this family: `%c` of an Undefined is
			// "not jinja2.runtime.Undefined" while `%f` of the same
			// value is "not Undefined" and `%x` "not Undefined".
			// A builtin is unqualified either way, so a dict view
			// stays "dict_keys".
			return formatted{}, c.errPercentC(QualifiedTypeName(v))
		}
		n, ok := v.Int64()
		if !ok || n < 0 || n > 0x10FFFF {
			return formatted{}, c.errPercentCRange("0x110000")
		}
		return formatted{body: text(string(rune(n)))}, nil

	case 'd', 'i', 'u':
		// These three accept a float and truncate it toward zero; the
		// radix conversions below do not.
		digits, negative, err := c.integerDigits(v, 10, true)
		if err != nil {
			return formatted{}, err
		}
		return formatted{prefix: c.sign(negative), body: digits, numeric: true}, nil

	case 'o', 'x', 'X':
		base := map[byte]int{'o': 8, 'x': 16, 'X': 16}[c.verb]
		digits, negative, err := c.integerDigits(v, base, false)
		if err != nil {
			return formatted{}, err
		}
		if c.verb == 'X' {
			digits = strings.ToUpper(digits)
		}
		prefix := c.sign(negative)
		if c.has('#') {
			// Python's alternate form is Python's own spelling:
			// 0o, 0x, 0X -- not C's bare leading zero for octal.
			prefix += map[byte]string{'o': "0o", 'x': "0x", 'X': "0X"}[c.verb]
		}
		return formatted{prefix: prefix, body: digits, numeric: true}, nil

	case 'e', 'E', 'f', 'F', 'g', 'G':
		return c.floatBody(v)
	}
	return formatted{}, c.errUnsupported()
}

// truncate applies a string conversion's precision, which counts characters.
func (c *conversion) truncate(s string) string {
	if !c.hasPrec {
		return s
	}
	if out, err := StrSlice(s, nil, &c.prec, nil); err == nil {
		return out
	}
	return s
}

// integerDigits renders the magnitude of an integer argument in the given
// base, and reports whether it was negative.
//
// allowFloat separates %d, %i and %u -- which truncate a float toward zero --
// from %o, %x and %X, which Python refuses one for outright.
func (c *conversion) integerDigits(v Value, base int, allowFloat bool) (string, bool, error) {
	var b *big.Int
	switch {
	case v.IsInteger():
		b, _ = v.BigInt()
	case v.kind == KindFloat && allowFloat:
		f := v.AsFloat()
		// Python separates these: an infinity is out of range for any
		// integer, a NaN is not a number to convert at all.
		if math.IsNaN(f) {
			return "", false, errs.New(errs.ValueError,
				"cannot convert float NaN to integer")
		}
		if math.IsInf(f, 0) {
			return "", false, errs.New(errs.OverflowError,
				"cannot convert float infinity to integer")
		}
		b, _ = big.NewFloat(math.Trunc(f)).Int(nil)
	default:
		// CPython words the two families differently, and the wording
		// is the distinction: %d, %i and %u take a real number and
		// truncate it, while %o, %x and %X take an integer only.
		want := "an integer"
		if allowFloat {
			want = "a real number"
		}
		return "", false, c.errNumberRequired(want, QualifiedTypeName(v), v.TypeName())
	}

	// Decimal text is limited and %o, %x and %X are not: CPython refuses
	// `'%d' % (10 ** 5000)` and answers `'%x' % (10 ** 5000)`.
	if base == 10 {
		if err := BigIntStrRefusal(b); err != nil {
			return "", false, err
		}
	}
	negative := b.Sign() < 0
	digits := new(big.Int).Abs(b).Text(base)
	// For an integer, precision is a minimum number of digits. Python
	// keeps the single zero that C's "%.0d" of zero drops.
	if c.hasPrec && len(digits) < c.prec {
		if err := chargeBytes(c.budget, int64(c.prec-len(digits))); err != nil {
			return "", false, err
		}
		digits = strings.Repeat("0", c.prec-len(digits)) + digits
	}
	return digits, negative, nil
}

// floatBody renders a float conversion, in Python's spelling.
func (c *conversion) floatBody(v Value) (formatted, error) {
	if c.bytes && !c.py.BytesFloatConvertsLikeStr() {
		// PyBytes_Format words this after the verb rather than after the
		// type: "float argument required, not str" where
		// PyUnicode_Format says "must be real number, not str". It says
		// the same for an integer too wide for a float64 -- "float
		// argument required, not int" -- where the str side reports the
		// overflow. One conversion either succeeds or does not; it does
		// not distinguish why. The integer verbs agree on both sides;
		// only the float ones split -- until 3.15, which made the bytes
		// side the str side's.
		if _, err := FloatOrOverflow(v); !v.IsNumber() || err != nil {
			return formatted{}, errs.New(errs.TypeError,
				"float argument required, not %s", v.TypeName())
		}
	}
	f, err := floatOperand(v)
	if err != nil {
		// Both halves come from floatOperand: "must be real number" for
		// something that is not one, and the OverflowError for an
		// integer too wide for a float64, which `%f` printed as "inf".
		// 3.15 replaces the TypeError with its own, naming the verb; the
		// overflow is not a TypeError and comes through as it is.
		if c.py.PercentErrorsNameTheArgument() && errs.KindOf(err) == errs.TypeError {
			return formatted{}, c.errNumberRequired("a real number", QualifiedTypeName(v), "")
		}
		return formatted{}, err
	}
	negative := math.Signbit(f)
	sign := c.sign(negative)

	// Python writes inf and nan in words rather than in Go's spelling, and
	// zero-pads them like any other number: "%09f" of infinity is
	// "000000inf".
	switch {
	case math.IsNaN(f):
		body := "nan"
		if c.verb == 'E' || c.verb == 'F' || c.verb == 'G' {
			body = "NAN"
		}
		// A NaN is never negative in Python's output, however its sign
		// bit happens to be set.
		return formatted{prefix: c.sign(false), body: body, numeric: true}, nil
	case math.IsInf(f, 0):
		body := "inf"
		if c.verb == 'E' || c.verb == 'F' || c.verb == 'G' {
			body = "INF"
		}
		return formatted{prefix: sign, body: body, numeric: true}, nil
	}

	prec := 6
	if c.hasPrec {
		prec = c.prec
	}
	verb := c.verb
	if verb == 'F' {
		verb = 'f'
	}
	spec := "%"
	if c.has('#') {
		spec += "#"
	}
	spec += "." + strconv.Itoa(prec) + string(verb)
	// The digits a precision asks for are allocated by Sprintf, so they are
	// paid for first. Up to INT_MAX is CPython's to attempt; the parser
	// refused anything past it.
	if err := chargeBytes(c.budget, int64(prec)); err != nil {
		return formatted{}, err
	}
	body := fmt.Sprintf(spec, math.Abs(f))
	return formatted{prefix: sign, body: body, numeric: true}, nil
}

// markupConvert applies the conversions markupsafe's argument helper changes.
// handled is false for the ones it leaves to the ordinary path.
func (c *conversion) markupConvert(v Value) (out formatted, handled bool, err error) {
	switch c.verb {
	case 'o', 'x', 'X':
		// No __index__ on the helper, so the argument is never right.
		return formatted{}, true, c.errNumberRequired("an integer", markupHelperName, "_MarkupEscapeHelper")
	case 'c':
		// The operand reaching markupConvert is wrapped in markupsafe's
		// escape helper, and 3.14 names the wrapper rather than what is
		// inside it.
		return formatted{}, true, c.errPercentC(markupHelperName)

	case 'r', 'a':
		// repr() of a Markup is text *about* the markup, so it is
		// escaped like any other text -- including when the value it
		// describes is itself Markup.
		text, err := ReprErr(v, c.py)
		if c.verb == 'a' {
			text, err = AsciiErr(v, c.py)
		}
		if err != nil {
			return formatted{}, true, err
		}
		return formatted{body: c.truncate(EscapeHTML(text))}, true, nil

	case 'd', 'i', 'u':
		n, err := markupInt(v, c.verb, c.py)
		if err != nil {
			if errs.KindOf(err) == errs.TypeError {
				err = c.errNumberRequired("a real number", markupHelperName, "_MarkupEscapeHelper")
			}
			return formatted{}, true, err
		}
		digits, negative, err := c.padDigits(n)
		if err != nil {
			return formatted{}, true, err
		}
		return formatted{prefix: c.sign(negative), body: digits, numeric: true}, true, nil

	case 'e', 'E', 'f', 'F', 'g', 'G':
		f, err := markupFloat(v, c.py)
		if err != nil {
			// float() over the helper raises its own TypeError, which
			// 3.15's formatfloat replaces with the verb's; a string that
			// does not parse is a ValueError and passes through.
			if c.py.PercentErrorsNameTheArgument() && errs.KindOf(err) == errs.TypeError {
				err = c.errNumberRequired("a real number", markupHelperName, "")
			}
			return formatted{}, true, err
		}
		out, err := c.floatBody(Float(f))
		return out, true, err
	}
	return formatted{}, false, nil
}

// padDigits renders an integer's magnitude and applies the minimum-digit
// precision, which is the part %d shares between the two paths.
func (c *conversion) padDigits(b *big.Int) (string, bool, error) {
	// %d is decimal text, which CPython refuses past MaxStrDigits; %x and %o
	// are not, and do not come through here.
	if err := BigIntStrRefusal(b); err != nil {
		return "", false, err
	}
	digits := new(big.Int).Abs(b).Text(10)
	if c.hasPrec && len(digits) < c.prec {
		if err := chargeBytes(c.budget, int64(c.prec-len(digits))); err != nil {
			return "", false, err
		}
		digits = strings.Repeat("0", c.prec-len(digits)) + digits
	}
	return digits, b.Sign() < 0, nil
}

// markupInt is Python's int() over the helper: a number truncates, a string is
// parsed, and anything else is the conversion's own type error.
func markupInt(v Value, verb byte, py PythonVersion) (*big.Int, error) {
	switch {
	case v.IsInteger():
		b, _ := v.BigInt()
		return b, nil
	case v.kind == KindFloat:
		f := v.AsFloat()
		if math.IsNaN(f) {
			return nil, errs.New(errs.ValueError, "cannot convert float NaN to integer")
		}
		if math.IsInf(f, 0) {
			return nil, errs.New(errs.OverflowError, "cannot convert float infinity to integer")
		}
		b, _ := big.NewFloat(math.Trunc(f)).Int(nil)
		return b, nil
	case v.kind == KindString:
		text, ok := pyNumericText(v.str, false, py)
		// Decimal text past CPython's digit limit is refused before it
		// is converted, which is the conversion the limit exists to
		// keep from running.
		if err := intTextLimit(v.str, text, ok, 10, py); err != nil {
			return nil, err
		}
		if ok {
			if b, good := new(big.Int).SetString(text, 10); good {
				return b, nil
			}
		}
		return nil, errs.New(errs.ValueError,
			"invalid literal for int() with base 10: %s", TruncatedRepr(v, py))
	}
	return nil, errs.New(errs.TypeError,
		"%%%c format: a real number is required, not _MarkupEscapeHelper", verb)
}

// markupFloat is Python's float() over the helper.
func markupFloat(v Value, py PythonVersion) (float64, error) {
	if f, ok := v.Float64(); ok {
		return f, nil
	}
	if v.kind == KindString {
		if text, ok := pyNumericText(v.str, true, py); ok {
			if f, ok := ParseFloat(text, py); ok {
				return f, nil
			}
		}
		return 0, errs.New(errs.ValueError,
			"could not convert string to float: %s", ReprFor(v, py))
	}
	return 0, errs.New(errs.TypeError,
		"float() argument must be a string or a real number, not '%s'", v.TypeName())
}

// pyNumericText prepares a string for int() or float(): Python trims
// surrounding whitespace and allows single underscores between digits, neither
// of which Go's parsers accept.
func pyNumericText(s string, isFloat bool, py PythonVersion) (string, bool) {
	// Decimal digits from any script become ASCII first, exactly as
	// CPython transforms them, so the rules below are about the shape of
	// the number and not about which alphabet wrote it.
	s = strings.TrimFunc(DecimalASCII(s, py), unicode.IsSpace)
	if s == "" {
		return "", false
	}
	if !strings.Contains(s, "_") {
		return s, true
	}
	var b strings.Builder
	for i := range len(s) {
		if s[i] != '_' {
			b.WriteByte(s[i])
			continue
		}
		// An underscore must sit between two digits.
		if i == 0 || i == len(s)-1 || !isASCIIDigit(s[i-1]) || !isASCIIDigit(s[i+1]) {
			return "", false
		}
	}
	return b.String(), true
}

func isASCIIDigit(b byte) bool { return b >= '0' && b <= '9' }
