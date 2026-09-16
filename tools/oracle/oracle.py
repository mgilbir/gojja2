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

ORACLE = {"impl": "cpython-jinja2", "version": jinja2.__version__}

SEPARATOR = "\n---\n"

# Environment options a case may set. Anything else in __settings__ is an error,
# so a typo in a fixture fails loudly instead of being silently ignored.
SETTING_KEYS = {
    "block_start_string",
    "block_end_string",
    "variable_start_string",
    "variable_end_string",
    "comment_start_string",
    "comment_end_string",
    "line_statement_prefix",
    "line_comment_prefix",
    "trim_blocks",
    "lstrip_blocks",
    "newline_sequence",
    "keep_trailing_newline",
    "autoescape",
    "optimized",
    "undefined",
}

UNDEFINED_KINDS = {
    "default": jinja2.Undefined,
    "strict": jinja2.StrictUndefined,
    "chainable": jinja2.ChainableUndefined,
    "debug": jinja2.DebugUndefined,
}


class CaseError(Exception):
    """A malformed fixture (as opposed to a template that fails to render)."""


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
        opts = dict(self.settings)
        undefined = opts.pop("undefined", "default")
        if undefined not in UNDEFINED_KINDS:
            raise CaseError(f"{self.rel}: unknown undefined kind {undefined!r}")
        sources = dict(self.templates)
        sources[self.rel] = self.source
        return jinja2.Environment(
            loader=jinja2.DictLoader(sources),
            undefined=UNDEFINED_KINDS[undefined],
            **opts,
        )

    def render(self) -> dict:
        try:
            env = self.environment()
            template = env.get_template(self.rel)
            output = template.render(self.context)
        except Exception as exc:  # noqa: BLE001 - any exception is a valid result
            return {"ok": False, "error": describe(exc)}
        return {"ok": True, "output": output}


def describe(exc: BaseException) -> dict:
    """Serialise an exception the way a conformance check needs to see it.

    `type` is the assertion that matters and is compared strictly; `message`
    and `lineno` are recorded so divergence is visible but can be graded more
    loosely while error strings are still being brought into line.
    """
    info = {"type": type(exc).__name__, "message": str(exc)}
    lineno = getattr(exc, "lineno", None)
    if lineno is not None:
        info["lineno"] = lineno
    name = getattr(exc, "name", None)
    if name is not None:
        info["name"] = name
    return info


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
