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


_SEEN: set[str] = set()


def case(name: str, template: str, **header) -> None:
    # A name is a path, so two cases sharing one silently overwrote each
    # other: main() writes them in order and the second wins. The count this
    # file printed counted CASES, not files, so 740 cases became 739 on disk
    # and nothing said so. Two striptags cases had been collapsed that way,
    # and the one that lost covered partial tags and HTML comments -- it was
    # generated, overwritten, and never graded again.
    if name in _SEEN:
        raise SystemExit(f"gen_corpus: duplicate case name {name!r}")
    _SEEN.add(name)
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

# Which template a render extends is settled once, for the whole render, so
# jinja2 refuses an {% extends %} compiled into a frame of its own. `{% if %}`
# is the exception, and the only one: it compiles inline, which is what makes
# the conditional-extends idiom legal at any depth of conditions. gojja2 let
# every scope through, so inheritance followed runtime control flow --
# `{% for i in [] %}{% extends %}` silently skipped it and `[1]` applied it.
case("inherit/extends_in_if",
     "{% if true %}{% extends 'base.html' %}{% endif %}{% block body %}c{% endblock %}",
     __templates__=LAYOUT)
case("inherit/extends_in_if_false",
     "{% if false %}{% extends 'base.html' %}{% endif %}{% block body %}c{% endblock %}",
     __templates__=LAYOUT)
case("inherit/extends_in_else",
     "{% if false %}x{% else %}{% extends 'base.html' %}{% endif %}{% block body %}c{% endblock %}",
     __templates__=LAYOUT)
case("inherit/extends_in_elif",
     "{% if false %}x{% elif true %}{% extends 'base.html' %}{% endif %}{% block body %}c{% endblock %}",
     __templates__=LAYOUT)
case("inherit/extends_in_nested_if",
     "{% if true %}{% if true %}{% extends 'base.html' %}{% endif %}{% endif %}{% block body %}c{% endblock %}",
     __templates__=LAYOUT)
for _scope, _wrap in [
    ("for", "{% for i in [1] %}BODY{% endfor %}"),
    ("for_else", "{% for i in [] %}x{% else %}BODY{% endfor %}"),
    ("for_if", "{% for i in [1] %}{% if true %}BODY{% endif %}{% endfor %}"),
    ("if_for", "{% if true %}{% for i in [1] %}BODY{% endfor %}{% endif %}"),
    ("block", "{% block z %}BODY{% endblock %}"),
    ("macro", "{% macro m() %}BODY{% endmacro %}"),
    ("with", "{% with x = 1 %}BODY{% endwith %}"),
    ("filter", "{% filter upper %}BODY{% endfilter %}"),
    ("set_block", "{% set v %}BODY{% endset %}"),
    ("call", "{% macro mm() %}{{ caller() }}{% endmacro %}{% call mm() %}BODY{% endcall %}"),
    ("autoescape", "{% autoescape true %}BODY{% endautoescape %}"),
]:
    case("errors/extends_in_" + _scope,
         _wrap.replace("BODY", "{% extends 'base.html' %}") + "{% block body %}c{% endblock %}",
         __templates__=LAYOUT)

# super() is compiled into a block function's frame, so a body defined inside
# the block -- a macro, or a {% call %} body -- closes over it and carries that
# binding wherever it is invoked. The name is lexical both ways: a macro
# written at template level has no super() however deep in a block it is
# called, and one written in a block keeps that block's super() even when it is
# smuggled through a namespace into a different block. Nothing graded this, and
# every deferred body answered "'super' is undefined".
case("inherit/super_in_macro",
     "{% extends 'base.html' %}{% block body %}{% macro m() %}<{{ super() }}>{% endmacro %}{{ m() }}{% endblock %}",
     __templates__=LAYOUT)
case("inherit/super_in_nested_macro",
     "{% extends 'base.html' %}{% block body %}{% macro m() %}{% macro inner() %}<{{ super() }}>{% endmacro %}{{ inner() }}{% endmacro %}{{ m() }}{% endblock %}",
     __templates__=LAYOUT)
case("inherit/super_in_call_body",
     "{% extends 'base.html' %}{% block body %}{% macro w() %}[{{ caller() }}]{% endmacro %}{% call w() %}{{ super() }}{% endcall %}{% endblock %}",
     __templates__=LAYOUT)
case("inherit/super_in_macro_in_loop",
     "{% extends 'base.html' %}{% block body %}{% for i in [1] %}{% macro m() %}<{{ super() }}>{% endmacro %}{{ m() }}{% endfor %}{% endblock %}",
     __templates__=LAYOUT)
case("inherit/super_macro_called_twice",
     "{% extends 'mid.html' %}{% block body %}{% macro m() %}<{{ super() }}>{% endmacro %}{{ m() }}{{ m() }}{% endblock %}",
     __templates__={**LAYOUT, "mid.html": "{% extends 'base.html' %}{% block body %}M({{ super() }}){% endblock %}"})
case("inherit/super_super_in_macro",
     "{% extends 'mid.html' %}{% block body %}{% macro m() %}<{{ super.super() }}>{% endmacro %}{{ m() }}{% endblock %}",
     __templates__={**LAYOUT, "mid.html": "{% extends 'base.html' %}{% block body %}M({{ super() }}){% endblock %}"})
# Defined in one block, called from another: the binding follows the macro.
case("inherit/super_travels_with_macro",
     "{% extends 'base.html' %}{% set ns = namespace(f=none) %}"
     "{% block body %}{% macro m() %}<{{ super() }}>{% endmacro %}{% set ns.f = m %}B{% endblock %}"
     "{% block title %}{{ ns.f() }}{% endblock %}",
     __templates__=LAYOUT)
# ... and a macro written outside every block has no super() to carry in.
case("inherit/super_outside_block_is_undefined",
     "{% extends 'base.html' %}{% macro m() %}<{{ super() }}>{% endmacro %}{% block body %}{{ m() }}{% endblock %}",
     __templates__=LAYOUT)

# A BlockReference defines no __str__, so only a call renders a block: printing
# `self.body` prints the object. `super` is a property on the reference, which
# is undefined past the end of the chain and says so when it is used. The
# objects themselves carry an address and are ungradable, so what is pinned here
# is everything around them.
case("inherit/self_call", "{% block b %}hi{% endblock %}|{{ self.b() }}")
case("inherit/self_super_undefined", "{% block b %}hi{% endblock %}[{{ self.b.super }}]")
case("errors/self_super_past_end", "{% block b %}hi{% endblock %}{{ self.b.super() }}")
case("inherit/self_print_does_not_render",
     "{% block b %}hi{% endblock %}{{ self.b|string|length > 20 }}")
case("inherit/template_reference_repr", "{% block b %}{% endblock %}{{ self }}")

# --- include and import -------------------------------------------------------
INC = {"inc.html": "[{{ v|default('none') }}]", "mac.html": "{% macro f(x) %}<{{x}}>{% endmacro %}{% set exported = 'E' %}"}
case("include/basic", "{% set v = 'V' %}{% include 'inc.html' %}", __templates__=INC)
case("include/without_context", "{% set v = 'V' %}{% include 'inc.html' without context %}", __templates__=INC)
case("include/in_loop", "{% for v in [1,2] %}{% include 'inc.html' %}{% endfor %}", __templates__=INC)
case("include/missing", "A{% include 'nope.html' %}B", __templates__=INC)
case("include/ignore_missing", "A{% include 'nope.html' ignore missing %}B", __templates__=INC)

# What a template does to its arguments has to survive being handed across a
# template boundary. A render argument is converted from Go once and memoised,
# and handing the whole context to an include realised every name again from the
# caller's original -- so an include silently put the context back as it found
# it, and `{{ l.append(9) }}{% include %}{{ l|length }}` printed 3 where CPython
# prints 4. None of the cases above mutate anything before crossing, which is
# why none of them noticed.
CTX = {
    "show": "[{{ l|length }}]",
    "showx": "[{{ x|default('unset') }}]",
    "setx": "{% set x = 99 %}",
    "showi": "[{{ i|default('noi') }}]",
    "showd": "[{{ d|length }}]",
    "showy": "[{{ y|default('noy') }}]",
    "show3": "[{{ l3|length }}]",
    "nested": "{% include 'show' %}{% include 'showx' %}",
    "mod": "{% macro n() %}N{% endmacro %}{% macro showx() %}{{ x|default('nox') }}{% endmacro %}",
}


def ctx(name: str, template: str) -> None:
    case(name, template, __templates__=CTX, l=[1, 2, 3], d={"a": 1})


