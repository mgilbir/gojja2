#!/usr/bin/env python3
# Copyright 2026 The gojja2 Authors
# SPDX-License-Identifier: Apache-2.0
"""Synthesise a JSON context for a template by watching it render.

Templates harvested from real projects come without their context: an Ansible
role or an MkDocs theme gets one from the host application, not from a file
next to it. Without a context the template renders to a page of undefineds,
which tests nothing.

`jinja2.meta.find_undeclared_variables` only reports top-level names. It cannot
tell you that `page.meta.title` is wanted, or that `nav` is iterated -- and the
*shape* is what matters. So the shape is recorded by rendering once against a
proxy that logs every access and hands back another proxy, then reading the log
back as a plain JSON object.

The second render is the important half. Rendering again against that plain
object must produce byte-identical output; if it does not, the template needs
something a JSON context cannot express -- a callable, a Python object with
methods, a host-application filter -- and no golden is emitted for it. Those
go to an `unsupported/` bucket instead, which is worth as much as the corpus:
it is the list of what a JSON-context corpus structurally cannot cover.
"""

from __future__ import annotations

import jinja2
import jinja2.meta

# How many children a proxy yields when iterated. Three is enough for a
# template to exercise loop.first, loop.last and the branch between them,
# and small enough to keep a recorded context readable.
ITERATION_WIDTH = 3

# The filler a leaf becomes in the emitted context. It is deliberately not a
# number or a date: a template that formats it will fail loudly in both
# implementations rather than quietly producing different text.
LEAF_VALUE = "x"

# Names a proxy must not claim to have.
#
# These are asked for with hasattr() by machinery outside the template --
# markupsafe checks __html__ before escaping, jinja2 checks jinja_pass_arg on a
# filter. A proxy that answers them is taken for something it is not, so
# __getattr__ raises for these and for every dunder. Defining __html__ as a
# real method instead does not work: hasattr then finds it and the raise
# escapes from inside escape().
PASSTHROUGH = {
    "__html__",
    "__htmlsafe__",
    "jinja_pass_arg",
    "_ipython_canary_method_should_not_exist_",
}


class Node:
    """One recorded position in the context tree."""

    def __init__(self) -> None:
        self.attrs: dict[str, Node] = {}
        self.iterated = False
        self.used_as_leaf = False

    def child(self, name: str) -> Node:
        node = self.attrs.get(name)
        if node is None:
            node = Node()
            self.attrs[name] = node
        return node

    def to_json(self) -> object:
        """Read the recorded tree back as a plain JSON value."""
        if self.iterated:
            # Every element shares the shape recorded under the proxy's
            # children, which is what a loop body actually exercised.
            element = self._object() if self.attrs else LEAF_VALUE
            return [element] * ITERATION_WIDTH
        if self.attrs:
            return self._object()
        return LEAF_VALUE

    def _object(self) -> dict:
        return {name: node.to_json() for name, node in self.attrs.items()}


class Proxy:
    """Stands in for a context value and records what is asked of it."""

    __slots__ = ("_node",)

    def __init__(self, node: Node) -> None:
        object.__setattr__(self, "_node", node)

    # -- access -------------------------------------------------------------

    def __getattr__(self, name: str):
        if name in PASSTHROUGH or name.startswith("__") and name.endswith("__"):
            raise AttributeError(name)
        return Proxy(self._node.child(name))

    def __getitem__(self, key):
        # An integer subscript is an index into a sequence, not a named
        # member, so it records the same child every element shares.
        if isinstance(key, int):
            self._node.iterated = True
            return Proxy(self._node)
        return Proxy(self._node.child(str(key)))

    def __iter__(self):
        self._node.iterated = True
        return iter([Proxy(self._node) for _ in range(ITERATION_WIDTH)])

    def __len__(self):
        self._node.iterated = True
        return ITERATION_WIDTH

    def __contains__(self, item):
        self._node.iterated = True
        return False

    # -- use as a value -----------------------------------------------------

    def __str__(self) -> str:
        self._node.used_as_leaf = True
        return LEAF_VALUE

    def __repr__(self) -> str:
        return LEAF_VALUE

    def __bool__(self) -> bool:
        # True, so a template's guarded branches are entered and recorded.
        # A False here would leave most of the template unexplored.
        return True

    def __eq__(self, other) -> bool:
        return False

    def __hash__(self) -> int:
        return id(self._node)


def record(env: jinja2.Environment, source: str, names: list[str]) -> tuple[dict, str]:
    """Render source against proxies, returning the context and the output."""
    roots = {name: Node() for name in names}
    context = {name: Proxy(node) for name, node in roots.items()}
    output = env.from_string(source).render(context)
    return {name: node.to_json() for name, node in roots.items()}, output


def top_level_names(env: jinja2.Environment, source: str) -> list[str]:
    """The undeclared variables a template reads, as jinja2 reports them."""
    return sorted(jinja2.meta.find_undeclared_variables(env.parse(source)))


class Unsupported(Exception):
    """The template cannot be driven by a JSON-only context."""


def synthesise(env: jinja2.Environment, source: str) -> tuple[dict, str]:
    """Return a JSON context for source, and the output it produces.

    Raises Unsupported when the recorded context does not reproduce the
    proxy render, which is what happens when a template needs a callable, a
    host-application filter, or an object with behaviour rather than shape.
    """
    try:
        names = top_level_names(env, source)
    except jinja2.TemplateError as exc:
        raise Unsupported(f"does not compile: {exc}") from exc
    return synthesise_with(env, source, names)


def synthesise_with(
    env: jinja2.Environment, source: str, names: list[str]
) -> tuple[dict, str]:
    """synthesise, for a caller that already knows which names to proxy.

    An {% include %} shares its parent's context, so a template's own
    undeclared variables are not the whole list -- the caller gathers them
    across the include closure and passes them in.
    """
    try:
        context, proxied = record(env, source, names)
    except Exception as exc:  # noqa: BLE001 - any failure means "cannot drive"
        raise Unsupported(f"proxy render failed: {type(exc).__name__}: {exc}") from exc

    # Pass two. The proxies are gone; only JSON remains.
    try:
        plain = env.from_string(source).render(context)
    except Exception as exc:  # noqa: BLE001
        raise Unsupported(f"json render failed: {type(exc).__name__}: {exc}") from exc

    # The two renders must agree. A proxy answers anything asked of it, so it
    # can drive a template down a path that the flat JSON standing in for it
    # cannot -- a value reached both as a mapping and as a sequence, say. When
    # that happens the recorded context is not equivalent to what was recorded
    # against, and a golden taken from it would be recording an accident.
    if proxied != plain:
        raise Unsupported("recorded context does not reproduce the proxy render")

    # Rendering is expected to be a function of the context; anything else
    # cannot have a stable golden.
    if plain != env.from_string(source).render(context):
        raise Unsupported("render is not deterministic")

    return context, plain
