# Writing a filter, a test, a global or an object

Four extension points, one contract. The contract is the reason this page
exists: gojja2 renders templates its caller may not have written, so anything
you add has to hold the same line the built-ins do — charge before you allocate,
and yield so a cancelled render can stop.

The three full definitions below — `bannerFilter`, `tempC` and
`missingIsAnError` — are copied verbatim from `example_test.go`, which `go test`
runs and `TestExtendingDocExamplesAreReal` pins, so none can drift from the API
without the build noticing.

## The four shapes

```go
env, err := gojja2.New()

env.AddFilter("name", func(s *gojja2.State, v value.Value, args *value.CallArgs) (value.Value, error))
env.AddTest("name",   func(s *gojja2.State, v value.Value, args *value.CallArgs) (bool, error))
env.AddGlobal("name", value.Value)                       // any value
env.AddGlobal("name", gojja2.Func("name", func(s *gojja2.State, args *value.CallArgs) (value.Value, error)))
```

An `Environment` is safe for concurrent use **once configured**. Register
everything before the first render; registering while templates are in flight is
not safe.

Replacing one of jinja2's own filters also gives up the argument checking that
came with its signature. What the replacement accepts becomes the replacement's
business.

What a filter, a `Func` or a `Class` you registered returns -- and a global you
registered -- may be something you keep: a cached list, a shared config. A
template could change it in place, so a render copies it the first time it
reaches it and works on the copy; the next render, and every other tenant, sees
yours unchanged. You do not need to copy defensively, and returning the same
value on every call costs one copy per render, not one per call.

What the engine copies is *values*. It cannot see into your code, so the code
you register -- a filter, a test, a `Func`, a `value.Object` -- runs as you
wrote it, and whether it keeps state from one render to the next is yours to
decide: a filter that counts its calls counts across renders and tenants. Two
cases the engine does guard:

- A Go method declared on a **pointer receiver** can change the object you
  passed in, so by default a template calling one is refused with an error
  wrapping `gojja2.ErrSharedState`; `WithSharedState(AllowPointerMethods)`
  allows it. A method declared on the value receives a copy of the struct and
  is callable as usual -- but it can still write through a map, a slice or a
  pointer the struct holds, which the engine cannot see.
