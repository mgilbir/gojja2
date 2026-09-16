#!/usr/bin/env python3
# Copyright 2026 The gojja2 Authors
# SPDX-License-Identifier: Apache-2.0
"""Named environment profiles a corpus case can render under.

Some corpora are not written against a bare `jinja2.Environment`. LLM chat
templates in particular are written against the environment
`transformers.apply_chat_template` builds, which sets whitespace options,
enables an extension, overrides a filter and injects two globals. A template
harvested from that world renders differently -- or not at all -- under the
default environment, so the environment has to travel with the case.

A case names one with `"__profile__": "transformers"` in its JSON header.
The name is recorded rather than the individual knobs deliberately: there are
two implementations of each profile, this one and conformance/profile.go, and
a single name is far harder to let drift apart than a list of settings.

Every profile must be deterministic. `strftime_now` would otherwise make a
template's output depend on the day it was rendered, so it is frozen; see
FROZEN_NOW.
"""

from __future__ import annotations

import datetime
import json

import jinja2

# FROZEN_NOW is what `strftime_now` sees, instead of the real clock.
#
# A chat template that stamps the date would otherwise produce a golden that
# stops matching tomorrow. The Go side freezes to this same instant; the two
# must agree exactly, so it is a UTC instant with no sub-second part.
FROZEN_NOW = datetime.datetime(2026, 1, 1, 0, 0, 0, tzinfo=datetime.timezone.utc)


def _raise_exception(message):
    """transformers' raise_exception: templates use it to reject bad input."""
    raise jinja2.exceptions.TemplateError(message)


def _tojson(x, ensure_ascii=False, indent=None, separators=None, sort_keys=False):
    """transformers' tojson override.

    jinja2's own tojson escapes HTML characters and sorts keys, because it is
    meant for embedding in a <script> block. transformers replaces it with a
    plain json.dumps, so chat templates that serialise tool calls produce
    readable JSON in the model's own key order. The difference is observable in
    almost every tool-calling template, which is why the override is part of
    the profile rather than something a case can ignore.
    """
    return json.dumps(
        x,
        ensure_ascii=ensure_ascii,
        indent=indent,
        separators=separators,
        sort_keys=sort_keys,
    )


def _strftime_now(fmt):
    return FROZEN_NOW.strftime(fmt)


# TRANSFORMERS mirrors transformers/utils/chat_template_utils.py, which builds
# ImmutableSandboxedEnvironment(trim_blocks=True, lstrip_blocks=True,
# extensions=[AssistantTracker, jinja2.ext.loopcontrols]) and then installs the
# tojson filter and the two globals below.
#
# Two deliberate departures, both recorded in the corpus SOURCES.md:
#
#   - The sandbox is not applied. It restricts access to *Python* objects,
#     which a JSON-only context does not have; see docs/scope.md.
#   - AssistantTracker, which provides `{% generation %}`, is not implemented.
#     Templates using it are dropped at import time rather than rendered
#     wrongly here.
TRANSFORMERS = {
    "settings": {
        "trim_blocks": True,
        "lstrip_blocks": True,
        "extensions": ["loopcontrols"],
    },
    "filters": {"tojson": _tojson},
    "globals": {
        "raise_exception": _raise_exception,
        "strftime_now": _strftime_now,
    },
}

PROFILES = {"transformers": TRANSFORMERS}


def settings_for(name: str) -> dict:
    """Return the environment settings a profile implies."""
    return dict(PROFILES[name]["settings"])


def apply(env: jinja2.Environment, name: str) -> None:
    """Install a profile's filters and globals onto a built environment."""
    profile = PROFILES[name]
    env.filters.update(profile["filters"])
    env.globals.update(profile["globals"])
