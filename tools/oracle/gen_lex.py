#!/usr/bin/env python3
# Copyright 2026 The gojja2 Authors
# SPDX-License-Identifier: Apache-2.0
"""Generate the lexer conformance corpus for internal/lexer/lexer_test.go.

jinja2's lexer is reachable directly, so the token stream itself can be graded
rather than only the rendered output. That matters because whitespace control,
trim_blocks/lstrip_blocks, raw blocks and line statements all decide what data
tokens contain, and a single wrong newline there is invisible in most templates
and glaring in a few.
"""

from __future__ import annotations

import json
import sys
from pathlib import Path

from jinja2 import Environment
from jinja2.lexer import TOKEN_EOF

OUT = Path(__file__).resolve().parents[2] / "internal" / "lexer" / "testdata" / "lex.jsonl"

DEFAULT = {}
TRIM = {"trim_blocks": True}
LSTRIP = {"lstrip_blocks": True}
BOTH = {"trim_blocks": True, "lstrip_blocks": True}
KEEP = {"keep_trailing_newline": True}
LINES = {"line_statement_prefix": "#", "line_comment_prefix": "##"}
CUSTOM = {
    "block_start_string": "<%",
    "block_end_string": "%>",
    "variable_start_string": "<<",
    "variable_end_string": ">>",
    "comment_start_string": "<#",
    "comment_end_string": "#>",
}
CRLF = {"newline_sequence": "\r\n", "keep_trailing_newline": True}

