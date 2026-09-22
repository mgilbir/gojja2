#!/usr/bin/env python3
# Copyright 2026 The gojja2 Authors
# SPDX-License-Identifier: Apache-2.0
"""Break the analysis on purpose, one line at a time, and see what notices.

A passing test suite says the code does what the tests ask. It does not say the
tests ask for much. This asks the other question: for each place the analysis
records something -- an effect applied, a dependency drawn, a value emitted --
take it out and find out whether anything fails.

A mutation that survives is a line no test constrains. That is not automatically
a bug: some of them are unreachable in practice, and some are belt-and-braces
over a rule enforced elsewhere. But each survivor is a claim nothing is checking,
and the list of them is the honest measure of how much the suite is worth.

It is not part of `make check`: it edits the working tree and takes minutes. Run
it with `make mutate` when the analysis changes.
"""

from __future__ import annotations

import re
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]

# Where the analysis records something. Each match becomes one mutation.
SITES = [
    (ROOT / "dataflow/walk.go", r"^\s*a\.(apply|taint|emit|depend)\(.*\)$"),
    (ROOT / "dataflow/analyze.go", r"^\s*a\.(apply|taint|emit|depend)\(.*\)$"),
    (ROOT / "dataflow/namespace.go", r"^\s*a\.(apply|taint|emit|depend)\(.*\)$"),
    # The tree the analysis runs on is built here, and every answer rests on
    # it: a Def, a Use or a Scope that goes unrecorded is a name the analysis
    # cannot see, which is indistinguishable from a name that does nothing.
    (ROOT / "syntax_build.go", r"^\s*b\.info\.(Defs|Uses|Scopes|Context)\[.*\] = .*$"),
]

# Rules that are a single predicate rather than a call, mutated by inverting
# them: the walk still runs, it just believes the wrong thing.
PREDICATES = [
    (ROOT / "dataflow/walk.go", "func canRaise(k syntax.Kind) bool {",
     "func canRaise(k syntax.Kind) bool {\n\treturn false\n\t//"),
    (ROOT / "dataflow/walk.go", "func canFailIn(n *syntax.Node) bool {",
     "func canFailIn(n *syntax.Node) bool {\n\treturn false\n\t//"),
    (ROOT / "dataflow/analyze.go", "func (a *analyzer) seedAliases() {",
     "func (a *analyzer) seedAliases() {\n\tif true {\n\t\treturn\n\t}"),
]

# Fast but representative: the unit tables, the two corpus differentials and the
# render checks. A soak would be better and would take an hour per mutation.
CHECK = ["go", "test", "-count=1", "./dataflow/", "./syntax/"]
CHECK_CONFORMANCE = [
    "go", "test", "-count=1", "./conformance/", "-run",
    "TestDataflowMatchesTheReference|TestSyntaxMatchesTheReference|"
    "TestNegativesSurviveRendering|TestGeneratedCorporaAgree",
]


def split_args(text: str) -> list[str]:
    """Split a call's arguments on the commas that are not inside anything."""
    out, depth, start, quote = [], 0, 0, ""
    for i, ch in enumerate(text):
        if quote:
            if ch == quote and text[i - 1] != "\\":
                quote = ""
            continue
        if ch in "'\"`":
            quote = ch
        elif ch in "([{":
            depth += 1
        elif ch in ")]}":
            depth -= 1
        elif ch == "," and depth == 0:
            out.append(text[start:i].strip())
            start = i + 1
    tail = text[start:].strip()
    if tail:
        out.append(tail)
    return out


