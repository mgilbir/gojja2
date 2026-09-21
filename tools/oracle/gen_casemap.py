#!/usr/bin/env python3
# Copyright 2026 The gojja2 Authors
# SPDX-License-Identifier: Apache-2.0
"""Write the case mappings and cased-character sets Python uses and Go does not.

Go's unicode package implements *simple* case mapping: one rune in, one rune
out. Python implements *full* case mapping, where a character may map to
several -- `"ß".upper()` is "SS", `"ﬁ".upper()` is "FI", `"İ".lower()` is "i"
followed by a combining dot. Using strings.ToUpper for str.upper therefore left
102 characters unchanged that Python expands, and casefold, which was
strings.ToLower, disagreed with Python for 298.

The second half is what counts as *cased*. Python's islower/isupper/istitle
rest on Unicode's Cased derived property, which is Lu/Ll/Lt plus
Other_Lowercase and Other_Uppercase -- the modifier letters, the Roman
numerals, the circled letters. Go's unicode.IsLower and IsUpper are the general
categories alone, so 370 code points answered the wrong way.

Only the exceptions are written down. Where Python's answer is a single rune,
Go's simple mapping already agrees -- that was checked across every code point,
and CPython 3.11's Unicode 14.0.0 and Go's 15.0.0 do not differ on any of them.
The digest at the end is what keeps that true: it covers every code point's
answer, and the Go test recomputes it, so a Go release that moves the tables
fails loudly rather than quietly.
"""

from __future__ import annotations

import hashlib
import subprocess
import sys

import pyversions
import unicodedata
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
DST = ROOT / "casemap.go"
MAX = 0x110000
# Lone surrogates have no UTF-8 encoding, and a Go string cannot hold one.
SURROGATES = range(0xD800, 0xE000)


def ranges(points: list[int]) -> list[tuple[int, int]]:
    out: list[tuple[int, int]] = []
    for cp in points:
        if out and cp == out[-1][1] + 1:
            out[-1] = (out[-1][0], cp)
        else:
            out.append((cp, cp))
    return out


def table(name: str, points: list[int], doc: str) -> str:
    r16 = [r for r in ranges(points) if r[1] <= 0xFFFF]
    r32 = [r for r in ranges(points) if r[1] > 0xFFFF]
    lines = [doc, "var %s = &unicode.RangeTable{" % name]
    if r16:
        lines.append("\tR16: []unicode.Range16{")
        lines += ["\t\t{0x%04x, 0x%04x, 1}," % (lo, hi) for lo, hi in r16]
        lines.append("\t},")
    if r32:
        lines.append("\tR32: []unicode.Range32{")
        lines += ["\t\t{0x%06x, 0x%06x, 1}," % (lo, hi) for lo, hi in r32]
        lines.append("\t},")
    lines.append("\tLatinOffset: %d," % sum(1 for lo, _ in r16 if lo <= 0xFF))
    lines.append("}")
    return "\n".join(lines)


def mapping(name: str, pairs: dict[int, str], doc: str) -> str:
    lines = [doc, "var %s = map[rune]string{" % name]
    for cp in sorted(pairs):
        lines.append("\t0x%04x: %s," % (cp, gostr(pairs[cp])))
    lines.append("}")
    return "\n".join(lines)


def gostr(s: str) -> str:
    return '"' + "".join("\\u%04x" % ord(c) if ord(c) < 0x10000
                         else "\\U%08x" % ord(c) for c in s) + '"'


def simple_lower(cp: int, go: dict[int, tuple[int, int, int]]) -> str:
    """The single-rune lowercase gojja2 falls back to when folding.

    pyFoldRune answers unicode.ToLower for anything not in foldSpecial, so this
    has to be *Go's* lowercase and not Python's: where the two disagree -- and
    they do for every code point Unicode gained after Go's tables were cut --
    the fold has to be recorded rather than left to the fallback.
    """
    return chr(go[cp][1])


def go_simple_mappings() -> dict[int, tuple[int, int, int]]:
    """Go's ToUpper, ToLower and ToTitle for every code point.

    tools/gocase prints the case predicates alongside the mappings, for the
    generators that need them; only the first four columns are read here. They
    are taken by position rather than unpacked, so a column added there does
    not break this the way it did once already.
    """
    out = subprocess.run(["go", "run", "./tools/gocase"], cwd=ROOT,
                         capture_output=True, text=True, check=True).stdout
    m = {}
    for line in out.splitlines():
        parts = line.split("\t")
        if len(parts) < 4:
            raise SystemExit(f"tools/gocase printed {len(parts)} columns, need at "
                             "least code point, upper, lower and title")
        cp, u, l, t = parts[:4]
        m[int(cp)] = (int(u), int(l), int(t))
    return m


