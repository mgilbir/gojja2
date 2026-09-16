#!/usr/bin/env python3
# Copyright 2026 The gojja2 Authors
# SPDX-License-Identifier: Apache-2.0
"""Write gojja2's own conformance corpus.

The imported corpora -- MiniJinja's fixtures and the templates harvested from
Jinja's suite -- are gitignored, so a fresh checkout would otherwise have
almost no conformance coverage. This corpus is committed, along with the
goldens CPython produced for it, so `go test ./...` grades against the real
thing without a network or a Python interpreter.

Cases are written here rather than as loose files so that a whole area can be
covered in a few lines, and so the contexts are visible next to the templates
they feed.
"""

from __future__ import annotations

import json
import shutil
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
DST = ROOT / "testdata" / "corpus"
SEPARATOR = "\n---\n"

CASES: list[tuple[str, str, dict]] = []


def case(name: str, template: str, **header) -> None:
    CASES.append((name, template, header))


# Shared contexts, so a filter case reads as the filter and not as its setup.
SEQ = {"seq": [1, 2, 3, 4, 5]}
WORDS = {"words": ["banana", "Apple", "cherry"]}
USERS = {
    "users": [
        {"name": "ana", "age": 30, "city": "Lisbon"},
        {"name": "bo", "age": 25, "city": "Porto"},
        {"name": "cy", "age": 30, "city": "Lisbon"},
    ]
}
TEXT = {"text": "  the quick brown fox jumps over the lazy dog  "}
MAP = {"map": {"b": 2, "a": 1, "C": 3}}

# --- literals and operators ---------------------------------------------------
case("literals/numbers", "{{ 1 }}|{{ -1 }}|{{ 1.5 }}|{{ 1e3 }}|{{ 0x1f }}|{{ 0o17 }}|{{ 0b101 }}|{{ 1_000 }}")
case("literals/bignum", "{{ 2 ** 100 }}|{{ 9223372036854775807 + 1 }}|{{ -(2**70) }}")
case("literals/floats", "{{ 0.1 + 0.2 }}|{{ 1e16 }}|{{ 1e-5 }}|{{ 1/3 }}|{{ 10/2 }}")
case("literals/strings", r"""{{ "a\nb" }}|{{ 'it\'s' }}|{{ "\x41é" }}|{{ "a" "b" }}""")
case("literals/containers", "{{ [] }}|{{ [1,2,] }}|{{ () }}|{{ (1,) }}|{{ {'a':1,} }}|{{ {} }}")
case("literals/dict_int_keys", '{% set d = {1:"a", 2:"b",} %}{{ d[1] }}{{ d[2] }}{{ d }}')
case("literals/dict_key_identity", '{% set d = {1:"x"} %}{{ d[true] }}{{ d[1.0] }}|{{ {1:"a", 1.0:"b", True:"c"} }}')
case("literals/dict_tuple_keys", '{% set d = {(1,2):"a"} %}{{ d[(1,2)] }}|{{ d }}')
case("literals/constants", "{{ true }}{{ True }}{{ false }}{{ False }}{{ none }}{{ None }}")

case("operators/arithmetic", "{{ 7+2 }}|{{ 7-2 }}|{{ 7*2 }}|{{ 7/2 }}|{{ 7//2 }}|{{ 7%2 }}|{{ 7**2 }}")
case("operators/negative_floordiv", "{{ -7//2 }}|{{ -7%2 }}|{{ 7//-2 }}|{{ 7%-2 }}|{{ -7.0//2 }}|{{ -7.0%2 }}")
case("operators/precedence", "{{ 2 ** 3 ** 2 }}|{{ -2 ** 2 }}|{{ 1 + 2 * 3 }}|{{ 'a' ~ 1 + 2 }}")
case("operators/concat", "{{ 'a' ~ 1 ~ none ~ true ~ [1] }}")
case("operators/comparison", "{{ 1 < 2 < 3 }}|{{ 3 > 2 > 3 }}|{{ 1 == 1.0 }}|{{ 1 == true }}|{{ 'a' == 1 }}")
case("operators/membership", "{{ 1 in [1,2] }}|{{ 'a' in 'cat' }}|{{ 'a' in {'a':1} }}|{{ 1 not in [1] }}")
case("operators/logic", "{{ [] and 1 }}|{{ [] or 1 }}|{{ 0 or 'x' }}|{{ not [] }}|{{ not 1 }}")
case("operators/repetition", "{{ 'ab' * 2 }}|{{ 2 * 'ab' }}|{{ [1] * 3 }}|{{ 'ab' * -1 }}|{{ (1,) * 2 }}")
case("operators/sequence_add", "{{ [1] + [2] }}|{{ (1,) + (2,) }}|{{ 'a' + 'b' }}")
case("operators/percent_format", """{{ "%s-%d" % ("a", 5) }}|{{ "%(x)s" % {"x":1} }}|{{ "%05.2f" % 3.14159 }}|{{ "%r|%x|%o" % ("a", 255, 8) }}""")

