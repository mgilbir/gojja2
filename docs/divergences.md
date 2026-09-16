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

## Lazy sequence filters

```jinja
{% if items|selectattr("active") %}...{% endif %}
```

jinja2's `map`, `select`, `reject`, `selectattr`, `rejectattr` and `unique`
return generators. gojja2's return lists. Anything that *consumes* the result
-- iterating it, `|list`, `|join`, `|first`, `|sort` -- behaves identically.
Three things do not:

| template | jinja2 | gojja2 |
|---|---|---|
| `{{ [1,2]\|map("string") }}` | `<generator object ... at 0x7f9c...>` | `['1', '2']` |
| `{% if items\|selectattr("active") %}` | always taken | taken when non-empty |
| `{{ items\|selectattr("active")\|length }}` | `TypeError: object of type 'generator' has no len()` | the count |

The first cannot be matched by anyone: the address differs between two runs of
CPython itself, which is why the corpus case that prints one is marked
*ungradable* rather than failing.

The second and third could be matched, and are not. A generator is always
truthy, so in jinja2 `{% if items|selectattr("active") %}` runs its body even
when nothing was selected -- a long-standing footgun that the documentation
tells you to spell `|selectattr("active")|list` around. Reproducing it would
mean building the trap on purpose, and a template that guards a section on an
empty filter result would render the section. gojja2 answers the question the
template asked. This is the one divergence here that can change what a working
template renders, and it changes it toward what the author meant; if you are
porting templates, `|list` before `|length` or a truth test is exact in both.

A knock-on: a filter that raises does so at the point gojja2 applies it, where
jinja2 defers until the generator is consumed. The exception is the same; where
it surfaces can differ by a tag or two.

## Limits on allocation

### A width limit on `**`

```jinja
{{ 2 ** 100000000 }}
```

CPython will try to materialise the integer. gojja2 refuses with `OverflowError`
once the result would exceed 2**20 bits (128 KiB), because an exponent in a
template is frequently attacker-influenced and the honest answer is a
denial-of-service. Integers below that limit are exact and unbounded by machine
word size, so `{{ 2 ** 100 }}` still renders all 31 digits.

### A length limit on repetition

```jinja
{{ "x" * 2147483648 }}
```

Repeating a string or a sequence is refused once the result would exceed
2**31 elements, for the same reason: the count is often attacker-influenced.
CPython would attempt the allocation.

That cap is on a single result. A repetition just under it is still gigabytes,
so the render's budget is charged for what a repetition is about to allocate
*before* it allocates -- bytes against the output budget, elements against the
iteration budget. Charging it afterwards would be charging for memory that is
already gone.

### A size limit on constant folding

```jinja
{{ "x" * 1000000000 }}
{{ "x" * 60000 + "x" * 60000 }}
```

Both are constant, so jinja2's optimizer and gojja2's evaluate them at compile
time. There is no render at compile time and so no budget to charge, which made
`env.FromString` on the first of these allocate a gigabyte before anything had
asked for a render; the second doubles for every level a template nests it.

gojja2 declines to fold a constant above 64 KiB, leaving the expression to be
evaluated at render time where the budget bounds it. The rendered result is
identical -- it is just not computed early. Ordinary constants still fold, and
nothing written on purpose builds a 64 KiB one this way.

## A bound on nesting depth

```jinja
{{ [[[[[ ... 100000 levels ... ]]]]] }}
```

CPython raises `RecursionError` at around a thousand frames. Go grows a
goroutine's stack on demand, so gojja2 would parse a million levels happily and
then die on an allocation it cannot recover from.

gojja2 refuses past 1,000 levels of expression or statement nesting with a
`TemplateSyntaxError`. Templates are frequently attacker-supplied and nothing
written on purpose nests ten deep, let alone a thousand, so the limit is
generous and the failure is clean. It is a safety control rather than a
behavioural choice, which is why the exception class differs from CPython's.