def main() -> int:
    points = [cp for cp in range(MAX) if cp not in SURROGATES]

    # Go's own answers, so "where CPython differs" can be computed rather than
    # assumed. Assuming it -- taking every multi-character mapping and nothing
    # else -- held only while Go and CPython tracked the same Unicode, and the
    # pinned interpreter is free to move ahead: Unicode 16 gave 54 code points
    # a simple mapping Go 1.26 does not have, and every one of them was wrong
    # here until this read Go's tables instead of guessing at them.
    go = go_simple_mappings()

    def differs(cp: int, py: str, which: int) -> bool:
        if len(py) > 1:
            return True
        return ord(py) != go[cp][which]

    upper = {cp: chr(cp).upper() for cp in points if differs(cp, chr(cp).upper(), 0)}
    lower = {cp: chr(cp).lower() for cp in points if differs(cp, chr(cp).lower(), 1)}
    title = {cp: chr(cp).title() for cp in points if differs(cp, chr(cp).title(), 2)}
    fold = {cp: chr(cp).casefold() for cp in points
            if chr(cp).casefold() != simple_lower(cp, go)}

    is_lower = [cp for cp in points if chr(cp).islower()]
    is_upper = [cp for cp in points if chr(cp).isupper()]
    # Cased is what a word boundary in title() and the swap in swapcase() are
    # decided by, and it includes titlecase characters as well.
    cased = [cp for cp in points
             if chr(cp).islower() or chr(cp).isupper() or chr(cp).istitle()]

    h = hashlib.sha256()
    for cp in points:
        ch = chr(cp)
        h.update(("%d\t%s\t%s\t%s\t%s\t%d%d%d\n" % (
            cp, ch.upper(), ch.lower(), ch.title(), ch.casefold(),
            ch.islower(), ch.isupper(), ch.istitle())).encode("utf-8"))
    digest = h.hexdigest()

    parts = [
        "// Copyright 2026 The gojja2 Authors",
        "// SPDX-License-Identifier: Apache-2.0",
        "",
        "// Code generated by tools/oracle/gen_casemap.py from CPython %s"
        " (Unicode %s). DO NOT EDIT." % (
            pyversions.running(), unicodedata.unidata_version),
        "//",
        "// Go's unicode package does simple case mapping, one rune to one rune.",
        "// Python does full case mapping, where a character may map to several.",
        "// Only the differences are here; everywhere Python's answer is a single",
        "// rune, Go's tables already agree, and caseMapDigest is what proves it.",
        "",
        "package gojja2",
        "",
        'import "unicode"',
        "",
        mapping("upperSpecial", upper,
                "// upperSpecial is str.upper where it is not one rune: the sharp s, the\n"
                "// ligatures, and the Greek letters that carry their accents into separate\n"
                "// characters."),
        "",
        mapping("lowerSpecial", lower,
                "// lowerSpecial is str.lower where it is not one rune. There is exactly one:\n"
                "// LATIN CAPITAL LETTER I WITH DOT ABOVE, which lowercases to i plus a\n"
                "// combining dot so that the dot survives."),
        "",
        mapping("titleSpecial", title,
                "// titleSpecial is str.title's per-character mapping where it is not one\n"
                "// rune. It differs from upperSpecial: the sharp s titlecases to \"Ss\" and\n"
                "// uppercases to \"SS\"."),
        "",
        mapping("foldSpecial", fold,
                "// foldSpecial is str.casefold where it differs from the simple lowercase\n"
                "// mapping. casefold is not lowercase: it folds for caseless *comparison*,\n"
                "// so the final sigma folds onto the ordinary one and the micro sign onto\n"
                "// Greek mu. Reading it as lower was wrong for every entry here."),
        "",
        table("lowerCased", is_lower,
              "// lowerCased is str.islower for a single character: lowercase *and* cased.\n"
              "// Broader than unicode.IsLower, which is the Ll category alone -- Python\n"
              "// also counts Other_Lowercase, the modifier letters and the like."),
        "",
        table("upperCased", is_upper,
              "// upperCased is str.isupper for a single character, broader than unicode.IsUpper\n"
              "// for the same reason: Other_Uppercase carries the Roman numerals and the\n"
              "// circled capitals."),
        "",
        table("anyCased", cased,
              "// anyCased is Unicode's Cased property: what title() treats as inside a word\n"
              "// and what swapcase() will swap. Titlecase characters are cased too, which\n"
              "// is why this is not simply lowerCased plus upperCased."),
        "",
        "// caseMapDigest is a sha256 over every code point's upper, lower, title,\n"
        "// casefold, islower, isupper and istitle, as CPython answers them.\n"
        "//\n"
        "// The tables above record only the differences from Go's own, which is only\n"
        "// safe while Go and CPython still agree everywhere else. They track different\n"
        "// Unicode versions and are free to diverge at any release, so the test\n"
        "// recomputes this digest from gojja2's own functions across every code point.\n"
        "// A Go upgrade that moves a mapping breaks it, which is the point.",
        'const caseMapDigest = "%s"' % digest,
        "",
    ]
    DST.write_text("\n".join(parts), encoding="utf-8")
    print("wrote %s: upper %d, lower %d, title %d, fold %d; lower %d/upper %d/cased %d ranges"
          % (DST.relative_to(ROOT), len(upper), len(lower), len(title), len(fold),
             len(ranges(is_lower)), len(ranges(is_upper)), len(ranges(cased))),
          file=sys.stderr)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
