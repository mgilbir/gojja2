# Using gojja2

Everything a caller configures, under a heading you can link to. The README has
the thirty-second version; this is the page you come back to when you need one
specific answer.

If you are adding a filter or exposing your own Go type, that is
[extending.md](extending.md). If a render failed with a bound, that is
[limits.md](limits.md).

## Loading templates

```go
env := gojja2.New(gojja2.WithLoader(gojja2.FSLoader{FS: os.DirFS("templates")}))
tmpl, err := env.GetTemplate("page.html")
```

| loader | serves from |
|---|---|
| `FSLoader{FS: fs.FS, Root: string}` | any `fs.FS` — a directory, an `embed.FS`, a zip. `Root` is an optional prefix |
| `DictLoader{"name": "source"}` | an in-memory map; the easiest way to test inheritance |
| `PrefixLoader{Mapping: map[string]Loader, Delimiter: "/"}` | dispatches on a leading segment: `admin/index.html` is `index.html` in the loader under `admin` |
| `ChoiceLoader{a, b}` | each in turn, first hit wins |

A loader of your own implements `Load(name string) (string, error)`. **Report a
miss as anything `errors.Is(err, ErrNotFound)` matches** — the sentinel itself,
or an error wrapping it with `%w` — never as an empty template. Otherwise
`{% include ... ignore missing %}`, `ChoiceLoader`'s fallthrough and
`select_template`'s cannot tell a miss from a failure.

Any other error stops the search and reaches you unchanged, which is the point
of the distinction: a loader whose backing store is failing must not read as
"not here" and let the next loader quietly answer in its place.

`FSLoader` refuses a name with a `..` segment rather than cleaning it into
something else. A template that asks for a file outside its root gets
"not found", not a different file.

| entry point | what it does |
|---|---|
| `GetTemplate(name)` | loads, compiles, **caches** on success |
| `SelectTemplate(names)` | the first that exists — what `{% extends %}` does with a list |
| `FromString(source)` | compiles anonymously; autoescapes by default; **does not cache** |
| `FromNamedString(name, source)` | compiles under a name, which decides autoescaping and appears in errors |

Compiled templates live in a bounded LRU of 400, as jinja2's does.
`WithCacheSize` adjusts it; `ClearCache` and `ForgetTemplate(name)` pick up an
edit. There is no `auto_reload` — the `Loader` interface returns source and
nothing else, so there is no freshness to consult.

## Rendering

```go
err := tmpl.Render(ctx, w, map[string]any{"user": user})     // streams to w
s, err := tmpl.RenderString(ctx, map[string]any{...})        // returns the whole document
err := tmpl.RenderValues(ctx, w, map[string]value.Value{...}) // skips the Go conversion
```

`RenderString` returns the empty string when the render fails, not a partial
document. `Render` flushes what it produced before the failure, because leaving
it in the buffer would make the amount `w` receives depend on where the buffer
happened to be.

