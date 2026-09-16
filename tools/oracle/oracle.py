#!/usr/bin/env python3
# Copyright 2026 The gojja2 Authors
# SPDX-License-Identifier: Apache-2.0
"""CPython jinja2 oracle.

Renders every case in a corpus with the real `jinja2` package and records the
result as a golden JSON file. Whatever this script produces *is* the spec: when
gojja2 and this disagree, gojja2 is wrong.

Case file format (`*.jj2`), deliberately close to MiniJinja's fixture format so
its corpus can be consumed without rewriting:

    {"json": "header", "__settings__": {...}, "__templates__": {...}}
    ---
    template source

The header is the render context. Two reserved keys are stripped out of it:

    __settings__   environment options (see SETTING_KEYS)
    __templates__  {name: source} of extra templates reachable by
                   include/import/extends

A file with no `\n---\n` separator is treated as all-template, empty context.
"""

from __future__ import annotations

import argparse
import json
import os
import sys
from pathlib import Path

import jinja2

from jinjaoracle import (  # noqa: E402 - sibling module, not a package
    SETTING_KEYS,
    CaseError,
    build_environment,
    describe,
)

ORACLE = {"impl": "cpython-jinja2", "version": jinja2.__version__}

SEPARATOR = "\n---\n"

class Case:
    def __init__(self, path: Path, root: Path):
        self.path = path
        self.rel = path.relative_to(root).as_posix()
        raw = path.read_text(encoding="utf-8")
        header, _, source = raw.partition(SEPARATOR)
        if not _:
            header, source = "{}", raw
        try:
            ctx = json.loads(header) if header.strip() else {}
        except json.JSONDecodeError as exc:
            raise CaseError(f"{self.rel}: bad JSON header: {exc}") from exc
        if not isinstance(ctx, dict):
            raise CaseError(f"{self.rel}: header must be a JSON object")

        self.settings = ctx.pop("__settings__", {}) or {}
        self.templates = ctx.pop("__templates__", {}) or {}
        self.context = ctx
        self.source = source

        unknown = set(self.settings) - SETTING_KEYS
        if unknown:
            raise CaseError(f"{self.rel}: unknown __settings__ keys: {sorted(unknown)}")

    def environment(self) -> jinja2.Environment:
        sources = dict(self.templates)
        sources[self.rel] = self.source
        return build_environment(self.settings, sources, self.rel)

    def render(self) -> dict:
        try:
            template = self.environment().get_template(self.rel)
            output = template.render(self.context)
        except Exception as exc:  # noqa: BLE001 - any exception is a valid result
            return {"ok": False, "error": describe(exc)}
        return {"ok": True, "output": output}


def golden_for(case: Case) -> dict:
    result = {"case": case.rel, "oracle": ORACLE}
    result.update(case.render())
    return result


def dump(obj: dict) -> str:
    return json.dumps(obj, indent=2, ensure_ascii=False) + "\n"


def collect(corpus: Path) -> list[Path]:
    return sorted(p for p in corpus.rglob("*.jj2") if p.is_file())


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--corpus", type=Path, help="directory of *.jj2 cases")
    ap.add_argument("--golden", type=Path, help="directory to write *.json goldens into")
    ap.add_argument("--check", action="store_true", help="fail instead of rewriting goldens")
    ap.add_argument("--template", help="render this source instead of a corpus")
    ap.add_argument("--context", default="{}", help="JSON context for --template")
    args = ap.parse_args()

    if args.template is not None:
        case = Case.__new__(Case)
        case.rel = "<stdin>"
        case.settings, case.templates = {}, {}
        case.context = json.loads(args.context)
        case.source = args.template
        print(dump(golden_for(case)), end="")
        return 0

    if not args.corpus or not args.golden:
        ap.error("--corpus and --golden are required unless --template is used")

    cases = collect(args.corpus)
    if not cases:
        print(f"no *.jj2 cases under {args.corpus}", file=sys.stderr)
        return 0

    stale, written = [], 0
    for path in cases:
        case = Case(path, args.corpus)
        want = dump(golden_for(case))
        out = args.golden / (case.rel[: -len(".jj2")] + ".json")
        if args.check:
            have = out.read_text(encoding="utf-8") if out.exists() else None
            if have != want:
                stale.append(case.rel)
            continue
        out.parent.mkdir(parents=True, exist_ok=True)
        if not out.exists() or out.read_text(encoding="utf-8") != want:
            out.write_text(want, encoding="utf-8")
            written += 1

    if args.check:
        for rel in stale:
            print(f"stale golden: {rel}", file=sys.stderr)
        print(f"{len(cases) - len(stale)}/{len(cases)} goldens up to date", file=sys.stderr)
        return 1 if stale else 0

    # Goldens whose case file has been deleted must not linger.
    live = {case.rel[: -len(".jj2")] + ".json" for case in (Case(p, args.corpus) for p in cases)}
    removed = 0
    for path in sorted(args.golden.rglob("*.json")):
        if path.relative_to(args.golden).as_posix() not in live:
            path.unlink()
            removed += 1
    print(
        f"{len(cases)} cases, {written} golden(s) written, {removed} removed "
        f"(oracle {ORACLE['impl']} {ORACLE['version']})",
        file=sys.stderr,
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