ctx("include/mutation_survives", "{{ l.append(9) }}{% include 'show' %}")
ctx("include/mutation_without_context", "{{ l.append(9) }}{% include 'show' without context %}")
ctx("include/mutation_between_includes", "{% include 'show' %}{{ l.append(9) }}{% include 'show' %}")
ctx("include/mutation_through_alias", "{% set l2 = l %}{{ l2.append(9) }}{% include 'show' %}{{ l|length }}")
ctx("include/two_mutations", "{{ l.append(1) }}{{ l.append(2) }}{% include 'show' %}{{ l.append(3) }}{% include 'show' %}")
ctx("include/dict_mutation_survives", "{% set _ = d.update({'z': 1}) %}{% include 'showd' %}")
ctx("include/dict_mutation_through_alias", "{% set d2 = d %}{% set _ = d2.update({'q': 2}) %}{% include 'showd' %}")
ctx("include/mutation_then_import", "{% set _ = d.update({'z': 1}) %}{% import 'mod' as m %}{{ m.n() }}{{ d|length }}")
ctx("include/mutation_in_nested_include", "{{ l.append(9) }}{% include 'nested' %}")
ctx("include/mutation_then_loop", "{% for i in l %}{{ i }}{% endfor %}{{ l.append(9) }}{% for i in l %}{{ i }}{% endfor %}")
ctx("include/mutation_inside_filter_block", "{% filter upper %}{% include 'show' %}{% endfilter %}")
ctx("include/set_is_visible", "{% set x = 1 %}{% include 'showx' %}")
ctx("include/set_inside_does_not_escape", "{% set x = 1 %}{% include 'setx' %}{{ x }}")
ctx("include/set_inside_does_not_leak", "{% include 'setx' %}{{ x|default('unset') }}")
ctx("include/loop_variable_is_visible", "{% for i in [1,2] %}{% include 'showi' %}{% endfor %}")
ctx("include/with_binding_is_visible", "{% with y = 3 %}{% include 'showy' %}{% endwith %}")
ctx("include/set_then_nested", "{% set x = 1 %}{% include 'nested' %}")
ctx("include/appends_in_a_loop", "{% set l3 = [] %}{% for i in [1,2,3] %}{{ l3.append(i) }}{% endfor %}{% include 'show3' %}")
ctx("include/appends_seen_each_iteration", "{% set l3 = [] %}{% for i in [1,2,3] %}{{ l3.append(i) }}{% include 'show3' %}{% endfor %}")
ctx("import/context_carries_set", "{% set x = 5 %}{% import 'mod' as m with context %}{{ m.showx() }}")
ctx("import/without_context_does_not", "{% set x = 5 %}{% import 'mod' as m %}{{ m.showx() }}")
ctx("import/from_with_context", "{% from 'mod' import n with context %}{{ n() }}")
ctx("import/macro_sees_mutation", "{% macro mm() %}{{ l|length }}{% endmacro %}{{ l.append(9) }}{{ mm() }}")

# {% include %} compiles to get_or_select_template, while {% extends %},
# {% import %} and {% from %} compile to get_template. So a name that is not a
# string is a *selection* in the first -- empty by truthiness, then iterated --
# and a missing template in the others, reported as it was written.
case("errors/include_none", "{% include none %}", __templates__=INC)
case("errors/include_empty_list", "{% include [] %}", __templates__=INC)
case("errors/include_empty_dict", "{% include {} %}", __templates__=INC)
case("errors/include_number", "{% include 1 %}", __templates__=INC)
case("errors/include_number_ignore_missing", "{% include 1 ignore missing %}", __templates__=INC)
case("errors/include_dict_not_found", "{% include {'a':1} %}", __templates__=INC)
case("errors/include_none_ignore_missing", "[{% include none ignore missing %}]", __templates__=INC)
case("include/select_dict_key", "{% include {'inc.html': 1} %}", __templates__=INC)
case("include/select_tuple", "{% include ('inc.html',) %}", __templates__=INC)
case("errors/import_number", "{% import 1 as m %}{{ m }}", __templates__=INC)
case("errors/import_none", "{% import none as m %}{{ m }}", __templates__=INC)
case("errors/import_list", "{% import ['a'] as m %}{{ m }}", __templates__=INC)
case("errors/from_number", "{% from 1 import x %}{{ x }}", __templates__=INC)
case("errors/extends_dict", "{% extends {} %}", __templates__=INC)
case("import/module", "{% import 'mac.html' as m %}{{ m.f(1) }}{{ m.exported }}", __templates__=INC)
case("import/from", "{% from 'mac.html' import f, f as g %}{{ f(1) }}{{ g(2) }}", __templates__=INC)
case("import/from_missing", "{% from 'mac.html' import nope %}{{ nope }}", __templates__=INC)

# A TemplateModule's str() is what the imported template rendered, and its
# __html__ is the same string -- the body was produced under that template's own
# escaping, so it is markup already and is not escaped a second time. `~` is not
# __html__: markup_join soft_strs its operands first, so the result is a plain
# string escaped as a whole. And an error names the template the name was asked
# *of*, with its module path.
BODY = {"body.html": "<b>{{ w|default('?') }}</b>", "mac.html": INC["mac.html"]}
case("import/module_body", '{% import "body.html" as m %}{{ m }}', __templates__=BODY)
case("import/module_body_escaped", '{% import "body.html" as m %}{{ m }}',
     __templates__=BODY, __settings__={"autoescape": True})
case("import/module_body_filters",
     '{% import "body.html" as m %}{{ m|safe }}|{{ m|escape }}|{{ [m]|join("-") }}|'
     '{{ m is escaped }}|{{ "" ~ m }}',
     __templates__=BODY, __settings__={"autoescape": True})
case("import/module_body_empty", '[{% import "mac.html" as m %}{{ m }}]', __templates__=BODY)
case("import/module_repr", '{% import "mac.html" as m %}{{ m|pprint }}', __templates__=BODY)
case("errors/module_attribute", '{% import "mac.html" as m %}{{ m.nope.x }}', __templates__=BODY)

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

# --- line statements ----------------------------------------------------------
# An entire syntax mode with no coverage: nothing in this corpus uses
# line_statement_prefix, and the differential generator cannot reach it -- it
# draws autoescape and nothing else. The edges are where the rule is decided:
# whether leading whitespace still counts as the start of a line, whether text
# before the prefix cancels it, whether a prefix with nothing after it counts.
LS = {"line_statement_prefix": "#", "line_comment_prefix": "##"}


def ls(name: str, template: str, **settings) -> None:
    merged = dict(LS)
    merged.update(settings)
    case(f"linestatement/{name}", template, __settings__=merged)


ls("for", "# for i in [1,2]\nx{{ i }}\n# endfor\n")
ls("for_no_trailing_newline", "# for i in [1,2]\nx\n# endfor")
ls("if_between_text", "before\n# if true\nyes\n# endif\nafter")
ls("if_else", "# if false\nno\n# else\nyes\n# endif")
ls("elif_chain", "# if 1\na\n# elif 2\nb\n# else\nc\n# endif")
ls("indented_prefix", "   # if true\nindented\n   # endif")
ls("tab_indented_prefix", "\t# if true\ntabbed\n# endif")
ls("set", "# set x = 5\n{{ x }}")
ls("comment_after_text", "a ## this is a comment\nb")
ls("comment_whole_line", "## whole line comment\nkept")
ls("comment_then_statement", "x ## trailing\n# if true\ny\n# endif")
ls("comment_inside_block", "# for i in [1]\n## inner comment\nz\n# endfor")
ls("text_before_prefix_is_text", "not # a statement because text precedes it")
ls("prefix_without_space", "#not a statement, no space\n")
ls("statement_with_stray_delimiter", "# if true %}\nbroken\n# endif")
ls("mixed_with_tags", "{% if true %}tag{% endif %}\n# if true\nline\n# endif")
ls("for_with_filter", "# for i in [1,2,3] if i > 1\n{{ i }}\n# endfor")
ls("macro", "# macro m(a)\nM{{ a }}\n# endmacro\n{{ m(1) }}")
ls("with", "# with y = 2\n{{ y }}\n# endwith")
ls("filter_block", "# filter upper\nshout\n# endfilter")
ls("raw_block", "# raw\n# if true\n# endraw")
ls("loop_index", "# for i in [1,2]\n{{ loop.index }}\n# endfor")
ls("empty_body", "# if true\n# endif")
ls("blank_line_body", "# if true\n\n# endif")
ls("with_block_comment", "text\n# if true\n{{ 1 }}{# block comment #}\n# endif\ntail\n")
ls("percent_prefix", "% for i in [1,2]\nx{{ i }}\n% endfor",
   line_statement_prefix="%", line_comment_prefix="%%")
ls("percent_comment", "%% comment\nkept",
   line_statement_prefix="%", line_comment_prefix="%%")
ls("multichar_prefix", "$$ for i in [1]\n{{ i }}\n$$ endfor",
   line_statement_prefix="$$", line_comment_prefix="$$$")
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

# The environment offers four of jinja2's Undefined classes and the corpus
# graded only the default, so three of them were unexercised. These cover the
# one thing they must all agree on first: an undefined produced by *constant
# folding* is still the environment's class. The evaluator built them with the
# zero behaviour, so a StrictUndefined environment folded `{{ none.missing }}`
# to "" at compile time and never raised -- a strictness setting silently not
# applied, which is the failure mode strictness exists to prevent.
_FOLDED = [
    ("attr_on_none", "{{ none.missing }}"),
    ("item_on_none", "{{ none['missing'] }}"),
    ("attr_on_float", "{{ 1.5.missing }}"),
    ("attr_on_bool", "{{ true.missing }}"),
    ("missing_dict_key", "{{ {'a': 1}['b'] }}"),
    ("chained_on_none", "{{ none.a.b }}"),
    ("folded_length", "{{ none.missing|length }}"),
    ("folded_arith", "{{ none.missing + 1 }}"),
    ("folded_truth", "{% if none.missing %}t{% else %}f{% endif %}"),
]
for _kind in ("strict", "chainable", "debug", "default"):
    for _n, _src in _FOLDED:
        # |length on an undefined is a separate defect: StrictUndefined must
        # raise from __len__ and gojja2 answers 0. It is graded where that is
        # fixed; folding is not what is wrong with it.
        if (_kind, _n) == ("strict", "folded_length"):
            continue
        case(f"undefined/{_kind}_{_n}", _src, __settings__={"undefined": _kind})

