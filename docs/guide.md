# Using gojja2

Everything a caller configures, under a heading you can link to. The README has
the thirty-second version; this is the page you come back to when you need one
specific answer.

If you are adding a filter or exposing your own Go type, that is
[extending.md](extending.md). If a render failed with a bound, that is
[limits.md](limits.md).

## Loading templates

```go
env, err := gojja2.New(gojja2.WithLoader(gojja2.FSLoader{FS: os.DirFS("templates")}))
if err != nil {
    return err // a configuration New cannot honour: see below
}
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
- an **embedded struct** promotes, as it does in Go: `{{ user.ID }}` reaches
  the embedded base's field, a shallower field of the same name shadows a
  deeper one, and two of one name at one depth promote neither. The embedded
  field stays reachable by its own name (`{{ user.Base.ID }}`). For *listing* —
  `|items`, `{% for %}`, `|tojson` — the rule is `encoding/json`'s instead: an
  embedded struct serialises flat, so `{{ user|tojson }}` is what
  `json.Marshal` gives. A `json` tag on an embedded field names it and stops
  the promotion, exactly as it does there.

  One corner differs from `encoding/json`: a tag on an embedded field whose
  *type* is unexported is ignored, and the fields promote as if it were not
  there. Naming the field means reading its value, and reflection will not hand
  over an unexported field's without unsafe access;
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
gojja2.New(gojja2.WithAutoescape(true))                  // always
gojja2.New(gojja2.WithAutoescapeExtensions(".html"))     // by name
```

Prefer `WithAutoescapeExtensions`. Extensions are matched as a *suffix*, so an
argument that is not an extension never matches -- and never matching means
never escaping. `SelectAutoescape("*.html")`, which is how you would write it
thinking of a glob, leaves every `.html` template unescaped and says nothing;
jinja2 does the same.

`WithAutoescapeExtensions` refuses that, because this is the one setting whose
failure mode is cross-site scripting. How much it refuses is a knob:

```go
// the default: refuses a glob, a path, an empty string, whitespace
gojja2.WithAutoescapeExtensions("*.html")   // error

// jinja2's behaviour exactly: taken literally, matches nothing, escapes nothing
gojja2.WithAutoescapeSelection(gojja2.SelectAutoescapeConfig{
    Enabled:  []string{"*.html"},
    Leniency: gojja2.AcceptAnyExtension,
})
```

The zero value of `Leniency` is `RefuseImpossibleExtensions`, so a caller who
says nothing gets the checking. `Disabled` entries are checked the same way --
one that cannot match errs toward escaping *more*, which is safe, but it is
still not the configuration you wrote down.

`SelectAutoescape` and `SelectAutoescapeWith` are unchanged and always lenient:
they return an `AutoescapeFunc` and have nowhere to report a refusal, so the
checking lives in the option, which has an error to return.

A typo that is still a plausible extension -- `hmtl` -- cannot be told from a
suffix somebody really uses, and is accepted at either setting.

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

## What New refuses

`New` returns an error rather than accepting a configuration it cannot honour.
There is no environment to use when it does -- it returns `nil` and the error.

| refused | because |
|---|---|
| an unknown name in `WithExtensions` | a typo used to be ignored, so the feature you asked for was simply off, and the template said so later by failing on a tag that should have existed |
| `WithNewlineSequence` outside `"\n"`, `"\r\n"`, `"\r"` | jinja2 asserts the same three; anything else rendered here and raised there |
| two of the block, variable and comment *opening* strings being equal | a template cannot be read two ways, and guessing which is worse than saying so. `WithDelimiterLeniency` turns this down; see below |
| a negative `TruncateLeeway` | `truncate` refuses it when it runs, so the environment built and then failed on every render that reached the filter |
| a `PythonVersion` that is not one of the four | gojja2 reproduces CPython 3.11 to 3.14; anything else has no answers to give, and taking the default silently would render something nobody asked for |
| an `UnsupportedLeniency` that is neither value | likewise |

Two things that look like they should be refused and are not. An **empty**
delimiter means "leave this one alone", which is how one can be overridden
without restating the rest. And a **line-statement or line-comment prefix may
equal a delimiter** -- `WithLineStatementPrefix("%")` with `%` as the block
opening is a configuration jinja2 accepts and renders, and so does this.

The delimiter check is stricter than jinja2's in one place, and that is a knob.
jinja2 writes its assertion as `a != b != c`, a chained comparison, so it never
compares the block opening against the comment one and accepts them being
equal:

```go
// the default: all three pairs compared
gojja2.New(gojja2.WithCommentDelimiters("{%", "%}"))   // error, block is {% too

// jinja2's check exactly: block vs variable, variable vs comment, and no more
gojja2.New(
    gojja2.WithCommentDelimiters("{%", "%}"),
    gojja2.WithDelimiterLeniency(gojja2.MatchJinja2Delimiters),
)
```

