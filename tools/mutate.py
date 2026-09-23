#!/usr/bin/env python3
# Copyright 2026 The gojja2 Authors
# SPDX-License-Identifier: Apache-2.0
"""Break the analysis and the budget on purpose, a line at a time, and see what notices.

A passing test suite says the code does what the tests ask. It does not say the
tests ask for much. This asks the other question: for each place the analysis
records something -- an effect applied, a dependency drawn, a value emitted --
take it out and find out whether anything fails.

A mutation that survives is a line no test constrains. That is not automatically
a bug: some of them are unreachable in practice, and some are belt-and-braces
over a rule enforced elsewhere. But each survivor is a claim nothing is checking,
and the list of them is the honest measure of how much the suite is worth.

The budget is here for the same reason the analysis is: both are gojja2's own,
and neither has an oracle. Everything a template *renders* is graded against
CPython by five thousand corpus cases and sixty thousand generated templates a
run, which is a far stronger check than mutating it would be -- so the
value-producing code is deliberately not in this list. The allocation bound has
no counterpart in CPython at all, so nothing outside this repository can say
whether it holds.

It is not part of `make check`: it edits the working tree and takes minutes. Run
it with `make mutate` when the analysis or the budget changes.
"""

from __future__ import annotations

import atexit
import re
import shutil
import signal
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
    # ...and the frame rules the tree is built from. Which names a frame owns
    # is decided here, on jinja2's first-mention rule, so a load, a store or a
    # settle that goes unrecorded moves a name into or out of a scope -- which
    # is a different answer to every question asked afterwards.
    (ROOT / "frames.go", r"^\s*v\.(load|store|settle)\(.*\)$"),
]

# Where the render reserves memory or iterations before it takes them. Each
# match becomes one mutation, and the mutation removes the *charge*, not just
# its refusal: leaving the debit in place lets a later charge refuse instead,
# and the question here is whether this allocation is bounded at all. Sixteen of
# the thirty-eight read as caught under the weaker mutation and were not.
CHARGES = re.compile(
    r"^(\s*)if err := ([\w.]+)\.(ChargeBytes|ChargeItems|Step)\((.*)\); err != nil \{$")

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
# A mutation that removes a bound is *expected* to allocate without one, so the
# run that measures it gets a cap of its own -- `f(*range(10000000000))` took the
# whole runner down with it otherwise, and a killed runner reports nothing at
# all. A run the cap stops is the clearest "caught" there is.
CAP = ["systemd-run", "--user", "--scope", "-q",
       "-p", "MemoryMax=1500M", "-p", "MemorySwapMax=0",
       "-p", "RuntimeMaxSec=240", "--"]
