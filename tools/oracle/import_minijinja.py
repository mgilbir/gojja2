#!/usr/bin/env python3
# Copyright 2026 The gojja2 Authors
# SPDX-License-Identifier: Apache-2.0
"""Convert MiniJinja's fixture corpus into gojja2 cases.

MiniJinja's fixtures are a broad, adversarial set of templates someone else
wrote, which makes them far better at finding bugs than cases written by the
person fixing them. Their *expected output* is not used: every case is re-run
through CPython jinja2, and that answer is the one gojja2 is graded against.
Where MiniJinja diverges from jinja2 on purpose, jinja2 wins.

The fixtures are never vendored. `make suites` clones them into the gitignored
third_party/, and the converted cases land in the gitignored
testdata/generated/.
"""

from __future__ import annotations

import json
import shutil
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
SRC = ROOT / "third_party" / "minijinja" / "minijinja" / "tests" / "inputs"
DST = ROOT / "testdata" / "generated" / "minijinja"

SEPARATOR = "\n---\n"

# Keys MiniJinja's own harness reads as settings rather than as context.
SETTING_KEYS = {
    "keep_trailing_newline",
    "lstrip_blocks",
    "trim_blocks",
    "markers",
    "line_statement_prefix",
    "line_comment_prefix",
    "undefined",
}

MARKER_FIELDS = [
    "block_start_string",
    "block_end_string",
    "variable_start_string",
    "variable_end_string",
    "comment_start_string",
    "comment_end_string",
]


def load_refs() -> dict[str, str]:
    refs_dir = SRC / "refs"
    if not refs_dir.is_dir():
        return {}
    return {
        p.name: p.read_text(encoding="utf-8")
        for p in sorted(refs_dir.iterdir())
        if p.is_file()
    }


def convert(path: Path, refs: dict[str, str]) -> tuple[str, str]:
    raw = path.read_text(encoding="utf-8")
    header, sep, source = raw.partition(SEPARATOR)
    if not sep:
        header, source = "{}", raw

    ctx = json.loads(header) if header.strip() else {}
    settings = {}
    for key in list(ctx):
        if key in SETTING_KEYS:
            settings[key] = ctx.pop(key)

    if "markers" in settings:
        markers = settings.pop("markers")
        settings.update(dict(zip(MARKER_FIELDS, markers)))

    out = dict(ctx)
    if settings:
        out["__settings__"] = settings
    if refs:
        out["__templates__"] = refs

    return json.dumps(out, ensure_ascii=False, indent=2), source


def main() -> int:
    if not SRC.is_dir():
        print(f"{SRC} not found; run `make suites` first", file=sys.stderr)
        return 1

    if DST.exists():
        shutil.rmtree(DST)
    DST.mkdir(parents=True)

    refs = load_refs()
    count = 0
    for path in sorted(SRC.iterdir()):
        if not path.is_file():
            continue
        header, source = convert(path, refs)
        # The stem alone, so `block.txt` and `block.html` stay distinct.
        name = path.name.replace(".", "_") + ".jj2"
        (DST / name).write_text(header + SEPARATOR + source, encoding="utf-8")
        count += 1

    print(f"imported {count} MiniJinja fixtures into {DST.relative_to(ROOT)}", file=sys.stderr)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
