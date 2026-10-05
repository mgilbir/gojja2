#!/usr/bin/env python3
# Copyright 2026 The gojja2 Authors
# SPDX-License-Identifier: Apache-2.0
"""Count the error messages no corpus case has ever produced.

Every claim gojja2 makes about CPython is graded by rendering a corpus case and
comparing the answer. An error message that no case produces is therefore not
evidence of anything: it has never been compared to anything. Worse, it reads as
*agreement* in every column of the version matrix, because the matrix compares
the cases the corpus holds and nothing else -- so a message that is wrong on one
interpreter and right on another looks identical to one that is right on all of
them.

This measures that gap. It runs the corpus under coverage, intersects the blocks
that never executed with the lines that build an error, and prints what is left.

The first run of it found six bugs in the first seventy-eight shapes probed:
bytes.hex accepted a separator of any length and rejected a bytes one, |xmlattr
had its message inside out, nine numeric methods had no arity check at all,
bytes.fromhex skipped one whitespace character instead of six and reported the
wrong position, a dict-update element message had changed in 3.14 unnoticed, and
selectattr named itself in rejectattr's error. The next hundred and eighty-seven
shapes found nothing, which is the other half of the result.

A site here is one of three things, and telling them apart is the work:

  - unreachable from a template -- a configuration error, a budget refusal, a Go
    bridge complaint. Graded by Go tests, and not a statement about CPython.
  - an internal invariant -- "unknown operator", "cannot execute". gojja2's own
    parser cannot build the node, though the tree type is public so a caller can.
  - reachable, and simply never probed. This is the interesting kind, and the
    only way to tell is to read the message and write the template that reaches
    it.

It is not part of `make check`: it needs a coverage run of the whole corpus.
Run it with `make ungraded` after adding error messages.
"""

from __future__ import annotations

import collections
import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]

# The packages whose messages a template can observe. The others -- the loader,
# the parser's own diagnostics -- are graded elsewhere or are not CPython's to
# compare against.
PACKAGES = "github.com/mgilbir/gojja2,github.com/mgilbir/gojja2/value"

# The corpus, under every interpreter, because a message may exist only on one.
TESTS = "TestConformance$|TestEveryPythonVersion"

BUILDS_AN_ERROR = re.compile(r"errs\.New\(")


def coverage(profile: Path) -> None:
    cmd = [
        "go", "test", "-covermode=count", "-coverpkg=" + PACKAGES,
        "-coverprofile=" + str(profile), "-run", TESTS, "./conformance/",
    ]
    r = subprocess.run(cmd, cwd=ROOT, capture_output=True, text=True)
    if r.returncode != 0:
        sys.stderr.write(r.stdout[-4000:] + r.stderr[-4000:])
        raise SystemExit("the corpus does not pass; fix that before measuring")


def blocks_by_file(profile: Path) -> dict[str, list[tuple[int, int, int]]]:
    out: dict[str, list[tuple[int, int, int]]] = collections.defaultdict(list)
    for line in profile.read_text(encoding="utf-8").splitlines():
        line = line.strip()
        if not line or line.startswith("mode:"):
            continue
        loc, count = line.rsplit(" ", 1)
        loc, _statements = loc.rsplit(" ", 1)
        path, span = loc.split(":")
        start, end = span.split(",")
        out[path.split("gojja2/", 1)[-1]].append(
            (int(start.split(".")[0]), int(end.split(".")[0]), int(count)))
    return out


def count_for(blocks: list[tuple[int, int, int]], lineno: int) -> int | None:
    """The count of the smallest block containing the line, or None for no block.

    Smallest, because coverage blocks nest: an `if` inside a function is its own
    block, and the function's own count would say the line ran when it did not.
    """
    best, best_size = None, None
    for start, end, count in blocks:
        if start <= lineno <= end and (best_size is None or end - start < best_size):
            best, best_size = count, end - start
    return best


def message_at(src: list[str], i: int) -> str:
    """The site's text, carried far enough to include the format string."""
    text, j = src[i - 1].strip(), i
    while '"' not in text and j < len(src):
        j += 1
        text = src[j - 1].strip()
    return text


def main() -> int:
    profile = ROOT / "ungraded.cov"
    try:
        coverage(profile)
        blocks = blocks_by_file(profile)
        total, ungraded = 0, []
        for rel in sorted(blocks):
            path = ROOT / rel
            if not path.exists():
                continue
            src = path.read_text(encoding="utf-8").splitlines()
            for i, text in enumerate(src, 1):
                if not BUILDS_AN_ERROR.search(text):
                    continue
                total += 1
                if count_for(blocks[rel], i) == 0:
                    ungraded.append((rel, i, message_at(src, i)))
    finally:
        profile.unlink(missing_ok=True)

    print(f"{len(ungraded)} of {total} error sites no corpus case reaches")
    print()
    for name, n in collections.Counter(r[0] for r in ungraded).most_common():
        print(f"  {n:3d}  {name}")
    if "-v" in sys.argv or "--verbose" in sys.argv:
        print()
        for rel, i, text in ungraded:
            print(f"{rel}:{i}\t{text[:100]}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
