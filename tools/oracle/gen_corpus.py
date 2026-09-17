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
# A name merely mentioned inside an {% if %} branch settles at the enclosing
# level, so a later assignment no longer claims it.
case("scope/branch_load_then_set", "{% if false %}{% else %}[{{ m }}]{% endif %}{% from 'mac.txt' import m %}",
     __templates__={"mac.txt": "{% macro m(x) %}M{% endmacro %}"}, m=10)
case("scope/branch_test_then_set", "{% if m %}[{{ m }}]{% endif %}{% from 'mac.txt' import m %}",
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
# The escaping in force is not one setting per template. Text escapes by where
# it was written -- which for a macro body is where the macro was defined --
# while a filter escapes by where it is *called*, because jinja2 hands it the
# context's eval context. A macro can therefore print by one setting and be
# trusted by another within a single call.
AE = {"autoescape": True}
case("escape/block_reaches_filter", "{% autoescape false %}{{ ([s, mk|safe]|join(sep))|pprint }}|{{ [s, mk|safe]|join(sep) }}{% endautoescape %}",
     __settings__=AE, s="a", mk="&", sep="&")
case("escape/block_reaches_filter_on", "{% autoescape true %}{{ ([s, mk|safe]|join(sep))|pprint }}|{{ [s, mk|safe]|join(sep) }}{% endautoescape %}",
     s="a", mk="&", sep="&")
case("escape/block_volatile", "{% autoescape v %}{{ ([s, mk|safe]|join(sep))|pprint }}{% endautoescape %}|{% autoescape w %}{{ ([s, mk|safe]|join(sep))|pprint }}{% endautoescape %}",
     __settings__=AE, v=False, w=True, s="a", mk="&", sep="&")
case("escape/block_constant_fold", "{% autoescape false %}{{ (['a', '&'|safe]|join('&'))|pprint }}{% endautoescape %}",
     __settings__=AE)
# A {% block %} body is compiled against a fresh eval context, so what is
# folded inside one does not see the surrounding {% autoescape %} even though
# what is left for run time does.
case("escape/named_block_folds_with_environment", "{% autoescape true %}{% block b %}{{ (['a', '&'|safe]|join('&'))|pprint }}|{{ ([s, mk|safe]|join(sep))|pprint }}{% endblock %}{% endautoescape %}",
     s="a", mk="&", sep="&")
case("escape/macro_prints_where_written", "{% macro m(x) %}{{ ([x, mk|safe]|join(sep))|pprint }}/{{ [x, mk|safe]|join(sep) }}{% endmacro %}{% autoescape false %}{{ m(s) }}{% endautoescape %}",
     __settings__=AE, s="a", mk="&", sep="&")
case("escape/macro_trusted_where_called", "{% macro m(x) %}{{ [x, mk|safe]|join(sep) }}{% endmacro %}{% autoescape true %}[{{ m(s) is escaped }}][{{ m(s) }}]{% endautoescape %}",
     s="a", mk="&", sep="&")

# do_replace's autoescaping rule is finer than "escape everything": `old` is
# matched verbatim, `new` is escaped only when the subject it replaces into is
# Markup, and a plain subject with plain arguments stays a plain string. The
# subject is escaped only when a Markup `old` -- or a Markup `new` against a
# plain subject -- means the result has to be Markup.
case("escape/replace_plain", "{{ (s|replace(o, n))|pprint }}|{{ (s|replace(o, n)) is escaped }}|{{ s|replace(o, n) }}",
     __settings__={"autoescape": True}, s="a&<b", o="&", n="+")
case("escape/replace_markup_subject", "{{ (s|safe|replace(o, n))|pprint }}|{{ s|safe|replace(o, n) }}",
     __settings__={"autoescape": True}, s="a&<b", o="&", n="<i>")
case("escape/replace_markup_old", "{{ (s|replace(o|safe, n))|pprint }}|{{ s|replace(o|safe, n) }}",
     __settings__={"autoescape": True}, s="a&<b", o="&", n="<i>")
case("escape/replace_markup_new", "{{ (s|replace(o, n|safe))|pprint }}|{{ s|replace(o, n|safe) }}",
     __settings__={"autoescape": True}, s="a&<b", o="a", n="<i>")
case("escape/replace_markup_both", "{{ (s|safe|replace(o, n|safe))|pprint }}|{{ s|safe|replace(o, n|safe) }}",
     __settings__={"autoescape": True}, s="a&<b", o="a", n="<i>")
case("escape/replace_no_autoescape", "{{ (s|replace(o, n))|pprint }}|{{ s|replace(o, n) }}",
     s="a&<b", o="&", n="<i>")
case("escape/replace_constant", '{{ ("a&<b"|replace("&", "+"))|pprint }}|{{ "a&<b"|replace("&", "+") }}',
     __settings__={"autoescape": True})

# do_join only coerces to Markup when there is markup to preserve. With none,
# an autoescaping join is a plain string of str()s and the escaping happens at
# output -- which is invisible in `{{ xs|join(",") }}` and decides everything
# else the result is used for. The context supplies the operands so that
# constant folding cannot answer these instead.
case("escape/join_plain", "{{ (xs|join(sep))|pprint }}|{{ (xs|join(sep)) is escaped }}|{{ xs|join(sep)|length }}|{{ xs|join(sep) }}",
     __settings__={"autoescape": True}, xs=["a'", "<i>"], sep="&")
case("escape/join_markup_item", "{{ ([a, b|safe]|join(sep))|pprint }}|{{ ([a, b|safe]|join(sep)) is escaped }}|{{ [a, b|safe]|join(sep) }}",
     __settings__={"autoescape": True}, a="a'", b="<i>", sep="&")
case("escape/join_markup_sep", "{{ ([a, b]|join(sep|safe))|pprint }}|{{ [a, b]|join(sep|safe) }}",
     __settings__={"autoescape": True}, a="a'", b="<i>", sep="&")
case("escape/join_no_autoescape", "{{ ([a, b|safe]|join(sep))|pprint }}|{{ [a, b|safe]|join(sep) }}",
     a="a'", b="<i>", sep="&")
case("escape/join_attribute", "{{ (users|join(sep, attribute='city'))|pprint }}",
     __settings__={"autoescape": True}, sep="&", **USERS)
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
# Only complete tags and comments go; an unpaired "<" stays.
case("filters/striptags_partial", "{{ '<'|striptags }}|{{ '<b'|striptags }}|{{ 'a<!--c-->b'|striptags }}|{{ 'a < b'|striptags }}")
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
case("markup/add_nonstring", "{{ 0 + ('a'|safe) }}")
case("markup/add_to_list", "{{ ['a'] + ('b'|safe) }}")
case("markup/range_equality", "{{ range(3,0,-1) == range(3,0,-1) }}{{ range(0,3,2) == range(0,4,2) }}{{ range(3) == [0,1,2] }}")
case("markup/groupby_json", "{{ {'a': 1}|groupby('city')|list|tojson }}")
case("markup/preserved", "{{ ('<b>'|safe)|upper|pprint }}|{{ ('a b'|safe)|trim|pprint }}|{{ ('ab'|safe)|title|pprint }}")
case("markup/indexing", "{{ ('ab'|safe)[0]|pprint }}|{{ ('ab'|safe)|last|pprint }}|{{ ('ab'|safe)|first|pprint }}")
case("markup/percent_format", "{{ ('%s'|safe) % '<i>' }}|{{ (('%s'|safe) % '<i>') is escaped }}|{{ '%s' % '<i>' }}")
case("markup/percent_width", "[{{ ('%10s'|safe) % '<i>' }}]")
case("markup/tojson_is_markup", "{{ ([1]|tojson)|pprint }}")

# --- textwrap and pprint ------------------------------------------------------
case("layout/wordwrap_escaped", "{{ {1: 'a', 2: 'b'}|urlize|wordwrap(10) }}")
case("layout/wordwrap_hyphens", "{{ 'a-very-long-hyphenated-word here'|wordwrap(8) }}")
case("layout/wordwrap_nobreak", "{{ 'abcdefghij kl'|wordwrap(5, false) }}|{{ 'abcdefghij kl'|wordwrap(4) }}")
# A string too long for one line is split at word boundaries, one repr per
# line, parenthesised when it is the outermost value.
case("layout/pprint_long_string", "{{ users|lower|pprint }}", **USERS)
case("layout/pprint_nested_string", "{{ [users|lower]|pprint }}", **USERS)
case("layout/pprint_short_string", "{{ 'short'|pprint }}")
case("layout/pprint_wrapping", "{{ users|pprint }}|{{ {'a': users}|pprint }}", **USERS)
case("layout/indent_empty", "[{{ ''|indent(2, true) }}]")

# --- error shapes -------------------------------------------------------------
case("errshape/sort_mixed", "{{ mix|sort }}", mix=[1, "a", 2.5, True, None])
case("errshape/sort_mixed_reverse", "{{ mix|sort(true) }}", mix=[1, "a", 2.5, True, None])
case("errshape/indent_types", "{{ nope|indent(2) }}")
case("errshape/indent_tuple", "{{ (1, 2)|indent(2) }}")
case("errshape/truncate_list", "{{ long|truncate(3) }}", long=list("abcdefghijklmnopqrst"))
case("errshape/groupby_tuple", "{% for g in users|groupby('city') %}[{{ g.grouper }}:{{ g.list|length }}]{% endfor %}|{{ users|groupby('city') }}", **USERS)
# A tuple subclass -- what |groupby yields -- behaves as the tuple it stands
# for wherever the tuple type is what decides: concatenation, repetition,
# slicing, tuple's own methods, the argument tuple of %, and ordering. It is
# still named _GroupTuple by everything that reports a type.
GROUP = "{% set g = users|groupby('city')|first %}"
case("grouptuple/concat", GROUP + "{{ g + (1,) }}|{{ (1,) + g }}|{{ g + () }}|{{ g + g }}", **USERS)
case("grouptuple/repeat", GROUP + "{{ g * 2 }}|{{ 2 * g }}|{{ g * 0 }}|{{ g * true }}", **USERS)
case("grouptuple/slice", GROUP + "{{ g[:1] }}|{{ g[::-1] }}|{{ g[1:] }}|{{ g[0] }}|{{ g[0:2:1] }}", **USERS)
case("grouptuple/methods", GROUP + "{{ g.index(g.list) }}|{{ g.count('Lisbon') }}|{{ g.grouper }}", **USERS)
case("grouptuple/percent", GROUP + '{{ "%s/%s" % g }}|{{ "%r" % g[0] }}', **USERS)
case("grouptuple/order", GROUP + "{{ g < ('Lisbon', []) }}|{{ ('Lisbon', []) < g }}|{{ g == ('x',) }}", **USERS)
case("grouptuple/equality", GROUP + "{{ g == ('Lisbon', users[:1] + users[2:]) }}|{{ ('Lisbon', []) == g }}|{{ g == ['Lisbon'] }}|{{ g in [('Lisbon', users[:1] + users[2:])] }}", **USERS)
case("grouptuple/sum_start", GROUP + "{{ [g]|sum(start=()) }}", **USERS)

case("errshape/grouptuple_concat_str", GROUP + '{{ g + "s" }}', **USERS)
case("errshape/grouptuple_concat_list", GROUP + "{{ g + [1] }}", **USERS)
case("errshape/grouptuple_list_concat", GROUP + "{{ [1] + g }}", **USERS)
case("errshape/grouptuple_repeat_str", GROUP + '{{ g * "s" }}', **USERS)
case("errshape/grouptuple_str_repeat", GROUP + '{{ "s" * g }}', **USERS)
case("errshape/grouptuple_repeat_float", GROUP + "{{ 2.0 * g }}", **USERS)
case("errshape/grouptuple_order_str", GROUP + '{{ "s" < g }}', **USERS)
case("errshape/grouptuple_order_reflected", GROUP + "{{ (1,) < g }}", **USERS)
case("errshape/grouptuple_percent_extra", GROUP + '{{ "%s" % g }}', **USERS)
case("errshape/grouptuple_indent", GROUP + "{{ g|indent(2) }}", **USERS)
case("errshape/range_slice", "{{ range(3)[::2] }}|{{ range(10)[2:8:3] }}|{{ range(0,10,2)[1:4] }}")
# A literal percent is the two characters "%%" and nothing else: once flags, a
# width, a precision or a mapping key intervene, the '%' is a conversion
# character with no meaning, and it takes an argument before saying so.
case("errshape/format_percent_width", "{{ '%5%' % 1 }}")
case("errshape/format_percent_starved", "{{ '%5%' % () }}")
case("errshape/format_percent_urlencoded", "{{ ({(1,2): 'x'}|urlencode) % 2 }}")
case("markup/format_percent_literal", "{{ '100%% sure, %s' % 'really' }}|{{ '%s%%' % 5 }}")
case("errshape/format_char", "{{ '%S' % 'a' }}")
# The C functions jinja2 registers do not all word an argument error alike:
# abs, len and callable say "takes exactly one argument", the operator.*
# comparisons behind eq and lt say "expected 2 arguments, got N" and name
# themselves _operator.eq when refusing a keyword.
case("errshape/operator_test_arity", "{{ 1 is eq(1,2) }}")
case("errshape/operator_test_too_few", "{{ 1 is eq }}")
case("errshape/operator_test_alias_arity", "{{ 1 is lessthan(1,2) }}")
case("errshape/operator_test_keyword", "{{ 1 is ge(zz=1) }}")
case("errshape/builtin_filter_arity", "{{ [1]|length(2) }}")
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

# --- type objects -------------------------------------------------------------
# __class__ is a real attribute of every value, found before any lookup hook,
# so it resolves even on an undefined.
case("classes/builtin", "{{ true.__class__ }}{{ 1.__class__ }}{{ 'x'.__class__ }}{{ 1.5.__class__ }}{{ none.__class__ }}")
case("classes/containers", "{{ [].__class__ }}{{ {}.__class__ }}{{ (1,).__class__ }}{{ range(3).__class__ }}")
case("classes/markup", "{{ ('x'|safe).__class__ }}|{{ ('x'|safe).__class__.__name__ }}")
case("classes/undefined", "{{ nope.__class__ }}|{{ nope.__class__.__name__ }}")
case("classes/identity", "{{ true.__class__ == true.__class__ }}{{ true.__class__ == 1.__class__ }}{{ true.__class__ is callable }}")
case("classes/via_format", '{{ "a{0.__class__}b".format(42) }}')
case("classes/via_format_markup", '{{ ("a{0.__class__}b{1}"|safe).format(42, "<foo>") }}')
# A dunder name misses like any other attribute on an undefined, where a plain
# name raises straight away.
case("classes/attr_dunder", "{{ nope|attr('__foo__') is undefined }}|{{ nope|attr('__subclasses__') }}")
case("classes/attr_dunder_used", "{{ nope|attr('__foo__')() }}")
case("classes/attr_plain", "{{ nope|attr('items') }}")
case("classes/dot_dunder", "{{ nope.__subclasses__ }}")

# --- globals ------------------------------------------------------------------
case("globals/range", "{{ range(3)|list }}|{{ range(1,4)|list }}|{{ range(0,10,3)|list }}|{{ range(3,0,-1)|list }}|{{ range(3) }}")
case("globals/dict", "{{ dict(a=1, b=2) }}|{{ dict({'a':1}, b=2) }}")
case("globals/namespace", "{% set ns = namespace(a=1) %}{{ ns.a }}{{ ns.missing }}|{{ ns }}")
case("globals/cycler", "{% set c = cycler('a','b') %}{{ c.next() }}{{ c.next() }}{{ c.current }}{{ c.next() }}")
case("globals/joiner", "{% set j = joiner('; ') %}{% for x in [1,2,3] %}{{ j() }}{{ x }}{% endfor %}")

# --- string methods -----------------------------------------------------------
case("methods/string", "{{ 'a,b,c'.split(',') }}|{{ ' a  b '.split() }}|{{ '-'.join(['a','b']) }}|{{ 'abc'.startswith('a') }}|{{ 'abc'.find('b') }}")
# str.format's replacement fields take attribute and index accessors, and the
# attribute form does not fall back to items -- which is why a dict raises.
case("methods/format_fields", '{{ "{0[foo]}".format({"foo": 42}) }}|{{ "{0[0]}".format([1,2]) }}|{{ "{a[b][0]}".format(a={"b":[7]}) }}|{{ "{x[k]}".format_map({"x":{"k":1}}) }}')
case("methods/format_attr_on_dict", '{{ "{0.foo}".format({"foo": 42}) }}')
case("methods/format_index_range", '{{ "{0[9]}".format([1]) }}')
case("methods/string_more", "{{ 'a'.center(5,'*') }}|{{ '5'.zfill(3) }}|{{ 'a\\nb'.splitlines() }}|{{ 'AbC'.swapcase() }}|{{ 'a{0}b{x}'.format(1, x=2) }}")
case("methods/dict", "{{ map.keys()|list }}|{{ map.values()|list }}|{{ map.items()|list }}|{{ map.get('a') }}|{{ map.get('z', 'd') }}", **MAP)
case("methods/list", "{% set l = [3,1,2] %}{{ l.index(1) }}{{ l.count(3) }}{% do l.append(4) %}{% do l.reverse() %}{{ l }}",
     __settings__={"extensions": ["do"]})


# --- cases that were once loose files -----------------------------------------
# These were added straight into testdata/corpus/ rather than here, and this
# script rebuilds that directory from scratch -- so `make oracle`, the
# documented way to regenerate the goldens, quietly deleted them along with the
# regressions they pin. Two of them are the ones that pin the very bugs the
# commit which introduced them had fixed. The generator is the single source of
# truth for the corpus; a case that is not in it does not exist.
case("errshape/dict_pop_arity", "{{ {}.pop() }}")
case("escape/markup_percent_format",
     r"""{{ "%s"|safe|format("<b>") }}|{{ "%s"|format("<b>") }}|{{ "%(a)s"|safe|format(a="<b>") }}|{{ "%s and %s"|safe|format("<a>", "<b>") }}|{{ "%d"|safe|format(5) }}|{{ "%s"|safe|format(["<b>"]) }}|{{ "%s"|safe|format(none) }}|{{ "%s"|safe|format(true) }}|{{ "%s"|safe|format("x"|safe) }}|{{ ("%s"|safe|format("x")).__class__.__name__ }}|{{ ("%s"|format("x")).__class__.__name__ }}""")
case("escape/markup_pprint",
     r"""{{ "x"|safe|pprint }}|{{ "x"|pprint }}|{{ ["a"]|safe|pprint }}|{{ long|escape|pprint }}|{{ long|pprint }}""",
     long="the quick brown fox jumps over the lazy dog and keeps on running well past eighty columns")
case("filters/round_signed_zero",
     r"""{{ -0.0|round(1, 'floor') }}|{{ -0.0|round(1, 'ceil') }}|{{ -0.0|round(1) }}|{{ -0.4|round(0, 'floor') }}|{{ -0.4|round(0, 'ceil') }}|{{ 0.0|round(1, 'floor') }}|{{ -1.5|round(0, 'floor') }}|{{ -0.04|round(1, 'ceil') }}""")
case("macro/default_per_call",
     "{% macro fresh(v=[]) %}{% set _ = v.append(1) %}{{ v }}{% endmacro %}{{ fresh() }} {{ fresh() }}\n"
     "{%- set n = 1 %}{% macro outer(x=n) %}{{ x }}{% endmacro %}{{ outer() }}{% set n = 2 %}{{ outer() }}\n"
     "{%- set L = [9] %}{% macro shared(v=L) %}{% set _ = v.append(1) %}{{ v }}{% endmacro %}{{ shared() }} {{ shared() }} {{ L }}\n"
     "{%- macro chain(a, b=2, c=b) %}{{ a }}{{ b }}{{ c }}{% endmacro %}{{ chain(1) }}\n"
     '{%- macro dct(v={}) %}{% set _ = v.update({"a": 1}) %}{{ v }}{% endmacro %}{{ dct() }} {{ dct() }}')
case("subscript/empty_subscript", "{{ [1,2][] }}|{{ {(): 5}[] }}|{{ {(1,2): 'p'}[1,2] }}|{{ a[] is undefined }}")
case("tests/callable_macro",
     "{% macro local(x) %}{% endmacro %}{% from 'mac.html' import f %}{% import 'mac.html' as mod %}"
     "{{ local is callable }}{{ f is callable }}{{ mod.f is callable }}{{ mod.exported is callable }}{{ local(1) is callable }}",
     __templates__={"mac.html": "{% macro f(x) %}<{{x}}>{% endmacro %}{% set exported = 'E' %}"})

# --- printf-style formatting --------------------------------------------------
# `%` sizes its result from a width the template wrote and lays it out by rules
# that are C's, not Go's. A sweep of 62,000 combinations of flag, width,
# precision, verb and argument found 2,444 of them wrong; these are one row per
# rule that was broken.
case("operators/percent_flags",
     r"""{{ "[%05s][%05r][%05c][%05d][%05d][%-05d]" % ("x", "x", 65, 42, -42, 42) }}""")
case("operators/percent_width",
     r"""{{ "[%5c][%5.2c][%10s][%-8s][%8.3s]" % (65, 65, "éü", "x", "abcdef") }}""")
case("operators/percent_precision",
     r"""{{ "[%.0d][%.3d][%5.0d][%.3x][%.2s][%.0s]" % (0, 5, 0, 255, "éüö", "abc") }}""")
case("operators/percent_alt",
     r"""{{ "[%#o][%#x][%#X][%#08x][%#.0f]" % (8, 255, 255, 255, 1.0) }}""")
case("operators/percent_sign",
     r"""{{ "[%+d][% d][%+08.3f][%08.3f][%+x]" % (42, 42, 1.5, -1.5, 42) }}""")
case("operators/percent_star",
     r"""{{ "[%*s][%-*s][%*s][%.*f][%*.*f][%.*s]" % (5, "x", 5, "x", -5, "x", 3, 1.5, 8, 2, 1.5, -3, "abc") }}""")
case("operators/percent_nonfinite",
     "{% set big = 1e308 %}" +
     r"""{{ "[%f][%f][%F][%E][%09f][%09f][%09f][%+f][%-9f]" % (big*10, -big*10, big*10-big*10, big*10, big*10, -big*10, big*10-big*10, big*10-big*10, big*10-big*10) }}""")
case("operators/percent_wide",
     r"""{{ "[%d][%x][%.30f]" % (2**70, 2**70, 1.5) }}|{{ "%f" % -0.0 }}""")
case("errors/percent_c_type", '{{ "%c" % 1.0 }}')
case("errors/percent_c_range", '{{ "%c" % 1114112 }}')
case("errors/percent_x_float", '{{ "%x" % 1.5 }}')
case("errors/percent_d_string", '{{ "%d" % "x" }}')
case("errors/percent_d_nan", "{% set big = 1e308 %}{{ '%d' % (big*10-big*10) }}")
case("errors/percent_d_undefined", '{{ "%d" % nope }}')
case("errors/percent_x_undefined", '{{ "%x" % nope }}')

# markupsafe's __mod__ wraps each argument in a helper that defines __str__,
# __repr__, __int__ and __float__ and nothing else. Which of those a conversion
# reaches decides the answer, and `%c` reached none of them -- so it used to
# write a raw "<" into a value the template had been told was trusted.
case("escape/markup_percent_verbs",
     r"""{{ ("[%s][%r][%a]"|safe) % ("<b>", "<b>", "<b>") }}|{{ ("[%r]"|safe) % ("<b>"|safe) }}""")
case("escape/markup_percent_numbers",
     r"""{{ ("[%d][%d][%f][%d]"|safe) % (60, "12", "1.5", 1.5) }}""")
case("errors/markup_percent_c", '{{ ("%c"|safe) % 60 }}')
case("errors/markup_percent_x", '{{ ("%x"|safe) % 60 }}')
case("errors/markup_percent_int", '{{ ("%d"|safe) % "x" }}')
case("errors/markup_percent_float", '{{ ("%f"|safe) % "x" }}')
case("errors/markup_percent_none", '{{ ("%d"|safe) % none }}')

# --- other places the output was not CPython's --------------------------------
# tojson wrote "Infinity" and then reset the whole builder to correct the sign,
# which discarded every byte of the document produced so far.
case("filters/tojson_nonfinite",
     "{% set big = 1e308 %}{{ [1, -big*10, 2]|tojson }}|{{ (big*10)|tojson }}|{{ (big*10-big*10)|tojson }}|{{ {'a': 1, 'z': -big*10}|tojson }}")
# A literal outside float64's range is inf or 0.0, not a syntax error.
case("literals/float_range", "{{ 1e999 }}|{{ -1e999 }}|{{ 1e-999 }}|{{ 1e999 == 1e999 }}")
# Python's sum refuses a str start outright and points at join instead.
case("errors/sum_string_start", "{{ ['a','b']|sum(start='') }}")
case("filters/sum_list_start", "{{ [[1],[2]]|sum(start=[]) }}")
# Eight hex digits overflow a rune, so this arrived as -1 and passed a check
# written for values above 0x10FFFF.
case("errors/unicode_escape_overflow", r'{{ "\Uffffffff" }}')
case("literals/unicode_escape", r'{{ "\U0000ffff"|length }}|{{ "\U0010FFFF"|length }}|{{ "é" }}|{{ "\xe9" }}')

# --- membership and slicing ---------------------------------------------------
# `x in range(...)` is arithmetic in Python, not a search.
case("operators/range_membership",
     "{{ 5 in range(10) }}|{{ -1 in range(10) }}|{{ 1.0 in range(3) }}|{{ 1.5 in range(3) }}|"
     "{{ 'x' in range(3) }}|{{ 4 in range(0,10,2) }}|{{ 3 in range(0,10,2) }}|{{ 2 in range(3,0,-1) }}|"
     "{{ 9223372036854775806 in range(9223372036854775807) }}")
case("subscript/slice_steps",
     "{{ 'abcdefg'[::2] }}|{{ 'abcdefg'[::-1] }}|{{ 'abcdefg'[::-2] }}|{{ 'abcdefg'[1:6:3] }}|"
     "{{ 'é1ü2ö3'[::-1] }}|{{ 'é1ü2ö3'[1:4] }}|{{ 'é1ü2ö3'[-2:] }}|{{ 'abc'[99:] }}|{{ 'abc'[-99:] }}")
case("subscript/slice_sequences",
     "{{ [1,2,3,4,5][::2] }}|{{ [1,2,3,4,5][::-1] }}|{{ (1,2,3)[1:] }}|{{ range(7)[1:6:2] }}|{{ range(3)[::-1] }}")

# A tuple key is hashed by a walk of the whole tuple, which has to stay exact.
case("literals/nested_tuple_keys",
     '{% set d = {(1,(2,3)): "a", ((1,2),3): "b", (1,2,3): "c", (): "d", ((),): "e"} %}'
     '{{ d[(1,(2,3))] }}{{ d[((1,2),3)] }}{{ d[(1,2,3)] }}{{ d[()] }}{{ d[((),)] }}|{{ d|length }}')


# --- argument binding ---------------------------------------------------------
# Every argument error a template can provoke is CPython's, raised by CPython's
# own binding against a signature in jinja2.filters or jinja2.tests. Nothing
# checked any of it: extra arguments were read as the next parameter, so
# `|min(1,2,3,4,5)` took the 2 for an attribute name, and an unknown keyword
# was dropped in silence. The four shapes, and the order they are reported in.
case("errors/arity_too_many", '{{ "x"|upper(1) }}')
case("errors/arity_too_many_range", '{{ "x"|replace("a","b",1,2) }}')
case("errors/arity_unknown_keyword", '{{ "x"|upper(zzzz=1) }}')
case("errors/arity_multiple_values", '{{ "x"|center(3, width=4) }}')
case("errors/arity_multiple_values_self", '{{ "x"|upper(s=1) }}')
case("errors/arity_missing_one", '{{ "x"|replace("a") }}')
case("errors/arity_missing_two", '{{ "x"|replace() }}')
case("errors/arity_builtin", '{{ -1|abs(1) }}')
case("errors/arity_builtin_keyword", '{{ 1 is callable(zzz=1) }}')
case("errors/arity_test", '{{ 1 is odd(1) }}')
case("errors/arity_injected", '{{ "x"|truncate(1,2,3,4,5) }}')
# Keyword problems beat count problems, and count problems beat missing ones;
# among keywords the first one in the call wins.
case("errors/arity_keyword_beats_count", '{{ "x"|upper(1, zzzz=2) }}')
case("errors/arity_dup_beats_count", '{{ "x"|center(1, 2, width=3) }}')
case("errors/arity_first_keyword_wins", '{{ "x"|center(1, zzzz=4, width=3) }}')
case("errors/arity_keyword_beats_missing", '{{ "x"|replace(zzzz=1) }}')
# Calls that are legal and must stay so.
case("filters/arity_keyword_forms",
     '{{ "x"|center(width=3) }}|{{ "ab"|replace(old="a", new="b") }}|'
     '{{ "x"|indent(width=2, first=true) }}|{{ 1|round(precision=1, method="ceil") }}|'
     '{{ "x"|format(zzzz=1) }}|{{ [1,2]|sum(start=3) }}')
# A wrapstring that cannot join, and an ellipsis that has no length, both fail
# where the attribute is looked up rather than where the value is used.
case("errors/wordwrap_wrapstring", '{{ 0|wordwrap(1, 2, 3) }}')
case("errors/truncate_end_length", '{{ "abc"|truncate(1,2,3) }}')
case("errors/truncate_too_short", '{{ "abc"|truncate(1) }}')
case("filters/truncate_unicode_end", '{{ "abcdefghij"|truncate(6, true, "éé") }}')
case("errors/urlize_rel_type", '{{ "x"|urlize(rel=4) }}')
case("filters/urlize_attrs", '{{ "http://a.com"|urlize(rel="me") }}|{{ "http://a.com"|urlize(target="_b") }}')


# --- what a module exports ----------------------------------------------------
# jinja2 exports the names a top-level binding actually made, recorded as the
# template runs. gojja2 kept that list too and then answered from the frame
# instead, so a name the frame had pre-declared and never assigned -- one
# bound only inside an `{% if %}` that did not run, or inside a loop -- was
# exposed as an undefined rather than not exposed at all.
MODULE = {
    "__templates__": {
        "mod.html": "{% if false %}{% set a = 1 %}{% endif %}{% set b = 2 %}"
                    "{% set _c = 3 %}{% macro m() %}M{% endmacro %}"
                    "{% for i in [1] %}{% set d = 4 %}{% endfor %}"
    }
}
case("import/module_exports",
     '{% import "mod.html" as mod %}{{ mod.a is defined }}|{{ mod.b }}|'
     '{{ mod._c is defined }}|{{ mod.m() }}|{{ mod.d is defined }}|{{ mod.zz is defined }}',
     **MODULE)
case("import/from_unassigned", '{% from "mod.html" import a %}{{ a is defined }}', **MODULE)
case("import/from_assigned", '{% from "mod.html" import b, m %}{{ b }}{{ m() }}', **MODULE)


# do_dictsort checks `by` as its first statement, ahead of anything that looks
# at the input, so a bad `by` is reported even when the input could not be
# sorted either.
case("errors/dictsort_by_before_input", '{{ nope|dictsort(1,2,3) }}')
case("errors/dictsort_by_before_type", '{{ 5|dictsort(1,2,3) }}')
case("errors/dictsort_undefined", '{{ nope|dictsort }}')
case("filters/dictsort_order", '{{ {"b":1,"A":2}|dictsort }}|{{ {"b":1,"A":2}|dictsort(true) }}|'
     '{{ {"b":1,"a":2}|dictsort(false,"value") }}|{{ {"b":1,"a":2}|dictsort(false,"key",true) }}')


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
