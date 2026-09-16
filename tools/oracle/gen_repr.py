#!/usr/bin/env python3
# Copyright 2026 The gojja2 Authors
# SPDX-License-Identifier: Apache-2.0
"""Generate the CPython repr() corpus that value/repr_test.go checks against.

repr() is not jinja2's, it is Python's, and it leaks into rendered output
everywhere a container is printed. Matching it is therefore part of the spec,
so the expected strings come from a real interpreter rather than from
hand-written guesses.
"""

from __future__ import annotations

import json
import random
import struct
import sys
from pathlib import Path

OUT = Path(__file__).resolve().parents[2] / "value" / "testdata"

# Values chosen to sit on every boundary in CPython's float_repr: the switch to
# exponential form at both ends, subnormals, the shortest-digits cases, and the
# single-significant-digit case that must not gain a ".0".
FLOAT_EDGES = [
    0.0, -0.0, 1.0, -1.0, 0.5, -0.5,
    1e15, 1e16, 1e17, -1e16, 9999999999999998.0, 1234567890123456.0,
    1e-4, 1e-5, 1e-3, 0.0001, 0.00001,
    1e100, 1e-100, 1e308, 1.7976931348623157e308, 5e-324, 2.2250738585072014e-308,
    0.1, 0.2, 0.30000000000000004, 1 / 3, 2 / 3, 3.14159265358979,
    100.0, 1000000.0, -2.5e-7, 123456789012345.0,
    float("inf"), float("-inf"), float("nan"),
]

STRING_EDGES = [
    "", "a", "it's", 'say "hi"', "both ' and \"", "tab\there", "nl\nhere",
    "cr\rhere", "back\\slash", "\x00\x01\x1f\x7f", "\xa0nbsp", "héllo",
    "日本語", "emoji \U0001f600", "  ", "​", "￿",
    "\U0010ffff", "á", "\x85", "\v\f", "'", '"', "\\", "€", " ",
    "　", "᠎", "", "line1\nline2\n",
]

# Code points that exercise each isprintable() category boundary.
CHAR_POOL = (
    list(range(0x20, 0x7F))
    + [0x0A, 0x0D, 0x09, 0x00, 0x1B, 0x7F, 0xA0, 0xAD, 0xE9, 0x2028, 0x3042,
       0x200B, 0xFFFD, 0x1F600, 0x10FFFF, 0xD7FF, 0xE000, 0x2003, 0x5C, 0x27, 0x22]
)


def floats(n: int) -> list[dict]:
    rng = random.Random(20260916)
    vals = list(FLOAT_EDGES)
    while len(vals) < n:
        f = struct.unpack("<d", struct.pack("<Q", rng.getrandbits(64)))[0]
        if f != f or f in (float("inf"), float("-inf")):
            continue
        vals.append(f)
        vals.append(rng.uniform(-1e6, 1e6))
        vals.append(rng.uniform(-1, 1))
        vals.append(float(rng.randint(-(10**18), 10**18)))
    return [
        {"bits": struct.unpack("<Q", struct.pack("<d", f))[0], "repr": repr(f)}
        for f in vals[:n]
    ]


def strings(n: int) -> list[dict]:
    rng = random.Random(20260917)
    vals = list(STRING_EDGES)
    while len(vals) < n:
        size = rng.randint(0, 12)
        vals.append("".join(chr(rng.choice(CHAR_POOL)) for _ in range(size)))
    return [{"s": s, "repr": repr(s)} for s in vals[:n]]


def write(name: str, rows: list[dict]) -> None:
    path = OUT / name
    path.write_text(json.dumps(rows, ensure_ascii=False, indent=0) + "\n", encoding="utf-8")
    print(f"{path.relative_to(Path.cwd())}: {len(rows)} cases", file=sys.stderr)


def main() -> int:
    OUT.mkdir(parents=True, exist_ok=True)
    write("repr_float.json", floats(2000))
    write("repr_str.json", strings(1200))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