# DebugUndefined renders the expression that failed instead of "", and jinja2's
# __str__ has no fallback: every undefined has one of three forms, chosen the
# way the error message is -- by whether there is a hint, an owner, and a key
# that is not a string. gojja2 rendered "" for the hint and index forms, so an
# out-of-range subscript and a filter's own undefined both vanished; and the
# subscript forms that carried a hint with the right text rendered it as
# "undefined value printed: ..." where jinja2 names the subscript.
_DEBUG = [
    ("name", "{{ nope }}", {}),
    ("attr_missing", "{{ d.missing }}", {"d": {"a": 1}}),
    ("item", "{{ d['missing'] }}", {"d": {"a": 1}}),
    ("index", "{{ seq[42] }}", {"seq": [1, 2, 3]}),
    ("index_str", "{{ s[9] }}", {"s": "ab"}),
    ("index_folded", "{{ [1,2][9] }}", {}),
    ("index_tuple", "{{ (1,2)[9] }}", {}),
    ("index_int_base", "{{ 1[0] }}", {}),
    ("key_not_str", "{{ [1,2][none] }}", {}),
    ("key_float", "{{ [1,2][1.5] }}", {}),
    ("slice_bad_stop", "{{ 'ab'[1:'x'] }}", {}),
    ("slice_bad_step", "{{ 'ab'[::1.5] }}", {}),
    ("slice_all_bad", "{{ 'ab'['a':'b':'c'] }}", {}),
    ("hint_from_filter", "{{ nope|first }}", {}),
    ("hint_from_empty", "{{ []|first }}", {}),
    ("in_string_filter", "{{ nope|string }}", {}),
    ("chained", "{{ nope.a }}", {}),
    ("printed_twice", "{{ nope }}{{ d.missing }}", {"d": {"a": 1}}),
]
for _n, _src, _ctx in _DEBUG:
    case(f"undefined/debug_{_n}", _src, __settings__={"undefined": "debug"}, **_ctx)
    # The same shapes under the default class, so making one of them render
    # cannot quietly change what the other does.
    case(f"undefined/plain_{_n}", _src, **_ctx)


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
case("filters/striptags_partial_tags", "{{ '<'|striptags }}|{{ '<b'|striptags }}|{{ 'a<!--c-->b'|striptags }}|{{ 'a < b'|striptags }}")
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
# count, find, rfind, index, rindex, startswith and endswith all take an
# optional start and end that select the slice they look at. They are slice
# indices: counted in characters, negative from the end, clamped out of range.
case("methods/string_search_bounds",
     "{{ 'Hello World'.find('o',1,2) }}|{{ 'Hello World'.count('o',1,2) }}|"
     "{{ 'Hello World'.find('o',5) }}|{{ 'Hello World'.find('o',5,8) }}|"
     "{{ 'Hello World'.rfind('o',0,6) }}|{{ 'Hello World'.count('o',-3) }}")
case("methods/string_search_bounds_clamp",
     "{{ 'Hello World'.count('o',100) }}|{{ 'Hello World'.count('o',-100) }}|"
     "{{ 'Hello World'.count('o',5,1) }}|{{ 'Hello World'.count('o',none,none) }}|"
     "{{ 'Hello World'.startswith('H',1) }}|{{ 'Hello World'.endswith('o',0,5) }}")
case("methods/string_search_bounds_unicode",
     "{{ 'h\u00e9llo w\u00f6rld'.find('l',1,4) }}|{{ 'h\u00e9llo w\u00f6rld'.count('l',1) }}")
case("errors/string_search_bound_not_an_integer", "{{ 'Hello'.count('o','x') }}")
case("errors/string_index_with_bounds", "{{ 'Hello'.index('o',0,2) }}")

# Python's round preserves the numeric type, so round(3, -1) is the int 0. The
# rounding is ties-to-even on the scaled value, and exact past 2**53.
case("filters/round_negative_precision",
     "{{ (5)|round(-1) }}|{{ (15)|round(-1) }}|{{ (25)|round(-1) }}|{{ (35)|round(-1) }}|"
     "{{ (-15)|round(-1) }}|{{ (99)|round(-1) }}|{{ (50)|round(-1) }}|{{ (150)|round(-2) }}")
case("filters/round_keeps_the_type",
     "{{ (3)|round(-1) }}|{{ (-4)|round(-1) }}|{{ (3)|round(0) }}|{{ (2.5)|round(-1) }}|"
     "{{ (10000000000000000000000)|round(-1) }}|{{ (10000000000000000000000)|round(-25) }}")

# do_round is three different things depending on the method, and it checks the
# method before it looks at anything else. "common" is Python's round(): the
# method is looked up on the *value*, so a type with no __round__ is refused
# before the precision is examined; None asks for an integer rather than a
# float, exactly; and bool is an int. "ceil" and "floor" raise ten to the
# precision instead, so they take a float one, fail on None at the exponent, and
# divide by zero once the power underflows.
case("filters/round_none_precision",
     "{{ 1.5|round(none) }}|{{ 2.5|round(none) }}|{{ 3.5|round(none) }}|"
     "{{ -2.5|round(none) }}|{{ 0.5|round(none) }}|{{ -0.0|round(none) }}|{{ 5|round(none) }}")
case("filters/round_bool_is_an_int",
     "{{ true|round(-1) }}|{{ true|round(0) }}|{{ false|round(-1) }}|"
     "{{ 1.5|round(true) }}|{{ 1.55|round(true) }}|{{ 1.5|round(false) }}")
case("filters/round_negative_precision_exact",
     "{{ 1e300|round(-300) }}|{{ 1e300|round(-301) }}|{{ 1.5|round(-400) }}|{{ -1.5|round(-400) }}")
case("filters/round_float_precision_ceil",
     "{{ 1.5|round(2.5, 'ceil') }}|{{ 1.5|round(-1.5, 'ceil') }}|{{ 1.5|round(-3, 'ceil') }}")
case("errors/round_method_first", "{{ 'x'|round(1.5, 'nope') }}")
case("errors/round_method_unhashable", "{{ 1|round(method=[]) }}")
case("errors/round_value_before_precision", "{{ 'abc'|round(1.5) }}")
case("errors/round_precision_not_whole", "{{ 1.5|round(1.5) }}")
case("errors/round_ceil_exponent_first", "{{ 'abc'|round(none, 'ceil') }}")
case("errors/round_ceil_not_real", "{{ 'abc'|round(0, 'ceil') }}")
case("errors/round_ceil_underflow", "{{ 1.5|round(-400, 'ceil') }}")

# |int with a base is Python's int(str, base): base 0 detects the prefix in
# either case, a matching prefix is allowed but not required, a sign does not
# hide it, and underscores separate digits -- including after a prefix.
case("filters/int_base_prefixes",
     "{{ '0x1f'|int(-1,0) }}|{{ '0X1F'|int(-1,0) }}|{{ '0b11'|int(-1,0) }}|"
     "{{ '0o17'|int(-1,0) }}|{{ '0x1f'|int(-1,16) }}|{{ '0B11'|int(-1,16) }}")

# int() takes exactly one sign. big.Int reads one of its own, so a repeated one
# cancelled out and answered a number -- and because |int falls back to its
# default rather than raising, a template got a plausible answer and no signal.
case("filters/int_double_sign",
     "{{ '--4'|int(-1) }}|{{ '++4'|int(-1) }}|{{ '+-4'|int(-1) }}|{{ '-+4'|int(-1) }}|"
     "{{ '-4'|int(-1) }}|{{ '+4'|int(-1) }}")
case("filters/int_double_sign_base",
     "{{ '--4'|int(-1,16) }}|{{ '--0x10'|int(-1,16) }}|{{ '-0x10'|int(-1,16) }}")
case("filters/int_from_join_sign", "{{ ('-4'|join(d='-'))|int }}")
case("filters/int_base_signs_and_underscores",
     "{{ '-0x10'|int(-1,16) }}|{{ '+0x10'|int(-1,0) }}|{{ '1_0'|int(-1,16) }}|"
     "{{ '0x_1f'|int(-1,16) }}|{{ '1__0'|int(-1,16) }}|{{ '_10'|int(-1,16) }}")
case("filters/int_base_edges",
     "{{ '0'|int(-1,16) }}|{{ 'z'|int(-1,36) }}|{{ '0x'|int(-1,16) }}|"
     "{{ '0x'|int(-1,36) }}|{{ '010'|int(-1,0) }}|{{ '010'|int(-1,8) }}")

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

# The indent is a pad *unit*, not a count: an integer is that many spaces but a
# string is used literally, so tojson("\t") indents with tabs. It is also only
# worked out once something needs indenting -- JSONEncoder.encode returns a str
# before it builds any -- so a bad unit passes unnoticed on a string value and
# raises on everything else.
case("filters/tojson_indent_str",
     "{{ [1,2]|tojson('  ') }}|{{ [1,2]|tojson('x') }}|{{ [1,2]|tojson('ab') }}|"
     "{{ [1,2]|tojson('') }}|{{ [1,2]|tojson(true) }}")
case("filters/tojson_indent_str_nested",
     "{{ [[1]]|tojson('x') }}|{{ {'a':1,'b':{'c':2}}|tojson('\t') }}|"
     "{{ 's'|tojson(1.5) }}|{{ 's'|tojson([1]) }}")
