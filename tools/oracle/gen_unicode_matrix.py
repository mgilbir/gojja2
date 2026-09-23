#!/usr/bin/env python3
# Copyright 2026 The gojja2 Authors
# SPDX-License-Identifier: Apache-2.0
"""Write where another CPython's Unicode answers differ from the pinned one's.

CPython carries its own Unicode, so the interpreter decides a template's case
mappings, which characters are digits, and how repr escapes them. Across the
versions gojja2 reproduces that is some ten thousand code points -- and gojja2
once answered the pinned version's way whatever WithPythonVersion asked for,
which made the option mean less than it said.

Almost all of it is one property. The oldest and newest disagree about
isprintable for ten thousand code points and about everything else put together
for a couple of hundred, because the difference is simply which characters had
been assigned yet -- and newly assigned characters arrive in blocks, so they
collapse into a few dozen ranges. The whole table is a handful of kilobytes.

Nothing here is derived. Each interpreter is asked directly, through
`uv run --python`, so a release that changes something unexpected shows up as a
diff rather than as a rule nobody wrote down.
"""

from __future__ import annotations

import subprocess
import sys
from pathlib import Path

import pyversions

ROOT = pyversions.ROOT
DST = ROOT / "value" / "unicode_other.go"
DUMPER = Path(__file__).resolve().parent / "dump_unicode.py"

# Which interpreters exist and which is the pin both come from pyversions, so
# a bump to PYTHON_VERSION rotates this table without editing it.
VERSIONS = pyversions.others()
DEFAULT = pyversions.default()

# The columns dump_unicode.py writes, after the code point.
UPPER, LOWER, TITLE, FOLD, DECIMAL, FLAGS = range(6)
ISLOWER, ISUPPER, ISTITLE, ISDIGIT, ISNUMERIC, ISPRINTABLE, ISALPHA = range(7)


def dump(version: str) -> dict[int, tuple[str, ...]]:
    """Ask one interpreter about every code point."""
    proc = subprocess.run(
        ["uv", "run", "--python", version, "--no-project", "python", str(DUMPER)],
        capture_output=True, text=True, check=True)
    sys.stderr.write("  " + proc.stderr.strip() + "\n")
    out = {}
    for line in proc.stdout.splitlines():
        parts = line.split("\t")
        if len(parts) == 7:
            out[int(parts[0])] = tuple(parts[1:])
    if not out:
        raise SystemExit(f"no output from CPython {version}")
    return out


def go_answers() -> dict[int, tuple[bool, bool, bool]]:
    """What Go's tables say about isalpha, isprintable and isdigit.

    gojja2 reads those three straight off Go, which is right only while Go and
    the pinned CPython agree about which characters exist -- and they need not.
    Go 1.26 is Unicode 15.0; a CPython on a later one knows letters Go does not,
    and one on an earlier release knows fewer.
    """
    out = subprocess.run(["go", "run", "./tools/gocase"], cwd=ROOT,
                         capture_output=True, text=True, check=True).stdout
    m = {}
    for line in out.splitlines():
        cp, _u, _l, _t, alpha, printable, digit = line.split("\t")
        m[int(cp)] = (alpha == "true", printable == "true", digit == "true")
    return m


def ranges(points: set[int]) -> list[tuple[int, int]]:
    """Collapse code points into inclusive ranges."""
    out: list[tuple[int, int]] = []
    for cp in sorted(points):
        if out and cp == out[-1][1] + 1:
            out[-1] = (out[-1][0], cp)
        else:
            out.append((cp, cp))
    return out


def range_table(name: str, points: set[int], doc: str) -> str:
    r16 = [r for r in ranges(points) if r[1] <= 0xFFFF]
    r32 = [r for r in ranges(points) if r[1] > 0xFFFF]
    lines = [doc, f"var {name} = &unicode.RangeTable{{"]
    if r16:
        lines.append("\tR16: []unicode.Range16{")
        lines += ["\t\t{0x%04x, 0x%04x, 1}," % r for r in r16]
        lines.append("\t},")
    if r32:
        lines.append("\tR32: []unicode.Range32{")
        lines += ["\t\t{0x%06x, 0x%06x, 1}," % r for r in r32]
        lines.append("\t},")
    lines.append("}")
    return "\n".join(lines)


