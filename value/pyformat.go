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
func FormatPercent(format, args Value) (Value, error) {
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
			arg = Undefined
		default:
			if arg, err = takeArg(); err != nil {
				return Undefined, err
			}
		}

		// `*` pulls width and precision from the argument stream, ahead
		// of the value itself.
		if conv.starWidth {
			w, err := takeStarInt(&positional, &next)
			if err != nil {
				return Undefined, err
			}
			conv.width, conv.hasWidth = w, true
			if arg, err = takeArg(); err != nil {
				return Undefined, err
			}
		}
		if conv.starPrec {
			p, err := takeStarInt(&positional, &next)
			if err != nil {
				return Undefined, err
			}
			conv.prec, conv.hasPrec = p, true
			if arg, err = takeArg(); err != nil {
				return Undefined, err
			}
		}

		text, err := conv.apply(arg, escaping)
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

// goVerb assembles the equivalent Go format string. Go and C share the flag,
// width and precision grammar, so only the verb and the argument type need
// translating.
func (c *conversion) goVerb(verb byte) string {
	var b strings.Builder
	b.WriteByte('%')
	b.WriteString(c.flags)
	if c.hasWidth {
		b.WriteString(strconv.Itoa(c.width))
	}
	if c.hasPrec {
		b.WriteByte('.')
		b.WriteString(strconv.Itoa(c.prec))
	}
	b.WriteByte(verb)
	return b.String()
}

func (c *conversion) apply(v Value, escaping bool) (string, error) {
	// A Markup format escapes whatever it substitutes, unless that value is
	// itself Markup.
	text := func(s string) string {
		if escaping && !v.safe {
			return EscapeHTML(s)
		}
		return s
	}

	switch c.verb {
	case 's':
		return fmt.Sprintf(c.goVerb('s'), text(Str(v))), nil
	case 'r':
		return fmt.Sprintf(c.goVerb('s'), text(Repr(v))), nil
	case 'a':
		return fmt.Sprintf(c.goVerb('s'), text(Ascii(v))), nil

	case 'd', 'i', 'u', 'o', 'x', 'X':
		n, err := c.integerArg(v)
		if err != nil {
			return "", err
		}
		verb := c.verb
		switch verb {
		case 'i', 'u':
			verb = 'd'
		}
		return fmt.Sprintf(c.goVerb(verb), n), nil

	case 'e', 'E', 'f', 'F', 'g', 'G':
		f, ok := v.Float64()
		if !ok {
			return "", errs.New(errs.TypeError,
				"must be real number, not %s", v.TypeName())
		}
		verb := c.verb
		if verb == 'F' {
			verb = 'f'
		}
		return fmt.Sprintf(c.goVerb(verb), f), nil

	case 'c':
		if v.kind == KindString {
			if StrLen(v.str) != 1 {
				return "", errs.New(errs.TypeError, "%%c requires int or char")
			}
			return text(v.str), nil
		}
		n, ok := v.Int64()
		if !ok || n < 0 || n > 0x10FFFF {
			return "", errs.New(errs.OverflowError, "%%c arg not in range(0x110000)")
		}
		return string(rune(n)), nil
	}
	return "", errs.New(errs.ValueError,
		"unsupported format character '%c' (0x%x) at index %d", c.verb, c.verb, c.at-1)
}

// integerArg coerces to an integer the way Python's %d does, which accepts a
// float and truncates toward zero.
func (c *conversion) integerArg(v Value) (any, error) {
	switch {
	case v.IsInteger():
		if n, ok := v.Int64(); ok {
			return n, nil
		}
		b, _ := v.BigInt()
		return b, nil
	case v.kind == KindFloat:
		f := v.AsFloat()
		if math.IsInf(f, 0) || math.IsNaN(f) {
			return nil, errs.New(errs.OverflowError, "cannot convert float infinity to integer")
		}
		t := math.Trunc(f)
		if t >= math.MinInt64 && t <= math.MaxInt64 {
			return int64(t), nil
		}
		b, _ := big.NewFloat(t).Int(nil)
		return b, nil
	}
	return nil, errs.New(errs.TypeError,
		"%%%c format: a real number is required, not %s", c.verb, v.TypeName())
}