# --- indexing and slicing -----------------------------------------------------
case("subscript/index", "{{ seq[0] }}|{{ seq[-1] }}|{{ seq.0 }}|{{ seq[10] }}", **SEQ)
case("subscript/slice", "{{ seq[1:3] }}|{{ seq[:2] }}|{{ seq[2:] }}|{{ seq[::2] }}|{{ seq[::-1] }}|{{ seq[:] }}", **SEQ)
case("subscript/slice_neg", "{{ seq[-2:] }}|{{ seq[:-2] }}|{{ seq[-4:-1] }}|{{ seq[5:1:-1] }}", **SEQ)
case("subscript/string", "{{ s[0] }}|{{ s[-1] }}|{{ s[1:4] }}|{{ s[::-1] }}|{{ s|length }}", s="héllo wörld")
case("subscript/dict", "{{ d['a'] }}|{{ d.a }}|{{ d.missing }}|{{ d['missing'] }}", d={"a": 1})
# `.items` finds the method, not the entry, while `['items']` finds the entry.
# The method's repr embeds its address, so only its callability is compared.
case("subscript/attr_fallback", "{{ d.items is callable }}|{{ d['items'] }}|{{ d|attr('items') is callable }}", d={"items": "shadowed"})

# --- control flow -------------------------------------------------------------
case("control/if", "{% if a %}A{% elif b %}B{% else %}C{% endif %}", a=False, b=True)
case("control/if_no_scope", "{% set x = 1 %}{% if true %}{% set x = 2 %}{% endif %}{{ x }}")
case("control/for", "{% for x in seq %}{{ x }}{% endfor %}", **SEQ)
case("control/for_scope", "{% set x = 1 %}{% for i in [1,2] %}[{{ x }}]{% set x = x + 1 %}{% endfor %}[{{ x }}]")
case("control/for_else", "{% for x in [] %}a{% else %}empty{% endfor %}")
case("control/for_filter", "{% for x in seq if x is odd %}{{ loop.index }}:{{ x }} {% endfor %}", **SEQ)
case("control/for_unpack", "{% for a, b in pairs %}{{a}}={{b}} {% endfor %}", pairs=[[1, 2], [3, 4]])
case("control/for_nested_unpack", "{% for a, (b, c) in pairs %}{{a}}{{b}}{{c}} {% endfor %}", pairs=[[1, [2, 3]]])
case("control/loop_vars", "{% for x in seq %}{{ loop.index }}{{ loop.index0 }}{{ loop.revindex }}{{ loop.revindex0 }}{{ loop.first }}{{ loop.last }}{{ loop.length }} {% endfor %}", **SEQ)
case("control/loop_adjacent", "{% for x in seq %}{{ loop.previtem|default('-') }}/{{ loop.nextitem|default('-') }} {% endfor %}", **SEQ)
case("control/loop_cycle", "{% for x in seq %}{{ loop.cycle('a','b') }}{% endfor %}", **SEQ)
case("control/loop_changed", "{% for x in xs %}{{ loop.changed(x) }}{% endfor %}", xs=[1, 1, 2, 2, 1])
case("control/loop_recursive", "{% for item in tree recursive %}[{{ item.name }}{{ loop(item.children) if item.children }}]{% endfor %}",
     tree=[{"name": "a", "children": [{"name": "b", "children": []}]}, {"name": "c", "children": []}])
case("control/for_dict", "{% for k in map %}{{ k }}{% endfor %}|{% for k, v in map|items %}{{k}}{{v}}{% endfor %}", **MAP)
case("control/with", "{% with a = 1, b = a %}{{ a }}{{ b }}{% endwith %}{{ a|default('-') }}", a="outer")
case("control/set_tuple", "{% set a, b = 1, 2 %}{{ a }}{{ b }}")
case("control/set_block", "{% set v %}A{{ 1 }}{% endset %}[{{ v }}]")
case("control/set_block_filter", "{% set v | upper %}ab{% endset %}[{{ v }}]")
case("control/namespace", "{% set ns = namespace(total=0) %}{% for x in seq %}{% set ns.total = ns.total + x %}{% endfor %}{{ ns.total }}", **SEQ)
case("control/filter_block", "{% filter upper|trim %} ab {% endfilter %}")
case("control/loop_controls", "{% for x in seq %}{% if x == 2 %}{% continue %}{% endif %}{% if x == 4 %}{% break %}{% endif %}{{ x }}{% endfor %}",
     __settings__={"extensions": ["loopcontrols"]}, **SEQ)
