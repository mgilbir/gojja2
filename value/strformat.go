// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package value

import (
	"math"
	"math/big"
	"strconv"
	"strings"

	"github.com/mgilbir/gojja2/errs"
)

// FormatValue is Python's format(v, spec) -- the __format__ behind every
// replacement field in str.format.
//
// It is a different mini-language from the one FormatPercent implements, and
// deliberately a separate one: `%` writes `%-8.3f` and this writes `:<8.3f`,
// the alignment characters mean different things, and `#` and the grouping
// options exist only here.
//
// An empty spec is str(v) for every type, which is why `{}` renders a list or
// None happily while `{:>8}` on either is a TypeError.
func FormatValue(v Value, spec string, budget Budget) (string, error) {
	if spec == "" {
		return Str(v), nil
	}
	f, err := parseFormatSpec(spec, v)
	if err != nil {
		return "", err
	}
	body, numeric, err := f.body(v, spec)
	if err != nil {
		return "", err
	}
	return f.pad(body, numeric, budget)
}

// formatSpec is [[fill]align][sign][#][0][width][grouping][.precision][type].
type formatSpec struct {
	fill     rune
	align    byte // '<' '>' '^' '=', or 0 when the type's default applies
	sign     byte // '+' '-' ' ', or 0
	alt      bool // '#'
	zero     bool // a leading '0', which is fill '0' with align '='
	width    int
	hasWidth bool
	grouping byte // ',' '_' or 0
	prec     int
	hasPrec  bool
	typ      byte
}

func parseFormatSpec(spec string, v Value) (formatSpec, error) {
	f := formatSpec{fill: ' '}
	r := []rune(spec)
	i := 0

	// A fill character is only a fill when an alignment follows it, so ">8"
	// is align-8 and "*>8" is fill-align-8.
	if len(r) >= 2 && isAlign(r[1]) {
		f.fill, f.align = r[0], byte(r[1])
		i = 2
	} else if len(r) >= 1 && isAlign(r[0]) {
		f.align = byte(r[0])
		i = 1
	}
	if i < len(r) && (r[i] == '+' || r[i] == '-' || r[i] == ' ') {
		f.sign = byte(r[i])
		i++
	}
	if i < len(r) && r[i] == '#' {
		f.alt = true
		i++
	}
	if i < len(r) && r[i] == '0' {
		// A leading zero is fill '0' and align '=', unless an explicit
		// alignment already said otherwise.
		f.zero = true
		if f.align == 0 {
			f.fill, f.align = '0', '='
		}
		i++
	}
	start := i
	for i < len(r) && r[i] >= '0' && r[i] <= '9' {
		i++
	}
	if i > start {
		n, err := strconv.Atoi(string(r[start:i]))
		if err != nil {
			return f, invalidSpec(spec, v)
		}
		f.width, f.hasWidth = n, true
	}
	if i < len(r) && (r[i] == ',' || r[i] == '_') {
		f.grouping = byte(r[i])
		i++
	}
	if i < len(r) && r[i] == '.' {
		i++
		start = i
		for i < len(r) && r[i] >= '0' && r[i] <= '9' {
			i++
		}
		if i == start {
			return f, invalidSpec(spec, v)
		}
		n, err := strconv.Atoi(string(r[start:i]))
		if err != nil {
			return f, invalidSpec(spec, v)
		}
		f.prec, f.hasPrec = n, true
	}
	switch len(r) - i {
	case 0:
	case 1:
		f.typ = byte(r[i])
	default:
		// More than one character left is never a type, whatever it is.
		return f, invalidSpec(spec, v)
	}
	return f, nil
}

func isAlign(r rune) bool {
	return r == '<' || r == '>' || r == '^' || r == '='
}

func invalidSpec(spec string, v Value) error {
	return errs.New(errs.ValueError, "Invalid format specifier '%s' for object of type '%s'",
		spec, v.TypeName())
}

func unknownCode(typ byte, v Value) error {
	return errs.New(errs.ValueError, "Unknown format code '%c' for object of type '%s'",
		typ, v.TypeName())
}

