// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/mgilbir/gojja2/errs"
	"github.com/mgilbir/gojja2/value"
)

// str.encode used to ignore both of its arguments and always answer UTF-8, so
// `{{ "é".encode("latin-1") }}` gave the two UTF-8 bytes rather than the one
// latin-1 byte, and `{{ "€".encode("ascii") }}` gave bytes at all rather than
// raising. Silently answering in the wrong encoding is the worst of the three.
//
// gojja2 knows the three codecs a template plausibly asks for. CPython knows a
// hundred, so an encoding this does not implement is refused with CPython's own
// LookupError rather than quietly encoded as something else; see
// docs/divergences.md.

// codecName canonicalises an encoding the way CPython's lookup does, which is
// why "UTF-8", "utf8" and "u8" are one codec. The canonical spelling is the one
// that appears in an error message: "latin1" reports itself as "latin-1".
func codecName(name string) (string, bool) {
	norm := strings.Map(func(r rune) rune {
		switch {
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		case r == '-' || r == ' ':
			return '_'
		}
		return r
	}, name)
	switch norm {
	case "utf_8", "utf8", "u8", "utf", "cp65001":
		return "utf-8", true
	case "ascii", "us_ascii", "646", "ansi_x3.4_1968":
		return "ascii", true
	case "latin_1", "latin1", "latin", "l1", "iso_8859_1", "iso8859_1", "8859", "cp819":
		return "latin-1", true
	}
	return "", false
}

// charEscape is how Python names a character inside a codec error, and what
// backslashreplace substitutes: the shortest of \xNN, \uNNNN and \UNNNNNNNN.
func charEscape(r rune) string {
	switch {
	case r < 0x100:
		return fmt.Sprintf(`\x%02x`, r)
	case r < 0x10000:
		return fmt.Sprintf(`\u%04x`, r)
	default:
		return fmt.Sprintf(`\U%08x`, r)
	}
}

// encodeString is str.encode. pos counts characters, not bytes, which is what
// CPython reports and what makes "aéb" fail at position 1.
func encodeString(s, codec, handler string) ([]byte, error) {
	limit := 0
	switch codec {
	case "utf-8":
		// Go strings are already UTF-8, so there is nothing a valid one
		// cannot represent.
		return []byte(s), nil
	case "ascii":
		limit = 0x80
	case "latin-1":
		limit = 0x100
	}
	var out []byte
	for pos, r := range []rune(s) {
		if int(r) < limit {
			out = append(out, byte(r))
			continue
		}
		switch handler {
		case "strict":
			return nil, errs.New(errs.UnicodeEncodeError,
				"'%s' codec can't encode character '%s' in position %d: ordinal not in range(%d)",
				codec, charEscape(r), pos, limit)
		case "ignore":
		case "replace":
			out = append(out, '?')
		case "xmlcharrefreplace":
			out = append(out, fmt.Sprintf("&#%d;", r)...)
		case "backslashreplace":
			out = append(out, charEscape(r)...)
		case "surrogateescape", "surrogatepass":
			// Both exist to carry a lone surrogate through, and
			// fall back on strict for anything else. A Go string
			// holds no surrogates, so "anything else" is every
			// character that can reach here.
			return nil, errs.New(errs.UnicodeEncodeError,
				"'%s' codec can't encode character '%s' in position %d: ordinal not in range(%d)",
				codec, charEscape(r), pos, limit)
		default:
			return nil, errs.New(errs.LookupError,
				"unknown error handler name '%s'", handler)
		}
	}
	return out, nil
}

// decodeBytes is bytes.decode.
// Decoding walks a subject of the caller's length, so each of the three loops
// yields. The charge for the result is made once by the caller, which consults
// the context once and then leaves the walk out of reach of any deadline.
func decodeBytes(st *State, b []byte, codec, handler string) (string, error) {
	var out strings.Builder
	switch codec {
	case "latin-1":
		// Every byte is the code point of the same value, so this is the
		// one decode that cannot fail.
		for _, c := range b {
			if err := st.Poll(); err != nil {
				return "", err
			}
			out.WriteRune(rune(c))
		}
		return out.String(), nil
	case "ascii":
		for pos, c := range b {
			if err := st.Poll(); err != nil {
				return "", err
			}
			if c < 0x80 {
				out.WriteByte(c)
				continue
			}
			if err := decodeError(&out, handler, b[pos:pos+1], func() error {
				return errs.New(errs.UnicodeDecodeError,
					"'ascii' codec can't decode byte 0x%02x in position %d: ordinal not in range(128)",
					c, pos)
			}); err != nil {
				return "", err
			}
		}
		return out.String(), nil
	}
	// utf-8
	for i := 0; i < len(b); {
		if err := st.Poll(); err != nil {
			return "", err
		}
		r, size, reason, bad := utf8Step(b, i)
		if reason == "" {
			out.WriteRune(r)
			i += size
			continue
		}
		// One error covers `bad` bytes, so one replacement character
		// stands for all of them and the walk resumes past the lot.
		start, end := i, i+bad
		if err := decodeError(&out, handler, b[start:end], func() error {
			if bad == 1 {
				return errs.New(errs.UnicodeDecodeError,
					"'utf-8' codec can't decode byte 0x%02x in position %d: %s",
					b[start], start, reason)
			}
			return errs.New(errs.UnicodeDecodeError,
				"'utf-8' codec can't decode bytes in position %d-%d: %s",
				start, end-1, reason)
		}); err != nil {
			return "", err
		}
		i = end
	}
	return out.String(), nil
}

