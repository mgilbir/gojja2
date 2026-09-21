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
			return v.DebugText()
		}
		return ""
	case KindObject:
		if s, ok := v.obj.(Strer); ok {
			return s.Str()
		}
	}
	return Repr(v)
}

// HTML is the escaped form a value carries for itself -- Python's __html__.
// ok is false when it has none, which is every value but an Object that
// implements HTMLer. A Markup string answers through IsSafe instead, because
// markupsafe returns it unchanged rather than asking it for anything.
func HTML(v Value) (string, bool) {
	if v.kind == KindObject {
		if h, ok := v.obj.(HTMLer); ok {
			return h.HTML(), true
		}
	}
	// ChainableUndefined is the one Undefined class that defines __html__,
	// returning str(self). That is visible: `nope is escaped` asks only
	// whether the attribute is there, and answers True under that class
	// alone.
	if v.kind == KindUndefined && v.undef().behavior == UndefinedChainable {
		return Str(v), true
	}
	return "", false
}

// Repr is Python's repr(): the form a value takes inside a container.
//
// It reproduces the default interpreter. Use [ReprFor] wherever a render's own
// version is in reach -- which is everywhere the result can be seen by a
// template, rather than only by a Go caller.
func Repr(v Value) string { return ReprFor(v, DefaultPythonVersion) }

// ReprFor is [Repr] reproducing one interpreter.
//
// repr escapes by str.isprintable, and CPython carries its own Unicode: 3.11
// and 3.14 disagree about 10,294 code points, every one of them a character
// assigned in between. The overrides are looked up once here rather than once
// per rune.
func ReprFor(v Value, py PythonVersion) string {
	var b strings.Builder
	// No budget: nothing is charged and nothing can refuse, so the error is
	// always nil. See ReprBudget.
	_ = writeRepr(&b, v, nil, false, nil, UnicodeFor(py))
	return b.String()
}

// ReprBudget is [Repr] with the walk charged to b and stopped when b says to
// stop.
//
// A repr is as long as the value it describes, so building one is work set by
// the caller's data rather than by the template that asked for it -- and it
// used to be one uninterruptible pass. `{{ big|safe|pprint }}` over a 17MB
// string spent 90% of its time here with a deadline an eighth of that, and the
// deadline did not arrive until it was over.
//
// The returned string is only meaningful when the error is nil.
func ReprBudget(v Value, bud Budget, py PythonVersion) (string, error) {
	var b strings.Builder
	if err := writeRepr(&b, v, nil, false, bud, UnicodeFor(py)); err != nil {
		return "", err
	}
	return b.String(), nil
}

// reprChargeBlock is how much of a string is written between charges. It is the
// budget's own check interval, so each block is one consultation -- charging
// the whole length at once would consult the context once and then run to the
// end, which is the defect rather than the fix.
const reprChargeBlock = 1 << 12

// active tracks the containers currently being rendered, so a value that
// contains itself prints the way CPython prints one.
//
// CPython marks only a container on the *active* path: repr({'s': m}) where m
// is the outer dict gives "{'s': {...}}", while two references to one
// non-cyclic dict are both expanded in full. Adding on the way down and
// removing on the way back up reproduces exactly that.
type active map[any]bool

func (a active) enter(key any) (active, bool) {
	if a[key] {
		return a, false
	}
	if a == nil {
		a = make(active, 4)
	}
	a[key] = true
	return a, true
}

func (a active) leave(key any) { delete(a, key) }

// writeRepr renders v, walking containers on an explicit stack rather than on
// the goroutine's.
//
// The depth of a value graph is chosen at render time, not at compile time:
// `{% for %}{% set ns.t = [ns.t] %}{% endfor %}` builds one as deep as the loop
// is long, and the loop is bounded by the iteration budget at ten million. A
// recursive walk died on that, with `fatal error: stack overflow` -- which is
// not a panic, so the backstop in catchPanic never saw it and the process went
// down. Repr and Str are called from more than a hundred places and cannot
// return an error, so the walk has to be total: every one of those callers was
// a crash site, including `{{ deep|upper }}`.
//
// CPython raises RecursionError here instead, at around 990 levels. Matching
// that would mean making Str and Repr partial everywhere they are used to
// build a message; see docs/divergences.md.
// ascii selects Python's ascii() rather than its repr(): every non-ASCII code
// point is escaped, wherever it sits. It travels with the walk because ascii()
// applies to the whole structure and not only to a bare string -- ascii(['é'])
// is "['\\xe9']".
func writeRepr(b *strings.Builder, v Value, seen active, ascii bool, bud Budget, u *UnicodeOverrides) error {
	frame, open, err := openRepr(b, v, &seen, ascii, bud, u)
	if err != nil {
		return err
	}
	if !open {
		return nil
	}
	stack := []reprFrame{frame}
	for len(stack) > 0 {
		top := &stack[len(stack)-1]
		child, more := top.advance(b)
		if !more {
			seen.leave(top.key)
			// Clear before shrinking: `stack[:len-1]` leaves the
			// element in the backing array, still referencing this
			// container's children.
			*top = reprFrame{}
			stack = stack[:len(stack)-1]
			continue
		}
		// One element of one container per pass, so the walk yields in
		// the size of the value rather than at the end of it.
		if err := chargeItems(bud, 1); err != nil {
			return err
		}
		frame, open, err := openRepr(b, child, &seen, ascii, bud, u)
		if err != nil {
			return err
		}
		if open {
			stack = append(stack, frame)
		}
	}
	return nil
}

