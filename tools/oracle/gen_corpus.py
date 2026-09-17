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
# A negative constant base under ** changes sign when the power survives to run
# time. jinja2 writes the constant into the generated Python as its repr, so
# `(-8) ** m` is emitted as `-8 ** m`, which Python reads as -(8 ** m); a power
# it can fold keeps the grouping the template wrote.
case("operators/negative_power_folded", "{{ (-8) ** 2 }}|{{ -8 ** 2 }}|{{ (-2) ** 3 }}")
case("operators/negative_power_runtime", "{% set m = 2 %}{{ (-8) ** m }}|{{ (-8.5) ** m }}|{{ (0-8) ** m }}|{{ (-8) ** -m }}", )
case("operators/negative_power_variable_base", "{% set m = 2 %}{% set x = -8 %}{{ x ** m }}|{% set y = 8 %}{{ (-y) ** m }}")
case("operators/negative_power_test_exponent", "{{ '[%o]' % -8 ** 0 is eq(n) }}")
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

# What may be folded is decided by looking inside the value, not at its type:
# jinja2's has_safe_repr recurses through a list and a dict and accepts only
# exact types, so a list of |groupby pairs is not foldable even though a list
# is. Folding one anyway answers a branch that jinja2 evaluates, and swallows
# the error that evaluating it raises.
case("fold/groupby_result_is_not_constant", "{% with w = blank if (-1)[-2:] else {1: 'a', 2: 'b'}|batch(2)|list|groupby('city')|list %}{% endwith %}",
     blank="")
case("fold/nested_constant_still_folds", "{% with w = 1 if (-1)[-2:] else [[1, {'k': (2, 'x')}]] %}{{ w }}{% endwith %}")

# `~` inside a volatile {% autoescape %} concatenates plainly whatever the
# setting says: jinja2 picks the join with `markup_join if
# context.eval_ctx.volatile else str_join`, and an eval context is only ever
# volatile at compile time, so the attribute the generated code reads is always
# False. The same `~` one line further out answers Markup.
case("escape/volatile_concat", "{% autoescape yes %}{{ (mk|safe) ~ s }}|{{ ((mk|safe) ~ s) is escaped }}|{{ ((mk|safe) ~ s)|length }}{% endautoescape %}",
     yes=True, mk="<i>", s="a&b")
case("escape/volatile_concat_nested", "{% autoescape yes %}{% autoescape true %}{{ (mk|safe) ~ s }}{% endautoescape %}{% endautoescape %}|{% autoescape true %}{{ (mk|safe) ~ s }}{% endautoescape %}",
     yes=True, mk="<i>", s="a&b")
case("escape/volatile_concat_macro", "{% autoescape yes %}{% macro q() %}{{ (mk|safe) ~ s }}{% endmacro %}{{ q() }}{% endautoescape %}",
     yes=True, mk="<i>", s="a&b")

# Markup on the left of * settles the operation before an undefined on the
# right can raise: Markup.__mul__ asks for __index__ and lets that TypeError
# out, where str.__mul__ steps aside and the undefined raises instead.
case("errshape/markup_times_undefined", "{{ (s|safe) * nope }}", s="a")
case("errshape/string_times_undefined", "{{ s * nope }}", s="a")
case("errshape/undefined_times_markup", "{{ nope * (s|safe) }}", s="a")

# An {% autoescape %} whose argument is not constant leaves the escaping
# unknowable until the render -- a volatile eval context. jinja2 still folds a
# constant print there, and folds it with the setting the block was supposed to
# replace, because a volatile context carries no value of its own. A filter is
# not folded there at all: Filter.as_const refuses outright, so it runs at the
# render, under the block's own setting. The two halves of one block therefore
# disagree, and both are graded here.
case("escape/volatile_folds_constant", "{% autoescape yes %}{{ {'a': 1} }}|{{ '<x>'|upper }}{% endautoescape %}",
     yes=True)
case("escape/volatile_folds_constant_escaping", "{% autoescape blank %}{{ {'a': 1} }}|{{ '<x>'|upper }}{% endautoescape %}",
     __settings__={"autoescape": True}, blank="")
case("escape/constant_block_folds_with_its_own", "{% autoescape true %}{{ {'a': 1} }}|{{ '<x>'|upper }}{% endautoescape %}")

# `loop` is the iterator the loop is walking, not a copy of it: consuming it
# from inside the body advances that walk, each item arrives as a (value, loop)
# pair, and the loop in the pair is the same object -- so its repr says where
# the walk had got to when the pair was rendered. It has a length and no
# indexing, which is __len__ without __getitem__.
case("loops/loop_is_the_iterator", "{% for i in [1,2,3] %}[{{ loop|list }}]{% endfor %}")
case("loops/loop_first_takes_one", "{% for i in [1,2,3] %}[{{ loop|first }}]{% endfor %}")
case("loops/loop_length_and_shape", "{% for i in [1,2,3] %}[{{ loop|length }}{{ loop|count }}{{ loop is sequence }}{{ loop is iterable }}]{% endfor %}")
case("loops/loop_join_renders_as_it_walks", "{% for i in [1,2,3] %}[{{ loop|join(',') }}]{% endfor %}")
case("loops/loop_map_applies_as_it_walks", "{% for i in [1,2,3] %}[{{ loop|map('string')|list }}]{% endfor %}")
case("loops/loop_in_dict", "{% for i in [1,2,3] %}[{{ dict(loop, extra=2) }}]{% endfor %}")

