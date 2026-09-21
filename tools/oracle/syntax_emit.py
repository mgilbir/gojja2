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
from jinja2.idtracking import symbols_for_node

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


def scope_node(kind, attrs=None):
    """A node created before its children, so a frame can be opened on it.

    N() builds a node from finished edges, which is fine for anything that does
    not introduce a scope. A scope has to be the frame's identity while its body
    is walked, so it exists first and is filled in after.
    """
    out = {"k": kind}
    if attrs:
        out["a"] = dict(sorted(attrs.items()))
    return out


def close(out, edges):
    if edges:
        out["e"] = edges
    return out


def E(edges, role, node):
    if node is not None:
        edges.append([role, node])


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


class Unsupported(Exception):
    """A node the vocabulary has no spelling for.

    Raised rather than skipped: a tree quietly missing a node would compare
    equal for the wrong reason, which is the one failure this whole exercise
    exists to rule out.
    """



class Emitter:
    """Writes jinja2's tree in gojja2's vocabulary, and tracks its scopes.

    The scope rules are jinja2's own: which names a frame owns comes from
    jinja2.idtracking, the module its code generator uses, rather than from a
    second reading of the rule. That is the part worth not reimplementing --
    ownership is decided per frame on a name's first mention, and the
    consequences are invisible until they are wrong.
    """

    # The nodes jinja2 treats as roots of a symbol frame, which is the same set
    # the engine's frameLocals stops at.
    FRAMES = ("Template", "For", "Macro", "CallBlock", "FilterBlock", "With",
              "Block", "Scope", "OverlayScope", "ScopedEvalContextModifier",
              "AssignBlock")

    def __init__(self, globals_=()):
        self.frames = []          # [(emitted scope node, {name: symbol})]
        self.symbols = []         # jinja2 Symbols, parallel to frames
        self.scopes = {}          # id(scope node) -> [symbol]
        self.scope_nodes = {}     # id(scope node) -> the node itself
        self.context = {}         # name -> symbol
        self.defs = {}            # id(name node) -> symbol
        self.uses = {}            # id(name node) -> symbol
        self.globals = set(globals_)
        self._keep = []           # nodes must outlive their ids

    def sym(self, name, kind, scope):
        return {"name": name, "kind": kind, "scope": scope}

    def push(self, scope_node, jinja_node):
        parent = self.symbols[-1] if self.symbols else None
        self.symbols.append(symbols_for_node(jinja_node, parent))
        self.frames.append((scope_node, {}))
        self.scopes[id(scope_node)] = []
        self.scope_nodes[id(scope_node)] = scope_node
        self._keep.append(scope_node)

    def pop(self):
        self.frames.pop()
        self.symbols.pop()

    def declare(self, name, kind):
        scope_node, owned = self.frames[-1]
        if name in owned:
            return owned[name]
        s = self.sym(name, kind, scope_node)
        owned[name] = s
        self.scopes[id(scope_node)].append(s)
        self._keep.append(s)
        return s

    def declare_body(self):
        """Claim the names jinja2 says this frame owns."""
        sym = self.symbols[-1]
        for name, ref in sym.refs.items():
            if sym.loads.get(ref, (None, None))[0] in ("param", "undefined"):
                self.declare(name, "local")

    def declare_targets(self, target, kind):
        t = type(target).__name__
        if t == "Name":
            self.declare(target.name, kind)
        elif t in ("Tuple", "List"):
            for item in target.items:
                self.declare_targets(item, kind)

    def resolve(self, name):
        for _node, owned in reversed(self.frames):
            if name in owned:
                return owned[name]
        if name in self.context:
            return self.context[name]
        kind = "global" if name in self.globals else "context"
        s = self.sym(name, kind, None)
        self.context[name] = s
        self._keep.append(s)
        return s

    def record(self, emitted, name, store):
        self._keep.append(emitted)
        s = self.resolve(name)
        (self.defs if store else self.uses)[id(emitted)] = s
        return emitted

    def args_edges(self, n, edges):
        for a in getattr(n, "args", None) or ():
            E(edges, "arg", self.expr(a))
        for kw in getattr(n, "kwargs", None) or ():
            E(edges, "kwarg", N("keyword", {"name": kw.key},
                                [["value", self.expr(kw.value)]]))
        E(edges, "dynargs", self.expr(getattr(n, "dyn_args", None)))
        E(edges, "dynkw", self.expr(getattr(n, "dyn_kwargs", None)))


    def expr(self, n):
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
            store = n.ctx != "load"
            return self.record(N("name", {"name": n.name, "store": store}),
                               n.name, store)
        if t == "NSRef":
            # `{% set ns.x = 1 %}` mutates the namespace rather than rebinding it,
            # so it reads ns and writes through it.
            return self.record(N("nsref", {"name": n.name, "attr": n.attr}),
                               n.name, False)
        if t == "Tuple":
            return N("tuple", {"store": n.ctx != "load"},
                     [["item", self.expr(i)] for i in n.items])
        if t == "List":
            return N("list", None, [["item", self.expr(i)] for i in n.items])
        if t == "Dict":
            return N("dict", None, [["item", self.expr(p)] for p in n.items])
        if t == "Pair":
            return N("pair", None, [["key", self.expr(n.key)], ["value", self.expr(n.value)]])
        if t == "Keyword":
            return N("keyword", {"name": n.key}, [["value", self.expr(n.value)]])
        if t == "CondExpr":
            e = []
            E(e, "test", self.expr(n.test))
            E(e, "then", self.expr(n.expr1))
            E(e, "other", self.expr(n.expr2))
            return N("cond", None, e)
        if t in BINOP:
            return N("binop", {"op": BINOP[t]},
                     [["left", self.expr(n.left)], ["right", self.expr(n.right)]])
        if t in UNARYOP:
            return N("unaryop", {"op": UNARYOP[t]}, [["operand", self.expr(n.node)]])
        if t == "Concat":
            return N("concat", None, [["item", self.expr(i)] for i in n.nodes])
        if t == "Compare":
            e = [["left", self.expr(n.expr)]]
            for op in n.ops:
                e.append(["operand", self.expr(op)])
            return N("compare", None, e)
        if t == "Operand":
            return N("operand", {"op": n.op}, [["value", self.expr(n.expr)]])
        if t == "Getattr":
            return N("getattr", {"attr": n.attr}, [["subject", self.expr(n.node)]])
        if t == "Getitem":
            return N("getitem", None,
                     [["subject", self.expr(n.node)], ["index", self.expr(n.arg)]])
        if t == "Slice":
            e = []
            E(e, "start", self.expr(n.start))
            E(e, "stop", self.expr(n.stop))
            E(e, "step", self.expr(n.step))
            return N("slice", None, e)
        if t == "Call":
            e = []
            E(e, "callee", self.expr(n.node))
            self.args_edges(n, e)
            return N("call", None, e)
        if t == "Filter":
            e = []
            E(e, "subject", self.expr(n.node))
            self.args_edges(n, e)
            return N("filter", {"name": n.name}, e)
        if t == "Test":
            e = []
            E(e, "subject", self.expr(n.node))
            self.args_edges(n, e)
            return N("test", {"name": n.name}, e)
        raise Unsupported(f"expression {t}")


    def stmt(self, n):
        t = type(n).__name__

        if t == "Template":
            out = scope_node("template")
            self.push(out, n)
            self.declare_body()
            e = [["body", self.stmt(s)] for s in n.body]
            self.pop()
            return close(out, e)
        if t == "Output":
            return N("output", None, [["value", self.expr(i)] for i in n.nodes])
        if t == "If":
            e = [["test", self.expr(n.test)]]
            e += [["body", self.stmt(s)] for s in n.body]
            e += [["else", self.stmt(s)] for s in n.elif_]
            e += [["else", self.stmt(s)] for s in n.else_]
            return N("if", None, e)
        if t == "For":
            out = scope_node("for", {"recursive": bool(n.recursive)})
            # The sequence is read outside the loop's frame; the rest is the
            # loop's own.
            it = self.expr(n.iter)
            self.push(out, n)
            self.declare_targets(n.target, "target")
            self.declare("loop", "provided")
            self.declare_body()
            e = []
            E(e, "target", self.expr(n.target))
            E(e, "iter", it)
            E(e, "test", self.expr(n.test))
            e += [["body", self.stmt(s)] for s in n.body]
            e += [["else", self.stmt(s)] for s in n.else_]
            self.pop()
            return close(out, e)
        if t == "Assign":
            return N("assign", None,
                     [["target", self.expr(n.target)], ["value", self.expr(n.node)]])
        if t == "AssignBlock":
            out = scope_node("assign_block")
            e = [["target", self.expr(n.target)]]
            E(e, "filter", self.expr(n.filter))
            self.push(out, n)
            self.declare_body()
            e += [["body", self.stmt(s)] for s in n.body]
            self.pop()
            return close(out, e)
        if t == "Macro":
            out = scope_node("macro", {"name": n.name})
            self.push(out, n)
            for a in n.args:
                self.declare(a.name, "param")
            for given in ("varargs", "kwargs", "caller"):
                self.declare(given, "provided")
            self.declare_body()
            e = [["param", self.expr(a)] for a in n.args]
            e += [["default", self.expr(d)] for d in n.defaults]
            e += [["body", self.stmt(s)] for s in n.body]
            self.pop()
            return close(out, e)
        if t == "CallBlock":
            out = scope_node("call_block")
            callee = self.expr(n.call)
            self.push(out, n)
            for a in n.args:
                self.declare(a.name, "param")
            self.declare_body()
            e = [["callee", callee]]
            e += [["param", self.expr(a)] for a in n.args]
            e += [["default", self.expr(d)] for d in n.defaults]
            e += [["body", self.stmt(s)] for s in n.body]
            self.pop()
            return close(out, e)
        if t == "FilterBlock":
            out = scope_node("filter_block")
            e = [["filter", self.expr(n.filter)]]
            self.push(out, n)
            self.declare_body()
            e += [["body", self.stmt(s)] for s in n.body]
            self.pop()
            return close(out, e)
        if t == "With":
            out = scope_node("with")
            # Values are read outside the block; targets bind inside it.
            values = [self.expr(v) for v in n.values]
            self.push(out, n)
            for target in n.targets:
                self.declare_targets(target, "target")
            self.declare_body()
            e = []
            for i, target in enumerate(n.targets):
                E(e, "target", self.expr(target))
                if i < len(values):
                    E(e, "value", values[i])
            e += [["body", self.stmt(s)] for s in n.body]
            self.pop()
            return close(out, e)
        if t == "Block":
            out = scope_node("block", {"name": n.name, "scoped": bool(n.scoped),
                                       "required": bool(getattr(n, "required", False))})
            self.push(out, n)
            self.declare_body()
            e = [["body", self.stmt(s)] for s in n.body]
            self.pop()
            return close(out, e)
        if t == "Extends":
            return N("extends", None, [["template", self.expr(n.template)]])
        if t == "Include":
            return N("include", {"with_context": bool(n.with_context),
                                 "ignore_missing": bool(n.ignore_missing)},
                     [["template", self.expr(n.template)]])
        if t == "Import":
            return N("import", {"target": n.target,
                                "with_context": bool(n.with_context)},
                     [["template", self.expr(n.template)]])
        if t == "FromImport":
            names = [[x, x] if isinstance(x, str) else [x[0], x[1]] for x in n.names]
            return N("from_import", {"names": names,
                                     "with_context": bool(n.with_context)},
                     [["template", self.expr(n.template)]])
        if t == "ExprStmt":
            return N("expr_stmt", None, [["value", self.expr(n.node)]])
        if t in ("Scope", "OverlayScope"):
            out = scope_node("scope")
            self.push(out, n)
            self.declare_body()
            e = [["body", self.stmt(s)] for s in n.body]
            self.pop()
            return close(out, e)
        if t in ("EvalContextModifier", "ScopedEvalContextModifier"):
            # `{% autoescape x %}`. jinja2 spells it as a generic eval-context
            # change carrying a keyword; the vocabulary spells the one form a
            # template can actually write.
            value = None
            for kw in n.options:
                if kw.key == "autoescape":
                    value = self.expr(kw.value)
            out = scope_node("autoescape")
            self.push(out, n)
            self.declare_body()
            e = []
            E(e, "value", value)
            e += [["body", self.stmt(s)] for s in getattr(n, "body", [])]
            self.pop()
            return close(out, e)
        if t == "Break":
            return N("break")
        if t == "Continue":
            return N("continue")
        raise Unsupported(f"statement {t}")