case("errors/tojson_indent_float", "{{ [1,2]|tojson(1.5) }}")
case("errors/tojson_indent_empty_list", "{{ []|tojson(1.5) }}")
case("errors/tojson_indent_scalar", "{{ 1|tojson(1.5) }}")
case("errors/tojson_indent_list_unit", "{{ {}|tojson([1]) }}")

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
case("filters/striptags_partial_entities", "{{ '&notit;|&notit|&not|&amp|&ampx|&#;|&;|&'|striptags }}")
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

# The globals are ordinary Python callables, and a call to one is bound the way
# Python binds it. range is a C function: it refuses keywords outright, and
# words too few and too many differently. cycler and joiner are classes, so the
# call binds against __init__ with self counted -- which is why joiner('-','x')
# reports three positional arguments where two were written. A Cycler is not
# callable at all, and a Joiner hands back the separator it was given rather
# than a string made of it.
case("errors/range_no_args", "{{ range() }}")
case("errors/range_too_many", "{{ range(1,2,3,4) }}")
case("errors/range_keyword", "{{ range(1,a=2) }}")
case("errors/range_count_before_type", "{{ range('x',1,2,3) }}")
case("errors/lipsum_too_many", "{{ lipsum(1,2,3,4,5) }}")
case("errors/lipsum_unexpected_kw", "{{ lipsum(1,2,3,4,5,a=1) }}")
case("errors/lipsum_multiple_values", "{{ lipsum(1,n=2) }}")
case("errors/cycler_keyword", "{{ cycler(1,a=2) }}")
case("errors/cycler_not_callable", "{% set c = cycler(1,2) %}{{ c() }}")
case("errors/cycler_next_arity", "{% set c = cycler(1,2) %}{{ c.next(1) }}")
case("errors/joiner_too_many", "{{ joiner('-','x') }}")
case("errors/joiner_keyword", "{{ joiner(1,sep=2) }}")
case("errors/joiner_call_arity", "{% set j = joiner('-') %}{{ j(1) }}")
case("globals/joiner_non_string_sep",
     "{% set j = joiner(1) %}{{ j() }}|{{ j() }}|"
     "{% set k = joiner(none) %}{{ k() }}|{{ k() }}|"
     "{% set m = joiner([1,2]) %}{{ m() }}|{{ m() }}")

# loop, a block reference and the macro a {% call %} block builds are Python
# objects too, so a call to one binds against a method with self counted --
# before the body gets to complain about a missing 'recursive' marker or an
# empty cycle. The caller macro has no name, and CPython prints that as None.
case("errors/loop_cycle_keyword", "{% for i in [1,2] %}{{ loop.cycle(a=1) }}{% endfor %}")
case("errors/loop_changed_keyword", "{% for i in [1,2] %}{{ loop.changed(a=1) }}{% endfor %}")
case("errors/loop_call_missing", "{% for i in [1,2] %}{{ loop() }}{% endfor %}")
case("errors/loop_call_too_many", "{% for i in [1,2] %}{{ loop(i,i) }}{% endfor %}")
case("errors/loop_call_not_recursive", "{% for i in [1,2] %}{{ loop(i) }}{% endfor %}")
case("errors/block_call_arity", "{% block b %}x{% endblock %}{{ self.b(1) }}")
case("errors/block_call_keyword", "{% block b %}x{% endblock %}{{ self.b(a=1) }}")
case("errors/caller_too_many",
     "{% macro m() %}{{ caller(1) }}{% endmacro %}{% call m() %}x{% endcall %}")
case("errors/caller_keyword",
     "{% macro m() %}{{ caller(a=1) }}{% endmacro %}{% call m() %}x{% endcall %}")
case("runtime/loop_call_keyword",
     "{% for i in [[1],[2]] recursive %}{{ loop(iterable=i) if i is sequence else i }}{% endfor %}")
case("runtime/macro_repr",
     "{% macro m() %}{{ caller }}{% endmacro %}{% call m() %}x{% endcall %}|"
     "{% macro n() %}{% endmacro %}{{ n }}")

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

# |sort hands reverse to sorted(), which takes it as an integer, so a string or
# a None is refused there rather than read for its truth -- only case_sensitive
# is a plain `if` in jinja2's own code. And |center's width has no None to fall
# back on: 80 is the default for an argument that was not written, and an
# explicit None reaches str.center.
case("errors/sort_reverse_none", "{{ [3,1,2]|sort(none) }}")
case("errors/sort_reverse_str", "{{ [3,1,2]|sort('x') }}")
case("errors/sort_value_before_reverse", "{{ 1|sort(none) }}")

# `is divisibleby` is `value % num == 0`, a comparison and not a truth test.
# They part company wherever % is not division: on a string it is formatting.
# And CPython shares one empty tuple, so `() is ()` is true where no other pair
# of separately built containers is.
case("tests/divisibleby_string_format",
     "{{ '' is divisibleby([]) }}|{{ '' is divisibleby({}) }}|"
     "{{ 'a%s' is divisibleby([1]) }}|{{ 4 is divisibleby(2) }}|{{ 5 is divisibleby(2) }}")
case("tests/sameas_empty_tuple",
     "{{ () is sameas(()) }}|{{ (1,) is sameas((1,)) }}|{{ [] is sameas([]) }}|"
     "{{ () is sameas([]) }}")

# The methods on str, list, dict and tuple are bound by CPython before they run,
# and gojja2 checked none of it: `[1].append("a", 1)` answered None. The
# wordings are not derivable from a signature -- a method that takes nothing,
# one that takes exactly one, and one with an optional second all word it
# differently -- so they are probed; see tools/oracle/gen_methods.py.
case("errors/method_takes_no_arguments", "{{ 'ab'.upper(1) }}")
case("errors/method_takes_no_arguments_list", "{{ [1].clear(none) }}")
case("errors/method_exactly_one_too_many", "{{ [1].append('a', 1) }}")
case("errors/method_exactly_one_too_few", "{{ [1].append() }}")
case("errors/method_exactly_two", "{{ [1].insert(0) }}")
case("errors/method_range_too_few", "{{ 'ab'.center() }}")
case("errors/method_range_too_many", "{{ 'ab'.center(1, 'x', 2) }}")
case("errors/method_count_too_many", "{{ 'ab'.count('a', 1, 2, 3) }}")
case("errors/method_get_too_many", "{{ {'a':1}.get('a', 1, 2) }}")
case("errors/method_no_keywords", "{{ 'ab'.upper(zz=1) }}")
case("errors/method_invalid_keyword", "{{ 'a,b'.split(zz=1) }}")
case("errors/method_keyword_before_count", "{{ 'ab'.upper(1, zz=1) }}")
case("methods/arity_accepted",
     "{{ 'ab'.upper() }}|{{ 'a,b'.split(sep=',') }}|{{ 'a,b,c'.split(',', maxsplit=1) }}|"
     "{{ 'ab'.center(6, '-') }}|{{ {'a':1}.get('a', 2) }}|{{ '{0}{k}'.format(1, k=2) }}")

# A method's optional argument has a default for being *absent*, not for being
# None: CPython hands an explicit None to the same check every other value goes
# through. A string search's bounds really are optional and say so in their own
# message, which is what tells the two apart.
case("errors/method_none_expandtabs", "{{ 'ab'.expandtabs(none) }}")
case("errors/method_none_zfill", "{{ 'ab'.zfill(none) }}")
case("errors/method_none_maxsplit", "{{ 'a,b'.split('a', none) }}")
case("errors/method_none_replace_count", "{{ 'ab'.replace('a', 'b', none) }}")
case("errors/method_none_pop", "{{ [1].pop(none) }}")
case("errors/method_none_insert", "{{ [1].insert(none, 1) }}")
case("errors/method_none_keepends", "{{ 'ab'.splitlines(none) }}")
case("errors/method_float_keepends", "{{ 'ab'.splitlines(1.5) }}")
case("errors/method_none_fillchar", "{{ 'ab'.center(4, none) }}")
case("errors/method_none_encoding", "{{ 'ab'.encode(none) }}")
case("errors/method_none_errors", "{{ 'ab'.encode('utf-8', none) }}")
case("methods/optional_none_bounds",
     "{{ 'ab'.count('a', none) }}|{{ 'ab'.find('a', none) }}|"
     "{{ 'ab'.startswith('a', none) }}|{{ 'ab'.endswith('b', 0, none) }}|"
     "{{ 'ab'.splitlines(1) }}")

# list.index and tuple.index search only between start and stop, and answer an
# index into the whole list. Both were read and then ignored. Their bounds have
# no None form, which the message says by leaving "or None" out.
case("methods/seq_index_window",
     "{{ [1,2,1].index(1) }}|{{ [1,2,1].index(1, 1) }}|{{ [1,2,1].index(1, 1, 3) }}|"
     "{{ [1,2,1].index(1, -1) }}|{{ [1,2,1].index(1, 0, -1) }}|{{ (1,2,1).index(1, 1) }}")
case("errors/seq_index_window_empty", "{{ [1,2,1].index(1, 1, 2) }}")
case("errors/seq_index_bounds_none", "{{ [1,2,1].index(1, none) }}")
case("errors/seq_index_bounds_float", "{{ [1,2,1].index(1, 1.5) }}")