case("control/do", "{% set l = [] %}{% do l.append(1) %}{% do l.append(2) %}{{ l }}",
     __settings__={"extensions": ["do"]})

# --- frame scoping ------------------------------------------------------------
# Whether a name resolves from the render arguments or from the template's own
# frame depends on which mention comes first, and nested frames read through to
# the root rather than to the arguments.
SCOPE = {"x": 5}
case("scope/set_then_read", "{% set x = 1 %}{{ x }}", **SCOPE)
case("scope/read_then_set", "{{ x }}{% set x = 1 %}", **SCOPE)
case("scope/conditional_set", "{% if false %}{% set x = 9 %}{% endif %}[{{ x }}]", **SCOPE)
case("scope/taken_conditional_set", "{% if true %}{% set x = 9 %}{% endif %}[{{ x }}]", **SCOPE)
case("scope/loop_before_set", "{% for i in [1] %}[{{ x }}]{% endfor %}{% set x = 1 %}", **SCOPE)
case("scope/loop_no_set", "{% for i in [1] %}[{{ x }}]{% endfor %}", **SCOPE)
case("scope/macro_no_set", "{% macro mm() %}[{{ x }}]{% endmacro %}{{ mm() }}", **SCOPE)
case("scope/macro_after_set", "{% macro mm() %}[{{ x }}]{% endmacro %}{% set x = 9 %}{{ mm() }}", **SCOPE)
case("scope/macro_before_set", "{% macro mm() %}[{{ x }}]{% endmacro %}{{ mm() }}{% set x = 9 %}{{ mm() }}", **SCOPE)
case("scope/with_before_set", "{% with %}[{{ x }}]{% endwith %}{% set x = 1 %}", **SCOPE)
case("scope/filter_before_set", "{% filter upper %}[{{ x }}]{% endfilter %}{% set x = 1 %}", **SCOPE)
case("scope/block_before_set", "{% block b %}[{{ x }}]{% endblock %}{% set x = 1 %}", **SCOPE)
case("scope/block_after_set", "{% set x = 9 %}{% block b %}[{{ x }}]{% endblock %}", **SCOPE)
case("scope/setblock_before_set", "{% set v %}[{{ x }}]{% endset %}{{ v }}{% set x = 1 %}", **SCOPE)
case("scope/scoped_block_in_loop", "{% for i in [1] %}{% block b scoped %}[{{ x }}][{{ i }}]{% endblock %}{% endfor %}{% set x = 1 %}", **SCOPE)
case("scope/import_after_use", "{% macro mm() %}[{{ m }}]{% endmacro %}{{ mm() }}{% from 'mac.txt' import m %}",
     __templates__={"mac.txt": "{% macro m(x) %}M{% endmacro %}"}, m=10)
case("scope/loop_conditional_set", "{% for i in [1,2,3] %}{% if loop.first %}{% set c = 0 %}{% endif %}{{ c }}{% endfor %}")

# --- constant folding ---------------------------------------------------------
# A print tag whose whole expression is literal is evaluated at compile time
# through a lookup that swallows failures, so it renders nothing where the same
# subscript raises anywhere else.
case("folding/constant_print", "[{{ 0[1:] }}]")
case("folding/constant_operand", "{{ 0[1:] * -2 }}")
case("folding/constant_in_if", "{% if 0[1:] %}y{% else %}n{% endif %}")
case("folding/constant_dict_slice", "[{{ {'a': 1}[:] }}]")
case("folding/nonconstant_dict_slice", "[{{ d[:] }}]", d={"a": 1})
case("folding/constant_filter", "{% set w = 3[1:]|urlencode %}[{{ w }}]")

