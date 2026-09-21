#!/usr/bin/env python3
# Copyright 2026 The gojja2 Authors
# SPDX-License-Identifier: Apache-2.0
"""Write one line per code point describing what this interpreter makes of it.

Run under each CPython gojja2 reproduces. gen_unicode_older.py reads the
results and records where the older ones differ from the default.

Kept separate from the generator, and free of every import but the two it
needs, so it can run under a bare `uv run --python 3.11` with nothing
installed.
"""

from __future__ import annotations

import sys
import unicodedata

MAX = 0x110000


def main() -> int:
    out = []
    for cp in range(MAX):
        if 0xD800 <= cp < 0xE000:
            # A lone surrogate has no UTF-8 encoding and a Go string cannot
            # hold one, so nothing here can ask about it.
            continue
        ch = chr(cp)
        try:
            dec = str(unicodedata.decimal(ch))
        except (ValueError, TypeError):
            dec = "-"
        out.append("%d\t%s\t%s\t%s\t%s\t%s\t%d%d%d%d%d%d%d\n" % (
            cp, ch.upper(), ch.lower(), ch.title(), ch.casefold(), dec,
            ch.islower(), ch.isupper(), ch.istitle(),
            ch.isdigit(), ch.isnumeric(), ch.isprintable(), ch.isalpha()))
    sys.stdout.write("".join(out))
    sys.stderr.write("dumped %d code points from CPython %s (Unicode %s)\n" % (
        len(out), sys.version.split()[0], unicodedata.unidata_version))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
