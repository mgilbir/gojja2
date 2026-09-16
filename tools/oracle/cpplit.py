#!/usr/bin/env python3
# Copyright 2026 The gojja2 Authors
# SPDX-License-Identifier: Apache-2.0
"""Read C++ literals out of a test file.

minja and llama.cpp both keep their Jinja test corpora as C++ source: a call
naming a template, a context and some options. The templates are the valuable
part and there is no other way to reach them, so the calls are parsed here.

This is not a C++ parser. It knows string literals -- ordinary, raw, and
adjacent ones concatenated -- brace and paren nesting, and comments, which is
everything needed to lift out one call's arguments. Anything it cannot read it
declines to read, so a construct it does not handle drops a case rather than
producing a wrong one.
"""

from __future__ import annotations

import json
import re

# A C++ escape inside an ordinary string literal.
_ESCAPES = {
    "n": "\n",
    "t": "\t",
    "r": "\r",
    "0": "\0",
    "b": "\b",
    "f": "\f",
    "v": "\v",
    "a": "\a",
    "\\": "\\",
    '"': '"',
    "'": "'",
    "?": "?",
}


class ParseError(Exception):
    """A construct this reader does not handle."""


def _skip_trivia(src: str, i: int) -> int:
    """Advance past whitespace and comments."""
    while i < len(src):
        if src[i].isspace():
            i += 1
        elif src.startswith("//", i):
            j = src.find("\n", i)
            i = len(src) if j < 0 else j + 1
        elif src.startswith("/*", i):
            j = src.find("*/", i)
            if j < 0:
                raise ParseError("unterminated block comment")
            i = j + 2
        else:
            break
    return i


def _read_string(src: str, i: int) -> tuple[str, int]:
    """Read one string literal starting at src[i], returning its value."""
    # Raw literal: R"delim(...)delim"
    if src[i] == "R" and i + 1 < len(src) and src[i + 1] == '"':
        j = src.index("(", i + 2)
        delim = src[i + 2 : j]
        close = ")" + delim + '"'
        k = src.find(close, j + 1)
        if k < 0:
            raise ParseError("unterminated raw string")
        return src[j + 1 : k], k + len(close)

    if src[i] != '"':
        raise ParseError(f"not a string literal at {i}")

    out = []
    i += 1
    while i < len(src):
        c = src[i]
        if c == '"':
            return "".join(out), i + 1
        if c != "\\":
            out.append(c)
            i += 1
            continue
        i += 1
        e = src[i]
        if e in _ESCAPES:
            out.append(_ESCAPES[e])
            i += 1
        elif e == "u":
            out.append(chr(int(src[i + 1 : i + 5], 16)))
            i += 5
        elif e == "U":
            out.append(chr(int(src[i + 1 : i + 9], 16)))
            i += 9
        elif e == "x":
            m = re.match(r"[0-9a-fA-F]+", src[i + 1 :])
            out.append(chr(int(m.group(0), 16)))
            i += 1 + len(m.group(0))
        else:
            raise ParseError(f"unknown escape \\{e}")
    raise ParseError("unterminated string")


def read_concatenated_string(src: str, i: int) -> tuple[str, int]:
    """Read one or more adjacent string literals as a single value.

    C++ joins `"a" "b"` into `"ab"`, and both test suites lay templates out one
    line per line of template, so this is the common case rather than an
    oddity.
    """
    parts = []
    while True:
        i = _skip_trivia(src, i)
        if i >= len(src) or (src[i] != '"' and not src.startswith('R"', i)):
            break
        text, i = _read_string(src, i)
        parts.append(text)
    if not parts:
        raise ParseError(f"no string literal at {i}")
    return "".join(parts), i


def split_arguments(src: str, open_paren: int) -> list[str]:
    """Split the argument list of the call whose '(' is at open_paren.

    Returns the raw source of each argument, trimmed. Nesting and string
    literals are respected, so a comma inside a brace initialiser or a string
    does not split an argument.
    """
    if src[open_paren] != "(":
        raise ParseError("expected '('")
    args: list[str] = []
    depth = 0
    start = open_paren + 1
    i = start
    while i < len(src):
        c = src[i]
        if c == '"' or src.startswith('R"', i):
            _, i = _read_string(src, i + 1 if c == "R" else i)
            continue
        if src.startswith("//", i) or src.startswith("/*", i):
            i = _skip_trivia(src, i)
            continue
        if c in "([{":
            depth += 1
        elif c in ")]}":
            if c == ")" and depth == 0:
                args.append(src[start:i].strip())
                return args
            depth -= 1
        elif c == "," and depth == 0:
            args.append(src[start:i].strip())
            start = i + 1
        i += 1
    raise ParseError("unterminated argument list")