# A loop's `if` runs as the loop walks, not before it starts: jinja2 compiles
# it into a generator the LoopContext consumes an item at a time, so a test
# that reads what the body writes sees the writes.
case("loops/filter_runs_as_it_walks", "{% set ns = namespace(n=0) %}{% for i in [1,2,3] if ns.n == 0 %}{% set ns.n = 1 %}{{ i }}{% endfor %}")
case("loops/filter_state_across_recursion", "{% for a in [1,2] %}{% for i in [7,8] if loop.changed(i) recursive %}{{ loop([i]) }}x{% endfor %}{% endfor %}")
case("loops/filtered_length_still_counts", "{% for i in [1,2,3] if i > 1 %}{{ loop.length }}{{ loop.revindex }}{{ loop.last }}{% endfor %}")
case("loops/filtered_else", "{% for i in [1,2,3] if false %}x{% else %}none{% endfor %}")

# A slice asks the base before it judges its operands: Python builds
# slice(1.5, None) happily and leaves the complaining to __getitem__, so a base
# with no subscript says so first and a dict calls the slice unhashable. The
# evaluator and the constant folder share one implementation of all this, which
# is why a folded slice of a |groupby pair is the tuple the unfolded one is.
case("errshape/slice_index_types", "{% set q = 'abcdef' %}{{ q[1.5:] }}")
case("errshape/slice_of_a_dict", "{% set q = {'a': 1} %}{{ q['x':] }}")
case("errshape/slice_of_an_int", "{% set q = 3 %}{{ q[1.5:] }}")
case("errshape/slice_step_zero", "{{ 'abcdef'[::0] }}")
case("errshape/slice_step_zero_runtime", "{% set q = 'abcdef' %}{{ q[::0] }}")
case("filters/slice_of_a_group_tuple", "{{ ([2.675]|groupby('age')|list|max)[::2] }}|{{ (users|groupby('city')|first)[::2] }}", **USERS)

# |urlencode builds each pair as it takes it -- `"&".join(f"..." for k, v in
# items)` -- which shows when the input is an iterator something else is also
# walking.
case("filters/urlencode_renders_as_it_walks", "{% for i in [1,2,3] %}[{{ loop|urlencode }}]{% endfor %}")

# A constant that folds to an infinity is written into jinja2's generated
# Python as the bare word `inf`, which is not a literal -- so the template
# raises NameError where it renders here. Listed in known_failures.txt.
case("fold/infinite_constant", "{% if 1e400 %}y{% endif %}")

# int() of a float is exact at any size, and float() has no range to fail on:
# a literal too large is inf and one too small is zero. int() of an infinity is
# an OverflowError that |int lets out, and int() of a NaN is a ValueError it
# catches, so the two answer differently.
case("filters/int_of_a_big_float", "{{ '9.223372036854776e+18'|int }}|{{ 9223372036854775808|float|int }}|{{ '9223372036854775808'|int }}")
case("filters/float_out_of_range", "{{ '1e400'|float }}|{{ '-1e400'|float }}|{{ '1e-400'|float }}")
case("filters/int_of_infinity_from_a_string", "{{ 'inf'|int }}|{{ 'nan'|int }}|{{ 'inf'|int(7) }}|{{ '-inf'|int }}")
case("filters/int_of_a_nan", "{{ z|float|int }}", z="nan")
case("errshape/int_of_an_infinity", "{{ z|float|int }}", z="inf")

# The case-folding a sort does keeps Markup: markupsafe overrides the case
# methods, because changing the case of escaped text cannot unescape it. The
# folded key is what a comparison error names.
case("errshape/sort_markup_key", "{{ [false, mk|safe]|sort }}", mk="<i>")
case("markup/sort_keeps_markup", "{{ ([mk|safe, 'B', 'a']|sort)|pprint }}|{{ [mk|safe, 'B']|min|pprint }}", mk="<i>")

# A filtered loop with a tuple target walks tuples. jinja2 compiles the filter
# into a function that unpacks the target and yields it straight back --
# `for a, b in fiter: if cond: yield (a, b)` -- so the items the loop sees are
# that tuple and not the source's own lists. Without a filter there is no such
# function, and they are.
case("loops/filtered_tuple_target", "{% for a, b in pairs if true %}[{{ loop.previtem }}][{{ loop.nextitem }}]{% endfor %}", pairs=[[1, 2], [3, 4]])
case("loops/unfiltered_tuple_target", "{% for a, b in pairs %}[{{ loop.previtem }}]{% endfor %}", pairs=[[1, 2], [3, 4]])
case("loops/filtered_single_target", "{% for x in pairs if true %}[{{ loop.previtem }}]{% endfor %}", pairs=[[1, 2], [3, 4]])
case("loops/filtered_nested_target", "{% for a, (b, c) in nested if true %}[{{ loop.nextitem }}]{% endfor %}", nested=[[1, [2, 3]], [4, [5, 6]]])

# |last goes through reversed(), which wants indexing and not just iteration,
# so a LoopContext -- which knows its length and nothing else -- is refused.
case("errshape/last_of_a_loop", "{% for i in [1,2,3] %}{{ loop|last }}{% endfor %}")
case("filters/last_of_what_reverses", "{{ [1,2]|last }}|{{ 'ab'|last }}|{{ range(3)|last }}|{{ {1:2,3:4}|last }}")

