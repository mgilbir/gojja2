#!/usr/bin/env python3
# Copyright 2026 The gojja2 Authors
# SPDX-License-Identifier: Apache-2.0
"""Context variants for LLM chat templates.

A chat template is one template rendered against many shapes of conversation,
and the shapes are where the coverage is: a template that handles a trailing
user turn may mishandle a trailing assistant turn, a missing system message, or
a tool call. Rendering each template once would exercise one branch of a dozen.

So each template is paired with every variant below and each pairing becomes
its own case. Coverage comes from context variation, not from collecting more
templates -- the templates themselves are heavily copy-pasted between model
repos and dedupe away.

Every value here is a JSON literal. Nothing is generated from the clock, a
random source or the filesystem, so a case renders the same today and next
year.
"""

from __future__ import annotations

BOS = "<s>"
EOS = "</s>"

_SYSTEM = {"role": "system", "content": "You are a helpful assistant."}
_USER1 = {"role": "user", "content": "What is the capital of France?"}
_ASSISTANT1 = {"role": "assistant", "content": "Paris."}
_USER2 = {"role": "user", "content": "And of Japan?"}
_ASSISTANT2 = {"role": "assistant", "content": "Tokyo."}

# A tool in the schema apply_chat_template passes through.
_TOOL = {
    "type": "function",
    "function": {
        "name": "get_weather",
        "description": "Get the current weather in a location.",
        "parameters": {
            "type": "object",
            "properties": {
                "location": {"type": "string", "description": "City name"},
                "unit": {"type": "string", "enum": ["celsius", "fahrenheit"]},
            },
            "required": ["location"],
        },
    },
}

_TOOL_CALL_TURN = {
    "role": "assistant",
    "content": None,
    "tool_calls": [
        {
            "id": "call_1",
            "type": "function",
            "function": {
                "name": "get_weather",
                "arguments": {"location": "Paris", "unit": "celsius"},
            },
        }
    ],
}

_TOOL_RESULT_TURN = {
    "role": "tool",
    "name": "get_weather",
    "tool_call_id": "call_1",
    "content": '{"temperature": 12, "unit": "celsius"}',
}

# Content as a list of parts, which is how multimodal models receive it.
_MULTIMODAL_TURN = {
    "role": "user",
    "content": [
        {"type": "text", "text": "Describe this image."},
        {"type": "image"},
    ],
}


def _base(messages: list, **extra) -> dict:
    ctx = {
        "messages": messages,
        "add_generation_prompt": True,
        "bos_token": BOS,
        "eos_token": EOS,
    }
    ctx.update(extra)
    return ctx


# VARIANTS maps a short name, which becomes part of the case filename, to the
# context. The names are stable: a case that regresses keeps its identity
# across a re-import.
VARIANTS = {
    # The plainest thing a template must handle.
    "single_user": _base([_USER1]),
    # Whether a system message is present is the single most common branch.
    "system": _base([_SYSTEM, _USER1]),
    "no_system_multiturn": _base([_USER1, _ASSISTANT1, _USER2]),
    "system_multiturn": _base([_SYSTEM, _USER1, _ASSISTANT1, _USER2]),
    # A trailing assistant turn is the continuation case, and templates
    # frequently get its terminator wrong.
    "trailing_assistant": _base([_USER1, _ASSISTANT1], add_generation_prompt=False),
    "no_generation_prompt": _base([_SYSTEM, _USER1], add_generation_prompt=False),
    # Tool calling: the schema, the call and the result.
    "tools": _base([_SYSTEM, _USER1], tools=[_TOOL]),
    "tool_call": _base(
        [_USER1, _TOOL_CALL_TURN, _TOOL_RESULT_TURN, _ASSISTANT2], tools=[_TOOL]
    ),
    # Content as parts rather than a string.
    "multimodal": _base([_MULTIMODAL_TURN]),
    # An empty conversation: templates that index messages[0] unguarded fail
    # here, and failing the same way as CPython is the point.
    "empty": _base([]),
}