# --- macros -------------------------------------------------------------------
case("macro/basic", "{% macro m(a, b=2) %}[{{a}},{{b}}]{% endmacro %}{{ m(1) }}{{ m(1,3) }}{{ m(b=4, a=5) }}")
case("macro/varargs", "{% macro m(a) %}{{a}}|{{ varargs }}|{{ kwargs }}{% endmacro %}{{ m(1,2,3,x=4) }}")
case("macro/no_varargs", "{% macro m(a) %}{{a}}{% endmacro %}{{ m(1,2) }}")
case("macro/no_kwargs", "{% macro m(a) %}{{a}}{% endmacro %}{{ m(1,b=2) }}")
case("macro/attributes", "{% macro m(a, b=1) %}{{ m.name }}|{{ m.arguments }}|{{ m.catch_kwargs }}|{{ m.catch_varargs }}|{{ m.caller }}{% endmacro %}{{ m(1) }}")
case("macro/call", "{% macro m() %}<{{ caller() }}>{% endmacro %}{% call m() %}BODY{% endcall %}")
case("macro/call_args", "{% macro m() %}{{ caller(1,2) }}{% endmacro %}{% call(a, b) m() %}{{a}}-{{b}}{% endcall %}")
case("macro/closure", "{% for i in [7] %}{% macro m() %}[{{ i }}]{% endmacro %}{{ m() }}{% endfor %}")
case("macro/late_binding", "{% macro m() %}{{ x|default('none') }}{% endmacro %}{% set x = 5 %}{{ m() }}")
case("macro/missing_arg", "{% macro m(a, b) %}{{a}}{{b}}{% endmacro %}{{ m(1) }}")

# --- inheritance --------------------------------------------------------------
LAYOUT = {"base.html": "[{% block title %}base title{% endblock %}|{% block body %}base body{% endblock %}]"}
case("inherit/override", "{% extends 'base.html' %}{% block body %}child{% endblock %}", __templates__=LAYOUT)
case("inherit/super", "{% extends 'base.html' %}{% block body %}<{{ super() }}>{% endblock %}", __templates__=LAYOUT)
case("inherit/before_extends", "BEFORE{% extends 'base.html' %}AFTER{% block body %}c{% endblock %}TAIL", __templates__=LAYOUT)
case("inherit/set_visible_in_block", "{% extends 'base.html' %}{% set v = 'V' %}{% block body %}{{ v }}{% endblock %}", __templates__=LAYOUT)
case("inherit/three_level", "{% extends 'mid.html' %}{% block body %}C{{ super() }}{% endblock %}",
     __templates__={**LAYOUT, "mid.html": "{% extends 'base.html' %}{% block body %}M{{ super() }}{% endblock %}"})
case("inherit/dynamic", "{% extends parent %}{% block body %}c{% endblock %}", __templates__=LAYOUT, parent="base.html")
case("inherit/select", "{% extends ['nope.html', 'base.html'] %}{% block body %}c{% endblock %}", __templates__=LAYOUT)
case("inherit/self", "{% block b %}B{% endblock %}|{{ self.b() }}")
case("inherit/block_in_loop", "{% for i in [1,2] %}{% block b %}[{{ i|default('-') }}]{% endblock %}{% endfor %}")
case("inherit/block_in_loop_scoped", "{% for i in [1,2] %}{% block b scoped %}[{{ i }}]{% endblock %}{% endfor %}")
case("inherit/endblock_name", "{% block b %}x{% endblock b %}")

# --- include and import -------------------------------------------------------
INC = {"inc.html": "[{{ v|default('none') }}]", "mac.html": "{% macro f(x) %}<{{x}}>{% endmacro %}{% set exported = 'E' %}"}
case("include/basic", "{% set v = 'V' %}{% include 'inc.html' %}", __templates__=INC)
case("include/without_context", "{% set v = 'V' %}{% include 'inc.html' without context %}", __templates__=INC)
case("include/in_loop", "{% for v in [1,2] %}{% include 'inc.html' %}{% endfor %}", __templates__=INC)
case("include/missing", "A{% include 'nope.html' %}B", __templates__=INC)
case("include/ignore_missing", "A{% include 'nope.html' ignore missing %}B", __templates__=INC)
case("import/module", "{% import 'mac.html' as m %}{{ m.f(1) }}{{ m.exported }}", __templates__=INC)
case("import/from", "{% from 'mac.html' import f, f as g %}{{ f(1) }}{{ g(2) }}", __templates__=INC)
case("import/from_missing", "{% from 'mac.html' import nope %}{{ nope }}", __templates__=INC)

# --- context-free includes ----------------------------------------------------
# `{% include ... without context %}` yields into the enclosing function's
# output in jinja2, bypassing any {% filter %} or block {% set %} buffer around
# it, so the included text is neither filtered nor escaped and lands first.
BYPASS = {"inc.txt": "<x>"}
case("include/filter_bypass", "{% filter escape %}{% include 'inc.txt' without context %}{% endfilter %}",
     __templates__=BYPASS)
