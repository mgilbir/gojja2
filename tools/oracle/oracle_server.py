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
"""

from __future__ import annotations

import json
import resource
import signal
import sys

from jinjaoracle import CaseError, describe, render

# A template that wants more than this is a generator bug, not a conformance
# question. Keeping the limit here means a bad input degrades to an error
# instead of taking the machine down.
MEMORY_LIMIT_BYTES = 2 * 1024 * 1024 * 1024
RENDER_TIMEOUT_SECONDS = 5.0


class RenderTimeout(Exception):
    pass


def _on_alarm(signum, frame):  # noqa: ARG001 - signal handler signature
    raise RenderTimeout("render exceeded the time limit")


def apply_limits() -> None:
    soft, hard = resource.getrlimit(resource.RLIMIT_AS)
    limit = MEMORY_LIMIT_BYTES if hard == resource.RLIM_INFINITY else min(MEMORY_LIMIT_BYTES, hard)
    resource.setrlimit(resource.RLIMIT_AS, (limit, hard))
    signal.signal(signal.SIGALRM, _on_alarm)


# Failures that mean "this server ran out of room", not "this template is
# wrong". A result produced by hitting a sandbox limit says nothing about
# conformance and must not be graded.
RESOURCE_ERRORS = {"MemoryError", "RecursionError", "RenderTimeout", "OverflowError"}


def handle(request: dict) -> dict:
    name = request.get("name") or "<fuzz>"
    signal.setitimer(signal.ITIMER_REAL, RENDER_TIMEOUT_SECONDS)
    try:
        result = render(
            name,
            request.get("src", ""),
            request.get("ctx") or {},
            request.get("settings") or {},
            request.get("templates") or {},
        )
        # render() catches everything, so a limit hit comes back as an
        # ordinary failure and has to be recognised here.
        if not result["ok"] and result["error"]["type"] in RESOURCE_ERRORS:
            result["resource"] = True
        return result
    except (RenderTimeout, MemoryError, RecursionError, CaseError) as exc:
        # These escape render() because they can fire inside its own except
        # clause; report them rather than letting the server die.
        return {"ok": False, "error": describe(exc), "resource": True}
    finally:
        signal.setitimer(signal.ITIMER_REAL, 0)


def main() -> int:
    apply_limits()
    # A deep template recurses in the compiler as well as at render time.
    sys.setrecursionlimit(3000)

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
