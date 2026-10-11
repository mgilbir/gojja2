// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package value

import (
	"math/big"
	"strings"
	"sync"
	"unicode"

	"github.com/mgilbir/gojja2/errs"
)

// MaxStrDigits is CPython's limit on converting between an int and decimal
// text: sys.int_info.default_max_str_digits, 4300 since 3.11 (CVE-2020-10735).
//
// It is CPython's behaviour, not one of gojja2's bounds, and it is reproduced
// because a template can see it: `{{ n }}` of a 4301-digit integer is a
// ValueError there, and so is `{{ ('1' * 4301)|int }}` -- silently, since |int
// catches the ValueError and answers its default. gojja2's own bound on
// integers is WithMaxIntBits, which is about how wide a number may be computed
// and is a separate question from how wide one may be printed: 2**20 bits is
// some 315,000 digits, far past this.
//
// Only base 10 is limited, in both directions. CPython exempts the bases that
// are powers of two -- `'%x' % n`, `'{:b}'.format(n)`, `int(s, 16)` -- because
// converting those is linear; the quadratic algorithm the limit exists to
// stop is the decimal one. Every interpreter from 3.11 to 3.15 has the same
// limit and the same two messages.
const MaxStrDigits = 4300

// maxStrDigitsBits is the bit length of 10**4300. An integer narrower than it
// is below 10**4300 and so has at most 4300 digits; one wider is at least
// 2**14285, which is past it. Only an integer exactly this wide needs the
// comparison.
const maxStrDigitsBits = 14285

// tenToMaxStrDigits is 10**4300, the smallest magnitude with 4301 digits. It is
// built once and only ever read, so renders can share it.
var tenToMaxStrDigits = sync.OnceValue(func() *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(MaxStrDigits), nil)
})

// exceedsStrDigits reports whether b's magnitude has more than MaxStrDigits
// decimal digits.
//
// The answer is exact, as CPython's is: it counts the digits of the number it
// has converted, and the sign is not one of them -- -(10**4300 - 1) prints,
// 10**4300 does not. The bit length decides every case but one width, so the
// comparison is almost never made.
func exceedsStrDigits(b *big.Int) bool {
	switch n := b.BitLen(); {
	case n < maxStrDigitsBits:
		return false
	case n > maxStrDigitsBits:
		return true
	}
	return b.CmpAbs(tenToMaxStrDigits()) >= 0
}

// IntStrRefusal is the error CPython raises converting v to decimal text, and
// nil for every value it would convert -- including every value that is not an
// int, and every int narrower than 4301 digits.
//
// A bool is an int there and never refuses: it prints as True or False.
func IntStrRefusal(v Value) error {
	if v.kind != KindInt {
		return nil
	}
	b, ok := v.obj.(*big.Int)
	if !ok || !exceedsStrDigits(b) {
		return nil
	}
	return intStrError()
}

// BigIntStrRefusal is [IntStrRefusal] for a number already in hand.
func BigIntStrRefusal(b *big.Int) error {
	if !exceedsStrDigits(b) {
		return nil
	}
	return intStrError()
}

func intStrError() error {
	return errs.New(errs.ValueError,
		"Exceeds the limit (%d digits) for integer string conversion; "+
			"use sys.set_int_max_str_digits() to increase the limit", MaxStrDigits)
}

// IntParseRefusal is the error CPython raises reading decimal text with digits
// digits, and nil when there are few enough.
//
// The count is of digits alone: the sign, surrounding whitespace and the
// underscores between digits are not counted, and leading zeros are. The base
// is the caller's question -- only a base that is not a power of two is
// limited, and a base-0 literal with a 0x, 0o or 0b prefix is one that is.
func IntParseRefusal(digits int) error {
	if digits <= MaxStrDigits {
		return nil
	}
	return errs.New(errs.ValueError,
		"Exceeds the limit (%d digits) for integer string conversion: "+
			"value has %d digits; use sys.set_int_max_str_digits() to increase the limit",
		MaxStrDigits, digits)
}

// IntParseBaseLimited reports whether int(s, base) is subject to the digit
// limit: every base but the five that are powers of two.
func IntParseBaseLimited(base int) bool {
	switch base {
	case 2, 4, 8, 16, 32:
		return false
	}
	return true
}

