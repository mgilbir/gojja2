#!/usr/bin/env python3
# Copyright 2026 The gojja2 Authors
# SPDX-License-Identifier: Apache-2.0
"""Harvest template sources from Jinja's own pytest suite.

The suite's assertions are Python and cannot be run against a Go library, but
the *templates* in it are the most adversarial corpus available: they were
written by the people who know where the language is sharp. Each one is
extracted and re-rendered through CPython jinja2 to get its expected result, so
whatever it does -- render, or raise -- becomes a conformance case.

Most are extracted without their context, so they raise UndefinedError or
TemplateNotFound. That is not a defect: getting the same failure, with the same
message on the same line, is exactly what conformance means.

Nothing is vendored. `make suites` clones the suite into the gitignored
third_party/, and the cases land in the gitignored testdata/generated/.
"""

from __future__ import annotations

import ast
import hashlib
import json
import re
import shutil
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
SRC = ROOT / "third_party" / "jinja" / "tests"
DST = ROOT / "testdata" / "generated" / "jinja-harvest"

SEPARATOR = "\n---\n"

# A string is taken for a template only if it contains a delimiter; anything
# else is an assertion message or a filename.
TEMPLATE_MARKER = re.compile(r"\{\{|\{%|\{#")

# Constructs whose output is random or environment-dependent, so no
# implementation could be graded against a recorded answer.
NONDETERMINISTIC = re.compile(r"\blipsum\b|\brandom\b|\bnow\(|\bid\(")

# Async-only templates: out of scope, see docs/scope.md.
OUT_OF_SCOPE = re.compile(r"\basync\b|\bawait\b")


def template_strings(path: Path) -> list[str]:
    """Return every string constant in a file that looks like a template."""
    try:
        tree = ast.parse(path.read_text(encoding="utf-8"))
    except SyntaxError:
        return []

    found: list[str] = []
    for node in ast.walk(tree):
        if not isinstance(node, ast.Constant) or not isinstance(node.value, str):
            continue
        text = node.value
        if not TEMPLATE_MARKER.search(text):
            continue
        if NONDETERMINISTIC.search(text) or OUT_OF_SCOPE.search(text):
            continue
        # Very long strings are usually fixtures for debug/traceback tests.
        if len(text) > 2000:
            continue
        found.append(text)
    return found


def main() -> int:
    if not SRC.is_dir():
        print(f"{SRC} not found; run `make suites` first", file=sys.stderr)
        return 1

    if DST.exists():
        shutil.rmtree(DST)
    DST.mkdir(parents=True)

    seen: set[str] = set()
    count = 0
    for path in sorted(SRC.glob("test_*.py")):
        stem = path.stem.removeprefix("test_")
        for text in template_strings(path):
            if text in seen:
                continue
            seen.add(text)
            # The hash keeps names stable as the suite changes, so a case
            # that regresses keeps the same identity across updates.
            digest = hashlib.sha256(text.encode()).hexdigest()[:10]
            name = f"{stem}_{digest}.jj2"
            (DST / name).write_text("{}" + SEPARATOR + text, encoding="utf-8")
            count += 1

    print(f"harvested {count} templates into {DST.relative_to(ROOT)}", file=sys.stderr)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
