#!/usr/bin/env python3
# Copyright 2026 The gojja2 Authors
# SPDX-License-Identifier: Apache-2.0
"""Shared CPython jinja2 oracle: build an environment, render, describe failure.

Used by oracle.py, which grades a corpus on disk, and by oracle_server.py,
which answers one render at a time over a pipe so a fuzzer can ask thousands of
questions a second.
"""

from __future__ import annotations

import resource
import signal
import sys

import jinja2

import profiles

# Environment options a case may set. Anything else is an error, so a typo in a
# fixture fails loudly instead of being silently ignored.
SETTING_KEYS = {
    "block_start_string",
    "block_end_string",
    "variable_start_string",
    "variable_end_string",
    "comment_start_string",
    "comment_end_string",
    "line_statement_prefix",
    "line_comment_prefix",
    "trim_blocks",
    "lstrip_blocks",
    "newline_sequence",
    "keep_trailing_newline",
    "autoescape",
    "optimized",
    "undefined",
    "extensions",
}

UNDEFINED_KINDS = {
    "default": jinja2.Undefined,
    "strict": jinja2.StrictUndefined,
    "chainable": jinja2.ChainableUndefined,
    "debug": jinja2.DebugUndefined,
}

# Optional tags, named the short way a case writes them.
EXTENSION_MODULES = {
    "do": "jinja2.ext.do",
    "loopcontrols": "jinja2.ext.loopcontrols",
}


# --- resource limits ----------------------------------------------------------
#
# A template can ask jinja2 for unbounded work -- `{{ "x" * 2**40 }}`, or a
# `range()` it walks forever -- and jinja2 has no bounds of its own, so the
# question has to be bounded here. Both entry points apply these: the batch
# tool that writes goldens as well as the server that answers a fuzzer. The
# batch tool having none is what let a hostile case take the machine down while
# goldens were being generated.
MEMORY_LIMIT_BYTES = 2 * 1024 * 1024 * 1024
RENDER_TIMEOUT_SECONDS = 5.0
RECURSION_LIMIT = 3000


class RenderTimeout(Exception):
    """A render that exceeded the per-case wall clock."""


# Failures that mean "this interpreter ran out of room", not "this template is
# wrong". A result produced by hitting a limit says nothing about conformance,
# so it is neither graded nor recorded as a golden.
#
# OverflowError is deliberately NOT in this set. "Python int too large to
# convert to C ssize_t" is what CPython says about an *argument*, on any machine
# and every time -- it is the answer, not a symptom of this process's limits.
# Listing it hid a whole family of divergences from the fuzzer and the soak,
# while the batch tool, which never classified anything, recorded the same
# exception as the expected answer and graded it. The two paths disagreed about
# what one exception meant, so this set now has one definition and both import
# it. A 36-case sweep of integer arguments reported 2 divergences with
# OverflowError listed here and 23 without.
RESOURCE_ERRORS = {"MemoryError", "RecursionError", "RenderTimeout"}

# The narrower question the batch tool asks: can this be written down as the
# expected answer?
#
# RecursionError is in RESOURCE_ERRORS but not here, and the difference is
# real. A *generated* template of accidental depth raises it only on this
# machine, so a fuzzer must discard it -- but a template that recurses
# infinitely, `{% extends "self.txt" %}`, raises it on every machine, and two
# committed goldens say so. It is reproducible only because apply_limits pins
# the recursion limit, which the batch tool did not do before: the server ran
# at 3000 and the batch tool at CPython's default 1000, so the two disagreed
# about how deep is too deep.
UNRECORDABLE_ERRORS = {"MemoryError", "RenderTimeout"}


def _on_alarm(signum, frame):  # noqa: ARG001 - signal handler signature
    raise RenderTimeout("render exceeded the time limit")


def apply_limits() -> None:
    """Bound this process's address space, arm the per-case alarm, and pin the
    recursion limit so that both entry points agree about depth."""
    soft, hard = resource.getrlimit(resource.RLIMIT_AS)
    limit = MEMORY_LIMIT_BYTES if hard == resource.RLIM_INFINITY else min(MEMORY_LIMIT_BYTES, hard)
    resource.setrlimit(resource.RLIMIT_AS, (limit, hard))
    signal.signal(signal.SIGALRM, _on_alarm)
    # A deep template recurses in the compiler as well as at render time.
    sys.setrecursionlimit(RECURSION_LIMIT)


