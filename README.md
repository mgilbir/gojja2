# gojja2

A pure Go implementation of the [Jinja2](https://jinja.palletsprojects.com/)
template language, built to be behaviourally identical to CPython's `jinja2`.

[Documentation index](docs/README.md) ·
[Divergences](docs/divergences.md) ·
[Guide](docs/guide.md) ·
[Limits](docs/limits.md) ·
[Extending](docs/extending.md) ·
[Scope](docs/scope.md) ·
[Audits](docs/audits/README.md)

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

Five things worth knowing before the sixth line:

- **Go values cross by reflection.** A struct exposes its exported fields, by
  name or `json` tag, and its methods that take no arguments; slices become
  lists, maps become dicts. A method that *takes* arguments is not exposed by
  default, because calling one lets the template choose what a host method is
  invoked with.
- **Errors carry the Python exception class** jinja2 would have raised, so
  `errors.Is(err, errs.UndefinedError)` works, and `errors.Is(err,
  errs.LookupError)` catches a `KeyError` exactly as `except` would.
- **A template compiled with `FromString` is escaped**, because it has no name
  to decide by. Defaulting it to *unescaped* is how an escaped-by-configuration
  project ends up emitting raw user input.
- **A render cannot mutate what you passed it**, and cannot outrun its
  `context.Context` — or, behind that, ten million loop iterations and 256 MiB
  of output. Zero means "the default" for every limit option, so a configuration
  nobody filled in is the safe one.
- **`FSLoader` refuses any name with a `..` segment** rather than cleaning it
  into something else: a template that asks for a file outside its root gets
  "not found", and not a different file.

[docs/guide.md](docs/guide.md) has the rest under headings you can link to:
loaders, rendering and what buffers, the Go bridge, errors, the four undefined
behaviours, autoescaping, the syntax options, and concurrency.
[docs/extending.md](docs/extending.md) covers adding a filter, a test or a
global, and exposing a type on its own terms.

## Ground truth

CPython's `jinja2` **is** the specification. Every behavioural question is
settled by rendering the template with the real thing and recording what it
produced:

```
$ .venv/bin/python tools/oracle/oracle.py --template '{% set d = {1:"a",} %}{{ d[1] }}'
{
  "case": "<stdin>",
  "oracle": {
    "impl": "cpython-jinja2",
    "version": "3.1.6",
    "markupsafe": "3.0.3",
    "python": "3.11.15"
  },
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
| gojja2's own (committed, with goldens) | 739 | 738 |
| MiniJinja fixtures | 159 | 159 |
| Jinja's own test suite (harvested templates) | 658 | 656 |
| minja's syntax tests | 162 | 162 |
| llama.cpp's Jinja tests | 281 | 281 |
| LLM chat templates x 10 conversation shapes | 810 | 808 |
| A documentation theme's templates | 84 | 84 |
| Cookiecutter project templates | 166 | 166 |
| **total** | **3059** | **3054 (99.8%)** |

Each imported corpus is a different project's independent reading of the
language -- MiniJinja (Rust), minja (C++), llama.cpp's own engine, the
templates real models ship, a theme written to be used rather than tested, and
four project generators. Only their *inputs* are used: every expected output is
regenerated from the pinned CPython jinja2, because that is the specification.
On top of that, roughly a million generated templates have been rendered by both
implementations and compared (see below).

Those numbers are not typed in by hand. `TestConformance` parses this README and
fails the build on any row that disagrees with what it just measured, whenever
every corpus is present -- which it does because the table had drifted, twice,
after cases were added and the prose was not.

The 5 that differ are listed, with reasons, in `testdata/known_failures.txt`. A
case on that list which starts passing also fails the test, so the list can only
shrink deliberately.

They are three kinds. **Two** are Jinja's own sandbox-escape tests, which walk a
Python object graph out to `__subclasses__` and `__import__`; `__class__` *is*
implemented, and these two go past it. **Two** are DeepSeek-R1's chat template,
which writes `{{ tools|map(attribute='function')|tojson }}` -- jinja2's `map`
returns a generator, which `json.dumps` refuses, so the template raises under
CPython and renders under gojja2. **The fifth** is `{% if 1e400 %}`: jinja2
writes a folded constant into its generated Python as that constant's repr, and
`repr(float("inf"))` is the bare word `inf`, so the template raises a NameError
there and renders here. All three are explained in
[docs/divergences.md](docs/divergences.md).

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
error, and a mutation changes one choice rather than corrupting a tag. A
divergence is shrunk against the same check before it is reported, so findings
arrive minimal.

The oracle runs as a warm subprocess. That is what makes a soak practical at
all: starting an interpreter and importing jinja2 per case costs tens of
milliseconds, which caps a cold run at a few tens of templates a second, where
keeping one up runs into the hundreds. Budget minutes for
`make soak N=200000`, not seconds -- which is why the target allows itself an
hour. The subprocess runs under a memory cap and a per-render timeout, so a
pathological case degrades to an error instead of taking the machine down.

This is where most of the subtler behaviour in this list came from: that
jinja2 wraps a sort key in a list (so two undefineds sort but do not compare),
that `{% include ... without context %}` bypasses an enclosing filter buffer,
and that a name assigned anywhere at template level is invisible to nested
scopes until the assignment runs.

## Scope

Jinja2's template language, not Python. Constructs that merely *look* Pythonic
are in scope -- multi-line tags, tuple unpacking, slicing, trailing commas in
every literal. Constructs that exist only because jinja2 compiles to Python
bytecode are not, and neither are async rendering, the i18n extension, bytecode
caches, or `SandboxedEnvironment`.

[docs/scope.md](docs/scope.md) draws the line in full and says why each exclusion
falls where it does. The sandbox entry is the one worth reading before you assume
you know the answer: what bounds a template's reach here is `WithMethodPolicy`
and what the caller puts in the context, not a type system and not an
allow-list.

## Licence

Apache 2.0. See `LICENSE` and `NOTICE`.

gojja2 contains no code from any other implementation of the language. Ten
upstream projects are consulted as behavioural references and corpus inputs:
Jinja itself, MiniJinja, minja, llama.cpp, a collection of real chat templates,
a documentation theme and four project generators. `make suites` downloads them
on demand into the gitignored `third_party/`; nothing from any of them is
vendored, committed or redistributed. `NOTICE` names each one with its licence,
and each generated corpus repeats it in its own `SOURCES.md`.

## Development

```
make help       # all targets
make venv       # CPython + jinja2 oracle
make suites     # download reference suites (gitignored)
make oracle     # regenerate goldens from CPython jinja2
make test       # go test ./...
make check      # everything CI runs, in CI's order
make ask T='{{ 1/2 }}'  # what does CPython jinja2 render for this?
```

CI runs on every pull request and on every commit that reaches `main`. A pull
request from a **fork** deliberately runs nothing until a maintainer has read
the diff and added the `safe-to-test` label: CI executes the code in the pull
request, and that is not something to do to an unreviewed branch. Pushing a new
commit after the label is applied requires it to be applied again.