# A nested macro is not a boundary for `caller`: jinja2's search does not stop
# at closure frames, so mentioning it inside one makes the enclosing macro
# accept a caller as well.
case("macros/caller_inside_a_nested_macro", "{% macro mm(x) %}{% macro nn() %}{{ caller() }}{% endmacro %}{% call nn() %}I{% endcall %}{% endmacro %}{% call mm(1) %}B{% endcall %}")
case("macros/caller_nested_unused", "{% macro mm(x) %}{% macro nn() %}{{ caller() }}{% endmacro %}{% endmacro %}{% call mm(1) %}B{% endcall %}")

# A {% block %} body is compiled as a standalone function resolving against
# the context, so the context is not an enclosing frame for it: a name the
# block assigns late is undefined inside it beforehand, even when it was passed
# in. A macro body does nest inside the frame that defined it, and starts from
# what that frame had.
case("scope/block_owns_a_name_it_sets_late", "{% block a %}{% for i in [1] %}[{{ m }}]{% endfor %}{% set m = 1 %}[{{ m }}]{% endblock %}", m=10)
case("scope/block_reads_a_name_it_never_sets", "{% block a %}{% for i in [1] %}[{{ m }}]{% endfor %}{% endblock %}", m=10)
case("scope/macro_aliases_the_defining_frame", "{% set m = 1 %}{% macro qq() %}{% for i in [1] %}[{{ m }}]{% endfor %}{% set m = 2 %}{% endmacro %}{{ qq() }}", m=10)
case("scope/macro_without_an_enclosing_local", "{% macro qq() %}{% for i in [1] %}[{{ m }}]{% endfor %}{% set m = 2 %}{% endmacro %}{{ qq() }}", m=10)

# {% autoescape %} is a scope, as jinja2 compiles it: what the body assigns
# does not reach the frame outside it, and a name that frame assigns later is
# already its local when the body reads it.
case("scope/autoescape_scopes_names", "{% autoescape true %}{% set q = 1 %}{% endautoescape %}[{{ q }}]", q=7)
case("scope/autoescape_reads_a_later_local", "{% autoescape true %}[{{ q }}]{% endautoescape %}{% set q = 1 %}[{{ q }}]", q=7)

# `~` is markup_join, which only switches to joining as Markup once it meets an
# operand that already is. With none it concatenates the str()s into a plain
# string, and the escaping happens at output like any other value.
case("escape/concat_plain", "{{ (sv ~ n)|pprint }}|{{ (sv ~ n) is escaped }}|{{ (sv ~ n)|length }}|{{ sv ~ n }}",
     __settings__={"autoescape": True}, sv="a<b", n=5)
case("escape/concat_markup", "{{ (sv ~ mk|safe)|pprint }}|{{ sv ~ mk|safe }}|{{ (mk|safe ~ sv)|pprint }}",
     __settings__={"autoescape": True}, sv="a<b", mk="<i>")
case("escape/concat_no_autoescape", "{{ (sv ~ mk|safe)|pprint }}|{{ sv ~ mk|safe }}", sv="a<b", mk="<i>")

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
# do_batch never converts linecount: it only compares it. A linecount no length
# can equal puts everything in one row rather than raising, and 0 equals the
# length of the empty row the generator starts with, so that row is yielded once.
case("filters/batch_linecount_is_only_compared",
     "{{ seq|batch('x')|list }}|{{ seq|batch(none)|list }}|{{ seq|batch(-1)|list }}|{{ seq|batch(2.5)|list }}", **SEQ)
case("filters/batch_linecount_zero_yields_an_empty_row",
     "{{ seq|batch(0)|list }}|{{ seq|batch(false)|list }}|{{ seq|batch(true)|list }}", **SEQ)
# The padding is the one place linecount has to be more than comparable:
# `len(tmp) < linecount` orders it, and `[fill] * (linecount - len(tmp))`
# multiplies by it. Only the last row is ever padded.
case("filters/batch_fill_orders_the_linecount",
     "{{ seq|batch('x', 'X')|list }}", **SEQ)
case("filters/batch_fill_pads_only_the_last_row",
     "{{ seq|batch(4, 'X')|list }}|{{ [9]|batch(3, 'X')|list }}|{{ []|batch(2, 'X')|list }}|{{ seq|batch(9, 'X')|list }}", **SEQ)
case("filters/batch_fill_of_none_does_not_pad",
     "{{ [9]|batch(3, none)|list }}|{{ [9]|batch(3)|list }}|{{ [9]|batch('x', none)|list }}")
case("filters/slice", "{{ seq|slice(3)|list }}|{{ seq|slice(3, 'X')|list }}|{{ []|slice(2, 'X')|list }}", **SEQ)
# do_slice does not convert slices either: it divides the length by it twice
# and then hands it to range(). Each of those refuses differently, and the
# order is what decides which error a template sees.
case("filters/slice_count_divides_the_length", "{{ seq|slice('x')|list }}", **SEQ)
case("filters/slice_count_divides_the_length_none", "{{ seq|slice(none)|list }}", **SEQ)
case("filters/slice_count_divides_by_zero", "{{ seq|slice(0)|list }}", **SEQ)
case("filters/slice_count_divides_by_false", "{{ seq|slice(false)|list }}", **SEQ)
# A float divides happily -- 5 // 2.5 is 2.0 -- and it is range() that refuses.
case("filters/slice_count_reaches_range_as_a_float", "{{ seq|slice(2.5)|list }}", **SEQ)
# range() of a negative count is empty, so there is no slice at all, with or
# without a fill to put in one.
case("filters/slice_count_negative_yields_nothing",
     "{{ seq|slice(-1)|list }}|{{ seq|slice(-1, 'X')|list }}|{{ []|slice(-1)|list }}", **SEQ)
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
# bytes.__repr__ escapes one byte at a time and has no \u or \U form, so a
# character outside ASCII is one escape per UTF-8 byte -- not the single escape
# the str repr writes for the rune those bytes decode to.
# list.sort is in place and answers None; dict.popitem takes the pair inserted
# last, dicts having been ordered since 3.7; dict.fromkeys is a classmethod, so
# the dict it is reached through contributes nothing.
case("methods/list_sort_in_place",
     "{% set L = [3,1,2] %}{{ L.sort() }}|{{ L }}|"
     "{% set M = ['b','A','c'] %}{{ M.sort(reverse=true) }}|{{ M }}|"
     "{% set N = [1.5, 1, true] %}{{ N.sort() }}|{{ N }}")