# What CPython says about an argument it does not like, and when it says it. The
# wordings are not interchangeable: a string search raises from a helper shared
# by all of them and names neither the method nor the parameter; startswith
# names itself; strip names itself and not the type; replace and maketrans
# number their arguments; and the parser's messages call the None singleton
# "None" where a hand-written check says "NoneType".
case("errors/search_arg_bare", "{{ 'ab'.count(1) }}")
case("errors/search_arg_bare_find", "{{ 'ab'.find(none) }}")
case("errors/search_bounds_before_sub", "{{ 'ab'.count(1, 1.5) }}")
case("errors/startswith_names_itself", "{{ 'ab'.startswith(1) }}")
case("errors/endswith_names_itself", "{{ 'ab'.endswith(none) }}")
case("errors/strip_names_itself", "{{ 'ab'.lstrip([]) }}")
case("errors/removesuffix_names_itself", "{{ 'ab'.removesuffix(none) }}")
case("errors/replace_numbers_its_args", "{{ 'ab'.replace('a', none) }}")
case("errors/replace_numbers_its_args_first", "{{ 'ab'.replace(1, none) }}")
case("errors/maketrans_second_first", "{{ 'ab'.maketrans(1, none) }}")
case("errors/maketrans_first_arg", "{{ 'ab'.maketrans(1, 'b') }}")
case("errors/join_any_iterable", "{{ '-'.join(none) }}")
case("errors/encode_errors_before_codec", "{{ 'ab'.encode('nosuch', 1) }}")

# Neither format_map nor translate converts the argument it is handed.
# translate is `table[ord(c)]` per character, catching LookupError, so an empty
# string never touches the table and anything subscriptable by an integer will
# do. format_map subscripts its mapping once per *named* field.
case("methods/translate_is_lazy",
     "[{{ ''.translate(1) }}]|{{ 'a'.translate('xyz') }}|{{ 'a'.translate(['z']) }}|"
     "{{ 'a'.translate({97: 'Z'}) }}|{{ 'ab'.translate({98: 'Z'}) }}")
case("errors/translate_not_subscriptable", "{{ 'a'.translate(1) }}")
case("methods/format_map_is_lazy",
     "{{ 'ab'.format_map(1) }}|{{ 'ab'.format_map(none) }}|{{ '{a}'.format_map({'a': 1}) }}")
case("errors/format_map_positional", "{{ '{0}'.format_map({'a': 1}) }}")
case("errors/format_map_list", "{{ '{a}'.format_map([1]) }}")
case("errors/format_map_str", "{{ '{a}'.format_map('x') }}")
case("errors/format_map_none", "{{ 'x{a}'.format_map(none) }}")

# The format mini-language, from a 40,000-case sweep against CPython. A grouping
# option is allowed or refused by the format *code* alone, before the value is
# looked at; a type with no __format__ of its own never reads the spec; a
# leading zero fills any value but aligns only a number; the alternate form
# keeps a float's point and stops at the exponent; padding zeros join a grouped
# number and take separators of their own; and a precision with no type is 'g'
# with the threshold one place lower.
case("format/grouping_by_code",
     "{{ '{:,f}'.format(1.5) }}|{{ '{:_x}'.format(1048575) }}|{{ '{:_b}'.format(255) }}|"
     "{{ '{:_d}'.format(1048575) }}|{{ '{:,g}'.format(123456.0) }}|{{ '{:,.0E}'.format(1) }}")
case("format/zero_fill_and_align",
     "[{{ '{:0.0s}'.format('ab') }}]|{{ '{:05}'.format('ab') }}|{{ '{:>06,}'.format(1) }}|"
     "{{ '{:=-06g}'.format(1.5) }}")
case("format/alternate_keeps_the_point",
     "{{ '{:#.0f}'.format(1.5) }}|{{ '{:#.0e}'.format(1.5) }}|{{ '{:#.3}'.format(1.5) }}|"
     "{{ '{:#.0%}'.format(1.5) }}")
case("format/padding_is_grouped",
     "{{ '{:06,d}'.format(1) }}|{{ '{:-#06,.0%}'.format(1) }}|{{ '{:=+#06_.0F}'.format(-1) }}")
case("format/typeless_precision",
     "{{ '{:.0}'.format(1.5) }}|{{ '{:.1}'.format(1.5) }}|{{ '{:.2}'.format(1.5) }}|"
     "{{ '{:.3}'.format(12.0) }}|{{ '{:.2}'.format(123456.789) }}|{{ '{:.0g}'.format(1.5) }}")
case("errors/format_group_with_code", "{{ '{:,x}'.format(1.5) }}")
case("errors/format_group_with_n", "{{ '{:,n}'.format(5) }}")
case("errors/format_group_with_str", "{{ '{:+_s}'.format('ab') }}")
case("errors/format_string_space", "{{ '{: s}'.format('ab') }}")
case("errors/format_string_alternate", "{{ '{:=#s}'.format('ab') }}")
case("errors/format_int_precision_first", "{{ '{:+.3c}'.format(5) }}")
case("errors/format_int_c_alternate", "{{ '{:#c}'.format(5) }}")
case("errors/format_object_ignores_spec", "{{ '{:,n}'.format(none) }}")
case("errors/format_c_too_large", "{{ '{:c}'.format(2**70) }}")

# A slice index too big for the machine is still an integer, and CPython clamps
# it. And jinja2's getitem catches TypeError and LookupError alike, so a key the
# container cannot take renders as nothing rather than raising.
case("slice/huge_bounds",
     "{{ 'abcde'[:2**70] }}|{{ 'abcde'[1:2**70] }}|{{ 'abcde'[-(2**70):2] }}|"
     "[{{ 'abcde'[2**70:] }}]|{{ [1,2,3][:2**70] }}|[{{ [1,2,3][:-(2**70)] }}]")
case("slice/huge_step",
     "{{ 'abcde'[::2**70] }}|{{ 'abcde'[::-(2**70)] }}|"
     "{{ [1,2,3][::9223372036854775807] }}|{{ [1,2,3][::-9223372036854775808] }}")
case("slice/bad_key_is_undefined",
     "[{{ xs[none] }}][{{ xs[1.5] }}][{{ xs[[]] }}][{{ d[[]] }}][{{ xs[9] }}]",
     xs=[1, 2, 3], d={"a": 1})
case("errors/center_width_none", "{{ 'abc'|center(none) }}")
case("filters/sort_reverse_int",
     "{{ [3,1,2]|sort(1) }}|{{ [3,1,2]|sort(0) }}|{{ [3,1,2]|sort(true) }}|"
     "{{ ['b','A']|sort(false, none) }}|{{ ['b','A']|sort(false, 1) }}")

# do_int wraps the whole conversion in `except (TypeError, ValueError)`, so a
# base that is not usable as one is swallowed rather than reported -- and the
# base is only ever passed on for a str value. do_random is random.choice,
# which is len(seq) and then seq[i]: a value with no length is refused as len()
# refuses it, and a mapping is subscripted by the *index*.
case("filters/int_unusable_base",
     "{{ '10'|int(2, 1.5) }}|{{ '10'|int(2, 'x') }}|{{ '10'|int(2, none) }}|"
     "{{ '10'|int(2, 2**70) }}|{{ '10'|int(2, true) }}|{{ 1.9|int(2, 'x') }}|"
     "{{ 'abc'|int(2, 1.5) }}|{{ '10'|int(2, 16) }}|{{ '0x1f'|int(2, none) }}")
case("errors/random_no_len", "{{ 1|random }}")
case("errors/random_mapping_index", "{{ {'a':1}|random }}")
case("filters/random_empty",
     "[{{ []|random }}][{{ {}|random }}][{{ ''|random }}][{{ nope|random }}]")
case("filters/random_single", "{{ [7]|random }}|{{ 'q'|random }}|{{ {0:'z'}|random }}")

# make_attrgetter substitutes its default with `if default is not None`, so an
# explicit None is no default at all: the undefined stays, and whatever it does
# next is what happens. 0 and "" are not None, and still stand in.
ROWS = {"rows": [{"a": 1}, {}]}
case("errors/groupby_none_default", "{{ rows|groupby('a', none)|list }}", **ROWS)
case("errors/groupby_none_default_index", "{{ [1,2]|groupby(1, none)|list }}")
case("filters/groupby_zero_default", "{{ rows|groupby('a', 0)|list }}", **ROWS)
case("filters/map_none_default",
     "{{ [1,2]|map(attribute='x', default=none)|list }}|"
     "{{ [1,2]|map(attribute='x')|list }}|"
     "{{ [1,2]|map(attribute='x', default='d')|list }}")
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

# |truncate never converts its arguments. It asserts `length >= len(end)` and
# `leeway >= 0` with Python's own comparison -- so a float compares and only has
# to be whole at the slice, a non-number refuses by naming the operator, and the
# assertion reports the value as Python prints it (True, not 1). The end is
# concatenated, not formatted, so a list end on a string input is a TypeError.
case("filters/truncate_float_args",
     '{{ t|truncate(10, false, "...", 1.5) }}|{{ "abc"|truncate(5.5) }}|'
     '{{ t|truncate(10, false, "...", none) }}|{{ t|truncate(true, false, "") }}',
     t="  the quick brown fox jumps over the lazy dog  ")
case("errors/truncate_length_none", "{{ 'abc'|truncate(none) }}")
case("errors/truncate_length_str", "{{ 'abc'|truncate('x') }}")
case("errors/truncate_length_bool", "{{ 'abc'|truncate(true) }}")
case("errors/truncate_length_float", "{{ 'abc'|truncate(2.0) }}")
case("errors/truncate_length_unwhole",
     '{{ t|truncate(3.0) }}', t="  the quick brown fox jumps over the lazy dog  ")