// The three things CPython's decoder can say about a byte it will not take.
const (
	utf8InvalidStart = "invalid start byte"
	utf8InvalidCont  = "invalid continuation byte"
	utf8EndOfData    = "unexpected end of data"
)

// isContinuation is CPython's IS_CONTINUATION_BYTE.
func isContinuation(c byte) bool { return c >= 0x80 && c < 0xc0 }

// utf8Step is CPython's own UTF-8 state machine, transcribed from
// stringlib/codecs.h, decoding the sequence that begins at i. A good sequence
// comes back as its rune and length with an empty reason; a bad one comes back
// as the reason and the number of bytes the error covers.
//
// That count is not decoration. It is the endinpos CPython hands the error
// handler, so it decides three visible things at once: whether the message
// names one byte or a range of them, how far the walk resumes past the damage,
// and -- because "replace" substitutes one character per error and not per
// byte -- how many replacement characters a bad sequence becomes. A decoder
// that reports every bad byte separately gets all three wrong for a truncated
// sequence: `b"\xf0\x9f"` is one error over two bytes, printed as "position
// 0-1" and replaced by a single U+FFFD.
//
// The three ranges CPython rejects before looking at a continuation byte are
// the ones that could only encode something already spelled shorter or not at
// all: \xc0-\xc1 (a code point below 0x80), \xe0 followed by under \xa0 (below
// 0x800), \xf0 followed by under \x90 (below 0x10000), \xed followed by \xa0 or
// more (a surrogate), \xf4 followed by \x90 or more (above U+10FFFF), and
// \xf5-\xff (above U+10FFFF whatever follows).
func utf8Step(b []byte, i int) (r rune, size int, reason string, bad int) {
	c := b[i]
	left := len(b) - i
	switch {
	case c < 0x80:
		return rune(c), 1, "", 0

	case c < 0xc2:
		// \x80-\xbf continues nothing; \xc0-\xc1 is overlong.
		return 0, 0, utf8InvalidStart, 1

	case c < 0xe0:
		if left < 2 {
			return 0, 0, utf8EndOfData, left
		}
		if !isContinuation(b[i+1]) {
			return 0, 0, utf8InvalidCont, 1
		}
		return rune(c&0x1f)<<6 | rune(b[i+1]&0x3f), 2, "", 0

	case c < 0xf0:
		if left < 2 {
			return 0, 0, utf8EndOfData, left
		}
		c2 := b[i+1]
		// CPython's `ch2 < 0xA0 ? ch == 0xE0 : ch == 0xED`: a low
		// second byte is overlong after \xe0, and a high one is a
		// surrogate after \xed.
		outOfRange := c == 0xed
		if c2 < 0xa0 {
			outOfRange = c == 0xe0
		}
		if !isContinuation(c2) || outOfRange {
			return 0, 0, utf8InvalidCont, 1
		}
		if left < 3 {
			return 0, 0, utf8EndOfData, left
		}
		if !isContinuation(b[i+2]) {
			return 0, 0, utf8InvalidCont, 2
		}
		return rune(c&0x0f)<<12 | rune(c2&0x3f)<<6 | rune(b[i+2]&0x3f), 3, "", 0

	case c < 0xf5:
		if left < 2 {
			return 0, 0, utf8EndOfData, left
		}
		c2 := b[i+1]
		// CPython's `ch2 < 0x90 ? ch == 0xF0 : ch == 0xF4`: overlong
		// after \xf0, past U+10FFFF after \xf4.
		outOfRange := c == 0xf4
		if c2 < 0x90 {
			outOfRange = c == 0xf0
		}
		if !isContinuation(c2) || outOfRange {
			return 0, 0, utf8InvalidCont, 1
		}
		if left < 3 {
			return 0, 0, utf8EndOfData, left
		}
		if !isContinuation(b[i+2]) {
			return 0, 0, utf8InvalidCont, 2
		}
		if left < 4 {
			return 0, 0, utf8EndOfData, left
		}
		if !isContinuation(b[i+3]) {
			return 0, 0, utf8InvalidCont, 3
		}
		return rune(c&0x07)<<18 | rune(c2&0x3f)<<12 | rune(b[i+2]&0x3f)<<6 | rune(b[i+3]&0x3f), 4, "", 0
	}
	return 0, 0, utf8InvalidStart, 1
}