def go_rune_map(name: str, pairs: dict[int, str], doc: str) -> str:
    lines = [doc, f"var {name} = map[rune]string{{"]
    for cp in sorted(pairs):
        lines.append("\t0x%04x: %s," % (cp, go_string(pairs[cp])))
    lines.append("}")
    return "\n".join(lines)


def go_string(s: str) -> str:
    """A Go string literal.

    Go's \\u takes exactly four hex digits, so anything above the basic plane
    needs \\U and eight. Writing five digits does not fail -- it reads the
    first four and leaves the rest as text, which turned Garay's lowercase into
    a Georgian letter followed by a zero.
    """
    out = []
    for ch in s:
        cp = ord(ch)
        if " " <= ch <= "~" and ch not in '"\\':
            out.append(ch)
        elif cp <= 0xFFFF:
            out.append("\\u%04x" % cp)
        else:
            out.append("\\U%08x" % cp)
    return '"%s"' % "".join(out)


def main() -> int:
    sys.stderr.write("asking each interpreter about every code point:\n")
    base = dump(DEFAULT)
    parts = [f'''// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

// Code generated by tools/oracle/gen_unicode_matrix.py. DO NOT EDIT.

package value

import "unicode"

// The Unicode answers a non-pinned CPython gives where they differ from the
// pinned one's, which the committed tables are built for. The set includes
// versions newer than the pin as well as older ones.
//
// CPython carries its own Unicode, so the interpreter decides case mappings,
// which characters are digits, and how repr escapes them. Almost all of the
// difference is isprintable, and almost all of that is simply which characters
// had been assigned yet -- so it arrives in blocks and stores as ranges.
//
// Regenerate with `make unicode-matrix`, which asks each interpreter directly.
''']

    # The pinned interpreter's own answers, not the difference from Go's.
    #
    # These were a delta over unicode.IsLetter and unicode.IsGraphic, which made
    # every answer depend on the Unicode release of the toolchain that generated
    # them: a Go upgrade moved the baseline and left the delta describing a
    # difference that was no longer there. It failed loudly rather than quietly
    # -- the corpus catches it -- but "loudly" still meant two tracked files
    # changing under whoever happened to upgrade first, and a table that is only
    # true for one compiler is not a table.
    #
    # Written whole, they cost about nine times the ranges and owe nothing to Go.
    alpha = {cp for cp in base if base[cp][FLAGS][ISALPHA] == "1"}
    printable = {cp for cp in base if base[cp][FLAGS][ISPRINTABLE] == "1"}
    parts.append(range_table(
        "alphaDefault", alpha,
        "// alphaDefault is str.isalpha for the pinned interpreter, whole rather\n"
        "// than as a difference from Go's tables: the answer is CPython's and\n"
        f"// owes nothing to the Unicode release Go carries. {len(alpha)} code points."))
    parts.append(range_table(
        "printableDefault", printable,
        "// printableDefault is str.isprintable for the pinned interpreter, which\n"
        "// is what repr escapes by -- and, like alphaDefault, CPython's own answer\n"
        f"// rather than a correction to Go's. {len(printable)} code points."))

    counts = []
    for v in VERSIONS:
        older = dump(v)
        const = "Python" + v.replace(".", "")
        suffix = v.replace(".", "")

        printable = {cp for cp in base
                     if older.get(cp) and older[cp][FLAGS][ISPRINTABLE] != base[cp][FLAGS][ISPRINTABLE]}
        digit = {cp for cp in base
                 if older.get(cp) and older[cp][FLAGS][ISDIGIT] != base[cp][FLAGS][ISDIGIT]}
        numeric = {cp for cp in base
                   if older.get(cp) and older[cp][FLAGS][ISNUMERIC] != base[cp][FLAGS][ISNUMERIC]}
        decimal = {cp for cp in base
                   if older.get(cp) and older[cp][DECIMAL] != base[cp][DECIMAL]}
        lower = {cp for cp in base
                 if older.get(cp) and older[cp][FLAGS][ISLOWER] != base[cp][FLAGS][ISLOWER]}
        upper = {cp for cp in base
                 if older.get(cp) and older[cp][FLAGS][ISUPPER] != base[cp][FLAGS][ISUPPER]}
        title = {cp for cp in base
                 if older.get(cp) and older[cp][FLAGS][ISTITLE] != base[cp][FLAGS][ISTITLE]}
        alpha = {cp for cp in base
                 if older.get(cp) and older[cp][FLAGS][ISALPHA] != base[cp][FLAGS][ISALPHA]}
        maps = {col: {cp: older[cp][col] for cp in base
                      if older.get(cp) and older[cp][col] != base[cp][col]}
                for col in (UPPER, LOWER, TITLE, FOLD)}

        parts.append(range_table(
            f"printableOther{suffix}", printable,
            f"// printableOther{suffix} is where CPython {v} and the pin disagree about\n"
            f"// isprintable, which is what repr escapes by. {len(printable)} code points."))
        for col, nm in ((UPPER, "Upper"), (LOWER, "Lower"), (TITLE, "Title"), (FOLD, "Fold")):
            parts.append(go_rune_map(
                f"{nm.lower()}Other{suffix}", maps[col],
                f"// {nm.lower()}Other{suffix} is str.{nm.lower()}() where CPython {v} differs\n"
                f"// from the pin. {len(maps[col])} code points."))
        for pts, nm in ((digit, "digit"), (numeric, "numeric"), (decimal, "decimal"),
                        (lower, "isLower"), (upper, "isUpper"), (title, "isTitle"),
                        (alpha, "isAlpha")):
            parts.append(range_table(
                f"{nm}Other{suffix}", pts,
                f"// {nm}Other{suffix} is where CPython {v} and the pin disagree\n"
                f"// about {nm}. {len(pts)} code points."))
        counts.append((v, len(printable), len(digit) + len(numeric) + len(decimal),
                       sum(len(m) for m in maps.values()) + len(lower) + len(upper) + len(title)))

    # One place that names every table, so a lookup asks by version rather than
    # by spelling a variable name.
    rows = []
    for v in VERSIONS:
        suffix = v.replace(".", "")
        rows.append(
            f"\tPython{suffix}: {{\n"
            f"\t\tprintable: printableOther{suffix},\n"
            f"\t\tupper: upperOther{suffix}, lower: lowerOther{suffix},\n"
            f"\t\ttitle: titleOther{suffix}, fold: foldOther{suffix},\n"
            f"\t\tdigit: digitOther{suffix}, numeric: numericOther{suffix},\n"
            f"\t\tdecimal: decimalOther{suffix},\n"
            f"\t\tisLower: isLowerOther{suffix}, isUpper: isUpperOther{suffix},\n"
            f"\t\tisTitle: isTitleOther{suffix}, isAlpha: isAlphaOther{suffix},\n"
            f"\t}},\n")
    parts.append(
        "// unicodeOther is every non-pinned interpreter's overrides, by version.\n"
        "// A version absent here answers exactly as the pinned one does.\n"
        "var unicodeOther = map[PythonVersion]*UnicodeOverrides{\n"
        + "".join(rows) + "}")

    body = "\n\n".join(parts) + "\n"
    DST.write_text(body, encoding="utf-8")
    sys.stderr.write(f"pin: {len(alpha)} isalpha, {len(printable)} isprintable "
                     "(absolute, not a delta over Go)\n")
    for v, p, n, c in counts:
        sys.stderr.write(f"CPython {v}: {p} printable, {n} numeric, {c} casing\n")
    sys.stderr.write(f"wrote {DST.relative_to(ROOT)}\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
