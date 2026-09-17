// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package value

import (
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"

	"github.com/mgilbir/gojja2/errs"
)

// FormatPercent implements `"..." % args`, Python's printf-style string
// formatting, which templates reach for often enough that leaving it out would
// be a visible gap.
//
// The right operand decides how arguments are consumed: a tuple is positional,
// a mapping is consulted by name when the format uses `%(key)s`, and anything
// else -- a bare dict included -- is a single argument.
func FormatPercent(format, args Value, budget Budget) (Value, error) {
	spec := format.str
	// markupsafe wraps each argument so that it escapes as it is
	// substituted, and returns Markup. Escaping happens *before* padding,
	// so a width applies to the escaped text.
	escaping := format.safe

	// CPython decides between "a mapping" and "one positional argument" by
	// asking whether the right operand supports subscripting, excluding
	// tuple and str. A list therefore counts as a mapping -- which is why
	// `"" % []` is "" while `"" % 1` is a TypeError -- even though looking
	// a name up in one can only fail. The operand is still available as the
	// single positional argument either way, so `"%s" % {}` renders "{}".
	mapping, hasMapping := args, isMappingArg(args)

	positional := []Value{args}
	if s, ok := args.Seq(); ok && args.kind == KindTuple {
		positional = s.items
	}

	var out strings.Builder
	next := 0
	takeArg := func() (Value, error) {
		if next >= len(positional) {
			return Undefined, errs.New(errs.TypeError, "not enough arguments for format string")
		}
		v := positional[next]
		next++
		return v, nil
	}

	for i := 0; i < len(spec); {
		c := spec[i]
		if c != '%' {
			out.WriteByte(c)
			i++
			continue
		}
		i++
		if i >= len(spec) {
			return Undefined, errs.New(errs.ValueError, "incomplete format")
		}
		if spec[i] == '%' {
			out.WriteByte('%')
			i++
			continue
		}

		var conv conversion
		var err error
		if i, err = parseConversion(spec, i, &conv); err != nil {
			return Undefined, err
		}
		// CPython reports where an unknown conversion was found, and
		// the index it gives is of the character after the verb.
		conv.at = i

		// `*` pulls width and then precision from the argument stream,
		// both of them *before* the value. Taking the value first made
		// `"%*s" % (5, "x")` read the 5 as the string and the "x" as
		// the width, so every starred conversion failed.
		if conv.starWidth {
			w, err := takeStarInt(&positional, &next)
			if err != nil {
				return Undefined, err
			}
			// A negative width means left-adjust, as in C.
			if w < 0 {
				conv.flags, w = conv.flags+"-", -w
			}
			conv.width, conv.hasWidth = w, true
		}
		if conv.starPrec {
			p, err := takeStarInt(&positional, &next)
			if err != nil {
				return Undefined, err
			}
			// Python clamps a negative precision to zero rather
			// than treating it as absent, which is what C does.
			conv.prec, conv.hasPrec = max(p, 0), true
		}

		// Resolve the value this conversion formats.
		var arg Value
		switch {
		case conv.key != "":
			if !hasMapping {
				return Undefined, errs.New(errs.TypeError, "format requires a mapping")
			}
			v, err := lookupFormatKey(mapping, conv.key)
			if err != nil {
				return Undefined, err
			}
			arg = v
		case conv.verb == '%':
			// A bare %% is handled above; this is one that carried
			// flags or a width, which Python still lays out.
			arg = Undefined
		default:
			if arg, err = takeArg(); err != nil {
				return Undefined, err
			}
		}

		text, err := conv.apply(arg, escaping, budget)
		if err != nil {
			return Undefined, err
		}
		out.WriteString(text)
	}

	if !hasMapping && next != len(positional) {
		return Undefined, errs.New(errs.TypeError,
			"not all arguments converted during string formatting")
	}
	if escaping {
		return Safe(out.String()), nil
	}
	return String(out.String()), nil
}

