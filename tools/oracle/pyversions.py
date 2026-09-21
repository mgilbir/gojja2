#!/usr/bin/env python3
# Copyright 2026 The gojja2 Authors
# SPDX-License-Identifier: Apache-2.0
"""The interpreters gojja2 reproduces, and which of them is the specification.

Every generator that has to ask more than one CPython a question needs the same
two facts: the list of versions, and which one is the default. Those used to be
written out again in each generator, which is the shape that has already cost
this codebase real bugs -- a table recording "only the difference" from another
source, with nothing checking the other source still agrees. Bumping the pin
then means finding every copy, and the one that is missed regenerates against
the wrong interpreter without saying so.

So the pin is read from the Makefile, which is where a contributor changes it,
and the version list lives here once. `value.DefaultPythonVersion` is the same
fact on the Go side; TestDefaultVersionMatchesThePin checks the two agree by
reading the header of a generated file, so a bump that moves one and not the
other fails the build rather than the goldens.
"""

from __future__ import annotations

import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
MAKEFILE = ROOT / "Makefile"

# Oldest first. A version is added here, to value.PythonVersion, and to
# conformance/versions_test.go's table; nothing else enumerates them.
ALL = ["3.11", "3.12", "3.13", "3.14"]


def _make_var(name: str) -> str:
    m = re.search(rf"^{name}\s*:=\s*(\S+)\s*$", MAKEFILE.read_text(encoding="utf-8"), re.M)
    if not m:
        raise SystemExit(f"{name} is not set in {MAKEFILE}")
    return m.group(1)


def default() -> str:
    """The pinned interpreter: the one the committed tables and goldens are for."""
    v = _make_var("PYTHON_VERSION")
    if v not in ALL:
        raise SystemExit(
            f"PYTHON_VERSION is {v}, which is not one of {', '.join(ALL)}. "
            "Add it to tools/oracle/pyversions.py and to value/pyversion.go, "
            "or the generators cannot say what it differs from.")
    return v


def others() -> list[str]:
    """Every version that is not the pin, oldest first."""
    d = default()
    return [v for v in ALL if v != d]


def jinja() -> str:
    return _make_var("JINJA_VERSION")


def markupsafe() -> str:
    return _make_var("MARKUPSAFE_VERSION")


def running() -> str:
    """The minor version of the interpreter executing this, e.g. "3.13".

    Every generated file records which CPython produced it, and it records the
    *minor* version rather than the full one on purpose. What is pinned is the
    minor -- PYTHON_VERSION, value.DefaultPythonVersion and every rule in
    value/pyversion.go are keyed on it -- and the patch releases within it do
    not change an answer; CPython does not reword an error or move a code point
    in a bugfix release.

    Recording the patch made the repository non-reproducible. Two machines a
    patch apart regenerated 2,281 identical-in-substance files, every one of
    them differing in nothing but that line, which is both noise and a place a
    real change could hide. The scheduled oracle workflow found it the first
    time it ran, on a runner one patch ahead.

    The exact build is still worth knowing; `make venv` prints it and every
    generator logs it. It is just not something to write down 2,241 times.
    """
    return ".".join(map(str, sys.version_info[:2]))


def run(version: str, args: list[str], **kw) -> subprocess.CompletedProcess:
    """Run a script under one interpreter, with the pinned jinja2 available.

    `uv run --no-project` builds the environment per call and throws it away,
    so reaching four interpreters needs no four checked-in virtualenvs and
    cannot drift from the pin the way a stale one would.
    """
    cmd = ["uv", "run", "--quiet", "--python", version, "--no-project",
           "--with", f"jinja2=={jinja()}",
           "--with", f"markupsafe=={markupsafe()}", "python", *args]
    return subprocess.run(cmd, cwd=ROOT, text=True, check=True, **kw)


if __name__ == "__main__":
    print(f"pin: {default()}   others: {', '.join(others())}   "
          f"jinja2 {jinja()}   markupsafe {markupsafe()}", file=sys.stderr)
