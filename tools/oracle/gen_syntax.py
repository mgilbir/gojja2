#!/usr/bin/env python3
# Copyright 2026 The gojja2 Authors
# SPDX-License-Identifier: Apache-2.0
"""Record jinja2's parse tree for every corpus case, in gojja2's vocabulary.

syntax_emit.py writes jinja2's tree in the normalised form the `syntax` package
defines, together with the scope and binding facts that go with it. This writes
one line per case, and conformance/syntax_test.go requires the engine to encode
both to the same bytes.

Byte equality is a much stronger statement than agreeing about the answer to any
one question. An analysis that matches proves the two agree about *that*
analysis; trees that match prove they agree about every question either will
ever be asked, including the ones nobody has written yet. That is what makes an
exposed tree worth exposing: a caller writing their own query gets the same
answer they would have got from jinja2, and this is the evidence.

One file rather than 2,207: the trees are small, a line per case diffs exactly
as a file per case would, and the directory listing stays legible.
"""

from __future__ import annotations

import json
import sys

import pyversions
import syntax_emit
from oracle import Case

ROOT = pyversions.ROOT
CORPUS = ROOT / "testdata/corpus"
DST = ROOT / "testdata/syntax.jsonl"


def main() -> int:
    if not CORPUS.is_dir():
        raise SystemExit(f"{CORPUS} does not exist; run `make oracle` first")

    lines, skipped = [], 0
    unsupported = {}
    for path in sorted(CORPUS.rglob("*.jj2")):
        case = Case(path, CORPUS)
        try:
            env = case.environment()
            # Compiled as well as parsed, because the engine refuses at compile
            # time what jinja2 refuses at code generation -- a block defined
            # twice, `{% extends %}` below the top level -- and a case neither
            # will compile has no tree to compare.
            env.get_template(case.rel)
            tree = env.parse(case.source, case.rel)
        except Exception:
            skipped += 1
            continue
        try:
            emitted = syntax_emit.canonical(tree, env.globals)
            info = syntax_emit.canonical_info(tree, env.globals)
        except syntax_emit.Unsupported as exc:
            # A node the vocabulary cannot spell is a gap in the vocabulary,
            # not a case to drop quietly.
            unsupported[str(exc)] = unsupported.get(str(exc), 0) + 1
            continue
        lines.append(json.dumps(
            {"case": case.rel, "tree": emitted, "info": info},
            ensure_ascii=False, separators=(",", ":")))

    if unsupported:
        for what, n in sorted(unsupported.items()):
            print(f"no spelling for {what} ({n} case(s))", file=sys.stderr)
        raise SystemExit("the syntax vocabulary is missing a node; add it to "
                         "gojja2/syntax and to both emitters")

    DST.write_text("\n".join(lines) + "\n", encoding="utf-8")
    print(f"{len(lines)} trees, {skipped} cases that do not compile, "
          f"into {DST.relative_to(ROOT)}", file=sys.stderr)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
