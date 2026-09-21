#!/usr/bin/env python3
# Copyright 2026 The gojja2 Authors
# SPDX-License-Identifier: Apache-2.0
"""Record what each corpus template does with the caller's variables.

nameflow.py answers, for one template, which of the caller's variables can
reach the output, which only steer it, and which the analysis could not follow.
This writes those answers down for every committed case, so the engine's own
implementation -- a different analysis over a different tree -- can be held to
them.

That is the whole point of having two. The answer has to be a property of the
template, not of either AST's shape, and the only way to know it is to derive it
twice and require the results to match. A single implementation would be right
by definition.

The encoding is terse because there are thousands of these: "o" the value can be
printed, "f" it can change the output without being printed, "of" both, "-"
neither, and a trailing "?" for an answer with no reliable negative -- a route
the analysis could not follow, so the variable may reach the output anyway.
"""

from __future__ import annotations

import json
import sys
from pathlib import Path

import nameflow
import pyversions
from oracle import Case

ROOT = pyversions.ROOT
CORPUS = ROOT / "testdata/corpus"
DST = ROOT / "testdata/nameflow"


def encode(v: dict) -> str:
    s = ("o" if v["output"] else "") + ("f" if v["flow"] else "")
    return (s or "-") + ("?" if v["unknown"] else "")


def main() -> int:
    if not CORPUS.is_dir():
        raise SystemExit(f"{CORPUS} does not exist; run `make oracle` first")
    cases = sorted(CORPUS.rglob("*.jj2"))
    written = skipped = 0
    keep = set()
    for path in cases:
        # The case's own settings decide the environment -- which extensions
        # are on, what the delimiters are -- so this builds it exactly as the
        # oracle does rather than assuming a default. A template using
        # {% break %} parses only where the case asked for loopcontrols, and
        # the engine's side has to be looking at the same thing.
        case = Case(path, CORPUS)
        rel = case.rel
        try:
            env = case.environment()
            # Compiled, not merely parsed: jinja2 defers several refusals to
            # code generation -- a block defined twice, `{% extends %}` below
            # the top level, an unknown filter -- and the engine refuses them
            # too. A case neither can compile has nothing to analyse, and one
            # this analysed but the engine rejected would fail as a mismatch
            # for a reason that is not about the analysis.
            env.get_template(case.rel)
            tree = env.parse(case.source, case.rel)
        except Exception:
            skipped += 1
            continue
        # The case's own templates are what its references resolve to, so the
        # analysis follows them exactly as the engine's does.
        def resolver(name, _env=env):
            try:
                return _env.parse(_env.loader.get_source(_env, name)[0], name)
            except Exception:
                return None

        result = nameflow.analyze(tree, env.globals, resolver)
        out = DST / (rel[: -len(".jj2")] + ".json")
        keep.add(out)
        body = json.dumps(
            {"case": rel, "variables": {k: encode(v) for k, v in result.items()}},
            indent=2, ensure_ascii=False, sort_keys=True) + "\n"
        out.parent.mkdir(parents=True, exist_ok=True)
        if not out.exists() or out.read_text(encoding="utf-8") != body:
            out.write_text(body, encoding="utf-8")
        written += 1

    removed = 0
    if DST.is_dir():
        for stale in sorted(DST.rglob("*.json")):
            if stale not in keep:
                stale.unlink()
                removed += 1
        for d in sorted(DST.rglob("*"), reverse=True):
            if d.is_dir() and not any(d.iterdir()):
                d.rmdir()

    print(f"{written} analysed, {skipped} unparseable, {removed} stale removed",
          file=sys.stderr)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
