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

import syntax_emit

OUTPUT = 1
FLOW = 2
# The render can fail because of this variable: its value feeds something
# that can raise. An over-approximation -- the operation *can* raise, not that
# it will -- so the useful signal is its absence.
REQUIRED = 4

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


# The frames a scope is opened for, which is the set syntax_emit opens one for.
FRAME_NODES = ("Template", "For", "Macro", "CallBlock", "FilterBlock", "With",
               "Block", "Scope", "OverlayScope", "ScopedEvalContextModifier",
               "AssignBlock")


class Analysis:
    def __init__(self):
        self._ids = itertools.count()
        self.syms: dict[int, Sym] = {}
        self.context: dict[str, Sym] = {}
        self.stack: list = []
        self.em = None
        self._bysym: dict[int, Sym] = {}
        self._emsym: dict[int, dict] = {}
        # Set when a construct could route the whole context somewhere this
        # cannot follow -- an include, or an import. Every context name is then
        # unknown, because the other template can print any of them.
        self.opaque_sink = False
        # Following a reference to another template needs a way to reach it,
        # and a way not to follow a cycle round for ever.
        self.resolver = None
        self.visiting = set()
        self.cache = {}
        # Namespaces being followed field by field, and the ones that got away.
        # Keyed by symbol id, because Sym is a dataclass and so unhashable.
        self.namespaces = {}
        self.aliased = set()
        # A stack of block-set captures: while one is open, output is collected
        # rather than emitted, because it becomes a value instead of a document.
        self.capture: list[set[int]] = []
        # Macros by name, so a call can bind arguments to parameters instead of
        # giving up. Only macros defined in this template and called by their
        # own name; anything else is an opaque call.
        self.in_macro: set[int] = set()
        self.macro_params: dict[int, list] = {}
        self.macro_out: dict[int, set[int]] = {}

    # --- symbols ---------------------------------------------------------

    def new_sym(self, name, context=False) -> Sym:
        s = Sym(id=next(self._ids), name=name, context=context)
        self.syms[s.id] = s
        return s

    def of(self, emitted) -> Sym:
        """The working symbol for one of the emitter's.

        The scope model is not rebuilt here. syntax_emit derives it from
        jinja2's own idtracking, the engine derives it from frameLocals, and the
        two are required to encode identically for every committed and imported
        template -- so reading it is both less work and less to be wrong about.
        What is written twice is the dataflow reasoning, which is the thing this
        file exists to check.
        """
        key = id(emitted)
        if key not in self._bysym:
            s = self.new_sym(emitted["name"], context=emitted["kind"] == "context")
            self._bysym[key] = s
            self._emsym[s.id] = emitted
            if emitted["kind"] == "context":
                self.context[emitted["name"]] = s
            if emitted.get("aliases") is not None:
                # A frame's own copy starts out holding whatever the enclosing
                # binding held; a write to it does not reach back.
                s.deps.add(self.of(emitted["aliases"]).id)
        return self._bysym[key]

    def context_sym(self, name) -> Sym:
        """A caller's variable named by something other than a name node.

        An imported template can mention a name this one never writes down.
        """
        if name not in self.context:
            self.context[name] = self.new_sym(name, context=True)
        return self.context[name]

    def node_sym(self, node) -> Sym:
        """What a Name or NSRef node refers to."""
        emitted = self.em.sym_of.get(id(node))
        if emitted is None:
            return self.context_sym(getattr(node, "name", "?"))
        return self.of(emitted)

    def scope_symbol(self, name) -> Sym | None:
        """A name looked up the chain of open scopes.

        Used where there is no node to ask: a macro carries its name as an
        attribute, and an import binds one without writing it anywhere.
        """
        for frame in reversed(self.stack):
            owned = self.em.owned_of.get(id(frame), {})
            if name in owned:
                return self.of(owned[name])
        return None

    def enclosing_scope_symbol(self, name) -> Sym | None:
        """scope_symbol ignoring the innermost scope."""
        for frame in reversed(self.stack[:-1]):
            owned = self.em.owned_of.get(id(frame), {})
            if name in owned:
                return self.of(owned[name])
        return None

    def lookup(self, name) -> Sym:
        s = self.scope_symbol(name)
        if s is not None:
            return s
        # The emitter's symbol for the name, when it has one, so a mark put here
        # lands on the same storage a later `{{ name }}` reads. Inventing a
        # fresh one instead put the mark somewhere nothing looked.
        emitted = self.em.context.get(name) if self.em is not None else None
        if emitted is not None:
            return self.of(emitted)
        return self.context_sym(name)

    # --- frames ----------------------------------------------------------

    def push(self, node):
        self.stack.append(node)

    def pop(self):
        self.stack.pop()

    # --- recording -------------------------------------------------------

    def mark_unknown(self, name):
        self.lookup(name).unknown = True

    # --- namespaces ------------------------------------------------------
    #
    # `{% set ns = namespace(total=0) %}` then writing ns.total in a loop is the
    # idiom for carrying a value out of one, because a plain `{% set %}` does not
    # escape. Treating the namespace as one opaque blob makes the answer for
    # whatever fed it "might reach the output" when it plainly does, so each
    # field gets a symbol and the ordinary dataflow applies.
    #
    # Only while the namespace itself is never handed anywhere. Two names for
    # one object means a write through either reaches the other, and following
    # that is alias analysis; getting it subtly wrong would mean reporting a
    # real negative that is not true. So a namespace read anywhere but as the
    # subject of a field access collapses back to opaque.

    def declare_namespace(self, sym, call):
        if sym is None:
            return
        # A name assigned a namespace more than once keeps one field map
        # holding both, rather than giving up on it. Merging is sound where
        # replacing is not: the later assignment need not be the one that ran,
        # because the two can sit in different branches of an `{% if %}`.
        self.namespaces.setdefault(sym.id, {})
        for kw in call.kwargs or ():
            self.namespace_field(sym, kw.key).deps |= self.expr(kw.value)
        # `namespace(d)` and `namespace(**d)` fill it from something this
        # cannot name the fields of, so it holds whatever that held and the
        # fields cannot be told apart.
        rest = set()
        for extra in list(call.args or ()) + [getattr(call, "dyn_args", None),
                                              getattr(call, "dyn_kwargs", None)]:
            if extra is not None:
                rest |= self.expr(extra)
        if rest:
            # `namespace(d)` raises unless d is a mapping.
            self.apply(rest, REQUIRED)
            sym.deps |= rest
            self.aliased.add(sym.id)

    def namespace_field(self, sym, field):
        if sym is None or sym.id not in self.namespaces:
            return None
        fields = self.namespaces[sym.id]
        if field not in fields:
            fields[field] = self.new_sym(f"{sym.name}.{field}")
        return fields[field]

    def namespace_of(self, node):
        if not isinstance(node, nodes.Name) or node.ctx != "load":
            return None
        s = self.node_sym(node)
        return s if s.id in self.namespaces else None

    def seal_namespaces(self):
        """Give up on every namespace that got away.

        After the walk, because the assignment that aliases one can come after
        the reads that looked safe.
        """
        for sid, fields in self.namespaces.items():
            if sid not in self.aliased:
                continue
            ns = self.syms[sid]
            ns.unknown = True
            for f in fields.values():
                f.unknown = True
                # A write through the other name could have put anything in
                # any field...
                f.deps.add(sid)
                # ...and whoever holds it can read every field, so doing
                # anything with the namespace does that to all of them.
                ns.deps.add(f.id)

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
        sub.em = syntax_emit.Emitter(self.em.globals)
        sub.em.stmt(tree)
        sub.push(tree)
        sub.stmts(tree.body)
        sub.pop()
        sub.seal_namespaces()
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
            # lookup rather than context_sym: the name may already have the
            # emitter's symbol, and a mark put on a fresh one is a mark nothing
            # reads.
            s = self.lookup(nm)
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
            s = self.node_sym(n)
            if s.id in self.namespaces:
                # Reached by name rather than through a field, so another name
                # now refers to the same object and every field is in play.
                self.aliased.add(s.id)
            return {s.id}

        if isinstance(n, nodes.Getattr):
            ns = self.namespace_of(n.node)
            if ns is not None:
                return {self.namespace_field(ns, n.attr).id}
            out = self.expr(n.node)
            # Reaching *through* an undefined raises, and the thing most likely
            # to be one is an attribute that was not there. `x.a` cannot fail
            # whatever x holds -- a missing attribute is undefined and prints
            # empty -- while `(x.a).b` can, because `x.a` is undefined for most
            # x. A plain name or a constant as the subject is what makes the
            # one-step case safe; anything computed can hand back an undefined.
            if not isinstance(n.node, (nodes.Name, nodes.Const)):
                self.apply(out, REQUIRED)
            return out

        if isinstance(n, nodes.NSRef):
            # A namespace read. Layer 2 will follow the field; until then the
            # honest answer is that anything could be in it.
            s = self.node_sym(n)
            s.unknown = True
            return {s.id}

        if isinstance(n, (nodes.Const, nodes.TemplateData)):
            return set()

        if isinstance(n, nodes.Pair):
            # A mapping key that cannot be hashed stops the render -- and it is
            # still part of the mapping, so printing the mapping prints it.
            key = self.expr(n.key)
            self.apply(key, REQUIRED)
            return key | self.expr(n.value)

        if isinstance(n, nodes.CondExpr):
            # The test chooses which value the expression yields, and what is
            # done with that value afterwards is not visible from here.
            self.apply(self.expr(n.test), FLOW | REQUIRED)
            return self.expr(n.expr1) | self.expr(n.expr2)

        if isinstance(n, nodes.Getitem):
            # Subscripting something that cannot be, or with a key of the wrong
            # type, raises.
            # `data[key]` reads out of data whatever key turns out to be, so
            # the result derives from data and "can data reach the output" is a
            # plain yes. Which *part* is read is a different question, and
            # answering it with unknown used to weaken one that was never in
            # doubt. The key chooses among the values, which is steering --
            # and it joins them, because a lookup that misses answers an
            # undefined carrying the key: under a DebugUndefined `{{ d[n] }}`
            # prints "{{ no such element: dict object['<n>'] }}". Steering
            # alone said the key could not reach the output, which is the one
            # thing this analysis promises never to say wrongly.
            base = self.expr(n.node)
            # The key can stop the render too: unhashable, or a type the
            # container cannot take.
            key = self.expr(n.arg)
            self.apply(key, FLOW | REQUIRED)
            self.apply(base, REQUIRED)
            return base | key

        if isinstance(n, nodes.Filter):
            if n.name == "attr":
                # `obj|attr(name)` is a computed lookup like `obj[name]`: the
                # result comes out of obj, and name picks which part -- and
                # joins it, for the reason the subscript above gives.
                out = self.expr(n.node)
                for a in n.args or ():
                    # A name that is not a string stops the render.
                    name = self.expr(a)
                    self.apply(name, FLOW | REQUIRED)
                    out = out | name
                self.apply(out, REQUIRED)
                return out
            out = self.expr(n.node) | self.args_of(n)
            if n.node is None:
                # The leading filter of a {% filter %} block or a block set;
                # its input is the captured body, handled by the caller.
                out = self.args_of(n)
            self.apply(out, REQUIRED)
            return out

        if isinstance(n, nodes.Call):
            fn = n.node
            out = self.args_of(n)
            if isinstance(fn, nodes.Name):
                called = self.node_sym(fn)
                if called.id in self.macro_params:
                    out |= self.call_macro(called, n)
                    self.apply(out, REQUIRED)
                    return out
            if isinstance(fn, nodes.Name) and fn.name == "namespace":
                self.taint(out)
                self.apply(out, REQUIRED)
                return out
            # A call whose body this cannot see: a method on a value, a
            # global, a macro held in a variable.
            #
            # Giving up here cost more than it bought: `{{ msg.strip() }}`
            # reads out of msg and puts the result in the document, which is
            # not in doubt.
            #
            # What the giving-up was for is mutation. `{{ l.append(x) }}`
            # renders nothing and leaves x inside l, so a later `{{ l }}`
            # prints x. That is a dependency rather than a reason to stop: the
            # receiver may now hold the arguments, so it gets an edge from
            # them. The cost is over-reporting, which is the safe direction.
            recv = self.expr(fn.node if isinstance(fn, nodes.Getattr) else fn)
            for sid in recv:
                self.syms[sid].deps |= out
            out |= recv
            self.apply(out, REQUIRED)
            return out

        # Everything else -- operators, comparisons, tests, attribute access,
        # containers, slices, concatenation -- is ordinary data flow: the
        # result derives from every operand.
        out = set()
        for child in n.iter_child_nodes():
            out |= self.expr(child)
        out |= self.args_of(n)
        if can_raise(n):
            self.apply(out, REQUIRED)
        return out

    # --- statements ------------------------------------------------------

    def stmts(self, body):
        for s in body or ():
            self.stmt(s)

    def bind(self, target, srcs):
        """Record that `target` now holds a value derived from `srcs`."""
        if isinstance(target, nodes.Name):
            self.node_sym(target).deps |= srcs
        elif isinstance(target, nodes.NSRef):
            s = self.node_sym(target)
            f = self.namespace_field(s, target.attr)
            if f is not None:
                f.deps |= srcs
                return
            # Not a namespace this is following -- one that was passed in, or
            # one that got away. The write still happened, and writing a field
            # of something that is not a namespace stops the render.
            s.deps |= srcs
            s.unknown = True
            s.roles |= REQUIRED
        elif isinstance(target, (nodes.Tuple, nodes.List)):
            # Unpacking: which element lands where is not tracked, so every
            # target derives from the whole right-hand side -- and the
            # unpacking itself can stop the render whatever is done with the
            # names, for a value that is not iterable or is the wrong length.
            self.apply(srcs, REQUIRED)
            for item in target.items:
                self.bind(item, srcs)
        else:
            self.taint(srcs)

    def capture_body(self, body) -> set[int]:
        """What a block-set or filter block's body would have printed."""
        self.capture.append(set())
        self.stmts(body)
        return self.capture.pop()

    def call_macro(self, sym, call) -> set[int]:
        """Bind a call's arguments to the macro's parameters, and return what
        the macro's body would print."""
        if sym.id in self.in_macro:
            # Recursive; the fixpoint below already carries roles around the
            # cycle, and re-entering would not terminate.
            return set()
        self.in_macro.add(sym.id)
        for i, param in enumerate(self.macro_params[sym.id]):
            srcs = set()
            if i < len(call.args):
                srcs = self.expr(call.args[i])
            for kw in call.kwargs or ():
                if param is not None and kw.key == param.name:
                    srcs |= self.expr(kw.value)
            if srcs and param is not None:
                param.deps |= srcs
        self.in_macro.discard(sym.id)
        return self.macro_out[sym.id]

    def if_stmt(self, n, chain_fails):
        test = self.expr(n.test)
        self.apply(test, FLOW)
        if chain_fails:
            self.apply(test, REQUIRED)
        self.stmts(n.body)
        for elif_ in getattr(n, "elif_", None) or ():
            self.if_stmt(elif_, chain_fails)
        self.stmts(n.else_)

    def stmt(self, n):
        if isinstance(n, nodes.Output):
            for item in n.nodes:
                self.emit(self.expr(item))

        elif isinstance(n, nodes.If):
            # chainFails is computed once for the whole chain: jinja2 hangs the
            # else off the outermost if, so an elif's own subtree does not hold
            # the arm that runs when it is false -- and it decides whether that
            # arm runs.
            self.if_stmt(n, can_fail_in(n.body) or can_fail_in(n.elif_)
                         or can_fail_in(n.else_))

        elif isinstance(n, nodes.For):
            # The iterable is evaluated outside the loop's own frame.
            srcs = self.expr(n.iter)
            # Its length decides how many times the body runs, so it can change
            # the output without appearing in it -- and iterating something
            # that is not iterable raises.
            self.apply(srcs, FLOW | REQUIRED)
            self.push(n)
            self.bind(n.target, srcs)
            # `loop` is supplied by the loop, not by the caller, and what it
            # reports -- index, length, first, last -- is derived from the
            # sequence being walked.
            loop = self.scope_symbol("loop")
            if loop is not None:
                loop.deps |= srcs
            loop_test = self.expr(n.test)
            self.apply(loop_test, FLOW)
            if can_fail_in(n.body):
                self.apply(loop_test, REQUIRED)
            self.stmts(n.body)
            self.stmts(n.else_)
            self.pop()

        elif isinstance(n, nodes.Assign):
            if (isinstance(n.node, nodes.Call)
                    and isinstance(n.node.node, nodes.Name)
                    and n.node.node.name == "namespace"
                    and isinstance(n.target, nodes.Name)):
                self.declare_namespace(self.node_sym(n.target), n.node)
            else:
                self.bind(n.target, self.expr(n.node))

        elif isinstance(n, nodes.AssignBlock):
            self.push(n)
            srcs = self.capture_body(n.body)
            self.pop()
            if n.filter is not None:
                srcs |= self.expr(n.filter)
            self.bind(n.target, srcs)

        elif isinstance(n, nodes.FilterBlock):
            self.push(n)
            srcs = self.capture_body(n.body)
            self.pop()
            srcs |= self.expr(n.filter)
            self.emit(srcs)

        elif isinstance(n, nodes.Macro):
            self.push(n)
            params = [self.scope_symbol(a.name) for a in n.args]
            # Outside the macro's own scope: a macro binds its name where it is
            # written, and its body may declare that name again.
            sym = self.enclosing_scope_symbol(n.name)
            for param, default in zip(n.args[len(n.args) - len(n.defaults):],
                                      n.defaults):
                self.bind(param, self.expr(default))
            out = self.capture_body(n.body)
            self.pop()
            if sym is not None:
                self.macro_params[sym.id] = params
                self.macro_out[sym.id] = out
            else:
                # The macro's name is not a binding in any scope: jinja2's
                # first-mention rule resolved it outward to the caller's
                # variables, which is what a dead `{% if %}` mentioning the
                # name first does. There is nothing to hang the body on, so a
                # call cannot be matched to it -- and dropping what the body
                # captured makes everything the macro prints invisible, which
                # is a false negative and the one answer this must never give.
                #
                # A defined macro can be called, and if it is, its body reaches
                # the document. Claiming that for one never called over-claims
                # in the safe direction; claiming nothing does not.
                self.emit(out)

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
            self.apply(self.expr(n.template), FLOW | REQUIRED)
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
            self.apply(self.expr(n.template), FLOW | REQUIRED)
            name = const_name(n.template)
            if name is None:
                if n.with_context:
                    self.opaque_sink = True
            elif n.with_context:
                self.inherit(name)
            # What it binds is a module, or a macro from one. Calling that
            # reaches code this does not follow.
            for bound in imported_names(n):
                self.lookup(bound).unknown = True

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
        self.seal_namespaces()
        self.propagate()
        out = {}
        for name, s in sorted(self.context.items()):
            out[name] = {
                "output": bool(s.roles & OUTPUT),
                "flow": bool(s.roles & FLOW),
                "required": bool(s.roles & REQUIRED),
                # An unknown answer has no reliable negative: the name may
                # reach the output by a route this could not follow. A name
                # without it is a real answer in both directions.
                "unknown": bool(s.unknown or self.opaque_sink),
            }
        return out


