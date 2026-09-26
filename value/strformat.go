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
	// A type with no __format__ of its own inherits object's, which takes
	// the empty spec and nothing else -- and never looks at what the spec
	// says. So `{:,n}` on None is about None, not about the comma.
	switch v.kind {
	case KindString, KindInt, KindBool, KindFloat:
	default:
		return "", errs.New(errs.TypeError,
			"unsupported format string passed to %s.__format__", v.TypeName())
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
	// zcoerce is 'z', which renders what rounds to zero without its sign.
	// Python 3.11 added it (PEP 682), which is every version modelled here,
	// so it needs no version gate.
	zcoerce bool
}

func parseFormatSpec(spec string, v Value) (formatSpec, error) {
	f := formatSpec{fill: ' '}
	r := []rune(spec)
	i := 0

	// A fill character is only a fill when an alignment follows it, so ">8"
	// is align-8 and "*>8" is fill-align-8.
	fillGiven := false
	if len(r) >= 2 && isAlign(r[1]) {
		f.fill, f.align = r[0], byte(r[1])
		fillGiven, i = true, 2
	} else if len(r) >= 1 && isAlign(r[0]) {
		f.align = byte(r[0])
		i = 1
	}
	if i < len(r) && (r[i] == '+' || r[i] == '-' || r[i] == ' ') {
		f.sign = byte(r[i])
		i++
	}
	// 'z' sits between the sign and '#', and nowhere else: `{:z#}` is a
	// spec and `{:#z}` is a '#' followed by a type called z.
	if i < len(r) && r[i] == 'z' {
		f.zcoerce = true
		i++
		// Which values may ask for it is decided here rather than at the
		// render, because CPython decides it here too: `{:zx}` on an int
		// is about the z and not about the x.
		switch {
		case v.Kind() == KindInt || v.Kind() == KindBool:
			return f, errs.New(errs.ValueError,
				"Negative zero coercion (z) not allowed in integer format specifier")
		case v.Kind() == KindString:
			return f, errs.New(errs.ValueError,
				"Negative zero coercion (z) not allowed in string format specifier")
		}
	}
	if i < len(r) && r[i] == '#' {
		f.alt = true
		i++
	}
	if i < len(r) && r[i] == '0' {
		// A leading zero is fill '0', and align '=' as well -- but only
		// for a value that aligns right by default, which is to say a
		// number. On a string it is a fill and nothing more, which is
		// why `{:0.0s}` is an empty string and not an alignment error.
		f.zero = true
		// The fill is set whenever the spec did not name one, whatever
		// the alignment says -- `{:>06}` pads with zeros. The alignment
		// only follows when none was given *and* the value aligns right
		// by default, which is to say a number: on a string the zero is
		// a fill and nothing more, which is why `{:0.0s}` is an empty
		// string and not an alignment error.
		if !fillGiven {
			f.fill = '0'
		}
		if f.align == 0 && v.IsNumber() {
			f.align = '='
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

	// Whether a grouping option is allowed at all depends on the format
	// code and nothing else, so CPython settles it here -- before the value
	// is looked at. That is why `{:,x}` on a float is about the comma and
	// `{:x}` on the same float is about the code.
	//
	// Underscore is allowed in the power-of-two bases where a comma is not,
	// and separates every four digits there rather than every three.
	if f.grouping != 0 {
		switch f.typ {
		case 0, 'd', 'e', 'E', 'f', 'F', 'g', 'G', '%':
		case 'b', 'o', 'x', 'X':
			if f.grouping != '_' {
				return f, cannotGroup(f.grouping, f.typ)
			}
		default:
			return f, cannotGroup(f.grouping, f.typ)
		}
	}
	return f, nil
}

// cannotGroup is the complaint a grouping option makes about the code it was
// written with. The code is the one in the spec, or the type's own default when
// the spec left it out -- which is how `{:,}` on a string says "with 's'".
func cannotGroup(sep, typ byte) error {
	return errs.New(errs.ValueError, "Cannot specify '%c' with '%c'.", sep, typ)
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
	// The order is CPython's, and every step of it is reachable: a spec
	// carrying two of these reports the first, not the last.
	if f.grouping != 0 {
		return "", false, cannotGroup(f.grouping, 's')
	}
	if f.sign == ' ' {
		return "", false, errs.New(errs.ValueError,
			"Space not allowed in string format specifier")
	}
	if f.sign != 0 {
		return "", false, errs.New(errs.ValueError,
			"Sign not allowed in string format specifier")
	}
	if f.alt {
		return "", false, errs.New(errs.ValueError,
			"Alternate form (#) not allowed in string format specifier")
	}
	if f.align == '=' {
		return "", false, errs.New(errs.ValueError,
			"'=' alignment not allowed in string format specifier")
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
		// A float code converts the integer first, and an integer too
		// wide for a float64 is an OverflowError rather than an
		// infinity -- `{{ '{:f}'.format(10 ** 400) }}` printed "inf".
		x, err := floatOperand(v)
		if err != nil {
			return "", err
		}
		return f.formatFloat(x, v)
	}
	base, prefix := 10, ""
	switch f.typ {
	case 0, 'd', 'n', 'c':
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
	// Precision is refused for every integer code, 'c' included, and before
	// anything 'c' has to say for itself.
	if f.hasPrec {
		return "", errs.New(errs.ValueError,
			"Precision not allowed in integer format specifier")
	}
	if f.typ == 'c' {
		if f.sign != 0 {
			return "", errs.New(errs.ValueError,
				"Sign not allowed with integer format specifier 'c'")
		}
		if f.alt {
			return "", errs.New(errs.ValueError,
				"Alternate form (#) not allowed with integer format specifier 'c'")
		}
		// Past a C long the conversion itself fails, before anything
		// asks whether the number is a code point.
		if !b.IsInt64() {
			return "", errs.New(errs.OverflowError,
				"Python int too large to convert to C long")
		}
		n := b.Int64()
		if n < 0 || n > 0x10FFFF {
			return "", errs.New(errs.OverflowError, "%%c arg not in range(0x110000)")
		}
		return string(rune(n)), nil
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

// groupWidth is how many digits this spec's separator goes between: four in the
// power-of-two bases, which only '_' reaches, and three everywhere else.
func (f formatSpec) groupWidth() int {
	if f.grouping == '_' {
		switch f.typ {
		case 'b', 'o', 'x', 'X':
			return 4
		}
	}
	return 3
}

// padGrouped left-pads a number's digits with zeros and regroups the result, so
// that the padding is separated like the rest of the number and the whole body
// still reaches the width.
//
// The digit run ends at the decimal point or the exponent; everything after it
// is carried along and counted, but not padded.
func padGrouped(rest string, need int, sep byte, size int) string {
	end := 0
	for end < len(rest) {
		c := rest[end]
		if c == '.' {
			break
		}
		if (c == 'e' || c == 'E') && end+1 < len(rest) &&
			(rest[end+1] == '+' || rest[end+1] == '-') {
			break
		}
		if c == sep || (c >= '0' && c <= '9') ||
			(c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F') {
			end++
			continue
		}
		break
	}
	tail := rest[end:]
	digits := strings.ReplaceAll(rest[:end], string(sep), "")
	want := need - StrLen(tail)
	n := len(digits)
	for n+(n-1)/size < want {
		n++
	}
	if n > len(digits) {
		digits = strings.Repeat("0", n-len(digits)) + digits
	}
	return group(digits, sep, size) + tail
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
	percent := false
	switch f.typ {
	case 'f', 'F':
		body = strconv.FormatFloat(math.Abs(x), 'f', prec, 64)
	case 'e', 'E':
		body = expForm(math.Abs(x), prec, f.typ == 'E')
	case 'g', 'G', 'n':
		body = generalForm(math.Abs(x), prec, f.typ == 'G', f.alt, false)
	case '%':
		// The sign goes on after the grouping: appending it here let the
		// separator fall between the last digits and the '%' itself.
		body = strconv.FormatFloat(math.Abs(x)*100, 'f', prec, 64)
		percent = true
	case 0:
		// No type at all is str(float) laid out, not %g: it keeps the
		// shortest round-tripping digits rather than six of them.
		//
		// An explicit precision changes that -- it becomes 'g' -- but
		// not quite into 'g': the flag this type carries moves the
		// threshold between fixed and exponential one place down and
		// keeps a ".0" on a result that would otherwise be all digits.
		// So `{:.0}` on 1.5 is "2e+00" where `{:.0g}` is "2".
		if f.hasPrec {
			body = generalForm(math.Abs(x), prec, false, f.alt, true)
		} else {
			body = FormatFloat(math.Abs(x))
		}
	default:
		return "", unknownCode(f.typ, v)
	}
	if math.IsInf(x, 0) || math.IsNaN(x) {
		// An infinity has no digits to lay out, so neither the
		// alternate form nor the grouping has anything to do -- but the
		// percent sign still goes on.
		body = strings.TrimPrefix(FormatFloat(math.Abs(x)), "-")
		if f.typ == 'E' || f.typ == 'G' || f.typ == 'F' {
			body = strings.ToUpper(body)
		}
	} else {
		if f.alt {
			body = withAltPoint(body)
		}
		if f.grouping != 0 {
			body = groupMantissa(body, f.grouping)
		}
	}
	if percent {
		body += "%"
	}
	// 'z' drops the sign from what *rounds* to zero rather than from -0.0
	// alone, so `{:z.1f}` of -0.04 is "0.0" while `{:z.2%}` of -0.001 keeps
	// its sign at "-0.10%". An infinity has no digits and is never coerced.
	negative := math.Signbit(x)
	if f.zcoerce && negative && !math.IsInf(x, 0) && !math.IsNaN(x) && !hasNonZeroDigit(body) {
		negative = false
	}
	return f.withSign(negative, "", body), nil
}

// hasNonZeroDigit reports whether a formatted body still has a digit that is
// not zero, which is what says a rounded result is not zero after all.
func hasNonZeroDigit(body string) bool {
	for i := range len(body) {
		if body[i] >= '1' && body[i] <= '9' {
			return true
		}
	}
	return false
}

// splitExponent separates the mantissa from the exponent, which is where both
// the alternate form and the grouping stop: `{:,.0E}` on 1 is "1E+00", not
// "1E,+00".
func splitExponent(body string) (head, exp string) {
	if i := strings.IndexAny(body, "eE"); i >= 0 {
		return body[:i], body[i:]
	}
	return body, ""
}

// withAltPoint is Python's alternate form for a float: the result always
// carries a decimal point, so `{:#.0f}` on 1.5 is "2." and not "2".
func withAltPoint(body string) string {
	head, exp := splitExponent(body)
	if !strings.Contains(head, ".") {
		head += "."
	}
	return head + exp
}

// groupMantissa separates the digits before the point, and only those.
func groupMantissa(body string, sep byte) string {
	head, exp := splitExponent(body)
	intPart, rest, found := strings.Cut(head, ".")
	intPart = group(intPart, sep, 3)
	if found {
		return intPart + "." + rest + exp
	}
	return intPart + exp
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
//
// dotZero is the flag the *typeless* spec carries. It moves the threshold one
// place down and keeps a ".0" on an otherwise all-digit result, which is what
// makes `{:.0}` and `{:.0g}` different answers for the same number.
func generalForm(x float64, prec int, upper, alt, dotZero bool) string {
	if prec == 0 {
		prec = 1
	}
	threshold := prec
	if dotZero {
		threshold = prec - 1
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
	if exp < -4 || exp >= threshold {
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
	if dotZero && !strings.ContainsAny(out, ".eE") {
		out += ".0"
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
		if f.grouping != 0 && f.fill == '0' {
			// Zeros written into a grouped number join it rather
			// than sitting in front of it, so they take separators
			// of their own: `{:06,}` on 1 is "00,001".
			return body[:i] + padGrouped(body[i:], f.width-StrLen(body[:i]),
				f.grouping, f.groupWidth()), nil
		}
		return body[:i] + pad + body[i:], nil
	default:
		return pad + body, nil
	}
}
