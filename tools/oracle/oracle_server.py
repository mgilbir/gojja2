#!/usr/bin/env python3
# Copyright 2026 The gojja2 Authors
# SPDX-License-Identifier: Apache-2.0
"""Answer render requests from a pipe, one JSON object per line.

The batch tool pays a Python start-up and a jinja2 import per invocation, which
is fine for a corpus and hopeless for a fuzzer. This keeps one interpreter warm
so a differential run can ask tens of thousands of questions.

Request (one line of JSON):
    {"src": "...", "ctx": {...}, "settings": {...}, "templates": {...}}
Response (one line of JSON):
    {"ok": true, "output": "..."}
    {"ok": false, "error": {"type": ..., "message": ..., "lineno": ...}}

A request that kills the render -- a runaway allocation, an endless loop -- must
not kill the server, so each one runs under a wall-clock alarm and the whole
process under an address-space limit. Both are reported as ordinary failures.
The limits, and what counts as hitting one, live in jinjaoracle so that this
and the batch tool cannot disagree about it.
"""

from __future__ import annotations

import json
import sys

from jinjaoracle import apply_limits, guarded, is_resource_error, render


def handle(request: dict) -> dict:
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
