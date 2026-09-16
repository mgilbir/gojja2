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
