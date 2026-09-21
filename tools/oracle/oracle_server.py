#!/usr/bin/env python3
# Copyright 2026 The gojja2 Authors
# SPDX-License-Identifier: Apache-2.0
"""Answer render requests from a pipe, one JSON object per line.

The batch tool pays a Python start-up and a jinja2 import per invocation, which
is fine for a corpus and hopeless for a fuzzer. This keeps one interpreter warm
so a differential run can ask tens of thousands of questions.

Request (one line of JSON):
    {"src": "...", "ctx": {...}, "settings": {...}, "templates": {...}}
    {"hello": true}
    {"analyze": true, "src": "...", "settings": {...}, "templates": {...}}
Response (one line of JSON):
    {"ok": true, "output": "..."}
    {"ok": false, "error": {"type": ..., "message": ..., "lineno": ...}}
    {"ok": true, "hello": {"python": ..., "jinja2": ..., "markupsafe": ...}}
    {"ok": true, "tree": "...", "info": "...", "variables": {...}}

The analyze request is what lets the structure and the analyses be fuzzed rather
than only the corpus. Starting an interpreter per template costs a tenth of a
second, which is fine for two thousand cases and hopeless for two hundred
thousand; this keeps one warm, so a soak can ask the same questions of both
implementations as fast as it can generate templates.

The hello says which interpreter and which libraries are answering. That is not
a courtesy: CPython carries its own Unicode and words several errors its own
way, so an oracle on a different release is a different specification, and a
differential run against one reports every version difference as a gojja2 bug.
GOJJA2_ORACLE_PYTHON can point this at any interpreter, so the caller has to be
able to find out which one it got.

A request that kills the render -- a runaway allocation, an endless loop -- must
not kill the server, so each one runs under a wall-clock alarm and the whole
process under an address-space limit. Both are reported as ordinary failures.
The limits, and what counts as hitting one, live in jinjaoracle so that this
and the batch tool cannot disagree about it.
"""

from __future__ import annotations

import importlib.metadata
import json
import platform
import sys

import nameflow
import syntax_emit
from jinjaoracle import (apply_limits, build_environment, guarded,
                         is_resource_error, render)


def hello() -> dict:
    """Who is answering: the interpreter, and the two libraries that decide."""
    return {
        "ok": True,
        "hello": {
            "python": ".".join(map(str, sys.version_info[:2])),
            "python_full": platform.python_version(),
            "jinja2": importlib.metadata.version("jinja2"),
            "markupsafe": importlib.metadata.version("markupsafe"),
        },
    }


def analyze(request: dict) -> dict:
    """The template's structure and what it does with its variables.

    Written in jinja2's vocabulary and then in gojja2's, which is the point:
    the engine answers the same three questions from its own tree, and a
    difference is a difference about the template rather than about either
    tree's shape.
    """
    name = request.get("name") or "<fuzz>"
    src = request.get("src", "")
    sources = dict(request.get("templates") or {})
    sources[name] = src
    env = build_environment(request.get("settings") or {}, sources, name,
                            request.get("profile"))
    # Compiled as well as parsed: jinja2 defers several refusals to code
    # generation, and the engine refuses them too. A template neither will
    # compile has nothing to compare.
    env.get_template(name)
    tree = env.parse(src, name)

    def resolver(other, _env=env):
        try:
            return _env.parse(_env.loader.get_source(_env, other)[0], other)
        except Exception:
            return None

    return {
        "ok": True,
        "tree": syntax_emit.canonical(tree, env.globals),
        "info": syntax_emit.canonical_info(tree, env.globals),
        "variables": {k: encode_effects(v) for k, v in
                      nameflow.analyze(tree, env.globals, resolver).items()},
    }


def encode_effects(v: dict) -> str:
    s = (("o" if v["output"] else "") + ("f" if v["flow"] else "")
         + ("r" if v["required"] else ""))
    return (s or "-") + ("?" if v["unknown"] else "")


def handle(request: dict) -> dict:
    if request.get("hello"):
        return hello()
    if request.get("analyze"):
        # Under the same limits a render gets: a template that takes the
        # machine down while being analysed is no better than one that does it
        # while rendering.
        return guarded(lambda: analyze(request))
    name = request.get("name") or "<fuzz>"
    result = guarded(
        lambda: render(
            name,
            request.get("src", ""),
            request.get("ctx") or {},
            request.get("settings") or {},
            request.get("templates") or {},
            request.get("profile"),
        )
    )
    # render() catches everything, so a limit hit comes back as an ordinary
    # failure and has to be recognised. What counts as one is defined once, in
    # jinjaoracle, because the batch tool has to agree with this.
    if is_resource_error(result):
        result["resource"] = True
    return result


def main() -> int:
    apply_limits()

    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        try:
            request = json.loads(line)
        except json.JSONDecodeError as exc:
            response = {"ok": False, "error": {"type": "ProtocolError", "message": str(exc)}}
        else:
            response = handle(request)
        sys.stdout.write(json.dumps(response, ensure_ascii=False) + "\n")
        sys.stdout.flush()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