case("errors/truncate_leeway_negative",
     '{{ t|truncate(10, false, "...", -1) }}', t="  the quick brown fox jumps over the lazy dog  ")
case("errors/truncate_end_concat",
     '{{ t|truncate(10, true, ["z"]) }}', t="  the quick brown fox jumps over the lazy dog  ")
case("errors/urlize_rel_type", '{{ "x"|urlize(rel=4) }}')
case("filters/urlize_attrs", '{{ "http://a.com"|urlize(rel="me") }}|{{ "http://a.com"|urlize(target="_b") }}')

# |urlize looks at its arguments where jinja2 looks at them. The trim limit is
# closed over by trim_url and compared once per link, so a text with no links
# never examines it; target and rel are decided by truthiness, so an empty list
# writes no attribute and is not asked for a split; and every extra scheme is
# matched against a regexp before anything is linked.
URLTEXT = {"u": "go https://example.com/long/path now", "plain": "no links here"}
case("filters/urlize_limit_unused", "{{ plain|urlize('x') }}|{{ plain|urlize(1.5) }}", **URLTEXT)
case("filters/urlize_falsey_attrs",
     "{{ u|urlize(none, false, []) }}|{{ u|urlize(none, false, none, 0) }}", **URLTEXT)
case("filters/urlize_extra_schemes",
     "{{ f|urlize(none, false, none, none, ['ftp://']) }}|{{ f|urlize(none, false, none, none, ['\u00fc2:']) }}",
     f="try ftp://x.com now")
case("errors/urlize_limit_str", "{{ u|urlize('x') }}", **URLTEXT)
case("errors/urlize_limit_float", "{{ u|urlize(10.0) }}", **URLTEXT)
case("errors/urlize_rel_truthy", "{{ u|urlize(none, false, none, [1]) }}", **URLTEXT)
case("errors/urlize_scheme_invalid", "{{ u|urlize(none, false, none, none, ['ftp']) }}", **URLTEXT)
case("errors/urlize_scheme_type", "{{ u|urlize(none, false, none, none, [1]) }}", **URLTEXT)

# urlize is built out of \w, \d and \s, and all three mean more in Python than
# in Go: a URL with a non-ASCII host or path is linked, an address written with
# Arabic-Indic digits is an address, and a word after a non-breaking space --
# or a separator control, or NEL -- is a word of its own.
case("filters/urlize_unicode_host",
     "{{ a|urlize }}|{{ b|urlize }}|{{ c|urlize }}",
     a="https://ex\u00e4mple.com/path", b="www.f\u00f6o.org",
     c="https://\u4e2d\u6587.cn/\u8def\u5f84")
case("filters/urlize_unicode_mail",
     "{{ a|urlize }}|{{ b|urlize }}",
     a="b\u00f6b@ex\u00e4mple.com", b="mailto:b\u00f6b@ex\u00e4mple.com")
case("filters/urlize_unicode_digits", "{{ a|urlize }}", a="http://\u0661\u0662\u0663.1.1.1")
case("filters/urlize_unicode_space",
     "{{ a|urlize }}|{{ b|urlize }}|{{ c|urlize }}|{{ d|urlize }}",
     a="a.com\u00a0http://b.org", b="http://g.org/p\u00a0q",
     c="http://e.org\u001cnext", d="http://f.org\u0085next")


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


# --- float() and int() read a bytes -------------------------------------------
# Python's float() and int() take a bytes exactly as they take a str. Both
# filters asked "is this a str", so a bytes fell through to the filter's
# *default*: `{{ "1.5".encode()|float }}` answered 0.0 rather than 1.5, which is
# a wrong number rather than an error, and silent.
case("filters/float_reads_bytes", '{{ "1.5".encode()|float }}|{{ " 1.5 ".encode()|float }}|{{ "inf".encode()|float }}|{{ "a".encode()|float }}|{{ "a".encode()|float(9) }}')
case("filters/int_reads_bytes", '{{ "15".encode()|int }}|{{ " 15 ".encode()|int }}|{{ "-15".encode()|int }}|{{ "1.5".encode()|int }}|{{ "a".encode()|int }}')
case("filters/filesizeformat_reads_bytes", '{{ "1000".encode()|filesizeformat }}')
case("errors/filesizeformat_bad_bytes", '{{ "a".encode()|filesizeformat }}')
# do_int passes the base only for a str, so a bytes is always base ten.
case("filters/int_base_is_for_strings_only",
     '{{ "ff"|int(0, 16) }}|{{ "ff".encode()|int(0, 16) }}|{{ "15".encode()|int(0, 16) }}|{{ "0x1f".encode()|int(0, 0) }}')
# wordwrap is the one filter a bytes gets past the attribute lookup of, so its
# failure is textwrap's -- and an empty bytes never reaches textwrap at all.
case("filters/wordwrap_empty_bytes", '{{ "".encode()|wordwrap }}')
case("errors/wordwrap_bytes", '{{ "a".encode()|wordwrap }}')
case("errors/wordwrap_non_string", '{{ 1|wordwrap }}')
# json.dumps builds the indent before it discovers it cannot serialise the
# value; only a str short-circuits before the indent is touched.
case("errors/tojson_indent_before_bytes_refusal", '{{ "a".encode()|tojson(1.5) }}')
case("filters/tojson_string_ignores_indent", '{{ "s"|tojson(1.5) }}')

# --- a raw tag with nothing after it -------------------------------------------
# jinja2 looks for a raw body with a regex that requires one, so an empty
# remainder never reaches the "missing end" branch and the tokenizer stops:
# `{% raw %}` renders "" and `{% raw %}abc` raises. The boundary is exactly "is
# there anything left", which is why one trailing space brings the error back.
case("syntax/raw_empty_at_eof", "{% raw %}")
case("syntax/raw_empty_at_eof_trimmed", "{%- raw -%}")
case("syntax/raw_empty_after_text", "a{% raw %}")
case("errors/raw_unterminated_with_body", "{% raw %}abc")
case("errors/raw_unterminated_whitespace_body", "{% raw %}   ")

# --- a macro with a repeated parameter name ------------------------------------
# jinja2 compiles a macro to a Python function, so the duplicate reaches the
# Python compiler; the error it raises names the identifier jinja2 *generated*
# and a line of the generated module. See docs/divergences.md.
case("macro/duplicate_parameter", "{% macro m(a, a) %}{{ a }}{% endmacro %}{{ m(1, 2) }}")

# --- dict views are views ------------------------------------------------------
# d.keys(), d.values() and d.items() returned lists, which a template can tell
# apart: the repr, the `is sequence` test, indexing, json.dumps, and -- the one
# that is not cosmetic -- that a view tracks the dict it came from.
#
# Not the lazy-sequence divergence: that is about the map/select/items
# *filters*, which return generators in jinja2 and lists here on purpose.
case("methods/dict_view_reprs", "{% set d = {'b': 2, 'a': 1} %}{{ d.keys() }}|{{ d.values() }}|{{ d.items() }}")
case("methods/dict_view_is_not_a_sequence",
     "{% set d = {'b': 2, 'a': 1} %}{{ d.keys() is sequence }}|{{ d.keys() is iterable }}|{{ d.keys()[0] }}|{{ d.keys()|length }}")
case("methods/dict_view_walks",
     "{% set d = {'b': 2, 'a': 1} %}{{ d.keys()|list }}|{{ d.keys()|sort }}|{{ d.values()|sum }}|{{ 'a' in d.keys() }}")
# A view is live, which a list cannot be.
case("methods/dict_view_tracks_the_dict",
     "{% set d = {'b': 2} %}{% set k = d.keys() %}{{ d.update({'c': 3}) }}{{ k|list }}|{{ k|length }}")
case("errors/dict_view_not_json", "{{ {'a': 1}.keys()|tojson }}")
# Keys and items compare as sets; a values view compares by identity.
case("methods/dict_view_equality",
     "{{ {'a':1}.keys() == {'a':2}.keys() }}|{{ {'a':1}.keys() == {'b':1}.keys() }}"
     "|{{ {'a':1}.items() == {'a':2}.items() }}|{{ {'a':1,'b':2}.keys() == {'b':2,'a':1}.keys() }}")
case("methods/dict_view_values_equality", "{% set d = {'a': 1} %}{{ d.values() == d.values() }}|{{ d.keys() == d.keys() }}")

# --- the bytes methods --------------------------------------------------------
# bytes had one method, decode, so the other forty-one were attribute errors on
# a type the engine otherwise supports fully. They are not the str methods:
# case and classification are ASCII only, positions are bytes, and arguments
# must be bytes-like -- several taking an integer as a byte value.
case("methods/bytes_case_is_ascii_only",
     '{{ "aBc dEf".encode().upper() }}|{{ "aBc dEf".encode().title() }}'
     '|{{ "éß".encode().upper() }}|{{ "éß".encode().swapcase() }}')
case("methods/bytes_title_boundary", '{{ "a1b".encode().title() }}|{{ "aBc dEf".encode().capitalize() }}')
case("methods/bytes_classification",
     '{{ "abc".encode().isalpha() }}|{{ "é".encode().isalpha() }}|{{ "".encode().isalpha() }}'
     '|{{ "".encode().isascii() }}|{{ "é".encode().isascii() }}|{{ "Ab".encode().istitle() }}')
