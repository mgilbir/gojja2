#!/usr/bin/env python3
# Copyright 2026 The gojja2 Authors
# SPDX-License-Identifier: Apache-2.0
"""Generate the parser conformance corpus for internal/parser/parser_test.go.

jinja2's AST is reachable through Environment.parse, so the tree can be graded
directly rather than inferred from rendered output. Both sides dump to the same
S-expression, using repr() for every string -- which gojja2 already reproduces
exactly -- so agreement of the dumps is agreement of the trees.
"""

from __future__ import annotations

import json
import sys
from pathlib import Path

from jinja2 import Environment, nodes

OUT = Path(__file__).resolve().parents[2] / "internal" / "parser" / "testdata" / "parse.jsonl"

BINOPS = {
    nodes.Add: "add", nodes.Sub: "sub", nodes.Mul: "mul", nodes.Div: "div",
    nodes.FloorDiv: "floordiv", nodes.Mod: "mod", nodes.Pow: "pow",
    nodes.And: "and", nodes.Or: "or",
}
UNARYOPS = {nodes.Not: "not", nodes.Neg: "neg", nodes.Pos: "pos"}


def q(s: str) -> str:
    return repr(s)


def seq(items) -> str:
    return "[" + " ".join(dump(i) for i in items) + "]"


def node(name: str, *fields) -> str:
    return "(" + " ".join([name, *fields]) + ")"


def dump(n) -> str:  # noqa: C901 - a flat dispatch reads better than a class tree
    if n is None:
        return "nil"
    if isinstance(n, bool):
        return "True" if n else "False"
    if isinstance(n, str):
        return q(n)
    if isinstance(n, tuple):
        # A (name, alias) pair in a from-import list.
        return node("alias", q(n[0]), q(n[1]))

    cls = type(n)
    if cls in BINOPS:
        return node("binop", BINOPS[cls], dump(n.left), dump(n.right))
    if cls in UNARYOPS:
        return node("unaryop", UNARYOPS[cls], dump(n.node))

    if isinstance(n, nodes.Template):
        return node("template", seq(n.body))
    if isinstance(n, nodes.Output):
        return node("output", seq(n.nodes))
    if isinstance(n, nodes.TemplateData):
        return node("templatedata", q(n.data))
    if isinstance(n, nodes.Const):
        return node("const", repr(n.value))
    if isinstance(n, nodes.Name):
        # ctx "param" is how jinja2 marks macro parameters and `with`
        # targets; both are stores as far as the tree shape goes.
        return node("name", q(n.name), "True" if n.ctx in ("store", "param") else "False")
    if isinstance(n, nodes.NSRef):
        return node("nsref", q(n.name), q(n.attr))
    if isinstance(n, nodes.Tuple):
        return node("tuple", seq(n.items))
    if isinstance(n, nodes.List):
        return node("list", seq(n.items))
    if isinstance(n, nodes.Dict):
        return node("dict", seq(n.items))
    if isinstance(n, nodes.Pair):
        return node("pair", dump(n.key), dump(n.value))
    if isinstance(n, nodes.Keyword):
        return node("keyword", q(n.key), dump(n.value))
    if isinstance(n, nodes.CondExpr):
        return node("condexpr", dump(n.test), dump(n.expr1), dump(n.expr2))
    if isinstance(n, nodes.Concat):
        return node("concat", seq(n.nodes))
    if isinstance(n, nodes.Compare):
        return node("compare", dump(n.expr), seq(n.ops))
    if isinstance(n, nodes.Operand):
        return node("operand", q(n.op), dump(n.expr))
    if isinstance(n, nodes.Getattr):
        return node("getattr", dump(n.node), q(n.attr))
    if isinstance(n, nodes.Getitem):
        return node("getitem", dump(n.node), dump(n.arg))
    if isinstance(n, nodes.Slice):
        return node("slice", dump(n.start), dump(n.stop), dump(n.step))
    if isinstance(n, nodes.Call):
        return node("call", dump(n.node), *call_args(n))
    if isinstance(n, nodes.Filter):
        return node("filter", dump(n.node), q(n.name), *call_args(n))
    if isinstance(n, nodes.Test):
        return node("test", dump(n.node), q(n.name), *call_args(n))

    if isinstance(n, nodes.For):
        return node("for", dump(n.target), dump(n.iter), seq(n.body),
                    seq(n.else_), dump(n.test), dump(n.recursive))
    if isinstance(n, nodes.If):
        return node("if", dump(n.test), seq(n.body), seq(n.elif_), seq(n.else_))
    if isinstance(n, nodes.Assign):
        return node("assign", dump(n.target), dump(n.node))
    if isinstance(n, nodes.AssignBlock):
        return node("assignblock", dump(n.target), dump(n.filter), seq(n.body))
    if isinstance(n, nodes.Macro):
        return node("macro", q(n.name), seq(n.args), seq(n.defaults), seq(n.body))
    if isinstance(n, nodes.CallBlock):
        return node("callblock", dump(n.call), seq(n.args), seq(n.defaults), seq(n.body))
    if isinstance(n, nodes.FilterBlock):
        return node("filterblock", seq(n.body), dump(n.filter))
    if isinstance(n, nodes.With):
        return node("with", seq(n.targets), seq(n.values), seq(n.body))
    if isinstance(n, nodes.Block):
        return node("block", q(n.name), seq(n.body), dump(n.scoped), dump(n.required))
    if isinstance(n, nodes.Extends):
        return node("extends", dump(n.template))
    if isinstance(n, nodes.Include):
        return node("include", dump(n.template), dump(n.with_context),
                    dump(n.ignore_missing))
    if isinstance(n, nodes.Import):
        return node("import", dump(n.template), q(n.target), dump(n.with_context))
    if isinstance(n, nodes.FromImport):
        # jinja2 records a bare string when no `as` was written and a pair
        # when one was; gojja2 always records both halves. Normalise to the
        # pair so the trees compare on meaning rather than on encoding.
        names = [(x, x) if isinstance(x, str) else x for x in n.names]
        return node("fromimport", dump(n.template), seq(names), dump(n.with_context))
    if isinstance(n, nodes.ExprStmt):
        return node("exprstmt", dump(n.node))
    if isinstance(n, nodes.Break):
        return node("break")
    if isinstance(n, nodes.Continue):
        return node("continue")

    # `{% autoescape %}` is a Scope wrapping a ScopedEvalContextModifier in
    # jinja2, and a single node in gojja2. Normalise to the latter.
    if isinstance(n, nodes.Scope):
        if len(n.body) == 1 and isinstance(n.body[0], nodes.ScopedEvalContextModifier):
            inner = n.body[0]
            if len(inner.options) == 1 and inner.options[0].key == "autoescape":
                return node("autoescape", dump(inner.options[0].value), seq(inner.body))
        return node("scope", seq(n.body))

    raise AssertionError(f"parse corpus has no dump for {type(n).__name__}")