# The expression kinds whose operands can stop a render. A bare print, an
# assignment, a container literal and an attribute access are deliberately
# absent: jinja2 answers Undefined for a missing attribute rather than raising,
# and printing an Undefined is what the undefined policy is for.
_RAISING = tuple(getattr(nodes, n) for n in
                 ("Add", "Sub", "Mul", "Div", "FloorDiv", "Mod", "Pow", "And",
                  "Or", "Not", "Neg", "Pos", "Compare", "Operand", "Test")
                 if hasattr(nodes, n))


def can_raise(n) -> bool:
    if isinstance(n, _RAISING):
        return True
    return False


# The constructs that can stop a render, asked of a whole body rather than of
# one value. A test guards the code beneath it, so if that code can fail the
# test decides whether it does.
# NSRef is in the list because it appears only as the target of a `{% set %}`,
# and writing a field needs something to write it to: the `=` form wants a
# namespace, and the block form does an item assignment that a list, a string or
# a None refuses.
_CAN_FAIL = tuple(getattr(nodes, n) for n in
                  ("Add", "Sub", "Mul", "Div", "FloorDiv", "Mod", "Pow", "And",
                   "Or", "Not", "Neg", "Pos", "Compare", "Operand", "Test",
                   "Filter", "Call", "Getitem", "Pair", "For", "Include",
                   "Extends", "Import", "FromImport", "NSRef")
                  if hasattr(nodes, n))


def can_fail_in(body) -> bool:
    if body is None:
        return False
    for n in body if isinstance(body, list) else [body]:
        if isinstance(n, _CAN_FAIL):
            return True
        for _ in n.find_all(_CAN_FAIL):
            return True
    return False


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
    a.em = syntax_emit.Emitter(globals_)
    a.em.stmt(tree)
    a.push(tree)
    a.stmts(tree.body)
    a.pop()
    return {k: v for k, v in a.result().items() if k not in set(globals_)}
