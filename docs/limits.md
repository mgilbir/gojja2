# What bounds a render

CPython jinja2 bounds nothing. It expects the caller to be running templates it
wrote itself, so `{% for i in range(10000000000) %}{% endfor %}` spends hours and
`{{ range(10000000000)|list }}` allocates until the OOM killer arrives. gojja2
renders templates whose author its caller may not be, so it bounds them.

**If you read one thing here, read this:** give every render a
`context.Context` with a deadline. That is the precise tool, and it is the one
that matches what you actually care about. Everything else on this page is a
backstop for a caller who passed `context.Background()`.

These are safety controls, not behavioural choices. That is why their errors
have no CPython counterpart, and why they live here rather than in
[divergences.md](divergences.md): CPython has no budget to exceed, so there is
nothing to match. What *is* a divergence is the existence of the bound, and
that is what this page records.

## The bounds

| what | default | how to change it | what you get |
|---|---|---|---|
| wall clock, cancellation | none | the `context.Context` every render takes | error wrapping `ctx.Err()` |
| loop iterations | 10,000,000 | `WithMaxIterations` | `ErrTooManyIterations` |
| output bytes | 256 MiB | `WithMaxOutputBytes` | `ErrOutputTooLarge` |
| include/extends/macro/block nesting | 100 | `WithMaxRecursion` | `RecursionError` |
| parse-tree nesting | 1,000 | fixed | `TemplateSyntaxError` |
| width of a `**` result | 2**20 bits | fixed | `OverflowError` |
| size of one repetition | 2**31 elements | fixed | `OverflowError` |
| any template-chosen allocation | 2**31 bytes or elements | fixed | `OverflowError` |
| a constant the optimizer will fold | 64 KiB | fixed | no error; the fold is declined and the work moves to render time, where the budget applies |

**Zero means *the default*, not "off", for all three configurable bounds.** To
remove one, pass a negative value or use `WithoutLimits()`. That way a
configuration nobody filled in -- a struct deserialised from YAML or flags -- is
the bounded one.

The iteration and output budgets are carried on the render's `*State` and shared
across `{% include %}`, `{% extends %}` and `{% import %}`, so a nested render
cannot start a fresh allowance.

If you are writing a filter, a test or a global of your own, the budget is your
responsibility too: charge a template-chosen size with `State.ChargeBytes` or
`State.ChargeItems` *before* you allocate it, and call `State.Poll` in any loop
that does sustained work without writing output. [extending.md](extending.md)
has the contract in full, with runnable examples.

---

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
`OverflowError` or fails the render with `ErrOutputTooLarge` /
`ErrTooManyIterations`, depending on which bound was reached. CPython has no
budget to exceed, so there is nothing to match here; the divergence is the
bound itself, which this file already records above.

Not all of them reach a `MemoryError` at all. `{{ x|slice(10000000000000000000000) }}`
is a perfectly legal `range()` in CPython, which then walks it, so the template
does not fail -- it runs until something outside the process stops it. gojja2
saturates the count and charges it, so the same template is an `OverflowError`
in bounded time. The count is charged rather than clamped: clamping a refusal
into "allocate the maximum" is the failure this ceiling exists to prevent.

### A backstop on panics

A panic anywhere inside gojja2 is turned into a render error wrapping
`ErrInternal`, at both entry points: `FromString`/`GetTemplate` and
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

CPython has three wordings for that error, and for most constructs which one
you get is not a property of the template. It records where *CPython's own*
stack ran out, so the identical recursion reports different messages depending
only on how many frames the caller was already using:

```python
# the same template, rendered from N frames deep
N=0   maximum recursion depth exceeded
N=1   maximum recursion depth exceeded while calling a Python object
N=2   maximum recursion depth exceeded
```

A macro calling itself, a recursive loop, a block reference and `{% import %}`
all move like that. Only two were stable at every depth tried:

| construct | message |
|---|---|
| `{% include %}` | `maximum recursion depth exceeded while calling a Python object` |
| an `{% extends %}` cycle | `maximum recursion depth exceeded in comparison` |

gojja2 reports CPython's wording for those two, and the C-level wording for
everything else. That last choice is arbitrary, and deliberately so: any
wording picked for the unstable constructs encodes the stack depth of whichever
harness recorded it. Two of the imported MiniJinja fixtures were captured with
`while calling a Python object` for exactly that reason, and a plain
`maximum recursion depth exceeded` -- which is what a shallow stack gives --
would make them fail. There is no answer here that is right in both harnesses,
so nothing is gained by changing it.

Autoescaping and `loop.previtem` move the wording too: the first because
markupsafe's escape is a C function, the second because reaching the previous
item compares against a sentinel. Neither is a thing a Go program does, and
neither changes what the template did wrong. The error kind is `RecursionError`
throughout, and the configured limit is on the error's `Limit` field.

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
both can be turned off by passing a *negative* value, or with `WithoutLimits()`,
when the templates are trusted and a deadline is doing the job instead. Zero is
not off: zero restores the default, for all three limit options, so a config
struct deserialised from YAML or flags with fields nobody set is the safe one
rather than the unbounded one. (The three used to disagree about that, which is
what `WithoutLimits` exists to make legible.)

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