case("methods/dict_popitem",
     "{% set D = {'a':1,'b':2} %}{{ D.popitem() }}|{{ D }}|"
     "{% set E = {'a':1} %}{{ E.popitem() }}|{{ E }}")
case("methods/dict_fromkeys",
     "{{ {'x': 1}.fromkeys('ab') }}|{{ {'x': 1}.fromkeys([3,1,2], 9) }}|"
     "{{ {'x': 1}.fromkeys([]) }}|{{ {'b':2,'a':1}.fromkeys({'b':2,'a':1}) }}")
# A C function counts its arguments, and a tuple names itself.
case("errors/dict_get_too_many", "{{ {'a':1}.get('a', 1, 2) }}")
case("errors/tuple_index_missing", "{{ (1,2).index(99) }}")
case("errors/list_index_missing", "{{ [1,2].index(99) }}")
case("errors/sort_positional_argument", "{% set L = [1] %}{{ L.sort(1) }}")
case("errors/popitem_on_an_empty_dict", "{{ {}.popitem() }}")
case("errors/cycler_without_items", "{{ cycler() }}")

# jinja2 compiles {% autoescape %} and {% scope %} as Scopes, so each body is a
# frame: a name the body assigns is that frame's own, and a read from a *nested*
# frame before the assignment sees undefined rather than the context's value.
# Wherever CPython uses an argument as an integer it goes through __index__,
# whose complaint names the type that was passed.
case("errshape/integer_argument_index_message",
     "{{ 'a'|center('x') }}")
case("errshape/integer_argument_names_the_type", "{{ 'a'|center([1]) }}")
case("errshape/integer_argument_on_a_method", "{{ 'a'.zfill('x') }}")

# |map calls a filter by name and select/reject call a test by name; jinja2
# reaches both through call_filter/call_test, which bind the arguments exactly
# as a written call does. A falsey input is never checked, because do_map
# prepares the filter inside `if value:`.
case("errshape/map_inner_filter_too_many", "{{ ['a']|map('upper','x')|list }}")
case("errshape/map_inner_filter_too_few", "{{ ['a']|map('replace')|list }}")
case("errshape/map_inner_filter_bad_keyword", "{{ ['a']|map('upper', foo=1)|list }}")
case("errshape/select_inner_test_too_many", "{{ [1]|select('odd','x')|list }}")
case("errshape/select_inner_test_too_few", "{{ [1]|select('divisibleby')|list }}")
case("filters/map_empty_input_is_never_bound",
     "{{ []|map('upper','x')|list }}|{{ []|map('replace')|list }}|"
     "{{ []|select('odd','x')|list }}|{{ none|map('upper','x')|list }}")

# A replacement field's [key] step is a real obj[key]. What decides the key's
# type is how it is spelled: all digits is an integer index, anything else --
# "-1" and " 0" included -- is a string key, so a negative index never occurs.
case("methods/format_field_subscript",
     "{{ '{0[0]}{0[1]}'.format('Hello') }}|{{ '{0[01]}'.format('Hello') }}|"
     "{{ '{0[0]}'.format([3,1]) }}|{{ '{0[1]}'.format((7,8)) }}|"
     "{{ '{0[a]}'.format({'a': 1}) }}|{{ '{0[0][0]}'.format(['ab']) }}")
case("errors/format_subscript_not_subscriptable", "{{ '{0[0]}'.format(3) }}")
case("errors/format_subscript_string_key_on_a_list", "{{ '{0[-1]}'.format([1]) }}")
case("errors/format_subscript_string_key_on_a_string", "{{ '{0[a]}'.format('ab') }}")
case("errors/format_subscript_out_of_range", "{{ '{0[9]}'.format([1]) }}")
case("errors/format_subscript_empty_key", "{{ '{0[]}'.format([1]) }}")

# int and float have real attributes, some properties and some methods, and
# jinja2 reaches them by getattr. bool is an int subclass, so True.real is 1.
case("methods/int_attributes",
     "{{ (3).real }}{{ (3).imag }}{{ (3).numerator }}{{ (3).denominator }}|"
     "{{ true.real }}{{ false.real }}|"
     "{{ (3).bit_length() }}{{ (0).bit_length() }}{{ (-4).bit_length() }}|"
     "{{ (3).bit_count() }}{{ (-4).bit_count() }}|{{ (3).as_integer_ratio() }}|"
     "{{ (10000000000000000000000).bit_length() }}")
case("methods/int_to_bytes",
     "{{ (3).to_bytes(2,'big') }}|{{ (3).to_bytes(2,'little') }}|{{ (255).to_bytes(1,'big') }}")