case("methods/bytes_search",
     '{{ "aBc dEf".encode().find("c".encode()) }}|{{ "aBc dEf".encode().find(66) }}'
     '|{{ "aBc dEf".encode().count("c".encode()) }}|{{ "aBc dEf".encode().rfind("z".encode()) }}')
# The start is resolved but not clamped up: past the end nothing is found.
case("methods/bytes_search_bounds",
     '{{ "abc".encode().find("".encode(), 3) }}|{{ "abc".encode().find("".encode(), 4) }}'
     '|{{ "abc".encode().count("".encode()) }}|{{ "abc".encode().startswith("".encode(), 4) }}')
case("errors/bytes_index_not_found", '{{ "ab".encode().index("zz".encode()) }}')
case("errors/bytes_needs_bytes", '{{ "ab".encode().replace("a", "X") }}')
case("errors/bytes_startswith_needs_bytes", '{{ "ab".encode().startswith("a") }}')
case("methods/bytes_split_and_strip",
     '{{ "a b  c".encode().split() }}|{{ "a,b,,c".encode().split(",".encode()) }}'
     '|{{ "  xy  ".encode().strip() }}|{{ "a,b".encode().partition(",".encode()) }}')
case("methods/bytes_splitlines", '{{ "l1\nl2\r\nl3".encode().splitlines() }}|{{ "l1\nl2".encode().splitlines(true) }}')
case("methods/bytes_pad", '{{ "ab".encode().center(7, "*".encode()) }}|{{ "42".encode().zfill(5) }}|{{ "a\tb".encode().expandtabs(4) }}')
case("methods/bytes_join", '{{ "-".encode().join(["a".encode(), "b".encode()]) }}')
case("errors/bytes_join_item", '{{ "-".encode().join(["a", "b"]) }}')
case("methods/bytes_hex", '{{ "ab".encode().hex() }}|{{ "abcde".encode().hex("-", 2) }}|{{ "abcde".encode().hex("-", -2) }}')
case("methods/bytes_translate",
     '{{ "abc".encode().translate(none, "b".encode()) }}'
     '|{{ "abc".encode().translate("x".encode().maketrans("abc".encode(), "xyz".encode())) }}')
case("methods/int_from_bytes",
     '{{ (0).from_bytes("a".encode(), "big") }}|{{ (0).from_bytes("ab".encode(), "little") }}'
     '|{{ (0).from_bytes("ÿ".encode(), "big", signed=true) }}')
case("methods/float_fromhex", '{{ (0.0).fromhex("0x1.8p+0") }}|{{ (0.0).fromhex("1.8") }}|{{ (0.0).fromhex("-0x1.8p-1") }}')
case("errors/float_fromhex_bad", '{{ (0.0).fromhex("zz") }}')

# --- where center puts the odd character --------------------------------------
# On the left when the margin and the width are both odd, and on the right
# otherwise: CPython computes it as `marg / 2 + (marg & width & 1)`. gojja2 read
# it as "the odd one goes right", which agrees whenever the margin is even --
# which is why every case here has an odd one.
case("methods/center_odd_margin", '{{ "ab".center(5) }}|{{ "ab".center(7) }}|{{ "abcd".center(7) }}')
case("methods/center_odd_margin_even_width", '{{ "abc".center(6) }}|{{ "a".center(4) }}')
case("methods/center_even_margin", '{{ "ab".center(6) }}|{{ "abc".center(7) }}')
case("filters/center_odd_margin", '{{ "ab"|center(5) }}|{{ "abc"|center(6) }}')
case("methods/center_odd_margin_fill", '{{ "ab".center(5, "*") }}|{{ "abc".center(6, "*") }}')

# --- bytes is a sequence of integers ------------------------------------------
# Indexing a bytes already gave the byte value, and iteration, len, comparison,
# concatenation and the sequence filters were all right. Slicing was missing
# entirely -- `x[0]` answered and `x[0:1]` said the object was not
# subscriptable -- and membership refused an integer where CPython reads it as
# the byte value to look for.
case("membership/bytes_slice", "{{ \"ab\".encode()[0:1] }}|{{ \"ab\".encode()[::-1] }}|{{ \"abcdef\".encode()[1:5:2] }}|{{ \"ab\".encode()[:] }}")
# Positions are bytes, not code points.
case("membership/bytes_slice_counts_bytes", '{{ "\u00e9".encode()|length }}|{{ "\u00e9".encode()[0:1]|length }}')
case("membership/bytes_contains_int", '{{ 97 in "ab".encode() }}|{{ 0 in "ab".encode() }}|{{ 255 in "ab".encode() }}|{{ true in "ab".encode() }}')
case("membership/bytes_contains_int_out_of_range", '{{ 256 in "ab".encode() }}')
case("membership/bytes_contains_negative", '{{ -1 in "ab".encode() }}')
case("membership/bytes_contains_other", '{{ "a" in "ab".encode() }}')

# --- tojson sorts keys as keys, then converts them -----------------------------
# json.dumps with sort_keys sorts the key *objects* and converts them
# afterwards. gojja2 converted first and sorted the text, which is a different
# order for numeric keys -- "100" precedes "20" as text -- and which let a dict
# mixing key types serialise where CPython refuses to compare them.
case("filters/tojson_numeric_key_order", "{{ {100: 1, 20: 2, 3: 3}|tojson }}|{{ {10: 1, 9: 2}|tojson }}")
case("filters/tojson_big_key_order", "{{ {2**70: 1, 3: 2}|tojson }}")
case("filters/tojson_text_key_order", '{{ {"10": 1, "9": 2}|tojson }}|{{ {"b": 1, "a": 2}|tojson }}')
case("filters/tojson_mixed_keys_refused", '{{ {1: 1, "a": 2}|tojson }}')
case("filters/tojson_mixed_bool_keys_refused", '{{ {none: 1, true: 2}|tojson }}')
# A key that is not already a string takes JSON's spelling, not Python's: str()
# gives "True" and "None" where JSON wants "true" and "null".
case("filters/tojson_key_spelling", "{{ {true: 1}|tojson }}|{{ {false: 1}|tojson }}|{{ {none: 1}|tojson }}|{{ {true: 1, false: 2}|tojson }}")
case("filters/tojson_numeric_key_spelling", "{{ {1: 1}|tojson }}|{{ {-0.0: 1}|tojson }}")
# bytes has no JSON type and json.dumps refuses it; writing it out as a string
# invented a document CPython will not produce.
case("filters/tojson_bytes_refused", '{{ "a".encode()|tojson }}')
case("filters/tojson_bytes_in_list_refused", '{{ ["a".encode()]|tojson }}')

# --- full case mapping and cased characters -----------------------------------
# Python's case operations are full mappings over cased characters; Go's are
# simple mappings over general categories. Both halves differed. "\u00df".upper()
# is "SS" and was "\u00df"; casefold was implemented as lower, which is wrong for
# 298 code points; and islower/isupper rest on Unicode's Cased property, which
# Go's IsLower/IsUpper do not carry, so 370 code points answered wrongly.
case("methods/case_full_upper", '{{ "\u00df".upper() }}|{{ "\ufb03".upper() }}|{{ "\u0390".upper() }}|{{ "\u0587".upper() }}')
case("methods/case_full_lower", '{{ "\u0130".lower() }}|{{ "\u0130"|lower }}')
case("methods/case_full_title", '{{ "\u00df".title() }}|{{ "\u00dfx y\u00df".title() }}')
case("methods/case_full_capitalize", '{{ "\u00df".capitalize() }}|{{ "\ufb03".capitalize() }}')
case("methods/case_full_swapcase", '{{ "\u00df".swapcase() }}|{{ "\u0130".swapcase() }}')
# casefold is not lowercase: it folds for caseless comparison.
case("methods/case_casefold", '{{ "\u00df".casefold() }}|{{ "\u03c2".casefold() }}|{{ "\u00b5".casefold() }}|{{ "\u017f".casefold() }}|{{ "\u0130".casefold() }}')
# Cased beyond Lu/Ll: the ordinals, the modifier letters, the Roman numerals.
case("methods/case_cased_predicates",
     '{{ "\u00aa".islower() }}|{{ "\u02b0".islower() }}|{{ "\u0345".islower() }}'
     '|{{ "\u2160".isupper() }}|{{ "\u24b6".isupper() }}|{{ "\u2160".istitle() }}')
case("tests/case_is_lower_upper", '{{ "\u00aa" is lower }}|{{ "\u2160" is upper }}|{{ "\u01c5" is upper }}')
# A word boundary in title() is an uncased character, not a non-letter.
case("methods/title_boundary_is_cased", '{{ "a1b".title() }}|{{ "2nd place".title() }}|{{ "x-ray".title() }}')
# jinja2's |title is not str.title(): it uses upper() on the first character
# where the method uses the titlecase mapping, and for the sharp s they differ.
case("filters/title_filter_is_not_str_title", '{{ "\u00df"|title }}|{{ "\u00df".title() }}|{{ "\u00dfx y\u00df"|title }}')
# The case-insensitive collation paths go through lower() too.
case("filters/case_insensitive_sort", '{{ ["\u00df","B","a"]|sort(case_sensitive=false) }}')