case("include/filter_with_context", "{% filter escape %}{% include 'inc.txt' %}{% endfilter %}",
     __templates__=BYPASS)
case("include/filter_bypass_order", "{% filter upper %}a{% include 'inc.txt' without context %}b{% endfilter %}",
     __templates__=BYPASS)
case("include/setblock_bypass", "{% set v %}{% include 'inc.txt' without context %}{% endset %}[{{ v }}]",
     __templates__=BYPASS)

# --- whitespace ---------------------------------------------------------------
for name, settings in [
    ("default", {}),
    ("trim", {"trim_blocks": True}),
    ("lstrip", {"lstrip_blocks": True}),
    ("both", {"trim_blocks": True, "lstrip_blocks": True}),
]:
    case(f"whitespace/{name}", "a\n   {% if true %}\nb\n   {% endif %}\nc", __settings__=settings)
    case(f"whitespace/{name}_controls", "  \n  {{- 1 -}}  \n  {%- if true -%}  x  {%- endif -%}  ", __settings__=settings)
case("whitespace/plus", "   {%+ if true %}x{% endif %}", __settings__={"lstrip_blocks": True})
case("whitespace/plus_end", "{% if true +%}\nx{% endif %}", __settings__={"trim_blocks": True})
case("whitespace/keep_trailing", "a\n", __settings__={"keep_trailing_newline": True})
case("whitespace/drop_trailing", "a\n")
case("whitespace/raw", "{% raw %}{{ x }}{% endraw %}|{%- raw -%}  y  {%- endraw -%}|")
case("whitespace/comment", "a{# c #}b|a{#- c -#}b")
case("whitespace/line_statements", "# for x in [1,2]\n{{ x }}\n# endfor\nrest ## trailing\n",
     __settings__={"line_statement_prefix": "#", "line_comment_prefix": "##"})
case("whitespace/custom_delims", "<< x >><% if true %>y<% endif %><# c #>",
     __settings__={"block_start_string": "<%", "block_end_string": "%>",
                   "variable_start_string": "<<", "variable_end_string": ">>",
                   "comment_start_string": "<#", "comment_end_string": "#>"}, x=1)

# --- escaping -----------------------------------------------------------------
case("escape/off", "{{ v }}|{{ v|escape }}|{{ v|safe }}", v="<b>&'\"")
case("escape/on", "{{ v }}|{{ v|escape }}|{{ v|safe }}|{{ v|safe|escape }}|{{ v|safe|forceescape }}",
     __settings__={"autoescape": True}, v="<b>&'\"")
case("escape/concat", "{{ a ~ b }}|{{ [a, b]|join('-') }}", __settings__={"autoescape": True}, a="<x>", b="<y>")
case("escape/block", "{% autoescape true %}{{ v }}{% endautoescape %}{{ v }}", v="<b>")
case("escape/block_off", "{% autoescape false %}{{ v }}{% endautoescape %}{{ v }}",
     __settings__={"autoescape": True}, v="<b>")
case("escape/markup_format", '{{ ("a{}b"|safe).format(v) }}', __settings__={"autoescape": True}, v="<x>")
case("escape/literal_data", "<b>{{ v }}</b>", __settings__={"autoescape": True}, v="<i>")
case("escape/macro", "{% macro m(x) %}<i>{{ x }}</i>{% endmacro %}{{ m(v) }}",
     __settings__={"autoescape": True}, v="<b>")

# --- undefined ----------------------------------------------------------------
for kind in ["default", "chainable", "debug", "strict"]:
    case(f"undefined/{kind}_print", "[{{ nope }}]", __settings__={"undefined": kind})
    case(f"undefined/{kind}_attr", "[{{ nope.attr }}]", __settings__={"undefined": kind})
    case(f"undefined/{kind}_bool", "{% if nope %}y{% else %}n{% endif %}", __settings__={"undefined": kind})
    case(f"undefined/{kind}_iter", "{% for x in nope %}{{ x }}{% endfor %}", __settings__={"undefined": kind})
case("undefined/messages", "{{ d.missing + 1 }}", d={"a": 1})
case("undefined/index_message", "{{ seq[42] + 1 }}", **SEQ)
case("undefined/arith", "{{ nope + 1 }}")
case("undefined/tests", "{{ nope is defined }}{{ nope is undefined }}{{ nope == nope }}{{ nope|default('d') }}")
case("undefined/length", "{{ nope|length }}|{{ nope|list }}|{{ nope|join(',') }}")