Under `MatchJinja2Delimiters` such a template renders exactly as CPython
renders it -- which of the two tags a `{%` opens is then settled by the lexer's
ordering rather than by anything the template says. The two pairs jinja2 *does*
compare are refused at either setting.

## Which CPython

jinja2 3.1.6 is one library, but it runs on an interpreter, and the interpreter
decides some of what a template does. `{{ d[0:1] }}` raises `TypeError` on
CPython 3.11 and `KeyError` on 3.12. `{{ xs|sort(reverse=none) }}` is an error
on 3.11 and a forward sort on 3.12. A division by zero is worded six ways before
3.14 and one way after. And CPython carries its own Unicode, so which characters
are digits, how they case, and how `repr` escapes them all move too.

Across 3.11 to 3.14 that is **66 of gojja2's 2,241 committed conformance cases**
-- 2.9%. Most answer exactly two ways; a few, such as `{{ 1.0 % 0 }}`, answer
three.

A render reproduces the pinned interpreter, 3.13, by default. Choose another to
match a service already running on it:

```go
env, err := gojja2.New(gojja2.WithPythonVersion(gojja2.Python311))
```

`Environment.PythonVersion` reports what an environment settled on. The
constants are `Python311` through `Python314`, and `DefaultPythonVersion` is the
pinned one -- the interpreter every committed table and golden was generated
from, which the Makefile's `PYTHON_VERSION` sets and a test holds the two to.

