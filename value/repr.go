// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package value

import (
	"math"
	"math/big"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Str is Python's str(): how a value renders when printed on its own.
//
// It differs from Repr only for strings, which print bare, and for the
// undefined value, which prints as nothing. Containers print their elements
// with Repr, which is why `{{ ["a"] }}` renders `['a']` and not `[a]`.
func Str(v Value) string {
	switch v.kind {
	case KindString:
		return v.str
	case KindUndefined:
		if v.undef().behavior == UndefinedDebug {
			if text, ok := v.DebugText(); ok {
				return text
			}
		}
		return ""
	case KindObject:
		if s, ok := v.obj.(Strer); ok {
			return s.Str()
		}
	}
	return Repr(v)
}

// Repr is Python's repr(): the form a value takes inside a container.
func Repr(v Value) string {
	var b strings.Builder
	writeRepr(&b, v)
	return b.String()
}

func writeRepr(b *strings.Builder, v Value) {
	switch v.kind {
	case KindUndefined:
		b.WriteString("Undefined")
	case KindNone:
		b.WriteString("None")
	case KindBool:
		if v.num != 0 {
			b.WriteString("True")
		} else {
			b.WriteString("False")
		}
	case KindInt:
		if big, ok := v.obj.(*big.Int); ok {
			b.WriteString(big.String())
		} else {
			b.WriteString(strconv.FormatInt(int64(v.num), 10))
		}
	case KindFloat:
		b.WriteString(FormatFloat(v.AsFloat()))
	case KindString:
		writeStringRepr(b, v.str)
	case KindBytes:
		b.WriteByte('b')
		writeStringRepr(b, v.str)
	case KindList:
		s, _ := v.Seq()
		b.WriteByte('[')
		for i, item := range s.items {
			if i > 0 {
				b.WriteString(", ")
			}
			writeRepr(b, item)
		}
		b.WriteByte(']')
	case KindTuple:
		s, _ := v.Seq()
		b.WriteByte('(')
		for i, item := range s.items {
			if i > 0 {
				b.WriteString(", ")
			}
			writeRepr(b, item)
		}
		// A one-element tuple needs the trailing comma to stay a tuple.
		if len(s.items) == 1 {
			b.WriteByte(',')
		}
		b.WriteByte(')')
	case KindDict:
		d, _ := v.Dict()
		b.WriteByte('{')
		for i, e := range d.entries {
			if i > 0 {
				b.WriteString(", ")
			}
			writeRepr(b, e.Key)
			b.WriteString(": ")
			writeRepr(b, e.Value)
		}
		b.WriteByte('}')
	case KindObject:
		if r, ok := v.obj.(Reprer); ok {
			b.WriteString(r.Repr())
			return
		}
		b.WriteString("<object>")
	case KindFunc:
		b.WriteString("<function>")
	}
}

// FormatFloat renders a float the way CPython's repr does.
//
// Go's %g and Python's repr both emit the shortest round-tripping digits but
// disagree on when to switch to exponent form, so the digits come from Go and
// the layout from Python's rule: fixed notation while the decimal point sits
// in (-4, 16], exponential otherwise, always with a visible fractional part
// and a signed two-digit exponent.
func FormatFloat(f float64) string {
	switch {
	case math.IsNaN(f):
		return "nan"
	case math.IsInf(f, 1):
		return "inf"
	case math.IsInf(f, -1):
		return "-inf"
	}

	// Shortest round-trip digits, as d.dddde±dd.
	sci := strconv.FormatFloat(f, 'e', -1, 64)
	neg := strings.HasPrefix(sci, "-")
	if neg {
		sci = sci[1:]
	}
	mantissa, expPart, _ := strings.Cut(sci, "e")
	exp, _ := strconv.Atoi(expPart)
	digits := strings.Replace(mantissa, ".", "", 1)
	// decpt is where the decimal point falls relative to the digit string.
	decpt := exp + 1

	var b strings.Builder
	if neg {
		b.WriteByte('-')
	}
	switch {
	case decpt <= -4 || decpt > 16:
		b.WriteByte(digits[0])
		// A single significant digit gets no decimal point at all in
		// exponential form: repr(1e16) is "1e+16", not "1.0e+16".
		if len(digits) > 1 {
			b.WriteByte('.')
			b.WriteString(digits[1:])
		}
		b.WriteByte('e')
		e := decpt - 1
		if e < 0 {
			b.WriteByte('-')
			e = -e
		} else {
			b.WriteByte('+')
		}
		if e < 10 {
			b.WriteByte('0')
		}
		b.WriteString(strconv.Itoa(e))
	case decpt <= 0:
		b.WriteString("0.")
		b.WriteString(strings.Repeat("0", -decpt))
		b.WriteString(digits)
	case decpt >= len(digits):
		b.WriteString(digits)
		b.WriteString(strings.Repeat("0", decpt-len(digits)))
		b.WriteString(".0")
	default:
		b.WriteString(digits[:decpt])
		b.WriteByte('.')
		b.WriteString(digits[decpt:])
	}
	return b.String()
}

// writeStringRepr renders a str the way Python's repr does: single quotes
// unless that would need escaping and double quotes would not, non-ASCII left
// intact when printable, and the rest escaped shortest-first.
func writeStringRepr(b *strings.Builder, s string) {
	quote := byte('\'')
	if strings.ContainsRune(s, '\'') && !strings.ContainsRune(s, '"') {
		quote = '"'
	}
	b.WriteByte(quote)
	for _, r := range s {
		switch {
		case r == rune(quote) || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r == utf8.RuneError:
			// Invalid UTF-8 survived the decode; show it as a byte.
			b.WriteString(`�`)
		case printable(r):
			b.WriteRune(r)
		case r < 0x100:
			b.WriteString(`\x`)
			writeHex(b, uint32(r), 2)
		case r < 0x10000:
			b.WriteString(`\u`)
			writeHex(b, uint32(r), 4)
		default:
			b.WriteString(`\U`)
			writeHex(b, uint32(r), 8)
		}
	}
	b.WriteByte(quote)
}

func writeHex(b *strings.Builder, v uint32, width int) {
	s := strconv.FormatUint(uint64(v), 16)
	for i := len(s); i < width; i++ {
		b.WriteByte('0')
	}
	b.WriteString(s)
}

// printable implements Python's str.isprintable for a single rune: graphic
// characters plus ASCII space, but no other whitespace separator -- a
// non-breaking space is escaped by repr, unlike in Go's notion of printable.
func printable(r rune) bool {
	if r == ' ' {
		return true
	}
	return unicode.IsGraphic(r) && !unicode.Is(unicode.Zs, r)
}