def find_calls(src: str, name: str) -> list[tuple[int, list[str]]]:
    """Find every call to `name`, returning (offset, raw arguments)."""
    out = []
    for m in re.finditer(r"\b" + re.escape(name) + r"\s*\(", src):
        # Skip the declaration and definition of the function itself.
        line_start = src.rfind("\n", 0, m.start()) + 1
        if re.match(r"\s*(static\s|void\s|inline\s)", src[line_start : m.start()]):
            continue
        try:
            args = split_arguments(src, m.end() - 1)
        except (ParseError, ValueError):
            continue
        out.append((m.start(), args))
    return out


def json_from_cpp(text: str) -> object:
    """Convert an nlohmann-json initialiser to a Python value.

    Handles the shapes these suites actually use:

        {}                          -> {}
        json::object()              -> {}
        json::array({1, 2})         -> [1, 2]
        {{"k", v}, {"k2", v2}}      -> {"k": v, "k2": v2}
        a plain JSON literal        -> itself

    Raises ParseError for anything else, so an unreadable context drops its
    case rather than being guessed at.
    """
    text = text.strip()
    if text in ("", "{}", "json::object()", "json()"):
        return {}
    if text == "json::array()":
        return []

    # json::parse("...") wraps a literal JSON document in a string.
    inner = _strip_call(text, "json::parse")
    if inner is not None:
        doc, _ = read_concatenated_string(inner, 0)
        return json.loads(doc)

    inner = _strip_call(text, "json::array")
    if inner is not None:
        return json_from_cpp_array(inner)
    inner = _strip_call(text, "json::object")
    if inner is not None:
        return json_from_cpp(inner)
    inner = _strip_call(text, "json")
    if inner is not None:
        return json_from_cpp(inner)

    # A brace initialiser of key/value pairs, or a plain JSON literal.
    if text.startswith("{"):
        return _object_or_json(text)
    if text.startswith("["):
        return json_from_cpp_array(text)
    return _scalar(text)


def _strip_call(text: str, name: str) -> str | None:
    prefix = name + "("
    if not text.startswith(prefix) or not text.endswith(")"):
        return None
    return text[len(prefix) : -1].strip()


def _object_or_json(text: str) -> object:
    # Try plain JSON first: `{"a": 1}` is valid in both languages.
    try:
        return json.loads(text)
    except json.JSONDecodeError:
        pass
    # Otherwise an nlohmann pair-list: {{"k", v}, {"k2", v2}}
    body = text[1:-1].strip()
    if not body:
        return {}
    out = {}
    for part in _split_top_level(body):
        part = part.strip()
        if not (part.startswith("{") and part.endswith("}")):
            raise ParseError(f"not a key/value pair: {part!r}")
        pieces = _split_top_level(part[1:-1].strip())
        if len(pieces) != 2:
            raise ParseError(f"not a key/value pair: {part!r}")
        key, _ = read_concatenated_string(pieces[0].strip(), 0)
        out[key] = json_from_cpp(pieces[1].strip())
    return out


def json_from_cpp_array(text: str) -> list:
    text = text.strip()
    if text.startswith("{") or text.startswith("["):
        body = text[1:-1].strip()
    else:
        raise ParseError(f"not an array: {text!r}")
    if not body:
        return []
    return [json_from_cpp(part.strip()) for part in _split_top_level(body)]


def _split_top_level(body: str) -> list[str]:
    parts, depth, start, i = [], 0, 0, 0
    while i < len(body):
        c = body[i]
        if c == '"' or body.startswith('R"', i):
            _, i = _read_string(body, i + 1 if c == "R" else i)
            continue
        if c in "([{":
            depth += 1
        elif c in ")]}":
            depth -= 1
        elif c == "," and depth == 0:
            parts.append(body[start:i])
            start = i + 1
        i += 1
    parts.append(body[start:])
    return [p for p in parts if p.strip()]


def _scalar(text: str) -> object:
    if text.startswith('"') or text.startswith('R"'):
        return read_concatenated_string(text, 0)[0]
    if text in ("true", "false"):
        return text == "true"
    if text in ("nullptr", "null", "json()"):
        return None
    try:
        return json.loads(text)
    except json.JSONDecodeError as exc:
        raise ParseError(f"unreadable scalar {text!r}") from exc