What it changes is a closed list -- sixteen behaviour and wording rules, plus
the Unicode tables -- and every rule is named in `value/pyversion.go` with the
release that moved it and the conformance case that grades it. Everything
outside that list is identical on every interpreter, which is 98.3% of the
corpus. [conformance.md](conformance.md#which-cpython) has the measurements and
how each version is graded.

This is *not* a compatibility shim for older jinja2: the library is pinned at
3.1.6 throughout. It is only about which Python that library is running on.

## Constructs gojja2 cannot honour

Three of CPython's codec error handlers have no answer here -- `namereplace`
needs the Unicode name database, and `surrogateescape` and `surrogatepass`
answer with a lone surrogate, which a Go string cannot hold. They are listed
with their reasons in [divergences.md](divergences.md#which-codecs-and-error-handlers-encode-and-decode-know).

The awkward part is *when* a template finds out. An error handler is looked up
only when a character actually needs it -- CPython works that way too, and
gojja2 matches -- so a template naming one compiles, renders, passes its tests,
and raises on the first input that reaches the handler.
`.encode("ascii", "namereplace")` is fine for every ASCII string and fails on
the first accented letter.

So compiling a template looks for them, and what it finds is on
`Template.Unsupported()` whether or not anyone asked:

```go
tmpl, err := env.GetTemplate("page.html")
for _, u := range tmpl.Unsupported() {
    log.Printf("%s:%d: %s", u.Template, u.Line, u.Error())
}
```

`WithUnsupportedReport` routes each finding somewhere as it is found -- gojja2
has no logger of its own and writes to no stream -- and
`WithUnsupportedLeniency(RefuseUnsupported)` makes it a compile error instead,
so the template cannot reach production carrying a failure only some inputs
show:

```go
env, err := gojja2.New(
    gojja2.WithUnsupportedReport(func(u gojja2.Unsupported) {
        log.Printf("%s:%d: %s", u.Template, u.Line, u.Error())
    }),
    gojja2.WithUnsupportedLeniency(gojja2.RefuseUnsupported),
)
```

Reporting is the default, and unlike the other leniency knobs the lenient value
is the zero one: those describe a configuration nobody has written yet, this
describes templates that already exist, and refusing by default would reject one
that works today because its data has never reached the handler.

The check reads the handler wherever a template writes one -- positionally, as
`errors=`, or assembled from constants. A handler that is genuinely dynamic,
`s.encode("ascii", h)`, cannot be judged before the render and is not guessed
at; that one still surfaces as the `LookupError` it always did.

## Asking questions about a template

`Template.Syntax` returns the template's structure in the vocabulary of the
[`syntax`](https://pkg.go.dev/github.com/mgilbir/gojja2/syntax) package: one node
type, a closed set of kinds, and children reached through **labelled edges**.

```go
tmpl, _ := env.FromString(`{% if admin %}{{ name }}{% endif %}`)

syntax.Walk(tmpl.Syntax(), func(n *syntax.Node, role syntax.Role) bool {
    if role == syntax.RoleTest && n.Kind == syntax.KindName {
        fmt.Println(n.Attr("name"), "decides something")
    }
    return true
})
```

The labels are what make a query short. `RoleTest` is a condition wherever it
appears, so asking "which names decide something" needs no knowledge of the node
set — where a positional tree would mean knowing that a condition is the first
field of an `if`, the third of a `for` and the test of a conditional expression.

It is the template **as written**, not as compiled: the constant folder has run
over the engine's own tree, and whether `{{ xs[[]] }}` is a constant subscript is
a fact about the optimizer rather than about the template.

### What the names mean

The tree comes with `Info`, which says who binds each name, what each read
resolves to, and which nodes introduce a scope:

```go
tree := tmpl.Syntax()
for name, sym := range tree.Info.Context {
    if sym.Kind == syntax.SymContext {
        fmt.Println(name, "must be supplied by the caller")
    }
}
```

These are kept beside the tree rather than on its nodes, the way `go/types`
keeps them beside `go/ast`. Some of them are not properties of a node at all —
which scope owns a name is a property of a (scope, name) pair — and keeping the
tree free of them keeps it immutable, which matters because one compiled
template is walked from many goroutines.

They are also the part a caller could not work out for themselves. jinja2
decides per frame, on a name's **first mention**, whether it belongs to the frame
or resolves from the caller: `{{ x }}{% set x = 1 %}` reads your `x`, and
`{% for i in [1] %}{{ x }}{% endfor %}{% set x = 1 %}` does not, because the root
frame claimed `x` before the loop ran. `Info` is graded against jinja2's own
`idtracking` for every committed case, so it is that rule rather than a second
reading of it.

The vocabulary is deliberately neither gojja2's internal tree nor jinja2's.
Matching jinja2's node classes would be a promise to reproduce a data structure
rather than a behaviour. What is promised instead is stronger and more useful:
gojja2's tree and jinja2's own parse tree encode to **byte-identical** canonical
form for every committed conformance case, so a query written against this
reaches the conclusion jinja2 would have reached. See
[docs/contributing.md](contributing.md) for how that is checked.

## What a template does with your variables

The [`dataflow`](https://pkg.go.dev/github.com/mgilbir/gojja2/dataflow) package
is a worked query over that tree. It answers, for each variable, whether its
**value can reach the output**, whether it only **steers** what is rendered, or
neither:

```go
tree := tmpl.Syntax()
for name, e := range dataflow.Analyze(tree).Context(tree) {
    fmt.Println(name, e&dataflow.Printed != 0, e&dataflow.Steers != 0)
}
```

The two are independent and a variable can be neither: `{% set unused = x %}`
with nothing reading `unused` means `x` cannot change the output at all. That
negative is the useful part, and it is why this is a dataflow analysis rather
than a scan for names — `{% set y = x %}{{ y }}` prints `x` without ever naming
it at an output position, and `{{ "yes" if flag else "no" }}` prints neither
operand while `flag` decides which.

`Opaque` means the answer has no reliable negative: a computed lookup like
`{{ data[key] }}` is a route the analysis does not follow. Nothing is ever
reported as unable to reach the output when it might.

### Namespaces

`{% set %}` inside a loop does not escape it, which is why `namespace()` exists
and why it is the shape worth following — accumulate then print is the case most
worth tracing:

```jinja
{% set ns = namespace(total=0) %}
{% for row in rows %}{% set ns.total = ns.total + row %}{% endfor %}
{{ ns.total }}
```

Each field is followed separately, so `rows` comes back as printed rather than
"might be", and `{% set ns = namespace(a=p, b=q) %}{{ ns.a }}` says `p` is
printed and `q` cannot affect the output at all.

That holds only while the namespace itself is never handed anywhere.
`{% set other = ns %}`, or passing it to a macro or a filter, makes two names for
one object, and following that is alias analysis — getting it subtly wrong would
mean reporting a real negative that is not true. So a namespace read anywhere but
as the subject of a field access collapses back to `Opaque`. The test is crude
and deliberately in the safe direction.

### Following `{% include %}` and friends

Give it a resolver and it reads the templates a template pulls in, which is how
a variable that only the *other* template mentions gets reported at all:

```go
flow := dataflow.Analyze(tree, dataflow.WithResolver(func(name string) *syntax.Tree {
    other, err := env.GetTemplate(name)
    if err != nil {
        return nil
    }
    return other.Syntax()
}))
```

The defaults differ and the difference is worth knowing: `{% include %}` hands
over the context, and `{% import %}` and `{% from … import %}` do not. An import
without context cannot see your variables at all, so it cannot print them —
a real negative rather than a shrug. A name that is not a constant
(`{% include page %}`) stays opaque, because which template runs is not a static
fact; `page` is reported as steering the output, since two names render two
documents.

Without a resolver, a reference to another template makes everything opaque. A
cycle terminates rather than recurring.

`Flow.Derives` is the dependency graph the verdict was propagated over, exposed
because the interesting question is not always "can this be printed" — "what
would change if I stopped passing x" and "which of these bindings is dead" are
the same graph asked differently.

**It reads only the public tree.** Everything in the package works from
`syntax.Tree` and its `Info`, which is the demonstration that the exposed tree is
enough to reason with: an answer it can reach is an answer you can reach. It is
graded against an independent implementation over jinja2's own AST for every
committed case.

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