// reprFrame is one container whose children are still being written. It stands
// in for the Go frame the recursive form used, and holds a cursor rather than
// a copy, so an open container costs one small struct per level.
type reprFrame struct {
	items []Value     // list and tuple elements
	ents  []DictEntry // dict entries
	dict  bool        // which of the two above is in use, including when empty
	half  bool        // a dict key has been written and its value is next
	tuple bool        // a one-element tuple needs its trailing comma
	i     int         // index of the next child
	close byte        // the bracket that ends this container
	key   any         // the container to unmark once it closes
}

// advance writes whatever punctuation comes next and returns the child to
// render, or reports false once the container is complete -- having written
// its closing bracket.
func (f *reprFrame) advance(b *strings.Builder) (Value, bool) {
	if f.dict {
		if f.half {
			// The key is written; the separator belongs to the value.
			f.half = false
			b.WriteString(": ")
			e := f.ents[f.i]
			f.i++
			return e.Value, true
		}
		if f.i >= len(f.ents) {
			b.WriteByte(f.close)
			return Value{}, false
		}
		if f.i > 0 {
			b.WriteString(", ")
		}
		f.half = true
		return f.ents[f.i].Key, true
	}
	if f.i >= len(f.items) {
		// A one-element tuple needs the trailing comma to stay a tuple.
		if f.tuple && len(f.items) == 1 {
			b.WriteByte(',')
		}
		b.WriteByte(f.close)
		return Value{}, false
	}
	if f.i > 0 {
		b.WriteString(", ")
	}
	v := f.items[f.i]
	f.i++
	return v, true
}

// openRepr writes v. A scalar is written whole and reports false; a container
// writes its opening bracket and returns the frame its children come from.
//
// seen is threaded by pointer because the marker tracks the *active* path: a
// container is marked on the way down and unmarked when its frame closes, so
// two references to one non-cyclic value are both expanded in full while a
// value that contains itself collapses.
func openRepr(b *strings.Builder, v Value, seen *active, ascii bool, bud Budget, u *UnicodeOverrides) (reprFrame, bool, error) {
	switch v.kind {
	case KindList, KindTuple, KindDict:
		next, ok := seen.enter(v.obj)
		if !ok {
			switch v.kind {
			case KindList:
				b.WriteString("[...]")
			case KindTuple:
				b.WriteString("(...)")
			default:
				b.WriteString("{...}")
			}
			return reprFrame{}, false, nil
		}
		*seen = next
		switch v.kind {
		case KindList:
			s, _ := v.Seq()
			b.WriteByte('[')
			return reprFrame{items: s.items, close: ']', key: v.obj}, true, nil
		case KindTuple:
			s, _ := v.Seq()
			b.WriteByte('(')
			return reprFrame{items: s.items, tuple: true, close: ')', key: v.obj}, true, nil
		default:
			d, _ := v.Dict()
			b.WriteByte('{')
			return reprFrame{ents: d.entries, dict: true, close: '}', key: v.obj}, true, nil
		}
	}
	if err := writeScalarRepr(b, v, ascii, bud, u); err != nil {
		return reprFrame{}, false, err
	}
	return reprFrame{}, false, nil
}