# --- errors -------------------------------------------------------------------
case("errors/syntax_unclosed", "{% if x %}")
case("errors/syntax_unexpected", "{{ 1 + }}")
case("errors/unknown_tag", "{% nope %}")
case("errors/unknown_filter", "{{ 1|nosuch }}")
case("errors/unknown_filter_soft", "{% if true %}{{ 1|nosuch }}{% endif %}")
case("errors/unknown_test", "{{ 1 is nosuch }}")
case("errors/bad_assign", "{% set 1 = 2 %}")
case("errors/nested_mismatch", "{% for x in [1] %}{% endif %}")
case("errors/zero_division", "{{ 1/0 }}|{{ 1//0 }}|{{ 1%0 }}|{{ 1.0/0 }}")
case("errors/type_mismatch", "{{ 1 + 'a' }}")
case("errors/block_twice", "{% block b %}{% endblock %}{% block b %}{% endblock %}")
case("errors/loop_assign", "{% for loop in [1] %}{% endfor %}")
case("errors/underscore_import", "{% from 'x' import _y %}")

# --- filters ------------------------------------------------------------------
case("filters/case", "{{ 'hello wOrld' | upper }}|{{ 'HELLO' | lower }}|{{ \"foo's bar-baz\" | title }}|{{ 'hELLO' | capitalize }}")
case("filters/trim", "[{{ text|trim }}]|[{{ 'xxaxx'|trim('x') }}]", **TEXT)
case("filters/default", "{{ nope|default('d') }}|{{ ''|default('d') }}|{{ ''|default('d', true) }}|{{ 0|default('d', boolean=true) }}")
case("filters/length", "{{ seq|length }}|{{ seq|count }}|{{ 'héllo'|length }}|{{ map|length }}", **SEQ, **MAP)
case("filters/join", "{{ seq|join('-') }}|{{ users|join(', ', attribute='name') }}", **SEQ, **USERS)
case("filters/first_last", "{{ seq|first }}|{{ seq|last }}|{{ []|first }}|{{ []|last }}", **SEQ)
case("filters/sort", "{{ words|sort }}|{{ words|sort(case_sensitive=true) }}|{{ words|sort(reverse=true) }}", **WORDS)
case("filters/sort_attr", "{{ users|sort(attribute='age')|map(attribute='name')|list }}|{{ users|sort(attribute='age,name')|map(attribute='name')|list }}", **USERS)
case("filters/dictsort", "{{ map|dictsort }}|{{ map|dictsort(by='value') }}|{{ map|dictsort(case_sensitive=true) }}|{{ map|dictsort(reverse=true) }}", **MAP)
case("filters/unique", "{{ [1,2,1,3]|unique|list }}|{{ ['a','A','b']|unique|list }}|{{ ['a','A','b']|unique(case_sensitive=true)|list }}")
case("filters/min_max", "{{ seq|min }}|{{ seq|max }}|{{ users|min(attribute='age') }}|{{ []|min }}", **SEQ, **USERS)
case("filters/sum", "{{ seq|sum }}|{{ users|sum(attribute='age') }}|{{ []|sum }}|{{ seq|sum(start=10) }}", **SEQ, **USERS)
case("filters/batch", "{{ seq|batch(2)|list }}|{{ seq|batch(2, 'X')|list }}", **SEQ)
case("filters/slice", "{{ seq|slice(3)|list }}|{{ seq|slice(3, 'X')|list }}|{{ []|slice(2, 'X')|list }}", **SEQ)
case("filters/groupby", "{{ users|groupby('city') }}", **USERS)
case("filters/map", "{{ seq|map('string')|list }}|{{ users|map(attribute='name')|list }}|{{ users|map(attribute='nope', default='?')|list }}", **SEQ, **USERS)
case("filters/select", "{{ seq|select('odd')|list }}|{{ seq|reject('odd')|list }}|{{ [0,1,'',2]|select|list }}", **SEQ)
case("filters/selectattr", "{{ users|selectattr('age', 'eq', 30)|map(attribute='name')|list }}|{{ users|rejectattr('age', 'eq', 30)|map(attribute='name')|list }}", **USERS)
case("filters/reverse", "{{ seq|reverse|list }}|{{ 'abc'|reverse }}", **SEQ)
case("filters/numbers", "{{ '3.7'|int }}|{{ 3.7|int }}|{{ 'x'|int }}|{{ 'x'|int(9) }}|{{ 'ff'|int(0, 16) }}|{{ '3.7'|float }}|{{ 'x'|float(1.5) }}")
case("filters/round", "{{ 2.5|round }}|{{ 3.5|round }}|{{ 2.675|round(2) }}|{{ 2.1|round(0,'ceil') }}|{{ 2.9|round(0,'floor') }}")
case("filters/abs", "{{ -3|abs }}|{{ -3.5|abs }}|{{ (-2**70)|abs }}")
case("filters/string_ops", "{{ 'a-b'|replace('-','+') }}|{{ 'aaa'|replace('a','b',2) }}|{{ 'ab'|center(6) }}|{{ text|wordcount }}", **TEXT)
case("filters/indent", "{{ 'a\\nb\\n\\nc'|indent(2) }}|{{ 'a\\nb'|indent(2, true) }}|{{ 'a\\n\\nb'|indent(2, blank=true) }}")
case("filters/truncate", "{{ text|truncate(20) }}|{{ text|truncate(20, true) }}|{{ 'short'|truncate(20) }}", **TEXT)
case("filters/wordwrap", "{{ text|wordwrap(10) }}", **TEXT)
case("filters/striptags", "{{ '<p>a  <b>b</b></p>'|striptags }}|{{ '&lt;a&gt;'|striptags }}")
case("filters/format", "{{ '%s-%d'|format('a', 5) }}|{{ '%(x)s'|format(x=1) }}")
case("filters/filesizeformat", "{{ 1|filesizeformat }}|{{ 1000|filesizeformat }}|{{ 1000000|filesizeformat }}|{{ 1024|filesizeformat(true) }}")
case("filters/urlencode", "{{ 'a b/c?d'|urlencode }}|{{ {'a':'1 2'}|urlencode }}")
case("filters/urlize", "{{ 'see http://example.com/a?b=1, and www.x.org. mail me@example.com'|urlize }}")
case("filters/urlize_args", "{{ 'go to http://example.com now'|urlize(10, target='_blank') }}")
case("filters/tojson", "{{ {'b':1,'a':[1,2],'c':'<x>'}|tojson }}|{{ [1,2]|tojson(indent=2) }}")
case("filters/xmlattr", "{{ {'class':'a b','id':none,'data-x':1}|xmlattr }}")
case("filters/pprint", "{{ map|pprint }}|{{ 'a'|pprint }}", **MAP)
case("filters/attr", "{{ d|attr('a') }}|{{ d|attr('missing') }}", d={"a": 1})
case("filters/items", "{{ map|items|list }}", **MAP)
case("filters/list_string", "{{ 'abc'|list }}|{{ map|list }}|{{ 1|string }}|{{ none|string }}", **MAP)

