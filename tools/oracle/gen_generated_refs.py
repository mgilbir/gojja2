#!/usr/bin/env python3
# Copyright 2026 The gojja2 Authors
# SPDX-License-Identifier: Apache-2.0
"""Reference trees and analyses for the imported corpora.

testdata/syntax.jsonl and testdata/nameflow cover the committed corpus, which is
written here and therefore knows what it is testing. The imported corpora are
2,176 templates nobody here wrote -- chat templates real models ship, Jinja's own
suite, cookiecutter projects, minja and llama.cpp -- and they are the ones that
find the constructs a corpus written by the author of the parser does not think
to include.

They are gitignored, so this cannot be a committed fixture; it is written beside
them by `make import` and the test that reads it skips when it is not there. The
check is the same one the committed corpus gets: the engine's tree, its scope
facts and its dataflow must match what is derived from jinja2's own AST.
"""

from __future__ import annotations

import json
import sys
from pathlib import Path

import nameflow
import pyversions
import syntax_emit
from oracle import Case

ROOT = pyversions.ROOT
GENERATED = ROOT / "testdata/generated"
DST = GENERATED / "references.jsonl"

CORPORA = ["chat-templates", "wild", "jinja-harvest", "cookiecutter",
           "minja", "llamacpp", "minijinja"]


def encode(v: dict) -> str:
    s = (("o" if v["output"] else "") + ("f" if v["flow"] else "")
         + ("r" if v["required"] else ""))
    return (s or "-") + ("?" if v["unknown"] else "")


def main() -> int:
    if not GENERATED.is_dir():
        raise SystemExit(f"{GENERATED} does not exist; run `make import` first")

    rows, skipped = [], 0
    for corpus in CORPORA:
        base = GENERATED / corpus
        if not base.is_dir():
            continue
        for path in sorted(base.rglob("*.jj2")):
            try:
                case = Case(path, base)
                env = case.environment()
                env.get_template(case.rel)
                tree = env.parse(case.source, case.rel)
            except Exception:
                skipped += 1
                continue

            def resolver(name, _env=env):
                try:
                    return _env.parse(_env.loader.get_source(_env, name)[0], name)
                except Exception:
                    return None

            variables = {k: encode(v) for k, v in
                         nameflow.analyze(tree, env.globals, resolver).items()}
            rows.append(json.dumps({
                "case": f"{corpus}/{case.rel}",
                "tree": syntax_emit.canonical(tree, env.globals),
                "info": syntax_emit.canonical_info(tree, env.globals),
                "variables": variables,
            }, ensure_ascii=False, separators=(",", ":")))

    DST.write_text("\n".join(rows) + "\n", encoding="utf-8")
    print(f"{len(rows)} reference trees and analyses, {skipped} that do not "
          f"compile, into {DST.relative_to(ROOT)}", file=sys.stderr)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