// CountDigits counts the digits in a number's normalised ASCII text, which is
// what CPython's limit counts: the sign, the whitespace and the underscores are
// skipped, and a letter is a digit in a base past ten.
func CountDigits(s string) int {
	n := 0
	for i := range len(s) {
		c := s[i] | 0x20
		if (s[i] >= '0' && s[i] <= '9') || (c >= 'a' && c <= 'z') {
			n++
		}
	}
	return n
}

// IntStrRefusalIn is [IntStrRefusal] for everything v's repr would print: the
// first int too wide for decimal text in it, in the order repr reaches it.
//
// It costs a walk and no text, which is what a caller wants that only needs to
// know whether repr() would raise -- the compiler asks it of every constant it
// keeps, nearly all of which are short strings.
func IntStrRefusalIn(v Value) error {
	stack := []Value{v}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		switch cur.kind {
		case KindInt:
			if err := IntStrRefusal(cur); err != nil {
				return err
			}
		case KindList, KindTuple:
			s, _ := cur.Seq()
			// Pushed in reverse so they come off in order.
			for i := len(s.items) - 1; i >= 0; i-- {
				stack = append(stack, s.items[i])
			}
		case KindDict:
			d, _ := cur.Dict()
			ents := d.Entries()
			for i := len(ents) - 1; i >= 0; i-- {
				stack = append(stack, ents[i].Value, ents[i].Key)
			}
		case KindObject:
			if r, ok := cur.obj.(ReprerErr); ok {
				if _, err := r.ReprErr(); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// intTextLimit is the digit limit as int(text, base) applies it, for text that
// pyNumericText has already normalised (ok reports whether it could). raw is
// the string as the template gave it, which 3.11 scanned for its count when
// the whole of it was not a number. A nil answer means the limit has nothing
// to say, and the caller converts or refuses the text as it always has.
func intTextLimit(raw, text string, ok bool, base int, py PythonVersion) error {
	if ok {
		eff, body := intTextBody(text, base)
		if !IntParseBaseLimited(eff) {
			return nil
		}
		if validIntBody(body, eff) {
			return IntParseRefusal(CountDigits(body))
		}
	}
	if py.IntDigitLimitAfterValidation() {
		return nil
	}
	return IntParseRefusal(leadingDigitRun(raw, base, py))
}

// intTextBody strips the sign and, where it chooses or matches the base, the
// prefix -- answering the base the digits are read in. Base 0 is decimal
// unless a 0x, 0o or 0b says otherwise.
func intTextBody(text string, base int) (int, string) {
	if text != "" && (text[0] == '+' || text[0] == '-') {
		text = text[1:]
	}
	if len(text) >= 2 && text[0] == '0' {
		prefixed := 0
		switch text[1] | 0x20 {
		case 'x':
			prefixed = 16
		case 'o':
			prefixed = 8
		case 'b':
			prefixed = 2
		}
		if prefixed != 0 && (base == 0 || base == prefixed) {
			return prefixed, text[2:]
		}
	}
	if base == 0 {
		return 10, text
	}
	return base, text
}

// digitValue is a character's value as a digit in any base up to 36, or 36
// for a character that is not one.
func digitValue(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c|0x20 >= 'a' && c|0x20 <= 'z':
		return int(c|0x20-'a') + 10
	}
	return 36
}

// validIntBody reports whether every character of body is a digit in base,
// and there is at least one.
func validIntBody(body string, base int) bool {
	if body == "" {
		return false
	}
	for i := range len(body) {
		if digitValue(body[i]) >= base {
			return false
		}
	}
	return true
}

// leadingDigitRun counts the digits 3.11's int() had read when it compared
// against the digit limit: the run after any whitespace and sign, with single
// underscores between digits allowed and not counted. A misplaced underscore is
// refused as an invalid literal before the count is compared, so it answers 0,
// and so does a prefix that puts the digits in an exempt base.
func leadingDigitRun(raw string, base int, py PythonVersion) int {
	s := strings.TrimLeftFunc(DecimalASCII(raw, py), unicode.IsSpace)
	if s != "" && (s[0] == '+' || s[0] == '-') {
		s = s[1:]
	}
	base, s = intTextBody(s, base)
	if !IntParseBaseLimited(base) {
		return 0
	}
	n := 0
	for i := 0; i < len(s); i++ {
		switch {
		case digitValue(s[i]) < base:
			n++
		case s[i] == '_':
			if n == 0 || i+1 >= len(s) || digitValue(s[i+1]) >= base {
				return 0
			}
		default:
			return n
		}
	}
	return n
}