CHECK_BUDGET = CAP + ["go", "test", "-count=1", "-timeout", "200s", ".", "./value/"]
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

    The frames.go sites are the same shape and were missing from this, so
    `v.store(entry.Alias)` inside `for _, entry := range n.Names` could not be
    mutated at all: removing it left entry declared-and-not-used, the build
    failed, and the site was recorded as un-makeable rather than measured. A
    site nothing could break and a site never broken print the same, which is
    the reason this function exists.
    """
    if m := re.match(
            r"^(\s*)\w+\.(?:apply|taint|emit|depend|load|store|settle)\((.*)\)$",
            line):
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


# Where the untouched copy of the file currently carrying a mutation is kept.
#
# A mutation that removes a bound is meant to let the render allocate without
# one, and twice that took the runner down with it -- once through the kernel's
# own killer, which no handler in this process gets to answer. So the copy goes
# on disk before the edit and is removed after it is put back, and a run that
# finds one left over restores it before doing anything else. An atexit hook
# alone silently left a mutated file in the working tree.
RESTORE_DIR = ROOT / "tools" / ".mutate-restore"
_mutated: dict[Path, str] = {}


def _hold(path: Path, original: str) -> None:
    RESTORE_DIR.mkdir(exist_ok=True)
    (RESTORE_DIR / backup_name(path)).write_text(original, encoding="utf-8")
    _mutated[path] = original


def _release(path: Path) -> None:
    (RESTORE_DIR / backup_name(path)).unlink(missing_ok=True)
    _mutated.pop(path, None)


def _restore() -> None:
    if not RESTORE_DIR.is_dir():
        return
    for held in RESTORE_DIR.iterdir():
        target = ROOT / held.name.replace("__", "/")
        if target.exists():
            target.write_text(held.read_text(encoding="utf-8"), encoding="utf-8")
            print(f"mutate: restored {target.relative_to(ROOT)} from an "
                  f"interrupted run", file=sys.stderr)
        held.unlink()
    _mutated.clear()


atexit.register(_restore)
signal.signal(signal.SIGTERM, lambda *_: sys.exit(1))
signal.signal(signal.SIGINT, lambda *_: sys.exit(1))


def backup_name(path: Path) -> str:
    """A backup file name unique to the source path, not to its basename."""
    return str(path.relative_to(ROOT)).replace("/", "__")


def caught(checks) -> bool:
    """Does anything fail with the mutation in place?"""
    for cmd in checks:
        r = subprocess.run(cmd, cwd=ROOT, capture_output=True, text=True)
        if r.returncode != 0:
            return True
    return False


def charge_files():
    """Every non-test source outside the generator, in a stable order."""
    return sorted(p for p in ROOT.glob("**/*.go")
                  if "_test" not in p.name and "/tools/" not in str(p)
                  # budget.go is the accounting itself; breaking it there is
                  # one mutation standing for all of them and says nothing
                  # about which call sites are measured.
                  and p.name != "budget.go")


def mutations():
    analysis = (CHECK, CHECK_CONFORMANCE)
    for path, pattern in SITES:
        lines = path.read_text(encoding="utf-8").split("\n")
        rx = re.compile(pattern)
        for i, line in enumerate(lines):
            if rx.match(line):
                yield path, f"{path.name}:{i + 1}", line.strip(), i, None, analysis
    for path, needle, replacement in PREDICATES:
        yield (path, f"{path.name}: {needle.split('(')[0].strip()}", needle, None,
               replacement, analysis)
    for path in charge_files():
        for i, line in enumerate(path.read_text(encoding="utf-8").split("\n")):
            if CHARGES.match(line):
                # The size expression is kept, so nothing it reads goes
                # declared-and-not-used and the mutation stays a mutation of
                # the *charge* rather than of the traversal around it.
                dropped = CHARGES.sub(
                    r"\1if err := func() error { _ = (\4); return nil }(); err != nil {",
                    line)
                yield (path, f"{path.name}:{i + 1}", line.strip(), i, dropped,
                       (CHECK_BUDGET,))


def main(only: str = "") -> int:
    # Before anything else, in case the last run was killed mid-mutation.
    _restore()
    backups = {}
    with tempfile.TemporaryDirectory() as td:
        for path, _pattern in SITES:
            backups[path] = Path(td) / backup_name(path)
            shutil.copyfile(path, backups[path])
        for path, _needle, _r in PREDICATES:
            if path not in backups:
                backups[path] = Path(td) / backup_name(path)
                shutil.copyfile(path, backups[path])

        survivors, total, uncompilable = [], 0, 0
        for path, where, what, line_no, replacement, checks in mutations():
            if only == "budget" and checks != (CHECK_BUDGET,):
                continue
            if only == "analysis" and checks == (CHECK_BUDGET,):
                continue
            total += 1
            if path not in backups:
                backups[path] = Path(td) / backup_name(path)
                shutil.copyfile(path, backups[path])
            original = backups[path].read_text(encoding="utf-8")
            if line_no is None:
                mutated = original.replace(what, replacement, 1)
            elif replacement is not None:
                lines = original.split("\n")
                lines[line_no] = replacement
                mutated = "\n".join(lines)
            else:
                lines = original.split("\n")
                lines[line_no] = "\t\t// MUTATED: " + lines[line_no].strip()
                mutated = "\n".join(lines)
            _hold(path, original)
            path.write_text(mutated, encoding="utf-8")

            build = subprocess.run(["go", "build", "./..."], cwd=ROOT,
                                   capture_output=True, text=True)
            if build.returncode != 0 and line_no is not None and replacement is None:
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
            elif caught(checks):
                status = "caught"
            else:
                status = "SURVIVED"
                survivors.append((where, what))
            print(f"  {status:12} {where}  {what[:76]}", file=sys.stderr)
            path.write_text(original, encoding="utf-8")
            _release(path)

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
    # `make mutate ARGS=--budget` runs the charge sites alone, and
    # `ARGS=--analysis` the rest. Either half takes a few minutes; both take
    # ten, which is long enough that halving it is worth a flag.
    raise SystemExit(main(sys.argv[1].lstrip("-") if len(sys.argv) > 1 else ""))