# --- importing a template that extends another --------------------------------
# A TemplateModule's str() is what the imported template rendered, and a
# template that extends renders through its parent -- so an extending module is
# the parent's output with the child's blocks, and a name set at the top level
# anywhere in the chain is exported.
#
# gojja2 ran the imported template's own body and stopped, which for an
# extending template emits nothing: the module was empty and exported nothing
# from up the chain. An {% include %} of the same template was already right,
# so the two disagreed about what one template renders.
_CHAIN = {
    "base.html": "B[{% block x %}bx{% endblock %}]{% set fromBase = 'FB' %}",
    "mid.html": '{% extends "base.html" %}{% block x %}mx{% endblock %}{% set fromMid = \'FM\' %}',
    "deep.html": '{% extends "mid.html" %}{% block x %}dx{% endblock %}{% set v = 7 %}{% macro m() %}M{% endmacro %}',
}
case("modules/import_extending_str", '[{% import "deep.html" as m %}{{ m }}]', __templates__=_CHAIN)
case("modules/import_extending_one_level", '[{% import "mid.html" as m %}{{ m }}]', __templates__=_CHAIN)
case("modules/import_extending_exports", '[{% import "deep.html" as m %}{{ m.v }}|{{ m.m() }}]', __templates__=_CHAIN)
case("modules/import_extending_chain_exports",
     '[{% import "deep.html" as m %}{{ m.fromMid }}|{{ m.fromBase }}]', __templates__=_CHAIN)
case("modules/import_extending_matches_include",
     '[{% import "deep.html" as m %}{{ m }}][{% include "deep.html" %}]', __templates__=_CHAIN)

# --- a range holds its bounds exactly -----------------------------------------
# range() builds a Python object holding Python ints, so bounds past an int64
# are legal: the range simply cannot be walked far. Printing it, deciding
# membership, indexing, slicing and asking for the first element all work
# without the length ever being reachable -- and len() is the one that refuses,
# because it converts to a C ssize_t.
#
# gojja2 narrowed the bounds when the range was built and refused the lot.
case("globals/range_wide_repr", "{{ range(1180591620717411303424) }}|{{ range(2,1180591620717411303424,3) }}")
case("globals/range_wide_first", "{{ range(1180591620717411303424)|first }}|{{ range(2,1180591620717411303424,3)|first }}")
case("globals/range_wide_contains",
     "{{ 3 in range(1180591620717411303424) }}|{{ -1 in range(1180591620717411303424) }}"
     "|{{ 1180591620717411303424 in range(1180591620717411303424) }}")
case("globals/range_wide_index_and_slice",
     "{{ range(1180591620717411303424)[5] }}|{{ range(1180591620717411303424)[1:3] }}")
case("globals/range_wide_attrs",
     "{{ range(1180591620717411303424).start }}|{{ range(1180591620717411303424).stop }}"
     "|{{ range(1180591620717411303424).step }}")
case("globals/range_wide_elements", "{{ range(1180591620717411303424,1180591620717411303427)|list }}")
case("globals/range_wide_equality",
     "{{ range(1180591620717411303424) == range(1180591620717411303424) }}"
     "|{{ range(1180591620717411303424) == range(3) }}")
# len() converts to a C ssize_t, so a range longer than one refuses there --
# which is where the refusal belongs, rather than at construction.
case("globals/range_wide_length_overflows", "{{ range(1180591620717411303424)|length }}")
case("globals/range_wide_negative", "{{ range(-1180591620717411303424,0) }}|{{ range(1180591620717411303424,0,-1) }}")

# --- a search bound is a slice index, so it clamps ----------------------------
# str.find and friends take their start and end as slice indices: out of range
# clamps rather than refusing, in both directions and in both positions. An
# integer too wide for the machine is still an integer, so it clamps too.
#
# These were refused as though the bound were not an integer at all, while the
# subscript path -- `{{ "abcde"[2**70:] }}` -- clamped correctly, which is the
# real defect: one conversion with two implementations that disagreed.
case("methods/search_bounds_clamp_wide",
     '{{ "abc".find("b",1180591620717411303424) }}|{{ "abc".rfind("b",1180591620717411303424) }}'
     '|{{ "abc".count("b",1180591620717411303424) }}|{{ "abc".startswith("b",1180591620717411303424) }}')
case("methods/search_bounds_clamp_wide_negative",
     '{{ "abc".find("b",-1180591620717411303424) }}|{{ "abc".count("b",-1180591620717411303424) }}'
     '|{{ "abc".index("b",-1180591620717411303424) }}')
case("methods/search_bounds_clamp_end",
     '{{ "abc".find("b",0,1180591620717411303424) }}|{{ "abc".find("b",0,-1180591620717411303424) }}'
     '|{{ "abc".count("b",0,1180591620717411303424) }}')
case("methods/search_bounds_clamp_index_raises",
     '{{ "abc".index("b",1180591620717411303424) }}')
# A bound that is not a whole number at all is still refused, in CPython's words.
case("methods/search_bounds_reject_float", '{{ "abc".find("b",1.5) }}')

# --- tojson's indent is a repetition, and fails like one ----------------------
# json.dumps builds the indent unit as `" " * indent`, so the multiplication is
# where a hostile argument is refused. An index-sized overflow fires whatever
# the sign -- CPython asks for the index before it looks at the sign -- and a
# non-int is reported as the multiplication it is.
#
# gojja2 narrowed the argument and dropped the "does it fit" answer, so an
# indent past int64 became zero and the document came out broken up with no
# leading spaces: a different document, silently.
case("filters/tojson_indent_overflows", "{{ [1,2]|tojson(indent=1180591620717411303424) }}")
case("filters/tojson_indent_overflows_negative", "{{ [1,2]|tojson(indent=-1180591620717411303424) }}")
case("filters/tojson_indent_overflows_scalar", "{{ 1|tojson(indent=9223372036854775808) }}")
# A string is encoded before any indentation is built, so its argument is never
# looked at however hostile it is.
case("filters/tojson_indent_unused_by_a_string", '{{ "s"|tojson(indent=1180591620717411303424) }}|{{ "s"|tojson(indent=1.5) }}')

# --- integer arguments and the C type they convert to -------------------------
# An argument used as an integer is converted by CPython's argument parser to a
# specific C type, and a template can see both halves of that: the range, and
# the type named in the OverflowError past it. Most convert to Py_ssize_t;
# expandtabs' tabsize and the two `bool(accept={int})` arguments -- splitlines'
# keepends and sorted's reverse -- convert to a plain C int and so give up at
# 2**31, in both directions.
#
# These used to answer "TypeError: 'int' object cannot be interpreted as an
# integer", which contradicts itself, or -- for the C int ones below 2**63 --
# quietly accepted the value.
case("errors/index_overflow_ssize_t", '{{ "ab"|center(9223372036854775808) }}')
case("errors/index_overflow_ssize_t_split", '{{ "a b".split(" ",9223372036854775808)|list }}')
case("errors/index_overflow_c_int_expandtabs", '{{ "a\tb".expandtabs(2147483648) }}')
case("errors/index_overflow_c_int_splitlines", '{{ "a\nb".splitlines(2147483648)|list }}')
case("errors/index_overflow_c_int_sort", '{{ [3,1]|sort(reverse=2147483648) }}')
case("errors/index_overflow_c_int_negative", '{{ [3,1]|sort(reverse=-2147483649) }}')
# The edge that must still be accepted, so the bound is a bound and not a clamp.
case("errors/index_at_c_int_max", '{{ [3,1]|sort(reverse=2147483647) }}|{{ "a\nb".splitlines(2147483647)|list }}')
case("errors/index_at_ssize_t_max", '{{ "a b c".split(" ",9223372036854775807)|list }}')
# A value that is genuinely not an integer keeps the TypeError -- the other half
# of what the conversion could not tell apart.
case("errors/index_not_an_integer", '{{ "ab"|center("x") }}')

# --- the width limit on computed integers -------------------------------------
# Deliberate divergences, listed in testdata/known_failures.txt. CPython computes
# both; gojja2 refuses past 2**20 bits. They compare rather than print, because
# CPython 3.11 caps int->str conversion at 4300 digits and would raise about the
# *printing* instead of answering the question these cases ask.
#
# The multiply case is the one that matters: the bound used to be on `**` alone,
# so `x * x` reached a width `x ** 2` was refused, and multiplication doubles,
# which makes a squaring loop exponential in a template of fixed size.
#
# The exponent comes from the context rather than the template, so that jinja2's
# optimizer cannot fold it. A folded constant is written into jinja2's generated
# Python as a decimal literal, which is an int->str conversion, and CPython 3.11
# caps those at 4300 digits -- so the folded form raises about *printing* the
# number instead of answering what the case asks.
case("limits/integer_width_multiply", "{% set x = 2 ** e %}{{ (x * x) > x }}", e=524288)
case("limits/integer_width_power", "{{ (2 ** e) > 0 }}", e=2000000)

def main() -> int:
    if DST.exists():
        shutil.rmtree(DST)

    for name, template, header in CASES:
        path = DST / (name + ".jj2")
        path.parent.mkdir(parents=True, exist_ok=True)
        text = json.dumps(header, ensure_ascii=False, indent=2) if header else "{}"
        path.write_text(text + SEPARATOR + template, encoding="utf-8")

    written = sum(1 for _ in DST.rglob("*.jj2"))
    if written != len(CASES):
        raise SystemExit(
            f"gen_corpus: {len(CASES)} cases but {written} files on disk"
        )
    print(f"wrote {written} cases into {DST.relative_to(ROOT)}", file=sys.stderr)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