// decodeError applies the handler to one undecodable run of bytes, writing
// whatever it substitutes and returning an error only for "strict".
//
// The run is the unit and not the byte. CPython calls the handler once per
// error with the whole range the error covers, so "replace" writes a single
// U+FFFD for a truncated sequence however long it was, and "backslashreplace"
// writes one \xNN for each byte of it.
//
// Two of CPython's handlers are encode-only -- its callback looks for a
// UnicodeEncodeError and refuses anything else by type -- and two more answer
// with a lone surrogate, which a Go string cannot hold; see
// docs/divergences.md for the second pair.
func decodeError(out *strings.Builder, handler string, bad []byte, strict func() error) error {
	switch handler {
	case "strict":
		return strict()
	case "ignore":
		return nil
	case "replace":
		out.WriteRune(utf8.RuneError)
		return nil
	case "backslashreplace":
		// A byte is below 0x100, so charEscape always answers the
		// \xNN form here -- the same escape the encode side writes.
		for _, c := range bad {
			out.WriteString(charEscape(rune(c)))
		}
		return nil
	case "xmlcharrefreplace", "namereplace":
		return errs.New(errs.TypeError,
			"don't know how to handle UnicodeDecodeError in error callback")
	default:
		// Including "surrogateescape" and "surrogatepass", which
		// CPython has and gojja2 cannot represent.
		return errs.New(errs.LookupError, "unknown error handler name '%s'", handler)
	}
}

// codecArgs reads the (encoding, errors) pair both encode and decode take.
func codecArgs(args *value.CallArgs, method string) (codec, handler string, err error) {
	codec, handler = "utf-8", "strict"
	// A None is not the default here either: str.encode's arguments are
	// declared as str, so an explicit None is refused rather than falling
	// back on utf-8.
	// Both arguments are converted while the call is parsed, before the
	// codec is looked up -- so `"x".encode("nosuch", 1)` is about the 1 and
	// not about the missing codec.
	encoding, hasEncoding := arg(args, 0, "encoding")
	if hasEncoding && !encoding.IsString() {
		return "", "", errs.New(errs.TypeError,
			"%s() argument 'encoding' must be str, not %s", method, clinicTypeName(encoding))
	}
	if v, ok := arg(args, 1, "errors"); ok {
		if !v.IsString() {
			return "", "", errs.New(errs.TypeError,
				"%s() argument 'errors' must be str, not %s", method, clinicTypeName(v))
		}
		handler = value.Str(v)
	}
	if hasEncoding {
		name, known := codecName(value.Str(encoding))
		if !known {
			return "", "", errs.New(errs.LookupError,
				"unknown encoding: %s", value.Str(encoding))
		}
		codec = name
	}
	return codec, handler, nil
}

// encodeExpansion and decodeExpansion are how much longer than its input a
// codec can make its result. The budget is charged before the work, so it has
// to be charged for the largest the pair can produce:
//
//	encode  xmlcharrefreplace turns U+0080 -- two bytes of UTF-8 -- into the
//	        six of "&#128;", and backslashreplace into the six of a u escape
//	decode  a latin-1 byte above 0x7f becomes two bytes of UTF-8, U+FFFD is
//	        three, and a backslash escape is four
//
// Four is the widest of those, and only an input already past half the 2**31
// ceiling can notice the difference between four and the exact ratio.
const codecMaxExpansion = 4

func encodeExpansion(handler string) int64 {
	switch handler {
	case "xmlcharrefreplace", "backslashreplace":
		return codecMaxExpansion
	}
	return 1
}

func decodeExpansion(codec, handler string) int64 {
	if codec == "latin-1" {
		return 2
	}
	switch handler {
	case "replace", "backslashreplace":
		return codecMaxExpansion
	}
	return 1
}

func methodEncode(s *State, r value.Value, args *value.CallArgs) (value.Value, error) {
	codec, handler, err := codecArgs(args, "encode")
	if err != nil {
		return value.Undefined, err
	}
	// An escaping handler makes the result longer than the input -- U+0080
	// is two bytes and "&#128;" is six -- so what is charged is the largest
	// this pair can produce, not the input's own length. The comment here
	// said that for some time while the arithmetic did not.
	if err := s.ChargeBytes(int64(len(r.AsString())) * encodeExpansion(handler)); err != nil {
		return value.Undefined, err
	}
	out, err := encodeString(r.AsString(), codec, handler)
	if err != nil {
		return value.Undefined, err
	}
	return value.Bytes(out), nil
}

func methodDecode(s *State, r value.Value, args *value.CallArgs) (value.Value, error) {
	codec, handler, err := codecArgs(args, "decode")
	if err != nil {
		return value.Undefined, err
	}
	raw := []byte(r.AsString())
	if err := s.ChargeBytes(int64(len(raw)) * decodeExpansion(codec, handler)); err != nil {
		return value.Undefined, err
	}
	out, err := decodeBytes(s, raw, codec, handler)
	if err != nil {
		return value.Undefined, err
	}
	return value.String(out), nil
}
