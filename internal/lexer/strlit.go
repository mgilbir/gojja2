// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package lexer

import (
	"strconv"
	"strings"

	"github.com/mgilbir/gojja2/errs"
)

// decodeStringLiteral resolves the escape sequences in a quoted literal's body.
//
// jinja2 does this by round-tripping the text through
// `.encode("ascii", "backslashreplace").decode("unicode-escape")`, and that
// detour is observable: a backslash in front of a non-ASCII character escapes
// the *escape* the encoder produced, so "\é" decodes to the four characters
// \xe9 rather than to é. Reproducing the two steps reproduces the quirk.
func decodeStringLiteral(body, newlineSequence string) (string, error) {
	if newlineSequence != "\n" {
		body = strings.ReplaceAll(body, "\n", newlineSequence)
	}
	return unicodeEscapeDecode(backslashReplaceNonASCII(body))
}

// backslashReplaceNonASCII is Python's "backslashreplace" error handler for an
// ASCII encode: every non-ASCII code point becomes its own escape sequence.
func backslashReplaceNonASCII(s string) string {
	if isASCII(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r < 0x80:
			b.WriteRune(r)
		case r < 0x100:
			b.WriteString(`\x`)
			writeHexDigits(&b, uint32(r), 2)
		case r < 0x10000:
			b.WriteString(`\u`)
			writeHexDigits(&b, uint32(r), 4)
		default:
			b.WriteString(`\U`)
			writeHexDigits(&b, uint32(r), 8)
		}
	}
	return b.String()
}

func isASCII(s string) bool {
	for i := range len(s) {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

func writeHexDigits(b *strings.Builder, v uint32, width int) {
	s := strconv.FormatUint(uint64(v), 16)
	for i := len(s); i < width; i++ {
		b.WriteByte('0')
	}
	b.WriteString(s)
}

// simpleEscapes are the one-character escapes Python's unicode-escape codec
// recognises. Anything not listed keeps its backslash, so "\d" stays "\d".
var simpleEscapes = map[byte]rune{
	'\\': '\\',
	'\'': '\'',
	'"':  '"',
	'a':  '\a',
	'b':  '\b',
	'f':  '\f',
	'n':  '\n',
	'r':  '\r',
	't':  '\t',
	'v':  '\v',
}

func unicodeEscapeDecode(s string) (string, error) {
	if !strings.ContainsRune(s, '\\') {
		return s, nil
	}
	var b strings.Builder
	b.Grow(len(s))

	for i := 0; i < len(s); {
		if s[i] != '\\' {
			b.WriteByte(s[i])
			i++
			continue
		}
		i++
		if i >= len(s) {
			// A trailing backslash survives as itself.
			b.WriteByte('\\')
			break
		}
		c := s[i]
		if r, ok := simpleEscapes[c]; ok {
			b.WriteRune(r)
			i++
			continue
		}
		switch {
		case c == '\n':
			// Line continuation: both the backslash and the newline
			// disappear.
			i++

		case c >= '0' && c <= '7':
			// Up to three octal digits, valued as a code point.
			j, v := i, 0
			for j < len(s) && j < i+3 && s[j] >= '0' && s[j] <= '7' {
				v = v*8 + int(s[j]-'0')
				j++
			}
			b.WriteRune(rune(v))
			i = j

		case c == 'x':
			r, next, err := readHexEscape(s, i+1, 2, `\xXX`)
			if err != nil {
				return "", err
			}
			b.WriteRune(r)
			i = next

		case c == 'u':
			r, next, err := readHexEscape(s, i+1, 4, `\uXXXX`)
			if err != nil {
				return "", err
			}
			b.WriteRune(r)
			i = next

		case c == 'U':
			r, next, err := readHexEscape(s, i+1, 8, `\UXXXXXXXX`)
			if err != nil {
				return "", err
			}
			if r > 0x10FFFF || (r >= 0xD800 && r <= 0xDFFF) {
				return "", errs.New(errs.TemplateSyntaxError, "illegal Unicode character")
			}
			b.WriteRune(r)
			i = next

		case c == 'N':
			// Named code points need the full Unicode name database,
			// which gojja2 does not carry. Malformed spellings still
			// get CPython's message; a well-formed name is reported
			// as unknown. See docs/divergences.md.
			if i+1 >= len(s) || s[i+1] != '{' {
				return "", errs.New(errs.TemplateSyntaxError, `malformed \N character escape`)
			}
			end := strings.IndexByte(s[i+1:], '}')
			if end < 0 {
				return "", errs.New(errs.TemplateSyntaxError, `malformed \N character escape`)
			}
			return "", errs.New(errs.TemplateSyntaxError, "unknown Unicode character name")

		default:
			// Not an escape at all: the backslash is literal.
			b.WriteByte('\\')
			b.WriteByte(c)
			i++
		}
	}
	return b.String(), nil
}

// readHexEscape reads exactly width hex digits, reporting CPython's truncation
// message when they are not all there.
func readHexEscape(s string, i, width int, form string) (rune, int, error) {
	if i+width > len(s) {
		return 0, 0, truncated(form)
	}
	v := 0
	for j := i; j < i+width; j++ {
		d := hexValue(s[j])
		if d < 0 {
			return 0, 0, truncated(form)
		}
		v = v*16 + d
	}
	return rune(v), i + width, nil
}

func truncated(form string) error {
	return errs.New(errs.TemplateSyntaxError, "truncated %s escape", form)
}

func hexValue(b byte) int {
	switch {
	case b >= '0' && b <= '9':
		return int(b - '0')
	case b >= 'a' && b <= 'f':
		return int(b-'a') + 10
	case b >= 'A' && b <= 'F':
		return int(b-'A') + 10
	}
	return -1
}
