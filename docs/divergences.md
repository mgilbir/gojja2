# Deliberate divergences from CPython jinja2

CPython's `jinja2` is the specification, and everywhere gojja2 disagrees with
it that is a bug. This file lists the handful of places where the disagreement
is intentional, along with what gojja2 does instead. Each one is asserted by a
test, so it cannot quietly turn into something else.

Anything not listed here is a bug. Please report it with the template and the
output `make ask T='...'` gives.

## Complex numbers

```jinja
{{ (-8) ** (1/3) }}
```

CPython renders `(1.0000000000000002+1.7320508075688772j)`. A negative base
raised to a fractional power is the only way a template can reach Python's
`complex` type: there is no complex literal, no filter accepts or returns one,
and every arithmetic operator on one is a type error in any template that then
tries to use the result.

gojja2 raises `ValueError` rather than inventing an approximation that would be
wrong in a different way. Asserted by `TestOperatorsMatchCPython`.

## `\N{...}` escapes in string literals

```jinja
{{ "\N{BULLET}" }}
```

Resolving a code point by its Unicode name needs the full name database, which
Go's standard library does not carry and which is not worth a megabyte of
generated tables for a construct no real template uses.

gojja2 reports `unknown Unicode character name` for any `\N{...}`, and CPython's
own `malformed \N character escape` for a malformed one. Every other escape --
`\xNN`, `\uNNNN`, `\UNNNNNNNN`, octal, and the single-character escapes -- is
exact, including CPython's quirk that `"\é"` decodes to the four characters
`\xe9`.

## A width limit on `**`

```jinja
{{ 2 ** 100000000 }}
```

CPython will try to materialise the integer. gojja2 refuses with `OverflowError`
once the result would exceed 2**20 bits (128 KiB), because an exponent in a
template is frequently attacker-influenced and the honest answer is a
denial-of-service. Integers below that limit are exact and unbounded by machine
word size, so `{{ 2 ** 100 }}` still renders all 31 digits.

## Identifier characters

jinja2 matches names against a table generated from Python's `str.isidentifier`.
gojja2 approximates it with Unicode categories: a name starts with `_`, a letter
or `Nl`, and continues with those plus `Nd`, `Mn`, `Mc` and `Pc`. The two agree
on every identifier anyone writes; they could differ on exotic code points, in
which case gojja2 reports `unexpected char` where jinja2 reports
`Invalid character in identifier`.

## Lazy sequence filters

```jinja
{{ [1,2]|map("string") }}
```

jinja2's `map`, `select`, `reject`, `selectattr`, `rejectattr` and `unique`
return generators. Printing one without `|list` renders
`<generator object sync_do_map at 0x7f9c...>` -- a memory address, which differs
between two runs of CPython itself, so no implementation can reproduce it.

gojja2's sequence filters are eager and render the list. Every use that does
anything with the result -- iterating it, `|list`, `|join`, `|first` -- behaves
identically.

## RecursionError wording

A template that includes, extends or calls itself without a base case raises
`RecursionError` in both. CPython's message names the interpreter operation
that happened to hit the limit ("maximum recursion depth exceeded while calling
a Python object", "... in comparison"), which varies with the call shape and
describes nothing that exists in a Go program.

gojja2 raises `RecursionError` with a message naming the limit and its
configured value, which `WithMaxRecursion` controls.

## lipsum() and random

`lipsum()` and the `random` filter draw from a random source. Their output
cannot match CPython's and is excluded from conformance comparison.

## Python object introspection

```jinja
{{ true.__class__ }}
{{ cls|attr("__subclasses__")() }}
{{ "a{0.__class__}b".format(42) }}
```

jinja2 templates run against real Python objects, so they can reach
`__class__`, `__subclasses__`, `__builtins__` and `__import__`, and a format
spec like `{0.foo}` takes a Python attribute. Jinja's own test suite covers
these precisely because they are the shape of a sandbox escape.

gojja2 has no Python object graph behind its values. These attributes do not
exist rather than being blocked, so the templates cannot be reproduced at all;
they raise instead. Every such case is listed in
`testdata/known_failures.txt`.

This is the one place where not matching CPython is the point.

## A macro containing a context-free include

```jinja
{% macro m(x) %}{% include "other.txt" without context %}{% endmacro %}{{ m(1) }}
```

`{% include ... without context %}` compiles, in jinja2, to a yield straight
into the enclosing function's output stream. Inside a macro that turns the
macro itself into a Python generator, which `{{ m(1) }}` then renders as
`<generator object root.<locals>.macro at 0x7f...>` -- an address, and a body
that never ran.

gojja2 renders the macro. The bypass itself *is* reproduced -- a context-free
include still escapes an enclosing `{% filter %}` buffer, which is covered by
the corpus -- but turning a function into an unconsumed generator is an
artefact of compiling to Python, not a property of the language.

## Comparison order inside a long sort

Sorting values that cannot be compared raises, and the message names the two
operands the sort happened to reach first. gojja2 reproduces CPython's order
exactly for lists of 64 elements or fewer, which is where CPython sorts a list
as a single run.

Above that, CPython splits the list into runs and merges them, and gojja2 uses
a stable sort of its own. The result is identical; only which pair a failing
comparison names can differ.
