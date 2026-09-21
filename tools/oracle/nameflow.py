#!/usr/bin/env python3
# Copyright 2026 The gojja2 Authors
# SPDX-License-Identifier: Apache-2.0
"""Which context variables can reach a template's output, and how.

The question is not "which names does this template mention". It is what a
caller actually wants to know about a variable before passing it:

  * **output** -- its value can appear in what the template renders.
  * **flow**   -- it can change what is rendered without appearing in it:
                  a condition, a loop's length, a filter argument.

A name can be both, and it can be neither: `{% set unused = x %}` with nothing
reading `unused` means `x` cannot affect the output at all.

Answering that needs dataflow, not a syntactic scan. `{% set y = x %}{{ y }}`
prints x. `{{ "a" if flag else "b" }}` prints neither branch's operand but flag
decides which. So every binding is a node in a graph, roles are attached where a
value is consumed, and then propagated backwards to whatever it came from.

**Scoping is jinja2's, not a reimplementation of it.** jinja2 decides per frame,
on a name's first mention, whether it belongs to the frame or resolves from the
context -- `{{ x }}{% set x = 1 %}` reads the context, but
`{% for i in [1] %}{{ x }}{% endfor %}{% set x = 1 %}` does not, because the
root frame claimed x. Getting that wrong silently changes the answer, so this
asks jinja2 itself, through jinja2.idtracking, rather than working it out again.

**Soundness has a direction.** Where the analysis cannot see -- a computed
lookup `obj[k]`, a namespace, a template included by a computed name -- it
answers `unknown` rather than guessing. An `unknown` name may or may not reach
the output; a name reported without it is a real answer. Nothing here ever says
"cannot reach the output" about a name that might.

This is the reference implementation. The engine has its own, over its own AST,
and conformance/nameflow_test.go requires them to reach the same conclusions --
which is the point of writing it twice: the answer must be a property of the
template, not of either tree's shape.
"""

from __future__ import annotations

import itertools
from dataclasses import dataclass, field

from jinja2 import nodes
from jinja2.idtracking import symbols_for_node

OUTPUT = 1
FLOW = 2

# A frame owns a name when jinja2 says the name is the frame's parameter or
# starts out undefined there. "resolve" means look outward at runtime, which at
# a nested frame is an enclosing frame and only at the root is the context.
OWNS = ("param", "undefined")


@dataclass
class Sym:
    """One storage location: a context variable, or a binding in some frame."""
    id: int
    name: str
    context: bool = False          # resolved from the caller's variables
    roles: int = 0                 # roles attached directly to this symbol
    deps: set[int] = field(default_factory=set)   # symbols its value came from
    unknown: bool = False          # something about it could not be followed


class Frame:
    def __init__(self, node, symbols):
        self.node = node
        self.symbols = symbols
        self.owned: dict[str, Sym] = {}


