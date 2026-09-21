#!/usr/bin/env python3
# Copyright 2026 The gojja2 Authors
# SPDX-License-Identifier: Apache-2.0
"""Write jinja2's parse tree in gojja2's normalised syntax vocabulary.

The engine exposes a template's structure through the `syntax` package: one node
type, a closed set of kinds, and children reached through labelled edges. That
vocabulary is deliberately neither tree's own shape -- jinja2 has nine classes
for binary operators and `syntax` has one node with the operator on it -- and
this writes jinja2's tree into it.

Doing so turns "the two see the same template" into something that can be
checked rather than believed, and checked once for every question rather than
once per question: if the canonical bytes match, no query can tell the trees
apart. conformance/syntax_test.go does the diffing.

Where the two disagree, this file is not the place to paper over it. A
difference in the canonical form is either a real difference in what the parsers
understood, which is a conformance bug, or a gap in the vocabulary, which is a
design one. Both are worth finding.
"""

from __future__ import annotations

import json

from jinja2 import nodes

# jinja2 spells each binary operator as its own class; the vocabulary spells it
# as an operator on one node, using the name gojja2's parser uses.
BINOP = {
    "Add": "add", "Sub": "sub", "Mul": "mul", "Div": "div",
    "FloorDiv": "floordiv", "Mod": "mod", "Pow": "pow",
    "And": "and", "Or": "or",
}
UNARYOP = {"Not": "not", "Neg": "neg", "Pos": "pos"}


def N(kind, attrs=None, edges=None):
    out = {"k": kind}
    if attrs:
        # Sorted, because the Go encoder sorts: two encoders must not be able
        # to differ by map order.
        out["a"] = dict(sorted(attrs.items()))
    if edges:
        out["e"] = edges
    return out


def E(edges, role, node):
    if node is not None:
        edges.append([role, node])


def args_edges(n, edges):
    for a in getattr(n, "args", None) or ():
        E(edges, "arg", expr(a))
    for kw in getattr(n, "kwargs", None) or ():
        E(edges, "kwarg", N("keyword", {"name": kw.key},
                            [["value", expr(kw.value)]]))
    E(edges, "dynargs", expr(getattr(n, "dyn_args", None)))
    E(edges, "dynkw", expr(getattr(n, "dyn_kwargs", None)))


def const_value(v):
    if v is None or isinstance(v, bool) or isinstance(v, str):
        return v
    if isinstance(v, float):
        # As digits rather than as a JSON number: Go writes float64(1) as `1`
        # and Python writes `1.0`, and the difference is the encoder's, not the
        # template's. repr() is the spelling gojja2 already reproduces exactly
        # -- value/testdata/repr_float.json grades it against CPython.
        return {"f": repr(v)}
    if isinstance(v, int):
        # Matches the Go side: an integer wider than int64 goes as digits,
        # because neither JSON nor a float carries it unchanged.
        return v if -(2**63) <= v < 2**63 else str(v)
    if isinstance(v, tuple):
        return [const_value(x) for x in v]
    return repr(v)


def expr(n):
    if n is None:
        return None
    t = type(n).__name__

    if t == "Const":
        return N("const", {"value": const_value(n.value)})
    if t == "TemplateData":
        return N("text", {"value": n.data})
    if t == "Name":
        # jinja2 has three contexts and the vocabulary has one question: is
        # this occurrence binding the name or reading it. A macro parameter and
        # a `{% with %}` target are spelled "param" there and are bindings here.
        return N("name", {"name": n.name, "store": n.ctx != "load"})
    if t == "NSRef":
        return N("nsref", {"name": n.name, "attr": n.attr})
    if t == "Tuple":
        return N("tuple", {"store": n.ctx != "load"},
                 [["item", expr(i)] for i in n.items])
    if t == "List":
        return N("list", None, [["item", expr(i)] for i in n.items])
    if t == "Dict":
        return N("dict", None, [["item", expr(p)] for p in n.items])
    if t == "Pair":
        return N("pair", None, [["key", expr(n.key)], ["value", expr(n.value)]])
    if t == "Keyword":
        return N("keyword", {"name": n.key}, [["value", expr(n.value)]])
    if t == "CondExpr":
        e = []
        E(e, "test", expr(n.test))
        E(e, "then", expr(n.expr1))
        E(e, "other", expr(n.expr2))
        return N("cond", None, e)
    if t in BINOP:
        return N("binop", {"op": BINOP[t]},
                 [["left", expr(n.left)], ["right", expr(n.right)]])
    if t in UNARYOP:
        return N("unaryop", {"op": UNARYOP[t]}, [["operand", expr(n.node)]])
    if t == "Concat":
        return N("concat", None, [["item", expr(i)] for i in n.nodes])
    if t == "Compare":
        e = [["left", expr(n.expr)]]
        for op in n.ops:
            e.append(["operand", expr(op)])
        return N("compare", None, e)
    if t == "Operand":
        return N("operand", {"op": n.op}, [["value", expr(n.expr)]])
    if t == "Getattr":
        return N("getattr", {"attr": n.attr}, [["subject", expr(n.node)]])
    if t == "Getitem":
        return N("getitem", None,
                 [["subject", expr(n.node)], ["index", expr(n.arg)]])
    if t == "Slice":
        e = []
        E(e, "start", expr(n.start))
        E(e, "stop", expr(n.stop))
        E(e, "step", expr(n.step))
        return N("slice", None, e)
    if t == "Call":
        e = []
        E(e, "callee", expr(n.node))
        args_edges(n, e)
        return N("call", None, e)
    if t == "Filter":
        e = []
        E(e, "subject", expr(n.node))
        args_edges(n, e)
        return N("filter", {"name": n.name}, e)
    if t == "Test":
        e = []
        E(e, "subject", expr(n.node))
        args_edges(n, e)
        return N("test", {"name": n.name}, e)
    raise Unsupported(f"expression {t}")


