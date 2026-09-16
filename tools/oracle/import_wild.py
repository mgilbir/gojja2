#!/usr/bin/env python3
# Copyright 2026 The gojja2 Authors
# SPDX-License-Identifier: Apache-2.0
"""Import real-world templates, driving them with a synthesised context.

Every other corpus here is made of templates written to be *tested*. These are
templates written to be *used*: a documentation theme, with inheritance,
partials, whitespace control and filters applied to values that come from a
host application. That is the shape the chat-template corpus does not reach --
chat templates have no {% extends %}, no {% include %} and no blocks at all.

The catch is that a template from a real project arrives without its context.
It is synthesised by recorder.py, which renders once against proxies that log
what is asked of them and once against the plain JSON that log reads back as,
and requires the two to agree. A template that needs something JSON cannot
express -- the host's `url` filter, a callable like `lang.t("toc")` -- is not
guessed at. It is dropped, and the reason is recorded in SOURCES.md.

That dropped list is the deliverable as much as the corpus is: it is the
catalogue of what a JSON-context corpus structurally cannot cover.

Nothing is vendored. `make suites` clones the theme into the gitignored
third_party/, and the cases land in the gitignored testdata/generated/.
"""

from __future__ import annotations

import collections
import json
import re
import shutil
import sys
from pathlib import Path

import jinja2
import jinja2.meta

import provenance
import recorder

ROOT = Path(__file__).resolve().parents[2]
SRC = ROOT / "third_party" / "mkdocs_material" / "material" / "templates"
DST = ROOT / "testdata" / "generated" / "wild"

SEPARATOR = "\n---\n"

# A template that reads a clock or a random source cannot have a stable
# golden, however well its context is synthesised.
NONDETERMINISTIC = re.compile(r"\bnow\(\)|\brandom\b|\blipsum\b")


def referenced(env: jinja2.Environment, source: str) -> set[str]:
    """Template names this source includes or extends, literal ones only."""
    try:
        return {
            name
            for name in jinja2.meta.find_referenced_templates(env.parse(source))
            if isinstance(name, str)
        }
    except jinja2.TemplateError:
        return set()


def closure(env: jinja2.Environment, root: Path, name: str) -> dict[str, str]:
    """Every template reachable from name, as {name: source}.

    A case has to be self-contained: the Go harness gets one JSON header and
    no filesystem, so whatever the template includes travels with it.
    """
    out: dict[str, str] = {}
    pending = [name]
    while pending:
        current = pending.pop()
        if current in out:
            continue
        path = root / current
        if not path.is_file():
            continue
        text = path.read_text(encoding="utf-8")
        out[current] = text
        pending.extend(referenced(env, text))
    return out


def undeclared_across(env: jinja2.Environment, sources: dict[str, str]) -> list[str]:
    """Top-level names read anywhere in a template and its includes.

    An include shares its parent's context, so a name only the partial reads
    still has to be in it -- otherwise the partial renders undefineds and the
    case exercises nothing.
    """
    names: set[str] = set()
    for text in sources.values():
        try:
            names |= jinja2.meta.find_undeclared_variables(env.parse(text))
        except jinja2.TemplateError:
            continue
    return sorted(names)


def main() -> int:
    if not SRC.is_dir():
        print(f"{SRC} not found; run `make suites` first", file=sys.stderr)
        return 1

    if DST.exists():
        shutil.rmtree(DST)
    DST.mkdir(parents=True)

    env = jinja2.Environment(loader=jinja2.FileSystemLoader(str(SRC)))
    kept, dropped = 0, []

    for path in sorted(SRC.rglob("*.html")):
        name = path.relative_to(SRC).as_posix()
        source = path.read_text(encoding="utf-8")
        if NONDETERMINISTIC.search(source):
            dropped.append((name, "reads the clock or a random source"))
            continue

        templates = closure(env, SRC, name)
        names = undeclared_across(env, templates)
        try:
            context, _ = recorder.synthesise_with(env, source, names)
        except recorder.Unsupported as exc:
            dropped.append((name, tidy(exc)))
            continue

        header = dict(context)
        # The template itself is the entry point; the rest ride along so the
        # case can be rendered with no filesystem.
        extra = {k: v for k, v in templates.items() if k != name}
        if extra:
            header["__templates__"] = extra
        stem = re.sub(r"[^A-Za-z0-9]+", "_", name.removesuffix(".html")).strip("_")
        body = json.dumps(header, ensure_ascii=False, indent=2) + SEPARATOR + source
        (DST / f"{stem}.jj2").write_text(body, encoding="utf-8")
        kept += 1

    (DST / "SOURCES.md").write_text(sources_md(kept, dropped), encoding="utf-8")
    print(
        f"imported {kept} wild templates into {DST.relative_to(ROOT)} "
        f"({len(dropped)} dropped)",
        file=sys.stderr,
    )
    return 0


def tidy(exc: Exception) -> str:
    """One readable line for a drop reason.

    jinja2's TemplateNotFound names every directory it searched, which is an
    absolute path on whoever ran the import. That would make the report differ
    between two machines that built the same corpus, so it is cut off.
    """
    reason = str(exc).split("\n")[0]
    reason = re.sub(r" not found in search path.*", " not found", reason)
    return reason.strip()


def sources_md(kept: int, dropped: list[tuple[str, str]]) -> str:
    tally = collections.Counter(reason for _, reason in dropped)
    lines = [
        "# wild corpus",
        "",
        "Generated by tools/oracle/import_wild.py. Not vendored, not committed:",
        "`make import` rebuilds it from the clone in third_party/.",
        "",
        "## Source",
        "",
        "github.com/squidfunk/mkdocs-material, material/templates -- MIT.",
        "",
        "Templates written to be used rather than tested: inheritance,",
        "partials, whitespace control, and filters over values a host",
        "application supplies. Each case embeds the templates it includes, so",
        "it renders with no filesystem.",
        "",
    ]
    lines += provenance.block(
        [("squidfunk/mkdocs-material", ROOT / "third_party" / "mkdocs_material", "MIT")]
    )
    lines += [
        "## Contexts",
        "",
        "These templates ship no context. Each one here was synthesised by",
        "tools/oracle/recorder.py: render once against proxies that record",
        "what is asked of them, read the recording back as plain JSON, render",
        "again, and require the two to agree byte for byte.",
        "",
        f"## Cases ({kept})",
        "",
        f"## Dropped ({len(dropped)})",
        "",
        "Not failures. These are templates a JSON-only context cannot drive,",
        "which is a statement about the corpus rather than about gojja2 -- the",
        "engine is never asked about them, so it can neither pass nor fail.",
        "",
    ]
    lines += [f"- {n} x {reason}" for reason, n in tally.most_common()]
    lines += ["", "### Individually", ""]
    lines += [f"- `{name}` -- {reason}" for name, reason in sorted(dropped)]
    lines.append("")
    return "\n".join(lines)


if __name__ == "__main__":
    raise SystemExit(main())
