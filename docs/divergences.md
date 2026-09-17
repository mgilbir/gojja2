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

jinja2's `map`, `select`, `reject`, `selectattr`, `rejectattr`, `unique` and
`items` return generators. gojja2's return lists. Anything that *consumes* the result
-- iterating it, `|list`, `|join`, `|first`, `|sort` -- behaves identically.
Three things do not:

| template | jinja2 | gojja2 |
|---|---|---|
| `{{ [1,2]\|map("string") }}` | `<generator object ... at 0x7f9c...>` | `['1', '2']` |
| `{% if items\|selectattr("active") %}` | always taken | taken when non-empty |
| `{{ items\|selectattr("active")\|length }}` | `TypeError: object of type 'generator' has no len()` | the count |
| `{{ tools\|map(attribute="f")\|tojson }}` | `TypeError: Object of type generator is not JSON serializable` | the JSON |

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

The fourth is not hypothetical: DeepSeek-R1's own chat template, as vendored by
llama.cpp, writes `{{ tools | map(attribute='function') | tojson(indent=2) }}`,
which raises under CPython jinja2 and renders under gojja2. Both cases are in
the chat-templates corpus, listed in testdata/known_failures.txt.

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

### A budget on constant folding

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

### A charge before every sized allocation

Anything a template sizes from a number it chose -- a pad width, an indent, a
rounding precision, a slice or batch count, `lipsum`'s paragraph count, the
result of a `replace` -- is charged against the render budget *before* it is
allocated, and refused outright past a hard ceiling of 2**31 bytes or elements.

The ceiling exists because a zero or negative budget means "unbounded", and
unbounded must still not mean "allocate 2**63 bytes". It is the same ceiling
`value.repeat` has always applied to `*`, so both halves of the engine refuse
the same sizes.

Where CPython raises `MemoryError` for these, gojja2 raises its own
`OverflowError` or fails the render with [ErrOutputTooLarge] /
[ErrTooManyIterations], depending on which bound was reached. CPython has no
budget to exceed, so there is nothing to match here; the divergence is the
bound itself, which this file already records above.

### A backstop on panics

A panic anywhere inside gojja2 is turned into a render error wrapping
[ErrInternal], at both entry points: `FromString`/`GetTemplate` and
`Render`/`RenderString`.

This is a backstop, not a licence. A template engine renders input its caller
does not control, so unwinding the caller's goroutine is never the right answer
to a bad template -- the render failed, so the render should say so. Every panic
that reaches it is a bug here rather than in the template, and the error says
so and carries the panic value, so a report can name it.

Constant folding recovers separately and more quietly: an optimisation must
never fail worse than not optimising, so a fold that panics simply does not
happen and the expression is left for runtime, where the render's budget
applies to it.

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

What is counted is the depth of the parsed *tree*, one level per node on a
root-to-leaf path, and not how deeply the parser happened to recurse. That
matters because a template can nest without any bracket at all:

```jinja
{{ not not not ... }}      {{ ------- ... 1 }}      {{ x|f|f|f|f ... }}
```

Each of those is built by a loop, and each produces a tree as deep as it is
long -- which the constant folder, the frame-local visitor, the dependency
checker and the evaluator all then descend once per level. `~` and a
comparison chain are the exception in the other direction: like jinja2's own
`Concat` and `Compare` they are one node over a flat list of operands, so
`a ~ b ~ c ~ ...` is one level however long it runs.

Runtime recursion -- a template that includes, extends or calls itself without
a base case -- is bounded separately at 100 levels, controlled by
`WithMaxRecursion`, and *does* raise `RecursionError` with CPython's wording.
The configured limit is on the error's `Limit` field rather than in the message.

## How deep a value may be before printing it fails

```jinja
{% set ns = namespace(t=[0]) %}
{% for i in range(5000) %}{% set ns.t = [ns.t] %}{% endfor %}
{{ ns.t }}
```

The depth of a *value* is chosen at render time, not at compile time -- the
nesting bound above is about the template's own text, and says nothing about
what a loop builds. CPython walks such a value with its interpreter stack and
raises `RecursionError` at around a thousand levels, with a different message
for each walk: `while getting the repr of an object`, `in comparison`, `while
encoding a JSON object`.

