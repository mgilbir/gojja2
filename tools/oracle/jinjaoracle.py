#!/usr/bin/env python3
# Copyright 2026 The gojja2 Authors
# SPDX-License-Identifier: Apache-2.0
"""Shared CPython jinja2 oracle: build an environment, render, describe failure.

Used by oracle.py, which grades a corpus on disk, and by oracle_server.py,
which answers one render at a time over a pipe so a fuzzer can ask thousands of
questions a second.
"""

from __future__ import annotations

import jinja2

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


class CaseError(Exception):
    """A malformed case, as opposed to a template that fails to render."""


def build_environment(settings: dict, sources: dict, where: str) -> jinja2.Environment:
    unknown = set(settings) - SETTING_KEYS
    if unknown:
        raise CaseError(f"{where}: unknown __settings__ keys: {sorted(unknown)}")

    opts = dict(settings)
    undefined = opts.pop("undefined", "default")
    if undefined not in UNDEFINED_KINDS:
        raise CaseError(f"{where}: unknown undefined kind {undefined!r}")

    extensions = []
    for name in opts.pop("extensions", []):
        module = EXTENSION_MODULES.get(name)
        if module is None:
            raise CaseError(f"{where}: unknown extension {name!r}")
        extensions.append(module)

    return jinja2.Environment(
        loader=jinja2.DictLoader(sources),
        undefined=UNDEFINED_KINDS[undefined],
        extensions=extensions,
        **opts,
    )


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


def render(name: str, source: str, context: dict, settings: dict, templates: dict) -> dict:
    """Render one template, returning the output or the exception it raised."""
    try:
        sources = dict(templates)
        sources[name] = source
        env = build_environment(settings, sources, name)
        output = env.get_template(name).render(context)
    except Exception as exc:  # noqa: BLE001 - any exception is a valid result
        return {"ok": False, "error": describe(exc)}
    return {"ok": True, "output": output}