# --- markupsafe ---------------------------------------------------------------
# Markup absorbs whatever it is joined to, escaping it and staying Markup, and
# it reports itself as Markup in type errors.
case("markup/concat", "{{ ('<b>'|safe) + '<i>' }}|{{ 'a<i>' + ('<b>'|safe) }}|{{ ('<b>'|safe) + ('<i>'|safe) }}")
case("markup/type_name", "{{ ('a'|safe) + 1 }}")
case("markup/preserved", "{{ ('<b>'|safe)|upper|pprint }}|{{ ('a b'|safe)|trim|pprint }}|{{ ('ab'|safe)|title|pprint }}")
case("markup/indexing", "{{ ('ab'|safe)[0]|pprint }}|{{ ('ab'|safe)|last|pprint }}|{{ ('ab'|safe)|first|pprint }}")
case("markup/tojson_is_markup", "{{ ([1]|tojson)|pprint }}")

# --- textwrap and pprint ------------------------------------------------------
case("layout/wordwrap_escaped", "{{ {1: 'a', 2: 'b'}|urlize|wordwrap(10) }}")
case("layout/wordwrap_hyphens", "{{ 'a-very-long-hyphenated-word here'|wordwrap(8) }}")
case("layout/wordwrap_nobreak", "{{ 'abcdefghij kl'|wordwrap(5, false) }}|{{ 'abcdefghij kl'|wordwrap(4) }}")
case("layout/pprint_wrapping", "{{ users|pprint }}|{{ {'a': users}|pprint }}", **USERS)
case("layout/indent_empty", "[{{ ''|indent(2, true) }}]")