gojja2 matches that wherever the walk can report a failure, and does not need
to wall the walk at all where it cannot:

| walk | CPython | gojja2 |
|---|---|---|
| `==`, `<`, `\|sort`, `\|min` | `RecursionError` at ~991 | the same error, at 1,000 |
| `\|tojson` | `RecursionError` at ~986 | the same error, at 1,000 |
| `\|pprint` | `RecursionError` at ~326 | the same error, at 1,000 |
| `{{ v }}`, `\|string`, `\|upper`, a dict key | `RecursionError` at ~989 | renders |
| hashing a tuple | no limit | no limit |

The last two rows are the divergence. `str()` and `repr()` are reached from
more than a hundred places here, most of them building an error message, and
none of them can return an error -- so those walks are written iteratively and
have no depth to exceed. That is strictly safer than the alternative: before,
every one of those call sites was somewhere a deep value ended the process
with a Go stack overflow, which is a fatal error rather than a panic and so is
not something the backstop in `catchPanic` can turn into a failed render.

Hashing a tuple has no wall in either implementation -- CPython's is iterative
too, and hashes a 65,000-deep tuple without complaint.

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

## The default autoescape extension set

```go
gojja2.SelectAutoescape()        // html, htm, xml, xhtml
```

jinja2's `select_autoescape()` defaults to `html, htm, xml`. gojja2 adds
`xhtml`.

The extension set only ever turns escaping *on* -- a name matching nothing
falls through to `Default`, which is `false` -- so a superset is strictly safer
than jinja2's, and it is what gojja2 shipped before the set was normalised.
Narrowing it to match would make this package escape less than it used to, which
is the wrong direction for the one setting whose failure mode is cross-site
scripting.

Everything else about the policy follows jinja2 exactly, including the parts
that are easy to get wrong: matching ignores case on both the template name and
the configured extensions, a leading dot is optional, matching happens on a
whole extension rather than on any trailing substring, and a template compiled
from a string is escaped by default (jinja2's `default_for_string=True`).
Asserted by the tests in `autoescape_test.go`.

## `len()` of a very long range

```jinja
{{ range(-9223372036854775808, 0)|length }}
```

CPython raises `OverflowError: Python int too large to convert to C ssize_t`,
because `len()` narrows its result to a `Py_ssize_t`. gojja2 reproduces that,
including the wording and the exact boundary: a length of `2**63-1` is returned
and `2**63` raises.

Internally the length is still computed exactly, in arbitrary precision, and
only clamped where a Go `int` is required -- iterating or indexing such a range.
That clamp is unobservable: a loop over a range that long is stopped by the
render budget long before the count could matter. Asserted by
`TestRangeLengthDoesNotOverflow` and graded against CPython over 1,452
start/stop/step combinations.

## `|pprint` of a value that contains itself

```jinja
{{ cyclic|pprint }}
```

CPython's `pprint` marks a container it has already entered as
`<Recursion on dict with id=131095544303808>`. The id is the object's address,
which differs between two runs of CPython itself, so this case is no more
gradable than `lipsum()` is. gojja2 prints the same form, with the address of
its own container.

A cyclic value reaches `pprint` at all only when its `repr` is too wide to
print on one line; a small one collapses to `{...}` first, which is exact.

## No automatic template reload

jinja2's `Environment` takes `auto_reload=True` and recompiles a template whose
source has changed. gojja2 does not, because its `Loader` interface returns
source and nothing else -- there is no freshness to consult, and inventing one
would mean every loader implementing a second method.

`Environment.ClearCache` and `Environment.ForgetTemplate` are the supported way
to pick up an edit; the template cache is otherwise a bounded LRU, defaulting to
the same 400 entries jinja2 uses.

## How promptly a cancelled render stops

A render reads its context at three points: a loop iteration, an output write,
and a filter's call to `State.Poll`. Cancellation is noticed at the first of
those it reaches, not at the instant it happens -- reading the context on every
operation would put a synchronisation on the inner loop of every template.

The built-in filters that do sustained work without writing output poll as they
go, so a cancelled render stops within microseconds rather than at the end of
whatever was running. A filter registered by a caller that loops without
writing output should poll too; one that does not is a region nothing can
interrupt.

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