// body renders the value without the padding, and reports whether it is a
// number -- which is what decides the default alignment and whether '=' and a
// zero fill are allowed.
func (f formatSpec) body(v Value, spec string) (string, bool, error) {
	switch v.kind {
	case KindString:
		return f.formatString(v)
	case KindBool:
		// bool has no __format__ of its own, so any spec at all makes it
		// the integer it is -- `{:>8}` on True is "       1". Only 's',
		// which str would have taken, is refused.
		if f.typ == 's' {
			return "", false, unknownCode('s', v)
		}
		n := int64(0)
		if v.num != 0 {
			n = 1
		}
		s, err := f.formatInt(big.NewInt(n), v)
		return s, true, err
	case KindInt:
		b, _ := v.BigInt()
		s, err := f.formatInt(b, v)
		return s, true, err
	case KindFloat:
		s, err := f.formatFloat(v.AsFloat(), v)
		return s, true, err
	default:
		// Everything else inherits object.__format__, which accepts only
		// the empty spec -- and FormatValue returned before reaching here
		// in that case.
		return "", false, errs.New(errs.TypeError,
			"unsupported format string passed to %s.__format__", v.TypeName())
	}
}

func (f formatSpec) formatString(v Value) (string, bool, error) {
	if f.typ != 0 && f.typ != 's' {
		return "", false, unknownCode(f.typ, v)
	}
	if f.align == '=' {
		return "", false, errs.New(errs.ValueError,
			"'=' alignment not allowed in string format specifier")
	}
	if f.sign != 0 {
		return "", false, errs.New(errs.ValueError,
			"Sign not allowed in string format specifier")
	}
	if f.grouping != 0 {
		return "", false, unknownCode(f.grouping, v)
	}
	s := v.str
	if f.hasPrec {
		// Precision truncates a string, counting characters.
		if r := []rune(s); len(r) > f.prec {
			s = string(r[:f.prec])
		}
	}
	return s, false, nil
}

// formatInt renders an integer in the requested base, or hands it to the float
// path when the type is one of the float codes.
func (f formatSpec) formatInt(b *big.Int, v Value) (string, error) {
	switch f.typ {
	case 'e', 'E', 'f', 'F', 'g', 'G', '%':
		x, _ := new(big.Float).SetInt(b).Float64()
		return f.formatFloat(x, v)
	case 'c':
		if f.sign != 0 {
			return "", errs.New(errs.ValueError,
				"Sign not allowed with integer format specifier 'c'")
		}
		n := b.Int64()
		if !b.IsInt64() || n < 0 || n > 0x10FFFF {
			return "", errs.New(errs.OverflowError, "%%c arg not in range(0x110000)")
		}
		return string(rune(n)), nil
	}
	base, prefix := 10, ""
	switch f.typ {
	case 0, 'd', 'n':
	case 'b':
		base, prefix = 2, "0b"
	case 'o':
		base, prefix = 8, "0o"
	case 'x':
		base, prefix = 16, "0x"
	case 'X':
		base, prefix = 16, "0X"
	default:
		return "", unknownCode(f.typ, v)
	}
	if f.hasPrec {
		return "", errs.New(errs.ValueError,
			"Precision not allowed in integer format specifier")
	}
	digits := new(big.Int).Abs(b).Text(base)
	if f.typ == 'X' {
		digits = strings.ToUpper(digits)
	}
	if !f.alt {
		prefix = ""
	}
	if f.grouping != 0 {
		digits = group(digits, f.grouping, groupSize(base))
	}
	return f.withSign(b.Sign() < 0, prefix, digits), nil
}

// groupSize is how many digits a separator goes between: three in base ten,
// four in the power-of-two bases, which only '_' is allowed to separate.
func groupSize(base int) int {
	if base == 10 {
		return 3
	}
	return 4
}

func group(digits string, sep byte, size int) string {
	if len(digits) <= size {
		return digits
	}
	var b strings.Builder
	lead := len(digits) % size
	if lead == 0 {
		lead = size
	}
	b.WriteString(digits[:lead])
	for i := lead; i < len(digits); i += size {
		b.WriteByte(sep)
		b.WriteString(digits[i : i+size])
	}
	return b.String()
}