# (source, [settings variants]) -- each source is lexed under every variant.
CASES: list[tuple[str, list[dict]]] = [
    # plain data and the trailing-newline rule
    ("hello", [DEFAULT]),
    ("hello\n", [DEFAULT, KEEP]),
    ("hello\n\n", [DEFAULT, KEEP]),
    ("a\r\nb\rc\n", [DEFAULT, KEEP, CRLF]),
    ("", [DEFAULT, KEEP]),
    ("\n", [DEFAULT, KEEP]),

    # print tags
    ("{{ x }}", [DEFAULT]),
    ("{{x}}", [DEFAULT]),
    ("{{ x }}{{ y }}", [DEFAULT]),
    ("a {{ x }} b", [DEFAULT]),
    ("{{ }}", [DEFAULT]),

    # whitespace control
    ("  \n  {{- x -}}  \n  ", [DEFAULT, TRIM, LSTRIP, BOTH]),
    ("  \n  {%- if x -%}  \n  {%- endif -%}\n", [DEFAULT, TRIM, LSTRIP, BOTH]),
    ("a\n   {% if x %}\nb\n   {% endif %}\nc", [DEFAULT, TRIM, LSTRIP, BOTH]),
    ("   {% if x %}", [DEFAULT, LSTRIP, BOTH]),
    ("   {%+ if x %}", [DEFAULT, LSTRIP, BOTH]),
    ("x   {% if y %}", [DEFAULT, LSTRIP, BOTH]),
    ("{% if x +%}\nbody", [DEFAULT, TRIM, BOTH]),
    ("{% if x %}\nbody", [DEFAULT, TRIM, BOTH]),
    ("{{ x }}\nbody", [DEFAULT, TRIM, BOTH]),
    ("{# c #}\nbody", [DEFAULT, TRIM, BOTH]),
    ("  {#- c -#}  ", [DEFAULT, TRIM, BOTH]),
    ("a\n\n\n{%- if x %}", [DEFAULT]),

    # comments
    ("a{# comment #}b", [DEFAULT]),
    ("a{# multi\nline #}b", [DEFAULT]),
    ("a{##}b", [DEFAULT]),

    # raw
    ("{% raw %}{{ x }}{% endraw %}", [DEFAULT, TRIM, BOTH]),
    ("{% raw %}\n{{ x }}\n{% endraw %}", [DEFAULT, TRIM, BOTH]),
    ("  {%- raw -%}  a  {%- endraw -%}  ", [DEFAULT, TRIM]),
    ("{% raw %}{% if %}{% endraw %}", [DEFAULT]),
    ("{%raw%}x{%endraw%}", [DEFAULT]),

    # literals
    ("{{ 1 }}{{ 0 }}{{ 0_0 }}{{ 1_000 }}", [DEFAULT]),
    ("{{ 0b1010 }}{{ 0o17 }}{{ 0xFF }}{{ 0x_ff }}", [DEFAULT]),
    ("{{ 1.5 }}{{ 1e3 }}{{ 1.5e-3 }}{{ 1_0.2_5 }}", [DEFAULT]),
    ("{{ 1. }}", [DEFAULT]),
    ("{{ x.5 }}", [DEFAULT]),
    ("{{ 2**100 }}", [DEFAULT]),
    ('{{ "a" }}{{ \'b\' }}', [DEFAULT]),
    (r'{{ "a\nb\tc\\d\"e" }}', [DEFAULT]),
    (r'{{ "\x41é\U0001F600\101" }}', [DEFAULT]),
    (r'{{ "\d\8" }}', [DEFAULT]),
    ('{{ "a\nb" }}', [DEFAULT]),
    ('{{ "héllo" }}', [DEFAULT]),
    (r'{{ "\é" }}', [DEFAULT]),
    ("{{ 'it\\'s' }}", [DEFAULT]),

    # operators and balancing
    ("{{ a + b - c * d / e // f % g ** h ~ i }}", [DEFAULT]),
    ("{{ a == b != c < d <= e > f >= g }}", [DEFAULT]),
    ("{{ a[1:2:3] }}{{ a.b }}{{ a|b }}{{ a(b, c=1) }}", [DEFAULT]),
    ('{{ {"a": 1} }}', [DEFAULT]),
    ('{{ {"a": 1}}}', [DEFAULT]),
    ("{{ [1, [2, 3]] }}", [DEFAULT]),
    ("{{ (1,) }}", [DEFAULT]),
    ("{% set d = {1:\"a\",} %}{{ d[1] }}", [DEFAULT]),
    ("{{ a };{{ b }}", [DEFAULT]),

    # multi-line tags
    ("{%\n  if\n  x\n%}y{%\nendif\n%}", [DEFAULT, TRIM]),
    ("{{\n  x\n}}", [DEFAULT]),

    # names
    ("{{ _x }}{{ x_1 }}{{ ünïcode }}", [DEFAULT]),

    # line statements and comments
    ("# for x in y\nbody\n# endfor\n", [LINES]),
    ("  # for x in y\nbody", [LINES]),
    ("a # not a statement\n", [LINES]),
    ("hello ## trailing comment\nworld", [LINES]),
    ("## whole line\nworld", [LINES]),
    ("# for x in y\n\nbody", [LINES]),
    ("# set x = 1\n{{ x }}", [LINES]),

    # custom delimiters
    ("<< x >><% if y %>a<% endif %><# c #>", [CUSTOM]),

    # error cases
    ("{# unterminated", [DEFAULT]),
    ("{% raw %}unterminated", [DEFAULT]),
    ("{{ a) }}", [DEFAULT]),
    ("{{ (a] }}", [DEFAULT]),
    ("{{ ? }}", [DEFAULT]),
    ("{{ a", [DEFAULT]),
    (r'{{ "\x" }}', [DEFAULT]),
    (r'{{ "\u12" }}', [DEFAULT]),
    (r'{{ "\U0011FFFF" }}', [DEFAULT]),
    (r'{{ "\N{DASH}" }}', [DEFAULT]),
    ('{{ "unterminated }}', [DEFAULT]),
]


def lex(source: str, settings: dict) -> dict:
    env = Environment(**settings)
    try:
        tokens = []
        for tok in env.lexer.tokenize(source, "<lex>"):
            if tok.type == TOKEN_EOF:
                break
            value = tok.value
            if tok.type in ("integer", "float"):
                value = repr(value)
            tokens.append([tok.lineno, tok.type, value])
    except Exception as exc:  # noqa: BLE001 - the failure is the expectation
        # Use .message, not str(exc): going through Environment.get_template
        # marks the error "translated", after which str() is the bare message.
        # Calling the lexer directly skips that, and would otherwise record a
        # File/line footer no user of the library ever sees.
        return {
            "err": type(exc).__name__,
            "msg": getattr(exc, "message", None) or str(exc),
            "line": getattr(exc, "lineno", 0),
        }
    return {"tokens": tokens}


def main() -> int:
    rows = []
    for source, variants in CASES:
        for settings in variants:
            row = {"src": source, "syntax": settings}
            row.update(lex(source, settings))
            rows.append(row)

    OUT.parent.mkdir(parents=True, exist_ok=True)
    OUT.write_text(
        "\n".join(json.dumps(r, ensure_ascii=False) for r in rows) + "\n",
        encoding="utf-8",
    )
    errors = sum(1 for r in rows if "err" in r)
    print(f"{OUT.name}: {len(rows)} cases ({errors} error cases)", file=sys.stderr)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