case("methods/float_attributes",
     "{{ (2.5).real }}{{ (2.5).imag }}|{{ (2.5).is_integer() }}{{ (1.0).is_integer() }}|"
     "{{ (2.5).as_integer_ratio() }}{{ (-1.5).as_integer_ratio() }}|{{ (2.5).conjugate() }}")
case("methods/float_hex",
     "{{ (2.5).hex() }}|{{ (1.0).hex() }}|{{ (0.0).hex() }}|{{ (-1.5).hex() }}|{{ (-0.5).hex() }}")
case("errors/to_bytes_negative", "{{ (-1).to_bytes(2,'big') }}")
case("errors/to_bytes_too_big", "{{ (300).to_bytes(1,'big') }}")

# json.dumps splits "no indent" from "an indent of zero": only None gives the
# one-line form, while 0 -- and any negative, which clamps to 0 -- still puts
# every element on its own line.
case("filters/tojson_indent_zero",
     "{{ [1,2]|tojson }}|{{ [1,2]|tojson(none) }}|{{ [1,2]|tojson(0) }}|"
     "{{ [1,2]|tojson(-1) }}|{{ [1,2]|tojson(2) }}")
case("filters/tojson_indent_nested",
     "{{ [[1]]|tojson(0) }}|{{ [[1]]|tojson(2) }}|{{ {'b':1,'a':2}|tojson(0) }}|"
     "{{ []|tojson(0) }}|{{ 1|tojson(0) }}")

# When a frame assigns a name, jinja2 asks the enclosing symbol table for a
# *reference* to it before settling on undefined. A reference is any mention at
# that level, so a read is enough and where it sits does not matter -- which is
# why the same inner frame answers differently depending on the rest of the
# template. A mention inside a nested frame is a different symbol table.
case("scope/inner_frame_with_no_outer_reference",
     "{% autoescape false %}{% for i in [1] %}[{{ m }}]{% endfor %}{% set m = 1 %}{% endautoescape %}", m=10)
case("scope/inner_frame_aliases_an_outer_read",
     "{% autoescape false %}{% for i in [1] %}[{{ m }}]{% endfor %}{% set m = 1 %}{% endautoescape %}{{ m }}", m=10)
case("scope/an_unreached_read_is_still_a_reference",
     "{% if false %}{{ m }}{% endif %}"
     "{% autoescape false %}{% for i in [1] %}[{{ m }}]{% endfor %}{% set m = 1 %}{% endautoescape %}", m=10)
case("scope/a_read_in_a_nested_frame_is_not",
     "{% for z in [] %}{{ m }}{% endfor %}"
     "{% autoescape false %}{% for i in [1] %}[{{ m }}]{% endfor %}{% set m = 1 %}{% endautoescape %}", m=10)
case("scope/a_block_body_never_aliases",
     "{{ m }}{% block b %}{% for i in [1] %}[{{ m }}]{% endfor %}{% set m = 1 %}{% endblock %}", m=10)

# One template per case: combining them changes the answer, because a read of
# the name at the *root* level anywhere in the template stops a nested Scope
# from owning it.
case("scope/autoescape_read_before_set",
     "{% autoescape false %}[{{ m }}]{% set m = 1 %}[{{ m }}]{% endautoescape %}", m=10)
case("scope/autoescape_owns_what_it_assigns",
     "{% autoescape false %}{% for i in [1] %}[{{ m }}]{% endfor %}{% set m = 1 %}{% endautoescape %}", m=10)
case("scope/autoescape_does_not_leak_out",
     "[{{ m }}]{% autoescape false %}{% set m = 1 %}{% endautoescape %}[{{ m }}]", m=10)
case("scope/autoescape_import_is_an_assignment",
     "{% autoescape false %}{% for i in [1] %}[{{ mod }}]{% endfor %}"
     "{% import 'mod.html' as mod %}{% endautoescape %}",
     m=10, __templates__={"mod.html": "{% set a = 1 %}"})

# `is filter` and `is test` are `value in env.filters` and `value in env.tests`,
# so the value is hashed before anything asks whether it could be a name.
case("tests/is_filter_and_is_test",
     "{{ 'upper' is filter }}{{ 'bogus' is filter }}|{{ 'odd' is test }}{{ 'odd' is filter }}|"
     "{{ 1 is filter }}{{ none is test }}{{ (1,2) is filter }}{{ nope is filter }}")
case("errors/is_filter_unhashable", "{{ [1] is filter }}")
case("errors/is_test_unhashable_in_a_tuple", "{{ (1,[2]) is test }}")

# str.encode honours its codec and its error handler. The three codecs here are
# the ones gojja2 implements; docs/divergences.md has why the rest are refused.
case("methods/encode_codecs",
     "{{ '\u00e9'.encode() }}|{{ '\u00e9'.encode('utf-8') }}|{{ '\u00e9'.encode('latin-1') }}|"
     "{{ '\u00e9'.encode('latin1') }}|{{ '\u00e9'.encode('iso-8859-1') }}|{{ 'abc'.encode('ascii') }}")
case("methods/encode_error_handlers",
     "{{ '\u00e9'.encode('ascii', 'ignore') }}|{{ '\u00e9'.encode('ascii', 'replace') }}|"
     "{{ '\u00e9'.encode('ascii', 'xmlcharrefreplace') }}|{{ '\u00e9'.encode('ascii', 'backslashreplace') }}|"
     "{{ '\u20ac'.encode('ascii', 'xmlcharrefreplace') }}|{{ '\u20ac'.encode('latin-1', 'ignore') }}")