// isMappingArg reports whether the right operand of % is subscriptable in
// CPython's sense: dict and list qualify, tuple and str explicitly do not.
func isMappingArg(v Value) bool {
	switch v.kind {
	case KindDict, KindList:
		return true
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

// lookupFormatKey resolves `%(name)s` against the right operand.
func lookupFormatKey(mapping Value, key string) (Value, error) {
	switch mapping.kind {
	case KindDict:
		d, _ := mapping.Dict()
		v, ok := d.GetString(key)
		if !ok {
			return Undefined, errs.New(errs.KeyError, "%s", Repr(String(key)))
		}
		return v, nil
	case KindList:
		// A list is subscriptable enough to be treated as a mapping but
		// cannot actually be indexed by name.
		return Undefined, errs.New(errs.TypeError,
			"list indices must be integers or slices, not str")
	case KindUndefined:
		// An undefined passes the subscript check -- it defines
		// __getitem__ -- and then raises its own error when the key is
		// actually looked up, which names the variable that was never
		// set rather than complaining about the operand's type.
		return Undefined, mapping.UndefinedError()
	case KindObject:
		if m, ok := mapping.Interface().(Mapping); ok {
			v, ok := m.GetItem(String(key))
			if !ok {
				return Undefined, errs.New(errs.KeyError, "%s", Repr(String(key)))
			}
			return v, nil
		}
	}
	return Undefined, errs.New(errs.TypeError, "format requires a mapping")
}

// conversion is one parsed `%...` directive.
type conversion struct {
	key       string // mapping key from %(name)s
	flags     string // any of "#0- +"
	width     int
	hasWidth  bool
	starWidth bool
	prec      int
	hasPrec   bool
	starPrec  bool
	verb      byte
	// at is the offset just past the verb, which is the position
	// CPython names when the verb is not one it knows.
	at int
}

func parseConversion(spec string, i int, c *conversion) (int, error) {
	// Mapping key.
	if i < len(spec) && spec[i] == '(' {
		depth, start := 1, i+1
		j := start
		for ; j < len(spec) && depth > 0; j++ {
			switch spec[j] {
			case '(':
				depth++
			case ')':
				depth--
			}
		}
		if depth != 0 {
			return 0, errs.New(errs.ValueError, "incomplete format key")
		}
		c.key = spec[start : j-1]
		i = j
	}

	for i < len(spec) && strings.IndexByte("#0- +", spec[i]) >= 0 {
		if !strings.ContainsRune(c.flags, rune(spec[i])) {
			c.flags += string(spec[i])
		}
		i++
	}

	if i < len(spec) && spec[i] == '*' {
		c.starWidth = true
		i++
	} else {
		start := i
		for i < len(spec) && spec[i] >= '0' && spec[i] <= '9' {
			i++
		}
		if i > start {
			c.width, _ = strconv.Atoi(spec[start:i])
			c.hasWidth = true
		}
	}

	if i < len(spec) && spec[i] == '.' {
		i++
		if i < len(spec) && spec[i] == '*' {
			c.starPrec = true
			i++
		} else {
			start := i
			for i < len(spec) && spec[i] >= '0' && spec[i] <= '9' {
				i++
			}
			c.prec, _ = strconv.Atoi(spec[start:i])
			c.hasPrec = true
		}
	}

	// Length modifiers are accepted and ignored, as in Python.
	for i < len(spec) && strings.IndexByte("hlL", spec[i]) >= 0 {
		i++
	}

	if i >= len(spec) {
		return 0, errs.New(errs.ValueError, "incomplete format")
	}
	c.verb = spec[i]
	return i + 1, nil
}

func takeStarInt(positional *[]Value, next *int) (int, error) {
	if *next >= len(*positional) {
		return 0, errs.New(errs.TypeError, "not enough arguments for format string")
	}
	v := (*positional)[*next]
	*next++
	n, ok := v.Int64()
	if !ok {
		return 0, errs.New(errs.TypeError, "* wants int")
	}
	return int(n), nil
}

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
	if v.IsUndefined() && strings.IndexByte("diueEfFgG", c.verb) >= 0 {
		return formatted{}, v.UndefinedError()
	}

	switch c.verb {
	case '%':
		return formatted{body: "%"}, nil
	case 's':
		return formatted{body: c.truncate(text(Str(v)))}, nil
	case 'r':
		return formatted{body: c.truncate(text(Repr(v)))}, nil
	case 'a':
		return formatted{body: c.truncate(text(Ascii(v)))}, nil

	case 'c':
		// Precision is accepted and ignored, as in Python.
		if v.kind == KindString {
			if StrLen(v.str) != 1 {
				return formatted{}, errs.New(errs.TypeError, "%%c requires int or char")
			}
			return formatted{body: text(v.str)}, nil
		}
		// Not an integer at all and an integer out of range fail
		// differently in Python, and the distinction is the useful
		// one: the first says %c took the wrong kind of thing, the
		// second says the code point does not exist.
		if !v.IsInteger() {
			return formatted{}, errs.New(errs.TypeError, "%%c requires int or char")
		}
		n, ok := v.Int64()
		if !ok || n < 0 || n > 0x10FFFF {
			return formatted{}, errs.New(errs.OverflowError, "%%c arg not in range(0x110000)")
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
	return formatted{}, errs.New(errs.ValueError,
		"unsupported format character '%c' (0x%x) at index %d", c.verb, c.verb, c.at-1)
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
		return "", false, errs.New(errs.TypeError,
			"%%%c format: %s is required, not %s", c.verb, want, v.TypeName())
	}

	negative := b.Sign() < 0
	digits := new(big.Int).Abs(b).Text(base)
	// For an integer, precision is a minimum number of digits. Python
	// keeps the single zero that C's "%.0d" of zero drops.
	if c.hasPrec && len(digits) < c.prec {
		digits = strings.Repeat("0", c.prec-len(digits)) + digits
	}
	return digits, negative, nil
}

// floatBody renders a float conversion, in Python's spelling.
func (c *conversion) floatBody(v Value) (formatted, error) {
	f, ok := v.Float64()
	if !ok {
		return formatted{}, errs.New(errs.TypeError,
			"must be real number, not %s", v.TypeName())
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
	body := fmt.Sprintf(spec, math.Abs(f))
	return formatted{prefix: sign, body: body, numeric: true}, nil
}