// writeScalarRepr renders everything that holds no children.
func writeScalarRepr(b *strings.Builder, v Value, ascii bool, bud Budget, u *UnicodeOverrides) error {
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
		if v.safe {
			// markupsafe's Markup has a repr of its own, which is
			// what |pprint and a container's repr show.
			b.WriteString("Markup(")
			if err := writeStringRepr(b, v.str, ascii, bud, u); err != nil {
				return err
			}
			b.WriteByte(')')
			return nil
		}
		if err := writeStringRepr(b, v.str, ascii, bud, u); err != nil {
			return err
		}
	case KindBytes:
		b.WriteByte('b')
		writeBytesRepr(b, v.str)
	case KindObject:
		if r, ok := v.obj.(Reprer); ok {
			// ascii() is repr() with what it produced escaped
			// afterwards, so an object that renders itself has no
			// say in it: `{{ "%a" % [g] }}` over a |groupby pair
			// escaped the list around it and left the group's own
			// text alone.
			writeEscapedNonASCII(b, r.Repr(), ascii)
			return nil
		}
		b.WriteString("<object>")
	case KindFunc:
		b.WriteString("<function>")
	}
	return nil
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
func writeStringRepr(b *strings.Builder, s string, asciiOnly bool, bud Budget, u *UnicodeOverrides) error {
	quote := byte('\'')
	if strings.ContainsRune(s, '\'') && !strings.ContainsRune(s, '"') {
		quote = '"'
	}
	b.WriteByte(quote)
	charged := 0
	for i, r := range s {
		if i-charged >= reprChargeBlock {
			if err := chargeBytes(bud, int64(i-charged)); err != nil {
				return err
			}
			charged = i
		}
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
			// Either a real U+FFFD or a byte that is not valid
			// UTF-8, which ranging over a string decodes as one.
			// Python strings cannot hold the second, so there is
			// nothing to match: it is written as the replacement
			// character, which is what it decoded to.
			b.WriteString(`�`)
		case u.PrintableWith(r) && (!asciiOnly || r < utf8.RuneSelf):
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
	if err := chargeBytes(bud, int64(len(s)-charged)); err != nil {
		return err
	}
	b.WriteByte(quote)
	return nil
}

// writeBytesRepr is bytes.__repr__, which escapes one *byte* at a time.
//
// writeStringRepr ranges over runes, which is right for a str and wrong here:
// the bytes of a multi-byte character decode to a single rune and print as one
// escape instead of one per byte, so `{{ "é".encode() }}` rendered b'\xe9'
// where CPython renders b'\xc3\xa9'. The bytes themselves were always correct
// -- |length agreed throughout -- so only the text was wrong, which is the
// kind of wrong that is read rather than caught.
//
// There is also no \u or \U form in a bytes repr at all: every byte that is
// not printable ASCII is \xNN, so a euro sign is three escapes and never
// €. DEL is not printable, which is why the range stops below 0x7f.
func writeBytesRepr(b *strings.Builder, s string) {
	quote := byte('\'')
	if strings.IndexByte(s, '\'') >= 0 && strings.IndexByte(s, '"') < 0 {
		quote = '"'
	}
	b.WriteByte(quote)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == quote || c == '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
		case c == '\n':
			b.WriteString(`\n`)
		case c == '\r':
			b.WriteString(`\r`)
		case c == '\t':
			b.WriteString(`\t`)
		case c >= 0x20 && c < 0x7f:
			b.WriteByte(c)
		default:
			b.WriteString(`\x`)
			writeHex(b, uint32(c), 2)
		}
	}
	b.WriteByte(quote)
}

// writeEscapedNonASCII writes text already in repr form, escaping the code
// points ascii() would. Everything ASCII is passed through untouched --
// backslashes included, because they are already the escapes repr wrote.
func writeEscapedNonASCII(b *strings.Builder, text string, ascii bool) {
	if !ascii {
		b.WriteString(text)
		return
	}
	for _, r := range text {
		switch {
		case r < utf8.RuneSelf:
			b.WriteRune(r)
		case r == utf8.RuneError:
			b.WriteString(`\ufffd`)
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
}

func writeHex(b *strings.Builder, v uint32, width int) {
	s := strconv.FormatUint(uint64(v), 16)
	for i := len(s); i < width; i++ {
		b.WriteByte('0')
	}
	b.WriteString(s)
}

// Ascii is Python's ascii(): repr() with every non-ASCII code point escaped.
//
// The escaping reaches inside a container, because ascii() is about the whole
// rendered form: ascii(['é']) is "['\\xe9']", not "['é']".
func Ascii(v Value) string { return AsciiFor(v, DefaultPythonVersion) }

// AsciiFor is [Ascii] reproducing one interpreter. It escapes everything
// outside ASCII, so the version reaches it only through which characters are
// printable at all.
func AsciiFor(v Value, py PythonVersion) string {
	var b strings.Builder
	_ = writeRepr(&b, v, nil, true, nil, UnicodeFor(py))
	return b.String()
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

// htmlEscaper matches markupsafe's escape(), which uses numeric references for
// the quotes rather than the named entities.
var htmlEscaper = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
	"'", "&#39;",
	`"`, "&#34;",
)

// EscapeHTML is markupsafe.escape for text that is not already Markup.
func EscapeHTML(s string) string { return htmlEscaper.Replace(s) }

// Escape returns v as Markup, escaping it unless it already is.
func Escape(v Value) Value {
	if v.IsSafe() {
		return v
	}
	return Safe(EscapeHTML(Str(v)))
}