case("methods/decode_round_trip",
     "{{ '\u00e9'.encode().decode() }}|{{ '\u00e9'.encode('latin-1').decode('latin-1') }}|"
     "{{ 'abc'.encode().decode('ascii') }}|{{ '\u00e9'.encode().decode('latin-1') }}")
# The position counts characters, not bytes.
case("errors/encode_ascii_position", "{{ 'a\u00e9b'.encode('ascii') }}")
case("errors/encode_latin1_range", "{{ '\u20ac'.encode('latin-1') }}")
case("errors/decode_ascii_range", "{{ '\u00e9'.encode().decode('ascii') }}")

# CPython picks between three recursion wordings by where its own stack ran
# out, which for most constructs is not a property of the template: the same
# macro recursion reports two different messages one frame apart. Only these
# two were stable at every depth tried, so only these two are graded; the rest
# is in docs/divergences.md.
case("errors/recursion_include", '{% include "self.txt" %}',
     __templates__={"self.txt": '{% include "self.txt" %}'})
case("errors/recursion_extends", '{% extends "selfext.txt" %}',
     __templates__={"selfext.txt": '{% extends "selfext.txt" %}'})

# str.format's replacement field is a name, then an optional !conversion, then
# an optional :format_spec -- a different mini-language from the one `%` uses.
case("methods/format_conversions",
     "{{ '{!r}'.format('ab') }}|{{ '{!s}'.format('ab') }}|{{ '{!a}'.format('\u00e9') }}|"
     "{{ '[{!r:>8}]'.format('ab') }}|{{ '{!r}'.format(none) }}")
case("methods/format_spec_strings",
     "[{{ '{:10}'.format('ab') }}]|[{{ '{:>10}'.format('ab') }}]|[{{ '{:^10}'.format('ab') }}]|"
     "[{{ '{:*^10}'.format('ab') }}]|[{{ '{:.3}'.format('abcdef') }}]|[{{ '{:>10.3}'.format('abcdef') }}]")
case("methods/format_spec_integers",
     "[{{ '{:5d}'.format(42) }}]|[{{ '{:05d}'.format(-42) }}]|[{{ '{:+d}'.format(42) }}]|"
     "[{{ '{:,}'.format(1234567) }}]|[{{ '{:_}'.format(1234567) }}]|[{{ '{:#x}'.format(255) }}]|"
     "[{{ '{:#06x}'.format(255) }}]|[{{ '{:b}'.format(10) }}]|[{{ '{:c}'.format(65) }}]")
case("methods/format_spec_floats",
     "[{{ '{:f}'.format(1.5) }}]|[{{ '{:.0f}'.format(1.5) }}]|[{{ '{:e}'.format(1234.5) }}]|"
     "[{{ '{:.3g}'.format(1234.5) }}]|[{{ '{:.1%}'.format(0.25) }}]|[{{ '{:08.3f}'.format(-1.5) }}]|"
     "[{{ '{:,.2f}'.format(1234567.891) }}]|[{{ '{:10}'.format(2.5) }}]")
# A bool has no __format__ of its own, so any spec makes it the integer it is.
case("methods/format_spec_bool",
     "{{ '{}'.format(true) }}|{{ '{:>8}'.format(true) }}|{{ '{:d}'.format(true) }}")
# The width and precision can themselves be replacement fields.
case("methods/format_nested_fields",
     "[{{ '{:{}}'.format(3, 6) }}]|[{{ '{0:{1}.{2}f}'.format(2.5, 9, 3) }}]|"
     "[{{ '{v:>{w}}'.format(v='ab', w=6) }}]|[{{ '{0[1]:03d}'.format([7, 8]) }}]")
# Only the empty spec reaches object.__format__, which is why `{}` renders a
# list happily and `{:>8}` on one does not.
case("errors/format_spec_on_a_list", "{{ '{:d}'.format([1]) }}")
case("errors/format_unknown_conversion", "{{ '{!z}'.format(1) }}")
case("errors/format_unknown_code", "{{ '{:*}'.format(1) }}")
case("errors/format_invalid_specifier", "{{ '{:qq}'.format(1) }}")
# One format string counts its fields or names them, never both.
case("errors/format_mixed_numbering", "{{ '{} {0}'.format(1) }}")
case("errors/format_unterminated_field", "{{ '{0'.format(1) }}")

# Python has three numeric predicates and they are three different sets: only
# isdecimal is a general category (Nd). isdigit adds Numeric_Type=Digit, and
# isnumeric adds everything carrying a numeric value, CJK ideographs included.
case("methods/numeric_predicates_differ",
     "{{ '\u00b2'.isdecimal() }}{{ '\u00b2'.isdigit() }}{{ '\u00b2'.isnumeric() }}|"
     "{{ '\u00bd'.isdecimal() }}{{ '\u00bd'.isdigit() }}{{ '\u00bd'.isnumeric() }}|"
     "{{ '\u4e00'.isdecimal() }}{{ '\u4e00'.isdigit() }}{{ '\u4e00'.isnumeric() }}|"
     "{{ '\u0667'.isdecimal() }}{{ '\u0667'.isdigit() }}{{ '\u0667'.isnumeric() }}")
case("methods/isalnum_is_the_union",
     "{{ '\u00b2'.isalnum() }}|{{ '\u00bd'.isalnum() }}|{{ 'a'.isalnum() }}|{{ '-'.isalnum() }}")
