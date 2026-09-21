#!/usr/bin/env python3
# Copyright 2026 The gojja2 Authors
# SPDX-License-Identifier: Apache-2.0
"""Report CPython releases gojja2 does not yet account for, and what they change.

gojja2 names the interpreters it reproduces, and the list is finite: 3.11 to
3.14 today. A new stable CPython is therefore an outstanding task, and it is one
nobody is told about -- the tests keep passing, because they only ever ask the
four versions already written down.

This is what tells them. It asks uv which CPythons exist, compares that against
tools/oracle/pyversions.py, and for anything new does the work of finding out
what it would take:

  * renders the whole committed corpus under the new interpreter and reports
    which cases answer differently from the pin, grouped by whether it is the
    behaviour or only the wording;
  * asks it about every code point and reports how far its Unicode has moved;
  * names the method-arity entries it words differently.

The output is Markdown, for a job summary or an issue. Nothing is written to
the repository: adding a version is a decision, and this only supplies the
evidence for it.

A release that *disappears* is reported too. The matrix targets reach every
version through `uv run --python`, so one uv can no longer provide is a
generator that will fail the next time somebody runs it.
"""

from __future__ import annotations

import argparse
import json
import subprocess
import sys
import tempfile
from pathlib import Path

import pyversions

ROOT = pyversions.ROOT
CORPUS = ROOT / "testdata/corpus"
GOLDEN = ROOT / "testdata/golden"
DUMPER = "tools/oracle/dump_unicode.py"
ORACLE = "tools/oracle/oracle.py"
METHODS = "tools/oracle/gen_methods.py"

# Below this, a release is old enough that gojja2 has decided not to reproduce
# it; the list is a floor, not an accident. Only newer unknowns are reported.
OLDEST = pyversions.ALL[0]


def available() -> list[str]:
    """The stable CPython minor versions uv can provide, oldest first."""
    proc = subprocess.run(
        ["uv", "python", "list", "--all-versions", "--output-format", "json"],
        capture_output=True, text=True, check=True)
    seen = set()
    for row in json.loads(proc.stdout):
        if row.get("implementation") != "cpython":
            continue
        # A prerelease spells itself 3.15.0b4; only finals count, because a
        # beta's error wordings are not a specification anybody should pin to.
        if not row["version"].replace(".", "").isdigit():
            continue
        p = row["version_parts"]
        seen.add((p["major"], p["minor"]))
    return [f"{a}.{b}" for a, b in sorted(seen)]


def newer(a: str, b: str) -> bool:
    return tuple(int(x) for x in a.split(".")) > tuple(int(x) for x in b.split("."))


def answer(path: Path) -> str:
    obj = json.loads(path.read_text(encoding="utf-8"))
    obj.pop("oracle", None)
    return json.dumps(obj, sort_keys=True, ensure_ascii=False)


def grade_corpus(version: str) -> dict:
    """Render every committed case under one interpreter and diff against the pin."""
    out = {"differs": [], "behaviour": 0, "wording": 0, "failed": None}
    with tempfile.TemporaryDirectory() as td:
        tmp = Path(td)
        try:
            pyversions.run(version, [ORACLE, "--corpus", str(CORPUS),
                                     "--golden", str(tmp)], capture_output=True)
        except subprocess.CalledProcessError as exc:
            out["failed"] = (exc.stderr or "")[-2000:]
            return out
        for p in sorted(tmp.rglob("*.json")):
            rel = p.relative_to(tmp)
            base = GOLDEN / rel
            if not base.exists():
                continue
            if answer(p) == answer(base):
                continue
            a = json.loads(p.read_text(encoding="utf-8"))
            b = json.loads(base.read_text(encoding="utf-8"))
            same_shape = (a.get("ok") == b.get("ok")
                          and (a.get("error") or {}).get("type") == (b.get("error") or {}).get("type")
                          and a.get("output") == b.get("output"))
            kind = "wording" if same_shape else "behaviour"
            out[kind] += 1
            out["differs"].append((str(rel)[:-5], kind,
                                   (b.get("error") or {}).get("message", "") or "<output>",
                                   (a.get("error") or {}).get("message", "") or "<output>"))
    return out