def call_args(n) -> list[str]:
    return [seq(n.args), seq(n.kwargs), dump(n.dyn_args), dump(n.dyn_kwargs)]


# (source, options) -- options name the jinja2 extensions the case needs.
CASES: list[tuple[str, dict]] = []


def case(src: str, **opts) -> None:
    CASES.append((src, opts))


# literals and operators
case("{{ 1 }}{{ 1.5 }}{{ 'a' }}{{ true }}{{ True }}{{ none }}{{ None }}{{ false }}")
case("{{ 'a' 'b' 'c' }}")
case("{{ 2 ** 3 ** 2 }}")
case("{{ -2 ** 2 }}")
case("{{ 1 + 2 * 3 - 4 / 5 // 6 % 7 }}")
case("{{ 'a' ~ 1 + 2 ~ 'b' }}")
case("{{ a ~ b ~ c }}")
case("{{ not a and b or c }}")
case("{{ 1 < 2 <= 3 > 4 >= 5 == 6 != 7 }}")
case("{{ a in b }}{{ a not in b }}")
case("{{ a if b else c }}")
case("{{ a if b }}")
case("{{ a if b else c if d else e }}")
case("{{ -x|abs }}")
case("{{ (-x)|abs }}")
case("{{ +1 }}{{ --1 }}")

# containers, slices, trailing commas
case("{{ [] }}{{ [1] }}{{ [1,] }}{{ [1, 2,] }}")
case("{{ {} }}{{ {'a': 1} }}{{ {'a': 1,} }}")
case('{% set d = {1:"a",} %}{{ d[1] }}')
case("{{ () }}{{ (1,) }}{{ (1, 2) }}{{ 1, 2 }}")
case("{{ a[1] }}{{ a[1:2] }}{{ a[:2] }}{{ a[1:] }}{{ a[::2] }}{{ a[::-1] }}{{ a[:] }}")
case("{{ a[1:2:3] }}{{ a[1, 2] }}{{ a[x:y, z:] }}")
case("{{ a.b.c }}{{ a.0 }}{{ a['b'] }}")