# isascii and isprintable answer True for the empty string; the rest answer False.
case("methods/empty_string_predicates",
     "{{ ''.isascii() }}{{ ''.isprintable() }}|{{ ''.isdigit() }}{{ ''.istitle() }}"
     "{{ ''.isidentifier() }}{{ ''.isnumeric() }}|{{ ' '.isprintable() }}{{ '\t'.isprintable() }}")
case("methods/istitle_and_isidentifier",
     "{{ 'Hello World'.istitle() }}{{ 'Hello world'.istitle() }}{{ \"It's\".istitle() }}|"
     "{{ 'class'.isidentifier() }}{{ '1x'.isidentifier() }}{{ 'a-b'.isidentifier() }}")
# expandtabs counts from the last line break, not from the start of the string.
case("methods/expandtabs",
     "[{{ 'a\tb'.expandtabs() }}]|[{{ 'ab\tcd'.expandtabs(4) }}]|"
     "[{{ 'a\tb\nc\td'.expandtabs(4) }}]|[{{ 'a\tb'.expandtabs(0) }}]")
# partition always answers a 3-tuple; which side the empties fall on is the
# only thing that differs when the separator is absent.
case("methods/partition",
     "{{ 'a-b-c'.partition('-') }}|{{ 'a-b-c'.rpartition('-') }}|"
     "{{ 'abc'.partition('-') }}|{{ 'abc'.rpartition('-') }}")
case("errors/partition_empty_separator", "{{ 'abc'.partition('') }}")
case("methods/remove_affix",
     "{{ 'abc'.removeprefix('ab') }}|{{ 'abc'.removeprefix('zz') }}|"
     "{{ 'abc'.removesuffix('bc') }}|{{ 'abc'.removesuffix('zz') }}|{{ 'abc'.removeprefix('') }}")
case("methods/maketrans_and_translate",
     "{{ 'abc'.maketrans('ab', 'xy') }}|{{ 'abc'.translate('abc'.maketrans('ab','xy')) }}|"
     "{{ 'abc'.translate({97: 'X'}) }}|{{ 'abc'.translate({97: none}) }}|"
     "{{ 'abc'.translate({}) }}|{{ 'abc'.translate('abc'.maketrans('a','x','b')) }}")

case("repr/bytes_escape_per_byte",
     "{{ 'é'.encode() }}|{{ '€'.encode() }}|{{ 'héllo'.encode() }}|{{ 'ab~'.encode() }}")
case("repr/bytes_inside_a_container",
     "{{ ['é'.encode()] }}|{{ {'k': '€'.encode()} }}|{{ 'é'.encode()|pprint }}|{{ '%r' % 'é'.encode() }}")
case("repr/bytes_short_escapes_and_quotes",
     "{{ '\t\n'.encode() }}|{{ \"it's\".encode() }}|{{ 'say \"hi\"'.encode() }}|{{ ''.encode() }}")
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
# ascii() is repr() with what it produced escaped afterwards, so an object that
# renders itself -- a |groupby pair -- is escaped along with everything else.
case("markup/ascii_nested_repr", "{{ '[%a]' % ([{'c':'\u00e9'}]|groupby('c')|list) }}|{{ '[%a]' % ['\u00e9', ('\u4e2d',)] }}")
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
# textwrap and do_indent look at their arguments in an order a template can
# see: the width is only used once there is a line to wrap, the wrapstring's
# join is resolved before the value is read at all, and the indent is sized
# before the value is touched. A bad argument beside an undefined value
# therefore decides which error comes out.
# textwrap breaks a word after a hyphen only when what surrounds it says so:
# two letters before, or a letter-hyphen-letter; and a letter, an optional
# hyphen and a letter after. Two or more hyphens are an em-dash instead, and
# become a chunk of their own.
# |striptags ends in Python's html.unescape, which resolves every HTML 5 named
# reference -- the uppercase spellings included, which is what an upper-cased
# entity becomes -- and numeric ones with the standard's rules for the invalid
# ones. The whitespace is collapsed before any of that, so a reference standing
# for a space survives as a character.
case("filters/striptags_entities", "{{ html|upper|striptags }}|{{ '<b>&copy;</b> &reg; &Yacute;'|striptags }}", html="<b>a &amp; b</b>")
case("filters/striptags_numeric", "{{ '&#38;|&#x26;|&#X26;|&#0000038;|&#128;|&#13;|&#55296;|&#1114112;'|striptags }}")
case("filters/striptags_partial", "{{ '&notit;|&notit|&not|&amp|&ampx|&#;|&;|&'|striptags }}")
case("filters/striptags_spacing", "{{ 'a&nbsp;b'|striptags }}|{{ 'a &nbsp; b'|striptags }}|{{ '&#32;a&#32;'|striptags }}|{{ 'a&Tab;b'|striptags }}")

case("filters/wordwrap_hyphens", "{{ 'well-known a-b ab-cd a-b-c-d co-op-er-ate'|wordwrap(6) }}")
case("filters/wordwrap_hyphen_runs", "{{ 'a--b ab--cd x--y--z a-1-b _a-_b'|wordwrap(5) }}")
case("filters/wordwrap_hyphen_chain", "{{ joined|wordwrap(12, false) }}|{{ joined|wordwrap(12) }}",
     joined="-".join("  the quick brown fox jumps over the lazy dog  "))
