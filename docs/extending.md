# Writing a filter, a test, a global or an object

Four extension points, one contract. The contract is the reason this page
exists: gojja2 renders templates its caller may not have written, so anything
you add has to hold the same line the built-ins do — charge before you allocate,
and yield so a cancelled render can stop.

The two full definitions below — `bannerFilter` and `tempC` — are copied
verbatim from `example_test.go`, which `go test` runs and
`TestExtendingDocExamplesAreReal` pins, so neither can drift from the API
without the build noticing.

## The four shapes

```go
env := gojja2.New()

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
build. Both check the render's budget *and* a hard ceiling of 2\*\*31, because a
zero budget means unbounded and unbounded must not mean 2\*\*63.

Four rules, each of which has been broken in this codebase at least once:

1. **Charge first.** After the allocation, the memory is already gone.
2. **Charge the total before splitting it.** The hard ceiling is applied per
   call, so two charges do not compose: `ChargeBytes(2<<30)` twice passes it
   both times while the sum does not. The *budget* would catch that, but only
   if the caller set one — under `WithoutLimits()` the ceiling is all there is.
3. **Saturate, never clamp.** Clamping an enormous count down to the ceiling
   turns "refuse" into "allocate the maximum".
4. **Handle a nil `State`.** Constant folding calls filters with no render
   behind them. `ChargeBytes`, `ChargeItems` and `Step` all cope, and still apply
   the hard ceiling — folding allocates just as much as rendering does.

### 2. Yield, or be uninterruptible

The render reads its context in three places: a loop iteration, an output write,
and `State.Poll`. A filter that loops without writing output touches none of
them, so it is a region nothing can interrupt. One did: it overran a one-second
deadline by seventeen seconds, and the error it finally returned was the output
bound rather than the deadline.

- `s.Step(n)` — charges `n` units of work *and* checks the context. Use it in a
  loop over a sequence whose length the caller controls.
- `s.Poll()` — checks the context without charging. Use it for sustained work
  that neither iterates nor allocates.

Either, every few thousand units, is cheap.

```go
for i := int64(0); i < steps; i++ {
	// Charge the work, and let a cancelled render stop here.
	if err := s.Step(1); err != nil {
		return value.Undefined, err
	}
	total++
}
```

A render whose context is already cancelled stops at the first such call, with
`render stopped: context canceled`.

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

## Exposing a Go type directly

Reflection handles ordinary structs, slices and maps — see the README. When you
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
| `Slicer` | `Slice(start, stop, step)` | `obj[a:b:c]`, answered by the object |
| `Container` | `Contains(item) (found, known bool)` | `x in obj` by arithmetic rather than a scan |
| `Equaler` | `Equals(other) (equal, known bool)` | `==` by value rather than identity |
| `Booler` | `IsTrue() bool` | truthiness — Python's `__bool__` |
| `Strer` | `Str() string` | `{{ obj }}` — Python's `__str__` |
| `Reprer` | `Repr() string` | `{{ [obj] }}` — Python's `__repr__`, used inside containers |
| `HTMLer` | `HTML() string` | the escaped form — Python's `__html__` |
| `TupleView` | `AsTuple() Value` | treatment as the tuple it stands for |
| `BigLener` | `BigLen() *big.Int` | a length that exceeds an `int`, as `range` needs |

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

`Container` and `BigLener` are worth knowing about even if you never implement
them, because they are why `{{ 5 in range(10000000000) }}` is a division rather
than a walk of ten billion elements, and why `len(range(-2**63, 2**63-1))` can
report a number no Go slice could index.

An `Object` reaches a render through `RenderValues`, which takes values that are
already template values and skips the conversion from Go:

```go
var out strings.Builder
err := tmpl.RenderValues(ctx, &out, map[string]value.Value{
	"t": value.FromObject(tempC(21.5)),
})
```

## What the built-ins do

Read these before writing your own:

- **`alloc.go`** first. It is the charging API, and its header comment is the
  case for it: "`round` formatted a two-billion-digit decimal, `tojson` built a
  two-gigabyte indent, `slice` allocated a hundred million lists, and each did
  it with the output budget set to four kilobytes." `repeatStringN` and
  `saturatingMulInt` are there too, and the comment on the latter explains why
  a clamp is the wrong shape.
- **`filters_seq.go`** for walking a sequence: `Step`, `ChargeItems` and the
  `materialize` pattern.
- **`methods.go`** for building output: the largest collection of `ChargeBytes`
  call sites, including the ones that charge inside a loop.
- **`globals.go`**'s `rangeObject` for the object interfaces — it implements
  `GetAttr`, `GetIndex`, `Len`, `BigLen`, `Slice`, `Contains`, `Equals` and
  `Repr`, which is most of the table above in one type.

## Related

- [limits.md](limits.md) — what the budget bounds, and the options that change it
- [architecture.md](architecture.md) — where charging sits in the render, drawn
- `go doc State`, `go doc value.Object`, `go doc value.CallArgs`
