# gojja2

A pure Go implementation of the [Jinja2](https://jinja.palletsprojects.com/)
template language, built to be behaviourally identical to CPython's `jinja2`.

## Using it

```go
env := gojja2.New(
    gojja2.WithLoader(gojja2.FSLoader{FS: os.DirFS("templates")}),
    gojja2.WithAutoescapeFunc(gojja2.SelectAutoescape(".html")),
)

tmpl, err := env.GetTemplate("page.html")
if err != nil {
    return err
}
return tmpl.Render(ctx, w, map[string]any{"user": user, "items": items})
```

Output is streamed to `w` as the template produces it, with one exception:
`{% include %}` renders the included template in full before writing it on, so
peak memory tracks the largest include rather than the write buffer.
`tmpl.RenderString(ctx, vars)` returns the whole document instead, and returns
nothing at all when the render fails.

Go values cross into templates by reflection: structs expose their exported
fields (by name or by `json` tag) and their methods that take no arguments,
whether the receiver is a value or a pointer; slices become lists, and maps
become dicts. Errors carry the Python exception class jinja2 would have raised,
so `errors.Is(err, errs.UndefinedError)` works.

A method is reached as Python reaches one, so `{{ user.Name }}` is the bound
method and `{{ user.Name() }}` is what it returns -- printing the first gives
`<bound method Name>`, as it does in jinja2. A trailing `error` result fails the
render whatever else the method returns, including when it is the only result.

A template name given to `FSLoader` is refused if any segment of it is `..`,
rather than being cleaned into something else: a template that asks for a file
outside its root gets "not found" and not a different file.

A method that *takes* arguments is not exposed by default, because calling one
means the template chooses what a host method is invoked with.
`WithMethodPolicy(value.AllMethods)` opts in, for templates as trusted as the
Go code they call into. A structure that refers to itself is fine to pass: it
converts once and is shared, so it renders the way Python renders one
(`{'k': 'v', 'self': {...}}`) rather than expanding forever.

`SelectAutoescape` follows jinja2's `select_autoescape`: matching ignores case
and a leading dot is optional, and a template compiled with `FromString` is
escaped -- it has no name to decide by, and defaulting it to *unescaped* is how
an escaped-by-configuration project ends up emitting raw user input.
`SelectAutoescapeWith` takes the disabled-extension and default settings as
well.

Every render takes a `context.Context` and stops when it is cancelled -- at the
next loop iteration, output write, or filter yield point, which is where the
context is read. Behind
it, a render is bounded by default to ten million loop iterations and 256 MiB
of output, and anything a template sizes from a number it chose -- a pad width,
an indent, a rounding precision -- is charged against that budget before it is
allocated. Zero means "the default" for every limit option; removing a bound
takes `WithoutLimits()`, so a configuration nobody filled in is the safe one.
See [docs/divergences.md](docs/divergences.md).

Compiled templates are cached in a bounded LRU of 400, as jinja2 does;
`WithCacheSize` adjusts it and `ClearCache` picks up an edited template.

## Ground truth

CPython's `jinja2` **is** the specification. Every behavioural question is
settled by rendering the template with the real thing and recording what it
produced:

```
$ .venv/bin/python tools/oracle/oracle.py --template '{% set d = {1:"a",} %}{{ d[1] }}'
{
  "ok": true,
  "output": "a"
}
```

Where gojja2 and CPython disagree, gojja2 is wrong. This extends past the
template language itself into Python's own semantics, because they are visible
in rendered output: arbitrary-precision integers, `repr()` of floats and
strings, code-point string indexing, insertion-ordered dicts, and dict keys
that hash `1`, `1.0` and `True` to the same slot.

That last one is not academic. A template like

```jinja
{% set d = {1:"a",} %}{{ d[1] }}
```

renders `a`, and an implementation that stringifies dict keys renders nothing
at all -- silently.

## Conformance

| corpus | gradable cases | matching CPython jinja2 |
|---|---|---|
| gojja2's own (committed, with goldens) | 513 | 512 |
| MiniJinja fixtures | 159 | 159 |
| Jinja's own test suite (harvested templates) | 658 | 656 |
| minja's syntax tests | 162 | 162 |
| llama.cpp's Jinja tests | 281 | 281 |
| LLM chat templates x 10 conversation shapes | 810 | 808 |
| A documentation theme's templates | 84 | 84 |
| Cookiecutter project templates | 166 | 166 |
| **total** | **2833** | **2828 (99.8%)** |

Each imported corpus is a different project's independent reading of the
language -- MiniJinja (Rust), minja (C++), llama.cpp's own engine, the
templates real models ship, a theme written to be used rather than tested, and
four project generators. Only their *inputs* are used: every expected output is
regenerated from the pinned CPython jinja2, because that is the specification. On top of that, roughly a million generated templates have been
rendered by both implementations and compared (see below).

The 5 that differ are listed, with reasons, in `testdata/known_failures.txt`;
a case on that list which starts passing fails the test, so the list can only
shrink deliberately. The table above is checked against the suite by
`TestConformance` whenever every corpus is present, so it cannot drift from
what is actually measured -- it had. Two are Jinja's own sandbox-escape tests, which walk a
Python object graph out to `__subclasses__` and `__import__`. `__class__` *is*
implemented; these two go past it. Two are DeepSeek-R1's chat
template, which writes `{{ tools|map(attribute='function')|tojson }}` -- jinja2's
`map` returns a generator, which `json.dumps` refuses, so the template raises
under CPython and renders under gojja2. The fifth is `{% if 1e400 %}`: jinja2
writes a folded constant into its generated Python as that constant's repr, and
`repr(float("inf"))` is the bare word `inf`, so the template raises a NameError
there and renders here. See [docs/divergences.md](docs/divergences.md) for all
three.

Four further cases are marked *ungradable* and left out of the table: they
render a generator's memory address, which differs between two runs of CPython
itself, so jinja2 does not match them either. Nothing else is excluded -- a case
gojja2 simply fails stays in the denominator.

Underneath, the pieces are graded separately against the real thing: CPython's
`repr()` over 3,200 floats and strings, every binary operator over a 39-value
pool (20,665 cases), jinja2's own token stream (113 cases) and its own parse
tree (100 cases).

The first corpus is committed with its goldens, so `go test ./...` grades
against CPython's answers on a fresh checkout with no network and no Python.
Run `make suites && make import` to add the rest: the upstream repositories are
cloned at pinned revisions into the gitignored `third_party/`, and the cases and
their goldens are built into the gitignored `testdata/generated/`. Nothing from
those projects is vendored or committed, and each generated corpus carries a
`SOURCES.md` recording where it came from, under what license, and which inputs
were dropped and why.

Cookiecutter templates are the one corpus that arrives with a context already
written: `cookiecutter.json` is one, in JSON, chosen by the template's author.
They contribute the shape of a template that generates a *file* -- 19 of the
166 wrap another templating language in `{% raw %}`, and 60 use whitespace
control -- which the chat templates and the theme between them do not reach.

The theme's templates arrive without any context at all -- a theme gets one from
MkDocs, not from a file next to it. Each context is synthesised by rendering
the template twice: once against proxies that record every access, and once
against the plain JSON that recording reads back as, requiring the two to agree
byte for byte. A template needing something JSON cannot express -- a host
filter, a callable -- is not guessed at; it is dropped, and `SOURCES.md` says
why. That dropped list is a deliverable in its own right: it is the catalogue
of what a JSON-context corpus structurally cannot reach.

`make import` also writes `testdata/generated/minijinja-divergences.md`, which
costs nothing and is worth having: MiniJinja ships a snapshot of what *it*
renders for each of its fixtures, and the CPython goldens for those same
fixtures are already recorded here. Of the 159 compared, 58 agree, 44 are
rejected by both with different wording, and 57 genuinely diverge -- MiniJinja
renders `range(3) * 3` and a case-insensitive `dictsort` where CPython raises,
among others. gojja2 matches CPython on every gradable one, which is the useful
part: those are the constructs two independent implementations read
differently, so they are where a third is most likely to be wrong.

Chat templates are not written against a bare environment -- `transformers`
gives them `trim_blocks`, `lstrip_blocks`, `loopcontrols`, a `tojson` that does
not sort keys or escape HTML, and the `raise_exception` and `strftime_now`
globals. A case records that as `"__profile__": "transformers"`, implemented
once for the oracle and once for gojja2, with `TestProfileMatchesOracle` pinning
the two together so they cannot drift apart unnoticed.

## Differential fuzzing

A corpus only covers what someone thought to write down. `make soak` generates
templates from the grammar, renders each with both implementations, and
requires them to agree on everything -- output, exception class, message and
line:

```
make soak N=200000      # seeded run, reproducible
make fuzz TIME=5m       # coverage-guided, via go test -fuzz
```

Generation is structured rather than byte-level: random bytes are read as
*grammar decisions*, so almost every case renders instead of being a syntax
error, and a mutation changes one choice rather than corrupting a tag. The
oracle runs as a warm subprocess -- about 5,000 templates a second rather than
ten -- under a memory cap and a per-render timeout, so a pathological case
degrades to an error instead of taking the machine down. A divergence is
shrunk against the same check before it is reported, so findings arrive
minimal.

This is where most of the subtler behaviour in this list came from: that
jinja2 wraps a sort key in a list (so two undefineds sort but do not compare),
that `{% include ... without context %}` bypasses an enclosing filter buffer,
and that a name assigned anywhere at template level is invisible to nested
scopes until the assignment runs.

## Scope

Jinja2's template language, not Python. Constructs that only exist because
jinja2 compiles to Python bytecode are out of scope. Constructs that merely
look Pythonic are in scope, including multi-line tags, tuple unpacking,
slicing, and trailing commas in every literal.

## Licence

Apache 2.0. See `LICENSE` and `NOTICE`.

gojja2 contains no code from Jinja or MiniJinja. Their test suites are used as
behavioural references and are downloaded on demand by `make suites` into the
gitignored `third_party/` directory -- never vendored.

## Development

```
make help       # all targets
make venv       # CPython + jinja2 oracle
make suites     # download reference suites (gitignored)
make oracle     # regenerate goldens from CPython jinja2
make test       # go test ./...
make check      # what CI runs: fmt, vet, test, race, lint
```

CI runs on every pull request and on every commit that reaches `main`. A pull
request from a **fork** deliberately runs nothing until a maintainer has read
the diff and added the `safe-to-test` label: CI executes the code in the pull
request, and that is not something to do to an unreviewed branch. Pushing a new
commit after the label is applied requires it to be applied again.
