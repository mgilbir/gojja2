// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"os"
	"strings"
	"testing"
)

var utf8Dump = flag.Bool("utf8dump", false,
	"print the UTF-8 decode transcript instead of hashing it, to diff against "+
		"`python tools/oracle/gen_utf8.py --dump`")

// utf8Alphabet is gen_utf8.py's ALPHABET and must stay in step with it: the
// bytes a decode decision turns on, which is what the three- and four-byte
// sequences are drawn from.
var utf8Alphabet = []byte{
	0x00, 0x41, 0x7F,
	0x80, 0x81, 0x8F, 0x90, 0x91, 0x9F, 0xA0, 0xA1, 0xBF,
	0xC0, 0xC1, 0xC2, 0xC3, 0xDF,
	0xE0, 0xE1, 0xEC, 0xED, 0xEE, 0xEF,
	0xF0, 0xF1, 0xF3, 0xF4, 0xF5, 0xFE, 0xFF,
}

// Every way the UTF-8 decoder can refuse a byte, checked against CPython's.
//
// Decoding is not a yes or no. For a sequence it will not take, CPython also
// decides which of three reasons to name, how many bytes the error covers --
// which is the difference between "byte 0xf0 in position 0" and "bytes in
// position 0-1" -- and, because the error handler runs once per error and not
// once per byte, how many replacement characters a bad sequence becomes.
//
// Four of those were wrong here at once, and no corpus case reached any of
// them: the old decoder asked utf8.DecodeRune and reported a single bad byte,
// so a truncated sequence came out as several errors, all of them named
// "invalid continuation byte", and \xc0 and \xc1 were continuation problems
// rather than start bytes.
//
// There is no natural corpus for a state machine with sixteen ways out, so
// this walks it: every one- and two-byte sequence and every three- and
// four-byte sequence over the bytes a decision turns on, each decoded strict,
// replace and ignore. The generator hashes CPython's answers; this recomputes
// the same transcript from gojja2's decoder. `go test -run UTF8Decode
// -utf8dump` prints the transcript to diff against the generator's --dump when
// the digest moves.
func TestUTF8DecodeMatchesCPython(t *testing.T) {
	h := sha256.New()
	var line strings.Builder
	n := 0
	emit := func(seq []byte) {
		line.Reset()
		line.WriteString(hex.EncodeToString(seq))
		line.WriteByte('\t')
		if s, err := decodeBytes(nil, seq, "utf-8", "strict"); err != nil {
			line.WriteString("err\t")
			line.WriteString(err.Error())
		} else {
			line.WriteString("ok\t")
			line.WriteString(s)
		}
		for _, handler := range []string{"replace", "ignore"} {
			s, err := decodeBytes(nil, seq, "utf-8", handler)
			if err != nil {
				t.Fatalf("decode %x with %q: %v", seq, handler, err)
			}
			line.WriteByte('\t')
			line.WriteString(handler)
			line.WriteByte('\t')
			line.WriteString(s)
		}
		line.WriteByte('\n')
		if *utf8Dump {
			if _, err := os.Stdout.WriteString(line.String()); err != nil {
				t.Fatalf("writing the dump: %v", err)
			}
		}
		h.Write([]byte(line.String()))
		n++
	}

	buf := make([]byte, 4)
	for i := range 256 {
		buf[0] = byte(i)
		emit(buf[:1])
	}
	for i := range 256 {
		for j := range 256 {
			buf[0], buf[1] = byte(i), byte(j)
			emit(buf[:2])
		}
	}
	a := utf8Alphabet
	// Length at a time, and each length in the alphabet's own order, because
	// the digest is over the transcript and the transcript is ordered.
	for _, x := range a {
		for _, y := range a {
			for _, z := range a {
				buf[0], buf[1], buf[2] = x, y, z
				emit(buf[:3])
			}
		}
	}
	for _, x := range a {
		for _, y := range a {
			for _, z := range a {
				for _, w := range a {
					buf[0], buf[1], buf[2], buf[3] = x, y, z, w
					emit(buf[:4])
				}
			}
		}
	}
	if *utf8Dump {
		return
	}
	if n != utf8DecodeCases {
		t.Fatalf("walked %d sequences, the digest covers %d: the alphabet "+
			"here and gen_utf8.py's ALPHABET have drifted apart", n, utf8DecodeCases)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != utf8DecodeDigest {
		t.Errorf("UTF-8 decode digest = %s, want %s\n"+
			"A sequence decodes, or fails, differently from CPython. To find "+
			"which:\n"+
			"  go test -run UTF8Decode -utf8dump > /tmp/go.txt\n"+
			"  .venv/bin/python tools/oracle/gen_utf8.py --dump > /tmp/py.txt\n"+
			"  diff /tmp/py.txt /tmp/go.txt | head",
			got, utf8DecodeDigest)
	}
}
