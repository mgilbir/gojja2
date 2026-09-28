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


def lit(s: str) -> str:
    """A Python string literal for a corpus template, independent of this
    interpreter.

    repr() cannot be used for this. It escapes by str.isprintable, which is one
    of the very properties that moves between CPython releases, so a corpus case
    holding a newly-assigned character came out as the character itself under one
    pin and as a backslash escape under another. The *input* to a conformance
    case must not depend on which interpreter generated it: two contributors on
    different pins would otherwise commit different templates and read the diff
    as noise.

    So the rule is fixed here, as a literal set rather than as a property
    lookup. unicodedata.category cannot be used either: an unassigned code point
    is Cn until the release that assigns it, which is the same dependency wearing
    a different hat.

    Escaped: the backslash and the quote, the ASCII controls, DEL, the C1 range
    and NBSP -- everything that would otherwise sit invisibly in a text file.
    Everything else goes in as itself. That is what repr() produces on all four
    interpreters for every character the corpus uses, so this changes no existing
    case; the difference is that it will keep producing it.

    A character outside that set but still invisible -- a zero-width space, say
    -- would go in raw. That is deliberate: it stays deterministic, which is what
    this is for. Write it escaped in the source list if it matters.
    """
    short = {"\n": "\\n", "\r": "\\r", "\t": "\\t"}
    out = ["'"]
    for ch in s:
        cp = ord(ch)
        if ch in ("\\", "'"):
            out.append("\\" + ch)
        elif ch in short:
            out.append(short[ch])
        elif cp < 0x20 or cp == 0x7F or 0x80 <= cp <= 0xA0:
            out.append(f"\\x{cp:02x}")
        else:
            out.append(ch)
    out.append("'")
    return "".join(out)


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

# The two extension-gated tags, and the message when they are not enabled: the
# tag is only a tag because the extension added it, so the refusal is part of
# what "extensions" means. Graded here because the soak enables them.
case("control/do_tuple", "{% set l = [] %}{% do l.append(1), l.append(2) %}{{ l }}",
     __settings__={"extensions": ["do"]})
case("control/do_discards", "{% do 1 %}{% do 'x'|upper %}{% do [1,2]|length %}[end]",
     __settings__={"extensions": ["do"]})
case("errors/do_without_extension", "{% do 1 %}")
case("errors/do_no_expression", "{% do %}", __settings__={"extensions": ["do"]})
case("errors/do_missing_comma", "{% do 1 2 %}", __settings__={"extensions": ["do"]})
case("errors/enddo", "{% do 1 %}{% enddo %}", __settings__={"extensions": ["do"]})
case("errors/do_raises", "{% do nope.attr %}", __settings__={"extensions": ["do"]})
case("errors/break_without_extension",
     "{% for x in seq %}{% break %}{% endfor %}", **SEQ)
case("errors/continue_without_extension",
     "{% for x in seq %}{% continue %}{% endfor %}", **SEQ)
case("errors/break_argument", "{% for x in seq %}{% break 1 %}{% endfor %}",
     __settings__={"extensions": ["loopcontrols"]}, **SEQ)

# jinja2 writes Python's own `break` and `continue`, and Python clears the
# for-else indicator at the *end* of the loop body -- so a pass that left early
# never clears it and the else branch runs. Every one of these printed the other
# answer here until the indicator was moved to where jinja2 keeps it.
case("control/loop_else_after_break",
     "{% for x in seq %}{{ x }}{% break %}{% else %}E{% endfor %}",
     __settings__={"extensions": ["loopcontrols"]}, **SEQ)
case("control/loop_else_after_continue",
     "{% for x in seq %}{% continue %}{{ x }}{% else %}E{% endfor %}",
     __settings__={"extensions": ["loopcontrols"]}, **SEQ)
case("control/loop_else_pass_completed",
     "{% for x in seq %}{{ x }}{% if x == 2 %}{% break %}{% endif %}{% else %}E{% endfor %}",
     __settings__={"extensions": ["loopcontrols"]}, **SEQ)
case("control/loop_else_filtered_continue",
     "{% for x in seq if x > 1 %}{% continue %}{% else %}E{% endfor %}",
     __settings__={"extensions": ["loopcontrols"]}, **SEQ)
case("control/loop_else_recursive_break",
     "{% for x in seq recursive %}{{ x }}{% break %}{% else %}E{% endfor %}",
     __settings__={"extensions": ["loopcontrols"]}, **SEQ)
# An inner loop's else body is emitted after the inner loop and inside the
# outer one, so a continue there binds to the *outer* loop -- which is why the
# `o` never prints.
case("control/loop_else_continues_outer",
     "{% for x in seq %}{% for y in [] %}i{% else %}{% continue %}{% endfor %}o{% endfor %}[end]",
     __settings__={"extensions": ["loopcontrols"]}, **SEQ)
case("control/loop_else_inner_and_outer",
     "{% for x in seq %}{% for y in [1] %}{% continue %}{% else %}I{% endfor %}{% else %}E{% endfor %}",
     __settings__={"extensions": ["loopcontrols"]}, **SEQ)

# Which blocks a break reaches out of. A filter block, a `{% set %}` block,
# `{% with %}` and `{% autoescape %}` are emitted inline, so the loop is still
# there; a macro, a block and a `{% call %}` body are functions of their own and
# jinja2's Python will not compile a break inside one (docs/divergences.md).
case("control/break_through_filter_block",
     "{% for x in seq %}{% filter upper %}a{% break %}{% endfilter %}{% endfor %}[end]",
     __settings__={"extensions": ["loopcontrols"]}, **SEQ)
case("control/break_through_set_block",
     "{% for x in seq %}{% set v %}a{% break %}{% endset %}{{ v }}{% endfor %}[end]",
     __settings__={"extensions": ["loopcontrols"]}, **SEQ)
case("control/break_through_with_block",
     "{% for x in seq %}{% with y = x %}{{ y }}{% break %}{% endwith %}{% endfor %}[end]",
     __settings__={"extensions": ["loopcontrols"]}, **SEQ)
case("control/break_through_autoescape",
     "{% for x in seq %}{% autoescape true %}{{ x }}{% break %}{% endautoescape %}{% endfor %}[end]",
     __settings__={"extensions": ["loopcontrols"]}, **SEQ)
case("control/break_in_macro_own_loop",
     "{% macro m() %}{% for x in seq %}{{ x }}{% break %}{% endfor %}{% endmacro %}{{ m() }}",
     __settings__={"extensions": ["loopcontrols"]}, **SEQ)
case("control/break_in_block_own_loop",
     "{% block b %}{% for x in seq %}{{ x }}{% break %}{% endfor %}{% endblock %}",
     __settings__={"extensions": ["loopcontrols"]}, **SEQ)
case("control/break_inner_loop_only",
     "{% for x in seq %}{% for y in seq %}{{ y }}{% break %}{% endfor %}{% endfor %}",
     __settings__={"extensions": ["loopcontrols"]}, **SEQ)
case("control/continue_keeps_counting",
     "{% for x in seq %}{% if x % 2 %}{% continue %}{% endif %}{{ loop.index }}:{{ x }},{% endfor %}",
     __settings__={"extensions": ["loopcontrols"]}, **SEQ)

# What a jump out of the middle of something leaves half done. jinja2 buffers a
# filter block's body and applies the filter *after* it, so a break inside one
# discards the buffer rather than filtering what was written; a `{% set %}` block
# is the same shape and the assignment never happens, so the name keeps whatever
# it had. None of this is jinja2's choice -- it is what Python's `break` does to
# the statements it jumps over -- and none of it was graded.
case("control/break_discards_filter_buffer",
     "{% for x in seq %}{% filter upper %}a{% break %}b{% endfilter %}{% endfor %}[end]",
     __settings__={"extensions": ["loopcontrols"]}, **SEQ)
case("control/break_keeps_earlier_filter_output",
     "{% for x in seq %}{% filter upper %}a{% if x == 2 %}{% break %}{% endif %}b{% endfilter %}{% endfor %}[end]",
     __settings__={"extensions": ["loopcontrols"]}, **SEQ)
case("control/break_abandons_set_block",
     "{% set v = 'pre' %}{% for x in seq %}{% set v %}a{% break %}{% endset %}{% endfor %}[{{ v }}]",
     __settings__={"extensions": ["loopcontrols"]}, **SEQ)
case("control/break_abandons_set_block_unset",
     "{% for x in seq %}{% set v %}a{% break %}{% endset %}{% endfor %}[{{ v|default('-') }}]",
     __settings__={"extensions": ["loopcontrols"]}, **SEQ)
# A namespace outlives the pass, so a continue keeps what the pass wrote to it --
# where a plain `{% set %}` in the body does not survive the iteration at all.
case("control/continue_keeps_namespace_write",
     "{% set ns = namespace(v=0) %}{% for x in seq %}{% set ns.v = x %}{% continue %}{% endfor %}[{{ ns.v }}]",
     __settings__={"extensions": ["loopcontrols"]}, **SEQ)
case("control/break_in_recursive_before_descent",
     "{% for x in seq recursive %}a{% break %}{{ loop([x]) }}{% endfor %}[end]",
     __settings__={"extensions": ["loopcontrols"]}, **SEQ)
case("control/break_in_recursive_after_descent",
     "{% for x in seq recursive %}{{ loop([]) }}a{% break %}{% endfor %}[end]",
     __settings__={"extensions": ["loopcontrols"]}, **SEQ)
case("control/loop_controls_read_the_loop_var",
     "{% for x in seq %}{{ loop.index }}{% if loop.first %}{% continue %}{% endif %}{% endfor %}|"
     "{% for x in seq %}{% if loop.last %}{% break %}{% endif %}{{ loop.revindex }}{% endfor %}",
     __settings__={"extensions": ["loopcontrols"]}, **SEQ)
case("control/break_in_inner_loop_with_else",
     "{% for x in seq %}{% for y in seq %}{% if y == 2 %}{% break %}{% endif %}{{ y }}{% else %}I{% endfor %}|{% endfor %}",
     __settings__={"extensions": ["loopcontrols"]}, **SEQ)
case("control/loop_in_loop_else_may_break",
     "{% for x in e %}a{% else %}{% for y in seq %}{% break %}{% endfor %}E{% endfor %}",
     __settings__={"extensions": ["loopcontrols"]}, e=[], **SEQ)
case("control/do_and_break_together",
     "{% set l = [] %}{% for x in seq %}{% do l.append(x) %}{% if x == 2 %}{% break %}{% endif %}{% endfor %}{{ l }}",
     __settings__={"extensions": ["loopcontrols", "do"]}, **SEQ)
case("control/do_inside_filter_block",
     "{% set l = [] %}{% for x in seq %}{% filter upper %}{% do l.append(x) %}a{% endfilter %}{% endfor %}{{ l }}",
     __settings__={"extensions": ["loopcontrols", "do"]}, **SEQ)

# Where a break binds to nothing at all. jinja2's parser accepts every one of
# these and CPython refuses the Python it generates, naming a line of that
# generated module -- so gojja2 refuses them too, with CPython's wording and
# without the line. Admitted in testdata/known_failures.txt, asserted by
# TestUnboundLoopControlIsRefused, and recorded in docs/divergences.md. They are
# here because the *shape* of what each side does is what the goldens pin: a
# refusal at compile time, from both.
case("errors/break_outside_loop", "{% break %}", __settings__={"extensions": ["loopcontrols"]})
case("errors/continue_outside_loop", "{% continue %}", __settings__={"extensions": ["loopcontrols"]})
case("errors/break_in_loop_else",
     "{% for x in seq %}x{% else %}{% break %}{% endfor %}",
     __settings__={"extensions": ["loopcontrols"]}, **SEQ)
case("errors/continue_in_loop_else",
     "{% for x in seq %}x{% else %}{% continue %}{% endfor %}",
     __settings__={"extensions": ["loopcontrols"]}, **SEQ)
case("errors/break_in_recursive_loop_else",
     "{% for x in seq recursive %}x{% else %}{% break %}{% endfor %}",
     __settings__={"extensions": ["loopcontrols"]}, **SEQ)
case("errors/break_in_macro",
     "{% for x in seq %}{% macro m() %}{% break %}{% endmacro %}{{ m() }}{% endfor %}",
     __settings__={"extensions": ["loopcontrols"]}, **SEQ)
case("errors/break_in_block",
     "{% for x in seq %}{% block b %}{% break %}{% endblock %}{% endfor %}",
     __settings__={"extensions": ["loopcontrols"]}, **SEQ)
case("errors/break_in_call_block",
     "{% macro m() %}{{ caller() }}{% endmacro %}"
     "{% for x in seq %}{% call m() %}{% break %}{% endcall %}{% endfor %}",
     __settings__={"extensions": ["loopcontrols"]}, **SEQ)

# `{% print %}` needs no extension and had no case at all: it is an Output node
# like `{{ }}`, but it takes a comma-separated *list* of expressions and the
# comma rules are its own.
case("control/print", "{% print 1 + 1 %}|{% print %}|{% print 'a', 'b' %}|{% print 1, 2, 3 %}")
case("control/print_expressions", "{% print 'x'|upper, 1 if 0 else 2, ((1, 2)), 1 == 1 %}")
case("control/print_undefined", "[{% print nope %}]")
case("control/print_in_blocks",
     "{% for x in seq %}{% print x, loop.index %}{% endfor %}|"
     "{% macro m() %}{% print 'm' %}{% endmacro %}{{ m() }}|"
     "{% block b %}{% print 'b' %}{% endblock %}|"
     "{% filter upper %}{% print 'f' %}{% endfilter %}|"
     "{% set v %}{% print 's' %}{% endset %}{{ v }}", **SEQ)
case("control/print_escapes", "{% print '<i>', ('<b>'|safe), v %}",
     __settings__={"autoescape": True}, v="<u>")
case("errors/print_trailing_comma", "{% print 1, %}")
case("errors/print_missing_comma", "{% print 1 2 %}")
case("errors/print_leading_comma", "{% print , 1 %}")
case("errors/print_star", "{% print *seq %}", **SEQ)
case("errors/endprint", "{% print 1 %}{% endprint %}")
case("errors/print_raises", "{% print nope.attr %}")

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
# jinja2's lexer matches a name out of a class that is *wider* than an
# identifier -- `jinja2._identifier.pattern` is `[\w<extra>]+`, Python's `\w`
# plus 2,231 code points frozen into that module at jinja2's release -- and then
# checks isidentifier() on what it matched. Two phases, three answers, and gojja2
# had one rule built from Unicode categories:
#
#   * U+00B7 MIDDLE DOT is one of the frozen extras and `'a\u00b7'.isidentifier()`
#     is True, so jinja2 renders. gojja2 refused. Same for U+1885, which Python
#     reads as an identifier start although it is a combining mark.
#   * U+0898 is a mark assigned *after* the extras were frozen, so it is outside
#     the class: the name ends before it and nothing matches it. gojja2 read Mn
#     as a continuation and *accepted* a name jinja2 rejects -- a template that
#     worked here and not there.
#   * U+00B2 SUPERSCRIPT TWO is `\w`, so it is part of the match, and then
#     isidentifier says no: "Invalid character in identifier" rather than
#     "unexpected char", and at the name rather than at the character.
#
# The class is generated per interpreter by tools/oracle/gen_name_class.py,
# because `\w` is CPython's and moves; the extras came out identical on all four.
for _n, _src in [
    ("middle_dot", "{% set a\u00b7 = 1 %}{{ a\u00b7 }}"),
    ("mongolian_mark_alone", "{% set \u1885 = 1 %}{{ \u1885 }}"),
    ("arabic_indic_digit", "{% set a\u0660 = 1 %}{{ a\u0660 }}"),
]:
    case(f"syntax/name_accepts_{_n}", _src)
for _n, _src in [
    ("a_mark_outside_the_class", "{% set a\u0898 = 1 %}{{ a\u0898 }}"),
    ("a_mark_outside_the_class_in_a_print", "{{ a\u0899 is defined }}"),
]:
    case(f"errors/name_unexpected_char_{_n}", _src)
for _n, _src in [
    ("superscript", "{% set a\u00b2 = 1 %}{{ a\u00b2 }}"),
    ("vulgar_fraction", "{{ x\u00bc }}"),
    ("superscript_alone", "{{ \u00b2 }}"),
    ("digit_start", "{% set \u0660 = 1 %}{{ \u0660 }}"),
]:
    case(f"errors/name_invalid_character_{_n}", _src)

# The *unqualified* name of the same objects, which is what a TypeError uses
# where an UndefinedError uses the qualified one: a macro is 'Macro' in
# "unsupported operand type(s) for +" and "jinja2.runtime.Macro object" in "has
# no attribute". Both were reachable and neither was graded, so both TypeName
# methods sat unexecuted by the whole suite.
#
# `{{ {self.b: 1} }}` is deliberately absent: a BlockReference has no repr of its
# own, so a dict holding one prints an address.
for _n, _src in [
    ("macro_addition", "{% macro m() %}{% endmacro %}{{ m + 1 }}"),
    ("macro_length", "{% macro m() %}{% endmacro %}{{ m|length }}"),
    ("macro_membership", "{% macro m() %}{% endmacro %}{{ 1 in m }}"),
    ("macro_iteration", "{% macro m() %}{% endmacro %}{{ m|sum }}"),
    ("block_addition", "{% block b %}{{ self.b + 1 }}{% endblock %}"),
    ("block_length", "{% block b %}{{ self.b|length }}{% endblock %}"),
    ("block_membership", "{% block b %}{{ 1 in self.b }}{% endblock %}"),
]:
    case(f"errors/unqualified_type_name_{_n}", _src)
# A macro has a repr of its own, so it can be a dict key and be printed.
case("methods/macro_as_a_dict_key",
     "{% macro m() %}{% endmacro %}{{ {m: 1} }}")
# ...and a subscript of either answers undefined rather than raising, because
# jinja2's getitem catches the TypeError.
case("subscript/of_the_engines_objects",
     "{% macro m() %}{% endmacro %}[{{ m[0] }}]|"
     "{% block b %}[{{ self.b[0] }}]{% endblock %}")

# What each of the engine's own objects calls itself, which a template sees when
# it asks one for an attribute it has not got: under StrictUndefined the name is
# the *qualified* one, as object_type_repr writes it, so a Macro is
# "jinja2.runtime.Macro object" while a method descriptor is plain
# "method_descriptor object" and a set is "set object" -- builtins are not
# qualified. Every one of these already agreed; they were the type names no case
# had ever asked for, which is what makes them load-bearing now.
#
# `{{ self.b|pprint }}` is deliberately absent: a BlockReference has no repr of
# its own, so jinja2 prints its address.
for _n, _src in [
    ("a_method_descriptor", "{% set d = {'a': 1} %}{{ d.__class__.get.nope }}"),
    ("a_macro", "{% macro m() %}{% endmacro %}{{ m.nope }}"),
    ("a_loop", "{% for i in [1] %}{{ loop.nope }}{% endfor %}"),
    ("a_set", "{% set d = {'a': 1} %}{{ (d.keys() - 'a').nope }}"),
    ("a_template_reference", "{{ self.nope }}"),
    ("a_block_reference", "{% block b %}{{ self.b.nope }}{% endblock %}"),
]:
    case(f"undefined/strict_attribute_of_{_n}", _src,
         __settings__={"undefined": "strict"})
# ...and the same attribute under the default class, which answers rather than
# refusing -- so the type name is only in the message and not in the answer.
case("undefined/attribute_of_the_engines_objects",
     "{% macro m() %}{% endmacro %}[{{ m.nope }}]|"
     "{% for i in [1] %}[{{ loop.nope }}]{% endfor %}|[{{ self.nope }}]|"
     "{{ m.nope|default('d') }}")
# The reprs that are not an address: a method descriptor names the type it came
# from, a macro its name, a loop its position.
case("methods/repr_of_the_engines_objects",
     "{% set d = {'a': 1} %}{{ d.__class__.get|pprint }}|"
     "{% macro m() %}{% endmacro %}{{ m|pprint }}|"
     "{% for i in [1] %}{{ loop|pprint }}{% endfor %}")

# `is callable` asks whether the value is callable, not what calling it does --
# which is the whole of what a builtinFunc's Call method is for: its body is
# unreachable (the evaluator hands a global the render through callWith, and the
# folder does not fold a call to a global), but the interface it satisfies is this
# answer. See runtime.go.
case("tests/callable_globals",
     "{{ lipsum is callable }}|{{ range is callable }}|{{ dict is callable }}|"
     "{{ namespace is callable }}|{{ cycler('a','b').next is callable }}|"
     "{{ 'x'.upper is callable }}|{{ 1 is callable }}")

# |xmlattr asks its subject for `items`, and what a subject without one says is
# the subject's own AttributeError: a Namespace raises `AttributeError(name)`, so
# the message is the bare word "items" with no explanation around it, where every
# other type says "'X' object has no attribute 'items'". A cycler is the third
# shape: it *has* an items attribute -- the tuple it cycles -- so the call fails
# instead.
case("errors/xmlattr_of_a_namespace", "{{ namespace(v=1)|xmlattr }}")
case("errors/xmlattr_of_a_cycler", "{{ cycler('a','b')|xmlattr }}")
for _n, _src in [
    ("a_joiner", "{{ joiner('-')|xmlattr }}"),
    ("a_range", "{{ range(3)|xmlattr }}"),
    ("an_int", "{{ 1|xmlattr }}"),
    ("a_string", "{{ 'x'|xmlattr }}"),
    ("a_list", "{{ [1]|xmlattr }}"),
]:
    case(f"errors/xmlattr_of_{_n}", _src)

# A loaded template is cached under `(weakref(loader), name)`, so each candidate
# is hashed as part of a tuple before it is looked up -- and an unhashable one
# raises there rather than missing. gojja2 stringified it and reported
# TemplatesNotFound with its repr. It is per candidate, so the second case reports
# the list and not the miss on 'nope' that precedes it.
#
# `{% import %}`, `{% extends %}` and `{% from %}` take a name rather than a list,
# so they hash the whole value and already said so -- errors/import_list above.
# Only the candidate list read its way past the hash.
for _n, _src in [
    ("a_list", "{% include [['x']] %}"),
    ("after_a_miss", "{% include ['nope', ['x']] %}"),
    ("in_a_tuple", "{% include ((['x'],)) %}"),
    ("a_dict_candidate", "{% include [{'a': 1}] %}"),
]:
    case(f"errors/include_unhashable_candidate_{_n}", _src, __templates__=INC)
# ...and the candidates that *are* hashable and simply miss, which is what keeps
# the hash from swallowing the message.
case("errors/include_candidates_miss",
     "{% include [1] %}", __templates__=INC)
case("include/select_hashable_candidates",
     "{% include ['nope', 'inc.html'] %}|{% include [((1, 2)), 'inc.html'] %}",
     __templates__=INC)
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
# And the same `~` asymmetric between the *folded* and the run-time path, with no
# volatility involved: Concat.as_const joins `str()` of each operand, so a Markup
# that the folder can see loses its safety and the output escapes it -- while the
# run-time concat answers Markup and the output leaves it alone. Two spellings of
# one expression, two answers, and jinja2 does the same.
#
# This is the exception TestFoldedMatchesUnfolded carries: it requires the two
# paths to agree everywhere else, and requires *these* to differ, so the
# exception cannot quietly become true.
case("escape/concat_markup_folded_and_not",
     "{% autoescape true %}{{ ('<b>'|safe) ~ 'x' }}|{{ 'x' ~ ('<b>'|safe) }}|"
     "{% set m = '<b>'|safe %}{{ m ~ 'x' }}|{{ 'x' ~ m }}|"
     "{{ ('<b>'|safe) ~ ('<i>'|safe) }}{% endautoescape %}")
case("escape/concat_markup_is_escaped_either_way",
     "{% autoescape true %}{{ (('<b>'|safe) ~ 'x') is escaped }}|"
     "{% set m = '<b>'|safe %}{{ ((m ~ 'x')) is escaped }}{% endautoescape %}")

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
# PyBytes_Format words a float verb's refusal after the *verb* rather than after
# the type -- "float argument required, not str" where PyUnicode_Format says
# "must be real number, not str" -- and says the same for an integer too wide for
# a float64, where the str side reports the overflow. One conversion either works
# or does not there; it does not distinguish why. The integer verbs agree on both
# sides, which is what made the float split look like it did not exist.
#
# Found by teaching the render differential to write a *bytes* format at all:
# PyBytes_Format is a different function with its own verbs and its own wording,
# and the generator had only ever written str ones.
for _n, _src in [
    ("bytes_float_of_str", "{% set s = 'x' %}{{ ('[%f]'.encode()) % s }}"),
    ("str_float_of_str", "{% set s = 'x' %}{{ '[%f]' % s }}"),
    ("bytes_float_of_list", "{% set l = [] %}{{ ('[%f]'.encode()) % l }}"),
    ("bytes_exp_of_list", "{% set l = [] %}{{ ('[%e]'.encode()) % l }}"),
    ("bytes_float_of_none", "{% set n = none %}{{ ('[%f]'.encode()) % n }}"),
    ("bytes_float_of_wide_int", "{% set n = 10 ** 400 %}{{ ('[%f]'.encode()) % n }}"),
    ("str_float_of_wide_int", "{% set n = 10 ** 400 %}{{ '[%f]' % n }}"),
    ("bytes_int_of_str", "{% set s = 'x' %}{{ ('[%d]'.encode()) % s }}"),
    ("bytes_hex_of_str", "{% set s = 'x' %}{{ ('[%x]'.encode()) % s }}"),
]:
    case(f"format/percent_verb_{_n}", _src)
# The bytes conversions that must keep working, including the width a float64
# still holds and the integer verb a wide int still formats.
case("format/percent_bytes_verbs",
     "{% set n = 42 %}{% set w = 2 ** 70 %}{% set b = 10 ** 400 %}"
     "{{ ('[%f]'.encode()) % n }}|{{ ('[%f]'.encode()) % w }}|"
     "{{ ('[%d]'.encode()) % b }}|{{ ('[%b]'.encode()) % 'ab'.encode() }}|"
     "{{ ('[%c]'.encode()) % 65 }}|{{ ('[%(k)b]'.encode()) % {'k': 'v'.encode()} }}")

# jinja2's _load_template checks for a loader before it looks at the name at all,
# so an environment with no loader reports *itself* rather than an unhashable list
# or an undefined name. A selection is the exception: select_template refuses an
# empty list before it looks up any name, so `{% include [] %}` says so even with
# no loader. gojja2 reported the name ahead of the loader in three of those.
#
# These cases have no __settings__, so they compile in the corpus environment,
# which has a loader -- the no-loader half is in loader_test.go, since a corpus
# case cannot ask for an environment without one.
for _n, _src in [
    ("extends_a_list", "{% set e = [1] %}{% extends e %}"),
    ("extends_an_empty_list", "{% set e = [] %}{% extends e %}"),
    ("extends_a_dict", "{% set d = {'a': 1} %}{% extends d %}"),
    ("include_an_empty_list", "{% set e = [] %}{% include e %}"),
    ("extends_an_undefined", "{% extends nope %}"),
    ("include_an_undefined", "{% include nope %}"),
    ("extends_a_number", "{% set n = 1 %}{% extends n %}"),
]:
    case(f"errors/template_name_{_n}", _src)

# The last reachable cluster from the audit, all of it already in agreement. The
# four filter-arity ones go through the *generated* filter table rather than the
# hand-written refusal beneath it, which is the same shadowing the method table
# does -- so those sites stay on the list and the cases grade jinja2's wording.
for _n, _src in [
    ("replace_missing_old", "{% set s = 'a' %}{{ s|replace(new='b') }}"),
    ("replace_missing_new", "{% set s = 'a' %}{{ s|replace(old='a') }}"),
    ("attr_missing_name_splatted", "{% set d = {'a': 1} %}{{ d|attr(**{}) }}"),
    ("groupby_missing_attribute", "{% set l = [1] %}{{ l|groupby() }}"),
    # A Cycler keeps its rotation in an attribute named `items`, so a filter
    # that calls one finds a tuple and fails trying to call it.
    ("cycler_through_dictsort", "{% set c = cycler('a','b') %}{{ c|dictsort }}"),
    ("cycler_through_xmlattr", "{% set c = cycler('a','b') %}{{ c|xmlattr }}"),
    # |random over a mapping indexes it by number.
    ("random_over_a_mapping", "{% set d = {'a': 1} %}{{ d|random }}"),
    # json.dumps takes str, int, float, bool and None as keys and nothing else.
    ("tojson_tuple_key", "{% set t = (1, 2) %}{% set d = {t: 1} %}{{ d|tojson }}"),
]:
    case(f"errors/{_n}", _src)
# |round's two methods convert through an integer, so an infinity refuses there
# while the default method answers one. A NaN never reaches |filesizeformat's
# int(bytes) branch -- `bytes < base` is false for it -- so it formats as "nan".
case("filters/round_of_nonfinite",
     "{% set a = 1e308 %}{% set b = a * 10 %}{{ b|round }}|{{ (b - b)|round }}")
for _n, _m in [("ceil", "ceil"), ("floor", "floor")]:
    case(f"errors/round_{_n}_of_infinity",
         "{% set a = 1e308 %}{% set b = a * 10 %}{{ b|round(0, '" + _m + "') }}")

# `%` decides between "a mapping" and "one positional argument" by asking whether
# the right operand supports subscripting, and the exceptions are the format's
# *own* type: PyUnicode_Format names tuple and str, PyBytes_Format names tuple,
# bytes and bytearray. So a bytes counts as a mapping when a str is formatted --
# `"0" % b""` renders "0" -- and does not when a bytes is, where `b"0" % b""` is
# "not all arguments converted". Only the str half was implemented, so a leftover
# bytes argument was silently dropped.
for _n, _src in [
    ("bytes_format_bytes_arg", "{% set b = 'a'.encode() %}{{ b % b }}"),
    ("bytes_format_leftover", "{% set b = '0'.encode() %}{% set e = ''.encode() %}{{ b % e }}"),
    ("bytes_format_str_arg", "{% set b = 'a'.encode() %}{% set s = 'a' %}{{ b % s }}"),
    ("str_format_bytes_arg", "{% set s = 'a' %}{% set b = 'a'.encode() %}{{ s % b }}"),
    ("str_format_bytes_arg_used", "{% set s = '0' %}{% set e = ''.encode() %}{{ s % e }}"),
    ("bytes_format_list_arg", "{% set b = '0'.encode() %}{% set l = [] %}{{ b % l }}"),
    ("bytes_format_consumed", "{% set b = '%s'.encode() %}{% set e = ''.encode() %}{{ b % e }}"),
    ("bytes_format_named_key",
     "{% set b = '%(k)s'.encode() %}{% set d = {'k': 'v'.encode()} %}{{ b % d }}"),
    ("str_format_leftover", "{% set s = 'a' %}{% set t = 'b' %}{{ s % t }}"),
]:
    case(f"format/percent_{_n}", _src)
# The printf refusals the audit listed, which already agreed.
for _n, _src in [
    ("mapping_required", "{% set s = '%(a)s' %}{% set n = 1 %}{{ s % n }}"),
    ("missing_key", "{% set s = '%(a)s' %}{% set d = {'b': 1} %}{{ s % d }}"),
    ("incomplete", "{% set s = '%' %}{% set n = 1 %}{{ s % n }}"),
    ("incomplete_key", "{% set s = '%(a' %}{% set d = {'a': 1} %}{{ s % d }}"),
    ("incomplete_after_key", "{% set s = '%(a)' %}{% set d = {'a': 1} %}{{ s % d }}"),
    ("not_enough_arguments", "{% set s = '%s%s' %}{% set l = [1] %}{{ s % l }}"),
    ("star_wants_int", "{% set s = '%*s' %}{% set l = ['a', 'b'] %}{{ s % l }}"),
    ("star_precision_wants_int", "{% set s = '%.*f' %}{% set l = ['a', 1.5] %}{{ s % l }}"),
    ("c_requires_int_or_char", "{% set s = '%c' %}{% set l = ['ab'] %}{{ s % l }}"),
    ("d_requires_a_number", "{% set s = '%d' %}{% set l = [] %}{{ s % l }}"),
    # A *str* format, so the key is a str and so is the complaint. The bytes
    # half of this is errors/percent_key_in_a_list_under_a_bytes_format; the
    # name used to say "bytes" and grade neither.
    ("key_in_a_list", "{% set s = '%(0)s' %}{% set l = [1] %}{{ s % l }}"),
    ("bytes_missing_key",
     "{% set b = '%(a)s'.encode() %}{% set d = {'b': 1} %}{{ b % d }}"),
    ("bytes_c_out_of_range", "{% set b = '%c'.encode() %}{% set l = [300] %}{{ b % l }}"),
    ("bytes_c_two_bytes",
     "{% set b = '%c'.encode() %}{% set l = ['ab'.encode()] %}{{ b % l }}"),
]:
    case(f"format/percent_error_{_n}", _src)

# A `[` in a replacement field's *name* opens an index that runs to the next `]`
# and may hold anything: `{0[a}b]}` is the key "a}b". Scanning for `}` alone made
# that a parse error and made `{0[x}` a *lookup* of "x" instead of the
# unterminated field it is. Only while reading the name -- once that ends at a
# `:` or a `!`, a `[` is an ordinary character and `{0:[^5}` fills with one.
for _n, _src, _ctx in [
    ("brace_in_a_key", "{0[a}b]}", "{% set d = {'a}b': 1} %}"),
    ("open_brace_in_a_key", "{0[a{b]}", "{% set d = {'a{b': 1} %}"),
    ("colon_in_a_key", "{0[a:b]}", "{% set d = {'a:b': 1} %}"),
    ("unterminated_index", "{0[x}", "{% set d = {'x': 1} %}"),
    ("unterminated_empty_index", "{0[}", "{% set d = {'x': 1} %}"),
    ("unterminated_numeric_index", "{0[0}", "{% set d = {'x': 1} %}"),
    ("unterminated_index_with_spec", "{0[x}:5}", "{% set d = {'x': 1} %}"),
    ("index_without_closing_brace", "{0[0]", "{% set d = {'x': 1} %}"),
]:
    case(f"format/field_name_{_n}", _ctx + "{% set s = '" + _src + "' %}{{ s.format(d) }}")
# ...and the shapes a bracket must *not* capture, so the rule cannot spread into
# the format spec.
case("format/spec_fill_is_a_bracket",
     "{% set n = 1 %}{{ '{0:[^5}'.format(n) }}|{{ '{0:]^5}'.format(n) }}|"
     "{{ '{0!r:[^7}'.format(n) }}|{{ '{0:{1}}'.format(n, 5) }}")
case("format/nested_index_and_spec",
     "{% set l = [[1],[2]] %}{% set d = {'b': 'x'} %}{{ '{0[1][0]}'.format(l) }}|"
     "{{ '{a[b]!r:>{w}}'.format(a=d, w=6) }}")

# list.sort's call shape was not checked at all, and the reason is mechanical:
# gen_methods.py reads the method maps as *text*, and sort is registered in
# init() because naming it in the literal is an initialisation cycle. So the
# generated table has never had an entry for it. CPython counts every argument
# first, saying "arguments" when any was positional and "keyword arguments" when
# none was, then refuses a positional at all, then an unknown name -- and none of
# those messages carries a count where the generator looks for one, so it is
# checked by hand. TestEveryMethodHasASignature is what found this and what will
# find the next one.
for _n, _src in [
    ("one_positional", "{% set l = [2,1] %}{{ l.sort(1) }}"),
    ("two_positional", "{% set l = [2,1] %}{{ l.sort(1, 2) }}"),
    ("three_positional", "{% set l = [2,1] %}{{ l.sort(1, 2, 3) }}"),
    ("three_keyword", "{% set l = [2,1] %}{{ l.sort(nope=1, nope2=2, nope3=3) }}"),
    ("mixed_over_the_count", "{% set l = [2,1] %}{{ l.sort(1, key=none, reverse=true) }}"),
    ("positional_with_keyword", "{% set l = [2,1] %}{{ l.sort(1, nope=2) }}"),
    ("unknown_keyword", "{% set l = [2,1] %}{{ l.sort(nope=1) }}"),
    ("known_keywords_still_sort",
     "{% set l = [2,1] %}{{ l.sort(key=none, reverse=true) }}{{ l }}"),
]:
    case(f"errors/list_sort_{_n}", _src)
# The IndexError beside it, which the same audit listed and which is real rather
# than shadowed.
case("errors/list_pop_out_of_range", "{% set l = [1] %}{{ l.pop(9) }}")

# A fourth batch from the same audit, and every one of them already agreed: what
# was missing was a case saying so. Written through names, for the reason the
# batch above gives.
for _n, _src in [
    ("bytes_from_an_out_of_range_int",
     "{% set b = 'a'.encode() %}{% set l = [300] %}{{ b.__class__(l) }}"),
    ("bytes_from_a_string_element",
     "{% set b = 'a'.encode() %}{% set l = ['a'] %}{{ b.__class__(l) }}"),
    ("bytes_center_fill_length",
     "{% set b = 'ab'.encode() %}{{ b.center(10, b) }}"),
    ("bytes_ljust_fill_length",
     "{% set b = 'ab'.encode() %}{{ b.ljust(10, b) }}"),
    ("bytes_rjust_fill_not_bytes",
     "{% set b = 'ab'.encode() %}{% set n = 1 %}{{ b.rjust(10, n) }}"),
    ("str_center_fill_length", "{% set s = 'ab' %}{{ s.center(10, s) }}"),
    ("bytes_index_missing", "{% set b = 'ab'.encode() %}{{ b.index('z'.encode()) }}"),
    ("bytes_count_wrong_type", "{% set b = 'ab'.encode() %}{% set f = 1.5 %}{{ b.count(f) }}"),
    ("bytes_split_empty_separator",
     "{% set b = 'ab'.encode() %}{% set e = ''.encode() %}{{ b.split(e) }}"),
    ("bytes_rsplit_empty_separator",
     "{% set b = 'ab'.encode() %}{% set e = ''.encode() %}{{ b.rsplit(e) }}"),
    ("bytes_join_a_non_iterable",
     "{% set b = 'ab'.encode() %}{% set n = 1 %}{{ b.join(n) }}"),
    ("bytes_translate_short_table", "{% set b = 'ab'.encode() %}{{ b.translate(b) }}"),
    ("int_base_not_an_integer", "{% set n = 1 %}{% set s = '10' %}{{ n.__class__(s, s) }}"),
    ("replace_missing_both", "{{ 'a'|replace() }}"),
    ("replace_missing_one", "{{ 'a'|replace('a') }}"),
    ("wordwrap_zero_width", "{% set n = 0 %}{{ 'a b'|wordwrap(n) }}"),
    ("sum_of_bytes",
     "{% set l = ['a'.encode()] %}{% set e = ''.encode() %}{{ l|sum(start=e) }}"),
    ("sum_of_strings", "{% set l = ['a'] %}{% set e = '' %}{{ l|sum(start=e) }}"),
    ("filesizeformat_of_a_list", "{% set l = [] %}{{ l|filesizeformat }}"),
    ("attr_missing_name", "{% set d = {'a': 1} %}{{ d|attr() }}"),
    ("cycler_with_no_items_splatted", "{% set e = [] %}{{ cycler(*e) }}"),
]:
    case(f"errors/{_n}", _src)

# A third batch. The bug this one found: a conversion constructor counted only
# its *positional* arguments against the maximum, so `int(s, 2, base=8)` bound
# the keyword and answered 8 where CPython counts three arguments and refuses.
# With a keyword present the count comes before any name -- `int(1, base=2,
# nope=3)` reports the count, not `nope` -- while a positional-only overflow
# keeps the per-class wording the two halves of the signature moved apart on in
# 3.13. Three cases, three orders.
for _n, _src in [
    ("int_total_with_keyword", "{% set n = 1 %}{% set s = '10' %}{{ n.__class__(s, 2, base=8) }}"),
    ("int_total_two_keywords", "{% set n = 1 %}{{ n.__class__(1, base=2, nope=3) }}"),
    ("int_positional_only", "{% set n = 1 %}{% set s = '10' %}{{ n.__class__(s, 2, 3) }}"),
    ("int_unexpected_keyword", "{% set n = 1 %}{% set s = '10' %}{{ n.__class__(s, nope=8) }}"),
    ("int_keyword_for_the_subject", "{% set n = 1 %}{% set s = '10' %}{{ n.__class__(x=s) }}"),
    ("str_total_with_keywords",
     "{% set s = 'a' %}{{ s.__class__(s, 'utf-8', errors='x', encoding='y') }}"),
    ("str_positional_only", "{% set s = 'a' %}{{ s.__class__(s, 'utf-8', 'strict', 1) }}"),
    ("str_duplicate_binding", "{% set s = 'a' %}{{ s.__class__(s, object='b') }}"),
    ("str_unexpected_keyword", "{% set s = 'a' %}{{ s.__class__(s, 'utf-8', nope=1) }}"),
    ("bytes_positional_only",
     "{% set b = 'a'.encode() %}{{ b.__class__(b, 'utf-8', 'strict', 1) }}"),
    ("bytes_total_with_keywords",
     "{% set b = 'a'.encode() %}{{ b.__class__(b, encoding='x', errors='y', nope=1) }}"),
    ("int_base_still_binds", "{% set n = 1 %}{% set s = '10' %}{{ n.__class__(s, base=8) }}"),
]:
    case(f"classes/construct_arity_{_n}", _src)
# The constructor refusals the same audit listed, which already agreed.
for _n, _src in [
    ("bytes_from_a_float", "{% set b = 'a'.encode() %}{% set f = 1.5 %}{{ b.__class__(f) }}"),
    ("bytes_count_over_index",
     "{% set b = 'a'.encode() %}{% set n = 10 ** 400 %}{{ b.__class__(n) }}"),
    ("bytes_encoding_without_a_string",
     "{% set b = 'a'.encode() %}{% set n = 10 ** 400 %}{{ b.__class__(n, 'utf-8') }}"),
    ("list_from_a_float", "{% set l = [1] %}{% set f = 1.5 %}{{ l.__class__(f) }}"),
]:
    case(f"classes/construct_{_n}", _src)
# ...and the %c and {:c} refusals, which are a code point's range and the C long
# the conversion goes through before anything asks about the range.
for _n, _src in [
    ("printf_c_out_of_range", "{% set n = 1114112 %}{{ '%c' % n }}"),
    ("format_c_out_of_range", "{% set n = 1114112 %}{{ '{:c}'.format(n) }}"),
    ("format_c_negative", "{% set n = -1 %}{{ '{:c}'.format(n) }}"),
    ("format_c_over_a_c_long", "{% set n = 10 ** 400 %}{{ '{:c}'.format(n) }}"),
    ("format_c_rejects_a_sign", "{% set n = 65 %}{{ '{:+c}'.format(n) }}"),
    ("format_c_rejects_alternate", "{% set n = 65 %}{{ '{:#c}'.format(n) }}"),
]:
    case(f"format/{_n}", _src)

# The second batch from the same audit, and the same story: all of them already
# agreed. Each goes through a name rather than a literal, because an all-constant
# expression is folded by both engines and the message then comes from the fold
# rather than from the site being graded.
for _n, _src in [
    ("range_step_zero", "{% set z = 0 %}{{ range(1, 5, z) }}"),
    ("dict_two_positionals", "{% set a = 1 %}{{ dict(a, a) }}"),
    ("cycler_with_no_items", "{{ cycler() }}"),
    ("divisibleby_without_argument", "{% set n = 1 %}{{ n is divisibleby }}"),
    ("sameas_without_argument", "{% set n = 1 %}{{ n is sameas }}"),
    ("in_without_argument", "{% set n = 1 %}{{ n is in }}"),
    ("eq_without_argument", "{% set n = 1 %}{{ n is eq }}"),
]:
    case(f"errors/{_n}", _src)
for _n, _spec in [
    ("space", "{: }"), ("sign", "{:+}"), ("alternate", "{:#}"),
    ("equals_align", "{:=5}"), ("grouping", "{:,}"),
]:
    case(f"format/string_spec_rejects_{_n}",
         "{% set s = 'x' %}{{ '" + _spec + "'.format(s) }}")
case("format/object_format_rejects_a_spec",
     "{% set d = {'a': 1} %}{{ '{:5}'.format(d) }}")

# Messages `make ungraded` reported that no case produced. These all already
# agreed with CPython; what was missing was a case saying so, which is the whole
# point of that audit -- a message nothing produces reads as agreement in every
# column of the version matrix.
for _n, _src in [
    ("float_fromhex_bad_type", "{{ (1.5).fromhex([1]) }}"),
    ("float_fromhex_empty", "{{ (1.5).fromhex('') }}"),
    ("float_fromhex_prefix_only", "{{ (1.5).fromhex('0x') }}"),
    ("float_fromhex_too_large", "{{ (1.5).fromhex('0x1p+99999') }}"),
    ("to_bytes_byteorder_type", "{{ (3).to_bytes(2, 1) }}"),
    ("to_bytes_byteorder_value", "{{ (3).to_bytes(2, 'nope') }}"),
    ("to_bytes_negative_length", "{{ (3).to_bytes(-1, 'big') }}"),
    ("to_bytes_length_over_ssize", "{{ (3).to_bytes(9999999999999999999, 'big') }}"),
    ("float_mod_string", "{{ 1.5 % 'x' }}"),
    ("unary_plus_string", "{{ +'x' }}"),
    ("unary_plus_list", "{{ +[1] }}"),
    ("unary_minus_string", "{{ -'x' }}"),
]:
    case(f"errors/{_n}", _src)

# A non-finite float built at *run time* is reachable, and that took two tries to
# see. `{{ (1e400).as_integer_ratio() }}` cannot run on CPython -- 1e400 folds to
# an infinity and the code generator writes it out as `inf`, which is not a
# Python name -- so I recorded those sites as ungradable. But `1e308` writes out
# as `1e+308`, so multiplying it through a *name* overflows during the render and
# nothing is ever written as `inf`. That is how the conversions below are
# reached, and the lesson is that "no template can reach this" is a claim about
# the templates tried so far.
for _n, _src in [
    ("int_of_infinity", "{% set a = 1e308 %}{% set b = a * 10 %}{% set n = 1 %}{{ n.__class__(b) }}"),
    ("int_of_nan",
     "{% set a = 1e308 %}{% set b = a * 10 %}{% set n = 1 %}{{ n.__class__(b - b) }}"),
    ("round_of_nan", "{% set a = 1e308 %}{% set b = a * 10 %}{{ (b - b)|round(0, 'ceil')|int }}"),
    ("ratio_of_infinity", "{% set a = 1e308 %}{% set b = a * 10 %}{{ b.as_integer_ratio() }}"),
    ("ratio_of_nan", "{% set a = 1e308 %}{% set b = a * 10 %}{{ (b - b).as_integer_ratio() }}"),
    # |int catches ValueError and answers its default, so a NaN through it is 0
    # on both sides -- which is why the constructor is what reaches the message.
    ("int_filter_of_nan", "{% set a = 1e308 %}{% set b = a * 10 %}{{ (b - b)|int }}"),
]:
    case(f"numbers/nonfinite_{_n}", _src)
# |filesizeformat formats a scaled float directly, and Python writes a non-finite
# in words where Go writes "NaN" and "+Inf". A *negative* infinity lands in the
# int(bytes) branch -- it is less than the base -- where an int64 conversion
# wrapped it to -9223372036854775808 instead of refusing.
for _n, _src in [
    ("nan", "{% set a = 1e308 %}{% set b = a * 10 %}{{ (b - b)|filesizeformat }}"),
    ("nan_binary", "{% set a = 1e308 %}{% set b = a * 10 %}{{ (b - b)|filesizeformat(true) }}"),
    ("infinity", "{% set a = 1e308 %}{% set b = a * 10 %}{{ b|filesizeformat }}"),
    ("negative_infinity", "{% set a = 1e308 %}{% set b = a * 10 %}{{ (-b)|filesizeformat }}"),
]:
    case(f"filters/filesizeformat_{_n}", _src)

# jinja2 writes a folded constant into the generated Python as its repr, and a
# float infinity's repr is `inf` -- which is not a Python name. So a template
# that folds one into a position the code generator writes out cannot run there:
# it raises NameError at render. Not every position does; a plain print puts the
# value in the module's constant table instead. gojja2 answers the number.
# Listed in known_failures.txt -- it accepts what CPython cannot run.
for _n, _src in [
    ("in_a_set", "{% set v = 'inf'|float %}{{ v }}"),
    ("in_a_filter_default", "{{ x|default('inf'|float) }}"),
    ("nan_in_a_set", "{% set v = 'nan'|float %}{{ v }}"),
]:
    case(f"divergence/folded_infinity_{_n}", _src)
# The positions that do work on both, so the entry above stays about where the
# constant lands and not about infinities.
case("numbers/folded_infinity_prints",
     "{{ 'inf'|float }}|{{ 'nan'|float }}|{{ '-inf'|float }}|"
     "{{ ('inf'|float) + 1 }}|{{ ('inf'|float)|string }}|{{ 1e400 }}|"
     "{{ 'inf'|float|abs }}")

# An integer too wide for a float64 is an OverflowError in Python, not an
# infinity -- and every site that converts one has to say so. `//`, `%` and `**`
# went through the checked coercion and were right, which is what made `+`, `-`,
# `*`, `/`, |float, |filesizeformat, |sum and the two format paths look
# deliberate: they answered "inf". True division of two ints words it after the
# division rather than after the operand, because the quotient is what does not
# fit.
_BIG = "(10 ** 400)"
for _n, _src in [
    ("float_filter", "{{ %s|float }}"),
    ("float_filter_with_default", "{{ %s|float(1.0) }}"),
    ("plus_float", "{{ %s + 1.5 }}"),
    ("float_plus", "{{ 1.5 + %s }}"),
    ("minus_float", "{{ %s - 1.5 }}"),
    ("float_minus", "{{ 1.5 - %s }}"),
    ("times_float", "{{ %s * 1.5 }}"),
    ("divided", "{{ %s / 2 }}"),
    ("divided_by_float", "{{ %s / 1.5 }}"),
    ("floordiv_float", "{{ %s // 1.5 }}"),
    ("mod_float", "{{ %s %% 1.5 }}"),
    ("power_float", "{{ %s ** 0.5 }}"),
    ("filesizeformat", "{{ %s|filesizeformat }}"),
    ("negated_through_float", "{{ -%s|float }}"),
    ("through_abs", "{{ %s|abs|float }}"),
    ("summed_with_a_float", "{{ [%s, 1.5]|sum }}"),
    ("printf_f", "{{ '%%f' %% %s }}"),
    ("format_f", "{{ '{:f}'.format(%s) }}"),
    ("format_e", "{{ '{:e}'.format(%s) }}"),
]:
    case(f"numbers/wide_int_to_float_{_n}", _src % _BIG)
# The boundary, and the shapes that must keep working: 2**1023 fits and 2**1024
# does not, a comparison never converts, and float() of a *string* overflows to
# inf as Python's does.
case("numbers/wide_int_to_float_boundary",
     "{{ (2 ** 1023)|float }}|{{ (2 ** 1024)|float is defined }}")
case("numbers/wide_int_no_conversion",
     "{{ (10 ** 400) < 1.5 }}|{{ (10 ** 400) == 1.5 }}|{{ (10 ** 400)|round }}|"
     "{{ (10 ** 400)|int }}|{{ (10 ** 400)|string|length }}|{{ '1e400'|float }}")

# A float power that overflows is an OverflowError, not an infinity -- and `**`
# is the only operator that does it. CPython's float_pow reads errno from the
# platform pow(), where the rest of the arithmetic saturates: `1e308 * 10` is
# inf and `2.0 ** 1024` raises. An *infinite operand* is answered before pow()
# is reached, so `(1e308 * 10) ** 2` is inf again, and an underflow is not an
# overflow. gojja2 answered inf for all of it; found by the fuzzer, on
# `{{ 1e16 ** 3 ** 3 ** 3 ** 0 }}`.
for _n, _src in [
    ("float_power_overflows", "{{ 1e16 ** 27 }}"),
    ("float_power_overflows_by_a_little", "{{ 2.0 ** 1024 }}"),
    ("float_power_at_the_boundary", "{{ 2.0 ** 1023 }}"),
    ("float_power_overflows_negative", "{{ (-2.0) ** 1024 }}"),
    ("float_power_overflows_by_a_float_exponent", "{{ 1.7976931348623157e308 ** 1.0000001 }}"),
    ("float_power_overflows_an_int_base", "{{ 10 ** 400.0 }}"),
    ("float_power_overflow_in_a_test", "{% if 1e16 ** 27 %}x{% endif %}"),
    ("float_power_overflow_in_a_comparison", "{{ 1e16 ** 27 == 0 }}"),
    ("float_power_overflow_in_a_list", "{{ [1e16 ** 27] }}"),
    ("float_power_underflows", "{{ 0.5 ** 10000 }}|{{ 1e-300 ** 5 }}"),
    ("float_power_of_an_infinity", "{{ (1e308 * 10) ** 2 }}|{{ (1e308 * 10) ** 0 }}|"
     "{{ (1e308 * 10) ** -1 }}|{{ 2 ** (1e308 * 10) }}|{{ 0.5 ** (1e308 * 10) }}"),
    ("float_power_by_a_name", "{{ 1e16 ** n3 }}|{{ 1e16 ** n3 ** 3 }}", ),
    ("other_operators_saturate", "{{ 1e300 * 1e300 }}|{{ 1e308 + 1e308 }}|{{ -1e308 - 1e308 }}"),
]:
    case("numbers/" + _n, _src, n3=3)

# do_items checks `isinstance(value, Undefined)` and returns before it yields
# anything, with no class distinction -- the filter is documented as answering
# an empty iterable for an undefined, and a StrictUndefined is one. Raising for
# strict alone looked like the rule every other filter follows and is not this
# filter's.
for _u in ("strict", "chainable", "debug", ""):
    _n = _u or "default"
    case(f"undefined/items_of_undefined_{_n}",
         "{{ nope|items|list }}|{{ 'x'.a|items|list }}|"
         "{% for k, v in nope|items %}x{% endfor %}",
         __settings__={"undefined": _u} if _u else {})
# A set hashes what it is asked about, and a template can ask about anything --
# so `{{ {} in (d.keys() - 'a') }}` is "unhashable type: 'dict'". Set.Contains had
# no channel for that complaint and hashed with the form that cannot fail, whose
# guard is a panic; the render answered "internal error in gojja2 (please report
# this)". Found by a soak seed, on the only set arithmetic a template can write.
# The dict is written in the template rather than passed as context, so the case
# renders through both paths: a Go map cannot carry a dictionary's order, and
# TestBothRenderPathsAgree skips every case whose context holds one.
_Q = "{% set q = {'b': 2, 'a': 1, 'C': 3} %}"
for _n, _src in [
    ("a_dict", "{{ {} in (q.keys() - 'a') }}"),
    ("a_list", "{{ [] in (q.keys() - 'a') }}"),
    ("a_nonempty_dict", "{{ {'a': 1} in (q.keys() - 'a') }}"),
    ("a_view", "{{ q.keys() in (q.keys() - 'a') }}"),
]:
    case(f"errors/set_membership_of_{_n}", _Q + _src)
# ...and the hashable ones, so the check is a check and not a refusal.
case("methods/set_membership",
     _Q + "{{ 'a' in (q.keys() - 'x') }}|{{ 'a' in (q.keys() - 'a') }}|"
     "{{ ((1, 2)) in (q.items() - []) }}")
for _u in ("strict", ""):
    _n = _u or "default"
    case(f"undefined/set_membership_of_an_undefined_{_n}",
         _Q + "{{ nope in (q.keys() - 'a') }}",
         __settings__={"undefined": _u} if _u else {})

# |urlencode puts each half of a pair through str(), so a StrictUndefined in
# either position refuses rather than encoding as nothing -- jinja2 writes
# `f"{quote(k)}={quote(v)}"`. gojja2 converted without consulting the refusal, so
# `{{ [(nope, 'x')]|urlencode }}` rendered "=x". Found by a soak seed through
# `|groupby`, whose group key was an undefined and whose pair `|urlencode` then
# encoded.
for _u in ("strict", ""):
    _n = _u or "default"
    _set = {"undefined": _u} if _u else {}
    case(f"undefined/urlencode_an_undefined_key_{_n}",
         "{{ [(nope, 'x')]|urlencode }}", __settings__=_set)
    case(f"undefined/urlencode_an_undefined_value_{_n}",
         "{{ [('x', nope)]|urlencode }}", __settings__=_set)
    case(f"undefined/urlencode_an_undefined_dict_value_{_n}",
         "{{ {'a': nope}|urlencode }}", __settings__=_set)
    case(f"undefined/urlencode_a_group_key_{_n}",
         "{{ {(1,2): 'x'}|groupby('age')|list|urlencode }}", __settings__=_set)
# ...and the pairs with nothing undefined in them, so the conversion is still a
# conversion.
case("filters/urlencode_converts_each_half",
     "{{ [('a', 1), ('b', none)]|urlencode }}|{{ {'a': 1, 'b': [1,2]}|urlencode }}|"
     "{{ [(1.5, true)]|urlencode }}")

# Hashing a tuple walks its elements in turn, so the first one with something to
# say decides. `(nope, [1])` is the undefined's own error under StrictUndefined
# and `([1], nope)` is "unhashable type: 'list'" -- the same pair either way
# round under every other class, where an undefined hashes by identity. gojja2
# consulted only the *outer* value's refusal, so an undefined inside a tuple
# hashed by identity and whatever came after it won. Found by a soak seed through
# `|groupby`, whose group tuples carry the grouping key and a list.
for _u in ("strict", ""):
    _n = _u or "default"
    _set = {"undefined": _u} if _u else {}
    case(f"undefined/hash_a_tuple_undefined_first_{_n}",
         "{{ {(nope, [1]): 1} }}", __settings__=_set)
    case(f"undefined/hash_a_tuple_list_first_{_n}",
         "{{ {([1], nope): 1} }}", __settings__=_set)
    # Nothing unhashable at all, so only the refusal can speak.
    case(f"undefined/hash_a_tuple_undefined_alone_{_n}",
         "{{ [(nope, 1)]|unique|list }}", __settings__=_set)
    # ...and nested, where the walk has to descend before it decides.
    case(f"undefined/hash_a_tuple_nested_{_n}",
         "{{ {(1, (nope, [1])): 1} }}", __settings__=_set)
    # A groupby group is a tuple whose first element is the key, which is how
    # the soak reached it.
    case(f"undefined/hash_a_group_tuple_{_n}",
         "{{ {'a': 1}|groupby('city')|list|unique|list }}", __settings__=_set)
# ...and the pair with nothing wrong with it, so the walk cannot simply refuse.
case("methods/hash_a_tuple_of_hashables", "{{ {(1, (2, 'x')): 'y'}[(1, (2, 'x'))] }}")

# An *empty* container answers before the item's refusal is consulted, because
# the refusal comes from the comparison each candidate makes and there are no
# candidates: `{{ nope in [] }}` is False under StrictUndefined, and so is the
# same question of an empty tuple, an empty range and an empty values view.
#
# A dict is the exception, and a keys or items view with it: they *hash* the item
# before they look for it, so `{{ nope in {} }}` raises on an empty dict where an
# empty list does not. gojja2 refused the item up front for every container --
# which was the right answer by the wrong route, and wrong for the empty ones.
# A range answers the refusal from its length rather than by walking to find
# something to compare against; see rangeObject.ContainsErr and
# TestStrictRangeMembershipIsConstantTime for why.
for _u in ("strict", ""):
    _n = _u or "default"
    _set = {"undefined": _u} if _u else {}
    case(f"undefined/membership_of_an_empty_list_{_n}",
         "{{ nope in [] }}|{{ nope in ((())) }}", __settings__=_set)
    # The two halves are separate cases on purpose: put them together and the
    # non-empty one raises, so the golden is the error either way and nothing
    # grades the empty one.
    case(f"undefined/membership_of_an_empty_range_{_n}",
         "{{ nope in range(0) }}", __settings__=_set)
    case(f"undefined/membership_of_a_range_{_n}",
         "{{ nope in range(3) }}", __settings__=_set)
    case(f"undefined/membership_of_an_empty_values_view_{_n}",
         "{% set q = {} %}{{ nope in q.values() }}", __settings__=_set)
    # ...and the three that hash, so an empty one refuses too.
    case(f"undefined/membership_of_an_empty_dict_{_n}",
         "{% set q = {} %}{{ nope in q }}", __settings__=_set)
    case(f"undefined/membership_of_an_empty_keys_view_{_n}",
         "{% set q = {} %}{{ nope in q.keys() }}", __settings__=_set)
    case(f"undefined/membership_of_an_empty_items_view_{_n}",
         "{% set q = {} %}{{ nope in q.items() }}", __settings__=_set)
# A range compares for real in CPython whenever the item is not an exact int, so
# these are the answers the arithmetic has to match.
case("subscript/range_membership_of_a_non_integer",
     "{{ 'x' in range(3) }}|{{ 0.5 in range(3) }}|{{ 1.0 in range(3) }}|"
     "{{ [1] in range(3) }}|{{ true in range(3) }}|{{ 'x' in range(0) }}")

# A bytes `%` float verb reports the *type* of an undefined, where a str one
# lets the undefined's own error out.
#
# formatfloat in bytesobject.c replaces whatever the conversion raised with
# "float argument required, not Undefined", so the undefined's error never
# escapes there. The integer verbs do not do that, and neither does the str
# formatter, so the same argument gives three different answers depending on
# which of the two format types and which half of the verbs it meets. gojja2
# refused every undefined for `diueEfFgG` before it looked at the format's type.
# Found by a soak seed, on an undefined that `|last` had built.
for _u in ("strict", "chainable", "debug", ""):
    _n = _u or "default"
    _set = {"undefined": _u} if _u else {}
    case(f"format/percent_bytes_float_of_an_undefined_{_n}",
         "{{ ('[%f]'.encode()) % nope }}", __settings__=_set)
    case(f"format/percent_bytes_exp_of_an_undefined_{_n}",
         "{{ ('[%e]'.encode()) % nope }}|{{ ('[%G]'.encode()) % nope }}", __settings__=_set)
    # The halves that keep the undefined's error: a bytes integer verb, and a
    # str float verb.
    case(f"format/percent_bytes_int_of_an_undefined_{_n}",
         "{{ ('[%d]'.encode()) % nope }}", __settings__=_set)
    case(f"format/percent_str_float_of_an_undefined_{_n}",
         "{{ '[%f]' % nope }}", __settings__=_set)
# ...and the hint an undefined built by a filter carries, which is what makes
# the difference visible rather than two spellings of the same name.
case("format/percent_bytes_float_of_a_built_undefined",
     "{{ ('[%f]'.encode()) % -1.5|attr('name')|last }}")
case("format/percent_str_float_of_a_built_undefined",
     "{{ '[%f]' % -1.5|attr('name')|last }}")

# `view - undefined` iterates the undefined rather than asking it for a value.
#
# dictviews_sub builds a set from the view and hands the other operand to
# set.difference_update, which *iterates* it -- so the right operand's refusal is
# never consulted, and the default Undefined, which iterates empty, leaves the
# view's elements untouched. gojja2 refused both operands before it looked at
# either, so `{{ d.keys() - nope }}` raised where jinja2 answers the keys.
#
# The two cases that must keep raising are what makes it a rule about the *view*
# and not about `-`: a values view is no set operand at all, so it falls through
# to the undefined's __rsub__, and an undefined on the left is asked for __sub__
# first and raises whatever is on the right. A one-key dict is used throughout
# because a set of two or more prints in a hash order nothing can reproduce.
for _u in ("strict", "chainable", "debug", ""):
    _n = _u or "default"
    _set = {"undefined": _u} if _u else {}
    case(f"undefined/view_minus_an_undefined_keys_{_n}",
         "{% set q = {'a': 1} %}{{ q.keys() - nope }}", __settings__=_set)
    case(f"undefined/view_minus_an_undefined_items_{_n}",
         "{% set q = {'a': 1} %}{{ q.items() - nope }}", __settings__=_set)
    case(f"undefined/view_minus_an_undefined_values_{_n}",
         "{% set q = {'a': 1} %}{{ q.values() - nope }}", __settings__=_set)
    case(f"undefined/an_undefined_minus_a_view_{_n}",
         "{% set q = {'a': 1} %}{{ nope - q.keys() }}", __settings__=_set)
# ...and an empty list on the right, which is the same answer by a route that
# has no undefined in it at all.
case("methods/dict_view_difference_empty_list",
     "{% set q = {'a': 1} %}{{ q.keys() - [] }}|{{ q.items() - [] }}")

# Every one of these is a real `==` per element, so a StrictUndefined among the
# *elements* refuses just as one in the item position does. gojja2 compared with a
# form that has nowhere to put an error, so all of them answered instead: `in`
# over a list, a tuple and a values view; list.index, list.count and list.remove;
# and a dict view compared as a set. Found by a soak seed on
# `range(3) in [yes, nope]`, which is the shape where nothing short-circuits --
# `1 in [yes, nope]` matches the first element and never reaches the second,
# which is why it is here too.
for _u in ("strict", ""):
    _n = _u or "default"
    _set = {"undefined": _u} if _u else {}
    case(f"undefined/compare_elements_in_a_list_{_n}",
         "{{ range(3) in [yes, nope] }}", __settings__=_set, yes=True)
    case(f"undefined/compare_elements_short_circuit_{_n}",
         "{{ 1 in [1, nope] }}|{{ 1 in (1, nope) }}", __settings__=_set)
    case(f"undefined/compare_elements_in_a_tuple_{_n}",
         "{{ range(3) in ((yes, nope)) }}", __settings__=_set, yes=True)
    case(f"undefined/compare_elements_searching_methods_{_n}",
         "{{ [1].index(nope) }}", __settings__=_set)
    case(f"undefined/compare_elements_index_of_an_undefined_{_n}",
         "{{ [nope].index(1) }}", __settings__=_set)
    case(f"undefined/compare_elements_count_{_n}",
         "{{ [nope].count(1) }}", __settings__=_set)
    case(f"undefined/compare_elements_remove_{_n}",
         "{% set l = [nope] %}{{ l.remove(1) }}", __settings__=_set)
    case(f"undefined/compare_elements_values_view_{_n}",
         "{% set q = {'a': nope} %}{{ 1 in q.values() }}", __settings__=_set)
    case(f"undefined/compare_elements_items_view_{_n}",
         "{% set q = {'a': nope} %}{{ ('a', 1) in q.items() }}", __settings__=_set)
    # An items view carries the dict's values and so refuses; a keys view
    # carries only the keys and answers True.
    case(f"undefined/compare_elements_items_equality_{_n}",
         "{% set q = {'a': nope} %}{{ q.items() == {'a': 1}.items() }}", __settings__=_set)
    case(f"undefined/compare_elements_keys_equality_{_n}",
         "{% set q = {'a': nope} %}{{ q.keys() == {'a': 1}.keys() }}", __settings__=_set)
# ...and the answers that must not change: a values view has no __eq__, so two
# are equal only by identity, and a set comparison ignores order.
case("methods/dict_view_values_are_equal_by_identity",
     "{% set q = {'a': 1, 'b': 2} %}{{ q.values() == q.values() }}|"
     "{{ q.items() == {'b': 2, 'a': 1}.items() }}")
case("methods/dict_view_values_membership",
     "{% set q = {'a': 1} %}{{ 1 in q.values() }}|{{ 2 in q.values() }}")

# An undefined built by hand, with a fourth argument that is not an exception
# class. jinja2's Undefined stores it and *raises* it, so raising is itself a
# TypeError -- and `environment.getitem` catches AttributeError, TypeError and
# LookupError and answers a fresh undefined, which is the same reason `{{ 1[0] }}`
# is empty. So the subscript renders nothing under the default class, the hint
# under DebugUndefined, and the fresh *environment* undefined's own refusal under
# StrictUndefined -- "object has no element 0" and not the TypeError.
#
# An attribute on the same undefined is the TypeError, because
# `environment.getattr` catches AttributeError alone. gojja2 let the TypeError
# out of the subscript too. Found by a soak seed.
for _u in ("strict", "chainable", "debug", ""):
    _n = _u or "default"
    case(f"undefined/element_of_a_built_undefined_{_n}",
         "[{{ (nope.__class__(1, 2, 3, 4))[0] }}]|"
         "{{ (nope.__class__(1, 2, 3, 4))[0] is defined }}|"
         "[{{ (nope.__class__(1, 2, 3, 'x'))[0] }}]",
         __settings__={"undefined": _u} if _u else {})
case("errors/attribute_of_a_built_undefined",
     "{{ (nope.__class__(1, 2, 3, 4)).x }}")

# ...and the shapes it must still refuse, so the rule above cannot spread.
case("errors/items_of_a_non_mapping", "{{ [1]|items|list }}")
case("errors/items_of_a_string", "{{ 'ab'|items|list }}")

# `x in y` asks y for a __contains__ before it looks at x at all, so a y that
# cannot be searched is a TypeError naming *its* type whatever x is -- including
# a StrictUndefined, whose refusal would otherwise come first.
for _n, _src in [
    ("float", "{% set f = 1.5 %}{{ 1 in f }}"),
    ("none", "{% set n = none %}{{ 1 in n }}"),
    ("bool", "{% set b = true %}{{ 1 in b }}"),
]:
    case(f"membership/not_a_container_{_n}", _src)
for _n, _src in [
    ("float", "{{ nope in 1.5 }}"),
    ("none", "{{ nope in none }}"),
]:
    case(f"undefined/strict_membership_not_a_container_{_n}", _src,
         __settings__={"undefined": "strict"})

# A keys view answers by looking its item up, so it hashes it and an unhashable
# item is a TypeError rather than a miss -- `{{ [1] in d.keys() }}` answered
# False. An items or values view compares element by element and does answer
# False, which is why this is the keys view alone.
# What a view examines decides which of the item's own refusals apply, so a view
# answers membership before they are consulted. A keys view hashes the item. An
# items view *unpacks* first, so anything that is not a two-element pair simply
# is not in it -- even a StrictUndefined, which every other container refuses --
# while the key of a pair is hashed. A values view scans and so behaves like a
# list. Found by teaching the differential to put a view on the right of `in`.
# "a two-element pair" means a *tuple*: dict_items.__contains__ checks
# PyTuple_Check before the size, so a two-element list is not a pair -- it is
# simply not in the view, and its first element is never hashed. Reading it as
# any two-element sequence answered False for the same pair spelled as a list
# and raised on `[['x'], 1]`. Found by a soak seed, which drew a list of pairs
# from the fuzz context and put it on the right of `not in`.
case("methods/dictview_membership",
     "{% set d = {'a': 1} %}{{ ('a', 1) in d.items() }}|{{ ('a', 2) in d.items() }}|"
     "{{ ('b', 1) in d.items() }}|{{ [1] in d.items() }}|{{ ('a', 1, 2) in d.items() }}|"
     "{{ 'a' in d.keys() }}|{{ 1 in d.values() }}|{{ [1] in d.values() }}")
case("methods/dictview_membership_list_pair",
     "{% set d = {'a': 1} %}{{ ['a', 1] in d.items() }}|{{ [['x'], 1] in d.items() }}|"
     "{{ 'ab' in d.items() }}|{{ ('a', 1) in d.items() }}")
case("errors/dictview_unhashable_pair_key",
     "{% set d = {'a': 1} %}{{ ([1], 1) in d.items() }}")
for _n, _src in [
    ("items", "{% set d = {'a': 1} %}{{ nope in d.items() }}"),
    ("items_pair_key", "{% set d = {'a': 1} %}{{ (nope, 1) in d.items() }}"),
    ("keys", "{% set d = {'a': 1} %}{{ nope in d.keys() }}"),
    ("values", "{% set d = {'a': 1} %}{{ nope in d.values() }}"),
    ("dict", "{% set d = {'a': 1} %}{{ nope in d }}"),
]:
    case(f"undefined/strict_dictview_membership_{_n}", _src,
         __settings__={"undefined": "strict"})
for _n, _src in [
    ("dict_in_keys", "{% set d = {'a': 1} %}{{ {1: 'a'} in d.keys() }}"),
    ("list_in_keys", "{% set d = {'a': 1} %}{{ [1] in d.keys() }}"),
]:
    case(f"errors/dictview_{_n}", _src)

# str.__contains__ and bytes.__contains__ type-check their left operand before
# they look at it, so an undefined there is a TypeError naming its class rather
# than the undefined's own refusal. Every other container reaches the item
# through a comparison, which is where the refusal comes from -- so this is two
# cases and not a rule about undefineds.
for _n, _src in [
    ("in_string", "{{ nope in 'abc' }}"),
    ("not_in_string", "{{ nope not in 'abc' }}"),
    ("in_bytes", "{{ nope in 'ab'.encode() }}"),
    # in_list and in_range are not here: the sweep over all four Undefined
    # classes already holds them (undefined/*_op_in_list and
    # undefined/membership_of_a_range_*), and a second copy under a second name
    # grades nothing twice.
    ("in_tuple", "{{ nope in (1,) }}"),
    ("in_dict", "{{ nope in {'a':1} }}"),
    ("container_is_undefined", "{{ 'a' in nope }}"),
]:
    case(f"undefined/strict_membership_{_n}", _src, __settings__={"undefined": "strict"})
# The same shapes with a defined left operand, so the type check above cannot
# start answering for values that were never undefined.
# Written through a name on purpose: all-constant operands are folded by both
# engines and the message never comes from the membership check at all.
case("membership/wrong_left_operand",
     "{% set s = 'abc' %}{{ 1 in s }}|{{ none in s }}|{{ 1.5 in s.encode() }}")

# Two foldable refusals in one expression, and the engines name different ones:
# jinja2's optimizer folds bottom-up, so the `or` inside the branch raises before
# the conditional's test is ever asked, where this folds top-down and asks the
# test first. Listed in known_failures.txt. Matching it would take jinja2's
# traversal *and* its refusal to fold a slice, and a slice is folded here on
# purpose -- gojja2's run-time slice raises where jinja2's getitem swallows, so
# the fold is what makes `((2.5)[1:2])[0]` chain under a ChainableUndefined.
# Both refuse the template; only which expression is named differs.
# Which of two refusals in one expression is named, and whether a refusal in a
# branch the chain never takes is reached at all. Both were divergences until the
# fold walked the way jinja2's optimizer does -- children before the node, with a
# printed expression tried top-down first and any refusal there discarded. A
# print and a {% set %} of the *same* expression differ on purpose, which is the
# pair below.
for _n, _src in [
    ("which_refusal_is_named", "{{ (3)[1] or True if (True)|attr('name') else 1.5 }}"),
    ("untaken_branch_in_set",
     "{% set v = 1 if [1] else (3 if (0b101)[::2] else 4) %}{{ v }}"),
    ("untaken_branch_in_with",
     "{% with w = 1 if [1] else (3 if (0b101)[::2] else 4) %}{% endwith %}"),
    ("untaken_branch_in_print", "{{ 1 if [1] else 3 if (0b101)[::2] else 4 }}"),
]:
    case(f"undefined/strict_fold_{_n}", _src, __settings__={"undefined": "strict"})

# Unpacking asks the value to iterate, and a StrictUndefined's refusal names the
# undefined. Both unpack sites answered "cannot unpack non-iterable
# StrictUndefined object" instead, which describes a type the value does not
# have and hides which name was missing -- the rule materializeOr already
# followed and these two did not.
for _n, _src in [
    ("set_target", "{% set a, b = nope %}"),
    ("loop_target", "{% for a, b in [nope] %}{% endfor %}"),
    ("loop_source", "{% for a, b in nope %}{% endfor %}"),
    ("through_urlencode", "{{ [nope]|urlencode }}"),
]:
    case(f"undefined/strict_unpack_{_n}", _src, __settings__={"undefined": "strict"})
# ...and the shapes that are still a plain unpacking failure, so the rule above
# cannot quietly swallow them.
case("errors/unpack_non_iterable_int", "{% set a, b = 1 %}")
case("errors/unpack_non_iterable_in_loop", "{% for a, b in [1] %}{% endfor %}")

# markupsafe's Markup.__add__ takes a str or anything answering __html__, and
# ChainableUndefined is the one Undefined class that defines __html__ -- as its
# own str, which is "". So a Markup absorbs one and every other class refuses.
# Only a Markup on the left reaches that method.
for _n, _src, _u in [
    ("markup_plus_chainable", "{{ 'x'|safe + nope }}", "chainable"),
    ("markup_plus_default", "{{ 'x'|safe + nope }}", ""),
    ("markup_plus_debug", "{{ 'x'|safe + nope }}", "debug"),
    ("markup_plus_strict", "{{ 'x'|safe + nope }}", "strict"),
    ("chainable_plus_markup", "{{ nope + 'x'|safe }}", "chainable"),
    ("str_plus_chainable", "{{ 'x' + nope }}", "chainable"),
    ("markup_plus_chainable_subscript", "{{ 'x'|safe + (false)[0] }}", "chainable"),
]:
    case(f"markup/{_n}", _src,
         __settings__={"undefined": _u} if _u else {})

# |join asks each item for its text, and a StrictUndefined refuses. It asked
# through strictStr on the plain path and through value.Str -- which answers ""
# for every undefined -- on the autoescaping one, so the same template raised
# without autoescaping and joined the undefined away with it. Only the escaping
# differs between those branches; what a value does when asked for its text does
# not.
for _n, _src, _esc in [
    ("attribute_missing", "{{ 'a'|join(attribute='name') }}", False),
    ("attribute_missing_escaped", "{{ 'a'|join(attribute='name') }}", True),
    ("attribute_missing_sep", "{{ ['a','b']|join('-', attribute='name') }}", False),
    ("attribute_missing_sep_escaped", "{{ ['a','b']|join('-', attribute='name') }}", True),
    ("markup_item_escaped", "{{ ['a'|safe, 'b']|join('-', attribute='name') }}", True),
    ("markup_sep_escaped", "{{ ['a','b']|join('-'|safe, attribute='name') }}", True),
]:
    _settings = {"undefined": "strict"}
    if _esc:
        _settings["autoescape"] = True
    case(f"undefined/strict_join_{_n}", _src, __settings__=_settings)

# An integer attribute over a bytes indexes to the byte *value* -- `b'b,c'[1]`
# is 44 -- and the attribute-path lookup the attribute= filters share had no arm
# for bytes at all, so every one of them was undefined. The run-time subscript
# always said 44, which is why only a filter showed it. constIndex is shared with
# the fold, so both paths are graded.
_BS = "{% set l = ['a'.encode(), 'b,c'.encode()] %}"
for _n, _src in [
    ("subscript_folded", "{{ ('b,c'.encode())[1] }}"),
    ("subscript_at_run_time", "{% set b = 'b,c'.encode() %}{{ b[1] }}"),
    ("groupby", _BS + "{{ l|groupby(1, 2)|list }}"),
    ("map", _BS + "{{ l|map(attribute=1)|list }}"),
    ("map_with_default", _BS + "{{ l|map(attribute=1, default=7)|list }}"),
    ("sort", _BS + "{{ l|sort(attribute=1)|list }}"),
    ("min", "{% set l = ['b,c'.encode(), 'd,e'.encode()] %}{{ l|min(attribute=1) }}"),
    ("selectattr", _BS + "{{ l|selectattr(1)|list }}"),
    ("negative_index", _BS + "{{ l|map(attribute=-1)|list }}"),
    ("out_of_range", "{% set l = ['a'.encode()] %}{{ l|map(attribute=5)|list }}"),
]:
    case(f"filters/bytes_integer_attribute_{_n}", _src)

# `~` evaluates every operand before it converts any of them, which is what
# jinja2's `str_join((a, b))` does: building that tuple is a name lookup, and an
# Undefined only refuses when str() reaches it. So an operand that fails outright
# is reported before an *earlier* StrictUndefined's refusal. The fold interleaves
# instead -- Concat.as_const joins a generator -- and that asymmetry is upstream's.
for _n, _src in [
    ("undefined_then_failing", "{{ nope ~ (1|reject('none')|list) }}"),
    ("failing_then_undefined", "{{ (1|reject('none')|list) ~ nope }}"),
    ("undefined_then_failing_named",
     "{% set n = 1 %}{{ nope ~ (n|reject('none')|list) }}"),
    ("undefined_then_constant", "{{ nope ~ 'a' }}"),
    ("three_operands", "{{ nope ~ 'a' ~ (1|reject('none')|list) }}"),
]:
    case(f"undefined/strict_concat_{_n}", _src, __settings__={"undefined": "strict"})

# ...and none of those refusals escapes where the escaping is not yet known.
# `{% autoescape nil %}` makes the context volatile, and there the template fails
# at render on the undefined name in the tag rather than at compile time on the
# expression inside it. jinja2's Concat.as_const checks the flag itself, because
# whether its result is Markup depends on the answer; the other three are not
# reached there at all. Ordinary folding continues -- escape/volatile_folds_
# constant grades that -- so this is about the refusal and not about folding.
for _n, _src in [
    ("concat", "{% autoescape nil %}{{ (0.0).a ~ 1 }}{% endautoescape %}"),
    ("or", "{% autoescape nil %}{{ (0.0).a or 0 }}{% endautoescape %}"),
    ("and", "{% autoescape nil %}{{ (0.0).a and 1 }}{% endautoescape %}"),
    ("condexpr", "{% autoescape nil %}{{ 1 if (0.0).a else 2 }}{% endautoescape %}"),
    ("slice_through_concat",
     "{% autoescape nil %}{{ ({'a': 1})[1:2] ~ 'x' }}{% endautoescape %}"),
]:
    case(f"undefined/strict_fold_volatile_{_n}", _src,
         __settings__={"undefined": "strict"})
# A *constant* autoescape argument is not volatile, so the refusal escapes there
# exactly as it does outside a block.
case("undefined/strict_fold_constant_autoescape",
     "{% autoescape true %}{{ (0.0).a ~ 1 }}{% endautoescape %}",
     __settings__={"undefined": "strict"})

# Under StrictUndefined a folded lookup becomes a strict undefined, and asking
# one for its truthiness or its text raises *while folding* -- at compile time,
# before any of the template has run. jinja2 lets that error out of from_string
# because Concat, And, Or and CondExpr have no `except Exception: Impossible`
# around them, where BinExpr, Compare, Filter and Test do. So `~`, `and`, `or`
# and a conditional's test refuse the template, and everything else compiles and
# fails at render.
#
# These were impossible to grade until gojja2 agreed about the phase: the suite
# requires both sides to agree on whether a template compiles at all, and every
# shape here was one CPython refused and gojja2 accepted.
for _n, _src in [
    # Refused at compile time.
    ("concat", "{{ (0.0).a ~ 1 }}"),
    ("concat_right", "{{ 'x' ~ (0.0).a }}"),
    ("concat_empty", "{{ (0.0).a ~ '' }}"),
    ("concat_both", "{{ (0.0).a ~ (0.0).b }}"),
    ("concat_in_set", "{% set v = (0.0).a ~ 1 %}"),
    ("concat_missing_element", "{{ [1][5] ~ 'x' }}"),
    ("concat_missing_key", "{{ {'a':1}['b'] ~ 'x' }}"),
    ("or", "{{ (0.0).a or 0 }}"),
    ("and", "{{ (0.0).a and 1 }}"),
    ("or_through_attr_filter", "{{ (1e3)|attr('nope') and 1 }}"),
    ("or_in_if", "{% if (0.0).a or 1 %}x{% endif %}"),
    ("condexpr_test", "{{ 1 if (0.0).a else 2 }}"),
    ("condexpr_test_no_else", "{{ 1 if (0.0).a }}"),
    ("nested_or_in_concat", "{{ ((0.0).a or 1) ~ 2 }}"),
    # Compiled, and refused at render: the fold is wrapped in these.
    ("print_alone", "{{ (0.0).a }}"),
    ("add", "{{ (0.0).a + 1 }}"),
    ("compare", "{{ (0.0).a == 1 }}"),
    ("membership", "{{ (0.0).a in [1] }}"),
    ("through_string_filter", "{{ (0.0).a|string }}"),
    ("condexpr_branch", "{{ (0.0).a if 1 else 2 }}"),
    ("statement_test", "{% if (0.0).a %}x{% endif %}"),
    ("unary", "{{ -((0.0).a) }}"),
    ("subscripted", "{{ (0.0).a[0] }}"),
    ("attribute_of", "{{ (0.0).a.b }}"),
    ("not", "{{ not (0.0).a }}"),
    ("loop_over", "{% for i in (0.0).a %}{% endfor %}"),
    ("as_a_key", "{{ [1,2][(0.0).a] }}"),
    # The right operand of a short-circuit is carried as a value, never asked
    # for its truthiness, so these reach the render like any other undefined.
    ("and_right", "{{ true and (0.0).a }}"),
    ("or_right", "{{ false or (0.0).a }}"),
    # Neither compiles nor renders as an error: nothing asks.
    ("bound_only", "{% set v = (0.0).a %}"),
    ("in_a_list", "{{ [(0.0).a] }}"),
    ("in_a_tuple", "{{ ((0.0).a,) }}"),
    ("is_defined", "{{ (0.0).a is defined }}"),
    ("through_default", "{{ (0.0).a|default('d') }}"),
]:
    case(f"undefined/strict_fold_{_n}", _src, __settings__={"undefined": "strict"})

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
    ("folded_arith", "{{ none.missing + 1 }}"),
    ("folded_truth", "{% if none.missing %}t{% else %}f{% endif %}"),
]
for _kind in ("strict", "chainable", "debug", "default"):
    for _n, _src in _FOLDED:
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
    ("chained", "{{ nope.a }}", {}),
    ("printed_twice", "{{ nope }}{{ d.missing }}", {"d": {"a": 1}}),
]
for _n, _src, _ctx in _DEBUG:
    case(f"undefined/debug_{_n}", _src, __settings__={"undefined": "debug"}, **_ctx)
    # The same shapes under the default class, so making one of them render
    # cannot quietly change what the other does.
    case(f"undefined/plain_{_n}", _src, **_ctx)


# ChainableUndefined differs from Undefined in exactly two observable ways, and
# gojja2 had neither. Its __getattr__ hands back the same undefined instead of
# raising -- which the dotted form did and the |attr filter did not, so
# `nope.a` chained and `nope|attr("a")` raised on the same lookup. And it is
# the one Undefined class that defines __html__, which `is escaped` asks for by
# name. A dunder is not chained under any class: Undefined.__getattr__ reports
# it as an ordinary missing attribute, so |attr answers with an undefined.
_CHAIN = [
    ("attr_filter", "{{ nope|attr('a') }}"),
    ("attr_filter_on_key", "{{ d.missing|attr('a') }}", {"d": {"a": 1}}),
    ("attr_filter_on_index", "{{ seq[42]|attr('a') }}", {"seq": [1, 2, 3]}),
    ("attr_filter_twice", "{{ nope|attr('a')|attr('b') }}"),
    ("attr_filter_dunder", "{{ nope|attr('__nosuch__') }}"),
    ("attr_filter_then_use", "{{ nope|attr('a') + 1 }}"),
    ("dotted_matches_filter", "{{ nope.a }}|{{ nope|attr('a') }}"),
    ("escaped", "{{ nope is escaped }}"),
    ("escaped_attr", "{{ d.missing is escaped }}", {"d": {"a": 1}}),
    ("deep_chain", "{{ nope.a.b.c }}"),
]
for _entry in _CHAIN:
    _n, _src = _entry[0], _entry[1]
    _ctx = _entry[2] if len(_entry) > 2 else {}
    for _kind in ("chainable", "default", "debug", "strict"):
        case(f"undefined/{_kind}_{_n}", _src,
             __settings__={"undefined": _kind}, **_ctx)


# StrictUndefined adds exactly five things to Undefined -- __str__, __iter__,
# __len__, __bool__/__eq__/__ne__/__hash__ and __contains__ -- and everything
# else an undefined refuses, it refuses under every class. gojja2 had the
# "everything else" and almost none of the five, so a strict environment
# rendered `{{ nope|upper }}` as "" and answered `{{ nope == nope }}` with
# True. That is the setting silently not applying, which is the whole reason
# to ask for it.
#
# Each shape is graded under all four classes, because what is being pinned is
# the difference between them: making strict refuse must not make the default
# class refuse too.
_STRICT = [
    # __str__, reached through every filter that renders its subject.
    ("str_filter", "{{ nope|string }}"),
    ("upper", "{{ nope|upper }}"),
    ("lower_filter", "{{ nope|lower }}"),
    ("title", "{{ nope|title }}"),
    ("capitalize", "{{ nope|capitalize }}"),
    ("trim", "{{ nope|trim }}"),
    ("center", "{{ nope|center(5) }}"),
    ("indent", "{{ nope|indent(2) }}"),
    ("truncate", "{{ nope|truncate(5) }}"),
    ("wordwrap", "{{ nope|wordwrap(5) }}"),
    ("wordcount", "{{ nope|wordcount }}"),
    ("replace", "{{ nope|replace('a','b') }}"),
    ("striptags", "{{ nope|striptags }}"),
    ("urlize", "{{ nope|urlize }}"),
    ("escape", "{{ nope|escape }}"),
    ("forceescape", "{{ nope|forceescape }}"),
    ("safe", "{{ nope|safe }}"),
    ("format_arg", "{{ '%s'|format(nope) }}"),
    ("percent", "{{ '%s' % nope }}"),
    ("percent_repr", "{{ '%r' % nope }}"),
    ("join_element", "{{ [nope]|join(',') }}"),
    # __len__
    ("length", "{{ nope|length }}"),
    ("length_folded", "{{ none.missing|length }}"),
    # __iter__
    ("iterate", "{% for x in nope %}{{ x }}{% endfor %}"),
    ("list", "{{ nope|list }}"),
    ("reverse", "{{ nope|reverse|list }}"),
    ("sort", "{{ nope|sort|list }}"),
    # __eq__, __ne__, __contains__, __hash__
    ("eq_self", "{{ nope == nope }}"),
    ("eq_none", "{{ nope == none }}"),
    ("eq_str", "{{ nope == 'x' }}"),
    ("ne", "{{ nope != 1 }}"),
    ("in_list", "{{ nope in [1] }}"),
    ("contains", "{{ 1 in nope }}"),
    ("is_eq", "{{ nope is eq 1 }}"),
    ("is_in", "{{ nope is in([1]) }}"),
    ("dict_key", "{{ {nope: 1} }}"),
    ("is_filter", "{{ nope is filter }}"),
    ("is_test", "{{ nope is test }}"),
    # __bool__
    ("truth", "{% if nope %}t{% else %}f{% endif %}"),
    ("not", "{{ not nope }}"),
    ("or", "{{ nope or 'x' }}"),
    # Tests that inspect rather than use, and the two that must not raise.
    ("is_sequence", "{{ nope is sequence }}"),
    ("is_iterable", "{{ nope is iterable }}"),
    ("is_lower", "{{ nope is lower }}"),
    ("is_upper", "{{ nope is upper }}"),
    ("is_defined", "{{ nope is defined }}"),
    ("is_undefined", "{{ nope is undefined }}"),
    ("default", "{{ nope|default('d') }}"),
    ("default_boolean", "{{ nope|default('d', true) }}"),
    ("pprint", "{{ nope|pprint }}"),
]
for _n, _src in _STRICT:
    for _kind in ("strict", "default", "chainable", "debug"):
        case(f"undefined/{_kind}_op_{_n}", _src, __settings__={"undefined": _kind})


# A subscript whose key the container cannot take at all -- `{{ 1[none] }}` --
# is swallowed by Environment.getitem into an undefined naming the owner and
# the key. gojja2 built a hint saying the container was not subscriptable,
# which is a different thing, threw the key away, and was not even well formed:
# ObjectTypeRepr already ends in " object", so the message read "int object
# object is not subscriptable". Printing the undefined hides all of that, so
# each case uses it.
_BADKEY = [
    ("none_on_int", "{{ x[none] + 1 }}", {"x": 1}),
    ("none_on_float", "{{ x[none] + 1 }}", {"x": 1.5}),
    ("none_on_bool", "{{ x[none] + 1 }}", {"x": True}),
    ("float_on_int", "{{ x[1.5] + 1 }}", {"x": 1}),
    ("list_on_int", "{{ x[[1]] + 1 }}", {"x": 1}),
    ("dict_on_int", "{{ x[{'a': 1}] + 1 }}", {"x": 1}),
    ("none_on_int_printed", "{{ x[none] }}", {"x": 1}),
    ("none_on_int_length", "{{ x[none]|length }}", {"x": 1}),
    ("folded_none_on_int", "{{ -1[none] }}", {}),
    ("folded_float_on_int", "{{ (1[1.5]) + 1 }}", {}),
]
for _n, _src, _ctx in _BADKEY:
    case(f"subscript/badkey_{_n}", _src, **_ctx)
    # DebugUndefined names the subscript, so it shows the shape of the
    # undefined directly rather than through a later use of it.
    case(f"subscript/badkey_{_n}_debug", _src,
         __settings__={"undefined": "debug"}, **_ctx)


# Slicing something that cannot be sliced folds, as jinja2's optimizer does,
# into an undefined naming the owner and the slice. gojja2 built a hint saying
# the base was not subscriptable, which under DebugUndefined rendered as
# "undefined value printed: ..." rather than naming the subscript. The run-time
# form raises TypeError on both engines and is unaffected; this is only about
# the constant that folding leaves behind.
# The run-time form carries the value in the *context*, written out per case
# rather than substituted into the template. It was substituted, and bound x=1
# for every one of them: five cases named after five types graded an int five
# times, and nothing said so because they all agreed.
_SLICEFOLD = [
    ("none", "{{ none[1:2] }}", "{{ x[1:2] }}", None),
    ("none_bad_stop", "{{ none[1:'x'] }}", "{{ x[1:'x'] }}", None),
    ("none_zero_step", "{{ none[::0] }}", "{{ x[::0] }}", None),
    ("bool", "{{ true[1:2] }}", "{{ x[1:2] }}", True),
    ("int", "{{ 1[1:2] }}", "{{ x[1:2] }}", 1),
    ("float", "{{ 1.5[1:2] }}", "{{ x[1:2] }}", 1.5),
    ("dict", "{{ {'a': 1}[1:2] }}", "{{ x[1:2] }}", {"a": 1}),
    ("dict_full", "{{ {'a': 1}[::-1] }}", "{{ x[::-1] }}", {"a": 1}),
]
for _n, _src, _runtime, _x in _SLICEFOLD:
    case(f"subscript/slicefold_{_n}", _src)
    case(f"subscript/slicefold_{_n}_debug", _src, __settings__={"undefined": "debug"})
    # The run-time form, which raises instead of folding.
    case(f"subscript/sliceruntime_{_n}", _runtime, x=_x)


# jinja2 writes a filter block's result into its output buffer as it stands and
# joins the buffer at the end, so a filter that answers with something other
# than a string fails there rather than being rendered: `{% filter length %}`
# is a TypeError, not "3". gojja2 rendered str() of whatever came back, which
# turned an author's mistake into plausible output.
#
# Only the writing form is affected. `{% set s | length %}` assigns the value
# and keeps it an int, which the paired cases below pin so the check cannot
# spread to it.
_FILTERBLOCK = [
    ("length", "{% filter length %}abc{% endfilter %}"),
    ("list", "{% filter list %}ab{% endfilter %}"),
    ("int", "{% filter int %}42{% endfilter %}"),
    ("float", "{% filter float %}4.5{% endfilter %}"),
    ("count", "{% filter count %}abc{% endfilter %}"),
    ("round", "{% filter round %}4.5{% endfilter %}"),
    ("abs", "{% filter abs %}-3{% endfilter %}"),
    ("chain_to_int", "{% filter string|length %}abc{% endfilter %}"),
    ("chain_to_str", "{% filter length|string %}abc{% endfilter %}"),
    ("nested", "{% filter upper %}{% filter length %}abc{% endfilter %}{% endfilter %}"),
    ("in_set_capture", "{% set s %}{% filter length %}abc{% endfilter %}{% endset %}{{ s }}"),
]
for _n, _src in _FILTERBLOCK:
    case(f"errors/filterblock_{_n}", _src)

# The shapes that stay strings, and the assigning form, which keeps the value.
_FILTERBLOCK_OK = [
    ("upper", "{% filter upper %}abc{% endfilter %}"),
    ("safe", "{% filter safe %}abc{% endfilter %}"),
    ("string", "{% filter string %}abc{% endfilter %}"),
    ("tojson", "{% filter tojson %}abc{% endfilter %}"),
    ("first", "{% filter first %}abc{% endfilter %}"),
    ("default", "{% filter default(5) %}{% endfilter %}"),
    ("set_length", "{% set s | length %}abc{% endset %}{{ s }}"),
    ("set_length_arith", "{% set s | length %}abc{% endset %}[{{ s + 1 }}]"),
    ("set_list", "{% set s | list %}ab{% endset %}{{ s }}"),
    ("set_upper", "{% set s | upper %}abc{% endset %}{{ s }}"),
    ("call_block", "{% macro w() %}{{ caller()|length }}{% endmacro %}{% call w() %}abc{% endcall %}"),
]
for _n, _src in _FILTERBLOCK_OK:
    case(f"control/filterblock_{_n}", _src)


# Five of the six globals are classes in jinja2 -- range and dict are builtin
# types, cycler, joiner and namespace are classes in jinja2.utils -- and only
# lipsum is a function. Calling one constructs a value either way, so the
# difference is invisible until something names the type. gojja2 modelled them
# all as functions, and then every message naming one said 'function' where
# CPython says 'type', across arithmetic, iteration, length, comparison and
# JSON.
_CLASSES = ["range", "cycler", "joiner", "namespace"]
_CLASS_OPS = [
    ("repr", "{{ X }}"),
    ("class", "{{ X.__class__ }}"),
    ("class_name", "{{ X.__class__.__name__ }}"),
    ("name", "{{ X.__name__ }}"),
    ("module", "{{ X.__module__ }}"),
    ("add", "{{ X + 1 }}"),
    ("sub", "{{ X - 1 }}"),
    ("mul", "{{ X * 2 }}"),
    ("neg", "{{ -X }}"),
    ("abs", "{{ X|abs }}"),
    ("length", "{{ X|length }}"),
    ("list", "{{ X|list }}"),
    ("join", "{{ X|join(',') }}"),
    ("sort", "{{ X|sort }}"),
    ("tojson", "{{ X|tojson }}"),
    ("lt", "{{ X < 1 }}"),
    ("iterate", "{% for z in X %}{{ z }}{% endfor %}"),
    ("contains", "{{ 1 in X }}"),
    ("as_key", "{{ {X: 1} }}"),
    ("upper", "{{ X|upper }}"),
    ("missing_attr", "{{ X.nosuch }}"),
    ("callable", "{{ X is callable }}"),
    ("mapping", "{{ X is mapping }}"),
    ("sequence", "{{ X is sequence }}"),
]
for _obj in _CLASSES:
    for _n, _op in _CLASS_OPS:
        case(f"classes/global_{_obj}_{_n}", _op.replace("X", _obj))

# lipsum is the one that really is a function, so it is the control: making the
# others report as types must not make this one.
for _n, _op in _CLASS_OPS:
    if _n in ("name", "module", "upper", "repr", "as_key"):
        continue  # a function's repr carries an address, wherever it appears
    case(f"classes/global_lipsum_{_n}", _op.replace("X", "lipsum"))

# And the values those classes construct keep their own identities.
for _n, _src in [
    ("range_instance", "{{ range(3)|list }}"),
    ("range_instance_class", "{{ range(3).__class__ }}"),
    ("namespace_instance_class", "{{ namespace(a=1).__class__ }}"),
    ("cycler_instance_class", "{{ cycler(1,2).__class__ }}"),
    ("joiner_instance_class", "{{ joiner().__class__ }}"),
    ("dict_instance", "{{ dict(a=1) }}"),
]:
    case(f"classes/{_n}", _src)

# The method resolution order is not implemented, and belongs with
# __subclasses__ rather than with __class__: it is the first step of the walk
# from a value to the interpreter's builtins. Listed in known_failures.txt.
case("divergence/class_mro", "{{ (1).__class__.__mro__ }}")

# Subscripting a type object: CPython words the refusal differently from the
# refusal the same expression gets one step earlier, and a template reaches a
# type object through `__class__`. A slice is the reachable spelling, because
# jinja2 retries an integer or string key as an attribute and gets undefined.
#
# Every subject is a context variable rather than a literal, because both
# engines fold a constant expression and *swallow* the error while folding, so
# `{{ (1.5).__class__[1:] }}` renders empty on both sides and grades nothing.
# The first six of these were written with literals and had to be rewritten.
for _n, _src, _ctx in [
    ("float", "{{ f.__class__[1:] }}", {"f": 1.5}),
    ("int", "{{ n.__class__[1:] }}", {"n": 1}),
    ("str", "{{ s.__class__[1:] }}", {"s": "x"}),
    ("bool", "{{ yes.__class__[1:] }}", {"yes": True}),
    ("none", "{{ nil.__class__[1:] }}", {"nil": None}),
    ("plain_object_contrast", "{{ f[1:] }}", {"f": 1.5}),
    ("key_falls_back", "{{ n.__class__[0] }}|{{ n.__class__['a'] }}", {"n": 1}),
    ("length", "{{ n.__class__|length }}", {"n": 1}),
]:
    case(f"classes/subscript_{_n}", _src, **_ctx)

# The two globals whose type object is built rather than converted; a call is
# not constant, so these are not folded away.
case("classes/subscript_namespace", "{{ namespace().__class__[1:] }}")
case("classes/subscript_range_global", "{{ range(3).__class__[1:] }}")

# The AttributeError for a type object is worded specially too -- `type object
# 'int' has no attribute 'items'` -- and eight filters reach it by asking a
# value for a method it does not have. The render differential found it as
# `true.__class__|dictsort`; these pin the four filters that surface it.
for _n, _src, _ctx in [
    ("dictsort", "{{ n.__class__|dictsort }}", {"n": 1}),
    ("xmlattr", "{{ n.__class__|xmlattr }}", {"n": 1}),
    ("wordwrap", "{{ n.__class__|wordwrap }}", {"n": 1}),
    ("wrapstring", "{{ s|wordwrap(3, wrapstring=n.__class__) }}",
     {"s": "a b c d", "n": 1}),
    ("urlize_rel", "{{ s|urlize(rel=n.__class__) }}", {"s": "x", "n": 1}),
    ("plain_object_contrast", "{{ f|dictsort }}", {"f": 1.5}),
]:
    case(f"classes/attrerror_{_n}", _src, **_ctx)

# Two type objects compare and hash by the class they name, which is what a
# template can actually observe about one.
for _n, _src, _ctx in [
    ("eq_same", "{{ n.__class__ == n.__class__ }}", {"n": 1}),
    ("eq_different", "{{ n.__class__ == s.__class__ }}", {"n": 1, "s": "x"}),
    ("eq_bool_is_not_int", "{{ yes.__class__ == n.__class__ }}",
     {"yes": True, "n": 1}),
    ("eq_not_a_class", "{{ n.__class__ == 1 }}", {"n": 1}),
    ("ne", "{{ n.__class__ != f.__class__ }}", {"n": 1, "f": 1.5}),
    ("in_list", "{{ n.__class__ in [s.__class__, n.__class__] }}",
     {"n": 1, "s": "x"}),
    ("dict_key", "{{ {n.__class__: 1} }}", {"n": 1}),
    ("unique", "{{ [n.__class__, s.__class__, n.__class__]|unique|list }}",
     {"n": 1, "s": "x"}),
    ("ordering_refused", "{{ n.__class__ < s.__class__ }}", {"n": 1, "s": "x"}),
    ("callable", "{{ n.__class__ is callable }}", {"n": 1}),
]:
    case(f"classes/compare_{_n}", _src, **_ctx)

# Calling a type object constructs the value, as calling a class does in Python.
# Every subject is a context variable, because a constant call is folded and a
# fold that raises renders empty on both sides rather than reporting anything.
_CALL = {"n": 1, "s": "ab", "f": 1.5, "yes": True, "nil": None,
         "lst": [1, 2], "d": {"a": 1}, "b64": "YWI="}
for _n, _src in [
    # The zero-argument form of each class, which is its empty value.
    ("empty_int", "{{ n.__class__() }}"),
    ("empty_str", "[{{ s.__class__() }}]"),
    ("empty_float", "{{ f.__class__() }}"),
    ("empty_bool", "{{ yes.__class__() }}"),
    ("empty_none", "{{ nil.__class__() }}"),
    ("empty_list", "{{ lst.__class__() }}"),
    ("empty_dict", "{{ d.__class__() }}"),
    # Conversion, which is the same int() and float() a %d or %f runs.
    ("int_of_str", "{{ n.__class__('42') }}"),
    ("int_of_float", "{{ n.__class__(f) }}|{{ n.__class__(-1.5) }}"),
    ("int_of_bool", "{{ n.__class__(yes) }}"),
    ("int_bad_str", "{{ n.__class__(s) }}"),
    ("int_of_none", "{{ n.__class__(nil) }}"),
    ("int_base", "{{ n.__class__('10', 2) }}|{{ n.__class__('z', 36) }}"),
    ("int_base_prefix", "{{ n.__class__('0x10', 16) }}|{{ n.__class__('0b11', 0) }}"),
    ("int_base_keyword", "{{ n.__class__('10', base=2) }}"),
    ("int_base_range", "{{ n.__class__('10', 1) }}"),
    ("int_base_without_string", "{{ n.__class__(5, 2) }}"),
    ("int_base_no_subject", "{{ n.__class__(base=2) }}"),
    ("int_unexpected_keyword", "{{ n.__class__(x=5) }}"),
    ("int_arity", "{{ n.__class__(1, 2, 3, 4) }}"),
    ("float_of_str", "{{ f.__class__('1.5') }}|{{ f.__class__('nan') }}"),
    ("float_bad_str", "{{ f.__class__(s) }}"),
    ("float_of_none", "{{ f.__class__(nil) }}"),
    ("float_arity", "{{ f.__class__(1, 2) }}"),
    ("float_keyword", "{{ f.__class__(x=1) }}"),
    # str() is repr for anything that is not one already.
    ("str_of_list", "{{ s.__class__(lst) }}"),
    ("str_of_dict", "{{ s.__class__(d) }}"),
    ("str_of_none", "{{ s.__class__(nil) }}"),
    ("str_of_float", "{{ s.__class__(f) }}"),
    ("str_keyword", "{{ s.__class__(object=5) }}"),
    ("str_encoding_type", "{{ s.__class__(5, 2) }}"),
    ("str_decoding_str", "{{ s.__class__(s, 'utf-8') }}"),
    ("str_decoding_int", "{{ s.__class__(5, 'utf-8') }}"),
    ("str_arity", "{{ s.__class__(1, 2, 3, 4) }}"),
    # bool() is truthiness, which is the one every short-circuit runs.
    ("bool_of_values", "{{ yes.__class__(0) }}{{ yes.__class__(lst) }}"
                       "{{ yes.__class__([]) }}{{ yes.__class__(nil) }}"),
    ("bool_arity", "{{ yes.__class__(1, 2) }}"),
    ("none_takes_nothing", "{{ nil.__class__(1) }}"),
    # list() and tuple() walk an iterable, and refuse what is not one.
    ("list_of_str", "{{ lst.__class__(s) }}"),
    ("list_of_dict", "{{ lst.__class__(d) }}"),
    ("list_of_range", "{{ lst.__class__(range(3)) }}"),
    ("list_not_iterable", "{{ lst.__class__(n) }}"),
    ("list_arity", "{{ lst.__class__(lst, 2) }}"),
    ("list_keyword", "{{ lst.__class__(x=1) }}"),
    # dict() is the dict global, which is the same class.
    ("dict_of_dict", "{{ d.__class__(d) }}"),
    ("dict_of_kwargs", "{{ d.__class__(a=1, b=2) }}"),
    ("dict_bad_pairs", "{{ d.__class__(s) }}"),
    # A class object is the class global, so it compares equal to one.
    ("is_the_global_dict", "{{ d.__class__ == dict }}"),
    ("is_the_global_range", "{{ range(3).__class__ == range }}"),
    ("is_the_global_namespace", "{{ namespace().__class__ == namespace }}"),
    ("namespace_of_kwargs", "{{ namespace().__class__(a=1) }}"),
    ("range_of_int", "{{ range(3).__class__(4) }}|{{ range(3).__class__(1, 5, 2) }}"),
    ("range_no_args", "{{ range(3).__class__() }}"),
    # An undefined's class builds another undefined, and still binds.
    ("undefined_builds_undefined", "[{{ nope.__class__() }}]"),
    ("undefined_keyword", "{{ nope.__class__(zz=1) }}"),
    # A dict view cannot be instantiated at all, and says so.
    ("dict_view_refuses", "{{ d.keys().__class__() }}"),
    ("dict_items_refuses", "{{ d.items().__class__(1) }}"),
]:
    case(f"classes/construct_{_n}", _src, **_CALL)

# Markup's class, reached through `|safe`. Markup does not escape what it is
# handed -- that is what it is for -- and its own binding wraps str's, so the
# arity and keyword refusals are Markup's while anything past them is str's.
for _n, _src in [
    ("markup_keeps_markup", "{{ m.__class__('<b>') }}"),
    ("markup_of_escaped", "{{ m.__class__(s) }}"),
    ("markup_empty", "[{{ m.__class__() }}]"),
    ("markup_arity", "{{ m.__class__(1, 2, 3, 4) }}"),
    ("markup_keyword", "{{ m.__class__(zz=1) }}"),
    ("markup_object_keyword", "{{ m.__class__(object=5) }}"),
    ("markup_encoding_type", "{{ m.__class__(5, 2) }}"),
]:
    case(f"classes/construct_{_n}",
         "{% set m = '<i>'|safe %}" + _src, s="<b>&amp;")

# The same two, autoescaping, where losing the safe mark is visible as escaped
# output rather than only as a different type.
case("classes/construct_markup_autoescape",
     "{% autoescape true %}{% set m = '<i>'|safe %}{{ m.__class__('<b>') }}"
     "|{{ m.__class__(s) }}{% endautoescape %}", s="<b>")

# str() of an undefined follows the undefined's own rules: a StrictUndefined
# refuses to become a string, where a plain one is "". Written under every
# setting, because the constructor must not decide this for itself.
for _kind in ("strict", "chainable", "debug", "default"):
    case(f"classes/construct_str_of_{_kind}_undefined",
         "[{{ s.__class__(nope) }}]", s="x",
         __settings__={"undefined": _kind})

# bytes() needs a bytes in the context, which JSON cannot carry, so these decode
# one first. The count and the iterable forms are budget-charged; see
# construct_budget_test.go.
for _n, _src in [
    ("bytes_empty", "{{ b.__class__() }}"),
    ("bytes_count", "{{ b.__class__(4) }}"),
    ("bytes_negative", "{{ b.__class__(-1) }}"),
    ("bytes_of_ints", "{{ b.__class__([104, 105]) }}"),
    ("bytes_out_of_range", "{{ b.__class__([300]) }}"),
    ("bytes_of_str", "{{ b.__class__('a') }}"),
    ("bytes_encoded", "{{ b.__class__('ab', 'utf-8') }}"),
    ("bytes_encoding_type", "{{ b.__class__(5, 2) }}"),
    ("bytes_encoding_without_str", "{{ b.__class__(5, 'utf-8') }}"),
    # bytes kept the pre-3.13 arity wording when int and str changed theirs,
    # while its *keyword* wording changed with them. The pair is here so the
    # version matrix grades the asymmetry rather than one half of it.
    ("bytes_arity", "{{ b.__class__(1, 2, 3, 4) }}"),
    ("bytes_unexpected_keyword", "{{ b.__class__(zz=5) }}"),
    ("bytes_of_none", "{{ b.__class__(nil) }}"),
    ("str_of_bytes", "{{ s.__class__(b, 'utf-8') }}"),
]:
    case(f"classes/construct_{_n}",
         "{% set b = 'ab'.encode() %}" + _src, nil=None)

# The generic-alias divergence, reached through `__class__` rather than through
# the `dict` global: CPython answers a types.GenericAlias, gojja2 has none.
# Listed in known_failures.txt with the global.
for _n, _src in [
    ("item", "{{ lst.__class__['a'] }}"),
    ("slice", "{{ lst.__class__[1:] }}"),
    ("attr", "{{ d.__class__.a }}"),
]:
    case(f"divergence/class_generic_alias_{_n}", _src, lst=[1], d={"a": 1})

# Two divergences the page records, pinned so they cannot drift into something
# else. Python 3.9 made a builtin type subscriptable as a type annotation, so
# `dict['k']` is a generic alias whose repr is `dict['k']` -- not a lookup, and
# nothing gojja2 models. `self` is iterable in jinja2 only because
# TemplateReference defines __getitem__, which the legacy protocol accepts.
for _n, _src in [
    ("dict_subscript_str", "{{ dict['k'] }}"),
    ("dict_subscript_int", "{{ dict[0] }}"),
    ("dict_attr_fallback", "{{ dict.nosuch }}"),
    ("range_subscript", "{{ range['k'] }}"),
    ("self_is_iterable", "{% block b %}B{% endblock %}{{ self is iterable }}"),
    ("self_list", "{% block b %}B{% endblock %}{{ self|list }}"),
]:
    case(f"divergence/{_n}", _src)


# A whitespace split with a maxsplit hands back the rest of the subject as its
# last part -- and "the rest" includes the whitespace at the subject's outer
# edge: `'  a b c d  '.split(None, 3)` ends with 'd  ', and rsplit's head keeps
# its leading spaces. Two bugs lived here, and coverage found the code because
# nothing reached it at all.
#
# rsplit(None, n) for n >= 2 *panicked*: the rejoin walked the parts it was
# keeping left to right while cutting them off the right-hand end, so it looked
# for the second-to-last part before the last one had gone, cut past both, and
# then sliced at LastIndex's -1. A limit of 1 was safe, which is why only the
# larger ones fell over.
_SPLITSUBJECTS = [
    "a b c d", "a b c d e", "a  b  c  d", "  a b c d  ", "a\tb\nc d",
    "one", "a b", "a   b   c", "\ta\tb\tc\t", "a b c d e f g",
    "  spaced  out  words  here  ",
]
for _i, _subj in enumerate(_SPLITSUBJECTS):
    for _n in (0, 1, 2, 3, 9):
        case(f"methods/split_ws_maxsplit_{_i}_{_n}", "{{ %r.split(None, %d) }}" % (_subj, _n))
        case(f"methods/rsplit_ws_maxsplit_{_i}_{_n}", "{{ %r.rsplit(None, %d) }}" % (_subj, _n))

# The separator forms take the other branch, and are the control.
for _i, _subj in enumerate(["a,b,c,d", ",a,,b,", "a,,b", ",,,", "abc"]):
    for _n in (0, 1, 2, 3):
        case(f"methods/split_sep_maxsplit_{_i}_{_n}", "{{ %r.split(',', %d) }}" % (_subj, _n))
        case(f"methods/rsplit_sep_maxsplit_{_i}_{_n}", "{{ %r.rsplit(',', %d) }}" % (_subj, _n))

# jinja2 has no bytes literal: `b'a'` lexes as the name b followed by a string,
# and the print statement ends at the name. The cases that meant to grade
# bytes.rsplit were written that way, so all twenty graded this refusal and
# nothing reached a bytes at all -- both of the bytes split helpers sat at zero
# coverage with a corpus that looked like it covered them. One case is enough
# to hold the refusal down.
case("errors/bytes_literal_is_not_syntax", "{{ b'a,b'.rsplit(b',', 1) }}")

# A bytes is spelled `.encode()`, which is how the rest of the bytes cases make
# one. Both branches of both methods are graded: a separator walks from one end
# or the other, and None -- or no argument at all -- splits on runs of ASCII
# whitespace and drops the empties at the edges.
_BYTESEP = ".encode().SPLIT(','.encode(), N) }}"
for _i, _subj in enumerate(["a,b,c,d", ",a,,b,", "a,,b", ",,,", "abc", ""]):
    for _n in (0, 1, 2, 3, 9):
        for _side in ("split", "rsplit"):
            body = "{{ " + lit(_subj) + _BYTESEP.replace("SPLIT", _side).replace("N", str(_n))
            case(f"bytes/{_side}_sep_maxsplit_{_i}_{_n}", body)
    for _side in ("split", "rsplit"):
        case(f"bytes/{_side}_sep_nomax_{_i}",
             "{{ " + lit(_subj) + ".encode()." + _side + "(','.encode()) }}")

for _i, _subj in enumerate(_SPLITSUBJECTS + ["", "   ", "\u00e9 \u00e9"]):
    for _n in (0, 1, 2, 3, 9):
        for _side in ("split", "rsplit"):
            case(f"bytes/{_side}_ws_maxsplit_{_i}_{_n}",
                 "{{ " + lit(_subj) + ".encode()." + _side + "(none, " + str(_n) + ") }}")
    for _side in ("split", "rsplit"):
        case(f"bytes/{_side}_ws_noarg_{_i}",
             "{{ " + lit(_subj) + ".encode()." + _side + "() }}")


# A line statement whose prefix is also the block delimiter. The lexer decided
# which of the two a position opened, and then worked out the tag's *end* by
# reading the opening token back -- "it is a line statement if it does not
# begin with the block delimiter" -- which is true of every line statement
# until the prefix is the block delimiter. Configured as "%" both ways, every
# line statement was lexed as a `{% %}` tag hunting for a closing "%".
_LSCOLLIDE = {"line_statement_prefix": "%", "block_start_string": "%",
              "block_end_string": "%"}
for _n, _src in [
    ("if", "% if true\nX\n% endif\n"),
    ("if_no_trailing_newline", "% if true\nX\n% endif"),
    ("after_text", "A\n% if true\nX\n% endif\n"),
    ("set_then_print", "% set a = 1\n{{ a }}\n"),
    ("set_only", "% set a = 1\n"),
    ("for_with_print", "% for i in [1,2]\n{{ i }}\n% endfor\n"),
    ("for_plain", "% for i in [1]\nX\n% endfor\n"),
    ("print_only", "{{ 1 }}\n"),
    ("plain_text", "plain\n"),
    ("indented", "   % if true\nX\n   % endif\n"),
    ("nested", "% for i in [1]\n% if true\n{{ i }}\n% endif\n% endfor\n"),
]:
    case(f"syntax/ls_is_block_{_n}", _src, __settings__=_LSCOLLIDE)

# The same prefix against the other delimiters, which were already right, and
# a line comment sharing the block delimiter.
for _n, _src, _st in [
    ("ls_vs_variable", "{{ 1 }}\n", {"line_statement_prefix": "{{"}),
    ("ls_vs_comment", "# if true\nX\n# endif\n",
     {"line_statement_prefix": "#", "comment_start_string": "#", "comment_end_string": "#"}),
    ("lc_vs_block", "%c\nkept\n",
     {"line_comment_prefix": "%", "block_start_string": "%", "block_end_string": "%"}),
    ("ls_normal_block", "% if true\nX\n% endif\n", {"line_statement_prefix": "%"}),
]:
    case(f"syntax/{_n}", _src, __settings__=_st)


# The index in "sequence item N" is how many pieces of output the frame already
# held. jinja2 yields a piece per literal run and per runtime print, merges a
# constant print into the literal beside it, and yields once per loop
# iteration -- so N depends on what the render did, not only on the template.
# gojja2 reported 0 always; it counts now.
_FILTERIDX = [
    ("alone", "{% filter length %}abc{% endfilter %}"),
    ("after_text", "x{% filter length %}abc{% endfilter %}"),
    ("after_folded_print", "x{{ 1 }}y{% filter length %}abc{% endfilter %}"),
    ("after_print", "{{ v }}{% filter length %}abc{% endfilter %}"),
    ("after_two_prints", "{{ v }}{{ v }}{% filter length %}abc{% endfilter %}"),
    ("interleaved", "a{{ v }}b{{ v }}c{% filter length %}abc{% endfilter %}"),
    ("after_if", "{% if true %}zz{% endif %}{% filter length %}abc{% endfilter %}"),
    ("after_loop", "{% for i in [1,2] %}{{ i }}{% endfor %}{% filter length %}abc{% endfilter %}"),
    ("after_longer_loop", "{% for i in [1,2,3] %}{{ i }}{% endfor %}{% filter length %}abc{% endfilter %}"),
    ("inside_loop", "{% for i in [1] %}{% filter length %}abc{% endfilter %}{% endfor %}"),
    ("inside_loop_after_text", "{% for i in [1] %}q{% filter length %}abc{% endfilter %}{% endfor %}"),
    ("inside_filter", "{% filter upper %}{% filter length %}abc{% endfilter %}{% endfilter %}"),
    ("inside_macro", "{% macro m() %}{% filter length %}abc{% endfilter %}{% endmacro %}{{ m() }}"),
    ("inside_set", "{% set z %}{{ v }}{% filter length %}abc{% endfilter %}{% endset %}{{ z }}"),
    ("after_set_and_print", "{% set x %}s{% endset %}{{ x }}{% filter length %}abc{% endfilter %}"),
]
for _n, _src in _FILTERIDX:
    case(f"errors/filteridx_{_n}", _src, v="V")


# --- errors -------------------------------------------------------------------
case("errors/syntax_unclosed", "{% if x %}")
case("errors/syntax_unexpected", "{{ 1 + }}")
case("errors/unknown_tag", "{% nope %}")
case("errors/unknown_filter", "{{ 1|nosuch }}")
case("errors/unknown_filter_soft", "{% if true %}{{ 1|nosuch }}{% endif %}")

# An `{% if %}` softens an unknown filter or test that it holds *directly*: the
# name is resolved when the branch runs, so a branch that is not taken renders.
# jinja2 spells that soft_frame, sets it in visit_If and visit_CondExpr, and
# clears it in Frame.inner() -- so every construct that gets a frame of its own
# refuses the same filter at compile time, even inside a branch that can never
# run. The hard cases below all say `{% if false %}` to make that the whole
# claim; the soft ones say `{% if true %}` so the runtime message is graded too.
for _n, _src in [
    ("for_body", "{% for i in seq %}{{ 1|nosuch }}{% endfor %}"),
    ("for_test", "{% for i in seq if 1|nosuch %}{% endfor %}"),
    ("for_else", "{% for i in seq %}{% else %}{{ 1|nosuch }}{% endfor %}"),
    ("for_recursive", "{% for i in seq recursive %}{{ 1|nosuch }}{% endfor %}"),
    ("macro_body", "{% macro m() %}{{ 1|nosuch }}{% endmacro %}"),
    ("macro_default", "{% macro m(a=1|nosuch) %}{% endmacro %}"),
    ("call_body", "{% macro m() %}{% endmacro %}"
                  "{% call m() %}{{ 1|nosuch }}{% endcall %}"),
    ("call_default", "{% macro m() %}{% endmacro %}"
                     "{% call(x=1|nosuch) m() %}{% endcall %}"),
    ("filterblock_name", "{% filter nosuch %}x{% endfilter %}"),
    ("filterblock_body", "{% filter upper %}{{ 1|nosuch }}{% endfilter %}"),
    ("block_body", "{% block b %}{{ 1|nosuch }}{% endblock %}"),
    ("with_body", "{% with a = 1 %}{{ 1|nosuch }}{% endwith %}"),
    ("setblock_body", "{% set v %}{{ 1|nosuch }}{% endset %}"),
    ("setblock_filter", "{% set v | nosuch %}x{% endset %}"),
    ("autoescape_body", "{% autoescape true %}{{ 1|nosuch }}{% endautoescape %}"),
    ("nested_if_for", "{% if false %}{% for i in seq %}{{ 1|nosuch }}"
                      "{% endfor %}{% endif %}"),
    ("elif_for", "{% if true %}{% else %}{% for i in seq %}{{ 1|nosuch }}"
                 "{% endfor %}{% endif %}"),
]:
    case(f"errors/unknown_filter_hard_{_n}",
         "{% if false %}" + _src + "{% endif %}", **SEQ)

for _n, _src in [
    ("for_iter", "{% for i in (1|nosuch) %}{% endfor %}"),
    ("with_value", "{% with a = 1|nosuch %}x{% endwith %}"),
    ("call_arg", "{% macro m() %}{% endmacro %}"
                 "{% call m(1|nosuch) %}{% endcall %}"),
    ("set_value", "{% set v = 1|nosuch %}"),
    ("test", "{{ 1 is nosuchtest }}"),
    ("nested_if", "{% if true %}{{ 1|nosuch }}{% endif %}"),
]:
    case(f"errors/unknown_filter_soft_{_n}",
         "{% if true %}" + _src + "{% endif %}", **SEQ)

# A conditional expression softens the same way, and only its own branches:
# jinja2 calls frame.soft() in visit_CondExpr.
case("errors/unknown_filter_soft_condexpr", "{{ (1|nosuch) if seq else 2 }}",
     seq=[])
case("errors/unknown_filter_soft_condexpr_test", "{{ 1 if (2|nosuch) else 2 }}")
case("errors/unknown_filter_soft_else", "{% if false %}x{% else %}"
     "{{ 1|nosuch }}{% endif %}")
case("errors/unknown_test", "{{ 1 is nosuch }}")
case("errors/bad_assign", "{% set 1 = 2 %}")

# dict() built from a sequence of pairs has two refusals, and they are not alike.
# An element that cannot be iterated at all is a TypeError; one that iterates to
# the wrong length is a ValueError. Neither had any coverage, which is why a 3.14
# change to the first went unnoticed: the version matrix can only compare cases
# the corpus holds, and a message nothing exercises reads as agreement in every
# column.
#
# 3.14 replaced the TypeError with a bare "object is not iterable" -- no index,
# no type name. The ValueError did not move. Both are written here, through every
# path that reaches unpackDictPair, because they share one routine and nothing
# else proves they stay wired to it.
for _n, _src in [
    ("element_not_iterable", "{{ dict([1, 2]) }}"),
    ("element_none", "{{ dict([none]) }}"),
    ("element_second_is_bad", "{{ dict(['ab', 3]) }}"),
    ("element_too_long", "{{ dict([(1, 2, 3)]) }}"),
    ("element_too_short", "{{ dict(['a']) }}"),
    ("element_str_pair", "{{ dict(['ab', 'cd']) }}"),
    ("argument_not_iterable", "{{ dict(1) }}"),
    ("namespace_element", "{{ namespace([1]) }}"),
    ("namespace_too_long", "{{ namespace([(1, 2, 3)]) }}"),
    ("update_element", "{{ d.update([1]) }}"),
    ("update_too_long", "{{ d.update([(1, 2, 3)]) }}"),
    ("class_element", "{{ d.__class__([1]) }}"),
]:
    case(f"errors/dict_update_{_n}", _src, d={})

# `{% set ns.attr = value %}` checks that the target is a namespace before it
# evaluates the value, because jinja2 compiles that check as a statement ahead
# of the assignment rather than as part of it. Every case below has a value
# that also fails, so a check made in the wrong order reports the wrong error
# rather than no error -- which is what the generated differential caught.
case("errors/nsref_checked_before_value", "{% set d.v = 1/0 %}", d=1)
case("errors/nsref_checked_before_value_undefined", "{% set d.v = 1/0 %}")
case("errors/nsref_checked_before_value_tuple",
     "{% set ns = namespace() %}{% set ns.a, d.b = 1/0, 2 %}", d=1)
# ... and the same check does not refuse a target that is a namespace, however
# many times the name appears in it.
case("errors/nsref_repeated_in_tuple",
     "{% set ns = namespace() %}{% set ns.a, ns.b = 1, 2 %}[{{ ns.a }}{{ ns.b }}]")

# `{% set ns.attr %}...{% endset %}` is not the same operation, and the contrast
# is the point of these. jinja2 compiles the two forms through different
# visitors: visit_Assign emits the namespace check above, visit_AssignBlock
# emits none and visit_NSRef writes a bare `ref[attr]`. So the block form is a
# plain item assignment -- which *succeeds* on a dict, where the `=` form on the
# same dict raises.
case("errors/nsref_block_dict", "{% set d.v %}x{% endset %}[{{ d }}]", d={})
case("errors/nsref_block_dict_nonempty", "{% set d.v %}x{% endset %}[{{ d }}]", d={"a": 1})
case("errors/nsref_eq_dict_still_raises", "{% set d.v = 1 %}", d={})
# The rest fail the way Python's __setitem__ fails, naming the concrete type.
case("errors/nsref_block_int", "{% set d.v %}x{% endset %}", d=1)
case("errors/nsref_block_none", "{% set d.v %}x{% endset %}", d=None)
case("errors/nsref_block_list", "{% set d = [1] %}{% set d.v %}x{% endset %}")
case("errors/nsref_block_tuple", "{% set d = (1,) %}{% set d.v %}x{% endset %}")
case("errors/nsref_block_markup", "{% set d = 'a'|safe %}{% set d.v %}x{% endset %}")
# A namespace still works, a filter on the block still applies, and a tuple
# target unpacks the captured text into whichever kind of thing each name holds.
case("errors/nsref_block_namespace",
     "{% set ns = namespace() %}{% set ns.v %}x{% endset %}[{{ ns.v }}]")
case("errors/nsref_block_filtered", "{% set d.v | upper %}ab{% endset %}[{{ d['v'] }}]", d={})
case("errors/nsref_block_tuple_target",
     "{% set ns = namespace() %}{% set ns.a, d.b %}xy{% endset %}[{{ ns.a }}{{ d }}]", d={})
# Listed in known_failures.txt: jinja2 names the sentinel its resolver returns
# for a name that was never set, and `_MissingType` has no counterpart here.
case("errors/nsref_block_undefined", "{% set d.v %}x{% endset %}")
case("errors/nested_mismatch", "{% for x in [1] %}{% endif %}")
case("errors/zero_division", "{{ 1/0 }}|{{ 1//0 }}|{{ 1%0 }}|{{ 1.0/0 }}")

# --- variables that steer without being printed --------------------------------
# The dataflow analysis makes two kinds of negative claim: that a variable's
# value cannot appear in the output, and that it cannot change the output at all.
# Both are checkable by rendering, which is the only check that does not depend
# on the analysis being written twice -- two implementations can be wrong the
# same way, and a render cannot.
#
# The corpus reached almost none of them: nearly every variable in it is printed,
# so there was nothing to check. These are the shapes where a variable decides
# something and is never shown, one per mechanism.
def steer(name, body):
    case(f"dataflow/{name}", body, a=True, c=False, k="one", n="x",
         xs=[1, 2], d={"one": "ONE"}, o={"x": "X"}, s="SECRET")

steer("if_decides", "{% if a %}yes{% else %}no{% endif %}")
steer("cond_expr_decides", '{{ "yes" if c else "no" }}')
steer("loop_length_decides", "{% for i in xs %}.{% endfor %}")
steer("subscript_key_decides", "{{ d[k] }}")
steer("attr_name_decides", "{{ o|attr(n) }}")
steer("test_decides", "{% if a is defined %}here{% endif %}")
# ... and one that cannot reach the output at all.
steer("bound_but_never_read", "{% set unused = s %}done")

# A namespace assigned more than once, which the corpus had no case for and the
# imported chat templates do -- four of them. jinja2's symbol table makes the
# second assignment an *alias* of the first when it is in a nested frame, one
# symbol, while at render time the frame gets its own copy. Reading that as one
# storage made `{{ ns.b }}` report y as never printed, and it is printed. These
# are here so the shape is graded rather than found by luck.
def ns_case(name, body):
    case(f"dataflow/{name}", body, x="X", y="Y", c=True, xs=[1])

ns_case("namespace_reassigned", "{% set ns = namespace(a=x) %}{% set ns = namespace(b=y) %}{{ ns.b }}")
ns_case("namespace_reassigned_in_branch",
        "{% if c %}{% set ns = namespace(a=x) %}{% else %}{% set ns = namespace(b=y) %}{% endif %}{{ ns.b }}")
ns_case("namespace_reassigned_in_loop",
        "{% set ns = namespace(a=x) %}{% for i in xs %}{% set ns = namespace(b=y) %}{{ ns.b }}{% endfor %}")
# A macro parameter's default is how an omitted argument reaches the body, and
# the analysis was evaluating defaults without binding them -- so a value that
# reached the output through one was reported as never printed. Found in Jinja's
# own harvested suite, which is gitignored; this is the committed version.
case("dataflow/macro_default_reaches_body",
     "{% macro m(a, b=src) %}{{ a }}{{ b }}{% endmacro %}{{ m(1) }}", src="S")
# The frame's own copy: the loop writes its own x, so the last read prints 1.
case("dataflow/loop_writes_its_own_copy",
     "{% set x = 1 %}{% for i in xs %}[{{ x }}]{% set x = outer %}{% endfor %}[{{ x }}]",
     xs=[1, 2], outer="O")

ns_case("namespace_field_from_outer",
        "{% set ns = namespace(a=x) %}{% for i in xs %}{{ ns.a }}{% endfor %}")

# Writing a namespace field *mentions* the namespace, which settles the name at
# that level: the later `{% set ns = ... %}` no longer claims it, so ns stays the
# caller's rather than becoming the loop's own.
#
# Mutation testing found this untested -- dropping the load the NSRef performs
# changed ns from one of the caller's variables into a loop-local, and nothing
# failed. It is the first-mention rule reached through a target rather than
# through a read, which is the part with no other case.
case("scope/nsref_write_settles_the_name",
     "{% for i in xs %}{% set ns.v = 1 %}{% set ns = namespace() %}{{ ns }}{% endfor %}",
     xs=[], ns=None)
case("scope/nsref_write_settles_the_name_block",
     "{% for i in xs %}{% set ns.v %}q{% endset %}{% set ns = namespace() %}{{ ns }}{% endfor %}",
     xs=[], ns=None)

# An attribute access decides whether the render finishes, and the analysis did
# not know it. Twice.
#
# The first pass taught it that reaching *through* an attribute can raise --
# `{{ (f.real).name }}` renders for a float and raises for a string -- and left a
# one-step access exempt, on the grounds that a missing attribute is undefined
# and prints empty. That is true of three of the four Undefined classes and false
# of the one whose whole purpose is to refuse: under StrictUndefined
# `{{ src.nosuch }}` raises at the access. The exemption was a false negative,
# which is the one kind of error a caller reading "cannot fail because of this"
# cannot recover from, and it cost 17 of 167 claims over the corpus to drop.
#
# Both were found by the soak's render check rather than by the differential,
# because the Python reference had the same gap each time -- the two agreed with
# each other and both were wrong. Writing "an undefined prints empty" is what
# produced both; it is a statement about a *setting*, not about jinja2.
case("dataflow/required_through_an_attribute", "{{ (src.real).name }}", src=2.5)
case("dataflow/required_through_an_item", "{{ (src.real)[0] }}", src=2.5)
case("dataflow/required_by_a_one_step_attribute", "[{{ src.nosuch }}]", src=2.5)
# A namespace field is the exception, and the only one: the field is named in the
# template, so whether it is there is not in doubt.
case("dataflow/not_required_through_a_namespace_field",
     "{% set ns = namespace(v=src) %}[{{ ns.v }}]", src=2.5)

# A guard whose branch writes a namespace field decides whether the render
# finishes, because writing one needs something to write it to: the `=` form
# wants a namespace, and the block form does an item assignment that a None
# refuses. An nsref appears only as a `{% set %}` target, so its presence in a
# branch is the whole test.
#
# Found by the soak's render check, like the two above it, and the Python
# reference had the same gap.
case("dataflow/required_guarding_a_namespace_write",
     "{% if src %}{% set nothing.v = 1 %}{% endif %}", src=True, nothing=None)
case("dataflow/required_guarding_a_namespace_block",
     "{% if src %}{% set nothing.v %}x{% endset %}{% endif %}", src=True, nothing=None)
# ...and a branch that only binds a plain name cannot fail, so the guard is not
# Required -- which is what keeps the rule about namespace writes.
case("dataflow/not_required_guarding_a_plain_set",
     "{% if src %}{% set q = 1 %}{{ q }}{% endif %}", src=True)

# A macro whose name is not a binding in any scope.
#
# jinja2's first-mention rule resolves a name outward when an earlier branch
# merely *mentions* it, so a macro defined once inside a dead `{% if %}` and
# again for real afterwards has no local symbol at all: both definitions write
# to the caller's variable. The analysis had nothing to hang the body on and
# dropped what the body would print, which made everything inside the macro
# invisible -- `src` reported as never printed, and it is printed.
#
# Found by `make soak-syntax` at 59,414 templates, by rendering rather than by
# comparing: both analyses agreed, and both were wrong. That is the case the
# render check exists for.
case("dataflow/macro_without_a_binding",
     "{% if false %}{% macro m(a) %}A{% endmacro %}{% endif %}"
     "{% macro m(a) %}{{ src }}{% endmacro %}{{ m(1) }}", src="S")
case("dataflow/macro_without_a_binding_in_a_loop",
     "{% if false %}{% macro m(a) %}A{% endmacro %}{% endif %}"
     "{% macro m(a) %}{% for i in xs %}{{ i }}{% endfor %}{% endmacro %}{{ m(1) }}", xs=["P", "Q"])
# ...and the shape that must keep answering "not printed": here `m` *is* a local
# binding, the second definition wins, and it does not print src.
case("dataflow/macro_redefined_does_not_print",
     "{% macro m(a) %}{{ src }}{% endmacro %}{% macro m(a) %}A{% endmacro %}{{ m(1) }}", src="S")
# Every shape a division by zero can take, one case per sentence. CPython had
# six distinct wordings before 3.14 collapsed them, and the case above reaches
# only three -- which is why "float modulo" kept the 3.11 wording on 3.13 with
# nothing to notice. Each is its own case so a regression names the operator.
case("errors/zero_division_float_mod", "{{ 1 % 0.0 }}")
case("errors/zero_division_float_mod_lhs", "{{ 1.0 % 0 }}")
case("errors/zero_division_float_mod_both", "{{ 1.0 % 0.0 }}")
case("errors/zero_division_float_floor", "{{ 1 // 0.0 }}")
case("errors/zero_division_float_floor_both", "{{ 1.0 // 0.0 }}")
case("errors/zero_division_float_div_rhs", "{{ 1 / 0.0 }}")
# The neighbouring message 3.14 moved in the same release.
case("errors/zero_to_negative_power", "{{ 0 ** -1 }}")
case("errors/zero_to_negative_power_float", "{{ 0.0 ** -1 }}")
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

# |truncate over a value that is not a string. jinja2 measures it, slices it,
# and then either concatenates the end or calls rsplit -- so what fails, and
# what the message is about, depends on the kind rather than on the filter.
#
# bytes is the one that is not an AttributeError: it *has* rsplit, and refuses
# the str separator jinja2 passes. The render differential found gojja2 saying
# `'bytes' object has no attribute 'rsplit'`, which is wrong about the type.
# The bytes here is built in the template, because JSON carries no bytes.
_LONG = "{% set big = ('ab' * 30).encode() %}"
for _n, _src in [
    ("bytes_rsplit_separator", _LONG + "{{ big|truncate(10) }}"),
    ("bytes_short_is_returned", "{{ 'ab'.encode()|truncate(10) }}"),
    ("bytes_end_is_str", _LONG + "{{ big|truncate(10, true) }}"),
    ("bytes_end_is_bytes", _LONG + "{{ big|truncate(10, true, 'xy'.encode()) }}"),
    ("bytes_end_is_bytes_no_kill", _LONG + "{{ big|truncate(10, false, 'xy'.encode()) }}"),
    ("bytes_under_leeway", _LONG + "{{ big|truncate(60) }}"),
    ("list_has_no_rsplit", "{{ longlist|truncate(10) }}"),
    ("list_end_is_str", "{{ longlist|truncate(10, true) }}"),
    ("dict_under_leeway", "{{ d|truncate(10) }}"),
    ("int_has_no_len", "{{ n|truncate(10) }}"),
]:
    case(f"filters/truncate_{_n}", _src,
         longlist=list(range(30)), d={"a": 1}, n=3)
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
# A C function counts its arguments, and a tuple names itself: see
# errors/method_get_too_many.
case("errors/tuple_index_missing", "{{ (1,2).index(99) }}")
case("errors/list_index_missing", "{{ [1,2].index(99) }}")
case("errors/sort_positional_argument", "{% set L = [1] %}{{ L.sort(1) }}")
case("errors/popitem_on_an_empty_dict", "{{ {}.popitem() }}")

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

# The argument clinic's shape for int.to_bytes, int.from_bytes and
# float.fromhex, none of which was checked at all: to_bytes reported the type of
# whatever landed on `byteorder`, from_bytes ignored a third argument and every
# unknown keyword, and fromhex took any number of arguments and read the first.
# CPython checks the total before the keyword names and the keyword names before
# the positional count, so all three thresholds are graded, in that order.
for _n, _src in [
    ("to_bytes_total", "{{ (3).to_bytes(2,'big',true,1) }}"),
    ("to_bytes_total_over_keyword", "{{ (3).to_bytes(2,'big',true,nope=1) }}"),
    ("to_bytes_unknown_keyword", "{{ (3).to_bytes(nope=1) }}"),
    ("to_bytes_positional", "{{ (3).to_bytes(2,'big',true) }}"),
    ("from_bytes_missing", "{{ (3).from_bytes() }}"),
    ("from_bytes_total", "{{ (3).from_bytes('ab'.encode(),'big',true,1) }}"),
    ("from_bytes_unknown_keyword", "{{ (3).from_bytes('ab'.encode(), nope=1) }}"),
    ("from_bytes_positional", "{{ (3).from_bytes('ab'.encode(),'big',true) }}"),
    ("float_fromhex_many", "{{ (1.5).fromhex('0x1.8p+0', 1) }}"),
    ("float_fromhex_none", "{{ (1.5).fromhex() }}"),
    ("float_fromhex_keyword", "{{ (1.5).fromhex(nope='a') }}"),
]:
    case(f"errors/clinic_{_n}", _src)

# The shapes these still accept, so a check added above cannot quietly narrow
# them: `signed` is keyword-only, and both parameters can be named.
# Go's float parser reads a hexadecimal float and Python's float() does not --
# only float.fromhex() takes that form, which is exactly what float.hex()
# writes. So `{{ (1.5).hex()|float }}` answered 1.5 where CPython answers the
# filter's default, and |filesizeformat sized a string CPython refuses.
for _n, _src in [
    ("hex_through_float", "{{ (1.5).hex()|float }}"),
    ("hex_upper_through_float", "{{ '0X1.8P+0'|float }}"),
    ("hex_signed_through_float", "{{ '-0x1.8p+0'|float }}"),
    ("hex_through_filesizeformat", "{{ (0.0).hex()|filesizeformat }}"),
    ("hex_through_int", "{{ '0x10'|int }}"),
    ("plain_still_reads", "{{ '1.5'|float }}|{{ '1_0'|float }}|{{ '1e5'|float }}|"
     "{{ ' 1.5 '|float }}|{{ 'inf'|float }}|{{ '-0.0'|float }}"),
]:
    case(f"filters/float_{_n}", _src)

# dict_keys and dict_items compare as sets, and defining __eq__ without __hash__
# leaves them unhashable; dict_values defines neither and hashes by identity.
# All three hashed by identity here, so a membership test that CPython refuses
# quietly answered False instead.
for _n, _src in [
    ("keys_in_dict", "{{ d.keys() in d }}"),
    ("items_in_dict", "{{ d.items() in d }}"),
    ("values_in_dict", "{{ d.values() in d }}"),
    ("keys_in_list", "{{ d.keys() in [1] }}"),
    ("keys_as_a_key", "{{ {d.keys(): 1} }}"),
    ("keys_through_unique", "{{ [d.keys(), d.keys()]|unique|list }}"),
    ("values_through_unique", "{{ [d.values()]|unique|list }}"),
]:
    case(f"methods/dictview_hash_{_n}", _src, d={"a": 1})

# A ChainableUndefined answers a further lookup with itself, and the fold has to
# as well: a *slice* of a value that has none is a hard TypeError at run time, so
# the undefined the fold produced never existed there to chain from and
# `((2.5)[1:2])[0]` raised where jinja2 prints nothing. An attribute route to the
# same shape worked, which is why it only ever showed through a slice.
for _n, _src in [
    ("slice_then_attr", "{{ ((2.5)[1:2]).nope }}"),
    ("slice_then_item", "{{ ((2.5)[1:2])[0] }}"),
    ("slice_then_item_twice", "{{ ((2.5)[1:2])[0][1] }}"),
    ("attr_then_item", "{{ ((2.5).nope)[0] }}"),
    ("slice_printed", "{{ (2.5)[1:2]|string }}"),
    ("slice_is_defined", "{{ ((2.5)[1:2]) is defined }}"),
]:
    case(f"undefined/fold_chain_{_n}", _src)
    for _u in ("chainable", "debug", "strict"):
        case(f"undefined/fold_chain_{_n}_{_u}", _src,
             __settings__={"undefined": _u})

# int.from_bytes takes anything bytes() would take from an iterable, which it
# only ever read as a bytes: `int.from_bytes([1, 2])` said it could not convert
# a list where CPython answers 258. A str and an int are refused outright, since
# bytes() reads one as text needing an encoding and the other as a count, and
# byteorder is bound and checked -- type, then value -- before the first
# argument is converted at all.
for _n, _src in [
    ("list", "{{ (3).from_bytes([1,2]) }}"),
    ("tuple", "{{ (3).from_bytes((1,2)) }}"),
    ("range", "{{ (3).from_bytes(range(3)) }}"),
    ("empty", "{{ (3).from_bytes([]) }}"),
    ("bools", "{{ (3).from_bytes([true, false]) }}"),
    ("little", "{{ (3).from_bytes([1,2], 'little') }}"),
    ("dict_yields_keys", "{{ (3).from_bytes({'a': 1}) }}"),
    ("element_not_an_integer", "{{ (3).from_bytes([1.5]) }}"),
    ("element_over_255", "{{ (3).from_bytes([300]) }}"),
    ("element_negative", "{{ (3).from_bytes([-1]) }}"),
    ("str_refused", "{{ (3).from_bytes('ab') }}"),
    ("int_refused", "{{ (3).from_bytes(3) }}"),
    ("none_refused", "{{ (3).from_bytes(none) }}"),
    ("float_refused", "{{ (3).from_bytes(1.5) }}"),
    ("byteorder_type_before_source", "{{ (3).from_bytes(1.5, 2) }}"),
    ("byteorder_value_before_source", "{{ (3).from_bytes(1.5, 'nope') }}"),
    ("byteorder_type_before_elements", "{{ (3).from_bytes([1.5], 2) }}"),
]:
    case(f"methods/from_bytes_{_n}", _src)

case("methods/int_clinic_keywords",
     "{{ (3).to_bytes(length=2, byteorder='big') }}|"
     "{{ (3).to_bytes(2, 'big', signed=true) }}|"
     "{{ (3).from_bytes(bytes='ab'.encode()) }}|"
     "{{ (3).from_bytes('ab'.encode(), byteorder='little') }}|"
     "{{ (3).from_bytes('ab'.encode(), 'big', signed=true) }}|"
     "{{ (1.5).fromhex('0x1.8p+0') }}")

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
# (The case that shows the autoescape block owning what it assigns is
# scope/inner_frame_with_no_outer_reference, which is the same template.)
case("scope/autoescape_does_not_leak_out",
     "[{{ m }}]{% autoescape false %}{% set m = 1 %}{% endautoescape %}[{{ m }}]", m=10)
case("scope/autoescape_import_is_an_assignment",
     "{% autoescape false %}{% for i in [1] %}[{{ mod }}]{% endfor %}"
     "{% import 'mod.html' as mod %}{% endautoescape %}",
     m=10, __templates__={"mod.html": "{% set a = 1 %}"})

# A binding the frame already has is not a copy of anything outside. jinja2's
# `Symbols.store` asks its *own* table first, so writing to a macro parameter,
# a loop target or a {% with %} name leaves that binding alone -- where gojja2
# saw a store of a name an enclosing frame also binds and made the parameter an
# alias of it. Nothing rendered differently, because a parameter is overwritten
# by its argument before the body runs; what was wrong was the scope facts the
# tree exposes, and so what the dataflow analysis derives from. Found by the
# syntax differential once the generator could write a macro parameter named
# after one of the specials -- an enclosing macro frame always provides those.
case("scope/param_written_in_the_body",
     "{% set x = 0 %}{% macro b(x) %}[{{ x }}]{% set x = 1 %}[{{ x }}]{% endmacro %}"
     "{{ b(2) }}[{{ x }}]")
case("scope/defaulted_param_written_in_the_body",
     "{% set x = 0 %}{% macro b(x=9) %}[{{ x }}]{% set x = 1 %}[{{ x }}]{% endmacro %}"
     "{{ b() }}[{{ x }}]")
case("scope/param_written_in_a_nested_macro",
     "{% macro a(x) %}{% macro b(x) %}[{{ x }}]{% set x = 1 %}[{{ x }}]{% endmacro %}"
     "{{ b(2) }}{% endmacro %}{{ a(3) }}")
case("scope/loop_target_written_in_the_body",
     "{% set x = 0 %}{% for x in [7] %}[{{ x }}]{% set x = 2 %}[{{ x }}]{% endfor %}[{{ x }}]")
case("scope/with_target_written_in_the_body",
     "{% set x = 0 %}{% with x = 5 %}[{{ x }}]{% set x = 1 %}[{{ x }}]{% endwith %}[{{ x }}]")
case("scope/param_named_caller_written_in_the_body",
     "{% macro sp(caller=2) %}[{{ caller }}]{% set caller = 1 %}[{{ caller }}]{% endmacro %}"
     "[{{ sp(3) }}]")
case("scope/call_block_param_written_in_the_body",
     "{% macro takes() %}<{{ caller(1) }}>{% endmacro %}"
     "{% call(p) takes() %}[{{ p }}]{% set p = 1 %}[{{ p }}]{% endcall %}")
case("scope/loop_target_written_inside_a_macro",
     "{% macro b(x) %}{% for x in [7] %}[{{ x }}]{% set x = 2 %}[{{ x }}]{% endfor %}"
     "[{{ x }}]{% endmacro %}{{ b(4) }}")

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
# str() of a *container* is its repr, and a repr escapes by the interpreter's
# isprintable -- so every filter, test and method that renders its subject as
# text has to read the version.
#
# `strictStr` passed DefaultPythonVersion and nineteen of its twenty callers used
# it, so |upper, |lower, |title, |trim, |replace, |center, |indent, |truncate,
# |wordwrap, |wordcount, |striptags, |format, |safe, |escape, |forceescape,
# |join, `is lower`/`is upper` and str() all answered the pin's escaping whatever
# WithPythonVersion said. It is gone: there is only strictStrFor now, and the
# version is not optional. Seven more sites went the same way -- |urlize,
# |xmlattr, |urlencode, |join's separator, escapeIfNeeded, str.format's `!s` and
# its empty conversion.
#
# Found by a soak on the version axis, on `'\ua7da'.splitlines(true)|upper` --
# the list repr |upper asks for, uppercased afterwards, which is why the escape
# came out as "\UA7DA".
for _n, _src in [
    ("upper", "{{ '\ua7da'.splitlines(true)|upper }}"),
    ("trim", "{{ ['\ua7da']|trim }}"),
    ("center", "{{ ['\ua7da']|center(30) }}"),
    ("truncate", "{{ ['\ua7da']|truncate(30) }}"),
    ("striptags", "{{ ['\ua7da']|striptags }}"),
    ("escape", "{{ ['\ua7da']|escape }}"),
    ("safe", "{{ ['\ua7da']|safe }}"),
    ("title", "{{ ['\ua7da']|title }}"),
    ("wordcount", "{{ ['\ua7da']|wordcount }}"),
    ("replace", "{{ ['\ua7da']|replace('x', 'y') }}"),
    ("urlize", "{{ ['\ua7da']|urlize }}"),
    ("xmlattr", "{{ {'a': ['\ua7da']}|xmlattr }}"),
    ("join", "{{ [['\ua7da'], 'x']|join('-') }}"),
    ("join_separator", "{{ [['\ua7da'], 'x']|join(['\ua7da']) }}"),
    ("is_lower", "{{ ['\ua7da'] is lower }}"),
    ("format_conversion", "{{ '{!s}'.format(['\ua7da']) }}"),
    ("format_empty_spec", "{{ '{}'.format(['\ua7da']) }}"),
    ("format_filter", "{{ ('%s'|safe)|format(['\ua7da']) }}"),
]:
    case(f"escape/container_text_by_version_{_n}", _src)

# Two more places the interpreter's tables have to reach, both found by a soak on
# the version axis.
#
# str.title decides a word boundary by the *Cased* property, which is Lowercase,
# Uppercase and the titlecase category together -- and the first two move between
# interpreters, so one fixed table for it disagreed with 3.11 about 73 code points
# and with 3.14 about 52. The one that mattered: a *new* uppercase letter did not
# count as cased, which ended a word and left the character after it titlecased
# instead of lowered. U+A7CB is that letter in 3.14.
case("methods/title_cased_boundary_by_version",
     "{{ '\ua7cb\ua7cc'.title() }}|{{ 'a\ua7cbb'.title() }}|"
     "{{ '\ua7cb\ua7cc'.swapcase() }}|{{ 'a1b'.title() }}|{{ 'a\u01f3'.title() }}")
# And a *folded* print converted its value with the pinned interpreter's tables:
# a container's text is its repr, repr escapes by isprintable, and the unfolded
# path was already right -- so `{{ ['\u1c89'] }}` disagreed with itself depending
# on whether the expression was constant. The same call folds `~`.
case("escape/folded_repr_escapes_by_version",
     "{{ ['\u1c89'] }}|{{ {'a': '\u1c89'} }}|{{ ('\u1c89',) }}|{{ ['\ua7da'] }}|"
     "{{ '\u1c89' ~ ['\u1c89'] }}|{{ ['\u0378'] }}")

# Every repr a *message* or an object carries escapes by the interpreter's
# isprintable too, and six of them were reading the pin's tables: the float
# conversion error on both the filter and the `%` path, a replacement field's
# KeyError, a dict view's repr, a namespace's, and a slice's inside a mapping's
# KeyError. A namespace and a view carry the version on the object, because Reprer
# takes no arguments. Found by a soak on the version axis, on
# `'\u019bA\u1c89 b'|filesizeformat`.
case("errors/float_conversion_repr_by_version",
     "{{ '\u019bA\u1c89 b'|filesizeformat() }}")
case("errors/percent_float_conversion_repr_by_version", "{{ '%f' % '\u1c89 x' }}")
case("errors/format_field_repr_by_version", "{{ '{\u1c89}'.format(a=1) }}")
case("methods/dict_view_repr_by_version",
     "{% set q = {'\u1c89': 1} %}{{ q.keys() }}|{{ q.items() }}")
case("globals/namespace_repr_by_version", "{{ namespace(v='\u1c89') }}")
case("errors/slice_key_repr_by_version", "{% set q = {'a': 1} %}{{ q['\u1c89':] }}")

# |wordcount and |wordwrap read Python's `\w` and `[^\d\W]`, which for a str
# pattern are Py_UNICODE_ISALNUM and that minus isdecimal -- not Go's IsLetter
# and IsDigit.
#
# gojja2 read them off Go's tables: 9,039 code points wrong against the pin for
# `\w` and 10,097 for `[^\d\W]`, with the count moving under whichever Unicode
# release the toolchain carried. Composing the classifiers gojja2 already has for
# isalpha and isnumeric is exact on all 1,112,064 code points for every
# interpreter, so no new table was needed -- only for these two to stop asking Go.
#
# The two directions: U+00B2 is `\w` and not `\d`, so it is a word *letter*
# although unicode.IsLetter says no -- which is why `a\u00b2-\u00b2b` breaks after
# the hyphen. U+A7DA and U+10D40 are unassigned before 3.14, so a word made of
# them is no word at all there. Found by a soak seed on `'\ua7da\ua7db\ua7dc'|wordcount`.
case("filters/wordcount_reads_the_interpreters_class",
     "{{ '\ua7da\ua7db\ua7dc'|wordcount }}|{{ '\u019b\u0264'|wordcount }}|"
     "{{ '\u00b2\u00b3'|wordcount }}|{{ 'a\u00b2b'|wordcount }}|"
     "{{ '\u0f33'|wordcount }}|{{ '[]'|wordcount }}|{{ 'a\u0897b'|wordcount }}")
# The width has to be one that makes the *hyphen rule* choose the break, not one
# that hard-breaks the word anyway: at width 3 `a\u00b2-\u00b2b` comes out the same
# whichever class U+00B2 is in, so that case graded nothing. At width 4 the
# position moves.
case("filters/wordwrap_word_letter_is_not_a_digit",
     "{{ 'a\u00b2-\u00b2bcdef'|wordwrap(4) }}|{{ '\u00b2\u00b2-\u00b2bcdef'|wordwrap(4) }}|"
     "{{ 'a\u00b2\u00b2-\u00b2bcdef'|wordwrap(5) }}|{{ 'a1-1bcdef'|wordwrap(4) }}|"
     "{{ 'ab-cdefgh'|wordwrap(4) }}")
case("filters/wordwrap_hyphens_and_dashes",
     "{{ 'a-1-b'|wordwrap(3) }}|{{ '\u019b-\u019b\u019b'|wordwrap(2) }}|"
     "{{ 'a--b'|wordwrap(2) }}")
# The decimal *value* an interpreter reads a code point as, which the override
# has to carry rather than flip: a version newer than the pin assigns digits the
# pin has never heard of, and recording membership alone answered -1 for all 80
# of 3.14's. `|int(-1)` shows it, because |int falls back rather than raising.
case("filters/decimal_value_by_version",
     "{{ '\U00010d40'|int(-1) }}|{{ '\U00010d41'|int(-1) }}|{{ '\u0f20'|int(-1) }}|"
     "{{ '\U00010d40'.isdecimal() }}|{{ '\U00010d40'.isdigit() }}|"
     "{{ '\U00010d40'.isnumeric() }}")

# str.isidentifier is CPython's XID_Start/XID_Continue tables, not the rule the
# grammar states.
#
# gojja2 read it as `unicode.IsLetter(c) || Nl || '_'` for the first character and
# that plus digits and Mn/Mc/Pc for the rest, which is the rule and *not* the
# table: it disagreed with the pin about 8,975 code points for the first half and
# 9,168 for the second, and it followed whichever Unicode release the Go
# toolchain carried. It is two absolute tables now, generated per interpreter
# like isalpha and isprintable.
#
# U+037A may not begin an identifier although it is a letter, and U+00B7 may
# continue one although it is punctuation -- the two directions Go's rule got
# wrong. U+200C and U+200D became continuers in 3.13, and U+1C89, U+A7CB and
# U+A7DA arrived in 3.14, so those are the version axis. Found by a soak seed
# after the generator learned to draw code points whose casing changed.
case("methods/isidentifier_against_the_table",
     "{{ '\u037a'.isidentifier() }}|{{ 'a\u037a'.isidentifier() }}|"
     "{{ '\u00b7'.isidentifier() }}|{{ 'a\u00b7'.isidentifier() }}|"
     "{{ '\u0e33'.isidentifier() }}|{{ 'a\u0387'.isidentifier() }}")
case("methods/isidentifier_by_version",
     "{{ 'a\u200c'.isidentifier() }}|{{ 'a\u200d'.isidentifier() }}|"
     "{{ '\u1c89'.isidentifier() }}|{{ 'a\u1c89'.isidentifier() }}|"
     "{{ '\ua7cb'.isidentifier() }}|{{ '\ua7da'.isidentifier() }}|"
     "{{ 'a\u0897'.isidentifier() }}")
case("methods/isidentifier_above_the_basic_plane",
     "{{ '\U00010d50'.isidentifier() }}|{{ '\U00010d70'.isidentifier() }}|"
     "{{ 'a\U00010d50'.isidentifier() }}|{{ '\U0001d7ca'.isidentifier() }}")
# ...and the shapes that decide nothing about tables: the empty string, a digit
# first, an underscore, and a keyword.
case("methods/isidentifier_shape",
     "{{ ''.isidentifier() }}|{{ '_'.isidentifier() }}|{{ '_a1'.isidentifier() }}|"
     "{{ '1a'.isidentifier() }}|{{ 'class'.isidentifier() }}|{{ 'a b'.isidentifier() }}")

# A set is unhashable: Python's set defines __eq__ without __hash__, and only
# frozenset hashes. gojja2's hashed by identity, so `{{ (d.keys() - 'a') is
# filter }}` answered False where CPython raises -- and a set went into a dict as
# a key and into |unique without complaint. It is the only set a template can
# hold, so `is filter` asking the environment's registry is the shape that found
# it: that is a dict membership test, which hashes before it looks at whether the
# value is a name.
for _n, _src in [
    ("is_filter", "{{ (q.keys() - 'a') is filter }}"),
    ("is_test", "{{ (q.keys() - 'a') is test }}"),
    ("as_a_dict_key", "{{ {(q.keys() - 'a'): 1} }}"),
    ("in_a_dict", "{{ (q.keys() - 'a') in q }}"),
    ("through_unique", "{{ [(q.keys() - 'a')]|unique|list }}"),
]:
    case(f"errors/set_unhashable_{_n}", _Q + _src)
# ...and tests/is_filter_and_is_test above already holds the questions that do
# have answers, which is what keeps the hash from being the whole of the test.

# A strict handler is handed the *maximal run* of characters the codec cannot
# represent, not the first one, and the message for a run of two or more is a
# different sentence: "can't encode characters in position 0-1" carries no
# character at all, says "characters", and ends inclusively. gojja2 reported the
# first character every time, so every run read as a single character. A run stops
# at the first encodable character, which is what keeps `'\u019ba\u0264'` at
# position 0 alone. Found by a soak seed, after the generator learned to draw
# code points whose casing changed between interpreters.
for _n, _src in [
    ("run_of_two", "{{ '\u019b\u0264'.encode('ascii') }}"),
    ("run_of_three", "{{ '\u019b\u0264\u1c89'.encode('ascii') }}"),
    ("run_inside", "{{ 'ab\u019b\u0264\u1c89cd'.encode('ascii') }}"),
    ("run_broken_by_an_encodable", "{{ '\u019ba\u0264'.encode('ascii') }}"),
    ("run_of_one_inside", "{{ 'a\u019bb'.encode('ascii') }}"),
    ("run_in_latin_1", "{{ '\u019b\u0264'.encode('latin-1') }}"),
    ("run_latin_1_takes_e_acute", "{{ '\u00e9\u00e9x'.encode('ascii') }}"),
]:
    case(f"errors/encode_{_n}", _src)
# ...and the handlers that do not raise, which walk the same run without needing
# to describe it.
case("methods/encode_handlers_over_a_run",
     "{{ '\u019b\u0264x'.encode('ascii', 'replace') }}|"
     "{{ '\u019b\u0264x'.encode('ascii', 'ignore') }}|"
     "{{ '\u019b\u0264x'.encode('ascii', 'backslashreplace') }}|"
     "{{ '\u019b\u0264x'.encode('ascii', 'xmlcharrefreplace') }}")
case("errors/encode_surrogateescape_over_a_run",
     "{{ '\u019b\u0264'.encode('ascii', 'surrogateescape') }}")
case("errors/encode_latin1_range", "{{ '\u20ac'.encode('latin-1') }}")
case("errors/decode_ascii_range", "{{ '\u00e9'.encode().decode('ascii') }}")

# --- where a \N escape is malformed rather than merely unknown ----------------
# gojja2 carries no Unicode name database, so every well-formed \N{...} is
# refused -- that is in docs/divergences.md and asserted in strlit_test.go. The
# *malformed* spellings are a different thing: they are wrong under CPython too,
# so the two agree and these grade that they keep agreeing.
#
# The boundary is exact and not obvious. An empty name is malformed; anything at
# all between the braces is a name CPython looks up, including a single space,
# which is "unknown" and not "malformed". gojja2 split the two one character off
# and called `\N{}` unknown.
for _n, _esc in [
    ("empty", r"\N{}"),
    ("empty_then_brace", r"\N{}}"),
    ("no_closing_brace", r"\N{BULLET"),
    ("no_brace_at_all", r"\N"),
    ("followed_by_text", r"\NX"),
]:
    case("errors/n_escape_malformed_" + _n, '{{ "' + _esc + '" }}')

# --- which error handler belongs to which direction ---------------------------
# The handlers are not one set. xmlcharrefreplace and namereplace are declared
# for an encode and CPython's callback refuses a UnicodeDecodeError by type, so
# asking for one on a decode is a TypeError and not a LookupError.
# backslashreplace goes both ways, and on a decode it writes one \xNN per byte
# of the error -- a truncated three-byte sequence is one error and three
# escapes. surrogateescape and surrogatepass are the two gojja2 does not have:
# both answer with a lone surrogate, which a Go string cannot hold. On an
# encode they have nothing to carry, because no character that reaches them is
# one, so they are strict.
case("methods/decode_backslashreplace",
     "{{ (255).to_bytes(1,'big').decode('utf-8','backslashreplace') }}|"
     "{{ ((240).to_bytes(1,'big') + (159).to_bytes(1,'big')).decode('utf-8','backslashreplace') }}|"
     "{{ ('a'.encode() + (255).to_bytes(1,'big') + 'b'.encode()).decode('utf-8','backslashreplace') }}|"
     "{{ '\u00e9'.encode().decode('ascii','backslashreplace') }}")
case("errors/decode_xmlcharrefreplace_is_encode_only",
     "{{ (255).to_bytes(1,'big').decode('utf-8','xmlcharrefreplace') }}")
case("errors/decode_namereplace_is_encode_only",
     "{{ (255).to_bytes(1,'big').decode('utf-8','namereplace') }}")
case("errors/encode_surrogateescape_is_strict",
     "{{ '\u00e9'.encode('ascii','surrogateescape') }}")
case("errors/encode_surrogatepass_is_strict",
     "{{ '\u20ac'.encode('latin-1','surrogatepass') }}")
case("methods/encode_surrogate_handlers_never_run",
     "{{ '\u00e9'.encode('utf-8','surrogateescape') }}|{{ '\u00e9'.encode('utf-8','surrogatepass') }}")
# The handler is looked up only when there is an error to hand it, so a name
# nobody has ever heard of is fine as long as everything encodes.
case("methods/handler_lookup_is_lazy",
     "{{ 'abc'.encode('ascii','nosuch') }}|{{ 'abc'.encode().decode('utf-8','nosuch') }}")
case("errors/encode_unknown_handler", "{{ '\u00e9'.encode('ascii','nosuch') }}")
case("errors/decode_unknown_handler",
     "{{ (255).to_bytes(1,'big').decode('utf-8','nosuch') }}")

# --- what the UTF-8 decoder says about a byte it will not take ---------------
# Refusing is not one answer. CPython names one of three reasons, covers a
# number of bytes that the message reports as either "byte 0xNN in position P"
# or "bytes in position P-Q", and calls the error handler once per error rather
# than once per byte -- so `replace` writes one U+FFFD for a truncated sequence
# and several for a run of bad start bytes. gojja2 asked utf8.DecodeRune and
# reported one byte at a time, which got all three wrong; utf8digest.go walks
# the whole state machine, and these are the ones worth reading.
#
# to_bytes and + are the only way to say an arbitrary byte in a template: there
# is no bytes literal, and .encode() cannot produce invalid UTF-8.
def _bs(*bytes_):
    return " + ".join("(%d).to_bytes(1,'big')" % b for b in bytes_)

for _name, _seq, _handler in [
    # An invalid start byte: a continuation with nothing to continue, and the
    # two leads that could only encode something already spelled shorter.
    ("start_byte", (0xff,), ""),
    ("start_byte_bare_continuation", (0x80,), ""),
    ("start_byte_overlong_c0", (0xc0, 0x80), ""),
    ("start_byte_overlong_c1", (0xc1, 0xbf), ""),
    ("start_byte_above_max", (0xf5, 0x80, 0x80, 0x80), ""),
    # A continuation byte that is wrong rather than missing, at each of the
    # three positions it can be wrong at.
    ("continuation_1", (0xc3, 0x28), ""),
    ("continuation_2", (0xe2, 0x82, 0x28), ""),
    ("continuation_3", (0xf0, 0x90, 0x80, 0x28), ""),
    ("continuation_overlong_e0", (0xe0, 0x80, 0x80), ""),
    ("continuation_surrogate_ed", (0xed, 0xa0, 0x80), ""),
    ("continuation_overlong_f0", (0xf0, 0x80, 0x80, 0x80), ""),
    ("continuation_above_max_f4", (0xf4, 0x90, 0x80, 0x80), ""),
    # Missing rather than wrong, which is the case the old decoder had no
    # wording for at all -- and the only one whose message names a range.
    ("end_of_data_1", (0xc3,), ""),
    ("end_of_data_2", (0xf0, 0x9f), ""),
    ("end_of_data_3", (0xf0, 0x9f, 0x92), ""),
    ("end_of_data_mid_string", (0x41, 0xe0, 0xa0), ""),
    ("end_of_data_after_valid", (0xc3, 0xa9, 0xf0, 0x9f), ""),
    # The truncated forms that are still decided before the end is reached.
    ("truncated_overlong_e0", (0xe0, 0x80), ""),
    ("truncated_surrogate_ed", (0xed, 0xa0), ""),
    ("truncated_ok_so_far_ed", (0xed, 0x9f), ""),
    ("truncated_ok_so_far_e0", (0xe0, 0xa0), ""),
    ("truncated_overlong_f0", (0xf0, 0x80), ""),
    ("truncated_above_max_f4", (0xf4, 0x90), ""),
]:
    case(f"errors/utf8_decode_{_name}", "{{ (" + _bs(*_seq) + ").decode() }}")

# One error is one replacement character, however many bytes it covered, and
# one skipped run for ignore. A run of bad start bytes is several errors; a
# truncated sequence is one.
for _name, _seq in [
    ("one_bad_byte", (0xff,)),
    ("run_of_bad_bytes", (0x80, 0x80)),
    ("truncated_pair", (0xf0, 0x9f)),
    ("truncated_triple", (0xf0, 0x9f, 0x92)),
    ("surrogate_three_errors", (0xed, 0xa0, 0x80)),
    ("above_max_four_errors", (0xf5, 0x80, 0x80, 0x80)),
    ("interrupted_text", (0x61, 0xff, 0x62)),
    ("lead_then_ascii", (0xf0, 0x28, 0x8c, 0x28)),
    ("valid_then_truncated", (0xc3, 0xa9, 0xe2, 0x82)),
]:
    _e = _bs(*_seq)
    case(f"methods/utf8_decode_replace_{_name}",
         "{{ (" + _e + ").decode('utf-8','replace')|length }}:"
         "{{ (" + _e + ").decode('utf-8','replace') }}")
    case(f"methods/utf8_decode_ignore_{_name}",
         "{{ (" + _e + ").decode('utf-8','ignore')|length }}:"
         "{{ (" + _e + ").decode('utf-8','ignore') }}")

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
# loop.changed is `self._last_checked_value != value`, a real `!=` on two tuples,
# so a StrictUndefined among the arguments raises instead of answering -- and it
# raises from the *second* call, not the first: the first has only the `missing`
# sentinel to compare against, which is not a tuple, so Python falls back to
# identity and never looks at the elements. gojja2 compared without consulting
# the refusal and answered "TrueTrue" for both.
#
# The one-iteration case is the one that keeps the fix honest: refusing on the
# first call as well would be just as wrong.
case("undefined/strict_loop_changed_first_call",
     "{% for a in [1] %}{{ loop.changed(nope) }}{% endfor %}",
     __settings__={"undefined": "strict"})
case("undefined/strict_loop_changed_two_arguments",
     "{% for a in [1] %}{{ loop.changed(nope, 1) }}{% endfor %}",
     __settings__={"undefined": "strict"})
case("undefined/strict_loop_changed_second_call",
     "{% for a in [1, 2] %}{{ loop.changed(nope) }}{% endfor %}",
     __settings__={"undefined": "strict"})
# Two undefineds compare as unequal here only because the comparison never
# happens; with a defined value it is a real comparison and the repeats show.
case("loops/loop_changed_repeats", "{% for a in [1, 1, 2] %}{{ loop.changed(a) }}{% endfor %}")
case("loops/loop_changed_no_arguments", "{% for a in [1, 2] %}{{ loop.changed() }}{% endfor %}")
case("errors/loop_call_missing", "{% for i in [1,2] %}{{ loop() }}{% endfor %}")
case("errors/loop_call_too_many", "{% for i in [1,2] %}{{ loop(i,i) }}{% endfor %}")
case("errors/loop_call_not_recursive", "{% for i in [1,2] %}{{ loop(i) }}{% endfor %}")
case("errors/block_call_arity", "{% block b %}x{% endblock %}{{ self.b(1) }}")
case("errors/block_call_keyword", "{% block b %}x{% endblock %}{{ self.b(a=1) }}")
case("errors/caller_too_many",
     "{% macro m() %}{{ caller(1) }}{% endmacro %}{% call m() %}x{% endcall %}")
case("errors/caller_keyword",
     "{% macro m() %}{{ caller(a=1) }}{% endmacro %}{% call m() %}x{% endcall %}")

# --- the three special parameters, and the word "undeclared" ------------------
# A macro gets `caller`, `kwargs` or `varargs` only when its *body* reads one
# without binding it first, and only when it has not declared a parameter of
# that name itself. jinja2 spells both halves in macro_body: the refusal for a
# `caller` parameter without a default sits under `if "caller" in undeclared`,
# and skip_special_params keeps a declared `kwargs` or `varargs` from being
# bound over. gojja2 had the first half for the runtime flags and neither for
# the rest, so it refused `{% macro m(caller) %}x{% endmacro %}` -- which jinja2
# renders -- ignored a `caller` parameter's default, and handed a macro that
# declared `kwargs` an empty dict instead of the argument it was passed.
for _n, _src in [
    ("caller_param_unused", "{% macro m(caller) %}x{% endmacro %}[{{ m(1) }}]"),
    ("caller_param_unused_after_another",
     "{% macro m(a, caller) %}{{ a }}{% endmacro %}[{{ m(1, 2) }}]"),
    ("caller_param_defaulted", "{% macro m(caller=1) %}{{ caller }}{% endmacro %}[{{ m() }}]"),
    ("caller_param_defaulted_given",
     "{% macro m(caller=1) %}{{ caller }}{% endmacro %}[{{ m(2) }}]"),
    ("caller_param_defaulted_attribute",
     "{% macro m(caller=1) %}{{ caller }}{% endmacro %}[{{ m.caller }}]"),
    ("caller_param_defaulted_in_a_call_block",
     "{% macro m(caller=1) %}[{{ caller() }}]{% endmacro %}{% call m() %}C{% endcall %}"),
    ("caller_param_stored_first",
     "{% macro m(caller) %}{% set caller = 1 %}{{ caller }}{% endmacro %}[{{ m(3) }}]"),
    ("caller_param_is_a_loop_target",
     "{% macro m(caller) %}{% for caller in [1] %}{{ caller }}{% endfor %}{% endmacro %}[{{ m(3) }}]"),
    ("caller_param_in_a_call_blocks_signature",
     "{% macro m() %}{{ caller(5) }}{% endmacro %}{% call(caller) m() %}x{% endcall %}"),
    ("kwargs_param_declared", "{% macro m(kwargs) %}{{ kwargs }}{% endmacro %}[{{ m(1) }}]"),
    ("kwargs_param_declared_attribute",
     "{% macro m(kwargs) %}{{ kwargs }}{% endmacro %}[{{ m.catch_kwargs }}]"),
    ("varargs_param_declared", "{% macro m(varargs) %}{{ varargs }}{% endmacro %}[{{ m(1) }}]"),
    ("varargs_param_declared_attribute",
     "{% macro m(varargs) %}{{ varargs }}{% endmacro %}[{{ m.catch_varargs }}]"),
    ("kwargs_param_declared_and_extra_passed",
     "{% macro m(kwargs) %}{{ kwargs }}{% endmacro %}[{{ m(1, x=2) }}]"),
    ("varargs_param_declared_and_extra_passed",
     "{% macro m(varargs) %}{{ varargs }}{% endmacro %}[{{ m(1, 2) }}]"),
    ("kwargs_param_declared_but_unused",
     "{% macro m(kwargs) %}x{% endmacro %}[{{ m(1) }}]"),
]:
    case("macros/" + _n, _src)

# The search that decides all of it is jinja2's find_undeclared, and what it
# walks is not what a reader would guess: it stops at a `{% block %}` and at
# nothing else -- not at a nested macro -- and a *parameter* anywhere in what it
# walks takes that name out of the search for good, while a default that reads
# one puts it in. So a `{% call(kwargs) %}` block inside a macro settles
# `kwargs` for the macro around it, and `{% call(p=kwargs) %}` does the
# opposite. gojja2 walked a call block's body and not its signature, so the
# enclosing macro swallowed keywords jinja2 refuses. Found by the fuzzer.
_TAKES = "{% macro takes() %}<{{ caller(1) }}>{% endmacro %}"
for _n, _src in [
    ("call_block_param_settles_kwargs",
     "{% macro m(x) %}{% call(kwargs) takes() %}{{ kwargs }}{% endcall %}{% endmacro %}"
     "[{{ m(1, **{'b': 2}) }}]"),
    ("call_block_param_settles_varargs",
     "{% macro m(x) %}{% call(varargs) takes() %}{{ varargs }}{% endcall %}{% endmacro %}"
     "[{{ m(1, 2) }}]"),
    ("call_block_param_settles_caller",
     "{% macro m(x) %}{% call(caller) takes() %}x{% endcall %}{% endmacro %}"
     "[{{ m(1) }}]{% call m(1) %}C{% endcall %}"),
    ("a_read_before_the_call_block_wins",
     "{% macro m(x) %}{{ kwargs }}{% call(kwargs) takes() %}{% endcall %}{% endmacro %}"
     "[{{ m(1, **{'b': 2}) }}]"),
    ("a_call_blocks_default_reads_kwargs",
     "{% macro m(x) %}{% call(p=kwargs) takes() %}x{% endcall %}{% endmacro %}"
     "[{{ m(1, **{'b': 2}) }}]"),
    ("a_nested_macros_param_settles_it",
     "{% macro m(x) %}{% macro inner(kwargs) %}{{ kwargs }}{% endmacro %}{{ inner(9) }}"
     "{% endmacro %}[{{ m(1, **{'b': 2}) }}]"),
    ("a_block_body_is_not_searched",
     "{% macro m(x) %}{% block bb %}{{ kwargs }}{% endblock %}{% endmacro %}"
     "[{{ m(1, **{'b': 2}) }}]"),
    ("a_loop_target_settles_it",
     "{% macro m(x) %}{% for kwargs in [1] %}{{ kwargs }}{% endfor %}{% endmacro %}"
     "[{{ m(1, **{'b': 2}) }}]"),
]:
    case("macros/undeclared_" + _n, _TAKES + _src)

case("errors/caller_param_used_without_a_default",
     "{% macro m(caller) %}{{ caller }}{% endmacro %}x")
case("errors/caller_param_used_before_a_default",
     "{% macro m(caller, a=1) %}{{ caller }}{% endmacro %}x")
case("errors/caller_param_used_after_another",
     "{% macro m(a, caller) %}{{ caller }}{% endmacro %}x")
case("errors/caller_param_called_without_a_default",
     "{% macro m(caller) %}{{ caller() }}{% endmacro %}x")
case("errors/caller_param_used_in_a_call_blocks_signature",
     "{% macro m() %}{{ caller(5) }}{% endmacro %}{% call(caller) m() %}{{ caller }}{% endcall %}")

# --- a keyword written twice in one call --------------------------------------
# jinja2 writes a call's keywords out as `name=value` pairs, so CPython's own
# compiler refuses the *generated module*: `{{ m(a=1, a=2) }}` does not compile
# there. gojja2 bound the first and swept the second into kwargs, which is the
# direction of divergence that costs a template author something -- the template
# worked here and failed under jinja2. It is refused now, with CPython's wording
# and the line of the template rather than of the module, so these are listed in
# known_failures.txt beside the repeated *parameter* name, for the same reason.
case("errors/keyword_repeated_in_a_macro_call",
     "{% macro m(a=1) %}{{ a }}{% endmacro %}{{ m(a=2, a=3) }}")
case("errors/keyword_repeated_in_a_filter", "{{ lst|join(d='-', d='+') }}", lst=[1, 2])
case("errors/keyword_repeated_in_a_test",
     "{{ n3 is divisibleby(num=2, num=3) }}", n3=3)
case("errors/keyword_repeated_in_a_global", "{{ dict(a=1, a=2) }}")
case("errors/keyword_repeated_in_a_method", "{{ s.split(sep='a', sep='b') }}", s="ab")
case("errors/keyword_repeated_in_a_call_block",
     "{% macro m(a=1) %}{{ a }}{{ caller() }}{% endmacro %}"
     "{% call m(a=2, a=3) %}c{% endcall %}")
case("errors/keyword_repeated_among_three",
     "{% macro m(a=1, b=2) %}{{ a }}{% endmacro %}{{ m(b=1, a=2, b=3) }}")
case("errors/keyword_repeated_in_a_dead_branch",
     "{% macro m(a=1) %}{{ a }}{% endmacro %}{% if false %}{{ m(a=2, a=3) }}{% endif %}ok")

# ...unless the node folds first, and then it never reaches the generator at
# all: jinja2's `as_const` collects the keywords through a dict comprehension,
# where a repeated name quietly keeps the last. So the same filter over a
# literal renders, and over a name does not compile. These are exact matches,
# not known failures, and they are the half that says the refusal is placed in
# the right phase rather than in the parser.
case("fold/keyword_repeated_but_folded", "{{ [1]|join(d='-', d='+') }}")
case("fold/keyword_repeated_in_a_folded_assignment",
     "{% set v = [1]|join(d='-', d='+') %}[{{ v }}]")
case("fold/keyword_repeated_below_extends",
     "{% extends 'base.txt' %}{% macro m(a=1) %}{{ a }}{% endmacro %}{{ m(a=2, a=3) }}",
     __templates__={"base.txt": "B"})
case("fold/order_a_lookup_beats_a_repeated_keyword",
     "{% macro m(a=1) %}{{ a }}{% endmacro %}{{ m(a=2, a=3) }}{{ 1|nosuchA }}")
case("fold/order_a_later_fold_beats_a_repeated_keyword",
     "{% macro m(a=1) %}{{ a }}{% endmacro %}{{ m(a=2, a=3) }}"
     "{{ (0 ** 0)[7] and 0 }}", __settings__={"undefined": "strict"})
case("fold/order_a_block_twice_beats_a_repeated_keyword",
     "{% macro m(a=1) %}{{ a }}{% endmacro %}{% block b %}{% endblock %}"
     "{% block b %}{% endblock %}{{ m(a=2, a=3) }}")
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

# bytes.hex asks four questions about its separator, in an order that matters.
# CPython *measures* it before looking at its type, so an int fails as something
# with no length rather than as a wrong type; only a value exactly one long is
# asked what it is; and only a str or bytes gets as far as the ASCII check.
#
# An audit of error sites no corpus case reaches found this: gojja2 asked the
# type alone, which got every wording wrong, silently accepted a separator of
# any length, and rejected a bytes while saying `sep must be str or bytes, not
# bytes`.
for _n, _src in [
    ("no_len_int", "{{ b.hex(1) }}"),
    ("no_len_float", "{{ b.hex(1.5) }}"),
    ("no_len_none", "{{ b.hex(none) }}"),
    ("length_zero_list", "{{ b.hex([]) }}"),
    ("length_zero_str", "{{ b.hex('') }}"),
    ("length_two_str", "{{ b.hex('--') }}"),
    ("length_zero_bytes", "{{ b.hex(''.encode()) }}"),
    ("not_str_or_bytes_list", "{{ b.hex(['x']) }}"),
    ("not_str_or_bytes_dict", "{{ b.hex({'a': 1}) }}"),
    ("not_str_or_bytes_range", "{{ b.hex(range(1)) }}"),
    ("not_ascii_str", "{{ b.hex('\u00e9') }}"),
    ("not_ascii_bytes", "{{ b.hex((255).to_bytes(1, 'big')) }}"),
    ("bytes_separator", "{{ b.hex('-'.encode()) }}"),
    ("str_separator", "{{ b.hex('-') }}"),
    ("separator_keyword", "{{ b.hex(sep='-') }}"),
    ("grouped_from_the_right", "{{ 'abcdef'.encode().hex('_', 2) }}"),
    ("grouped_from_the_left", "{{ 'abcdef'.encode().hex('_', -2) }}"),
    ("group_of_zero", "{{ b.hex('-', 0) }}"),
    ("group_not_an_integer", "{{ b.hex('-', 'x') }}"),
    ("empty_receiver", "{{ ''.encode().hex('-') }}"),
    ("empty_receiver_bad_sep", "{{ ''.encode().hex(1) }}"),
    ("no_separator", "{{ b.hex() }}"),
]:
    case(f"bytes/hex_{_n}", "{% set b = 'ab'.encode() %}" + _src)

# |xmlattr refuses a key that could close the attribute and open another. The
# same audit found the message reversed -- gojja2 said `Invalid character 'a b'
# in attribute name.` where jinja2 says `Invalid character in attribute name:
# 'a b'` -- and nothing anywhere had ever compared it.
#
# jinja2 matches [\s/>=] with re.ASCII, so the set is exactly these six plus
# ASCII whitespace: a non-breaking space is a legal attribute name.
for _n, _src in [
    ("space", "{{ {'a b': 1}|xmlattr }}"),
    ("slash", "{{ {'a/b': 1}|xmlattr }}"),
    ("gt", "{{ {'a>b': 1}|xmlattr }}"),
    ("equals", "{{ {'a=b': 1}|xmlattr }}"),
    ("tab", "{{ {'a\tb': 1}|xmlattr }}"),
    ("newline", "{{ {'a\nb': 1}|xmlattr }}"),
    ("nbsp_is_allowed", "{{ {'a\u00a0b': 1}|xmlattr }}"),
    ("lt_is_allowed", "{{ {'a<b': 1}|xmlattr }}"),
    ("quote_is_allowed", "{{ {\"a'b\": 1}|xmlattr }}"),
    ("empty_key", "{{ {'': 1}|xmlattr }}"),
    ("later_key", "{{ {'a': 1, 'b c': 2}|xmlattr }}"),
    ("skipped_none_then_bad", "{{ {'a': none, 'b c': 2}|xmlattr }}"),
]:
    case(f"filters/xmlattr_key_{_n}", _src)

# Nine numeric methods take no arguments at all, and none of them checked: gojja2
# answered `{{ n.bit_length(1) }}` as 1 where CPython refuses the call. Found
# while probing the neighbours of the ungraded error sites -- a *missing* message
# is invisible to that audit, which only sees the ones that exist.
#
# CPython names the receiver's own type rather than where the method was defined,
# so a bool says bool.bit_length(), and a keyword beats a count.
for _recv, _meths in [("n", ["conjugate", "is_integer", "bit_length",
                             "bit_count", "as_integer_ratio"]),
                      ("f", ["conjugate", "is_integer", "hex",
                             "as_integer_ratio"]),
                      ("yes", ["bit_length", "conjugate"])]:
    for _m in _meths:
        case(f"methods/noargs_{_recv}_{_m}",
             "{{ %s.%s() }}|{{ %s.%s(1) }}" % (_recv, _m, _recv, _m),
             n=3, f=1.5, yes=True)
for _recv, _m in [("n", "bit_length"), ("f", "hex"), ("yes", "conjugate")]:
    case(f"methods/noargs_{_recv}_{_m}_keyword",
         "{{ %s.%s(x=1) }}" % (_recv, _m), n=3, f=1.5, yes=True)
    case(f"methods/noargs_{_recv}_{_m}_keyword_beats_count",
         "{{ %s.%s(1, x=2) }}" % (_recv, _m), n=3, f=1.5, yes=True)
    case(f"methods/noargs_{_recv}_{_m}_two",
         "{{ %s.%s(1, 2) }}" % (_recv, _m), n=3, f=1.5, yes=True)

# bytes.fromhex is fussier than it looks. Whitespace separates byte pairs and may
# not sit inside one, every ASCII space counts and no other does, and the
# reported position is the offending character's -- with a pair cut short by the
# end of the string reported at the end rather than at the digit that began it.
# Positions are code points, because CPython counts them in a str.
#
# gojja2 skipped only " " and reported the start of the pair for every failure,
# which is right only when the first digit is the bad one. Found by the audit of
# error sites no corpus case reaches.
for _n, _arg in [
    ("short_pair", "'a'"),
    ("space_inside_a_pair", "'6 1'"),
    ("bad_first_digit", "'zz'"),
    ("bad_second_digit", "'6z'"),
    ("bad_later_digit", "'661z'"),
    ("surrounding_spaces", "'  61  '"),
    ("underscore", "'6_1'"),
    ("ends_after_a_digit", "'61 6'"),
    ("one_digit", "'6'"),
    ("empty", "''"),
    ("bad_in_the_middle", "'61z1'"),
    ("tab_separates", "'\t61'"),
    ("newline_separates", "'\n61'"),
    ("trailing_tab", "'61\t'"),
    ("tab_inside_a_pair", "'6\t1'"),
    ("return_separates", "'\r61'"),
    ("trailing_space_in_a_pair", "'6 '"),
    ("only_spaces", "'  '"),
    ("two_spaces_between", "'61  61'"),
    ("two_spaces_inside", "'6  1'"),
    ("nbsp_is_not_a_space", "'\u00a061'"),
    ("position_counts_code_points", "'61\u00e9'"),
    ("non_ascii_first", "'\u00e961'"),
    ("non_ascii_second", "'6\u00e9'"),
    ("non_ascii_after_a_space", "'61 \u00e9'"),
    ("wide_non_ascii", "'\u4e0061'"),
    ("non_ascii_in_the_middle", "'61\u00e91'"),
    ("not_a_string", "1"),
    ("valid", "'61'"),
    # 3.14 widened the argument to anything bytes-like and reworded the
    # refusal, and it replaced the position message with a digit count --
    # but only for a pair cut short by the end of the input. A pair spoiled
    # by a character still reports that character, on every version. The
    # version matrix caught all three while this branch was being gated.
    ("not_a_string_none", "none"),
    ("not_a_string_list", "[]"),
    ("bytes_argument", "'61'.encode()"),
    ("bytes_argument_bad_digit", "'6z'.encode()"),
    ("bytes_argument_short", "'a'.encode()"),
    ("bytes_argument_spaced", "'61 61'.encode()"),
    ("bytes_argument_empty", "''.encode()"),
]:
    case(f"bytes/fromhex_{_n}", "{{ ''.encode().fromhex(%s) }}" % _arg)

# list.sort(key=...) calls the key once per element, which is the first thing in
# gojja2 that runs template code from inside a method. gojja2 refused every key
# with `sort() key must be None`, so none of this was reachable.
#
# Two halves of CPython's implementation become observable with a key that can
# run: it takes the elements *out* of the list for the duration, leaving it
# empty, and refuses to put them back if anything touched it meanwhile.
_K = "{% macro k(v) %}{{ 9 - v }}{% endmacro %}"
for _n, _src in [
    # A key that is not callable is refused by the *call*, so an empty list
    # never notices.
    ("empty_list_never_calls", "{{ e.sort(key=1) }}|{{ e }}"),
    ("not_callable_int", "{{ one.sort(key=1) }}"),
    ("not_callable_str", "{{ lst.sort(key='x') }}"),
    ("not_callable_list", "{{ lst.sort(key=[]) }}"),
    ("key_none_is_the_default", "{{ lst.sort(key=none) }}{{ lst }}"),
    ("bad_keyword_beats_the_key", "{{ lst.sort(key=1, zz=2) }}"),
    ("bad_keyword_first", "{{ lst.sort(zz=2, key=1) }}"),
    ("positional_is_refused", "{{ lst.sort(1) }}"),
    # A macro key, which is the reachable callable.
    ("macro_key", _K + "{{ lst.sort(key=k) }}{{ lst }}"),
    ("macro_key_reversed", _K + "{{ lst.sort(key=k, reverse=true) }}{{ lst }}"),
    ("macro_key_wrong_arity", "{% macro k() %}x{% endmacro %}{{ lst.sort(key=k) }}"),
    ("macro_key_empty_body",
     "{% macro k(v) %}{% endmacro %}{{ lst.sort(key=k) }}{{ lst }}"),
    ("macro_key_is_stable",
     "{% macro k(v) %}{{ v|int }}{% endmacro %}{{ words.sort(key=k) }}{{ words }}"),
    ("class_global_key", "{{ lst.sort(key=namespace) }}"),
    ("function_global_key", "{{ lst.sort(key=lipsum) }}"),
    ("joiner_key", "{{ lst.sort(key=joiner()) }}"),
    # The list is empty while the sort runs, and modifying it is refused.
    ("key_appends", "{% macro k(v) %}{{ lst.append(9) }}{{ v }}{% endmacro %}"
                    "{{ lst.sort(key=k) }}{{ lst }}"),
    ("key_pops", "{% macro k(v) %}{{ lst.pop() }}{{ v }}{% endmacro %}"
                 "{{ lst.sort(key=k) }}"),
    ("key_clears", "{% macro k(v) %}{{ lst.clear() }}{{ v }}{% endmacro %}"
                   "{{ lst.sort(key=k) }}{{ lst }}"),
    ("key_sees_an_empty_list",
     "{% macro k(v) %}{{ lst }}{% endmacro %}{{ lst.sort(key=k) }}{{ lst }}"),
    ("key_measures_an_empty_list",
     "{% macro k(v) %}{{ lst|length }}{% endmacro %}{{ lst.sort(key=k) }}{{ lst }}"),
    ("key_sorts_the_same_list",
     "{% macro k(v) %}{{ lst.sort(key=k) }}{% endmacro %}{{ lst.sort(key=k) }}"),
    ("key_touches_another_list",
     "{% macro k(v) %}{{ lst.append(9) }}{% endmacro %}{{ e.sort(key=k) }}{{ lst }}"),
    # A key that raises leaves the elements where they were.
    ("key_raises", "{% macro k(v) %}{{ v.nosuch.deeper }}{% endmacro %}"
                   "{{ lst.sort(key=k) }}"),
]:
    case(f"methods/sort_key_{_n}", _src,
         e=[], one=[5], lst=[3, 1, 2], words=["b", "a", "c"])

# The select/reject family's refusals. selectattr and rejectattr share one
# message that names neither the filter nor the position, because both reach it
# through jinja2's prepare_attribute_parts; gojja2 had written its own wording
# for it, naming selectattr in rejectattr's error too.
#
# These also grade the error *class*, which a side-by-side probe of rendered
# strings does not: the golden carries the type, so FilterArgumentError and
# TemplateRuntimeError are distinguished here and nowhere else.
for _n, _src in [
    ("selectattr_no_attribute", "{{ lst|selectattr()|list }}"),
    ("rejectattr_no_attribute", "{{ lst|rejectattr()|list }}"),
    ("map_no_filter", "{{ lst|map()|list }}"),
    ("select_no_test_is_truthiness", "{{ lst|select()|list }}"),
    ("reject_no_test_is_truthiness", "{{ lst|reject()|list }}"),
    ("select_unknown_test", "{{ lst|select('nosuchtest')|list }}"),
    ("selectattr_unknown_test", "{{ pairs|selectattr('x','nosuchtest')|list }}"),
    ("map_unknown_filter", "{{ lst|map('nosuchfilter')|list }}"),
    ("groupby_no_attribute", "{{ lst|groupby()|list }}"),
]:
    case(f"filters/seqarg_{_n}", _src, lst=[1, 0], pairs=[{"x": 1}])

# `{{ nope.__class__(...) }}` builds another undefined, and jinja2's
# Undefined(hint, obj, name, exc) is observable in three ways: a *truthy* hint is
# the whole message, with no obj the name stands alone, and with an obj a string
# name is an attribute while anything else is an element. The name is written
# with !r in the error and raw in DebugUndefined's __str__, so the same value is
# "'zz' is undefined" and "{{ zz }}".
#
# gojja2 ignored all four arguments and produced a nameless undefined that said
# "'' is undefined" where jinja2 says "None is undefined". The render
# differential found it within twenty thousand templates of the generator
# learning to call a class object.
_U = [
    ("default_is_none", "{{ nope.__class__() + 1 }}"),
    ("prints_empty", "[{{ nope.__class__() }}]"),
    ("name", "{{ nope.__class__(name='zz') + 1 }}"),
    ("name_prints_raw", "[{{ nope.__class__(name='zz') }}]"),
    ("name_is_not_a_string", "{{ nope.__class__(name=3) + 1 }}"),
    ("name_is_none", "{{ nope.__class__(name=none) + 1 }}"),
    ("hint", "{{ nope.__class__(hint='h') + 1 }}"),
    ("hint_prints", "[{{ nope.__class__(hint='h') }}]"),
    ("hint_is_not_a_string", "{{ nope.__class__(hint=1) + 1 }}"),
    # jinja2 tests the hint for *truth*, not for presence, so every falsy one
    # falls through to the name form. A plant that checked presence instead was
    # caught by nothing until these were written: the four classes of hint above
    # are all either absent or truthy.
    ("falsy_hint_is_ignored", "{{ nope.__class__(hint=none) + 1 }}"),
    ("empty_hint_is_ignored", "{{ nope.__class__(hint='') + 1 }}"),
    ("zero_hint_is_ignored", "{{ nope.__class__(hint=0) + 1 }}"),
    ("empty_list_hint_is_ignored", "{{ nope.__class__(hint=[]) + 1 }}"),
    ("empty_hint_falls_to_the_name",
     "{{ nope.__class__(hint='', name='zz') + 1 }}"),
    ("zero_hint_falls_to_the_attribute",
     "{{ nope.__class__(hint=0, obj=1, name='k') + 1 }}"),
    ("positional", "{{ nope.__class__(1, 2, 3) + 1 }}"),
    ("obj_and_string_name", "{{ nope.__class__(obj=1, name='k') + 1 }}"),
    ("obj_and_other_name", "{{ nope.__class__(obj=1, name=3) + 1 }}"),
    ("obj_without_a_name", "{{ nope.__class__(obj=1) + 1 }}"),
    ("none_obj", "{{ nope.__class__(obj=none, name='k') + 1 }}"),
    ("is_still_undefined", "{{ nope.__class__() is defined }}"),
    ("walks_as_empty", "{{ nope.__class__()|list }}"),
    ("arity", "{{ nope.__class__(1, 2, 3, 4, 5) }}"),
    ("unexpected_keyword", "{{ nope.__class__(zz=1) }}"),
    ("duplicate_argument", "{{ nope.__class__(1, hint=2) }}"),
    # The fourth argument is the class the undefined *raises* with: jinja2 does
    # `raise exc(message)`, so a non-callable one fails before the message is
    # ever used. Found by the render differential on a fresh seed, after this
    # was implemented and its commit message claimed the fourth argument
    # changed nothing.
    ("exc_not_callable", "{{ -nope.__class__(1, 2, 3, 4) }}"),
    ("exc_by_keyword", "{{ -nope.__class__(exc=1) }}"),
    ("exc_none", "{{ -nope.__class__(1, 2, 3, none) }}"),
    ("exc_str", "{{ -nope.__class__(1, 2, 3, 's') }}"),
    ("exc_list", "{{ -nope.__class__(1, 2, 3, [1]) }}"),
    ("exc_with_a_name", "{{ -nope.__class__(name='zz', exc=4) }}"),
    ("exc_alone", "{{ -nope.__class__(exc=4) }}"),
    # It is only reached when the undefined is *used*: printing one is still
    # the empty string, and it is still undefined.
    ("exc_is_not_reached_by_printing", "[{{ nope.__class__(1, 2, 3, 4) }}]"),
    ("exc_is_not_reached_by_list", "{{ nope.__class__(1, 2, 3, 4)|list }}"),
    ("exc_is_still_undefined", "{{ nope.__class__(1, 2, 3, 4) is defined }}"),
]
# Under every setting, because the class that was called decides how the result
# behaves: one built from a StrictUndefined refuses just as it does.
for _kind in ("default", "strict", "debug", "chainable"):
    for _n, _src in _U:
        case(f"undefined/construct_{_kind}_{_n}", _src,
             __settings__={"undefined": _kind})

# markupsafe defines __html__ as an ordinary method, so the Markup *class object*
# holds it unbound: hasattr says yes -- which is what `is escaped` asks -- and
# calling it with no self raises. Every place that would escape the value fails
# rather than escaping it, and every place that only stringifies it is its repr.
#
# Found by the render differential once the generator could write a class object
# under autoescape. No other class carries __html__, which is why the int class
# beside each of these escapes normally.
_MK = "{% set M = ('x'|safe).__class__ %}"
for _n, _src in [
    ("is_escaped", "{{ M is escaped }}"),
    ("safe", "{{ M|safe }}"),
    ("escape", "{{ M|e }}"),
    ("printed", "{{ M }}"),
    ("string", "{{ M|string }}"),
    ("through_a_set", "{% set x = M %}{{ x }}"),
    ("forceescape", "{{ M|forceescape }}"),
    ("inside_a_list", "{{ [M] }}"),
    ("concatenated", "{{ M ~ 'a' }}"),
    ("name", "{{ M.__name__ }}"),
]:
    for _ae, _wrap in [("", "%s"), ("_autoescape",
                                    "{%% autoescape true %%}%s{%% endautoescape %%}")]:
        case(f"classes/markup_html_{_n}{_ae}", _wrap % (_MK + _src))
for _n, _src in [
    ("int_class_is_not_escaped", "{{ n.__class__ is escaped }}"),
    ("int_class_escapes", "{% autoescape true %}{{ n.__class__ }}{% endautoescape %}"),
    ("int_class_through_e", "{{ n.__class__|e }}"),
    ("int_class_forceescape", "{{ n.__class__|forceescape }}"),
]:
    case(f"classes/markup_html_{_n}", _src, n=1)

# |forceescape asks for __html__ and escapes str() of the answer, so a value
# carrying its own escaped form has that form escaped again -- which is what
# makes it differ from |e for a module.
case("filters/forceescape_a_module",
     "{% import 'body.html' as m %}{{ m|forceescape }}|{{ m|e }}|{{ m|string }}",
     __templates__={"body.html": "<b>x</b>"})

# A type object carries its class's methods unbound: `dict.items` is a value,
# `dict.items(d)` is `d.items()`, and everything past the first argument is the
# method's own -- so an arity error comes from the method rather than from the
# descriptor. These were a documented divergence until the descriptor was
# implemented; the line now sits at __mro__ and __subclasses__ alone, which lead
# back into the interpreter where a method call on a value does not.
for _n, _src in [
    ("dict_items", "{{ d.__class__.items() }}"),
    ("dict_items_through_dictsort", "{{ d.__class__|dictsort }}"),
    ("dict_items_through_xmlattr", "{{ d.__class__|xmlattr }}"),
    ("no_items_through_dictsort", "{{ s.__class__|dictsort }}"),
    ("no_items_through_xmlattr", "{{ lst.__class__|xmlattr }}"),
    ("str_upper_with_self", "{{ s.__class__.upper('a') }}"),
    ("str_upper_without_self", "{{ s.__class__.upper() }}"),
    ("list_append_with_self", "{{ lst.__class__.append([], 1) }}"),
    ("method_is_a_value", "{{ d.__class__.items }}"),
    ("method_is_defined", "{{ s.__class__.upper is defined }}"),
    ("method_prints", "{{ s.__class__.upper|string }}"),
    ("wrong_self_type", "{{ d.__class__.items(lst) }}"),
    ("wrong_self_type_number", "{{ n.__class__.bit_length('x') }}"),
    ("arity_comes_from_the_method", "{{ s.__class__.upper('a','b') }}"),
    ("with_extra_arguments", "{{ s.__class__.replace('abc', 'a', 'z') }}"),
    ("dict_get", "{{ d.__class__.get({'a': 1}, 'a') }}"),
    ("dict_keys", "{{ d.__class__.keys(d) }}"),
    ("list_count", "{{ lst.__class__.count([1,1], 1) }}"),
    ("float_is_integer", "{{ f.__class__.is_integer(1.5) }}"),
    ("unknown_name_is_undefined", "{{ s.__class__.nosuchmethod }}"),
    ("shared_method_wrong_self", "{{ lst.__class__.count((1,2), 1) }}"),
    ("shared_method_wrong_self_index", "{{ lst.__class__.index((1,2), 1) }}"),
    ("shared_method_wrong_self_reversed",
     "{% set t = (1,2) %}{{ t.__class__.count(lst, 1) }}"),
    # bool defines no methods of its own, so every one it answers is int's and
    # names int -- and an int is then an acceptable receiver, and a bool for a
    # descriptor reached through int.
    ("bool_method_belongs_to_int", "{{ yes.__class__.conjugate }}"),
    ("bool_method_takes_a_bool", "{{ yes.__class__.bit_length(yes) }}"),
    ("bool_method_takes_an_int", "{{ yes.__class__.bit_length(n) }}"),
    ("int_method_takes_a_bool", "{{ n.__class__.bit_length(yes) }}"),
    ("bool_method_refuses_a_float", "{{ yes.__class__.bit_length(f) }}"),
    # classmethod and staticmethod take no instance, so these are the same
    # callable an instance answers rather than a descriptor: read as one,
    # str.maketrans ate its source string and complained about what was left.
    ("classmethod_bytes_fromhex", "{{ ('ab'.encode()).__class__.fromhex('0102') }}"),
    ("classmethod_float_fromhex", "{{ f.__class__.fromhex('0x1.8p+0') }}"),
    ("classmethod_dict_fromkeys", "{{ d.__class__.fromkeys(lst) }}"),
    ("classmethod_int_from_bytes", "{{ n.__class__.from_bytes('ab'.encode()) }}"),
    ("staticmethod_str_maketrans", "{{ s.__class__.maketrans('a','b') }}"),
    ("classmethod_arity", "{{ d.__class__.fromkeys() }}"),
]:
    case(f"classes/unbound_{_n}", _src, d={"a": 1}, s="x", lst=[1], n=1, f=1.5)

# A class's *dunders* are reached the same way and are not implemented: gojja2
# exposes no `__len__` or `__abs__` on a value, so the type object has none to
# hand out either and the name is undefined. CPython answers a slot wrapper,
# whose refusal is worded differently from a method descriptor's ("requires a
# 'int' object but received a 'list'", not "doesn't apply to"). Listed in
# known_failures.txt. Pinned so the wording gojja2 does give is graded rather
# than drifting, and so the day it is implemented these say what to.
for _n, _src in [
    ("len_is_a_value", "{{ s.__class__.__len__ }}"),
    ("len_with_self", "{{ s.__class__.__len__(s) }}"),
    ("abs_with_self", "{{ n.__class__.__abs__(n) }}"),
    ("abs_wrong_self", "{{ n.__class__.__abs__(lst) }}"),
]:
    case(f"divergence/class_unbound_dunder_{_n}", _src,
         d={"a": 1}, s="x", lst=[1], n=1, f=1.5)

# bool's descriptors are int's, and so are their arity messages: CPython says
# "int.conjugate()" where the same method reached through the *value* says
# "bool.conjugate()". gojja2's descriptor delegates to the receiver's own bound
# method, which words it after the receiver either way. Listed in
# known_failures.txt; pinned so the wording it does give is graded.
case("divergence/class_unbound_bool_arity_names_int",
     "{{ yes.__class__.conjugate(yes, 1) }}", yes=True)

# The classes that genuinely lack the method agree exactly, which is what makes
# the above a gap rather than a wholly separate model.
for _n, _src in [
    ("int_has_no_items", "{{ n.__class__|dictsort }}"),
    ("int_has_no_items_xmlattr", "{{ n.__class__|xmlattr }}"),
    ("float_has_no_items", "{{ f.__class__|dictsort }}"),
]:
    case(f"classes/unbound_{_n}", _src, n=1, f=1.5)

# CPython 3.14 names the count in "too many values to unpack", and only for a
# list, a tuple or a dict: everything else is unpacked through the iterator path,
# which does not count, so the message carries no number however long the value
# is. gojja2 applied the version rule to every kind at one site and to none at
# the other -- found by running the render differential against 3.14, which until
# now asked one interpreter only.
for _n, _src in [
    ("list", "{% for a, b in [[1,2,3]] %}{% endfor %}"),
    ("tuple", "{% for a, b in [(1,2,3)] %}{% endfor %}"),
    ("dict", "{% for a, b in [d] %}{% endfor %}"),
    ("str", "{% for a, b in ['abc'] %}{% endfor %}"),
    ("bytes", "{% for a, b in ['abc'.encode()] %}{% endfor %}"),
    ("range", "{% for a, b in [range(3)] %}{% endfor %}"),
    ("dict_keys", "{% for a, b in [d.keys()] %}{% endfor %}"),
    ("dict_items", "{% for a, b in [d.items()] %}{% endfor %}"),
    ("markup", "{% for a, b in ['abc'|safe] %}{% endfor %}"),
    ("sorted_is_a_list", "{% for a, b in [[3,1,2]|sort] %}{% endfor %}"),
    ("split_is_a_list", "{% for a, b in ['a,b,c'.split(',')] %}{% endfor %}"),
    ("through_set", "{% set x, y = [1,2,3] %}"),
    ("through_set_str", "{% set x, y = 'abc' %}"),
    ("too_few_names_the_count_always", "{% for a, b in [[1]] %}{% endfor %}"),
    # The other site: |urlencode unpacks each element itself, and had the rule
    # applied nowhere.
    ("urlencode_list", "{{ [[1,2,3]]|urlencode }}"),
    ("urlencode_dict", "{{ [d]|urlencode }}"),
    ("urlencode_str", "{{ ['abc']|urlencode }}"),
]:
    case(f"errors/unpack_count_{_n}", _src, d={"a": 1, "b": 2, "c": 3})

# A lazy filter's result is a list here and an iterator there, so on 3.14 the
# count follows the type. Listed in known_failures.txt: it is the sequence-filter
# divergence showing through a new surface, not a separate decision.
case("divergence/unpack_count_lazy_filter",
     "{% for a, b in [[1,2,3]|reverse] %}{% endfor %}")

# A *callable* fourth argument to Undefined is not reproduced. jinja2 calls it at
# the raise -- `raise exc(message)` -- with whatever side effects it has, and
# gojja2 has no evaluator at the point an undefined reports itself. Calling it at
# construction instead would run it for an undefined that is never used, which is
# a worse wrong answer than not calling it. Listed in known_failures.txt.
for _n, _src in [
    ("class_global", "{{ -nope.__class__(1, 2, 3, range) }}"),
    ("class_that_refuses", "{{ -nope.__class__(1, 2, 3, dict) }}"),
    ("macro", "{% macro m(x) %}{% endmacro %}{{ -nope.__class__(1, 2, 3, m) }}"),
]:
    case(f"divergence/undefined_exc_callable_{_n}", _src)

# previtem and nextitem are the only undefineds a loop hands out, and they were
# built under the *default* Undefined whatever the environment was set to -- so
# `{{ loop.previtem }}` rendered nothing under DebugUndefined where jinja2 prints
# the hint, and nothing under StrictUndefined where jinja2 raises.
#
# Found by giving the render differential an undefined axis: it varied autoescape
# and nothing else, so sixty thousand templates a run all used the default class.
for _kind in ("default", "strict", "debug", "chainable"):
    for _n, _src in [
        ("previtem", "{% for i in [1,2] %}[{{ loop.previtem }}]{% endfor %}"),
        ("nextitem", "{% for i in [1,2] %}[{{ loop.nextitem }}]{% endfor %}"),
        ("previtem_used", "{% for i in [1,2] %}{{ loop.previtem + 1 }}{% endfor %}"),
        ("nextitem_used", "{% for i in [1,2] %}{{ loop.nextitem + 1 }}{% endfor %}"),
        ("changed_is_not_undefined",
         "{% for i in [1,2] %}[{{ loop.changed(i) }}]{% endfor %}"),
    ]:
        case(f"undefined/loop_{_kind}_{_n}", _src,
             __settings__={"undefined": _kind})

# str.isprintable and repr's escaping answer the same question, and they used to
# answer it two ways: isprintable asked Go's categories directly, repr read the
# table generated from CPython. They disagreed for 9,988 code points -- every one
# a character Go's Unicode release knows about and the pinned CPython does not,
# so `{{ c.isprintable() }}` said true for a character `{{ [c] }}` escaped.
#
# These are drawn from that set. Each pairs the predicate with the repr, because
# the bug was the two disagreeing rather than either one alone.
for _n, _cp in [
    ("arabic_088f", "\u088f"),
    ("arabic_0897", "\u0897"),
    ("telugu_0c5c", "\u0c5c"),
    ("kannada_0cdc", "\u0cdc"),
    ("combining_1acf", "\u1acf"),
    ("assigned_0041", "A"),
    ("space", " "),
    ("control_0000", "\u0000"),
]:
    case(f"unicode/isprintable_agrees_with_repr_{_n}",
         "{{ c.isprintable() }}|{{ [c] }}", c=_cp)

# str() of a container is its repr, and a repr escapes by printability -- which
# moves between releases. The print path reached Repr, which is the pinned
# interpreter's, so `{{ [c] }}` under WithPythonVersion(3.14) printed 3.13's
# answer while `{{ c.isprintable() }}` beside it printed 3.14's. These are
# graded on every interpreter, which is where that shows.
for _n, _cp in [
    ("newly_assigned", "\u0897"),
    ("arabic_088f", "\u088f"),
    ("ascii", "A"),
]:
    case(f"unicode/repr_follows_the_version_{_n}",
         "{{ [c] }}|{{ {1: c} }}|{{ [c]|string }}|{{ c }}", c=_cp)

# A constant folded under StrictUndefined has no cases here, and that is
# structural. jinja2's folder reads a subscript and an attribute through the
# lookup that swallows into an Undefined, so under strict the fold *raises* -- at
# compile time, before the template runs -- where gojja2 abandons the fold and
# lets the render raise the unswallowed error instead.
#
# Every shape of it is a template CPython refuses to compile and gojja2 accepts,
# and the suite requires the two to agree about that: TestSyntaxMatchesTheReference
# says "compiles here but has no reference tree". That is how two shapes which
# looked like they agreed on the message were caught -- the render comparison sees
# only the message, the syntax invariant sees the phase. See docs/divergences.md.

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

# Two complaints the mini-language makes while it is still *reading* the spec,
# so neither names the presentation type or the value's own -- which is what
# tells them apart from the two above.
#
# A comma and an underscore are read one after the other, so a spec carrying
# both says so in one message, in either order and whatever follows. Two of the
# *same* separator is the other complaint: the second is read as the
# presentation type, so `{:,,}` says "Cannot specify ',' with ','." and `{:,_}`
# does not. gojja2 reported the generic "with" form for the mixed pair and let
# `{:,_d}` fall through to "Invalid format specifier".
for _n, _src in [
    ("comma_then_underscore", "{{ '{:,_}'.format(1) }}"),
    ("underscore_then_comma", "{{ '{:_,}'.format(1) }}"),
    ("both_before_a_code", "{{ '{:,_d}'.format(1) }}"),
    ("both_before_a_third", "{{ '{:_,,}'.format(1.5) }}"),
    ("both_on_a_string", "{{ '{:,_}'.format('a') }}"),
    # The same separator twice is the *other* complaint, and it is here so
    # that reading the pair as "both" cannot pass unnoticed.
    ("comma_twice", "{{ '{:,,}'.format(1) }}"),
    ("underscore_twice", "{{ '{:__}'.format(1) }}"),
]:
    case(f"errors/format_separators_{_n}", _src)
# A dot with no digits after it, likewise: `{:.f}` on an int and `{:.>5.}` on a
# str report the same thing, and the digits must be bare -- a sign or a space
# after the dot is this and not a width.
for _n, _src in [
    ("bare_dot", "{{ '{:.}'.format(1) }}"),
    ("dot_then_code", "{{ '{:.f}'.format(1.5) }}"),
    ("dot_after_a_width", "{{ '{:5.}'.format('a') }}"),
    ("dot_is_also_the_fill", "{{ '{:.>5.}'.format(1) }}"),
    ("signed_precision", "{{ '{:.-5f}'.format(1.5) }}"),
    ("spaced_precision", "{{ '{:. 5f}'.format(1.5) }}"),
]:
    case(f"errors/format_precision_{_n}", _src)
# An object with no __format__ of its own never reads the spec, so neither
# complaint reaches it: these are about the type, not the spec.
case("errors/format_unread_spec",
     "{{ '{:,_}'.format(none) }}")
# The grouped and exponent forms the sweep above did not reach: a width that
# the separators have to be counted into, a grouped mantissa, and 'g' choosing
# between the two forms and then trimming.
case("format/grouped_width",
     "{{ '{:15,}'.format(1234567890) }}|{{ '{:015,}'.format(-1234567) }}|"
     "{{ '{:_>15,d}'.format(1234567) }}|{{ '{:015_x}'.format(1234567890) }}")
case("format/grouped_exponent",
     "{{ '{:,e}'.format(123456.789) }}|{{ '{:012,.3e}'.format(1e20) }}|"
     "{{ '{:,g}'.format(1e20) }}|{{ '{:015.4g}'.format(123456.789) }}")
case("format/general_form_picks_a_shape",
     "{{ '{:g}'.format(0.0001) }}|{{ '{:g}'.format(1e-20) }}|{{ '{:G}'.format(1e20) }}|"
     "{{ '{:.30g}'.format(1.5) }}|{{ '{:#g}'.format(0.0) }}|{{ '{:g}'.format(-0.0) }}")
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

# ...and what it is handed is *asked*, not type-checked. `f(**x)` makes Python
# look for `x.keys`, which every Undefined class refuses -- the chainable one
# answers itself and then refuses the call -- so `{{ m(**nope) }}` is "'nope' is
# undefined" under all four, where gojja2 said "argument after ** must be a
# mapping, not Undefined". `*x` is *iterated* instead, and there the classes
# differ: three of them yield nothing and the call goes ahead with no extra
# arguments, and only StrictUndefined raises. Both sites were deciding by type
# where jinja2 lets the value answer; the ** half was wrong for every class.
for _n, _src in [
    ("star_undefined", "{% macro mm(a=0) %}[{{ a }}]{% endmacro %}{{ mm(*nope) }}"),
    ("star_undefined_attribute",
     "{% macro mm(a=0) %}[{{ a }}]{% endmacro %}{{ mm(*d.missing) }}"),
    ("star_kwargs_undefined", "{% macro mm(a=0) %}[{{ a }}]{% endmacro %}{{ mm(**nope) }}"),
    ("star_kwargs_undefined_attribute",
     "{% macro mm(a=0) %}[{{ a }}]{% endmacro %}{{ mm(**d.missing) }}"),
    ("star_kwargs_undefined_in_a_global", "{{ dict(**nope) }}"),
    ("star_kwargs_undefined_in_a_filter", "{{ lst|join(**nope) }}"),
    ("star_undefined_in_a_filter", "{{ lst|join(*nope) }}"),
    ("star_kwargs_undefined_in_a_test", "{{ 1 is odd(**nope) }}"),
]:
    for _kind in ("default", "chainable", "debug", "strict"):
        _set = {} if _kind == "default" else {"undefined": _kind}
        case(f"errshape/{_n}_{_kind}", _src, __settings__=_set,
             lst=[1, 2], d={"a": 1})

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
# --- the methods coverage says nothing has ever run ---------------------------
# Five surfaces a template can reach that no corpus case mentioned. None of
# them turned out to be wrong, which is worth recording: the point of grading
# them is that until now nothing could have told.

# bytes classification: only isalpha, isascii and istitle were graded. All of
# these are ASCII-only, all but isascii are false for the empty bytes, and the
# non-ASCII bytes of an encoded character are never any class.
case("methods/bytes_isalnum",
     '{{ "a1".encode().isalnum() }}|{{ "a b".encode().isalnum() }}|'
     '{{ "".encode().isalnum() }}|{{ "_".encode().isalnum() }}|'
     '{{ "\u00e9".encode().isalnum() }}')
case("methods/bytes_isdigit",
     '{{ "12".encode().isdigit() }}|{{ "1a".encode().isdigit() }}|'
     '{{ "".encode().isdigit() }}|{{ "\u00b2".encode().isdigit() }}')
case("methods/bytes_isspace",
     '{{ " \t\n".encode().isspace() }}|{{ "".encode().isspace() }}|'
     '{{ "a ".encode().isspace() }}|{{ "\u00a0".encode().isspace() }}')
case("methods/bytes_islower",
     '{{ "ab".encode().islower() }}|{{ "aB".encode().islower() }}|'
     '{{ "1".encode().islower() }}|{{ "".encode().islower() }}|'
     '{{ "\u00e9".encode().islower() }}')
case("methods/bytes_isupper",
     '{{ "AB".encode().isupper() }}|{{ "Ab".encode().isupper() }}|'
     '{{ "1".encode().isupper() }}|{{ "".encode().isupper() }}')

# dict.setdefault returns what is already there and only stores when nothing
# is, and its own default default is None.
case("methods/dict_setdefault",
     "{% set d = {'a': 1} %}{{ d.setdefault('a', 9) }}|{{ d.setdefault('b', 2) }}|"
     "{{ d }}|{{ d.setdefault('c') }}|{{ d }}")
case("errors/dict_setdefault_no_args", "{% set d = {} %}{{ d.setdefault() }}")

# list.remove drops the first equal element, returns None, and raises for one
# that is not there.
case("methods/list_remove",
     "{% set l = [1, 2, 1] %}{{ l.remove(1) }}|{{ l }}|{{ l.remove(1) }}|{{ l }}")
case("errors/list_remove_missing", "{% set l = [1] %}{{ l.remove(9) }}")
case("errors/list_remove_no_args", "{% set l = [] %}{{ l.remove() }}")

# truncate past its length check slices the value and then either splits it or
# adds the end to it, so a non-string gets that far and fails on one of those
# rather than on being the wrong kind of input -- and with an end its own kind,
# succeeds.
case("methods/truncate_list_killwords",
     "{{ [1,2,3,4,5,6,7,8,9,10]|truncate(2, true, [0], 0) }}")
case("errors/truncate_list_end_is_a_string",
     "{{ [1,2,3,4,5,6,7,8,9,10]|truncate(2, true, '', 0) }}")
case("errors/truncate_tuple_end_is_a_string",
     "{{ (1,2,3,4,5,6,7,8,9,10)|truncate(3, true, '', 0) }}")
case("errors/truncate_list_not_killwords",
     "{{ [1,2,3,4,5,6,7,8,9,10]|truncate(2, false, '', 0) }}")
case("errors/truncate_range", "{{ range(10)|truncate(2, true, '', 0) }}")
case("errors/truncate_int", "{{ 1234567890|truncate(2, true, '', 0) }}")
case("errors/truncate_dict", "{{ {'a':1,'b':2,'c':3}|truncate(1, true, '', 0) }}")
# The cut itself was a second implementation of the evaluator's slice, and it
# had drifted the way the folder's copy once did: it asked for a sequence and
# handed back anything else untouched. A bytes has no sequence, so it came back
# whole and the end was appended to all of it -- a wrong answer rather than an
# error. A dict has none either, so the failure came from the `+` afterwards
# and said the wrong thing; `s[:n]` on a mapping is a slice used as a key.
case("methods/truncate_bytes_killwords",
     "{{ 'abcdefghij'.encode()|truncate(2, true, 'x'.encode(), 0) }}")
case("methods/truncate_tuple_killwords",
     "{{ (1,2,3,4,5,6,7,8,9,10)|truncate(2, true, (0,), 0) }}")
case("errors/truncate_dict_end_is_a_list",
     "{{ {'a':1,'b':2,'c':3}|truncate(1, true, [0], 0) }}")
case("errors/truncate_range_end_is_a_list",
     "{{ range(10)|truncate(2, true, [0], 0) }}")
case("errors/truncate_range_end_is_a_range",
     "{{ range(10)|truncate(2, true, range(1), 0) }}")
case("errors/truncate_list_end_is_a_tuple",
     "{{ [1,2,3,4,5,6,7,8,9,10]|truncate(2, true, (0,), 0) }}")
case("errors/truncate_tuple_end_is_a_list",
     "{{ (1,2,3,4,5,6,7,8,9,10)|truncate(2, true, [0], 0) }}")

# int() and float() read Python's own numeric text, which allows a single
# underscore between two digits and nowhere else. A string the rule rejects is
# not an error: the filters fall back on their default.
case("methods/int_float_underscores",
     '{{ "1_000"|int }}|{{ "1_"|int }}|{{ "_1"|int }}|{{ "1__0"|int }}|'
     '{{ "1_0.5"|float }}|{{ " 1_0 "|int }}|{{ "1_000_000"|int }}|{{ "1_0e1_0"|float }}')
# --- the default is not allowed to be a disguise ------------------------------
# |int and |float answer their default rather than raising, which is jinja2's
# own behaviour and not a divergence. It is also how a conversion bug hides:
# `{{ "٤٢"|int }}` was 0 for months, and 0 is what a template gets when the
# subject genuinely is not a number, so nothing looked wrong.
#
# So the default is spelled -999 here, which no subject below converts to. A
# case whose golden is -999 is one CPython could not convert either; a case
# where gojja2 answers -999 and CPython answers a number is a silent wrong
# answer, and the conformance run says so.
_SENTINEL_SUBJECTS = [
    # plain
    "42", "-42", "+42", "0", "007", "0_0", " 42 ", "\t42\n", "",  " ",
    # underscores, at every legal and illegal position
    "1_000", "1_000_000", "1_", "_1", "1__0", "_", "1_0.5", "1_0e1_0",
    # signs
    "--42", "+-42", "-+42", "- 42", "42-", "++42",
    # bases and prefixes
    "0x1f", "0X1F", "0b101", "0B101", "0o17", "0O17", "0x", "0b", "0o",
    "0x_1f", "0b_1_0", "1f", "17", "101",
    # floats and specials
    "1.5", ".5", "5.", "1e5", "1E5", "1e", "e5", "inf", "-inf", "Inf",
    "infinity", "nan", "NaN", "1.5.5", "1,000", "1 000",
    # non-ASCII decimal digits, from several scripts
    "٤٢", "۴۲", "๔๒", "०१",
    "４２", "\U0001d7dc\U0001d7da", "٠٠٤٢",
    "٤2", "2٤", "٤_٢", "-٤٢", " ٤٢ ",
    " ٤٢", "٤.٢", "٤٢e١",
    # numeric-looking but not decimal
    "²", "½", "Ⅴ", "一", "٤²", "²٤",
    # not numbers at all
    "abc", "0x1f.5", "None", "True", "[]", chr(0), "42" + chr(0),
]
for _i, _subj in enumerate(_SENTINEL_SUBJECTS):
    case(f"methods/int_default_is_not_a_disguise_{_i}",
         "{{ " + lit(_subj) + "|int(-999) }}")
    case(f"methods/float_default_is_not_a_disguise_{_i}",
         "{{ " + lit(_subj) + "|float(-999) }}")
# The same for the base forms, where a wrong base silently answers the default
# rather than saying the digits do not fit it.
for _i, _subj in enumerate(["1f", "0x1f", "101", "0b101", "17", "0o17",
                            "19", "12", "١٥", "0x١٥", "z"]):
    for _base in (0, 2, 8, 10, 16, 36):
        case(f"methods/int_default_base_{_i}_{_base}",
             "{{ " + lit(_subj) + "|int(-999, " + str(_base) + ") }}")

# --- the code points the interpreters disagree about --------------------------
# CPython carries its own Unicode, so which characters are printable, which are
# digits and how each one cases are the interpreter's answers rather than
# jinja2's. Across 3.11 to 3.14 that is 10,311 code points, and until the
# version became a value gojja2 answered the pinned one's way whatever was
# asked for.
#
# Nothing in the corpus reached any of them -- they are all characters assigned
# after Unicode 14, so no ordinary template contains one -- which is exactly why
# the gap survived. These cases exist to reach them. Under the default they
# grade the default; under WithPythonVersion they grade the option, and
# testdata/golden-3.11 and its neighbours record what the older ones said.
_UNI = {
    # Kawi and Nag Mundari digits: Unicode 15, so 3.11 has neither.
    "kawi_zero": "\U00011f50",
    "kawi_two": "\U00011f52",
    "nag_mundari_zero": "\U0001e4f0",
    # Kaktovik numerals: Unicode 15, No rather than Nd, so isnumeric only.
    "kaktovik": "\U0001d2c0",
    # Garay, which gained a case pair in Unicode 16.
    "garay_upper": "\U00010d50",
    "garay_lower": "\U00010d70",
    # Assigned in Unicode 16, so only 3.14 prints it unescaped.
    "todhri": "\U0001e030",
    # An ordinary character, as the control: every interpreter agrees.
    "latin_a": "a",
    "arabic_indic_four": "٤",
}
for _name, _ch in _UNI.items():
    _l = lit(_ch)
    case(f"unicode/predicates_{_name}",
         "{{ " + _l + ".isdigit() }}|{{ " + _l + ".isdecimal() }}|"
         "{{ " + _l + ".isnumeric() }}|{{ " + _l + ".isalnum() }}|"
         "{{ " + _l + ".isalpha() }}")
    case(f"unicode/case_{_name}",
         "{{ " + _l + "|upper }}|{{ " + _l + "|lower }}|"
         "{{ " + _l + ".title() }}|{{ " + _l + ".casefold() }}|"
         "{{ " + _l + ".swapcase() }}|{{ " + _l + ".isupper() }}|"
         "{{ " + _l + ".islower() }}")
    # repr escapes by isprintable, which is where almost all of the difference
    # between the interpreters lives.
    case(f"unicode/repr_{_name}", "{{ [" + _l + "]|pprint }}")
    case(f"unicode/numeric_{_name}",
         "{{ " + _l + "|int(-1) }}|{{ " + _l + "|float(-1) }}")

# --- int() and float() do not read ASCII digits -------------------------------
# Python transforms every character carrying a *decimal* value into the ASCII
# digit of that value before parsing, so int("\u0664\u0662") is 42 and the
# scripts may even be mixed. gojja2 read ASCII only, in all three places it
# prepared numeric text -- the |int filter, the |float filter and the
# %-format path -- so `{{ "\u0664\u0662"|int }}` answered the filter's default
# of 0. A wrong number, silently, for any template handling localised digits.
#
# Decimal is the narrowest of the three numeric predicates and that is the
# point: SUPERSCRIPT TWO is isdigit, VULGAR FRACTION ONE HALF and ROMAN NUMERAL
# FIVE are isnumeric, and int() refuses all three.
_DIGITS = {
    "arabic_indic": "\u0664\u0662",
    "extended_arabic_indic": "\u06f4\u06f2",
    "thai": "\u0e54\u0e52",
    "devanagari": "\u0966\u0967",
    "fullwidth": "\uff14\uff12",
    "mathematical": "\U0001d7dc\U0001d7da",
    "myanmar_leading_zero": "\u1040\u1041",
    "leading_zeros": "\u0660\u0660\u0664\u0662",
    "mixed_scripts": "\u06642",
    "mixed_the_other_way": "2\u0664",
}
for _name, _d in _DIGITS.items():
    case(f"methods/int_digits_{_name}", "{{ " + lit(_d) + "|int }}")
    case(f"methods/float_digits_{_name}", "{{ " + lit(_d) + "|float }}")

# The rules layered on top still apply after the transform.
for _name, _expr in [
    ("underscore", "'\u0664_\u0662'|int"),
    ("sign_minus", "'-\u0664\u0662'|int"),
    ("sign_plus", "'+\u0664\u0662'|int"),
    ("surrounding_space", "' \u0664\u0662 '|int"),
    ("unicode_space", "'\u00a0\u0664\u0662\u3000'|int"),
    ("base_sixteen", "'\u0664\u0662'|int(0, 16)"),
    ("base_two", "'\u0661\u0660'|int(0, 2)"),
    ("float_fraction", "'\u0664.\u0662'|float"),
    ("float_exponent", "'\u0664\u0662e\u0661'|float"),
    ("filesizeformat", "'\u0664\u0662'|filesizeformat"),
    ("format_verb", '"%d"|safe % \'\u0664\u0662\''),
    ("format_verb_float", '"%f"|safe % \'\u0664\u0662\''),
]:
    case(f"methods/int_digits_rule_{_name}", "{{ " + _expr + " }}")

# And the ones that are not decimal are still refused, by both.
for _name, _expr in [
    ("superscript", "'\u00b2'|int(-1)"),
    ("vulgar_fraction", "'\u00bd'|int(-1)"),
    ("roman_numeral", "'\u2164'|int(-1)"),
    ("cjk_one", "'\u4e00'|int(-1)"),
    ("digit_then_superscript", "'\u0664\u00b2'|int(-1)"),
    ("trailing_underscore", "'\u0664\u0662_'|int(-1)"),
    ("doubled_underscore", "'\u0664__\u0662'|int(-1)"),
]:
    case(f"methods/int_digits_refused_{_name}", "{{ " + _expr + " }}")

# The classification predicates read the same table now, so they answer for the
# same set. isdecimal used Go's category tables, which are a later Unicode than
# the CPython this is graded against, and said True for twenty code points the
# specification does not have.
case("methods/str_numeric_predicates",
     "{{ '\u0664\u0662'.isdecimal() }}|{{ '\u0664\u0662'.isdigit() }}|"
     "{{ '\u0664\u0662'.isnumeric() }}|{{ '\u00b2'.isdecimal() }}|"
     "{{ '\u00b2'.isdigit() }}|{{ '\u00b2'.isnumeric() }}|"
     "{{ '\u00bd'.isdigit() }}|{{ '\u00bd'.isnumeric() }}|"
     "{{ '\u2164'.isnumeric() }}|{{ '\u4e00'.isnumeric() }}")

case("methods/int_underscores_with_base",
     '{{ "1_f"|int(0,16) }}|{{ "0x_1f"|int(0,16) }}|{{ "a_b"|int(0,16) }}|'
     '{{ "1_0"|int(0,2) }}|{{ "0b_1_0"|int(0,0) }}')

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

# A view can be reversed. 3.8 gave dict and its views a defined order, so
# `reversed()` works on one -- which is how `|last` reaches the last key without
# walking the whole thing. gojja2's reversible() asked for a Sequence or a
# Mapping, and a view is deliberately neither: it has a length and cannot be
# indexed, which is the whole of what makes it a view. So `{{ d.keys()|last }}`
# raised "'dict_keys' object is not reversible" where CPython answers.
case("methods/dict_view_last",
     "{% set d = {'b': 2, 'a': 1} %}{{ d.keys()|last }}|{{ d.values()|last }}|{{ d.items()|last }}")
case("methods/dict_view_last_of_empty", "{% set e = {} %}[{{ e.keys()|last }}]")
case("methods/dict_view_first_and_reverse",
     "{% set d = {'b': 2, 'a': 1} %}{{ d.keys()|first }}|{{ d.keys()|reverse|list }}|{{ d.items()|reverse|list }}")
# ...and something that genuinely cannot be reversed still says so: a loop is
# walked forwards only, which is the case the wording exists for.
case("errors/loop_is_not_reversible", "{% for i in [1, 2] %}{{ loop|last }}{% endfor %}")

# --- an empty bytes decodes without looking the codec up -----------------------
# CPython answers "" for an empty bytes before it consults the codec registry,
# so `b''.decode('nope')` is not an error and `b'x'.decode('nope')` is. The
# handler is skipped the same way. It is a decode-side fast path only:
# `''.encode('nope')` still raises, which is the asymmetry to get right.
#
# This also settles every codec gojja2 does not implement, for the empty case:
# `b''.decode('utf-16')` now agrees exactly rather than falling under the
# divergence docs/divergences.md records for the rest of them.
case("codecs/empty_bytes_decode_unknown", "[{{ ''.encode().decode('nope') }}]")
case("codecs/empty_bytes_decode_unsupported", "[{{ ''.encode().decode('utf-16') }}]")
case("codecs/empty_bytes_decode_bad_handler",
     "[{{ ''.encode().decode('utf-8', 'nope') }}]|[{{ ''.encode().decode('nope', 'nope') }}]")
# ...and the three that must still fail, which is what makes the fast path a
# fast path rather than a hole.
case("errors/nonempty_bytes_decode_unknown", "{{ 'x'.encode().decode('nope') }}")
case("errors/empty_str_encode_unknown", "{{ ''.encode('nope') }}")
case("errors/empty_bytes_decode_bad_type", "{{ ''.encode().decode(1) }}")

# --- printf-style formatting on bytes (PEP 461) --------------------------------
# 3.5 gave bytes the same `%` formatting str has. gojja2 had it for str only, so
# every one of these was "unsupported operand type(s) for %". The numeric verbs
# behave identically and the result is bytes; three things differ:
#
#   %b and %s want a bytes-like object and nothing else -- not a str, not a
#     number -- and the refusal names %b whichever of the two was written;
#   %r is ascii(), because a bytes cannot hold what repr() may produce;
#   %c takes a single byte or an int in range(256), not a code point.
#
# And %b stays unsupported in a *str* format, which is the case that keeps the
# two apart.
case("format/bytes_percent_numeric",
     "{{ '%d'.encode() % 5 }}|{{ '%x'.encode() % 255 }}|{{ '%5.2f'.encode() % 1.5 }}|{{ '%e'.encode() % 1.5 }}")
case("format/bytes_percent_b_and_s",
     "{{ '%s'.encode() % 'x'.encode() }}|{{ '%b'.encode() % 'x'.encode() }}"
     "|{{ '%10s'.encode() % 'x'.encode() }}|{{ '%.2s'.encode() % 'abcd'.encode() }}")
case("format/bytes_percent_repr_and_ascii", "{{ '%a'.encode() % 'x' }}|{{ '%r'.encode() % 'x' }}")
case("format/bytes_percent_c", "{{ '%c'.encode() % 65 }}|{{ '%c'.encode() % 'A'.encode() }}")
case("format/bytes_percent_tuple_and_literal",
     "{{ '%s %s'.encode() % ('a'.encode(), 'b'.encode()) }}|{{ '%%'.encode() % () }}")
case("errors/bytes_percent_s_wants_bytes", "{{ '%s'.encode() % 'x' }}")
case("errors/bytes_percent_s_wants_bytes_not_int", "{{ '%s'.encode() % 5 }}")
case("errors/bytes_percent_too_many_args", "{{ 'ab'.encode() % 1 }}")
case("errors/bytes_percent_c_out_of_range", "{{ '%c'.encode() % 256 }}")
# 3.14 names a bytes of the wrong length by its *length* -- "not a bytes object
# of length 2" -- where anything that is not a bytes at all keeps the plain type
# name. It is the same split str.center's argument 2 makes; see
# FillCharMessageNamesTheLength. gojja2 said "not bytes" for the bytes case, so
# the 3.14 column read as agreement. Found by a soak on the version axis.
# 3.14 names the type *qualified* in the two `%c` messages and nowhere else in
# the family: `%c` of an Undefined is "not jinja2.runtime.Undefined" while `%f` of
# the same value is "not Undefined" and `%x` likewise. A builtin is unqualified
# either way, so a dict view stays "dict_keys". Found by a soak on the version
# axis, which is the only place the two names differ.
for _n, _src in [
    ("an_undefined", "{{ '[%c]' % nope }}"),
    ("an_undefined_in_bytes", "{{ ('[%c]'.encode()) % nope }}"),
    ("a_macro", "{% macro m() %}{% endmacro %}{{ '[%c]' % m }}"),
    ("a_loop", "{% for i in [1] %}{{ '[%c]' % loop }}{% endfor %}"),
    ("a_dict_view", "{% set d = {'a': 1} %}{{ '[%c]' % d.keys() }}"),
    ("a_list", "{{ '[%c]' % [1] }}"),
]:
    case(f"errors/percent_c_names_{_n}", _src)
# ...and the neighbours that stay unqualified, which is what makes it the `%c`
# pair rather than a rule about the family.
case("errors/percent_f_of_an_undefined_is_unqualified", "{{ '[%f]' % nope }}")
case("errors/percent_x_of_an_undefined_is_unqualified", "{{ '[%x]' % nope }}")
for _n, _src in [
    ("two_bytes", "{{ '%c'.encode() % 'ab'.encode() }}"),
    ("no_bytes", "{{ '%c'.encode() % ''.encode() }}"),
    ("three_bytes", "{{ '%c'.encode() % 'abc'.encode() }}"),
    ("a_str", "{{ '%c'.encode() % 'x' }}"),
    ("a_float", "{{ '%c'.encode() % 1.5 }}"),
    ("none", "{{ '%c'.encode() % none }}"),
    ("a_list", "{{ '%c'.encode() % [1] }}"),
]:
    case(f"errors/bytes_percent_c_of_{_n}", _src)
case("errors/bytes_percent_unsupported_verb", "{{ '%q'.encode() % 1 }}")
case("errors/str_percent_has_no_b", "{{ '%b' % 'x'.encode() }}")

# A bytes counts as a mapping on the right of `%`.
#
# CPython decides between "a mapping" and "one positional argument" by asking
# whether the operand supports subscripting, and names tuple and str as the two
# exceptions. A bytes is subscriptable and is neither, so it counts -- which is
# why `"0" % b""` renders "0" instead of complaining that the b"" was never
# converted. gojja2 had dict, list and undefined on that list and not bytes.
#
# Looking a *name* up in one still fails, as it does for a list, and CPython
# words it "byte indices" rather than "bytes indices".
case("format/percent_bytes_is_a_mapping",
     "[{{ '0' % ''.encode() }}]|[{{ '0' % 'xy'.encode() }}]|{{ '%s' % 'xy'.encode() }}")
case("errors/percent_bytes_key_lookup", "{{ '%(k)s' % ''.encode() }}")
case("errors/percent_bytes_not_enough_args", "{{ '%s %s' % 'xy'.encode() }}")
# ...and the operands that are still refused, which is what makes the rule a
# rule rather than a blanket.
case("errors/percent_str_is_not_a_mapping", "{{ '0' % 'x' }}")
case("errors/percent_int_is_not_a_mapping", "{{ '0' % 1 }}")
# The mapping key carries the *format string's* type, and so does the complaint
# an operand that cannot be indexed by name makes about it: a bytes format asks
# a list for a bytes key, so the list says "not bytes". gojja2 hardcoded "str"
# in that message and reported it under a bytes format too. Found by a soak seed
# that put a filtered dict -- a list by then -- on the right of a bytes `%`.
for _n, _src in [
    ("list_under_a_bytes_format", "{{ '%(k)s'.encode() % [1] }}"),
    ("list_under_a_bytes_b_verb", "{{ '%(k)b'.encode() % [] }}"),
    ("bytes_under_a_str_format", "{{ '%(k)d' % 'ab'.encode() }}"),
]:
    case(f"errors/percent_key_in_a_{_n}", _src)
# And the key that is looked up: a str key does not match a bytes one, in either
# direction, so the KeyError repr says which was asked for.
# A matching *str* key is still a miss under a bytes format, which the
# missing-key case above cannot show: its dict has no candidate at all.
case("errors/percent_key_is_bytes_in_a_dict", "{{ '%(k)s'.encode() % {'k': 1} }}")

# --- messages nothing had ever produced: methods.go ---------------------------
# `make ungraded` had 40 of its 109 remaining sites in methods.go, in four
# clusters. Every one of the shapes below already agreed with CPython; what was
# missing was a case saying so, which is the point of the audit -- an ungraded
# message reads as agreement in every column of the version matrix. One shape
# did not agree, and it is the block after this.
#
# str.maketrans and str.translate: the happy paths were generated, none of the
# refusals were.
for _n, _src in [
    ("maketrans_one_arg_not_a_dict", "{{ 'a'.maketrans('ab') }}"),
    ("maketrans_unequal_length", "{{ 'a'.maketrans('ab', 'x') }}"),
    ("maketrans_third_not_a_string", "{{ 'a'.maketrans('ab', 'xy', 1) }}"),
    ("maketrans_no_arguments", "{{ 'a'.maketrans() }}"),
    ("maketrans_too_many_arguments", "{{ 'a'.maketrans('a', 'b', 'c', 'd') }}"),
    ("maketrans_long_string_key", "{{ 'a'.maketrans({'ab': 1}) }}"),
    ("maketrans_key_is_a_float", "{{ 'a'.maketrans({1.5: 'x'}) }}"),
    ("translate_no_arguments", "{{ 'a'.translate() }}"),
    ("translate_out_of_range", "{{ 'abc'.translate({97: 1114112}) }}"),
    ("translate_bad_value", "{{ 'abc'.translate({97: 1.5}) }}"),
]:
    case(f"errors/{_n}", _src)
# A separator that is not a string, and the one that is empty.
for _n, _src in [
    ("split_separator_is_an_int", "{{ 'a b'.split(1) }}"),
    ("split_empty_separator", "{{ 'a b'.split('') }}"),
    ("rsplit_separator_is_a_float", "{{ 'a b'.rsplit(1.5) }}"),
    ("join_no_arguments", "{{ 'a'.join() }}"),
    ("join_item_is_an_int", "{{ ','.join([1]) }}"),
    ("join_second_item_is_an_int", "{{ ','.join(['a', 2]) }}"),
]:
    case(f"errors/{_n}", _src)
# The replacement-field parser's own complaints. Which one a truncated field
# gets depends on how far it read: a lone brace, a name that never closed, and a
# spec whose nested field never closed are three different messages.
for _n, _src in [
    ("format_single_close_brace", "{{ '}'.format() }}"),
    ("format_unmatched_in_spec", "{{ '{0:{1'.format(1, 2) }}"),
    ("format_unterminated_index", "{{ '{0[a'.format({'a': 1}) }}"),
    ("format_expected_close", "{{ '{0'.format(1) }}"),
    ("format_single_open_brace", "{{ '{'.format(1) }}"),
    ("format_empty_conversion", "{{ '{0!}'.format(1) }}"),
    ("format_map_key_missing", "{{ '{a}'.format_map({}) }}"),
    ("format_string_index_out_of_range", "{{ '{0[5]}'.format('abc') }}"),
    ("format_manual_then_automatic", "{{ '{0}{}'.format(1, 2) }}"),
    ("format_auto_index_out_of_range", "{{ '{}{}'.format(1) }}"),
    ("format_keyword_missing", "{{ '{x}'.format() }}"),
    ("format_map_auto_field", "{{ '{}'.format_map({}) }}"),
    ("format_index_key_missing", "{{ '{0[a]}'.format({'b': 1}) }}"),
]:
    case(f"errors/{_n}", _src)
# The arity checks on dict's and list's own methods, which each word themselves
# differently -- "expected at least 1 argument, got 0", "takes exactly one
# argument (0 given)", "takes no arguments (1 given)" -- and so cannot be
# generated from one rule.
for _n, _src in [
    ("dict_get_no_arguments", "{{ {'a': 1}.get() }}"),
    ("dict_pop_no_arguments", "{{ {'a': 1}.pop() }}"),
    ("dict_setdefault_no_arguments", "{{ {'a': 1}.setdefault() }}"),
    ("dict_popitem_takes_none", "{{ {'a': 1}.popitem(1) }}"),
    ("dict_fromkeys_no_arguments", "{{ {}.fromkeys() }}"),
    ("list_append_no_arguments", "{{ [].append() }}"),
    ("list_extend_no_arguments", "{{ [].extend() }}"),
    ("list_insert_one_argument", "{{ [1].insert(1) }}"),
    ("list_remove_no_arguments", "{{ [1].remove() }}"),
    ("list_index_no_arguments", "{{ [1].index() }}"),
    ("list_count_no_arguments", "{{ [1].count() }}"),
]:
    case(f"errors/{_n}", _src)

# An integer in the mini-language that does not fit.
#
# CPython reads a width, a precision and a replacement field's index with the
# same routine, which accumulates digit by digit and checks before each step
# that the result will still hold a Py_ssize_t -- so all three say "Too many
# decimal digits in format string". gojja2 multiplied and added without the
# check, which was not only the wrong message: `'{18446744073709551616}'`
# wrapped to 0 and printed the *first* argument, and a field index one past
# that printed the second.
#
# A leading-zero index is the case that keeps the check honest: those digits are
# long and the number is small, so refusing on length alone would break them.
for _n, _src in [
    ("format_digits_in_an_index", "{{ '{0[99999999999999999999]}'.format([1]) }}"),
    ("format_digits_in_a_field", "{{ '{99999999999999999999}'.format(1) }}"),
    ("format_digits_that_wrap", "{{ '{18446744073709551616}'.format('a', 'b') }}"),
    ("format_digits_that_wrap_to_one", "{{ '{18446744073709551617}'.format('a', 'b') }}"),
    ("format_digits_in_a_width", "{{ '{:99999999999999999999}'.format(1) }}"),
    ("format_digits_in_a_precision", "{{ '{:.99999999999999999999f}'.format(1.5) }}"),
    ("format_digits_in_a_nested_spec", "{{ '{:{}}'.format(1, 99999999999999999999) }}"),
]:
    case(f"errors/{_n}", _src)
# The `[key]` step of a replacement field is a real obj[key], and what each base
# says about one it cannot take is the base's own complaint. gojja2 answered a
# KeyError -- what a *mapping* says about a key it does not hold -- for every
# object, indexed a bytes as though it were a str, and reported a subscript
# error for an undefined instead of the undefined's own.
#
# A bytes indexes to the *number* its byte is; its refusals are "byte indices
# must be integers or slices, not str" and an out-of-range message that names no
# type at all. A range names itself in both, with an "object" in the
# out-of-range one that a list does not have. A groupby group is a namedtuple,
# and both of its complaints come from tuple rather than from the subclass. A
# namespace, a cycler, a joiner, a dict view and a class object are not
# subscriptable at all.
case("format/field_index_into_a_bytes",
     "{{ '{0[0]}'.format('ab'.encode()) }}|{{ '{0[1]}'.format('ab'.encode()) }}")
case("errors/field_index_bytes_out_of_range", "{{ '{0[9]}'.format('ab'.encode()) }}")
case("errors/field_index_bytes_string_key", "{{ '{0[a]}'.format('ab'.encode()) }}")
case("format/field_index_into_a_range",
     "{{ '{0[1]}'.format(range(3)) }}|{{ '{0[2]}'.format(range(1, 9, 3)) }}")
case("errors/field_index_range_out_of_range", "{{ '{0[9]}'.format(range(3)) }}")
case("errors/field_index_range_string_key", "{{ '{0[a]}'.format(range(3)) }}")
# "-1" and "1.5" are not all digits, so both are string keys and neither is an
# index -- which is why a negative one is a type error and not the last element.
case("errors/field_index_range_negative", "{{ '{0[-1]}'.format(range(3)) }}")
for _n, _src in [
    ("namespace", "{{ '{0[v]}'.format(namespace(v=1)) }}"),
    ("cycler", "{{ '{0[a]}'.format(cycler('a','b')) }}"),
    ("joiner", "{{ '{0[a]}'.format(joiner('-')) }}"),
    ("dict_view", "{{ '{0[a]}'.format({'a': 1}.items()) }}"),
    ("an_int", "{{ '{0[a]}'.format(1) }}"),
]:
    case(f"errors/field_index_not_subscriptable_{_n}", _src)
case("errors/field_index_on_an_undefined", "{{ '{0[a]}'.format(nope) }}")
case("format/field_index_into_a_group",
     "{% set g = [{'k': 1}]|groupby('k') %}{{ '{0[0]}'.format(g[0]) }}")
for _n, _src in [
    ("group_out_of_range",
     "{% set g = [{'k': 1}]|groupby('k') %}{{ '{0[9]}'.format(g[0]) }}"),
    ("group_string_key",
     "{% set g = [{'k': 1}]|groupby('k') %}{{ '{0[a]}'.format(g[0]) }}"),
]:
    case(f"errors/field_index_{_n}", _src)
# A manual index past the end, and a spec whose nested field is closed by the
# outer field's brace: two messages the parser makes that nothing reached, the
# second because an outer field that never closes is reported first.
case("errors/format_manual_index_out_of_range", "{{ '{5}'.format(1) }}")
case("errors/format_unmatched_in_a_closed_spec", "{{ '{0:{1}'.format(1, 2) }}")
case("errors/format_unmatched_after_spec_text", "{{ '{0:a{b}'.format(1) }}")
case("format/leading_zeros_in_an_index",
     "{{ '{0[00000000000000000001]}'.format([1, 2]) }}|"
     "{{ '{00000000000000000001}'.format(1, 2) }}|{{ '{:00000000000005d}'.format(1) }}")

# --- a dict view subtracts as a set --------------------------------------------
# `d.keys() - xs` is the whole of the set arithmetic a template can write:
# jinja2's grammar has no `&` or `^`, `|` is the filter operator, and CPython
# refuses `set - list` so the result cannot be the left operand of another one.
# gojja2 answered "unsupported operand type(s) for -" to all of it.
#
# The cases below are the ones with a stable answer. A set of two or more prints
# in CPython's hash order, which is randomised per process, so its repr is no
# more gradable than lipsum() is -- see docs/divergences.md, and
# TestSetOrderIsSorted for what is asserted instead.
case("methods/dict_view_difference_one_left",
     "{% set d = {'b': 2, 'a': 1} %}{{ d.keys() - ['a'] }}")
case("methods/dict_view_difference_empty",
     "{% set d = {'b': 2, 'a': 1} %}{{ d.keys() - ['a', 'b'] }}|{{ d.keys() - 'ab' }}")
case("methods/dict_view_difference_items",
     "{% set d = {'b': 2, 'a': 1} %}{{ d.items() - [('a', 1)] }}")
# Everything about the result that does not depend on its order.
case("methods/dict_view_difference_length_and_membership",
     "{% set d = {'b': 2, 'a': 1} %}{{ (d.keys() - [])|length }}|{{ 'b' in (d.keys() - ['a']) }}"
     "|{{ 'a' in (d.keys() - ['a']) }}|{{ (d.keys() - [])|list|sort }}")
case("methods/dict_view_difference_truthiness",
     "{% set d = {'b': 2, 'a': 1} %}{% if d.keys() - ['a','b'] %}T{% else %}F{% endif %}"
     "{% if d.keys() - ['a'] %}T{% else %}F{% endif %}")
case("methods/dict_view_difference_equality",
     "{% set d = {'b': 2, 'a': 1} %}{{ (d.keys() - []) == (d.keys() - []) }}")
# ...and the four refusals, each for its own reason.
case("errors/dict_view_difference_not_iterable", "{% set d = {'a': 1} %}{{ d.keys() - 0 }}")
case("errors/dict_values_has_no_difference", "{% set d = {'a': 1} %}{{ d.values() - [1] }}")
case("errors/dict_view_difference_unhashable", "{% set d = {'a': 1} %}{{ d.keys() - [[1]] }}")
case("errors/set_minus_a_list", "{% set d = {'a': 1} %}{{ (d.keys() - []) - [] }}")

# The left operand is hashed before the right one is even looked at: CPython
# builds the set from the view first, so items holding a dict are refused
# whatever is on the other side -- including an empty list, which is iterable and
# perfectly fine. Doing it the other way round reports whichever operand is wrong
# second, which is what the generated differential caught.
case("errors/set_difference_hashes_the_view_first",
     "{% set d = {'x': {'y': [1, 2]}} %}{{ d.items() - [] }}")
case("errors/set_difference_hashes_the_view_first_too",
     "{% set d = {'x': {'y': [1, 2]}} %}{{ d.items() - 1.5 }}")
# ...and with hashable elements on the left it is the right operand that is
# reported, which is the pair that makes the order observable.
case("errors/set_difference_then_the_other_operand",
     "{% set d = {'a': 1} %}{{ d.items() - 1.5 }}")

# ...and with the view written second, which CPython reaches through the view's
# __rsub__: the left operand is iterated into a set and the view taken out of it.
# gojja2 answered "unsupported operand type(s) for -" to all of these.
#
# The order is the other way round from the forward form, and observably so:
# __rsub__ builds set(other) *before* set(view), so a left operand that is not
# iterable is reported before the view is hashed, and a left operand holding
# something unhashable is reported before the view's own elements are looked at.
# Sorted, not printed: three elements print in CPython's hash order, which is
# randomised per process. Only a set of one or none has a stable repr -- see
# docs/divergences.md, "The order a set prints in".
case("methods/set_reverse_difference",
     "{% set d = {'b': 2, 'a': 1} %}{{ ('[1]' - d.items())|list|sort }}"
     "|{{ ('[1]' - d.items())|length }}")
case("methods/set_reverse_difference_one_element",
     "{% set d = {'b': 2, 'a': 1} %}{{ 'ax' - d.keys() }}")
case("methods/set_reverse_difference_empty",
     "{% set d = {'b': 2, 'a': 1} %}{{ 'ab' - d.keys() }}|{{ ['a'] - d.keys() }}")
case("errors/set_reverse_left_not_iterable",
     "{% set d = {'a': 1} %}{{ true - d.items() }}")
case("errors/set_reverse_left_not_iterable_wins",
     "{% set nd = {'x': {'y': [1, 2]}} %}{{ true - nd.items() }}")
case("errors/set_reverse_left_unhashable_wins",
     "{% set nd = {'x': {'y': [1, 2]}} %}{{ [[1]] - nd.items() }}")
case("errors/set_reverse_view_unhashable",
     "{% set nd = {'x': {'y': [1, 2]}} %}{{ 'ab' - nd.items() }}")
case("errors/set_reverse_values_has_none",
     "{% set d = {'a': 1} %}{{ ['a'] - d.values() }}")
# ...and subtraction that has nothing to do with sets is untouched.
case("methods/set_reverse_leaves_arithmetic_alone", "{{ 5 - 3 }}|{{ 2.5 - 1 }}")

# ...and the sixth surface, which *was* wrong: an empty view is falsey.
#
# Python takes bool() from __bool__, or from __len__ when there is no __bool__,
# and only then defaults to true. A view has a length and no __bool__, so an
# empty one is false. gojja2 answered the Object switch in IsTrue with Booler,
# Mapping and Sequence and a view is deliberately none of the three, so it fell
# through to the default and was true however empty it was.
#
# That is a wrong *branch*, not a wrong error: `{% if d.keys() %}` on an empty
# dict ran its body and printed something jinja2 printed nothing for. The
# generated differential found it once the generator learned to call methods,
# and the five cases above could not have: every one of them holds a dict with
# something in it.
case("methods/dict_view_empty_is_falsey",
     "{% set e = {} %}{% if e.keys() %}T{% else %}F{% endif %}"
     "{% if e.values() %}T{% else %}F{% endif %}{% if e.items() %}T{% else %}F{% endif %}")
case("methods/dict_view_nonempty_is_truthy",
     "{% set d = {'a': 1} %}{% if d.keys() %}T{% else %}F{% endif %}"
     "{% if d.values() %}T{% else %}F{% endif %}{% if d.items() %}T{% else %}F{% endif %}")
case("methods/dict_view_not_and_or",
     "{% set e = {} %}{% set d = {'a': 1} %}"
     "{{ not e.keys() }}|{{ not d.keys() }}|{{ e.keys() or 'fallback' }}|{{ d.values() and 'yes' }}")
# A view is live, so emptying the dict makes a view taken earlier falsey too.
case("methods/dict_view_truthiness_tracks_the_dict",
     "{% set d = {'a': 1} %}{% set k = d.keys() %}{% if k %}T{% else %}F{% endif %}"
     "{{ d.clear() }}{% if k %}T{% else %}F{% endif %}")

# --- a dict cannot be resized while a loop walks it ---------------------------
# Python raises RuntimeError the moment a dict's *size* changes under an
# iterator, and raises it on every step -- including the one that would have
# ended the loop, so a single-key dict counts. gojja2 walked a snapshot of the
# keys and noticed nothing: the loop finished, or failed later with whatever the
# body's second attempt raised, which is how the generated differential found it
# (`{% for i in d if d.pop('a') %}` gave KeyError here and RuntimeError there).
#
# Changing a *value* is not a resize and is allowed, which is the pair of cases
# that keeps the guard from being a blanket refusal. A list is deliberately not
# guarded: CPython does not guard one either.
case("loops/dict_resized_by_adding", "{% for k in d %}{% set _ = d.update({'z': 1}) %}{% endfor %}", d={"a": 1, "b": 2})
case("loops/dict_resized_by_removing", "{% for k in d %}{% set _ = d.pop('a') %}{% endfor %}", d={"a": 1, "b": 2})
case("loops/dict_resized_by_clearing", "{% for k in d %}{% set _ = d.clear() %}{% endfor %}", d={"a": 1, "b": 2})
case("loops/dict_resized_one_key", "{% for k in d %}{% set _ = d.update({'z': 1}) %}{% endfor %}", d={"a": 1})
case("loops/dict_resized_in_loop_filter", "{% for k in d if d.pop('a') %}{% endfor %}", d={"a": 1, "b": 2})
case("loops/dict_resized_through_keys_view",
     "{% for k in d.keys() %}{% set _ = d.update({'z': 1}) %}{% endfor %}", d={"a": 1, "b": 2})
case("loops/dict_resized_through_items_view",
     "{% for k in d.items() %}{% set _ = d.update({'z': 1}) %}{% endfor %}", d={"a": 1, "b": 2})
# ...and the three that must still be allowed.
case("loops/dict_value_changed_is_not_a_resize",
     "{% for k in d %}{% set _ = d.update({'a': 9}) %}{{ k }}{% endfor %}|{{ d.a }}", d={"a": 1, "b": 2})
case("loops/dict_resized_over_a_snapshot",
     "{% for k in d|list %}{% set _ = d.update({'z': 1}) %}{{ k }}{% endfor %}", d={"a": 1, "b": 2})
case("loops/dict_walked_without_touching_it", "{% for k in d %}{{ k }}{% endfor %}", d={"a": 1, "b": 2})

# --- a list is walked live, where a dict is guarded ----------------------------
# The other half of the cases above, and the opposite answer. CPython refuses a
# dict that changes size under an iterator and says nothing at all about a list:
# a list iterator holds an index and asks the list its length each time, so a
# body that shortens the list ends the loop early and one that lengthens it runs
# on. gojja2 walked a snapshot, so none of that showed -- `{{ i }}` with a pop in
# the body printed every original element instead of stopping halfway.
#
# Not an error-class difference: a wrong *output*, which is why the render soak
# found it and the corpus had not.
case("loops/list_shortened_while_walking",
     "{% for i in lst %}{{ i }}{% set _ = lst.pop() %}{% endfor %}", lst=[1, 2, 3, 4])
case("loops/list_cleared_while_walking",
     "{% for i in lst %}{{ i }}{% set _ = lst.clear() %}{% endfor %}", lst=[1, 2, 3, 4])
case("loops/list_grown_while_walking",
     "{% for i in lst %}{{ i }}{% if loop.index == 1 %}{% set _ = lst.insert(0, 9) %}{% endif %}{% endfor %}",
     lst=[1, 2, 3])
case("loops/list_shortened_by_a_loop_filter",
     "{% for i in lst %}{% for j in users if lst.pop() %}{% endfor %}{% endfor %}[{{ lst }}]",
     lst=[1, 2, 3, 4], users=[1, 2, 3])
# loop.length is the live length too, asked afresh each pass.
case("loops/list_length_while_walking",
     "{% for i in lst %}{{ loop.length }}{% endfor %}", lst=[1, 2, 3, 4])
# A tuple cannot be mutated, so it is the same either way.
case("loops/tuple_is_walked_the_same", "{% for i in (1, 2, 3) %}{{ i }}{% endfor %}")

# --- a loop filter that raises, reported where jinja2 reports it ---------------
# `loop.length`, `loop.revindex` and `loop.last` need the total, which for a
# filtered loop means running the test over the rest of the input -- and the test
# can raise. jinja2 raises it out of the property access; gojja2's source
# recorded it and waited for the loop's next pass, so a body that failed for its
# own reason first reported that instead.
#
# `loop.index` does not need the total and does not force the test, which is the
# case that tells the two apart: there the body's own error is correct.
case("loops/filter_raises_under_revindex",
     "{% for i in range(3) if d.pop('a') %}{{ loop.revindex|length }}{% endfor %}",
     d={"a": 1, "b": 2, "c": 3})
case("loops/filter_raises_under_length",
     "{% for i in range(3) if d.pop('a') %}{{ loop.length|length }}{% endfor %}",
     d={"a": 1, "b": 2, "c": 3})
case("loops/filter_raises_under_last",
     "{% for i in range(3) if d.pop('a') %}{{ loop.last|length }}{% endfor %}",
     d={"a": 1, "b": 2, "c": 3})
case("loops/filter_not_forced_by_index",
     "{% for i in range(3) if d.pop('a') %}{{ loop.index|length }}{% endfor %}",
     d={"a": 1, "b": 2, "c": 3})
# ...and a filtered loop whose test does not raise still answers the totals.
case("loops/revindex_under_a_working_filter",
     "{% for i in range(3) if i %}{{ loop.revindex }}{{ loop.length }}{{ loop.last }}{% endfor %}")

# A *filtered* loop walks the list live too.
#
# The live walk added earlier in this stack reached the plain `{% for %}` and not
# the filtered one: that path pulls from an iterator, and value.Iterate takes the
# slice as it is when the walk starts. So a test that shortens the list changed
# nothing, and the loop ran over every original element -- `{% for i in mix if
# mix.clear() or 7 %}` printed five where CPython prints one.
#
# The test runs between passes, so it sees what the body did *and* what it did
# itself, which is the whole reason the walk has to be live on this path too.
case("loops/filtered_list_cleared_by_its_own_test",
     "{% for i in mix if mix.clear() or 7 %}x{% endfor %}", mix=[1, 2, 3, 4, 5])
case("loops/filtered_list_shortened_by_its_own_test",
     "{% for i in mix if mix.pop() %}x{% endfor %}", mix=[1, 2, 3, 4, 5])
case("loops/filtered_list_shortened_by_the_body",
     "{% for i in mix if 1 %}x{% set _ = mix.pop() %}{% endfor %}", mix=[1, 2, 3, 4, 5])
# ...and the shapes that must not move: a test that touches nothing, and the
# containers a snapshot and a live walk cannot tell apart.
case("loops/filtered_list_untouched", "{% for i in mix if 1 %}x{% endfor %}", mix=[1, 2, 3, 4, 5])
case("loops/filtered_tuple_and_range",
     "{% for i in (1,2,3) if 1 %}x{% endfor %}|{% for i in range(3) if 1 %}y{% endfor %}")

# --- the 'z' format code ------------------------------------------------------
# Python 3.11 added `z` to the format mini-language (PEP 682): it renders what
# rounds to zero without its sign. gojja2 did not know the letter at all, so
# every spec below was "Unknown format code 'z'".
#
# It sits between the sign and '#' and nowhere else, which is the difference
# between `{:z#}` (a spec) and `{:#z}` (a '#' and a type called z).
#
# Who may ask for it goes by the *presentation type*, not by the value's own.
# gojja2 read it off the value and so refused `{:zG}` on an int, where CPython
# converts the int to a float for a float code and the z rides along. The
# refusal therefore waits for the formatter the type dispatches to, which also
# fixes its place in the order: after the code has been recognised (`{:zq}` is
# about the q) and after an integer precision is refused (`{:z.2d}` is about the
# precision), but before anything 'c' has to say and before a string's '#'.
# Found by the format-spec arm of the soak generator, on `{:-zG}`.
#
# 3.11 is the oldest interpreter modelled here, so this needs no version gate.
case("format/z_coerces_negative_zero",
     "{{ '{:z}'.format(nz) }}|{{ '{:z}'.format(0.0) }}|{{ '{:z.2f}'.format(nz) }}|{{ '{:zf}'.format(nz) }}", nz=-0.0)
case("format/z_keeps_a_real_sign", "{{ '{:z}'.format(-1.5) }}|{{ '{:z.2%}'.format(-0.001) }}|{{ '{:z}'.format(1.5) }}")
# The coercion is decided after rounding, not on -0.0 alone.
case("format/z_after_rounding", "{{ '{:z.1f}'.format(-0.04) }}|{{ '{:z.2f}'.format(-0.004) }}|{{ '{:.1f}'.format(-0.04) }}")
case("format/z_every_float_type",
     "{{ '{:ze}'.format(nz) }}|{{ '{:zg}'.format(nz) }}|{{ '{:z%}'.format(nz) }}|{{ '{:zE}'.format(nz) }}", nz=-0.0)
case("format/z_with_width_and_sign",
     "{{ '{:+z.2f}'.format(nz) }}|{{ '{:z08.2f}'.format(nz) }}|{{ '{:>z.2f}'.format(nz) }}|{{ '{:z#}'.format(1.5) }}", nz=-0.0)
# An infinity has no digits to round, so it keeps its sign.
# The infinity is computed from a context value, not written down. JSON cannot
# express one, and any constant expression that makes one gets folded -- into
# `inf` in jinja2's generated Python, which is the separate divergence recorded
# in docs/divergences.md rather than anything about z.
case("format/z_leaves_infinity_alone",
     "{{ '{:z}'.format(big * -10) }}|{{ '{:z}'.format(big * 10) }}", big=1e308)
case("errors/z_not_allowed_on_int", "{{ '{:z}'.format(1) }}")
case("errors/z_not_allowed_on_int_code", "{{ '{:zx}'.format(255) }}")
case("errors/z_not_allowed_on_bool", "{{ '{:z}'.format(true) }}")
case("errors/z_not_allowed_on_str", "{{ '{:zs}'.format('a') }}")
case("errors/z_out_of_position_after", "{{ '{:z+.2f}'.format(1.5) }}")
case("errors/z_out_of_position_before", "{{ '{:0z.2f}'.format(1.5) }}")
case("errors/z_after_hash_is_a_type", "{{ '{:#z}'.format(1.5) }}")
case("errors/z_int_code_on_a_float", "{{ '{:zd}'.format(1.5) }}")
# An int or a bool with a float code converts first, so the z is allowed there
# and the sign it coerces is the converted number's.
case("format/z_on_an_int_with_a_float_code",
     "{{ '{:zG}'.format(-1234567) }}|{{ '{:ze}'.format(-1234567) }}|{{ '{:z%}'.format(0) }}|"
     "{{ '{:z.2f}'.format(-1234567) }}|{{ '{:zg}'.format(true) }}|{{ '{:zn}'.format(1.5) }}")
# The same for a wide int, which converts through the same checked coercion the
# float codes use -- so this is an OverflowError and not an infinity.
case("errors/z_on_a_wide_int", "{{ '{:zf}'.format(10 ** 400) }}")
# Where the refusal sits in the order of complaints. Each of these would report
# the z if the check ran off the value's type at parse time.
for _n, _src in [
    ("code_first", "{{ '{:zq}'.format(1) }}"),
    ("code_first_on_a_str", "{{ '{:zq}'.format('a') }}"),
    ("grouping_first", "{{ '{:z,x}'.format(1) }}"),
    ("grouping_first_on_a_str", "{{ '{:z,}'.format('a') }}"),
    ("precision_first", "{{ '{:z.2d}'.format(1) }}"),
    ("z_before_c", "{{ '{:zc}'.format(1) }}"),
    ("z_before_a_str_hash", "{{ '{:z#s}'.format('a') }}"),
    # The sign comes before z in the grammar, so a string spec can carry
    # both -- and the sign is the one that complains. Found by a soak seed
    # on `{: z5.30}`.
    ("space_before_z_on_a_str", "{{ '{: z5.30}'.format('a') }}"),
    ("sign_before_z_on_a_str", "{{ '{:+zs}'.format('a') }}"),
    ("z_before_a_str_equals", "{{ '{:=zs}'.format('a') }}"),
    # ...and on an int the sign says nothing, so the z still wins.
    ("space_then_z_on_an_int", "{{ '{: zd}'.format(1) }}"),
]:
    case(f"errors/z_order_{_n}", _src)

# --- int.is_integer, which 3.12 added -----------------------------------------
# It answers True for every int, so that a caller can ask the question of a
# number without first knowing which kind it is. gojja2 had it on float only, so
# `{{ (0).is_integer() }}` was a missing attribute on every version -- right for
# 3.11 and wrong for the three after it. The version override records 3.11.
case("methods/int_is_integer", "{{ (0).is_integer() }}|{{ (-1).is_integer() }}|{{ n.is_integer() }}", n=7)
case("methods/float_is_integer_unchanged", "{{ (2.5).is_integer() }}|{{ (2.0).is_integer() }}|{{ (-0.0).is_integer() }}")

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

# Which operand of `==` a containment scan puts on the left, which decides
# whose refusal is reported when both have one. CPython's list_contains,
# tuplecontains and _PySequence_IterSearch all compare the *candidate* against
# the item, so `{{ nope in [d.nope] }}` names the element's complaint and only
# an element with no opinion of its own hands the question back -- which is
# what makes `{{ nope in [1, d.nope] }}` name `nope` instead. gojja2 had the
# item on the left everywhere, so the item always won. Found by the fuzzer, on
# `{% if nope in 7|urlencode|map(attribute='name')|list %}`.
for _n, _src in [
    ("in_a_list", "{{ nope in [d.nope] }}"),
    ("in_a_tuple", "{{ nope in (d.nope,) }}"),
    ("after_a_plain_element", "{{ nope in [1, d.nope] }}"),
    ("the_other_way_round", "{{ d.nope in [nope] }}"),
    ("in_a_mapped_list", "{{ nope in lst|map(attribute='name')|list }}"),
    ("in_a_values_view", "{{ nope in {'k': d.nope}.values() }}"),
    ("in_a_range", "{{ nope in range(3) }}"),
    ("in_an_empty_list", "{{ nope in [] }}"),
]:
    for _kind in ("default", "strict"):
        _set = {} if _kind == "default" else {"undefined": _kind}
        case(f"membership/element_first_{_n}_{_kind}", _src,
             __settings__=_set, d={"a": 1}, lst=[3, 1, 2])

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

# --- messages nothing had ever produced: the second audit ----------------------
# `make ungraded` counts the error sites no corpus case reaches, and 69 of 402
# were left. Every shape below was measured against CPython before it was written
# down, and every one already agreed -- which is exactly why they are worth a
# case: an ungraded message reads as agreement in every column of the version
# matrix, so nothing would have said when it stopped agreeing.

# A test that takes an argument, called without one. jinja2's parser lets it
# through and the arity check refuses it with CPython's wording, on all four
# routes a test can be called by -- which is what made the guards inside the
# tests themselves unreachable: they answered something else and were deleted.
for _n, _src in [
    ("is_divisibleby_no_argument", "{{ 4 is divisibleby }}"),
    ("is_divisibleby_empty_call", "{{ 4 is divisibleby() }}"),
    ("is_sameas_no_argument", "{{ 4 is sameas }}"),
    ("is_in_no_argument", "{{ 4 is in }}"),
    ("is_eq_no_argument", "{{ 4 is eq }}"),
    ("is_lessthan_no_argument", "{{ 4 is lessthan }}"),
    ("select_test_needing_an_argument", "{{ [1,2]|select('divisibleby')|list }}"),
    ("reject_test_needing_an_argument", "{{ [1,2]|reject('sameas')|list }}"),
    ("selectattr_test_needing_an_argument", "{{ [{'a':1}]|selectattr('a','in')|list }}"),
    ("select_operator_test_needing_an_argument", "{{ [1,2]|select('eq')|list }}"),
    # The parameter names are jinja2's own and a template may use them, which
    # is the other half of the same signature.
    ("is_divisibleby_by_keyword", "{{ 4 is divisibleby(num=2) }}"),
    ("is_sameas_by_keyword", "{{ 4 is sameas(other=4) }}"),
    ("is_in_by_keyword", "{{ 4 is in(seq=[4]) }}"),
    ("is_divisibleby_wrong_keyword", "{{ 4 is divisibleby(nope=2) }}"),
    ("is_eq_takes_no_keywords", "{{ 4 is eq(num=2) }}"),
]:
    case("tests/" + _n, _src)

# A filter whose required argument is missing, and the lazy ones where the
# refusal only surfaces when the generator is walked: printing `{{ 'a'|map }}`
# prints a generator's address in jinja2, so the case has to ask for the list.
for _n, _src in [
    ("replace_no_arguments", "{{ 'a'|replace }}"),
    ("replace_one_argument", "{{ 'a'|replace('b') }}"),
    ("attr_no_argument", "{{ 1|attr }}"),
    ("groupby_no_argument", "{{ [1]|groupby }}"),
    ("map_no_filter", "{{ 'a'|map|list }}"),
    ("map_unknown_filter", "{{ [1]|map('nope')|list }}"),
    ("select_unknown_test", "{{ [1]|select('nope')|list }}"),
    ("reject_unknown_test", "{{ [1]|reject('nope')|list }}"),
    ("selectattr_unknown_test", "{{ [1]|selectattr('x','nope')|list }}"),
]:
    case("errors/" + _n, _src)

# urlencode over something that is not a pair. jinja2 hands the sequence to a
# tuple unpacking, so what fails is Python's unpacking and not the filter.
case("filters/urlencode_not_a_pair", "{{ [1]|urlencode }}")
case("filters/urlencode_pair_too_short", "{{ [[1]]|urlencode }}")
case("filters/urlencode_pair_too_long", "{{ [[1,2,3]]|urlencode }}")

# printf-style formatting with a mapping, and with too few or too many
# arguments. `%(a)s` against a non-mapping and against a mapping missing the key
# are different failures, and the second reports the key alone.
case("methods/percent_mapping_required", "{{ '%(a)s' % 1 }}")
case("methods/percent_mapping_missing_key", "{{ '%(a)s' % {'b': 1} }}")
case("methods/percent_not_enough_arguments", "{{ '%s %s' % (('a',)) }}")
case("methods/percent_too_many_arguments", "{{ '%s' % ((1,2)) }}")
case("methods/percent_star_width", "{{ '%*d' % ((1,2)) }}")
case("methods/format_spec_unmatched_brace", "{{ '{0:{}'.format(1) }}")
case("methods/format_spec_manual_then_auto", "{{ '{0:>{}}'.format(1) }}")
case("methods/format_spec_nested", "{{ '{:{}}'.format(1, '>5') }}")

# The bytes methods that refuse. index and rindex raise where find and rfind
# answer -1, and every one of these had a happy-path case and no refusal.
case("bytes/index_not_found", "{{ 'abc'.encode().index('z'.encode()) }}")
case("bytes/rindex_not_found", "{{ 'abc'.encode().rindex('z'.encode()) }}")
case("bytes/join_rejects_str", "{{ 'x'.encode().join(['a']) }}")
case("bytes/join_rejects_int", "{{ 'x'.encode().join([1]) }}")
case("bytes/split_empty_separator", "{{ 'ab'.encode().split(''.encode()) }}")
case("bytes/rsplit_empty_separator", "{{ 'ab'.encode().rsplit(''.encode()) }}")
case("bytes/maketrans_unequal_length",
     "{{ 'ab'.encode().maketrans('a'.encode(), 'bc'.encode()) }}")

# A non-finite float where an integer is wanted. The value comes from the
# *context*, because `'inf'|float` is a constant expression: jinja2 folds it and
# writes `inf` into its generated Python, where the name does not exist, and the
# template fails with a NameError about jinja2's own output instead
# (docs/divergences.md, "A folded infinity jinja2 writes out").
NONFINITE = {"s_inf": "inf", "s_nan": "nan"}
for _n, _src in [
    ("int_of_infinity", "{{ s_inf|float|int }}"),
    ("int_of_negative_infinity", "{{ (s_inf|float * -1)|int }}"),
    ("int_of_infinity_with_default", "{{ s_inf|float|int(5) }}"),
    # int() of a NaN answers the default instead: jinja2 catches the
    # ValueError and falls back, which is not what it does for an infinity.
    ("int_of_nan", "{{ s_nan|float|int }}"),
    ("round_ceil_of_nan", "{{ (s_nan|float)|round(0,'ceil') }}"),
    ("round_floor_of_nan", "{{ (s_nan|float)|round(0,'floor') }}"),
    ("round_ceil_of_infinity", "{{ (s_inf|float)|round(0,'ceil') }}"),
    ("round_of_infinity", "{{ (s_inf|float)|round }}"),
    ("percent_d_of_nan", "{{ '%d' % (s_nan|float) }}"),
    ("percent_d_of_infinity", "{{ '%d' % (s_inf|float) }}"),
    ("percent_x_of_nan", "{{ '%x' % (s_nan|float) }}"),
    ("percent_c_of_nan", "{{ '%c' % (s_nan|float) }}"),
    ("format_d_of_nan", "{{ '{:d}'.format(s_nan|float) }}"),
    ("range_of_nan", "{{ range(s_nan|float) }}"),
    ("subscript_by_nan", "[{{ [1,2][s_nan|float] }}]"),
    ("truncate_by_nan", "{{ 'abcdef'|truncate(s_nan|float) }}"),
    ("center_by_nan", "{{ 'ab'|center(s_nan|float) }}"),
    ("filesizeformat_of_nan", "{{ (s_nan|float)|filesizeformat }}"),
    ("batch_by_nan", "{{ [1,2]|batch(s_nan|float)|list }}"),
    ("repeat_by_nan", "{{ 'x'*(s_nan|float) }}"),
    ("iterate_a_float", "{{ (s_nan|float)|list }}"),
]:
    case("nonfinite/" + _n, _src, **NONFINITE)

# lipsum's bounds go straight to random.randrange, and an empty range is the one
# thing about lipsum that is not random. The wording moved in 3.12, which is the
# version rule RandrangeNamesItsBounds -- gojja2 carried 3.11's for every
# interpreter, and no case had ever produced the message to say so.
case("errors/lipsum_empty_range", "{{ lipsum(1, false, 5, 3) }}")
case("errors/lipsum_equal_bounds", "{{ lipsum(1, false, 5, 5) }}")

# `%(name)s` against something that is not a dict. CPython's test is "supports
# subscripting", so anything that does gets asked and answers for itself -- a
# list and a range complain about the index type, and only something that cannot
# be subscripted at all gets "format requires a mapping".
case("methods/percent_mapping_range", "{{ '%(a)s' % range(3) }}")
case("methods/percent_mapping_empty_range", "{{ '%(a)s' % range(0) }}")
case("methods/percent_mapping_list", "{{ '%(a)s' % [1,2] }}")
case("methods/percent_mapping_str", "{{ '%(a)s' % 'abc' }}")
case("methods/percent_mapping_bytes", "{{ '%(a)s' % 'x'.encode() }}")
case("methods/percent_mapping_namespace", "{{ '%(a)s' % namespace(a=1) }}")
case("methods/percent_mapping_tuple", "{{ '%(a)s' % ((1,2)) }}")
case("methods/percent_mapping_cycler", "{{ '%(a)s' % cycler('a') }}")
case("methods/percent_star_not_enough", "{{ '%*d' % ((1,)) }}")

# The bytes searches that raise, and the branches a happy path does not reach: a
# window that cannot hold the needle, a separator that is an int rather than
# bytes, and a tuple of prefixes holding something that is not bytes.
case("bytes/index_window_empty", "{{ 'abc'.encode().index('a'.encode(), 5, 2) }}")
case("bytes/rindex_window_empty", "{{ 'abc'.encode().rindex('a'.encode(), 5, 2) }}")
case("bytes/split_int_separator", "{{ 'ab'.encode().split(300) }}")
case("bytes/split_negative_separator", "{{ 'ab'.encode().split(-1) }}")
case("bytes/startswith_tuple_of_str", "{{ 'ab'.encode().startswith(('a',)) }}")
case("bytes/endswith_tuple_of_int", "{{ 'ab'.encode().endswith((1,)) }}")
case("bytes/partition_empty_separator", "{{ 'ab'.encode().partition(''.encode()) }}")
case("bytes/rpartition_empty_separator", "{{ 'ab'.encode().rpartition(''.encode()) }}")

# |random over a dict subscripts it by the *index*, which is a key lookup: it
# finds something only when that integer is one of the keys. A one-entry dict
# makes the draw deterministic, so this is gradable.
# (|random over a one-entry dict is errors/random_mapping_index.)
case("filters/random_dict_integer_key", "{{ {0:'z'}|random }}")

# A structure that contains itself. |tojson refuses it, and printing it prints
# Python's ellipsis. (|pprint names an address, which is the divergence
# docs/divergences.md records.)
case("filters/tojson_circular_list", "{% set l = [] %}{% do l.append(l) %}{{ l|tojson }}",
     __settings__={"extensions": ["do"]})
case("filters/tojson_circular_dict", "{% set d = {} %}{% do d.update(k=d) %}{{ d|tojson }}",
     __settings__={"extensions": ["do"]})
case("filters/print_circular_list", "{% set l = [] %}{% do l.append(l) %}{{ l }}|{{ l|string }}",
     __settings__={"extensions": ["do"]})

# A cycler built from a dynamic splat, which is the only way to reach it with
# nothing to cycle -- and jinja2 refuses at construction either way.
case("errors/cycler_dynamic_empty", "{% set c = cycler(*e) %}{{ c.next() }}", e=[])

# loop.cycle with nothing to cycle. The other cycler -- the global -- already had
# a case; the loop's own method did not.
case("control/loop_cycle_no_items", "{% for i in seq %}{{ loop.cycle() }}{% endfor %}", **SEQ)


# The branches a happy path does not reach, found by reading the *line* the
# ungraded list names rather than the message: the same words are raised from
# two or three sites, and a case that produces them may leave the listed one
# untouched.
#
# searchArg takes bytes *or* an integer byte, so find/index/count reject an int
# outside range(0, 256) where split and partition reject any int at all.
case("bytes/find_int_out_of_range", "{{ 'ab'.encode().find(300) }}")
case("bytes/count_int_out_of_range", "{{ 'ab'.encode().count(300) }}")
case("bytes/index_negative_int", "{{ 'ab'.encode().find(-1) }}")
case("bytes/find_int_in_range", "{{ 'ab'.encode().find(97) }}|{{ 'ab'.encode().count(98) }}")
case("bytes/startswith_int", "{{ 'ab'.encode().startswith(300) }}")
case("bytes/endswith_int", "{{ 'ab'.encode().endswith(300) }}")
case("bytes/partition_int_separator", "{{ 'ab'.encode().partition(300) }}")

# round(x, None) is round(x): it answers an *integer*, which is the only route
# to the conversion that a non-finite float cannot make. round(x, 0) answers a
# float and does not.
case("nonfinite/round_none_of_infinity", "{{ (s_inf|float)|round(none) }}", s_inf="inf")
case("nonfinite/round_none_of_nan", "{{ (s_nan|float)|round(none) }}", s_nan="nan")
case("filters/round_none_is_an_integer", "{{ 2.5|round(none) }}|{{ 3.5|round(none) }}|{{ (-2.5)|round(none) }}")

# int.to_bytes, whose two refusals are CPython's own.
# (300 in one byte is errors/to_bytes_too_big.)
case("methods/to_bytes_wide_int", "{{ (2**100).to_bytes(4,'big') }}")
case("methods/to_bytes_negative", "{{ (-1).to_bytes(1,'big') }}")
# ...and the *signed* range is [-2**(8L-1), 2**(8L-1)-1], not "fits in 8L bits
# once complemented". gojja2 checked the complement, so one byte held -200 and
# 200 alike -- both of which CPython refuses. Zero bytes hold only zero.
for _n, _src in [
    ("signed_low", "{{ (-128).to_bytes(1,'big',signed=true) }}"),
    ("signed_high", "{{ (127).to_bytes(1,'big',signed=true) }}"),
    ("signed_under", "{{ (-129).to_bytes(1,'big',signed=true) }}"),
    ("signed_over", "{{ (128).to_bytes(1,'big',signed=true) }}"),
    ("signed_negative_fits_complemented", "{{ (-200).to_bytes(1,'big',signed=true) }}"),
    ("signed_positive_fits_unsigned", "{{ (200).to_bytes(1,'big',signed=true) }}"),
    ("signed_zero_length", "{{ (0).to_bytes(0,'big',signed=true) }}"),
    ("signed_zero_length_negative", "{{ (-1).to_bytes(0,'big',signed=true) }}"),
    ("signed_zero_length_positive", "{{ (1).to_bytes(0,'big',signed=true) }}"),
    ("signed_two_bytes", "{{ (-32768).to_bytes(2,'big',signed=true) }}|"
     "{{ (32767).to_bytes(2,'little',signed=true) }}|{{ (32768).to_bytes(2,'big',signed=true) }}"),
    ("signed_wide", "{{ (10**30).to_bytes(16,'big',signed=true) }}|"
     "{{ (10**40).to_bytes(16,'big',signed=true) }}"),
]:
    case("methods/to_bytes_" + _n, _src)

# A `*` width or precision reads an argument of its own, so it can run out
# before the conversion does.
case("methods/percent_star_no_arguments", "{{ '%*d' % (()) }}")
case("methods/percent_precision_star_no_arguments", "{{ '%.*f' % (()) }}")

# A replacement field's spec may hold a field of its own, and the brace rules
# inside one are their own.
case("methods/format_nested_spec_unmatched", "{{ '{0:{1}'.format(1,2) }}")
case("methods/format_nested_spec_stray_close", "{{ '{0:{1}}}'.format(1,2) }}")
case("methods/format_nested_spec_after_align", "{{ '{0:>{1}'.format(1,5) }}")

# --- pprint wraps a bytes, which nothing here did ------------------------------
# pprint dispatches on type(obj).__repr__, and CPython has an arm for bytes as
# well as for str: a bytes whose repr does not fit its line is split into
# four-byte-aligned pieces, one literal per line, parenthesised at the top level
# -- which is what makes adjacent literals one value in Python source. gojja2's
# dispatch had str, list, tuple and dict, and everything else fell through to its
# repr on one line.
#
# Found by `make fuzz` on `{{ 'ab'.encode().maketrans(...)|pprint }}`, whose
# table is 256 bytes. The boundaries are here because they are where the rule is:
# four bytes or fewer are printed whole (CPython measures the *value*, not its
# repr), the parentheses appear only at the top level, and the allowance is
# charged against the group that starts the last whole four -- so a length that
# is already a multiple of four never charges it.
case("filters/pprint_bytes_maketrans",
     "{{ 'ab'.encode().maketrans('a'.encode(), 'z'.encode())|pprint }}")
for _n, _src in [
    ("four", "{{ 'abcd'.encode()|pprint }}"),
    ("five", "{{ 'abcde'.encode()|pprint }}"),
    ("fits", "{{ ('x' * 76).encode()|pprint }}"),
    ("boundary", "{{ ('x' * 77).encode()|pprint }}"),
    ("over", "{{ ('x' * 78).encode()|pprint }}"),
    ("long", "{{ ('x' * 100).encode()|pprint }}"),
    ("in_a_list", "{{ [('x' * 100).encode()]|pprint }}"),
    ("in_a_dict", "{{ {'k': ('x' * 100).encode()}|pprint }}"),
    ("nested_twice", "{{ [[('x' * 100).encode()]]|pprint }}"),
    ("in_a_tuple", "{{ (('a' * 100).encode(), 1)|pprint }}"),
    ("two_short", "{{ [('x' * 30).encode(), ('y' * 30).encode()]|pprint }}"),
    ("multibyte", "{{ ('\u00e9' * 60).encode()|pprint }}"),
    ("not_a_multiple_of_four", "{{ ('x' * 101).encode()|pprint }}"),
    ("escapes", "{{ ('a\tb\nc' * 20).encode()|pprint }}"),
]:
    case("filters/pprint_bytes_" + _n, _src)

# The parser runs to completion before CPython sees the module jinja2 generates,
# so a template that is *both* an unbound break and malformed later reports the
# malformed part. gojja2 refused at the tag, which made the first of these a
# complaint about the break where jinja2 names the stray tag.
case("errors/break_before_a_syntax_error", "{% break %}{% else %}",
     __settings__={"extensions": ["loopcontrols"]})
case("errors/break_before_an_unknown_tag", "{% break %}{% nosuchtag %}",
     __settings__={"extensions": ["loopcontrols"]})
case("errors/break_before_a_bad_expression", "{% break %}{{ nope. }}",
     __settings__={"extensions": ["loopcontrols"]})

# `%(name)s` against an *undefined* mapping: the lookup answers the way a
# subscript of it would, so ChainableUndefined hands back another undefined and
# the conversion then refuses it by type -- "%b requires a bytes-like object ...
# not 'ChainableUndefined'" -- while every other class raises its own error
# naming the variable. gojja2 raised for all four, which the generated
# differential found under chainable.
for _kind in ("default", "chainable", "debug", "strict"):
    _set = {} if _kind == "default" else {"undefined": _kind}
    case("undefined/%s_percent_mapping_key" % _kind,
         "[{{ '%(k)s' % nope }}]", __settings__=_set)
    case("undefined/%s_percent_mapping_from_attr" % _kind,
         "[{{ '%(k)s' % (s|attr('nope')) }}]", __settings__=_set, s="x")
    case("undefined/%s_percent_bytes_mapping" % _kind,
         "[{{ ('%(k)b'.encode()) % (s|attr('nope')) }}]", __settings__=_set, s="x")
    case("undefined/%s_percent_integer_verb" % _kind,
         "[{{ '%d' % (s|attr('nope')) }}]", __settings__=_set, s="x")

# A set as the *item* of a containment test. It is unhashable, and this is the
# one place CPython does not stop there: set_contains catches the TypeError,
# makes a frozenset of the key and looks that up, so `{1} in {2}` is False
# rather than a refusal. A dict does not -- `{1} in d.keys()` refuses -- and
# neither does anything else. Found by the coverage-guided fuzzer, on a chained
# `html not in (d.keys() - []) not in (d.keys() - [])`, where the refusal
# reached a template that renders under CPython.
case("dictview/set_in_a_set",
     "{% set d = {'a': 1, 'b': 2} %}{% set e = {} %}"
     "{{ (d.keys() - []) in (d.keys() - []) }}|{{ (e.keys() - []) in (d.keys() - []) }}|"
     "{{ (d.keys() - []) not in (d.keys() - []) }}")
case("dictview/set_in_a_view",
     "{% set d = {'a': 1} %}{{ (d.keys() - []) in d.keys() }}")
case("dictview/set_in_an_items_view",
     "{% set d = {'a': 1} %}{{ (d.keys() - []) in d.items() }}|{{ (d.keys() - []) in d.values() }}")
case("dictview/set_in_a_list", "{% set d = {'a': 1} %}{{ (d.keys() - []) in [1] }}")
case("dictview/set_in_a_dict", "{% set d = {'a': 1} %}{{ (d.keys() - []) in {} }}")
case("dictview/unhashable_in_a_set",
     "{% set d = {'a': 1} %}{{ [1] in (d.keys() - []) }}")
case("dictview/chained_containment_over_sets",
     "{% set d = {'b': 2, 'a': 1, 'C': 3} %}{{ html not in (d.keys() - []) not in (d.keys() - []) }}",
     html="<b>a &amp; b</b>")

# A nested field is a *field*, not a name read up to the next brace: its own
# conversion and spec apply, so `'{0:{1:x}}'.format('y', 15)` formats 15 as hex
# and the outer spec becomes "f" -- an error for a str -- where ignoring the
# nested spec would use 15 as a width. One level and no more: CPython's
# build_string carries a recursion budget of two.
for _n, _src in [
    ("nested_spec_applies", "{{ '{0:{1:x}}'.format('y', 15) }}"),
    ("nested_spec_plain", "{{ '{0:{1:d}}'.format('y', 5) }}"),
    ("nested_conversion", "{{ '{0:{1!r}}'.format('y', 5) }}"),
    ("nested_field_plain", "{{ '{0:{1}}'.format('y', 5) }}"),
    ("nested_spec_in_a_nested_spec", "{{ '{0:{1:{2}}}'.format(1,2,3) }}"),
    ("brace_in_a_nested_name", "{{ '{0:{a{b}}}'.format(1) }}"),
    ("nested_index", "{% set d = {'a': 2} %}{{ '{0:{1[a]}}'.format(1, d) }}"),
    ("nested_attribute", "{{ '{0:{1.real}}'.format(1,2) }}"),
    ("nested_after_align", "{{ '{0:>{1}}'.format(1,5) }}"),
    ("two_nested", "{{ '{:{}{}}'.format(1,'>',5) }}"),
    ("nested_automatic", "{{ '{0:{}}'.format(1,5) }}"),
]:
    case("methods/format_" + _n, _src)

# The object.__format__ fall-back, which accepts only the empty spec.
case("methods/format_none_with_a_spec", "{{ '{:x}'.format(none) }}")
case("methods/format_list_with_a_spec", "{{ '{:x}'.format([1]) }}")
case("methods/format_none_empty_spec", "[{{ '{}'.format(none) }}]|[{{ '{:}'.format(none) }}]")

# The offset in "unexpected char" is in *code points*, because CPython counts
# them in a str: `é{{ $ }}` is 4 there and 5 bytes here. Every template in the
# corpus was ASCII, so the two agreed until the generated differential wrote one
# with 'Ɤꟍ' in it.
case("errors/unexpected_char_after_ascii", "aa{{ $ }}")
case("errors/unexpected_char_after_latin1", "\u00e9{{ $ }}")
case("errors/unexpected_char_after_three_bytes", "\u4e2d\u6587{{ $ }}")
case("errors/unexpected_char_after_astral", "\U0001f600{{ $ }}")
case("errors/unexpected_char_after_a_literal", "{{ '\u00e9' }}{{ $ }}")
case("errors/unexpected_char_inside_a_tag", "{{ 1 }}\u00e9{{ 2 $ }}")

# An `attribute=` that is an *index* reaches Environment.getitem like every other
# lookup, so it indexes whatever the item is -- a list, a tuple, a str, a bytes,
# and an object that presents a sequence. That last arm was missing: a range
# answered undefined, which a `default=` then covered up and `|sum` turned into
# an error. Found by the coverage-guided fuzzer, on
# `[range(3)]|groupby(1, 2, 3)`.
for _n, _src in [
    ("groupby_index_of_a_range", "{{ [range(3)]|groupby(1)|list }}"),
    ("groupby_index_with_a_default", "{{ [range(3)]|groupby(1, 2, 3)|list }}"),
    ("map_index_of_a_range", "{{ [range(3)]|map(attribute=1)|list }}"),
    ("map_index_of_a_range_as_a_string", "{{ [range(3)]|map(attribute='1')|list }}"),
    ("map_negative_index_of_a_range", "{{ [range(3)]|groupby(-1)|list }}"),
    ("map_index_past_a_range", "{{ [range(3)]|map(attribute=9)|list }}"),
    ("map_index_past_a_range_with_a_default",
     "{{ [range(3)]|map(attribute=9, default='d')|list }}"),
    ("sum_index_of_a_range", "{{ [range(3)]|sum(attribute=1) }}"),
    ("min_index_of_a_range", "{{ [range(3)]|min(attribute=1) }}"),
    ("sort_index_of_a_range", "{{ [range(3)]|sort(attribute=1)|list }}"),
    ("selectattr_index_of_a_range", "{{ [range(3)]|selectattr(1)|list }}"),
    ("map_index_of_a_batch", "{{ ['ab'|batch(1)|list]|map(attribute=0)|list }}"),
    ("map_index_of_an_empty_range", "{{ [range(0)]|map(attribute=0)|list }}"),
    # ...and the objects that are *not* sequences still answer undefined.
    ("map_index_of_a_cycler", "{{ [cycler('a','b')]|map(attribute=0)|list }}"),
    ("map_index_of_a_namespace", "{{ [namespace(v=1)]|map(attribute=0)|list }}"),
    ("map_index_of_a_dict", "{{ [{'a':1}]|map(attribute=0)|list }}"),
    ("map_path_through_a_range", "{{ [[range(3)]]|map(attribute='0.1')|list }}"),
]:
    case("filters/" + _n, _src)

# jinja2's operator tests are `operator.eq` and its neighbours, three of them
# under two names -- and half of them had no corpus case at all: `is gt`, `is le`
# and the three aliases were never written. What the aliases share is the
# *function*, which is why an arity error about `equalto` names eq.
for _n, _src in [
    ("eq", "{{ 1 is eq 1 }}|{{ 1 is eq 2 }}|{{ 2 is eq 1 }}|"
     "{{ 'a' is eq 'b' }}|{{ 1.0 is eq 1 }}|{{ [1] is eq [1] }}|"
     "{{ none is eq none }}|{{ 1 is eq(1) }}"),
    ("eq_mismatched", "{{ 1 is eq 'a' }}"),
    ("eq_undefined", "{{ nope is eq 1 }}"),
    ("ne", "{{ 1 is ne 1 }}|{{ 1 is ne 2 }}|{{ 2 is ne 1 }}|"
     "{{ 'a' is ne 'b' }}|{{ 1.0 is ne 1 }}|{{ [1] is ne [1] }}|"
     "{{ none is ne none }}|{{ 1 is ne(1) }}"),
    ("ne_mismatched", "{{ 1 is ne 'a' }}"),
    ("ne_undefined", "{{ nope is ne 1 }}"),
    ("lt", "{{ 1 is lt 1 }}|{{ 1 is lt 2 }}|{{ 2 is lt 1 }}|"
     "{{ 'a' is lt 'b' }}|{{ 1.0 is lt 1 }}|{{ [1] is lt [1] }}|"
     "{{ none is lt none }}|{{ 1 is lt(1) }}"),
    ("lt_mismatched", "{{ 1 is lt 'a' }}"),
    ("lt_undefined", "{{ nope is lt 1 }}"),
    ("le", "{{ 1 is le 1 }}|{{ 1 is le 2 }}|{{ 2 is le 1 }}|"
     "{{ 'a' is le 'b' }}|{{ 1.0 is le 1 }}|{{ [1] is le [1] }}|"
     "{{ none is le none }}|{{ 1 is le(1) }}"),
    ("le_mismatched", "{{ 1 is le 'a' }}"),
    ("le_undefined", "{{ nope is le 1 }}"),
    ("gt", "{{ 1 is gt 1 }}|{{ 1 is gt 2 }}|{{ 2 is gt 1 }}|"
     "{{ 'a' is gt 'b' }}|{{ 1.0 is gt 1 }}|{{ [1] is gt [1] }}|"
     "{{ none is gt none }}|{{ 1 is gt(1) }}"),
    ("gt_mismatched", "{{ 1 is gt 'a' }}"),
    ("gt_undefined", "{{ nope is gt 1 }}"),
    ("ge", "{{ 1 is ge 1 }}|{{ 1 is ge 2 }}|{{ 2 is ge 1 }}|"
     "{{ 'a' is ge 'b' }}|{{ 1.0 is ge 1 }}|{{ [1] is ge [1] }}|"
     "{{ none is ge none }}|{{ 1 is ge(1) }}"),
    ("ge_mismatched", "{{ 1 is ge 'a' }}"),
    ("ge_undefined", "{{ nope is ge 1 }}"),
    ("equalto", "{{ 1 is equalto 1 }}|{{ 1 is equalto 2 }}|{{ 2 is equalto 1 }}|"
     "{{ 'a' is equalto 'b' }}|{{ 1.0 is equalto 1 }}|{{ [1] is equalto [1] }}|"
     "{{ none is equalto none }}|{{ 1 is equalto(1) }}"),
    ("equalto_mismatched", "{{ 1 is equalto 'a' }}"),
    ("equalto_undefined", "{{ nope is equalto 1 }}"),
    ("greaterthan", "{{ 1 is greaterthan 1 }}|{{ 1 is greaterthan 2 }}|{{ 2 is greaterthan 1 }}|"
     "{{ 'a' is greaterthan 'b' }}|{{ 1.0 is greaterthan 1 }}|{{ [1] is greaterthan [1] }}|"
     "{{ none is greaterthan none }}|{{ 1 is greaterthan(1) }}"),
    ("greaterthan_mismatched", "{{ 1 is greaterthan 'a' }}"),
    ("greaterthan_undefined", "{{ nope is greaterthan 1 }}"),
    ("lessthan", "{{ 1 is lessthan 1 }}|{{ 1 is lessthan 2 }}|{{ 2 is lessthan 1 }}|"
     "{{ 'a' is lessthan 'b' }}|{{ 1.0 is lessthan 1 }}|{{ [1] is lessthan [1] }}|"
     "{{ none is lessthan none }}|{{ 1 is lessthan(1) }}"),
    ("lessthan_mismatched", "{{ 1 is lessthan 'a' }}"),
    ("lessthan_undefined", "{{ nope is lessthan 1 }}"),
]:
    case("tests/operator_" + _n, _src)

# Every type test against twenty operands, because one operand each is what the
# corpus had: `tests/kinds` asks `1 is integer` and `'a' is string` and stops
# there, which cannot see the answers that are *surprising* -- a bool is an
# integer and a number in Python, a str is a sequence and an iterable but a
# mapping is not a sequence, a range is both, a Markup is a string, and an
# undefined is falsy for all of them without raising. An even/odd of a non-number
# raises instead, which is the row that is an error rather than a line of
# booleans.
for _n, _src in [
    ("boolean", "{{ 1 is boolean }}|{{ 0 is boolean }}|{{ -1 is boolean }}|{{ 1.5 is boolean }}|{{ true is boolean }}|{{ false is boolean }}|{{ none is boolean }}|{{ 'a' is boolean }}|{{ '' is boolean }}|{{ 'A' is boolean }}|{{ [1] is boolean }}|{{ [] is boolean }}|{{ ((1,)) is boolean }}|{{ {} is boolean }}|{{ {'a':1} is boolean }}|{{ range(3) is boolean }}|{{ nope is boolean }}|{{ ('x'|safe) is boolean }}|{{ dict is boolean }}|{{ d.keys() is boolean }}"),
    ("integer", "{{ 1 is integer }}|{{ 0 is integer }}|{{ -1 is integer }}|{{ 1.5 is integer }}|{{ true is integer }}|{{ false is integer }}|{{ none is integer }}|{{ 'a' is integer }}|{{ '' is integer }}|{{ 'A' is integer }}|{{ [1] is integer }}|{{ [] is integer }}|{{ ((1,)) is integer }}|{{ {} is integer }}|{{ {'a':1} is integer }}|{{ range(3) is integer }}|{{ nope is integer }}|{{ ('x'|safe) is integer }}|{{ dict is integer }}|{{ d.keys() is integer }}"),
    ("float", "{{ 1 is float }}|{{ 0 is float }}|{{ -1 is float }}|{{ 1.5 is float }}|{{ true is float }}|{{ false is float }}|{{ none is float }}|{{ 'a' is float }}|{{ '' is float }}|{{ 'A' is float }}|{{ [1] is float }}|{{ [] is float }}|{{ ((1,)) is float }}|{{ {} is float }}|{{ {'a':1} is float }}|{{ range(3) is float }}|{{ nope is float }}|{{ ('x'|safe) is float }}|{{ dict is float }}|{{ d.keys() is float }}"),
    ("number", "{{ 1 is number }}|{{ 0 is number }}|{{ -1 is number }}|{{ 1.5 is number }}|{{ true is number }}|{{ false is number }}|{{ none is number }}|{{ 'a' is number }}|{{ '' is number }}|{{ 'A' is number }}|{{ [1] is number }}|{{ [] is number }}|{{ ((1,)) is number }}|{{ {} is number }}|{{ {'a':1} is number }}|{{ range(3) is number }}|{{ nope is number }}|{{ ('x'|safe) is number }}|{{ dict is number }}|{{ d.keys() is number }}"),
    ("string", "{{ 1 is string }}|{{ 0 is string }}|{{ -1 is string }}|{{ 1.5 is string }}|{{ true is string }}|{{ false is string }}|{{ none is string }}|{{ 'a' is string }}|{{ '' is string }}|{{ 'A' is string }}|{{ [1] is string }}|{{ [] is string }}|{{ ((1,)) is string }}|{{ {} is string }}|{{ {'a':1} is string }}|{{ range(3) is string }}|{{ nope is string }}|{{ ('x'|safe) is string }}|{{ dict is string }}|{{ d.keys() is string }}"),
    ("sequence", "{{ 1 is sequence }}|{{ 0 is sequence }}|{{ -1 is sequence }}|{{ 1.5 is sequence }}|{{ true is sequence }}|{{ false is sequence }}|{{ none is sequence }}|{{ 'a' is sequence }}|{{ '' is sequence }}|{{ 'A' is sequence }}|{{ [1] is sequence }}|{{ [] is sequence }}|{{ ((1,)) is sequence }}|{{ {} is sequence }}|{{ {'a':1} is sequence }}|{{ range(3) is sequence }}|{{ nope is sequence }}|{{ ('x'|safe) is sequence }}|{{ dict is sequence }}|{{ d.keys() is sequence }}"),
    ("mapping", "{{ 1 is mapping }}|{{ 0 is mapping }}|{{ -1 is mapping }}|{{ 1.5 is mapping }}|{{ true is mapping }}|{{ false is mapping }}|{{ none is mapping }}|{{ 'a' is mapping }}|{{ '' is mapping }}|{{ 'A' is mapping }}|{{ [1] is mapping }}|{{ [] is mapping }}|{{ ((1,)) is mapping }}|{{ {} is mapping }}|{{ {'a':1} is mapping }}|{{ range(3) is mapping }}|{{ nope is mapping }}|{{ ('x'|safe) is mapping }}|{{ dict is mapping }}|{{ d.keys() is mapping }}"),
    ("iterable", "{{ 1 is iterable }}|{{ 0 is iterable }}|{{ -1 is iterable }}|{{ 1.5 is iterable }}|{{ true is iterable }}|{{ false is iterable }}|{{ none is iterable }}|{{ 'a' is iterable }}|{{ '' is iterable }}|{{ 'A' is iterable }}|{{ [1] is iterable }}|{{ [] is iterable }}|{{ ((1,)) is iterable }}|{{ {} is iterable }}|{{ {'a':1} is iterable }}|{{ range(3) is iterable }}|{{ nope is iterable }}|{{ ('x'|safe) is iterable }}|{{ dict is iterable }}|{{ d.keys() is iterable }}"),
    ("callable", "{{ 1 is callable }}|{{ 0 is callable }}|{{ -1 is callable }}|{{ 1.5 is callable }}|{{ true is callable }}|{{ false is callable }}|{{ none is callable }}|{{ 'a' is callable }}|{{ '' is callable }}|{{ 'A' is callable }}|{{ [1] is callable }}|{{ [] is callable }}|{{ ((1,)) is callable }}|{{ {} is callable }}|{{ {'a':1} is callable }}|{{ range(3) is callable }}|{{ nope is callable }}|{{ ('x'|safe) is callable }}|{{ dict is callable }}|{{ d.keys() is callable }}"),
    ("none", "{{ 1 is none }}|{{ 0 is none }}|{{ -1 is none }}|{{ 1.5 is none }}|{{ true is none }}|{{ false is none }}|{{ none is none }}|{{ 'a' is none }}|{{ '' is none }}|{{ 'A' is none }}|{{ [1] is none }}|{{ [] is none }}|{{ ((1,)) is none }}|{{ {} is none }}|{{ {'a':1} is none }}|{{ range(3) is none }}|{{ nope is none }}|{{ ('x'|safe) is none }}|{{ dict is none }}|{{ d.keys() is none }}"),
    ("true", "{{ 1 is true }}|{{ 0 is true }}|{{ -1 is true }}|{{ 1.5 is true }}|{{ true is true }}|{{ false is true }}|{{ none is true }}|{{ 'a' is true }}|{{ '' is true }}|{{ 'A' is true }}|{{ [1] is true }}|{{ [] is true }}|{{ ((1,)) is true }}|{{ {} is true }}|{{ {'a':1} is true }}|{{ range(3) is true }}|{{ nope is true }}|{{ ('x'|safe) is true }}|{{ dict is true }}|{{ d.keys() is true }}"),
    ("false", "{{ 1 is false }}|{{ 0 is false }}|{{ -1 is false }}|{{ 1.5 is false }}|{{ true is false }}|{{ false is false }}|{{ none is false }}|{{ 'a' is false }}|{{ '' is false }}|{{ 'A' is false }}|{{ [1] is false }}|{{ [] is false }}|{{ ((1,)) is false }}|{{ {} is false }}|{{ {'a':1} is false }}|{{ range(3) is false }}|{{ nope is false }}|{{ ('x'|safe) is false }}|{{ dict is false }}|{{ d.keys() is false }}"),
    ("odd", "{{ 1 is odd }}|{{ 0 is odd }}|{{ -1 is odd }}|{{ 1.5 is odd }}|{{ true is odd }}|{{ false is odd }}|{{ none is odd }}|{{ 'a' is odd }}|{{ '' is odd }}|{{ 'A' is odd }}|{{ [1] is odd }}|{{ [] is odd }}|{{ ((1,)) is odd }}|{{ {} is odd }}|{{ {'a':1} is odd }}|{{ range(3) is odd }}|{{ nope is odd }}|{{ ('x'|safe) is odd }}|{{ dict is odd }}|{{ d.keys() is odd }}"),
    ("even", "{{ 1 is even }}|{{ 0 is even }}|{{ -1 is even }}|{{ 1.5 is even }}|{{ true is even }}|{{ false is even }}|{{ none is even }}|{{ 'a' is even }}|{{ '' is even }}|{{ 'A' is even }}|{{ [1] is even }}|{{ [] is even }}|{{ ((1,)) is even }}|{{ {} is even }}|{{ {'a':1} is even }}|{{ range(3) is even }}|{{ nope is even }}|{{ ('x'|safe) is even }}|{{ dict is even }}|{{ d.keys() is even }}"),
    ("lower", "{{ 1 is lower }}|{{ 0 is lower }}|{{ -1 is lower }}|{{ 1.5 is lower }}|{{ true is lower }}|{{ false is lower }}|{{ none is lower }}|{{ 'a' is lower }}|{{ '' is lower }}|{{ 'A' is lower }}|{{ [1] is lower }}|{{ [] is lower }}|{{ ((1,)) is lower }}|{{ {} is lower }}|{{ {'a':1} is lower }}|{{ range(3) is lower }}|{{ nope is lower }}|{{ ('x'|safe) is lower }}|{{ dict is lower }}|{{ d.keys() is lower }}"),
    ("upper", "{{ 1 is upper }}|{{ 0 is upper }}|{{ -1 is upper }}|{{ 1.5 is upper }}|{{ true is upper }}|{{ false is upper }}|{{ none is upper }}|{{ 'a' is upper }}|{{ '' is upper }}|{{ 'A' is upper }}|{{ [1] is upper }}|{{ [] is upper }}|{{ ((1,)) is upper }}|{{ {} is upper }}|{{ {'a':1} is upper }}|{{ range(3) is upper }}|{{ nope is upper }}|{{ ('x'|safe) is upper }}|{{ dict is upper }}|{{ d.keys() is upper }}"),
    ("escaped", "{{ 1 is escaped }}|{{ 0 is escaped }}|{{ -1 is escaped }}|{{ 1.5 is escaped }}|{{ true is escaped }}|{{ false is escaped }}|{{ none is escaped }}|{{ 'a' is escaped }}|{{ '' is escaped }}|{{ 'A' is escaped }}|{{ [1] is escaped }}|{{ [] is escaped }}|{{ ((1,)) is escaped }}|{{ {} is escaped }}|{{ {'a':1} is escaped }}|{{ range(3) is escaped }}|{{ nope is escaped }}|{{ ('x'|safe) is escaped }}|{{ dict is escaped }}|{{ d.keys() is escaped }}"),
    ("defined", "{{ 1 is defined }}|{{ 0 is defined }}|{{ -1 is defined }}|{{ 1.5 is defined }}|{{ true is defined }}|{{ false is defined }}|{{ none is defined }}|{{ 'a' is defined }}|{{ '' is defined }}|{{ 'A' is defined }}|{{ [1] is defined }}|{{ [] is defined }}|{{ ((1,)) is defined }}|{{ {} is defined }}|{{ {'a':1} is defined }}|{{ range(3) is defined }}|{{ nope is defined }}|{{ ('x'|safe) is defined }}|{{ dict is defined }}|{{ d.keys() is defined }}"),
    ("undefined", "{{ 1 is undefined }}|{{ 0 is undefined }}|{{ -1 is undefined }}|{{ 1.5 is undefined }}|{{ true is undefined }}|{{ false is undefined }}|{{ none is undefined }}|{{ 'a' is undefined }}|{{ '' is undefined }}|{{ 'A' is undefined }}|{{ [1] is undefined }}|{{ [] is undefined }}|{{ ((1,)) is undefined }}|{{ {} is undefined }}|{{ {'a':1} is undefined }}|{{ range(3) is undefined }}|{{ nope is undefined }}|{{ ('x'|safe) is undefined }}|{{ dict is undefined }}|{{ d.keys() is undefined }}"),
]:
    case("tests/kinds_" + _n, "{% set d = {'k': 1} %}" + _src)

# Whether a subscript folds is decided by its *argument* before the base's
# undefinedness decides what the fold answers. jinja2 folds a node only when
# every part of it is constant -- Name.as_const is Impossible -- so
# `((3)[-2:])[n]` is left for the render, where a slice bypasses
# Environment.getitem and raises. Chaining on the base first folded it to an
# undefined under ChainableUndefined and the comparison above it to False: a
# TypeError swallowed at compile time, which the coverage-guided fuzzer found.
#
# Every shape under every class, because the classes differ exactly here: only
# chainable reaches through, and only the fold path can swallow.
for _kind in ("default", "chainable", "debug", "strict"):
    _set = {} if _kind == "default" else {"undefined": _kind}
    for _n, _src in [
    ("slice_of_an_int_by_name", "{{ ((3)[-2:])[n3] == 0 == 0 }}"),
    ("undefined_by_name", "{{ (nope)[n3] }}"),
    ("undefined_by_constant", "{{ (nope)[0] }}"),
    ("chained_undefined_by_name", "{{ (nope.a)[n3] }}"),
    ("undefined_sliced_by_name", "{{ (nope)[n3:] }}"),
    ("undefined_sliced_by_constant", "{{ (nope)[0:1] }}"),
    ("int_sliced_by_name", "{{ ((3)[n3:]) }}"),
    ("int_sliced_by_name_then_indexed", "{{ ((3)[n3:])[0] }}"),
    ("folded_undefined_by_name", "{{ (none.missing)[n3] }}"),
    ("folded_undefined_by_constant", "{{ (none.missing)[1] }}"),
    ("folded_undefined_attribute", "{{ (none.missing).x }}"),
    ]:
        case("fold/%s_%s" % (_kind, _n), _src, __settings__=_set, n3=3)

# The filter *aliases*, which are the same function under a second name and had
# almost no cases: `|d` had none at all, `|e` four, `|count` two. What an alias
# shares is the function, so its arity error names the original -- do_default(),
# escape(), len() -- and `|count` is Python's len rather than a filter of its
# own.
case("filters/alias_default", "{{ nope|d('x') }}|{{ 1|d('x') }}|{{ ''|d('x', true) }}|{{ none|d('x') }}")
case("filters/alias_default_bare", "[{{ nope|d }}]|[{{ nope|d(boolean=true) }}]")
case("filters/alias_escape", "{{ '<b>'|e }}|{{ ('<b>'|safe)|e }}|{{ 1|e }}|{{ none|e }}")
case("filters/alias_count", "{{ [1,2]|count }}|{{ 'ab'|count }}|{{ {}|count }}|{{ range(5)|count }}")
case("errors/alias_count_of_an_int", "{{ 1|count }}")
case("errors/alias_default_too_many", "{{ nope|d('x', 1, 2) }}")
case("errors/alias_escape_with_an_argument", "{{ 'a'|e(1) }}")
case("errors/alias_count_with_an_argument", "{{ [1]|count(1) }}")

# ...and the sequence filters the corpus had two or five cases for.
case("filters/rejectattr_with_a_test", "{{ users|rejectattr('age', 'eq', 30)|list }}", **USERS)
case("filters/rejectattr_bare", "{{ users|rejectattr('name')|list }}", **USERS)
case("filters/selectattr_with_a_test",
     "{{ users|selectattr('age', 'gt', 25)|map(attribute='name')|list }}", **USERS)
case("filters/min_max_by_attribute",
     "{{ users|min(attribute='age') }}|{{ users|max(attribute='age') }}", **USERS)
case("filters/min_max_of_an_empty_sequence", "[{{ []|min }}]|[{{ []|max }}]")
case("errors/min_takes_no_default", "{{ []|min(default='d') }}")
case("filters/min_case_sensitive", "{{ [3,1,2]|min(case_sensitive=true) }}")
case("filters/slice_with_fill", "{{ range(5)|slice(2)|list }}|{{ range(5)|slice(2, 'x')|list }}")
case("filters/slice_more_slices_than_items", "{{ [1,2,3]|slice(4)|list }}")

# The same census, per *method*: .copy() had no case at all, .extend(), .lower()
# and .reverse() one each. A method with one case is a method whose *refusals*
# are ungraded -- list.reverse() takes no arguments, .extend() wants an iterable,
# .fromkeys() likewise -- and its edges with them.
case("methods/list_copy_is_a_copy",
     "{% set l = [1,2] %}{% set c = l.copy() %}{% do l.append(3) %}{{ c }}|{{ l }}",
     __settings__={"extensions": ["do"]})
case("methods/dict_copy_is_a_copy",
     "{% set d = {'a':1} %}{% set c = d.copy() %}{% do d.update(b=2) %}{{ c }}|{{ d }}",
     __settings__={"extensions": ["do"]})
case("methods/copy_of_a_literal", "{{ [1,2].copy() }}|{{ {'a':1}.copy() }}")
case("errors/str_has_no_copy", "{{ 'ab'.copy() }}")
case("methods/list_extend",
     "{% set l = [1] %}{% do l.extend([2,3]) %}{{ l }}|{% do l.extend('ab') %}{{ l }}",
     __settings__={"extensions": ["do"]})
case("errors/list_extend_an_int", "{% set l = [1] %}{% do l.extend(1) %}{{ l }}",
     __settings__={"extensions": ["do"]})
case("methods/list_reverse",
     "{% set l = [3,1,2] %}{% do l.reverse() %}{{ l }}|{% do l.reverse(1) %}{{ l }}",
     __settings__={"extensions": ["do"]})
case("methods/str_lower_cases",
     "{{ 'AB'.lower() }}|{{ '\u00c9'.lower() }}|{{ '\u00df'.lower() }}|[{{ ''.lower() }}]")
case("methods/str_capitalize_cases",
     "{{ 'aB c'.capitalize() }}|[{{ ''.capitalize() }}]|{{ '\u00df'.capitalize() }}")
case("methods/isascii",
     "{{ 'ab'.isascii() }}|{{ '\u00e9'.isascii() }}|{{ ''.isascii() }}|"
     "{{ 'ab'.encode().isascii() }}")
case("methods/str_rindex", "{{ 'abc'.rindex('b') }}|{{ 'abcb'.rindex('b') }}")
case("errors/str_rindex_missing", "{{ 'abc'.rindex('z') }}")
case("methods/str_rfind", "{{ 'abc'.rfind('z') }}|{{ 'abcb'.rfind('b') }}")
case("methods/str_expandtabs",
     "{{ 'a\tb'.expandtabs() }}|{{ 'a\tb'.expandtabs(4) }}|{{ 'a\tb'.expandtabs(0) }}")
# (setdefault, popitem and fromkeys already had a case each under these names;
# what was missing was the refusal below.)
case("errors/dict_fromkeys_an_int", "{{ {}.fromkeys(1) }}")
case("methods/list_insert_clamps",
     "{% set l = [1,3] %}{% do l.insert(1, 2) %}{{ l }}|{% do l.insert(99, 9) %}{{ l }}|"
     "{% do l.insert(-99, 0) %}{{ l }}", __settings__={"extensions": ["do"]})
case("methods/str_istitle",
     "{{ 'A b'.istitle() }}|{{ 'A B'.istitle() }}|{{ ''.istitle() }}|{{ '1a'.istitle() }}")
case("methods/str_zfill",
     "{{ '5'.zfill(3) }}|{{ '-5'.zfill(3) }}|{{ '+5'.zfill(3) }}|{{ 'ab'.zfill(1) }}|"
     "[{{ ''.zfill(2) }}]")

# ...and `==` is decided the same way: the same length, and every element of one
# in the other. Two *empty* views of different types are equal, and a view equals
# the set a difference built from it -- where the kind check would call them
# different things and stop. A values view is set-like in neither engine, so two
# of them are equal only by identity. Found by the coverage-guided fuzzer, on
# `{{ ed.items() == ed.keys() != 0 }}`.
case("dictview/empty_views_are_equal",
     "{% set e = {} %}{{ e.items() == e.keys() }}|{{ e.keys() == e.items() }}|"
     "{{ e.items() != e.keys() }}|{{ e.items() == e.keys() != 0 }}")
case("dictview/views_of_different_kinds",
     "{% set d = {'a':1} %}{{ d.items() == d.keys() }}|{{ d.keys() == d.items() }}")
case("dictview/views_of_the_same_kind",
     "{% set d = {'a':1} %}{% set e = {'a':1} %}{% set f = {'a':2} %}"
     "{{ d.keys() == e.keys() }}|{{ d.items() == e.items() }}|"
     "{{ d.keys() == f.keys() }}|{{ d.items() == f.items() }}")
case("dictview/values_are_equal_only_by_identity",
     "{% set d = {'a':1} %}{% set e = {} %}{{ d.values() == d.values() }}|"
     "{{ e.values() == e.values() }}|{{ e.values() == e.keys() }}")
case("dictview/a_view_equals_a_set",
     "{% set d = {'a':1} %}{% set e = {} %}{{ d.keys() == (d.keys() - []) }}|"
     "{{ d.items() == (d.keys() - []) }}|{{ e.keys() == (e.keys() - []) }}|"
     "{{ (e.keys() - []) == e.keys() }}")
case("dictview/a_view_against_other_types",
     "{% set e = {} %}{{ e.keys() == [] }}|{{ e.keys() == {} }}|{{ e.items() == 0 }}")

# `{% set v | f %}` wraps the filter's *result* under autoescape --
# `(Markup if autoescape else identity)(...)` -- so a filter that answers
# something other than a string leaves a Markup of its str() behind, not the
# value: `{% set v | length %}abc{% endset %}{{ v + 1 }}` is a TypeError there and
# was 4 here. With escaping off the value keeps its type, which is what
# identity() means. Found by teaching the generator to write the form at all.
for _n, _src in [
    ("length", "{% set v | length %}abc{% endset %}[{{ v }}][{{ v is string }}]"),
    ("length_arithmetic", "{% set v | length %}abc{% endset %}[{{ v + 1 }}]"),
    ("list", "{% set v | list %}a'b{% endset %}[{{ v }}]"),
    ("int", "{% set v | int %}12{% endset %}[{{ v }}][{{ v is string }}]"),
    ("first", "{% set v | first %}ab{% endset %}[{{ v }}]"),
    ("upper", "{% set v | upper %}a'b{% endset %}[{{ v }}]"),
    ("no_filter", "{% set v %}a'b{% endset %}[{{ v }}]"),
]:
    case("escape/set_block_filter_" + _n,
         "{% autoescape true %}" + _src + "{% endautoescape %}")
    case("control/set_block_filter_" + _n,
         "{% autoescape false %}" + _src + "{% endautoescape %}")

# ...and that Markup() *stringifies*, so a filter that answers an undefined
# raises there under StrictUndefined -- `{% set v | first %}{% endset %}` over
# an empty body is "No first item, sequence was empty." with escaping on and
# assigns quietly with it off, where identity() keeps the undefined whole. The
# wrap used the plain str(), which renders a strict undefined as "", so the
# refusal only arrived if something later printed v -- and these shapes never
# do. Graded under all four classes: the wrap must not make the default class
# refuse, and it must not swallow the debug class's message either.
for _n, _src in [
    ("first_empty", "{% set v | first %}{% endset %}"),
    ("last_empty", "{% set v | last %}{% endset %}"),
    ("first_empty_printed", "{% set v | first %}{% endset %}[{{ v }}]"),
    ("first_empty_length", "{% set v | first %}{% endset %}[{{ v|length }}]"),
    ("map_attribute", "{% set v | map(attribute='x')|first %}{% endset %}"),
    ("groupby_first", "{% set v | groupby('x')|first %}{% endset %}"),
]:
    for _kind in ("chainable", "default", "debug", "strict"):
        case(f"escape/set_block_filter_undefined_{_n}_{_kind}",
             "{% autoescape true %}" + _src + "{% endautoescape %}",
             __settings__={"undefined": _kind})
        case(f"control/set_block_filter_undefined_{_n}_{_kind}",
             "{% autoescape false %}" + _src + "{% endautoescape %}",
             __settings__={"undefined": _kind})

# jinja2's code generator collects every block in a pre-pass, before it generates
# a line, so a template that both defines a block twice *and* extends from
# somewhere it may not says "block 'a' defined twice" whatever order the two sit
# in. gojja2 checked the dependencies first.
case("errors/block_twice_and_extends_in_a_macro",
     "{% block a %}{% endblock %}{% macro mm(x) %}{% extends 'base.txt' %}"
     "{% block a %}{% endblock %}{% endmacro %}", __templates__={"base.txt": "B"})
case("errors/block_twice_and_extends_in_a_block",
     "{% block a %}{% extends 'base.txt' %}{% block a %}{% endblock %}{% endblock %}",
     __templates__={"base.txt": "B"})
case("errors/extends_in_a_macro",
     "{% macro m() %}{% extends 'base.txt' %}{% endmacro %}",
     __templates__={"base.txt": "B"})
case("errors/extends_in_a_loop",
     "{% for i in [1] %}{% extends 'base.txt' %}{% endfor %}",
     __templates__={"base.txt": "B"})

# Constant folding is not a pass over the tree -- it is something jinja2's code
# *generator* does to each node it writes out, so a node the generator never
# writes is never folded, and an error the fold would have raised never
# happens. Three rules follow, and gojja2 had none of them.
#
# The first: below an {% extends %} the child's own body prints nothing, and
# the generator leaves those print tags out entirely (`if self.has_known_
# extends: return`) rather than guarding them. So a fold that refuses -- here a
# subscript of an int, which is undefined, and `and` asks an undefined for its
# truth -- refuses only where the tag is still written. A {% block %}, a macro
# and a {% set %} body are written whatever the extends says, because none of
# them writes to the template's own stream.
_EXT = "{% extends 'base.txt' %}"


def _refuses(k=0):
    # A subscript of an int is undefined, and `and` asks an undefined for its
    # truth -- which a StrictUndefined answers with an error, out of the fold.
    # The index names the position, so a case can say *which* fold refused.
    return "{{ (0 ** 0)[%d] and 0 }}" % k


for _n, _src in [
    ("below_extends", _EXT + "@X@"),
    ("above_extends", "@X@" + _EXT),
    ("below_extends_in_a_branch", _EXT + "{% if true %}@X@{% endif %}"),
    ("below_extends_in_a_loop", _EXT + "{% for i in [1] %}@X@{% endfor %}"),
    ("below_extends_in_a_block", _EXT + "{% block a %}@X@{% endblock %}"),
    ("below_extends_in_a_macro", _EXT + "{% macro m() %}@X@{% endmacro %}"),
    ("below_extends_in_a_set_block", _EXT + "{% set q %}@X@{% endset %}"),
    ("below_extends_in_a_filter_block",
     _EXT + "{% filter upper %}@X@{% endfilter %}"),
    ("below_extends_in_a_with", _EXT + "{% with %}@X@{% endwith %}"),
    ("below_extends_in_an_autoescape",
     _EXT + "{% autoescape true %}@X@{% endautoescape %}"),
    ("below_a_conditional_extends", "{% if true %}" + _EXT + "{% endif %}@X@"),
    ("below_an_extends_a_branch_skips", "{% if false %}" + _EXT + "{% endif %}@X@"),
    ("below_extends_beside_a_literal", _EXT + "{{ 'a' }}@X@"),
    ("below_extends_after_a_block", _EXT + "{% block a %}{% endblock %}@X@"),
    ("in_a_block_above_extends", "{% block a %}@X@{% endblock %}" + _EXT),
    ("below_extends_in_an_assignment", _EXT + "{% set q = (0 ** 0)[0] and 0 %}"),
    ("below_extends_in_a_condition", _EXT + "{% if (0 ** 0)[0] and 0 %}x{% endif %}"),
    ("below_extends_in_a_loops_iterable",
     _EXT + "{% for i in [(0 ** 0)[0] and 0] %}x{% endfor %}"),
]:
    case("fold/" + _n, _src.replace("@X@", _refuses()),
         __settings__={"undefined": "strict"},
         __templates__={"base.txt": "B[{% block a %}{% endblock %}]"})

# The second: a {% block %} body is generated *after* the whole root body, from
# the flat list the generator collects up front -- so of two folds that refuse,
# the one in the root body wins however late it stands, and between two blocks
# the list's order decides. The list is find_all's, which is a pre-order walk,
# so a block nested inside another comes after its parent's own body rather
# than where it is written. Each case names a different subscript in each
# position, because what is being graded is *which* of the two refused.
for _n, _src in [
    ("root_after_a_block", "{% block a %}@7@{% endblock %}{% set q = (0 ** 0)[9] and 0 %}"),
    ("root_before_a_block", "{% set q = (0 ** 0)[9] and 0 %}{% block a %}@7@{% endblock %}"),
    ("a_macro_stays_where_it_is",
     "{% macro m() %}@7@{% endmacro %}{% set q = (0 ** 0)[9] and 0 %}"),
    ("a_block_loses_to_a_macro",
     "{% block a %}@7@{% endblock %}{% macro m() %}@9@{% endmacro %}"),
    ("a_block_loses_to_an_include",
     "{% block a %}@7@{% endblock %}{% include (0 ** 0)[9] and 0 %}"),
    ("a_nested_block_is_last",
     "{% block a %}{% block b %}@8@{% endblock %}@6@{% endblock %}{% block c %}@7@{% endblock %}"),
    ("a_nested_block_before_a_later_one",
     "{% block a %}{% block b %}@8@{% endblock %}{% endblock %}{% block c %}@7@{% endblock %}"),
]:
    _t = _src
    for _k in (6, 7, 8, 9):
        _t = _t.replace("@%d@" % _k, _refuses(_k))
    case("fold/order_" + _n, _t, __settings__={"undefined": "strict"},
         __templates__={"base.txt": "B"})

# The third: the two refusals the generator raises *before* it folds anything,
# and the one it raises in the middle. Collecting the blocks is a pre-pass, so
# a name defined twice is reported wherever the second definition stands; the
# non-top-level {% extends %} is a failure of the generator's own walk, so it
# beats a fold below it and loses to one above.
case("fold/order_block_twice_beats_a_fold",
     "{% block a %}{% endblock %}{% block a %}{% endblock %}" + _refuses(7),
     __settings__={"undefined": "strict"})
case("fold/order_block_twice_beats_an_earlier_fold",
     _refuses(7) + "{% block a %}{% endblock %}{% block a %}{% endblock %}",
     __settings__={"undefined": "strict"})
case("fold/order_a_macros_extends_beats_a_fold",
     "{% macro m() %}" + _EXT + "{% endmacro %}" + _refuses(7),
     __settings__={"undefined": "strict"}, __templates__={"base.txt": "B"})
case("fold/order_a_fold_beats_a_macros_extends",
     _refuses(7) + "{% macro m() %}" + _EXT + "{% endmacro %}",
     __settings__={"undefined": "strict"}, __templates__={"base.txt": "B"})
case("fold/order_a_blocks_extends_beats_a_fold_in_a_later_block",
     "{% block a %}" + _EXT + "{% endblock %}{% block b %}" + _refuses(7) + "{% endblock %}",
     __settings__={"undefined": "strict"}, __templates__={"base.txt": "B"})
case("fold/order_a_root_fold_beats_a_blocks_extends",
     "{% block a %}" + _EXT + "{% endblock %}" + _refuses(7),
     __settings__={"undefined": "strict"}, __templates__={"base.txt": "B"})

# The same three rules again, for the other thing the generator does as it
# writes a node out: look the filter or test up, and refuse a name the
# environment does not have. A print tag below a root-level {% extends %} is
# not written, so `{% extends 'base.txt' %}{{ 1|nosuch }}` renders the parent
# where gojja2 refused to compile it -- a template jinja2 accepts, which is the
# worse half of the divergence. The lookups inside a {% block %} happen where
# its body is generated, after the root body, so an unknown name there loses to
# one anywhere above.
for _n, _src in [
    ("below_extends", _EXT + "{{ 1|nosuchA }}"),
    ("below_extends_a_test", _EXT + "{{ 1 is nosuchtest }}"),
    ("below_extends_in_a_loop", _EXT + "{% for i in [1] %}{{ 1|nosuchA }}{% endfor %}"),
    ("below_extends_in_a_filter_block",
     _EXT + "{% filter upper %}{{ 1|nosuchA }}{% endfilter %}"),
    ("below_extends_in_a_with", _EXT + "{% with %}{{ 1|nosuchA }}{% endwith %}"),
    ("below_extends_in_a_branch", _EXT + "{% if true %}{{ 1|nosuchA }}{% endif %}"),
    ("below_extends_in_a_block", _EXT + "{% block a %}{{ 1|nosuchA }}{% endblock %}"),
    ("below_extends_in_a_macro", _EXT + "{% macro m() %}{{ 1|nosuchA }}{% endmacro %}"),
    ("below_extends_in_a_set_block", _EXT + "{% set q %}{{ 1|nosuchA }}{% endset %}"),
    ("below_extends_in_an_assignment", _EXT + "{% set q = 1|nosuchA %}"),
    ("below_extends_in_a_loops_iterable",
     _EXT + "{% for i in [1]|nosuchA %}x{% endfor %}"),
    ("below_a_conditional_extends", "{% if true %}" + _EXT + "{% endif %}{{ 1|nosuchA }}"),
    ("above_extends", "{{ 1|nosuchA }}" + _EXT),
]:
    case("fold/generated_lookup_" + _n, _src,
         __templates__={"base.txt": "B[{% block a %}{% endblock %}]"})

for _n, _src in [
    ("root_after_a_block",
     "{% block a %}{{ 1|nosuchA }}{% endblock %}{% set q = 1|nosuchB %}"),
    ("root_before_a_block",
     "{% set q = 1|nosuchB %}{% block a %}{{ 1|nosuchA }}{% endblock %}"),
    ("a_block_loses_to_a_macro",
     "{% block a %}{{ 1|nosuchA }}{% endblock %}"
     "{% macro m() %}{{ 1|nosuchB }}{% endmacro %}"),
    ("a_macro_stays_where_it_is",
     "{% macro m() %}{{ 1|nosuchB }}{% endmacro %}"
     "{% block a %}{{ 1|nosuchA }}{% endblock %}"),
    ("a_nested_block_is_last",
     "{% block a %}{% block b %}{{ 1|nosuchB }}{% endblock %}{{ 1|nosuchA }}"
     "{% endblock %}{% block c %}{{ 1|nosuchC }}{% endblock %}"),
    ("a_loop_beats_a_block",
     "{% for i in [1] %}{{ 1|nosuchA }}{% endfor %}"
     "{% block a %}{{ 1|nosuchB }}{% endblock %}"),
    ("a_branch_defers_and_a_block_does_not",
     "{% if true %}{{ 1|nosuchA }}{% endif %}{% block a %}{{ 1|nosuchB }}{% endblock %}"),
]:
    case("fold/order_lookup_" + _n, _src)

# ...and the two are one walk, not two: jinja2 folds an expression and looks the
# names in it up as it writes that one node out, so a template with a fold that
# refuses and a filter that does not exist names whichever the generator reaches
# first. Two passes cannot do that however carefully their walks are kept in
# step, which is why there is one here.
def _bad(k=7):
    return "(0 ** 0)[%d] and 0" % k


for _n, _src in [
    ("a_lookup_before_a_fold", "{{ 1|nosuchA }}{{ @B@ }}"),
    ("a_fold_before_a_lookup", "{{ @B@ }}{{ 1|nosuchA }}"),
    ("a_lookup_in_an_assignment_before_a_fold", "{% set q = 1|nosuchA %}{{ @B@ }}"),
    ("a_fold_before_a_lookup_in_an_assignment", "{{ @B@ }}{% set q = 1|nosuchA %}"),
    ("a_block_fold_after_a_root_lookup",
     "{% block a %}{{ @B@ }}{% endblock %}{{ 1|nosuchA }}"),
    ("a_block_lookup_after_a_root_fold",
     "{% block a %}{{ 1|nosuchA }}{% endblock %}{{ @B@ }}"),
    ("a_macro_lookup_before_a_fold",
     "{% macro m() %}{{ 1|nosuchA }}{% endmacro %}{{ @B@ }}"),
    ("a_branch_defers_its_lookup", "{% if true %}{{ 1|nosuchA }}{% endif %}{{ @B@ }}"),
    ("a_lookup_before_a_bad_extends",
     "{{ 1|nosuchA }}{% for i in [1] %}{% extends 'base.txt' %}{% endfor %}"),
    ("a_bad_extends_before_a_lookup",
     "{% for i in [1] %}{% extends 'base.txt' %}{% endfor %}{{ 1|nosuchA }}"),
]:
    case("fold/order_mixed_" + _n, _src.replace("@B@", _bad()),
         __settings__={"undefined": "strict"}, __templates__={"base.txt": "B"})

# The order inside one statement is the generator's too, and it is not the
# order the tag is written in: a loop's test becomes a function of its own,
# written before the loop that calls it, and a {% filter %}, a {% set %} with a
# body and a {% call %} all buffer their body first and write what consumes it
# afterwards.
for _n, _src in [
    ("a_loops_test_before_its_iterable", "{% for i in [1]|nosuchA if 1|nosuchB %}x{% endfor %}"),
    ("a_loops_test_folds_first", "{% for i in [1]|nosuchA if @B@ %}x{% endfor %}"),
    ("a_loops_iterable_before_its_body",
     "{% for i in [1]|nosuchA %}{{ 1|nosuchC }}{% endfor %}"),
    ("a_loops_body_before_its_else",
     "{% for i in [1] %}{{ 1|nosuchC }}{% else %}{{ 1|nosuchD }}{% endfor %}"),
    ("a_filter_blocks_body_before_its_filter",
     "{% filter nosuchA %}{{ 1|nosuchC }}{% endfilter %}"),
    ("a_set_blocks_body_before_its_filter",
     "{% set q | nosuchA %}{{ 1|nosuchC }}{% endset %}"),
    ("a_call_blocks_body_before_its_call",
     "{% call m(1|nosuchA) %}{{ 1|nosuchC }}{% endcall %}"),
    ("a_call_blocks_signature_before_its_body",
     "{% call(x=1|nosuchA) m() %}{{ 1|nosuchC }}{% endcall %}"),
    ("a_macros_signature_before_its_body",
     "{% macro m(a=1|nosuchA) %}{{ 1|nosuchC }}{% endmacro %}"),
    ("a_withs_values_before_its_body",
     "{% with x = 1|nosuchA %}{{ 1|nosuchC }}{% endwith %}"),
    ("a_filter_blocks_fold_before_a_later_lookup",
     "{% filter upper %}{{ @B@ }}{% endfilter %}{{ 1|nosuchA }}"),
]:
    case("fold/order_within_" + _n, _src.replace("@B@", _bad()),
         __settings__={"undefined": "strict"})

# A macro parameter that was not provided binds to an undefined carrying a
# *hint* -- jinja2's `undefined(f"parameter {name!r} was not provided")` -- not
# to one named after the parameter. The difference only speaks when the undefined
# does: the message under StrictUndefined, the hint's own rendering under
# DebugUndefined. Found by the generated differential once it could write a
# `{% call(p) %}` signature, where the caller supplies no argument at all.
for _kind in ("default", "chainable", "debug", "strict"):
    _set = {} if _kind == "default" else {"undefined": _kind}
    for _n, _src in [
        ("missing_parameter", "{% macro m(x) %}[{{ x }}]{% endmacro %}{{ m() }}"),
        ("missing_second_parameter", "{% macro m(x, y) %}[{{ y }}]{% endmacro %}{{ m(1) }}"),
        ("missing_parameter_reached_through",
         "{% macro m(x) %}[{{ x.attr }}]{% endmacro %}{{ m() }}"),
        ("missing_parameter_is_defined",
         "{% macro m(x) %}[{{ x is defined }}]{% endmacro %}{{ m() }}"),
        ("missing_parameter_default",
         "{% macro m(x) %}[{{ x|default('d') }}]{% endmacro %}{{ m() }}"),
        ("caller_argument_not_provided",
         "{% macro takes() %}<{{ caller() }}>{% endmacro %}"
         "{% call(p) takes() %}{{ p }}{% endcall %}"),
    ]:
        case("macro/%s_%s" % (_kind, _n), _src, __settings__=_set)

# A `{% filter %}` block's result is appended to jinja2's buffer rather than
# emitted, so it is neither escaped nor finalized. It usually makes no difference
# -- a filter over a Markup answers Markup -- but `|join` answers a plain str
# holding the body's escapes, and escaping it again turned `&#39;` into
# `&amp;#39;`.
for _n, _src in [
    ("join", "{% filter join('-') %}{{ \"it's\" }}{% endfilter %}"),
    ("join_include", "{% filter wordwrap(4) %}{% include 'inc.txt' %}{% endfilter %}"),
    ("upper_literal", "{% filter upper %}<b>{% endfilter %}"),
    ("upper_printed", "{% filter upper %}{{ '<b>' }}{% endfilter %}"),
    ("replace_ampersand", "{% filter replace('a','&') %}a{% endfilter %}"),
    ("trim", "{% filter trim %} <b> {% endfilter %}"),
    ("safe", "{% filter safe %}<b>{% endfilter %}"),
]:
    case("escape/filter_block_" + _n,
         "{% autoescape true %}" + _src + "{% endautoescape %}",
         __templates__={"inc.txt": "<{{ n|default('?') }}>"}, n=3)
    case("control/filter_block_" + _n,
         "{% autoescape false %}" + _src + "{% endautoescape %}",
         __templates__={"inc.txt": "<{{ n|default('?') }}>"}, n=3)

# --- a dict view compares as a set --------------------------------------------
# `<`, `<=`, `>` and `>=` between two views are the *subset* relation, not an
# ordering: CPython's dictview_richcompare answers containment, and neither
# `a < b` nor `a > b` need hold. The lengths decide first, which is why a longer
# view is not a proper subset without an element ever being looked at -- so
# `{'x': [1]}.items() > {}.keys()` is False rather than a complaint about the
# unhashable list.
#
# A values view is not set-like in either engine: its elements need be neither
# unique nor hashable, so the four raise for it. gojja2 raised for *every* pair
# of views, which the generated differential found through a chained comparison
# -- `[0o17] not in nested.items() < ed.keys()` -- where CPython answers False
# and carries on to fail somewhere else entirely.
_VIEWS = "{% set a = {'x': 1, 'y': 2} %}{% set b = {'x': 1} %}{% set e = {} %}"
for _n, _src in [
    ("keys_lt_keys", "{{ a.keys() < b.keys() }}|{{ b.keys() < a.keys() }}"),
    ("keys_le_keys", "{{ a.keys() <= b.keys() }}|{{ b.keys() <= a.keys() }}"),
    ("keys_gt_keys", "{{ a.keys() > b.keys() }}|{{ b.keys() > a.keys() }}"),
    ("keys_ge_keys", "{{ a.keys() >= b.keys() }}|{{ b.keys() >= a.keys() }}"),
    ("keys_lt_itself", "{{ a.keys() < a.keys() }}|{{ a.keys() <= a.keys() }}"),
    ("empty_keys", "{{ e.keys() < a.keys() }}|{{ e.keys() <= e.keys() }}|{{ a.keys() > e.keys() }}"),
    ("items_lt_items", "{{ a.items() < b.items() }}|{{ b.items() < a.items() }}"),
    ("items_le_items_same_key_other_value",
     "{% set c = {'x': 9} %}{{ b.items() <= c.items() }}|{{ b.items() <= b.items() }}"),
    ("items_across_views", "{{ a.keys() < b.items() }}|{{ a.items() < b.keys() }}"),
    ("values_are_not_a_set", "{{ a.values() < b.values() }}"),
    ("values_on_the_right", "{{ a.keys() < b.values() }}"),
    ("values_on_the_left", "{{ a.values() < b.keys() }}"),
    ("keys_against_a_list", "{{ a.keys() < [1] }}"),
    ("keys_against_a_string", "{{ a.keys() < 'x' }}"),
    ("set_against_a_view", "{{ (b.keys() - []) < a.keys() }}|{{ (a.keys() - []) < a.keys() }}"),
    ("view_against_a_set", "{{ a.keys() < (a.keys() - []) }}|{{ b.keys() <= (a.keys() - []) }}"),
    ("set_against_a_set", "{{ (b.keys() - []) < (a.keys() - []) }}"),
]:
    case("dictview/" + _n, _VIEWS + _src)
# An unhashable value in an items view: the lengths answer before anything is
# hashed one way, and the containment looks the key up rather than hashing the
# pair the other.
case("dictview/items_with_an_unhashable_value",
     "{% set a = {'x': [1]} %}{% set b = {'x': [1], 'y': 2} %}"
     "{{ a.items() <= b.items() }}|{{ a.items() < b.items() }}|{{ b.items() < a.items() }}")
case("dictview/unhashable_against_empty_keys",
     "{% set a = {'x': [1]} %}{% set e = {} %}{{ a.items() > e.keys() }}|{{ a.items() <= e.keys() }}")

# --- how a replacement field ends ---------------------------------------------
# CPython's parse_field reads the *name* up to the first '}', ':' or '!', then a
# conversion of exactly one character, then a spec whose braces it counts. Each
# of those three stages has its own complaint when the string runs out, and
# gojja2 had one message for all of them past the name: `'{:d'` and `'{0!r'`
# hold no nested field and CPython still calls them an unmatched brace.
#
# The conversion is one character and whatever character it is -- `'{!}'` takes
# '}' as the conversion and then finds nothing closing the field, which is why it
# reports the unmatched brace rather than the missing conversion. Found by the
# generated differential once the soak could draw custom delimiters, on
# `${- '{:z#]'.format(1e20) -}$`.
for _n, _src in [
    ("spec_ends_the_string", "{{ '{:z'.format(1) }}"),
    ("empty_spec_ends_the_string", "{{ '{:'.format(1) }}"),
    ("numbered_spec_ends_the_string", "{{ '{0:'.format(1) }}"),
    ("align_ends_the_string", "{{ '{0:>'.format(1) }}"),
    ("width_ends_the_string", "{{ '{0:>5'.format(1) }}"),
    ("type_ends_the_string", "{{ '{:d'.format(1) }}"),
    ("conversion_ends_the_string", "{{ '{!r'.format(1) }}"),
    ("numbered_conversion_ends_the_string", "{{ '{0!r'.format(1) }}"),
    ("bang_ends_the_string", "{{ '{0!'.format(1) }}"),
    ("conversion_then_junk", "{{ '{0!rr}'.format(1) }}"),
    ("conversion_then_bracket", "{{ '{0![a'.format(1) }}"),
    ("brace_is_the_conversion", "{{ '{!}'.format(1) }}"),
    ("colon_is_the_conversion", "{{ '{!:}'.format(1) }}"),
    ("brace_in_the_name", "{{ '{a{b}'.format() }}"),
    ("field_in_the_name", "{{ '{0{1}}'.format(1,2) }}"),
    ("brace_in_an_index", "{% set d = {'a{b': 1} %}{{ '{0[a{b]}'.format(d) }}"),
    ("name_ends_the_string", "{{ '{ '.format(1) }}"),
    ("index_ends_the_string", "{% set d = {'a': 1} %}{{ '{0[a'.format(d) }}"),
    ("attribute_ends_the_string", "{{ '{0.a'.format(1) }}"),
]:
    case("methods/format_field_" + _n, _src)

# pprint has an arm for a set too, and it is the same layout a list gets: one
# element per line in braces. What is different is the order -- CPython sorts
# with pprint._safe_key, the values' own ordering where they have one, so a set
# of integers pprints as 0, 1, 2 where this set's *repr* order (which is by
# repr, for a total order over mixed types) would give 0, 1, 10. Only the
# homogeneous case is gradable: _safe_key falls back to the object's id, which no
# other process can reproduce. See docs/divergences.md.
case("filters/pprint_set_of_strings",
     "{% set d = {'0': 0, '1': 1, '2': 2, '3': 3, '4': 4, '5': 5, '6': 6, '7': 7, '8': 8, '9': 9, '10': 10, '11': 11, '12': 12, '13': 13, '14': 14, '15': 15, '16': 16, '17': 17, '18': 18, '19': 19, '20': 20, '21': 21, '22': 22, '23': 23, '24': 24, '25': 25, '26': 26, '27': 27, '28': 28, '29': 29, '30': 30, '31': 31, '32': 32, '33': 33, '34': 34, '35': 35, '36': 36, '37': 37, '38': 38, '39': 39} %}{{ (d.keys() - [])|pprint }}")
case("filters/pprint_set_of_integers",
     "{% set d = {0: 0, 1: 1, 2: 2, 3: 3, 4: 4, 5: 5, 6: 6, 7: 7, 8: 8, 9: 9, 10: 10, 11: 11, 12: 12, 13: 13, 14: 14, 15: 15, 16: 16, 17: 17, 18: 18, 19: 19, 20: 20, 21: 21, 22: 22, 23: 23, 24: 24, 25: 25, 26: 26, 27: 27, 28: 28, 29: 29, 30: 30, 31: 31, 32: 32, 33: 33, 34: 34, 35: 35, 36: 36, 37: 37, 38: 38, 39: 39} %}{{ (d.keys() - [])|pprint }}")
# (A set short enough to fit on one line is printed by its repr, which is the
# hash order CPython randomises per process -- so there is no short-set case
# here. filters/pprint_set_empty is the exception: set() has one spelling.)
# A view has two attributes, and gojja2 had neither: `isdisjoint`, which only
# the set-like views have -- a values view is not a set, its elements need be
# neither unique nor hashable -- and `mapping`, the read-only proxy of the dict
# every view carries since 3.10. `{{ d.keys().mapping }}` printed nothing.
#
# The proxy is a dict in nearly every way a template can observe: it indexes,
# iterates, sizes, is `is mapping`, and equals the dict in either position
# because it delegates __eq__ to it. It differs in four: its repr says
# mappingproxy, it has only the five read-only methods, json.dumps refuses it,
# and hashing it complains about the *dict* it wraps. Found by coverage --
# dictView.GetAttr was 0%.
_DV = "{% set d = {'a': 1, 'b': 2} %}"
for _n, _src in [
    ("keys_mapping", "{{ d.keys().mapping }}"),
    ("items_mapping", "{{ d.items().mapping }}"),
    ("values_mapping", "{{ d.values().mapping }}"),
    ("mapping_through_attr", "{{ d.keys()|attr('mapping') }}"),
    ("mapping_repr", "{{ d.keys().mapping|pprint }}"),
    ("mapping_str", "{{ d.keys().mapping|string }}"),
    ("mapping_indexes", "{% set m = d.keys().mapping %}[{{ m['a'] }}][{{ m['z'] }}]"),
    ("mapping_iterates", "{% for k in d.keys().mapping %}[{{ k }}]{% endfor %}"),
    ("mapping_length", "{{ d.keys().mapping|length }}"),
    ("mapping_is_mapping",
     "{% set m = d.keys().mapping %}{{ m is mapping }}{{ m is sequence }}{{ m is iterable }}"),
    ("mapping_equals_the_dict",
     "{% set m = d.keys().mapping %}{{ m == d }}{{ d == m }}{{ m == d.keys().mapping }}"),
    ("mapping_dictsort", "{{ d.keys().mapping|dictsort }}"),
    ("mapping_items_filter", "{{ d.keys().mapping|items|list }}"),
    ("mapping_methods",
     "{% set m = d.keys().mapping %}{{ m.keys()|list }}{{ m.values()|list }}"
     "{{ m.items()|list }}{{ m.get('a') }}{{ m.get('z', 9) }}{{ m.copy() }}"),
    ("mapping_has_no_mapping", "[{{ d.keys().mapping.mapping }}]"),
    ("mapping_has_no_update", "{{ d.keys().mapping.update({'q': 1}) }}"),
    ("mapping_copy_is_a_dict", "{{ d.keys().mapping.copy().__class__ }}"),
    ("mapping_class", "{{ d.keys().mapping.__class__ }}"),
    ("mapping_tojson", "{{ d.keys().mapping|tojson }}"),
    ("mapping_is_unhashable", "{{ d.keys().mapping in d }}"),
    ("mapping_copy_arity", "{{ d.keys().mapping.copy(1) }}"),
    ("mapping_get_arity", "{{ d.keys().mapping.get() }}"),
    ("isdisjoint_true", "{{ d.keys().isdisjoint([1]) }}"),
    ("isdisjoint_false", "{{ d.keys().isdisjoint(['a']) }}"),
    ("isdisjoint_empty", "{{ d.keys().isdisjoint([]) }}"),
    ("isdisjoint_a_string", "{{ d.keys().isdisjoint('a') }}"),
    ("isdisjoint_a_dict", "{{ d.keys().isdisjoint(d) }}"),
    ("isdisjoint_unhashable", "{{ d.keys().isdisjoint([['x']]) }}"),
    ("isdisjoint_items_pair", "{{ d.items().isdisjoint([('a', 1)]) }}"),
    ("isdisjoint_items_list", "{{ d.items().isdisjoint([['a', 1]]) }}"),
    ("isdisjoint_values_has_none", "{{ d.values().isdisjoint([1]) }}"),
    ("isdisjoint_no_argument", "{{ d.keys().isdisjoint() }}"),
    ("isdisjoint_two_arguments", "{{ d.keys().isdisjoint(1, 2) }}"),
    ("isdisjoint_a_keyword", "{{ d.keys().isdisjoint(x=1) }}"),
    ("isdisjoint_not_iterable", "{{ d.keys().isdisjoint(5) }}"),
    # The proxy's class *is* constructible, unlike a view's, and what it
    # accepts is PyMapping_Check minus list and tuple -- so a string is a
    # mapping here and another proxy is one too.
    ("proxy_class_constructs", "{% set C = d.keys().mapping.__class__ %}{{ C({'a': 1}) }}"),
    ("proxy_class_needs_an_argument", "{{ d.keys().mapping.__class__() }}"),
    ("proxy_class_names_its_argument",
     "{% set C = d.keys().mapping.__class__ %}{{ C(mapping={'q': 1}) }}"),
    ("proxy_class_takes_one",
     "{% set C = d.keys().mapping.__class__ %}{{ C({'a': 1}, {}) }}"),
    ("proxy_class_refuses_a_list", "{% set C = d.keys().mapping.__class__ %}{{ C([1]) }}"),
    ("proxy_class_refuses_a_tuple", "{% set C = d.keys().mapping.__class__ %}{{ C((1,)) }}"),
    ("proxy_class_refuses_an_int", "{% set C = d.keys().mapping.__class__ %}{{ C(5) }}"),
    ("proxy_class_takes_a_string",
     "{% set C = d.keys().mapping.__class__ %}{{ C('ab') }}|{{ C('ab')|pprint }}"
     "|{{ C('ab')|list }}|{{ C('ab')|length }}"),
    ("proxy_class_nests", "{% set C = d.keys().mapping.__class__ %}{{ C(C({'a': 1}))|pprint }}"),
    ("view_class_is_not_constructible", "{{ d.keys().__class__() }}"),
]:
    case("dictview/" + _n, _DV + _src)

case("filters/pprint_set_empty", "{% set d = {'a': 1} %}{{ (d.keys() - d.keys())|pprint }}")
case("filters/pprint_set_in_a_list",
     "{% set d = {'0': 0, '1': 1, '2': 2, '3': 3, '4': 4, '5': 5, '6': 6, '7': 7, '8': 8, '9': 9, '10': 10, '11': 11, '12': 12, '13': 13, '14': 14, '15': 15, '16': 16, '17': 17, '18': 18, '19': 19, '20': 20, '21': 21, '22': 22, '23': 23, '24': 24, '25': 25, '26': 26, '27': 27, '28': 28, '29': 29, '30': 30, '31': 31, '32': 32, '33': 33, '34': 34, '35': 35, '36': 36, '37': 37, '38': 38, '39': 39} %}{{ [(d.keys() - [])]|pprint }}")
case("filters/pprint_set_long_strings",
     "{% set d = {'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa': 1,"
     " 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb': 2} %}{{ (d.keys() - [])|pprint }}")

# --- a guard over an arm that can fail ----------------------------------------
# The dataflow analysis answers whether a variable can stop the render, and a
# *guard* decides whether whatever it guards runs at all. Two shapes had it
# wrong, both found by rendering the analysis's negatives:
#
#   - an arm holding only an attribute access. Reaching through an undefined
#     raises, so the guard decides whether the render fails -- and Getattr had
#     been left out of the walk's list of what can fail, beside its sibling
#     Getitem.
#   - under StrictUndefined, an arm that merely *reads* a name. Reading one that
#     was not passed raises there, where every other class renders it.
#
# The cases are here so the rule is graded by testdata/nameflow as well as by
# the probe: what each records is the effect set for `c`.
case("dataflow/guard_over_getattr", "{% if c %}{{ nope.attr }}{% endif %}ok", c=1)
case("dataflow/guard_over_getattr_false", "{% if c %}{{ nope.attr }}{% endif %}ok", c=0)
case("dataflow/guard_over_getitem", "{% if c %}{{ nope['a'] }}{% endif %}ok", c=1)
case("dataflow/guard_over_text", "{% if c %}text{% endif %}ok", c=1)
case("dataflow/guard_over_a_name_strict", "{% if c %}{{ nope }}{% endif %}ok",
     __settings__={"undefined": "strict"}, c=0)
case("dataflow/guard_over_a_name_default", "{% if c %}{{ nope }}{% endif %}ok", c=1)
case("dataflow/loop_guard_over_getattr",
     "{% for i in seq %}{% if c %}{{ nope.attr }}{% endif %}{% endfor %}ok", c=0, **SEQ)

# --- a guard over a body that becomes a value ---------------------------------
# A branch inside a `{% set %}` with a body puts nothing of its own in the
# document, and the analysis said so -- but it decides what the *captured
# string* is, and whatever consumes that string can fail for one branch and not
# the other. `{% set v | last %}{% if c %}xx{% endif %}{% endset %}` is "x" when
# c is truthy and an undefined when it is not, which raises the moment it is
# printed under StrictUndefined and raises in `{% filter last %}` under every
# class. The analysis said "the render cannot fail because of c", which is the
# one answer it must never give; the syntax differential found it by rendering
# that negative with two values of c.
#
# The fix is an edge of its own, not a data edge: c decides what v holds without
# being part of it, so FLOW and REQUIRED travel back along it and OUTPUT does
# not. Each case pins which of the three the guard gets.
case("dataflow/capture_guard_filtered_in_the_block",
     "{% set fv | last %}{% if c %}xx{% endif %}{% endset %}[{{ fv }}]", c=1)
case("dataflow/capture_guard_filtered_at_the_use",
     "{% set fv %}{% if c %}xx{% endif %}{% endset %}[{{ fv|last }}]", c=1)
case("dataflow/capture_guard_printed_plainly",
     "{% set fv %}{% if c %}xx{% endif %}{% endset %}[{{ fv }}]", c=1)
case("dataflow/capture_guard_never_used",
     "{% set fv | last %}{% if c %}xx{% endif %}{% endset %}ok", c=1)
case("dataflow/capture_guard_strict",
     "{% set fv | last %}{% if c %}xx{% endif %}{% endset %}[{{ fv }}]",
     __settings__={"undefined": "strict"}, c=1)
case("dataflow/capture_guard_in_a_filter_block",
     "{% filter last %}{% if c %}xx{% endif %}{% endfilter %}", c=1)
case("dataflow/capture_guard_in_a_macro",
     "{% macro m() %}{% if c %}xx{% endif %}{% endmacro %}[{{ m()|last }}]", c=1)
case("dataflow/capture_guard_in_a_macro_printed",
     "{% macro m() %}{% if c %}xx{% endif %}{% endmacro %}[{{ m() }}]", c=1)
case("dataflow/capture_loop_test_steers",
     "{% set fv | last %}{% for i in seq if c %}x{% endfor %}{% endset %}[{{ fv }}]",
     c=1, **SEQ)
case("dataflow/capture_loop_length_steers",
     "{% set fv | last %}{% for i in seq %}x{% endfor %}{% endset %}[{{ fv }}]", **SEQ)
case("dataflow/capture_guard_nested",
     "{% set outer %}{% set inner | last %}{% if c %}xx{% endif %}{% endset %}"
     "[{{ inner }}]{% endset %}{{ outer }}", c=1)
case("dataflow/capture_guard_in_an_else",
     "{% set fv | last %}{% if c %}{% else %}yy{% endif %}{% endset %}[{{ fv }}]", c=1)

# --- newline_sequence, which nothing had ever graded -------------------------
# jinja2 normalises the newlines it finds in the *template* -- both in data and
# inside a string literal -- to the environment's newline_sequence, before the
# parser ever sees them. Nothing that arrives from the context is touched.
#
# The option had validation tests on both sides and not one case about what it
# does. It changes what a literal *is*, so `{{ 'a\r\nb'|length }}` is 4 under
# "\r\n" and 3 under "\n", and `|list` of it holds a different number of
# elements. A filter that re-joins lines does not use it: do_indent splits with
# splitlines() and joins with "\n" whatever the setting says.
_NEWLINE_BODIES = [
    ("data_mixed", "x\ny|x\r\ny|x\ry"),
    ("literal_mixed", "{{ 'a\nb' }}|{{ 'a\rb' }}|{{ 'a\r\nb' }}"),
    ("literal_length", "{{ 'a\r\nb'|length }}|{{ 'a\nb'|length }}|{{ 'a\rb'|length }}"),
    ("literal_list", "{{ 'a\r\nb'|list }}"),
    ("context_untouched", "{{ v }}"),
    ("block_body", "{% if 1 %}\na\r\nb\rc\n{% endif %}"),
    ("loop_body", "{% for i in [1,2] %}\r\n{{ i }}{% endfor %}"),
    ("indent_joins_with_lf", "{{ 'a\r\nb'|indent(2, true) }}"),
    ("splitlines", "{{ 'a\r\nb'.splitlines() }}"),
    ("wordwrap", "{{ 'a\r\nb'|wordwrap(1) }}"),
    ("raw_block", "{% raw %}a\r\nb{% endraw %}"),
    ("around_a_comment", "a\r\n{#c#}\r\nb"),
    ("trailing", "a\r\n"),
]
for _n, _src in _NEWLINE_BODIES:
    for _label, _seq in (("lf", "\n"), ("crlf", "\r\n"), ("cr", "\r")):
        case(f"newline/{_n}_{_label}", _src,
             __settings__={"newline_sequence": _seq}, v="a\r\nb")
# ...and the interaction with the two settings that also eat newlines.
case("newline/keep_trailing_crlf", "a\r\n",
     __settings__={"newline_sequence": "\r\n", "keep_trailing_newline": True}, v="")
case("newline/trim_blocks_crlf", "{% if 1 %}\r\na{% endif %}\r\nb",
     __settings__={"newline_sequence": "\r\n", "trim_blocks": True}, v="")
case("newline/lstrip_blocks_cr", "  {% if 1 %}\ra{% endif %}",
     __settings__={"newline_sequence": "\r", "lstrip_blocks": True}, v="")


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