func (f formatSpec) formatFloat(x float64, v Value) (string, error) {
	prec := f.prec
	if !f.hasPrec {
		prec = 6
	}
	var body string
	switch f.typ {
	case 'f', 'F':
		body = strconv.FormatFloat(math.Abs(x), 'f', prec, 64)
	case 'e', 'E':
		body = expForm(math.Abs(x), prec, f.typ == 'E')
	case 'g', 'G', 'n':
		body = generalForm(math.Abs(x), prec, f.typ == 'G', f.alt)
	case '%':
		body = strconv.FormatFloat(math.Abs(x)*100, 'f', prec, 64) + "%"
	case 0:
		// No type at all is str(float) laid out, not %g: it keeps the
		// shortest round-tripping digits rather than six of them.
		body = FormatFloat(math.Abs(x))
	default:
		return "", unknownCode(f.typ, v)
	}
	if math.IsInf(x, 0) || math.IsNaN(x) {
		body = strings.TrimPrefix(FormatFloat(math.Abs(x)), "-")
		if f.typ == 'E' || f.typ == 'G' || f.typ == 'F' {
			body = strings.ToUpper(body)
		}
	} else if f.grouping != 0 {
		intPart, rest, found := strings.Cut(body, ".")
		intPart = group(intPart, f.grouping, 3)
		if found {
			body = intPart + "." + rest
		} else {
			body = intPart
		}
	}
	return f.withSign(math.Signbit(x), "", body), nil
}

// expForm is Python's 'e': a two-digit exponent at minimum, where Go writes as
// few digits as it can.
func expForm(x float64, prec int, upper bool) string {
	s := strconv.FormatFloat(x, 'e', prec, 64)
	mant, exp, _ := strings.Cut(s, "e")
	signCh := exp[0]
	digits := strings.TrimLeft(exp[1:], "0")
	if digits == "" {
		digits = "0"
	}
	if len(digits) < 2 {
		digits = "0" + digits
	}
	out := mant + "e" + string(signCh) + digits
	if upper {
		return strings.ToUpper(out)
	}
	return out
}

// generalForm is Python's 'g': exponential when the exponent is below -4 or at
// least the precision, fixed otherwise, with trailing zeros removed unless '#'
// asked for them.
func generalForm(x float64, prec int, upper, alt bool) string {
	if prec == 0 {
		prec = 1
	}
	exp := 0
	if x != 0 {
		exp = int(math.Floor(math.Log10(x)))
		// Re-read the exponent from the rounded digits: log10 can sit a
		// place out for a value that rounds up to the next power.
		if e, err := strconv.Atoi(strings.SplitN(strconv.FormatFloat(x, 'e', prec-1, 64), "e", 2)[1]); err == nil {
			exp = e
		}
	}
	var out string
	if exp < -4 || exp >= prec {
		out = expForm(x, prec-1, false)
		if !alt {
			mant, e, _ := strings.Cut(out, "e")
			out = trimZeros(mant) + "e" + e
		}
	} else {
		out = strconv.FormatFloat(x, 'f', prec-1-exp, 64)
		if !alt {
			out = trimZeros(out)
		}
	}
	if upper {
		return strings.ToUpper(out)
	}
	return out
}

func trimZeros(s string) string {
	if !strings.Contains(s, ".") {
		return s
	}
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}

// withSign puts the sign and any alternate-form prefix in front of the digits.
func (f formatSpec) withSign(negative bool, prefix, digits string) string {
	switch {
	case negative:
		return "-" + prefix + digits
	case f.sign == '+':
		return "+" + prefix + digits
	case f.sign == ' ':
		return " " + prefix + digits
	}
	return prefix + digits
}

// pad lays the body out to the width. '=' puts the fill between the sign and
// the digits, which is what makes a zero fill read as a number rather than as
// text shoved to the right.
func (f formatSpec) pad(body string, numeric bool, budget Budget) (string, error) {
	if !f.hasWidth {
		return body, nil
	}
	n := StrLen(body)
	if n >= f.width {
		return body, nil
	}
	fill := f.width - n
	if err := chargeBytes(budget, int64(fill)*int64(len(string(f.fill)))); err != nil {
		return "", err
	}
	pad := strings.Repeat(string(f.fill), fill)

	align := f.align
	if align == 0 {
		// Numbers right-align by default; everything else left-aligns.
		if numeric {
			align = '>'
		} else {
			align = '<'
		}
	}
	switch align {
	case '<':
		return body + pad, nil
	case '^':
		left := fill / 2
		return strings.Repeat(string(f.fill), left) +
			body + strings.Repeat(string(f.fill), fill-left), nil
	case '=':
		// The sign, and an alternate-form prefix, stay in front.
		i := 0
		if i < len(body) && (body[i] == '-' || body[i] == '+' || body[i] == ' ') {
			i++
		}
		if len(body) >= i+2 && body[i] == '0' &&
			strings.IndexByte("bBoOxX", body[i+1]) >= 0 {
			i += 2
		}
		return body[:i] + pad + body[i:], nil
	default:
		return pad + body, nil
	}
}
