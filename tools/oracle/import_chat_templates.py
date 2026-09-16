#!/usr/bin/env python3
# Copyright 2026 The gojja2 Authors
# SPDX-License-Identifier: Apache-2.0
"""Import LLM chat templates as conformance cases.

Chat templates are the most discriminating Jinja corpus available: they are
written by many different people, shipped with real models, and the ecosystem
has already hit the reimplementation-divergence problem that this project
exists to avoid. They lean hard on whitespace control, `loop.last`, `.get()`,
`selectattr`, `tojson` and `namespace`.

Two sources, both cloned by `make suites` into the gitignored third_party/:

  - chujiezheng/chat_templates, a curated set, one per model family;
  - llama.cpp's models/templates, a vendored set of real model templates.

Neither ships an expected output that is usable here, and neither is asked for
one: every case is re-rendered through CPython jinja2 to get its golden.

Each template is paired with every context variant in chatctx.py, so coverage
comes from conversation shape rather than from template count -- the templates
themselves are heavily copy-pasted between repos and are deduplicated by
content hash.

Every case carries `"__profile__": "transformers"`, because that is the
environment these templates are written against; see profiles.py.
"""

from __future__ import annotations

import hashlib
import json
import re
import shutil
import sys
from pathlib import Path

import chatctx
import provenance

ROOT = Path(__file__).resolve().parents[2]
DST = ROOT / "testdata" / "generated" / "chat-templates"

SEPARATOR = "\n---\n"

SOURCES = [
    ("curated", ROOT / "third_party" / "chat_templates" / "chat_templates"),
    ("models", ROOT / "third_party" / "llamacpp" / "models" / "templates"),
]

# `{% generation %}` comes from transformers' AssistantTracker extension, which
# is not implemented on either side. A template using it would be graded
# against an environment that is not the one it needs.
UNSUPPORTED = re.compile(r"\{%-?\s*(end)?generation\b")

# Anything reading a real clock or a random source cannot have a stable golden.
# strftime_now is not on this list: the profile freezes it.
NONDETERMINISTIC = re.compile(r"\bnow\(\)|\brandom\b|\blipsum\b")


def normalised(source: str) -> str:
    """Key for deduplication: whitespace-insensitive template text.

    Model repos copy each other's templates and reindent them. Two templates
    that differ only in indentation produce the same cases, so only the first
    is kept.
    """
    return re.sub(r"\s+", " ", source).strip()


def collect_templates() -> tuple[dict[str, str], list[tuple[str, str]]]:
    """Return {case_stem: source} and the list of (name, reason) dropped."""
    kept: dict[str, str] = {}
    seen: dict[str, str] = {}
    dropped: list[tuple[str, str]] = []

    for tier, directory in SOURCES:
        if not directory.is_dir():
            print(f"{directory} not found; run `make suites` first", file=sys.stderr)
            continue
        for path in sorted(directory.glob("*.jinja")):
            name = f"{tier}_{path.stem}"
            source = path.read_text(encoding="utf-8")
            if UNSUPPORTED.search(source):
                dropped.append((name, "uses {% generation %} (AssistantTracker)"))
                continue
            if NONDETERMINISTIC.search(source):
                dropped.append((name, "reads the clock or a random source"))
                continue
            key = normalised(source)
            if key in seen:
                dropped.append((name, f"duplicate of {seen[key]}"))
                continue
            seen[key] = name
            kept[safe_stem(name)] = source
    return kept, dropped


def safe_stem(name: str) -> str:
    """A filename that survives every filesystem, with collisions kept apart."""
    cleaned = re.sub(r"[^A-Za-z0-9_.-]", "_", name)
    if cleaned != name:
        cleaned += "_" + hashlib.sha256(name.encode()).hexdigest()[:6]
    return cleaned


def main() -> int:
    templates, dropped = collect_templates()
    if not templates:
        print("no chat templates found; run `make suites` first", file=sys.stderr)
        return 1

    if DST.exists():
        shutil.rmtree(DST)
    DST.mkdir(parents=True)

    count = 0
    for stem, source in templates.items():
        for variant, context in chatctx.VARIANTS.items():
            header = {"__profile__": "transformers"}
            header.update(context)
            body = json.dumps(header, ensure_ascii=False, indent=2) + SEPARATOR + source
            (DST / f"{stem}__{variant}.jj2").write_text(body, encoding="utf-8")
            count += 1

    # The dropped list is not noise: it is the record of what this corpus does
    # not cover and why, which is the only way a gap stays visible.
    report = DST / "SOURCES.md"
    report.write_text(build_sources_md(templates, dropped), encoding="utf-8")

    print(
        f"imported {len(templates)} chat templates x {len(chatctx.VARIANTS)} contexts "
        f"= {count} cases into {DST.relative_to(ROOT)} "
        f"({len(dropped)} template(s) dropped)",
        file=sys.stderr,
    )
    return 0


def build_sources_md(templates: dict, dropped: list) -> str:
    lines = [
        "# chat-templates corpus",
        "",
        "Generated by tools/oracle/import_chat_templates.py. Not vendored, not",
        "committed: `make import` rebuilds it from the clones in third_party/.",
        "",
        "## Sources",
        "",
        "| tier | repo | license |",
        "| --- | --- | --- |",
        "| curated | github.com/chujiezheng/chat_templates | see repo LICENSE |",
        "| models | github.com/ggml-org/llama.cpp (models/templates) | MIT |",
        "",
    ]
    lines += provenance.block([
        ("chujiezheng/chat_templates", ROOT / "third_party" / "chat_templates", "see repo LICENSE"),
        ("ggml-org/llama.cpp", ROOT / "third_party" / "llamacpp", "MIT"),
    ])
    lines += [
        "Individual model templates carry their own upstream licenses, which",
        "range from Apache-2.0 to bespoke terms. Nothing here is redistributed:",
        "the directory is gitignored and rebuilt locally from the clones.",
        "",
        "## Environment",
        "",
        "Every case renders under `\"__profile__\": \"transformers\"`, which",
        "mirrors transformers/utils/chat_template_utils.py: trim_blocks,",
        "lstrip_blocks, the loopcontrols extension, a `tojson` override that",
        "does not sort keys or escape HTML, and the `raise_exception` and",
        "`strftime_now` globals. `strftime_now` is frozen to a fixed instant so",
        "goldens do not expire; see tools/oracle/profiles.py.",
        "",
        f"## Templates kept ({len(templates)})",
        "",
    ]
    lines += [f"- `{stem}`" for stem in sorted(templates)]
    lines += ["", f"## Templates dropped ({len(dropped)})", ""]
    if dropped:
        lines += [f"- `{name}` -- {reason}" for name, reason in sorted(dropped)]
    else:
        lines.append("None.")
    lines.append("")
    return "\n".join(lines)


if __name__ == "__main__":
    raise SystemExit(main())
