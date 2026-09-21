#!/usr/bin/env python3
# Copyright 2026 The gojja2 Authors
# SPDX-License-Identifier: Apache-2.0
"""Record what a CPython other than the pinned one answers, where it differs.

testdata/golden holds the pinned interpreter's answer to every corpus case, and
that is what TestConformance grades. But gojja2 reproduces four interpreters,
and they do not agree: `{{ d[1:2] }}` raises TypeError on 3.11 and KeyError on
3.12, division by zero is worded two ways, and CPython carries its own Unicode
so the digits themselves move. Those differences are what WithPythonVersion
exists to honour, and without a golden per version nothing would notice the
option being wrong.

Storing four full sets would be four copies of the same 2,000 answers, so each
non-pinned version gets only the cases it answers differently, laid over the
base set -- which is what conformance.LoadGoldenOver reads.

"Differently" means the *answer*: every golden also records which interpreter
produced it, and that differs by construction, so the comparison ignores the
oracle block. An override recording the same answer as the base would be a file
that can only rot, and TestVersionOverridesAreAllUsed fails on one.
"""

from __future__ import annotations

import json
import shutil
import sys
import tempfile
from pathlib import Path

import pyversions

ROOT = pyversions.ROOT
CORPUS = ROOT / "testdata/corpus"
BASE = ROOT / "testdata/golden"
ORACLE_SCRIPT = "tools/oracle/oracle.py"


def answer(path: Path) -> str:
    """A golden's answer, without the note of which interpreter gave it."""
    obj = json.loads(path.read_text(encoding="utf-8"))
    obj.pop("oracle", None)
    return json.dumps(obj, sort_keys=True, ensure_ascii=False)


def main() -> int:
    if not BASE.is_dir():
        raise SystemExit(f"{BASE} does not exist; run `make oracle` first")
    base = {p.relative_to(BASE): p for p in BASE.rglob("*.json")}
    if not base:
        raise SystemExit(f"no goldens under {BASE}; run `make oracle` first")

    stale = []
    for v in pyversions.ALL:
        dst = ROOT / f"testdata/golden-{v}"
        if v == pyversions.default():
            # The pin's answers are testdata/golden itself. A leftover
            # directory for it would be a full second copy that nothing reads.
            if dst.exists():
                shutil.rmtree(dst)
                print(f"removed {dst.relative_to(ROOT)}: {v} is now the pin",
                      file=sys.stderr)
            continue

        with tempfile.TemporaryDirectory() as td:
            tmp = Path(td)
            pyversions.run(v, [ORACLE_SCRIPT, "--corpus", str(CORPUS),
                               "--golden", str(tmp)])
            produced = {p.relative_to(tmp) for p in tmp.rglob("*.json")}
            if produced != set(base):
                missing = sorted(set(base) - produced)
                extra = sorted(produced - set(base))
                raise SystemExit(
                    f"CPython {v} answered a different set of cases than the base "
                    f"({len(missing)} missing, {len(extra)} extra); the base set is "
                    "stale, so run `make oracle` first")

            kept = 0
            if dst.exists():
                shutil.rmtree(dst)
            for rel in sorted(produced):
                if answer(tmp / rel) == answer(base[rel]):
                    continue
                out = dst / rel
                out.parent.mkdir(parents=True, exist_ok=True)
                shutil.copyfile(tmp / rel, out)
                kept += 1

        if kept == 0:
            # Every case agrees with the pin, so there is nothing to lay over.
            # Say so loudly: the version's row in conformance/versions_test.go
            # has to lose its override directory or the test fails on the gap.
            stale.append(v)
            print(f"CPython {v}: no case differs from the pin; drop its override "
                  "entry in conformance/versions_test.go", file=sys.stderr)
        else:
            print(f"CPython {v}: {kept} of {len(base)} cases differ", file=sys.stderr)

    return 1 if stale else 0


if __name__ == "__main__":
    raise SystemExit(main())