case("filters/wordwrap_lines", "{{ text|wordwrap(12) }}|{{ 'a\rb\r\nc'|wordwrap(9) }}|{{ ''|wordwrap('z') }}", **TEXT)
case("filters/wordwrap_float_width", "{{ 'ab cd ef'|wordwrap(2.5) }}|{{ 'abcd'|wordwrap(0.5) }}|{{ 'abcd'|wordwrap(2.5, false) }}")
case("filters/indent_carriage_return", "{{ 'a\rb\r\nc'|indent(2, true) }}")
case("errshape/wordwrap_undefined_wrapstring", "{{ nope|wordwrap(1, 2, 3) }}")
case("errshape/wordwrap_undefined_width", "{{ nope|wordwrap('z') }}")
case("errshape/wordwrap_width_type", "{{ 'ab cd'|wordwrap('z') }}")
case("errshape/wordwrap_width_float_slice", "{{ 'abcd'|wordwrap(2.5) }}")
case("errshape/indent_undefined_width", "{{ nope|indent(1.5) }}")
case("errshape/indent_width_type", "{{ 'a'|indent([1]) }}")
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

# --- cases that were once loose files -----------------------------------------
# These were added straight into testdata/corpus as .jj2 files, which this
# script deletes: main() starts by removing the whole directory and writing it
# from CASES, so anything not registered here does not survive `make oracle`.
# They are the cases covering dynamic call arguments, `attribute=` resolution
# and tests taking their argument by name.

# `**` is dict(**x): what it is handed must be a mapping, its keys must be
# strings, and a key that repeats a keyword already written is the caller's
# error and names the callee.
case("errshape/dyn_kwargs_names_the_filter", '{{ lst|join(**5) }}', lst=[1, 2])
case("errshape/dyn_kwargs_names_the_test", '{{ lst is odd(**5) }}', lst=[1, 2])
case("errshape/dyn_kwargs_names_a_builtin", '{{ lst|abs(**5) }}', lst=[1, 2])
case("errshape/dyn_kwargs_key_not_a_string", '{{ lst|join(**{1: "-"}) }}', lst=[1, 2])
case("errshape/dyn_kwargs_duplicate_in_a_filter", '{{ lst|join(d="-", **{"d": "+"}) }}', lst=[1, 2])
case("errshape/dyn_kwargs_duplicate_in_a_call",
     '{% macro mm(x) %}{% endmacro %}{{ mm(x=1, **{"x": 2}) }}', lst=[1, 2])

# A `*` or `**` argument is folded with the rest when everything in it is
# constant, and holds the fold back when it is not.
case("fold/star_args_fold_too",
     '{{ (false)[1:]|join(*["-"]) }}|{{ (0o17)[1:]|center(*[4]) }}|'
     '{{ ({})[1:]|default(*[1]) }}|{{ (0o17)[1:] is eq(*[2]) }}')
case("fold/star_arg_not_iterable", '{{ [1,2]|join(*5) }}')
case("fold/double_star_is_dict_update",
     '{{ [1,2]|join(**{"d": "-"}) }}|{{ [1,2]|join(**["db"]) }}|{{ [1,2]|join(d="-", **{"d": "+"}) }}')
case("fold/dynamic_args_unfolded", '{{ lst|join(*["-"]) }}|{{ lst|join(**{"d": "+"}) }}', lst=[1, 2])

# A test takes its argument by name, as a filter already could, and collides
# with the value being tested the way the signature says.
case("tests/argument_by_name",
     '{{ 4 is divisibleby(num=2) }}|{{ 2 is sameas(other=2) }}|{{ "a" is in(seq="ab") }}|'
     '{{ 4 is divisibleby(**{"num": 2}) }}|{{ lst is in(seq=[1,2]) }}', lst=[1, 2])
case("tests/argument_by_name_collides", '{{ "a" is in(seq="ab", value="a") }}')

# make_attrgetter splits `attribute=` on dots and reads a segment of digits as
# an index, so what is not a string is refused before anything is looked up.
case("filters/attribute_not_a_string",
     '{{ "ab cd"|groupby(attribute=none) }}|{{ "ab cd"|max(attribute=false) }}|'
     '{{ "ab cd"|min(attribute=false) }}|{{ "ab cd"|join(attribute=false) }}')
case("filters/attribute_bool_has_no_element", '{{ "ab cd"|max(attribute=true) }}')
case("filters/attribute_float_has_no_element", '{{ "ab cd"|max(attribute=2.5) }}')
case("filters/attribute_index_is_isdigit",
     '{{ [[1,2]]|map(attribute="-1")|list }}|{{ ["ab"]|map(attribute="\u0661")|list }}|'
     '{{ ["ab"]|map(attribute=" 1")|list }}|{{ ["ab"]|map(attribute="1")|list }}')
case("filters/attribute_prefers_the_item",
     '{{ [{"items": 1}]|map(attribute="items")|list }}|{{ [{"items": 1}]|join(attribute="items") }}|'
     '{{ [{"items": 1}, {"items": 0}]|sort(attribute="items")|list }}|'
     '{{ [{"items": 1}]|sum(attribute="items") }}|{{ [{"items": 1}]|groupby("items")|list }}')
# do_attr starts with getattr_static, so an unhashable name is refused by that
# lookup and a hashable non-string by the check after it.
case("filters/attr_name_not_a_string", '{{ "ab"|attr(name=true) }}')
case("filters/attr_name_unhashable", '{{ "ab"|attr(name=[1]) }}')


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