def canonical(tree, globals_=()) -> str:
    """The tree as the bytes both sides are compared in."""
    return json.dumps(Emitter(globals_).stmt(tree), ensure_ascii=False,
                      separators=(",", ":"), sort_keys=False)


def canonical_info(tree, globals_=()) -> str:
    """The tree's scope and binding facts, in the form Go writes them.

    Nodes are named by their position in a pre-order walk of the emitted tree,
    which costs nothing to agree on: the trees are already byte-identical, so
    the same walk reaches the same nodes in the same order on both sides.
    Symbols are numbered scope by scope in that same order, then the names that
    come from outside the template, sorted.
    """
    e = Emitter(globals_)
    root = e.stmt(tree)

    index, order = {}, []

    def walk(node):
        index[id(node)] = len(order)
        order.append(node)
        for _role, child in node.get("e", ()):
            walk(child)

    walk(root)

    rows = sorted((index[id(e.scope_nodes[sid])], syms)
                  for sid, syms in e.scopes.items())
    ids, symbols = {}, []
    for _node, syms in rows:
        for sym in syms:
            ids[id(sym)] = len(symbols)
            symbols.append(sym)
    for sym in sorted(e.context.values(), key=lambda s: s["name"]):
        ids[id(sym)] = len(symbols)
        symbols.append(sym)

    def pairs(m):
        return sorted([index[nid], ids[id(sym)]] for nid, sym in m.items())

    out = {
        "symbols": [{"kind": s["kind"], "name": s["name"],
                     "scope": index[id(s["scope"])] if s["scope"] is not None else -1}
                    for s in symbols],
        "scopes": [[node, [ids[id(s)] for s in syms]] for node, syms in rows],
        "defs": pairs(e.defs),
        "uses": pairs(e.uses),
    }
    return json.dumps(out, ensure_ascii=False, separators=(",", ":"),
                      sort_keys=False)