Output streams to `w` except where a construct captures its own body — see
[architecture.md](architecture.md#what-holds-output-before-writing-it) for the
six that do.

Every render takes a `context.Context` and stops when it is cancelled. See
[limits.md](limits.md) for where cancellation is noticed, and for the two
backstops behind it.

## Go values in the context

Reflection, with Python semantics on the other side:

- a **struct** exposes its exported fields, by name or by `json` tag, and its
  methods that take no arguments — value or pointer receiver;
- a **slice** becomes a list, a **map** becomes a dict;
- a value that refers to itself converts once and is shared, so it terminates
  rather than expanding forever, and `{{ n.self.self.k }}` resolves however deep
  it is followed. A self-referential *map* prints the way Python prints one,
  `{'k': 'v', 'self': {...}}`; a *struct* prints as `<pkg.Type object>`, which is
  what printing a struct gives whether or not it is cyclic.

A method is reached as Python reaches one: `{{ user.Name }}` is the bound
method and `{{ user.Name() }}` is what it returns. Printing the first gives
`<bound method Name>`, as it does in jinja2. A trailing `error` result fails the
render whatever else the method returns, including when it is the only result.

**A method that takes arguments is not exposed by default**, because calling one
lets the template choose what a host method is invoked with.
`WithMethodPolicy(value.AllMethods)` opts in, for templates as trusted as the Go
code they call into; a `value.MethodPolicy` of your own draws the line anywhere
between.

The real boundary is what you put in the context. If a template should not reach
something, hand it a narrower value — that is the control, not an allow-list
inside the engine. See [scope.md](scope.md) on why there is no sandbox.

To present a type on its own terms rather than by reflection, implement
`value.Object`: [extending.md](extending.md#exposing-a-go-type-directly).

## Errors

Every error carries the Python exception class jinja2 would have raised:

```go
if errors.Is(err, errs.UndefinedError) { ... }
if errors.Is(err, errs.LookupError)    { ... }  // catches KeyError and IndexError
```

`errs.Kind` mirrors CPython's class hierarchy, so `errors.Is` answers the
question `except` would. `err.Error()` is the bare message, matching Python's
`str(exc)`, so it can be compared against the oracle; location lives in the
fields, and `(*errs.Error).Detail()` renders class, message and
`template, line N` together for a human.

`ErrNotFound`, `ErrTooManyIterations`, `ErrOutputTooLarge` and `ErrInternal` are
the package-level sentinels.

## Missing values

`WithUndefined` picks which of jinja2's Undefined classes a failed lookup
returns. Rendering `[{{ nope }}]`, `[{{ nope.a.b }}]` and
`[{% if nope %}y{% endif %}]`:

| behaviour | jinja2 class | `{{ nope }}` | `{{ nope.a.b }}` | `{% if nope %}` |
|---|---|---|---|---|
| `UndefinedDefault` (zero value) | `Undefined` | `[]` | error | `[]` |
| `UndefinedChainable` | `ChainableUndefined` | `[]` | `[]` | `[]` |
| `UndefinedDebug` | `DebugUndefined` | `[{{ nope }}]` | error | `[]` |
| `UndefinedStrict` | `StrictUndefined` | error | error | error |

`UndefinedStrict` is the one to develop against: it turns a typo in a variable
name into a failure instead of a silently empty page.

## Autoescaping

```go
gojja2.New(gojja2.WithAutoescape(true))                               // always
gojja2.New(gojja2.WithAutoescapeFunc(gojja2.SelectAutoescape(".html"))) // by name
```

`SelectAutoescape` follows jinja2's `select_autoescape`: matching ignores case
on both sides, a leading dot is optional, and matching happens on a whole
extension rather than on any trailing substring. With no arguments it escapes
`html`, `htm`, `xml` and `xhtml` — jinja2's three plus one, which only ever
escapes *more*; see [divergences.md](divergences.md#the-default-autoescape-extension-set).

**A template compiled with `FromString` is escaped**, because it has no name to
decide by. Defaulting it to unescaped is how an escaped-by-configuration project
ends up emitting raw user input.

`SelectAutoescapeWith(SelectAutoescapeConfig{...})` takes the disabled-extension
list, the default, and `DisableForString`. Every field is written so the quiet
reading is the safe one: a caller has to say something explicit to escape less.

Inside a template, `|safe` marks a value as markup and `|e`/`|escape` forces
escaping. From Go, `value.Safe(s)` is the markup constructor.

## Syntax

| option | default | effect |
|---|---|---|
| `WithVariableDelimiters(a, b)` | `{{`, `}}` | `${ 1 + 1 }` with `("${", "}")` |
| `WithBlockDelimiters(a, b)` | `{%`, `%}` | |
| `WithCommentDelimiters(a, b)` | `{#`, `#}` | |
| `WithLineStatementPrefix(p)` | off | `% if 1` … `% endif` with `"%"` |
| `WithLineCommentPrefix(p)` | off | |
| `WithTrimBlocks(on)` | off | drops the first newline after a block tag |
| `WithLstripBlocks(on)` | off | strips whitespace before a block tag to the line start |
| `WithKeepTrailingNewline(on)` | off | jinja2 drops one trailing newline; this keeps it |
| `WithNewlineSequence(s)` | `"\n"` | what a newline is written as |
| `WithExtensions(names...)` | none | `"do"` enables `{% do %}`; `"loopcontrols"` enables `{% break %}` and `{% continue %}`. The `jinja2.ext.` prefix is accepted too |

`{% do %}` and `{% break %}`/`{% continue %}` are opt-in in jinja2 as well, and
both engines report the same `Encountered unknown tag 'do'.` without them.

`a\n{% if 1 %}\nb\n{% endif %}\nc` renders `a\n\nb\n\nc` by default and
`a\nb\nc` under `WithTrimBlocks(true)`.

## Filter policies

`WithPolicies` overrides the defaults jinja2 keeps in `Environment.policies`:
`URLizeRel` (jinja2 defaults it to `"noopener"`), `URLizeTarget`, and
`TruncateLeeway`.

## Concurrency

An `Environment` is safe for concurrent use **once configured**, and a compiled
`*Template` is safe to render from many goroutines at once. Registering filters,
tests or globals after templates are in flight is not.

A list written *in the template* is rebuilt per render, because the compiled
tree is shared by every render of that template. `concurrency_test.go` pins
this; the version without the rebuild fails there with one goroutine's values
appearing in another's output.

Two things are deliberately shared: a global registered on the `Environment`,
which lives there as it does in jinja2, and a host object exposed by pointer,
whose methods are the point. Everything else a render is handed is converted, so
a template cannot write to the caller's data —
[divergences.md](divergences.md#a-render-does-not-mutate-the-callers-data).