def is_resource_error(result: dict) -> bool:
    """Report whether a result came from hitting a limit rather than from the
    template's own terms."""
    return not result["ok"] and result["error"]["type"] in RESOURCE_ERRORS


def is_unrecordable(result: dict) -> bool:
    """Report whether a result is this machine's answer rather than CPython's,
    and so must never become a golden."""
    return not result["ok"] and result["error"]["type"] in UNRECORDABLE_ERRORS


def guarded(fn) -> dict:
    """Run one render under the per-case alarm, turning a limit into a result.

    RenderTimeout, MemoryError and RecursionError can fire inside an `except`
    clause and escape the caller's own handling, so they are caught here and
    reported rather than allowed to end the process.
    """
    signal.setitimer(signal.ITIMER_REAL, RENDER_TIMEOUT_SECONDS)
    try:
        return fn()
    except (RenderTimeout, MemoryError, RecursionError, CaseError) as exc:
        # Marked here rather than left to is_resource_error, because a
        # CaseError is a malformed case rather than a listed exception type
        # and must not be graded either.
        return {"ok": False, "error": describe(exc), "resource": True}
    finally:
        signal.setitimer(signal.ITIMER_REAL, 0)


class CaseError(Exception):
    """A malformed case, as opposed to a template that fails to render."""


def build_environment(
    settings: dict, sources: dict, where: str, profile: str | None = None
) -> jinja2.Environment:
    unknown = set(settings) - SETTING_KEYS
    if unknown:
        raise CaseError(f"{where}: unknown __settings__ keys: {sorted(unknown)}")

    # A profile supplies environment options of its own. A case may still
    # override one -- a chat template corpus that needs keep_trailing_newline,
    # say -- so the case's own settings win.
    opts = {}
    if profile is not None:
        if profile not in profiles.PROFILES:
            raise CaseError(f"{where}: unknown __profile__ {profile!r}")
        opts.update(profiles.settings_for(profile))
    opts.update(settings)
    undefined = opts.pop("undefined", "default")
    if undefined not in UNDEFINED_KINDS:
        raise CaseError(f"{where}: unknown undefined kind {undefined!r}")

    extensions = []
    for name in opts.pop("extensions", []):
        module = EXTENSION_MODULES.get(name)
        if module is None:
            raise CaseError(f"{where}: unknown extension {name!r}")
        extensions.append(module)

    env = jinja2.Environment(
        loader=jinja2.DictLoader(sources),
        undefined=UNDEFINED_KINDS[undefined],
        extensions=extensions,
        **opts,
    )
    if profile is not None:
        profiles.apply(env, profile)
    return env


def describe(exc: BaseException) -> dict:
    """Serialise an exception the way a conformance check needs to see it.

    `type` is the assertion that matters and is compared strictly; `message`
    and `lineno` are recorded so divergence is visible.
    """
    info = {"type": type(exc).__name__, "message": str(exc)}
    # An exception can carry anything on these attributes -- a KeyError raised
    # from a template may hold an Undefined, which is not JSON -- so both are
    # coerced rather than trusted.
    lineno = getattr(exc, "lineno", None)
    if isinstance(lineno, int):
        info["lineno"] = lineno
    name = getattr(exc, "name", None)
    if isinstance(name, str):
        info["name"] = name
    return info


def render(
    name: str,
    source: str,
    context: dict,
    settings: dict,
    templates: dict,
    profile: str | None = None,
) -> dict:
    """Render one template, returning the output or the exception it raised."""
    try:
        sources = dict(templates)
        sources[name] = source
        env = build_environment(settings, sources, name, profile)
        output = env.get_template(name).render(context)
    except Exception as exc:  # noqa: BLE001 - any exception is a valid result
        return {"ok": False, "error": describe(exc)}
    return {"ok": True, "output": output}