Runtime recursion -- a template that includes, extends or calls itself without
a base case -- is bounded separately at 100 levels, controlled by
`WithMaxRecursion`, and *does* raise `RecursionError` with CPython's wording.
The configured limit is on the error's `Limit` field rather than in the message.

## A budget on the work of one render

```jinja
{% for i in range(10000000000) %}{% endfor %}
{{ range(10000000000)|list }}
```

CPython runs both until the machine gives up: the first spends hours, the
second allocates until the OOM killer arrives. jinja2 has no bound on either,
because it expects the caller to be running templates it wrote itself.

gojja2 bounds one render three ways. The `context.Context` every render takes
is the precise tool -- cancel it or give it a deadline and the render stops at
the next loop pass or output write, returning an error wrapping `ctx.Err()`.
Behind it sit two backstops for a caller who passes `context.Background()`:
10,000,000 loop iterations (`WithMaxIterations`, `ErrTooManyIterations`) and
256 MiB of output (`WithMaxOutputBytes`, `ErrOutputTooLarge`). Both are
generous by design -- no template written on purpose comes near either -- and
both can be turned off with a non-positive value when the templates are trusted
and a deadline is doing the job instead.

The budget counts every walk that can be made large from a template, not just
`{% for %}`: a filter materialising a sequence, `f(*iterable)`, `{% set a, b =
iterable %}` and `list.extend` all charge as they go, so none of them can
allocate its way past the bound before the bound is consulted. It is shared
across `{% include %}` and `{% extends %}`, so a nested render cannot start a
fresh allowance.

Output is counted wherever it lands, including text captured by
`{% filter %}`, a block `{% set %}` or a macro body. Text that passes through
two buffers is therefore counted twice; the buffers are the memory the bound
exists to protect, so they are what has to be counted.

Like the nesting bound, this is a safety control rather than a behavioural
choice, which is why the errors have no CPython counterpart. Asserted by the
tests in `limits_test.go`.

## Identifier characters

jinja2 matches names against a table generated from Python's `str.isidentifier`.
gojja2 approximates it with Unicode categories: a name starts with `_`, a letter
or `Nl`, and continues with those plus `Nd`, `Mn`, `Mc` and `Pc`. The two agree
on every identifier anyone writes; they could differ on exotic code points, in
which case gojja2 reports `unexpected char` where jinja2 reports
`Invalid character in identifier`.

## lipsum() and random

`lipsum()` and the `random` filter draw from a random source. Their output
cannot match CPython's and is excluded from conformance comparison.

## Python object introspection

`__class__` is implemented. Every value answers it with a type object that has
a name, a repr and equality:

```jinja
{{ true.__class__ }}            <class 'bool'>
{{ nope.__class__.__name__ }}   Undefined
{{ ('x'|safe).__class__ }}      <class 'markupsafe.Markup'>
```

That is a statement about gojja2's own value model, and it is inert: there is
nothing behind it. Two of Jinja's sandbox-escape tests go further, and those
are not implemented:

```jinja
{{ foo.__class__.__subclasses__() }}
{{ "{0.__call__.__builtins__[__import__]}" | attr("format")(x) }}
```

The first wants the list of a class's subclasses; the second walks from a bound
method to the interpreter's builtins and out to `__import__`. Neither has a
counterpart in Go -- there is no class hierarchy to enumerate and no import
machinery to reach. Matching them would mean building a decoy of the escape
route the tests exist to document, which would be worse than not having one:
the next reader would have to work out that the ladder leads nowhere.

These are the only two entries in `testdata/known_failures.txt`, and this is
the one place where not matching CPython is the point.

Note what is *not* in this category. `str.format`'s replacement fields take
attribute and index accessors -- `"{0.foo}"`, `"{user[id]}"` -- and that is an
ordinary documented feature, implemented and matched. The tests above only use
it as a route to `__class__`.

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