# calls, filters, tests
case("{{ f() }}{{ f(1) }}{{ f(1, b=2) }}{{ f(*a) }}{{ f(**k) }}{{ f(*a, **k) }}")
case("{{ f(1,) }}")
case("{{ x|a|b(1)|c(d=2) }}")
case("{{ x|a.b }}")
case("{{ x is defined }}{{ x is not defined }}{{ x is divisibleby 3 }}")
case("{{ x is sameas y.z }}")
case("{{ x is eq(1) }}")
case("{{ f()() }}{{ (x|f)() }}")

# statements
case("{% for x in y %}a{% endfor %}")
case("{% for x, (y, z) in w %}a{% endfor %}")
case("{% for x in y if x %}a{% else %}b{% endfor %}")
case("{% for x in y recursive %}a{% endfor %}")
case("{% for x in y if x recursive %}a{% endfor %}")
case("{% if a %}1{% elif b %}2{% elif c %}3{% else %}4{% endif %}")
case("{% if a %}1{% endif %}")
case("{% if a, b %}1{% endif %}")
case("{% set x = 1 %}")
case("{% set x, y = 1, 2 %}")
case("{% set ns.x = 1 %}")
case("{% set x %}body{% endset %}")
case("{% set x | upper %}body{% endset %}")
case("{% with a = 1, b = 2 %}x{% endwith %}")
case("{% with %}x{% endwith %}")
case("{% block b %}x{% endblock %}")
case("{% block b %}x{% endblock b %}")
case("{% block b scoped %}x{% endblock %}")
case("{% block b required %}{% endblock %}")
case("{% block b scoped required %}  {% endblock %}")
case("{% extends 'a.html' %}")
case("{% include 'a.html' %}")
case("{% include 'a.html' ignore missing %}")
case("{% include 'a.html' ignore missing without context %}")
case("{% include 'a.html' with context %}")
case("{% import 'a.html' as m %}")
case("{% import 'a.html' as m with context %}")
case("{% from 'a.html' import a %}")
case("{% from 'a.html' import a, b as c %}")
case("{% from 'a.html' import a with context %}")
case("{% from 'a.html' import a, b with context %}")
case("{% macro m(a, b=1, c=2) %}x{% endmacro %}")
case("{% macro m() %}{% endmacro %}")
case("{% call m() %}x{% endcall %}")
case("{% call(a, b=1) m() %}x{% endcall %}")
case("{% filter upper %}x{% endfilter %}")
case("{% filter upper|trim %}x{% endfilter %}")
case("{% print 1 %}{% print 1, 2 %}")
case("{% autoescape true %}x{% endautoescape %}")
case("{% raw %}{{ x }}{% endraw %}")
case("{% do x.append(1) %}", do=True)
case("{% for x in y %}{% break %}{% continue %}{% endfor %}", loopcontrols=True)

# multi-line tags
case("{%\n  if\n  a\n%}x{%\nendif\n%}")
case("{{\n  a\n  +\n  b\n}}")
case("{% for\n  x\n  in\n  y\n%}a{% endfor %}")

# nesting and data runs
case("a{{ b }}c{% if d %}e{% endif %}f")
case("{% for a in b %}{% for c in d %}{{ a }}{% endfor %}{% endfor %}")

# error cases
case("{% endfor %}")
case("{% for x in y %}")
case("{% if a %}")
case("{% nope %}")
case("{% for x in y %}{% endif %}")
case("{{ }}")
case("{{ 1 + }}")
case("{% set 1 = 2 %}")
case("{% set a.b = 1 %}")
case("{% set a|b = 1 %}")
case("{% block b-c %}{% endblock %}")
case("{% block b required %}text{% endblock %}")
case("{% macro m(a=1, b) %}{% endmacro %}")
case("{% from 'a' import _x %}")
case("{% call x %}{% endcall %}")
case("{{ a is is b }}")
case("{{ f(a=1, 2) }}")
case("{{ f(**k, 1) }}")
case("{{ a. }}")
case("{% do x %}")
case("{% break %}")
case("{% for x in y %}{% break %}{% endfor %}")


def parse(src: str, opts: dict) -> dict:
    extensions = []
    if opts.get("do"):
        extensions.append("jinja2.ext.do")
    if opts.get("loopcontrols"):
        extensions.append("jinja2.ext.loopcontrols")
    env = Environment(extensions=extensions)
    try:
        tree = env.parse(src, "<parse>")
    except Exception as exc:  # noqa: BLE001 - the failure is the expectation
        return {
            "err": type(exc).__name__,
            "msg": getattr(exc, "message", None) or str(exc),
            "line": getattr(exc, "lineno", 0),
        }
    return {"ast": dump(tree)}


def main() -> int:
    rows = []
    for src, opts in CASES:
        row = {"src": src, "opts": opts}
        row.update(parse(src, opts))
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
