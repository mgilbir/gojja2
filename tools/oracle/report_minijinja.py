#!/usr/bin/env python3
# Copyright 2026 The gojja2 Authors
# SPDX-License-Identifier: Apache-2.0
"""Report where MiniJinja disagrees with CPython jinja2.

MiniJinja commits a snapshot of what *it* renders for each of its fixtures.
This project already records what CPython jinja2 renders for the same
fixtures, because those goldens are what gojja2 is graded against. Holding the
two side by side costs nothing and produces something neither project has on
its own: a catalogue of concrete, minimal incompatibilities between a widely
used reimplementation and the language's reference.

It grades nobody. MiniJinja diverges from jinja2 deliberately in places and
says so, and a row here is not a bug report. What it is good for is deciding
where gojja2 should be careful: a construct two independent implementations
read differently is a construct worth a corpus case, and several of the rows
below are already covered by one.

Output: testdata/generated/minijinja-divergences.md, gitignored like every
other generated artefact.
"""

from __future__ import annotations

import json
import re
import sys
from pathlib import Path

import provenance

ROOT = Path(__file__).resolve().parents[2]
SNAPS = ROOT / "third_party" / "minijinja" / "minijinja" / "tests" / "snapshots"
GOLDEN = ROOT / "testdata" / "generated" / "minijinja-golden"
KNOWN_FAILURES = ROOT / "testdata" / "known_failures.txt"
OUT = ROOT / "testdata" / "generated" / "minijinja-divergences.md"

# Only the template-rendering snapshots; the rest are of MiniJinja's lexer,
# parser and compiler, which have no counterpart here.
SNAP_PREFIX = "test_templates__vm@"

# MiniJinja's harness writes this marker when a fixture fails, followed by a
# debug rendering of its own error type with a source excerpt. That is a
# picture of MiniJinja's diagnostics, not an answer to compare against a
# CPython exception message -- so a fixture both implementations reject is
# counted and not listed. Whether each *raises* is the comparable part; the
# wording never was.
ERROR_MARKER = "!!!ERROR!!!"


def parse_snapshot(path: Path) -> tuple[str | None, str]:
    """Return (input_file, rendered body) from an insta .snap file.

    The format is a YAML front matter between two `---` lines, then the
    recorded output verbatim.
    """
    text = path.read_text(encoding="utf-8")
    lines = text.split("\n")
    if not lines or lines[0].strip() != "---":
        return None, ""
    end = next((i for i in range(1, len(lines)) if lines[i].strip() == "---"), None)
    if end is None:
        return None, ""
    header, body = lines[1:end], "\n".join(lines[end + 1 :])
    input_file = None
    for line in header:
        m = re.match(r"input_file:\s*(\S+)", line)
        if m:
            input_file = m.group(1)
    return input_file, body


def case_name(input_file: str) -> str:
    """The golden this project records for that fixture.

    import_minijinja.py names a case after the fixture with dots replaced,
    so `minijinja/tests/inputs/adding.txt` becomes `adding_txt`.
    """
    return Path(input_file).name.replace(".", "_")


def gojja2_failures() -> tuple[set[str], set[str]]:
    """Minijinja-corpus cases gojja2 does not match, split by gradability.

    An "ungradable" entry is one CPython does not match either -- it prints a
    generator's address, which differs between two runs of the same
    interpreter. Counting it against gojja2 would be a false finding, so the
    two are kept apart.
    """
    failed: set[str] = set()
    ungradable: set[str] = set()
    if not KNOWN_FAILURES.is_file():
        return failed, ungradable
    for line in KNOWN_FAILURES.read_text(encoding="utf-8").splitlines():
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        name, _, reason = line.partition(" ")
        if not name.startswith("minijinja/"):
            continue
        case = name.removeprefix("minijinja/").removesuffix(".jj2")
        if reason.strip().startswith("ungradable:"):
            ungradable.add(case)
        else:
            failed.add(case)
    return failed, ungradable


def main() -> int:
    if not SNAPS.is_dir():
        print(f"{SNAPS} not found; run `make suites` first", file=sys.stderr)
        return 1
    if not GOLDEN.is_dir():
        print(f"{GOLDEN} not found; run `make import` first", file=sys.stderr)
        return 1

    agree = both_raise = unmatched = 0
    rows = []
    for path in sorted(SNAPS.glob(f"{SNAP_PREFIX}*.snap")):
        input_file, body = parse_snapshot(path)
        if input_file is None:
            continue
        golden_path = GOLDEN / f"{case_name(input_file)}.json"
        if not golden_path.is_file():
            unmatched += 1
            continue
        golden = json.loads(golden_path.read_text(encoding="utf-8"))

        mini_raised = ERROR_MARKER in body
        cpy_raised = not golden["ok"]

        if mini_raised and cpy_raised:
            # Both reject it. The messages differ, and comparing them would
            # fill this report with diagnostics formatting.
            both_raise += 1
            continue
        if mini_raised:
            rows.append((input_file, "raises", golden["output"], "cpython-only"))
            continue
        if cpy_raised:
            detail = f"raises {golden['error']['type']}: {golden['error']['message']}"
            rows.append((input_file, body, detail, "minijinja-only"))
            continue

        # Trailing newlines are an artefact of how insta writes a snapshot
        # rather than of what MiniJinja rendered, so they are not graded.
        if body.rstrip("\n") == golden["output"].rstrip("\n"):
            agree += 1
            continue
        rows.append((input_file, body, golden["output"], "output"))

    OUT.parent.mkdir(parents=True, exist_ok=True)
    failed, ungradable = gojja2_failures()
    diverging = {case_name(r[0]) for r in rows}
    OUT.write_text(
        render(
            agree,
            both_raise,
            rows,
            unmatched,
            sorted(diverging & failed),
            sorted(diverging & ungradable),
        ),
        encoding="utf-8",
    )
    print(
        f"compared {agree + both_raise + len(rows)} MiniJinja snapshots against "
        f"CPython jinja2: {agree} agree, {both_raise} both reject, "
        f"{len(rows)} diverge ({unmatched} with no golden) "
        f"-> {OUT.relative_to(ROOT)}",
        file=sys.stderr,
    )
    return 0


