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
Response (one line of JSON):
    {"ok": true, "output": "..."}
    {"ok": false, "error": {"type": ..., "message": ..., "lineno": ...}}
    {"ok": true, "hello": {"python": ..., "jinja2": ..., "markupsafe": ...}}

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

from jinjaoracle import apply_limits, guarded, is_resource_error, render


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


def handle(request: dict) -> dict:
    if request.get("hello"):
        return hello()
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
