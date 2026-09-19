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
			if err := decodeError(&out, handler, func() error {
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
		r, size := utf8.DecodeRune(b[i:])
		if r != utf8.RuneError || size > 1 {
			out.WriteRune(r)
			i += size
			continue
		}
		pos, bad := i, b[i]
		if err := decodeError(&out, handler, func() error {
			return errs.New(errs.UnicodeDecodeError,
				"'utf-8' codec can't decode byte 0x%02x in position %d: %s",
				bad, pos, utf8Reason(b, pos))
		}); err != nil {
			return "", err
		}
		i++
	}
	return out.String(), nil
}

// decodeError applies the handler to one undecodable byte, writing whatever it
// substitutes and returning an error only for "strict".
func decodeError(out *strings.Builder, handler string, strict func() error) error {
	switch handler {
	case "strict":
		return strict()
	case "ignore":
		return nil
	case "replace":
		out.WriteRune(utf8.RuneError)
		return nil
	default:
		return errs.New(errs.LookupError, "unknown error handler name '%s'", handler)
	}
}

// utf8Reason is CPython's explanation for a byte that does not decode: a byte
// that cannot begin a sequence is an invalid start byte, and one that could
// begin a sequence whose continuation is missing or wrong names the
// continuation instead.
func utf8Reason(b []byte, i int) string {
	c := b[i]
	switch {
	case c < 0xc0 || c > 0xf4:
		return "invalid start byte"
	default:
		return "invalid continuation byte"
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

func methodEncode(s *State, r value.Value, args *value.CallArgs) (value.Value, error) {
	codec, handler, err := codecArgs(args, "encode")
	if err != nil {
		return value.Undefined, err
	}
	// An escaping handler can make the result longer than the input, so the
	// size is charged rather than assumed.
	if err := s.ChargeBytes(int64(len(r.AsString()))); err != nil {
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
	if err := s.ChargeBytes(int64(len(raw))); err != nil {
		return value.Undefined, err
	}
	out, err := decodeBytes(s, raw, codec, handler)
	if err != nil {
		return value.Undefined, err
	}
	return value.String(out), nil
}
