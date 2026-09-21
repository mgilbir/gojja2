// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

// gocase dumps what Go's own tables say, so a generator written in Python can
// compare them against CPython's.
//
// casemap.go records only where CPython differs from Go, which needs both
// answers. Python has one of them; this supplies the other. The alternative --
// assuming the two agree except where Python's mapping is multi-character --
// held only while the two tracked the same Unicode, and stopped the moment the
// pinned CPython moved ahead: Unicode 16 gave 54 code points a simple mapping
// that Go 1.26, on 15.0, does not have.
package main

import (
	"bufio"
	"fmt"
	"os"
	"unicode"
)

func main() {
	w := bufio.NewWriter(os.Stdout)
	for cp := range 0x110000 {
		if cp >= 0xD800 && cp < 0xE000 {
			continue
		}
		r := rune(cp)
		if _, err := fmt.Fprintf(w, "%d\t%d\t%d\t%d\t%t\t%t\t%t\n", cp,
			unicode.ToUpper(r), unicode.ToLower(r), unicode.ToTitle(r),
			unicode.IsLetter(r),
			// The two predicates gojja2 reads straight off Go's
			// tables, which is only right while Go and the pinned
			// CPython agree about which characters exist.
			unicode.IsGraphic(r) && !unicode.Is(unicode.Zs, r) || r == ' ',
			unicode.IsDigit(r)); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	if err := w.Flush(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