def drop_the_call(line: str):
    """Rewrite `a.emit(expr)` as `_ = expr`, keeping the arguments evaluated.

    The traversal a site's arguments perform is not the thing being mutated --
    the recording is -- and for a site that is the only reader of a loop
    variable, commenting the whole line out removes both and does not compile.
    """
    if m := re.match(r"^(\s*)a\.(?:apply|taint|emit|depend)\((.*)\)$", line):
        indent, args = m.group(1), split_args(m.group(2))
        if not args:
            return None
        blanks = ", ".join("_" for _ in args)
        return f"{indent}{blanks} = {', '.join(args)} // MUTATED: recording dropped"
    # An assignment records by landing somewhere; keep the value, drop the
    # landing. `b.info.Uses[out] = b.resolve(n.Name)` still resolves, and the
    # tree simply does not learn the answer.
    if m := re.match(r"^(\s*)[\w.\[\]()]+ = (.*)$", line):
        return f"{m.group(1)}_ = {m.group(2)} // MUTATED: recording dropped"
    return None


def caught() -> bool:
    """Does anything fail with the mutation in place?"""
    for cmd in (CHECK, CHECK_CONFORMANCE):
        r = subprocess.run(cmd, cwd=ROOT, capture_output=True, text=True)
        if r.returncode != 0:
            return True
    return False


def mutations():
    for path, pattern in SITES:
        lines = path.read_text(encoding="utf-8").split("\n")
        rx = re.compile(pattern)
        for i, line in enumerate(lines):
            if rx.match(line):
                yield path, f"{path.name}:{i + 1}", line.strip(), i, None
    for path, needle, replacement in PREDICATES:
        yield path, f"{path.name}: {needle.split('(')[0].strip()}", needle, None, replacement


def main() -> int:
    backups = {}
    with tempfile.TemporaryDirectory() as td:
        for path, _pattern in SITES:
            backups[path] = Path(td) / path.name
            shutil.copyfile(path, backups[path])
        for path, _needle, _r in PREDICATES:
            if path not in backups:
                backups[path] = Path(td) / path.name
                shutil.copyfile(path, backups[path])

        survivors, total, uncompilable = [], 0, 0
        for path, where, what, line_no, replacement in mutations():
            total += 1
            original = backups[path].read_text(encoding="utf-8")
            if line_no is None:
                mutated = original.replace(what, replacement, 1)
            else:
                lines = original.split("\n")
                lines[line_no] = "\t\t// MUTATED: " + lines[line_no].strip()
                mutated = "\n".join(lines)
            path.write_text(mutated, encoding="utf-8")

            build = subprocess.run(["go", "build", "./..."], cwd=ROOT,
                                   capture_output=True, text=True)
            if build.returncode != 0 and line_no is not None:
                # Commenting the line out left something declared and not
                # used -- the site is the only reader of a loop variable, so
                # removing it removes the traversal too. Drop the *recording*
                # and keep the arguments, which is the mutation this meant to
                # make in the first place and a harder one to catch.
                kept = drop_the_call(original.split("\n")[line_no])
                if kept is not None:
                    lines = original.split("\n")
                    lines[line_no] = kept
                    path.write_text("\n".join(lines), encoding="utf-8")
                    build = subprocess.run(["go", "build", "./..."], cwd=ROOT,
                                           capture_output=True, text=True)
            if build.returncode != 0:
                # Not a mutation this can make at all. Counted, not a
                # survivor, and reported apart so the headline cannot read as
                # though every site was exercised.
                status = "uncompilable"
                uncompilable += 1
            elif caught():
                status = "caught"
            else:
                status = "SURVIVED"
                survivors.append((where, what))
            print(f"  {status:12} {where}  {what[:76]}", file=sys.stderr)
            path.write_text(original, encoding="utf-8")

        subprocess.run(["go", "build", "./..."], cwd=ROOT, capture_output=True)

    exercised = total - uncompilable
    print(f"\n{total} mutations, {exercised} exercised, {len(survivors)} survived",
          file=sys.stderr)
    if uncompilable:
        print(f"  {uncompilable} could not be made at all -- neither removing the "
              f"line nor dropping the call compiles, so nothing was measured there",
              file=sys.stderr)
    for where, what in survivors:
        print(f"  survived: {where}  {what}", file=sys.stderr)
    return 1 if survivors else 0


if __name__ == "__main__":
    raise SystemExit(main())