def grade_unicode(version: str) -> dict:
    """How far this interpreter's Unicode has moved from the pin's."""
    def dump(v):
        proc = pyversions.run(v, [DUMPER], capture_output=True)
        rows = {}
        for line in proc.stdout.splitlines():
            parts = line.split("\t")
            if len(parts) == 7:
                rows[int(parts[0])] = tuple(parts[1:])
        return rows
    pin = dump(pyversions.default())
    new = dump(version)
    moved = [cp for cp in pin if new.get(cp) != pin[cp]]
    return {"moved": len(moved), "first": moved[:8]}


def grade_methods(version: str) -> int:
    """How many built-in method wordings this interpreter says differently."""
    import re
    row = re.compile(r'^\t"([\w.]+)":\s+(\{.*\}),$')

    def rows(path: Path) -> dict:
        return {m.group(1): m.group(2)
                for m in (row.match(l) for l in path.read_text(encoding="utf-8").splitlines())
                if m}
    with tempfile.TemporaryDirectory() as td:
        out = Path(td) / "method_arity.go"
        pyversions.run(version, [METHODS, "--out", str(out)], capture_output=True)
        base = rows(ROOT / "method_arity.go")
        return sum(1 for k, v in rows(out).items() if base.get(k) != v)


def report(version: str) -> str:
    lines = [f"## CPython {version}", ""]
    corpus = grade_corpus(version)
    if corpus["failed"] is not None:
        lines += ["The corpus could not be rendered under it:", "",
                  "```", corpus["failed"].strip(), "```", ""]
        return "\n".join(lines)

    uni = grade_unicode(version)
    methods = grade_methods(version)
    total = corpus["behaviour"] + corpus["wording"]
    lines += [
        f"- **{total}** of the committed corpus cases answer differently from the "
        f"pin ({pyversions.default()}) — {corpus['behaviour']} behaviour, "
        f"{corpus['wording']} wording only",
        f"- **{uni['moved']}** code points have different Unicode answers",
        f"- **{methods}** built-in method wordings differ",
        "",
    ]
    if corpus["differs"]:
        lines += ["<details><summary>Cases that differ</summary>", "",
                  "| case | kind | pin says | it says |", "|---|---|---|---|"]
        for name, kind, was, now in corpus["differs"][:60]:
            lines.append(f"| `{name}` | {kind} | {was} | {now} |")
        if len(corpus["differs"]) > 60:
            lines.append(f"| … and {len(corpus['differs']) - 60} more | | | |")
        lines += ["", "</details>", ""]
    lines += [
        "To adopt it: add the version to `tools/oracle/pyversions.py` and to",
        "`value/pyversion.go`, name each difference above as a rule with the",
        "corpus case that grades it, then `make oracle`. To make it the pin,",
        "move `PYTHON_VERSION` and `DefaultPythonVersion` together.",
        "",
    ]
    return "\n".join(lines)


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--grade", action="store_true",
                    help="render the corpus under each unknown version (slow)")
    ap.add_argument("--out", type=Path, help="write the Markdown report here")
    args = ap.parse_args()

    known = pyversions.ALL
    have = available()
    missing = [v for v in known if v not in have]
    unknown = [v for v in have if v not in known and newer(v, OLDEST)]

    lines = ["# CPython releases", "",
             f"gojja2 reproduces {', '.join(known)}; the pin is "
             f"**{pyversions.default()}**.", ""]
    problems = []

    if missing:
        problems.append("missing")
        lines += [f"## uv can no longer provide {', '.join(missing)}", "",
                  "Every matrix target reaches its interpreters through "
                  "`uv run --python`, so `make oracle` will fail on the next run.",
                  ""]
    if unknown:
        problems.append("new")
        lines += [f"## {len(unknown)} release(s) gojja2 does not know: "
                  f"{', '.join(unknown)}", ""]
        for v in unknown:
            lines.append(report(v) if args.grade else
                         f"## CPython {v}\n\nRun with `--grade` for what it changes.\n")
    if not problems:
        lines += ["Nothing to do: every version gojja2 names is available, and "
                  "there is no newer stable release.", ""]

    text = "\n".join(lines)
    if args.out:
        args.out.write_text(text, encoding="utf-8")
    print(text)
    return 1 if problems else 0


if __name__ == "__main__":
    raise SystemExit(main())