- A `value.Object` a template could change in place is copied per render only
  if it implements `value.Isolator`; see
  [Exposing a Go type directly](#exposing-a-go-type-directly).

`Filters()`, `Tests()` and `Globals()` read the registries back. Each returns a
copy, so writing to it changes nothing; the `Add` methods are how one is changed.

## The contract

### 1. Charge before you allocate

If the size of what you are about to allocate comes from the template — a width,
a count, a repeat, an indent — reserve it first:

```go
func bannerFilter(s *gojja2.State, v value.Value, args *value.CallArgs) (value.Value, error) {
	width := int64(40)
	if w, ok := args.Arg(0); ok {
		n, ok := w.Int64()
		if !ok {
			return value.Undefined, errs.New(errs.TypeError,
				"banner() width must be an integer, not %s", w.TypeName())
		}
		width = n
	}
	// Charge first. Charging afterwards charges for memory already gone.
	if err := s.ChargeBytes(width); err != nil {
		return value.Undefined, err
	}
	return value.String(strings.Repeat("=", int(width))), nil
}
```

`ChargeBytes` for output, `ChargeItems` for elements of a slice you are about to
build. Both check the render's budget *and* a hard ceiling of 2\*\*31−1 bytes or
elements, because a caller can remove the budget (`WithoutLimits()`) and an
unbounded budget must still not mean 2\*\*63. Past the ceiling the error is an
`OverflowError`; past the budget it is a `TemplateRuntimeError` wrapping
`ErrOutputTooLarge` or `ErrTooManyIterations` -- return either unchanged.

Four rules, each of which has been broken in this codebase at least once:

1. **Charge first.** After the allocation, the memory is already gone.
2. **Charge the total before splitting it.** The hard ceiling is applied per
   call, so two charges do not compose: `ChargeBytes(1<<30)` twice passes it
   both times while the sum does not. The *budget* would catch that, but only
   if the caller set one — under `WithoutLimits()` the ceiling is all there is.
3. **Saturate, never clamp.** Clamping an enormous count down to the ceiling
   turns "refuse" into "allocate the maximum".
4. **Charge at compile time too.** A filter or test may be run while the
   template compiles ([5 below](#5-you-may-be-run-at-compile-time)), with a
   `State` that has a small budget of its own. Charge exactly as you would in
   a render — folding allocates just as much as rendering does.

### 2. Yield, or be uninterruptible

The render reads its context through its budget, and only every 4,096 units of
work: a loop pass, an item charged with `Step` or `ChargeItems`, a byte written
or charged with `ChargeBytes`, a call to `State.Poll`. Reading it on every unit
would put a synchronisation in the inner loop of every template. A filter that
loops without charging anything advances none of those counters, so it is a
region nothing can interrupt. One did: it overran a one-second deadline by
seventeen seconds, and the error it finally returned was the output bound
rather than the deadline.

- `s.Step(n)` — charges `n` units of work and advances the count by `n`. Use it
  in a loop over a sequence whose length the caller controls.
- `s.Poll()` — advances the count by **one** without charging the budget. Call
  it once per unit of sustained work that neither iterates nor allocates — per
  item, per pass — not once per chunk: one call per thousand-unit chunk reads
  the context once every four million units.
- `s.PollWork(n)` — `Poll` after `n` units done uncharged, for work counted in
  batches.

```go
for i := int64(0); i < steps; i++ {
	// Charge the work, and let a cancelled render stop here.
	if err := s.Step(1); err != nil {
		return value.Undefined, err
	}
	total++
}
```

A render whose context is cancelled stops at the first read after that, within
4,096 units, with `render stopped: context canceled` (a `TemplateRuntimeError`
wrapping `ctx.Err()`). A render shorter than that finishes and succeeds, so a
caller that needs to fail fast checks `ctx.Err()` before rendering. See
[limits.md](limits.md#how-promptly-a-cancelled-render-stops).

### 3. Fail the way jinja2 fails

Return an `*errs.Error` carrying the exception class CPython would have raised,
so `errors.Is(err, errs.TypeError)` works for the caller and the conformance
suite can grade it:

```go
return value.Undefined, errs.New(errs.TypeError,
	"banner() width must be an integer, not %s", w.TypeName())
```

`errs.Kind` mirrors the CPython class hierarchy, so `errors.Is(err,
errs.LookupError)` catches a `KeyError` exactly as `except LookupError` would.

### 4. Respect autoescape

`s.Autoescape()` reports whether the render is currently escaping. A filter that
produces markup the caller should *not* escape returns `value.Safe(s)` rather
than `value.String(s)` — that is what `|safe` produces and what markupsafe calls
`Markup`. Returning `Safe` for text derived from template input is how an
escaping project starts emitting raw user input, so do it only for markup you
built yourself.

### 5. You may be run at compile time

A filter or test you registered, applied where every operand is a constant --
`{{ 'hello'|t }}`, `{{ [1, 2]|myfilter(4) }}`, `{% if 3 is mytest %}` -- is called
**once, when the template compiles**, as jinja2's optimizer does, and its result
is baked into the compiled template and reused by every render of it. (A call to
a global, `Func` or `Class`, is never folded.) The call you get then:

- has a non-nil `State` with no render behind it. `Resolve` sees the
  environment's globals and no render variables; `Context()` is
  `context.Background()`; `Autoescape()` is the escaping in force where the
  expression is written.
- has a budget of its own, whatever the environment's limits: 100,000 units of
  work and 64 KiB of output, reset for each fold attempt, plus the
  environment's integer-width ceiling. A charge past it is refused as in a
  render.
- may be abandoned. If you return an error -- a refused charge included -- the
  fold is quietly dropped and the expression is evaluated at render time
  instead, calling you again. So may a result over the 64 KiB a folded
  constant is allowed. You can therefore be called more than once for one
  expression.

So be **pure in your arguments**: the same arguments must give the same answer
in every render. A counter, a clock, a feature flag, or a translation that
reads `s.Resolve("lang")` gives the compile-time answer to every render, for
every user. jinja2 never folds a `@pass_context` filter; gojja2 has no such
marker, so a filter that needs the render must be handed something from it as
an argument -- `{{ 'hello'|t(lang) }}` is not constant, so it is not folded.

## Reading arguments

`*value.CallArgs` is the call site:

```go
v, ok := args.Arg(0)          // positional, by index
v, ok := args.Kwarg("width")  // keyword, by name
args.Pos                      // []Value
args.Kwargs                   // []Kwarg, in source order
```

Source order is kept for the keywords because some callables care: `dict(b=1,
a=2)` renders its keys in the order they were written.

### Or let `FilterFunc` read them

For a filter or test that is an ordinary Go function, `FilterFunc` and
`TestFunc` do the reading. Name the parameters after the value and a template
can pass them by keyword; a call of the wrong shape is refused before your
function runs, with the message CPython gives for a Python filter of the same
shape:

```
env.AddFilter("truncate_words",
	gojja2.FilterFunc("truncate_words", truncateWords, "count", "suffix"))

{{ text|truncate_words(3, suffix="...") }}
{{ text|truncate_words(3) }}   →  TypeError: truncate_words() missing 1 required positional argument: 'suffix'
```

Arguments convert to the parameter types as a Go method's do, a number to a
narrower type only when nothing is lost; a `value.Value` parameter takes the
template's value as it is. A Go function has no defaults, so every named
parameter is required, and a final `...T` collects extra positional arguments.
The rules above still apply inside the function: take a leading `*gojja2.State`
to charge, poll or check autoescaping.

## Exposing a Go type directly

Reflection handles ordinary structs, slices and maps — see
[guide.md](guide.md#go-values-in-the-context). When you
want the type to decide for itself how it looks to a template, implement
`value.Object` and pass it with `value.FromObject`.

Only `GetAttr` is required. Everything else is opt-in, one interface at a time,
the way Python adds one dunder at a time:

| interface | method | gives the template |
|---|---|---|
| `Object` | `GetAttr(name) (Value, bool)` | `obj.name` — **required** |
| `Mapping` | `GetItem`, `Keys`, `Len` | `obj[key]`, key iteration, `dictsort`, `items` |
| `Sequence` | `Len`, `GetIndex(i)` | `obj[i]` by integer index |
| `Iterable` | `Iterate() iter.Seq[Value]` | `{% for %}`, with no length and no indexing — a generator |
| `Sized` | `Len()` | `len(obj)` without indexing — Python's `__len__` alone |
| `SizedErr`, `IterableErr` | `LenErr()`, `IterateErr()` | `Sized` and `Iterable` for a length or an iteration that can raise; asked first |
| `Slicer` | `Slice(start, stop, step *int)` | `obj[a:b:c]`, answered by the object |
| `BigSlicer` | `BigSlice(start, stop, step *big.Int)` | the same with bounds past an `int`; must agree with `Slicer` |
| `Container` | `Contains(item) (found, known bool)` | `x in obj` by arithmetic rather than a scan |
| `Equaler` | `Equals(other) (equal, known bool)` | `==` by value rather than identity |
| `EqualerErr` | `EqualsErr(other, py) (equal, known bool, err error)` | `Equaler` for a comparison that can raise; asked first |
| `OrderDelegate` | `OrderDelegate() Value` | `<`, `>` and the rest answered as another value's |
| `Booler` | `IsTrue() bool` | truthiness — Python's `__bool__` |
| `Strer` | `Str() string` | `{{ obj }}` — Python's `__str__` |
| `Reprer` | `Repr() string` | `{{ [obj] }}` — Python's `__repr__`, used inside containers |
| `HTMLer` | `HTML() string` | the escaped form — Python's `__html__` |
| `TupleView` | `AsTuple() Value` | treatment as the tuple it stands for |
| `BigLener` | `BigLen() *big.Int` | a length that exceeds an `int`, as `range` needs |
| `BigSequence` | `Sequence`, `BigLener`, `BigIndex(*big.Int)` | `obj[i]` with an index past an `int` |
| `Caller` | `Call(args *CallArgs) (Value, error)` | `obj(...)` |
| `Isolator` | `IsolateWith(isolate) (Value, error)` | a copy per render — see below |

Returning `false` from `GetAttr` means "no such attribute", which the runtime
turns into Undefined rather than an error — jinja2's `getattr` fallback, so
`{{ obj.missing|default("n/a") }}` works.

```go
type tempC float64

func (t tempC) GetAttr(name string) (value.Value, bool) {
	switch name {
	case "celsius":
		return value.Float(float64(t)), true
	case "fahrenheit":
		return value.Float(float64(t)*9/5 + 32), true
	}
	return value.Undefined, false
}

// Str is what `{{ t }}` prints; Repr is what it prints inside a container.
func (t tempC) Str() string  { return fmt.Sprintf("%.1f°C", float64(t)) }
func (t tempC) Repr() string { return fmt.Sprintf("tempC(%g)", float64(t)) }

// HTML is Python's __html__: autoescape asks for this instead of escaping Str.
func (t tempC) HTML() string { return fmt.Sprintf("<b>%.1f&deg;C</b>", float64(t)) }
```

The arbitrary-precision interfaces are worth knowing about even if you never
implement them, because they are why `range` behaves as CPython's does at sizes
no Go slice could hold: `{{ 5 in range(10000000000) }}` is a division rather
than a walk of ten billion elements (a range answers membership through a
`ContainsErr` method, `Container`'s sibling that can also refuse),
`{{ range(10000000000)|last }}` answers at once, and
`{{ range(-2**63, 2**63-1)|length }}` raises CPython's own `OverflowError`
rather than reporting a saturated length.

**An `Object` the template can change must implement `value.Isolator`.** A
render copies what reaches it from outside -- a list, a dict, a namespace --
so one render's changes cannot reach the next, but it can only copy an
`Object` that says how: `IsolateWith` returns the copy, using the function it
is handed to copy what the object holds. An `Object` that does not implement it
is handed to every render as it is, so if `GetAttr` or a method changes it, that
change is shared by every render and every tenant given the same value. An
immutable `Object`, like `tempC`, needs nothing.

An `Object` reaches a render like any other value, wrapped with
`value.FromObject`:

```go
var out strings.Builder
err := tmpl.Render(ctx, &out, map[string]any{
	"t": value.FromObject(tempC(21.5)),
})
```

`RenderValues` takes a `map[string]value.Value` instead, for values that are
already template values; by default it copies what a template could change
exactly as `Render` does. Under `WithSharedState(ShareRenderValues)` it hands
the values over as they are, and a template's `{% do xs.append(9) %}` then
reaches your value and every later render given it, as in jinja2.

## A finalize that can fail

`WithFinalizeFunc` is the fifth shape: a hook every printed value passes through,
with the render's `State` and an error to return. It is held to the same
contract -- it runs once per print, so anything sized by the value it is handed
should be charged with `State.ChargeBytes` first, as a filter's would. This one
prints `None` as nothing and refuses a value the template never set:

```go
func missingIsAnError(s *gojja2.State, v value.Value) (value.Value, error) {
	if v.IsUndefined() {
		return value.Undefined, fmt.Errorf("%s printed a value nobody set", s.Name())
	}
	if v.IsNone() {
		return value.String(""), nil
	}
	return v, nil
}
```

The error comes back from `Render` unchanged, so `errors.Is` and `errors.As`
find it. [guide.md](guide.md#finalizing-what-is-printed) has how it differs from
`WithFinalize`, which jinja2 also distinguishes.

## What the built-ins do

Read these before writing your own:

- **`alloc.go`** first. It is the charging API, and its header comment is the
  case for it: "`round` formatted a two-billion-digit decimal, `tojson` built a
  two-gigabyte indent, `slice` allocated a hundred million lists, and each did
  it with the output budget set to four kilobytes." `repeatStringN` and
  `saturatingMulInt` are there too, and the comment on the latter explains why
  a clamp is the wrong shape.
- **`filters_seq.go`** for walking a sequence with `Step` and `ChargeItems`,
  and **`filters.go`** for `materialize`, which those filters call to pull a
  sequence out charged item by item.
- **`methods.go`** and **`bytes_methods.go`** for building output: the string
  and bytes methods charge with `ChargeBytes` before they build.
- **`globals.go`**'s `rangeObject` for the object interfaces — it implements
  `GetAttr`, `Len`, `GetIndex`, `BigLen`, `BigIndex`, `BigSlice`,
  `ContainsErr`, `Equals` and `Repr`, which is much of the table above in one
  type.

## Related

- [limits.md](limits.md) — what the budget bounds, and the options that change it
- [architecture.md](architecture.md) — where charging sits in the render, drawn
- `go doc -all State`, `go doc value.Object`, `go doc value.CallArgs` (`-all` prints each method's doc, not just its signature)