def fence(text: str) -> str:
    return "```\n" + text.rstrip("\n") + "\n```"


KIND_LABEL = {
    "output": "both render, and the text differs",
    "minijinja-only": "MiniJinja renders it; CPython jinja2 raises",
    "cpython-only": "CPython jinja2 renders it; MiniJinja raises",
}


def render(
    agree: int,
    both_raise: int,
    rows: list,
    unmatched: int,
    also_failed: list[str],
    ungradable: list[str],
) -> str:
    total = agree + both_raise + len(rows)
    lines = [
        "# MiniJinja vs CPython jinja2",
        "",
        "Generated by tools/oracle/report_minijinja.py. Not committed.",
        "",
        "MiniJinja ships a snapshot of what it renders for each of its",
        "fixtures; this project records what CPython jinja2 renders for the",
        "same ones. This is the difference.",
        "",
        "It grades nobody: MiniJinja diverges from jinja2 deliberately in",
        "places and documents doing so. It is useful here as a map of where",
        "two independent implementations read the language differently, which",
        "is where a third should be careful.",
        "",
    ]
    lines += provenance.block(
        [("mitsuhiko/minijinja", ROOT / "third_party" / "minijinja", "Apache-2.0")]
    )
    lines += [
        "## Summary",
        "",
        f"- fixtures compared: {total}",
        f"- agree with CPython jinja2: {agree}",
        f"- rejected by both, with different wording: {both_raise}",
        f"- diverge: {len(rows)}",
        "",
        "A fixture both implementations reject is not listed. MiniJinja's",
        "snapshot for one is a debug rendering of its own error type, complete",
        "with a source excerpt, and diffing that against a CPython exception",
        "message would bury the real findings under diagnostics formatting.",
        "That the two agree it is an error is the comparable part.",
    ]
    by_kind: dict[str, int] = {}
    for *_, kind in rows:
        by_kind[kind] = by_kind.get(kind, 0) + 1
    if by_kind:
        lines += ["", "Of those that diverge:", ""]
        lines += [f"- {n}: {KIND_LABEL[k]}" for k, n in sorted(by_kind.items())]
    if unmatched:
        lines.append(f"- skipped, no golden recorded: {unmatched}")

    # The useful cross-check, computed rather than asserted: every fixture
    # below is one where two implementations read the language differently,
    # so it is exactly where a third is most likely to be wrong too.
    lines += ["", "## Where gojja2 lands", ""]
    gradable = len(rows) - len(ungradable)
    if also_failed:
        lines += [
            f"gojja2 also diverges from CPython jinja2 on {len(also_failed)} of "
            f"the {gradable} gradable fixtures here:",
            "",
        ]
        lines += [f"- `{name}`" for name in also_failed]
        lines += ["", "Each is listed in testdata/known_failures.txt with a reason."]
    else:
        lines += [
            f"gojja2 matches CPython jinja2 on all {gradable} gradable fixtures",
            "here.",
            "",
            "That is the point of running this report. These are the constructs",
            "two independent implementations read differently, so they are where",
            "a third is most likely to be wrong -- and the corpus already grades",
            "every one of them.",
        ]
    if ungradable:
        lines += [
            "",
            f"{len(ungradable)} of the {len(rows)} is left out of that count "
            "because CPython does not match it either -- it prints a generator's "
            "address, which differs between two runs of the same interpreter:",
            "",
        ]
        lines += [f"- `{name}`" for name in ungradable]
    lines += ["", "## Divergences", ""]
    if not rows:
        lines.append("None.")
    for input_file, mini, cpy, kind in sorted(rows, key=lambda r: (r[3], r[0])):
        lines += [
            f"### `{Path(input_file).name}`",
            "",
            f"_{KIND_LABEL[kind]}._",
            "",
            "MiniJinja:",
            "",
            fence(mini),
            "",
            "CPython jinja2:",
            "",
            fence(cpy),
            "",
        ]
    return "\n".join(lines) + "\n"


if __name__ == "__main__":
    raise SystemExit(main())