# --- error shapes -------------------------------------------------------------
case("errshape/sort_mixed", "{{ mix|sort }}", mix=[1, "a", 2.5, True, None])
case("errshape/sort_mixed_reverse", "{{ mix|sort(true) }}", mix=[1, "a", 2.5, True, None])
case("errshape/indent_types", "{{ nope|indent(2) }}")
case("errshape/indent_tuple", "{{ (1, 2)|indent(2) }}")
case("errshape/truncate_list", "{{ long|truncate(3) }}", long=list("abcdefghijklmnopqrst"))
case("errshape/groupby_tuple", "{% for g in users|groupby('city') %}[{{ g.grouper }}:{{ g.list|length }}]{% endfor %}|{{ users|groupby('city') }}", **USERS)
case("errshape/range_slice", "{{ range(3)[::2] }}|{{ range(10)[2:8:3] }}|{{ range(0,10,2)[1:4] }}")
case("errshape/format_char", "{{ '%S' % 'a' }}")
case("errshape/callable_arity", "{{ 1 is callable(2) }}")
case("errshape/wordcount_unicode", "{{ ['héllo', 1]|wordcount }}")

# --- tests --------------------------------------------------------------------
case("tests/kinds", "{{ 1 is integer }}{{ 1.0 is float }}{{ 1 is number }}{{ 'a' is string }}{{ [] is sequence }}{{ {} is mapping }}{{ none is none }}{{ true is boolean }}")
case("tests/truth", "{{ true is true }}{{ 1 is true }}{{ false is false }}{{ 0 is false }}")
case("tests/numbers", "{{ 3 is odd }}{{ 4 is even }}{{ 9 is divisibleby(3) }}{{ 9 is divisibleby 4 }}")
case("tests/defined", "{{ x is defined }}{{ nope is defined }}{{ nope is undefined }}{{ nope is not defined }}", x=1)
case("tests/comparison", "{{ 1 is eq 1 }}{{ 1 is ne 2 }}{{ 1 is lt 2 }}{{ 2 is ge 2 }}{{ 'a' is in ['a'] }}")
case("tests/case", "{{ 'abc' is lower }}{{ 'ABC' is upper }}{{ 'Abc' is lower }}{{ '123' is upper }}")
case("tests/callable_iterable", "{{ range is callable }}{{ [] is iterable }}{{ 1 is iterable }}{{ 'a' is iterable }}")
case("tests/sameas", "{{ none is sameas none }}{{ true is sameas true }}{{ 1 is sameas 1.0 }}")
case("tests/filter_test", "{{ 'upper' is filter }}{{ 'nope' is filter }}{{ 'odd' is test }}{{ 'nope' is test }}")
case("tests/escaped", "{{ 'a'|safe is escaped }}{{ 'a' is escaped }}")

# --- globals ------------------------------------------------------------------
case("globals/range", "{{ range(3)|list }}|{{ range(1,4)|list }}|{{ range(0,10,3)|list }}|{{ range(3,0,-1)|list }}|{{ range(3) }}")
case("globals/dict", "{{ dict(a=1, b=2) }}|{{ dict({'a':1}, b=2) }}")
case("globals/namespace", "{% set ns = namespace(a=1) %}{{ ns.a }}{{ ns.missing }}|{{ ns }}")
case("globals/cycler", "{% set c = cycler('a','b') %}{{ c.next() }}{{ c.next() }}{{ c.current }}{{ c.next() }}")
case("globals/joiner", "{% set j = joiner('; ') %}{% for x in [1,2,3] %}{{ j() }}{{ x }}{% endfor %}")

# --- string methods -----------------------------------------------------------
case("methods/string", "{{ 'a,b,c'.split(',') }}|{{ ' a  b '.split() }}|{{ '-'.join(['a','b']) }}|{{ 'abc'.startswith('a') }}|{{ 'abc'.find('b') }}")
case("methods/string_more", "{{ 'a'.center(5,'*') }}|{{ '5'.zfill(3) }}|{{ 'a\\nb'.splitlines() }}|{{ 'AbC'.swapcase() }}|{{ 'a{0}b{x}'.format(1, x=2) }}")
case("methods/dict", "{{ map.keys()|list }}|{{ map.values()|list }}|{{ map.items()|list }}|{{ map.get('a') }}|{{ map.get('z', 'd') }}", **MAP)
case("methods/list", "{% set l = [3,1,2] %}{{ l.index(1) }}{{ l.count(3) }}{% do l.append(4) %}{% do l.reverse() %}{{ l }}",
     __settings__={"extensions": ["do"]})


def main() -> int:
    if DST.exists():
        shutil.rmtree(DST)

    for name, template, header in CASES:
        path = DST / (name + ".jj2")
        path.parent.mkdir(parents=True, exist_ok=True)
        text = json.dumps(header, ensure_ascii=False, indent=2) if header else "{}"
        path.write_text(text + SEPARATOR + template, encoding="utf-8")

    print(f"wrote {len(CASES)} cases into {DST.relative_to(ROOT)}", file=sys.stderr)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