class Unsupported(Exception):
    """A node the vocabulary has no spelling for.

    Raised rather than skipped: a tree quietly missing a node would compare
    equal for the wrong reason, which is the one failure this whole exercise
    exists to rule out.
    """


def stmt(n):
    t = type(n).__name__

    if t == "Template":
        return N("template", None, [["body", stmt(s)] for s in n.body])
    if t == "Output":
        return N("output", None, [["value", expr(i)] for i in n.nodes])
    if t == "If":
        e = [["test", expr(n.test)]]
        e += [["body", stmt(s)] for s in n.body]
        e += [["else", stmt(s)] for s in n.elif_]
        e += [["else", stmt(s)] for s in n.else_]
        return N("if", None, e)
    if t == "For":
        e = []
        E(e, "target", expr(n.target))
        E(e, "iter", expr(n.iter))
        E(e, "test", expr(n.test))
        e += [["body", stmt(s)] for s in n.body]
        e += [["else", stmt(s)] for s in n.else_]
        return N("for", {"recursive": bool(n.recursive)}, e)
    if t == "Assign":
        return N("assign", None,
                 [["target", expr(n.target)], ["value", expr(n.node)]])
    if t == "AssignBlock":
        e = [["target", expr(n.target)]]
        E(e, "filter", expr(n.filter))
        e += [["body", stmt(s)] for s in n.body]
        return N("assign_block", None, e)
    if t == "Macro":
        e = [["param", expr(a)] for a in n.args]
        e += [["default", expr(d)] for d in n.defaults]
        e += [["body", stmt(s)] for s in n.body]
        return N("macro", {"name": n.name}, e)
    if t == "CallBlock":
        e = [["callee", expr(n.call)]]
        e += [["param", expr(a)] for a in n.args]
        e += [["default", expr(d)] for d in n.defaults]
        e += [["body", stmt(s)] for s in n.body]
        return N("call_block", None, e)
    if t == "FilterBlock":
        e = [["filter", expr(n.filter)]]
        e += [["body", stmt(s)] for s in n.body]
        return N("filter_block", None, e)
    if t == "With":
        e = []
        for i, target in enumerate(n.targets):
            E(e, "target", expr(target))
            if i < len(n.values):
                E(e, "value", expr(n.values[i]))
        e += [["body", stmt(s)] for s in n.body]
        return N("with", None, e)
    if t == "Block":
        return N("block", {"name": n.name, "scoped": bool(n.scoped),
                           "required": bool(getattr(n, "required", False))},
                 [["body", stmt(s)] for s in n.body])
    if t == "Extends":
        return N("extends", None, [["template", expr(n.template)]])
    if t == "Include":
        return N("include", {"with_context": bool(n.with_context),
                             "ignore_missing": bool(n.ignore_missing)},
                 [["template", expr(n.template)]])
    if t == "Import":
        return N("import", {"target": n.target,
                            "with_context": bool(n.with_context)},
                 [["template", expr(n.template)]])
    if t == "FromImport":
        names = [[x, x] if isinstance(x, str) else [x[0], x[1]] for x in n.names]
        return N("from_import", {"names": names,
                                 "with_context": bool(n.with_context)},
                 [["template", expr(n.template)]])
    if t == "ExprStmt":
        return N("expr_stmt", None, [["value", expr(n.node)]])
    if t in ("Scope", "OverlayScope"):
        return N("scope", None, [["body", stmt(s)] for s in n.body])
    if t in ("EvalContextModifier", "ScopedEvalContextModifier"):
        # `{% autoescape x %}`. jinja2 spells it as a generic eval-context
        # change carrying a keyword; the vocabulary spells the one form a
        # template can actually write.
        value = None
        for kw in n.options:
            if kw.key == "autoescape":
                value = expr(kw.value)
        e = []
        E(e, "value", value)
        e += [["body", stmt(s)] for s in getattr(n, "body", [])]
        return N("autoescape", None, e)
    if t == "Break":
        return N("break")
    if t == "Continue":
        return N("continue")
    raise Unsupported(f"statement {t}")


def canonical(tree) -> str:
    """The tree as the bytes both sides are compared in."""
    return json.dumps(stmt(tree), ensure_ascii=False, separators=(",", ":"),
                      sort_keys=False)