class Analysis:
    def __init__(self):
        self._ids = itertools.count()
        self.syms: dict[int, Sym] = {}
        self.context: dict[str, Sym] = {}
        self.frames: list[Frame] = []
        # Set when a construct could route the whole context somewhere this
        # cannot follow -- an include, or an import. Every context name is then
        # unknown, because the other template can print any of them.
        self.opaque_sink = False
        # Following a reference to another template needs a way to reach it,
        # and a way not to follow a cycle round for ever.
        self.resolver = None
        self.visiting = set()
        self.cache = {}
        # A stack of block-set captures: while one is open, output is collected
        # rather than emitted, because it becomes a value instead of a document.
        self.capture: list[set[int]] = []
        # Macros by name, so a call can bind arguments to parameters instead of
        # giving up. Only macros defined in this template and called by their
        # own name; anything else is an opaque call.
        self.macros: dict[str, object] = {}
        self.in_macro: set[str] = set()
        self.macro_frames: dict[str, dict] = {}
        self.macro_out: dict[str, set[int]] = {}

    # --- symbols ---------------------------------------------------------

    def new_sym(self, name, context=False) -> Sym:
        s = Sym(id=next(self._ids), name=name, context=context)
        self.syms[s.id] = s
        return s

    def context_sym(self, name) -> Sym:
        if name not in self.context:
            self.context[name] = self.new_sym(name, context=True)
        return self.context[name]

    def resolve(self, name) -> Sym:
        """The storage a read of `name` sees, innermost frame outwards."""
        for f in reversed(self.frames):
            if name in f.owned:
                return f.owned[name]
        return self.context_sym(name)

    # --- frames ----------------------------------------------------------

    def push(self, node) -> Frame:
        parent = self.frames[-1].symbols if self.frames else None
        sym = symbols_for_node(node, parent) if node is not None else parent
        f = Frame(node, sym)
        # Claim the names this frame owns, before walking it: jinja2 decides
        # ownership for the whole frame up front, not as the walk reaches each
        # statement.
        for name, ref in (sym.refs.items() if sym else {}):
            kind = sym.loads.get(ref, (None, None))[0]
            if kind in OWNS:
                f.owned[name] = self.new_sym(name)
        self.frames.append(f)
        # An alias is the same value carried into the frame, so the frame's
        # copy starts out holding whatever the outer one held.
        for name, ref in (sym.refs.items() if sym else {}):
            kind, extra = sym.loads.get(ref, (None, None))
            if kind == "alias":
                inner = self.new_sym(name)
                outer = self.resolve(name)
                inner.deps.add(outer.id)
                f.owned[name] = inner
        return f

    def pop(self):
        self.frames.pop()

    # --- recording -------------------------------------------------------

    def use(self, name, roles):
        s = self.resolve(name)
        s.roles |= roles

    def mark_unknown(self, name):
        self.resolve(name).unknown = True

    # --- other templates -------------------------------------------------

    def context_effects_of(self, name):
        """What the named template does with the variables it is handed."""
        if name in self.cache:
            return self.cache[name]
        if name in self.visiting:
            return {}
        if self.resolver is None:
            self.opaque_sink = True
            return {}
        tree = self.resolver(name)
        if tree is None:
            self.opaque_sink = True
            return {}
        self.visiting.add(name)
        sub = Analysis()
        sub.resolver, sub.visiting, sub.cache = self.resolver, self.visiting, self.cache
        sub.push(tree)
        sub.stmts(tree.body)
        sub.pop()
        sub.propagate()
        self.visiting.discard(name)
        if sub.opaque_sink:
            self.opaque_sink = True
        eff = {nm: (s.roles, s.unknown) for nm, s in sub.context.items()}
        self.cache[name] = eff
        return eff

    def inherit(self, name):
        """Apply another template's effects to our own variables."""
        for nm, (roles, unknown) in self.context_effects_of(name).items():
            s = self.context_sym(nm)
            s.roles |= roles
            s.unknown = s.unknown or unknown

    # --- the walk --------------------------------------------------------
    #
    # expr() answers "what does this value derive from", as a set of symbol
    # ids, and applies FLOW itself at the one control position that lives
    # inside an expression -- a conditional's test. Everything else is data:
    # `{{ x|length }}` and `{{ x is defined }}` both put a value derived from x
    # into the document, so both are output. FLOW is for *choosing* between
    # alternatives, which is what `{% if %}`, a loop's length and `a if c else b`
    # do. That line has to be drawn somewhere, and drawing it at control
    # positions is the one place two implementations can agree without
    # comparing notes.

    def emit(self, srcs):
        """This value reaches the document -- unless a block-set is capturing."""
        if self.capture:
            self.capture[-1] |= srcs
            return
        self.apply(srcs, OUTPUT)

    def apply(self, srcs, roles):
        for sid in srcs:
            self.syms[sid].roles |= roles

    def taint(self, srcs):
        for sid in srcs:
            self.syms[sid].unknown = True

    def exprs(self, items):
        out = set()
        for n in items or ():
            out |= self.expr(n)
        return out

    def args_of(self, n):
        """Every argument of a call, filter or test, as one derived set."""
        out = self.exprs(getattr(n, "args", None))
        for kw in getattr(n, "kwargs", None) or ():
            out |= self.expr(kw.value)
        for extra in (getattr(n, "dyn_args", None), getattr(n, "dyn_kwargs", None)):
            if extra is not None:
                out |= self.expr(extra)
        return out

    def expr(self, n) -> set[int]:
        if n is None:
            return set()

        if isinstance(n, nodes.Name):
            if n.ctx == "store":
                return set()
            return {self.resolve(n.name).id}

        if isinstance(n, nodes.NSRef):
            # A namespace read. Layer 2 will follow the field; until then the
            # honest answer is that anything could be in it.
            s = self.resolve(n.name)
            s.unknown = True
            return {s.id}

        if isinstance(n, (nodes.Const, nodes.TemplateData)):
            return set()

        if isinstance(n, nodes.CondExpr):
            self.apply(self.expr(n.test), FLOW)
            return self.expr(n.expr1) | self.expr(n.expr2)

        if isinstance(n, nodes.Getitem):
            base = self.expr(n.node)
            if not isinstance(n.arg, nodes.Const):
                # obj[k] with a computed key: which part of obj is read is not
                # a static fact, so the whole of obj is in play.
                self.taint(base)
                base |= self.expr(n.arg)
            return base

        if isinstance(n, nodes.Filter):
            out = self.expr(n.node) | self.args_of(n)
            if n.node is None:
                # The leading filter of a {% filter %} block or a block set;
                # its input is the captured body, handled by the caller.
                out = self.args_of(n)
            if n.name in ("attr",):
                self.taint(out)
            return out

        if isinstance(n, nodes.Call):
            fn = n.node
            out = self.args_of(n)
            if isinstance(fn, nodes.Name) and fn.name in self.macros:
                return out | self.call_macro(fn.name, n)
            if isinstance(fn, nodes.Name) and fn.name == "namespace":
                self.taint(out)
                return out
            # A call to something this cannot see the body of: getattr, a
            # macro held in a variable, a global. Its result derives from its
            # arguments and from whatever it closed over, which is not visible.
            out |= self.expr(fn)
            self.taint(out)
            return out

        # Everything else -- operators, comparisons, tests, attribute access,
        # containers, slices, concatenation -- is ordinary data flow: the
        # result derives from every operand.
        out = set()
        for child in n.iter_child_nodes():
            out |= self.expr(child)
        out |= self.args_of(n)
        return out

    # --- statements ------------------------------------------------------

    def stmts(self, body):
        for s in body or ():
            self.stmt(s)

    def bind(self, target, srcs):
        """Record that `target` now holds a value derived from `srcs`."""
        if isinstance(target, nodes.Name):
            self.resolve(target.name).deps |= srcs
        elif isinstance(target, nodes.NSRef):
            # Layer 2: a namespace field. Until it is followed, a write into a
            # namespace makes the namespace opaque rather than silently lost.
            s = self.resolve(target.name)
            s.deps |= srcs
            s.unknown = True
        elif isinstance(target, (nodes.Tuple, nodes.List)):
            # Unpacking: which element lands where is not tracked, so every
            # target derives from the whole right-hand side.
            for item in target.items:
                self.bind(item, srcs)
        else:
            self.taint(srcs)

    def capture_body(self, body) -> set[int]:
        """What a block-set or filter block's body would have printed."""
        self.capture.append(set())
        self.stmts(body)
        return self.capture.pop()

    def call_macro(self, name, call) -> set[int]:
        """Bind a call's arguments to the macro's parameters, and return what
        the macro's body would print."""
        m = self.macros[name]
        if name in self.in_macro:
            # Recursive; the fixpoint below already carries roles around the
            # cycle, and re-entering would not terminate.
            return set()
        self.in_macro.add(name)
        frame = self.macro_frames[name]
        for i, param in enumerate(m.args):
            srcs = set()
            if i < len(call.args):
                srcs = self.expr(call.args[i])
            for kw in call.kwargs or ():
                if kw.key == param.name:
                    srcs |= self.expr(kw.value)
            if srcs:
                frame[param.name].deps |= srcs
        self.in_macro.discard(name)
        return self.macro_out[name]

    def stmt(self, n):
        if isinstance(n, nodes.Output):
            for item in n.nodes:
                self.emit(self.expr(item))

        elif isinstance(n, nodes.If):
            self.apply(self.expr(n.test), FLOW)
            self.stmts(n.body)
            for elif_ in getattr(n, "elif_", None) or ():
                self.stmt(elif_)
            self.stmts(n.else_)

        elif isinstance(n, nodes.For):
            # The iterable is evaluated outside the loop's own frame.
            srcs = self.expr(n.iter)
            # Its length decides how many times the body runs, so it can change
            # the output without appearing in it.
            self.apply(srcs, FLOW)
            self.push(n)
            self.bind(n.target, srcs)
            # `loop` is supplied by the loop, not by the caller, and what it
            # reports -- index, length, first, last -- is derived from the
            # sequence being walked.
            self.frames[-1].owned["loop"] = self.new_sym("loop")
            self.frames[-1].owned["loop"].deps |= srcs
            self.apply(self.expr(n.test), FLOW)
            self.stmts(n.body)
            self.stmts(n.else_)
            self.pop()

        elif isinstance(n, nodes.Assign):
            self.bind(n.target, self.expr(n.node))

        elif isinstance(n, nodes.AssignBlock):
            srcs = self.capture_body(n.body)
            if n.filter is not None:
                srcs |= self.expr(n.filter)
            self.bind(n.target, srcs)

        elif isinstance(n, nodes.FilterBlock):
            srcs = self.capture_body(n.body)
            srcs |= self.expr(n.filter)
            self.emit(srcs)

        elif isinstance(n, nodes.Macro):
            self.push(n)
            for supplied in ("varargs", "kwargs", "caller"):
                self.frames[-1].owned.setdefault(supplied, self.new_sym(supplied))
            self.macro_frames[n.name] = dict(self.frames[-1].owned)
            for param, default in zip(n.args[len(n.args) - len(n.defaults):],
                                      n.defaults):
                self.bind(param, self.expr(default))
            self.macro_out[n.name] = self.capture_body(n.body)
            self.pop()
            self.macros[n.name] = n

        elif isinstance(n, nodes.CallBlock):
            self.push(n)
            self.stmts(n.body)
            self.pop()
            self.emit(self.expr(n.call))

        elif isinstance(n, nodes.With):
            pairs = [(t, self.expr(v)) for t, v in zip(n.targets, n.values)]
            self.push(n)
            for target, srcs in pairs:
                self.bind(target, srcs)
            self.stmts(n.body)
            self.pop()

        elif isinstance(n, (nodes.Block, nodes.Scope, nodes.OverlayScope)):
            self.push(n)
            self.stmts(n.body)
            self.pop()

        elif isinstance(n, (nodes.Extends, nodes.Include)):
            # The named template renders with these variables, so what it does
            # with them is what this one does with them. Which template that is
            # steers the output -- two names render two documents -- even though
            # the name itself is never printed.
            self.apply(self.expr(n.template), FLOW)
            name = const_name(n.template)
            if name is None:
                self.opaque_sink = True
            elif isinstance(n, nodes.Include) and not n.with_context:
                pass          # handed nothing, so it can print nothing of ours
            else:
                self.inherit(name)

        elif isinstance(n, (nodes.Import, nodes.FromImport)):
            # An import binds names rather than rendering, and does not pass the
            # context unless asked. One that does not cannot see the caller's
            # variables at all, so it cannot print them.
            self.apply(self.expr(n.template), FLOW)
            name = const_name(n.template)
            if name is None:
                if n.with_context:
                    self.opaque_sink = True
            elif n.with_context:
                self.inherit(name)
            # What it binds is a module, or a macro from one. Calling that
            # reaches code this does not follow.
            for bound in imported_names(n):
                self.resolve(bound).unknown = True

        elif isinstance(n, nodes.ExprStmt):
            self.expr(n.node)

        elif isinstance(n, (nodes.EvalContextModifier,
                            nodes.ScopedEvalContextModifier)):
            # `{% autoescape x %}`. Whether output is escaped changes what is
            # rendered, so x steers the result without appearing in it. The
            # options are Keyword helpers rather than expressions, which is why
            # the generic branch below does not reach them.
            for kw in n.options:
                self.apply(self.expr(kw.value), FLOW)
            if isinstance(n, nodes.ScopedEvalContextModifier):
                self.push(n)
                self.stmts(n.body)
                self.pop()
            else:
                self.stmts(getattr(n, "body", []))

        elif isinstance(n, (nodes.Break, nodes.Continue)):
            pass

        else:
            # Anything unrecognised is walked for its expressions and treated
            # as opaque, rather than skipped: a statement nobody taught this
            # about must not read as "nothing happens here".
            for child in n.iter_child_nodes():
                if isinstance(child, nodes.Expr):
                    self.taint(self.expr(child))
                elif isinstance(child, nodes.Stmt):
                    self.stmt(child)

    # --- propagation -----------------------------------------------------

    def propagate(self):
        """Push roles backwards along the dependency edges, to a fixpoint.

        A symbol that reaches the output means everything its value came from
        reaches the output; the same for flow, and for not being understood.
        The graph can hold cycles -- a loop that accumulates into a variable it
        also reads -- so this iterates rather than recursing.
        """
        changed = True
        while changed:
            changed = False
            for s in self.syms.values():
                for dep in s.deps:
                    d = self.syms[dep]
                    roles = d.roles | s.roles
                    unknown = d.unknown or s.unknown
                    if roles != d.roles or unknown != d.unknown:
                        d.roles, d.unknown = roles, unknown
                        changed = True

    def result(self) -> dict[str, dict]:
        self.propagate()
        out = {}
        for name, s in sorted(self.context.items()):
            out[name] = {
                "output": bool(s.roles & OUTPUT),
                "flow": bool(s.roles & FLOW),
                # An unknown answer has no reliable negative: the name may
                # reach the output by a route this could not follow. A name
                # without it is a real answer in both directions.
                "unknown": bool(s.unknown or self.opaque_sink),
            }
        return out


def const_name(ref):
    """The template a reference names, when it names one at all."""
    if isinstance(ref, nodes.Const) and isinstance(ref.value, str):
        return ref.value
    return None


def imported_names(n):
    """What an import binds locally."""
    if isinstance(n, nodes.Import):
        return [n.target] if n.target else []
    return [x if isinstance(x, str) else x[1] for x in n.names]


def analyze(tree, globals_=(), resolver=None) -> dict[str, dict]:
    """Classify every context variable a parsed template reads.

    `globals_` are names the environment supplies -- range, dict, lipsum and
    their neighbours. A template reading one is not asking its caller for
    anything, so they are left out rather than reported as requirements.

    `resolver` maps a template name to its parsed tree, so `{% extends %}`,
    `{% include %}` and `{% import %}` can be followed rather than given up at.
    """
    a = Analysis()
    a.resolver = resolver
    a.push(tree)
    a.stmts(tree.body)
    a.pop()
    return {k: v for k, v in a.result().items() if k not in set(globals_)}
