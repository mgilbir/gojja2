# gojja2

A pure Go implementation of the [Jinja2](https://jinja.palletsprojects.com/)
template language, built to be behaviourally identical to CPython's `jinja2`.

[Documentation index](docs/README.md) ·
[Divergences](docs/divergences.md) ·
[Guide](docs/guide.md) ·
[Conformance](docs/conformance.md) ·
[Contributing](docs/contributing.md) ·
[Limits](docs/limits.md) ·
[Extending](docs/extending.md) ·
[Scope](docs/scope.md) ·
[Audits](docs/audits/README.md)

## Using it

```go
env, err := gojja2.New(
    gojja2.WithLoader(gojja2.FSLoader{FS: os.DirFS("templates")}),
    gojja2.WithAutoescapeExtensions(".html"),
)
if err != nil {
    return err
}

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
behaviours, autoescaping, the syntax options, which CPython to reproduce, and
concurrency.
[docs/extending.md](docs/extending.md) covers adding a filter, a test or a
global, and exposing a type on its own terms.

## Ground truth

CPython's `jinja2` **is** the specification -- pinned at 3.1.6, on a pinned
interpreter. Every behavioural question is settled by rendering the template
with the real thing and recording what it produced:

```
$ .venv/bin/python tools/oracle/oracle.py --template '{% set d = {1:"a",} %}{{ d[1] }}'
{
  "case": "<stdin>",
  "oracle": {
    "impl": "cpython-jinja2",
    "version": "3.1.6",
    "markupsafe": "3.0.3",
    "python": "3.13"
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

### Which CPython

The interpreter is part of the specification, not a detail of the harness. It
decides what `{{ d[0:1] }}` raises, whether `{{ xs|sort(reverse=none) }}` is an
error, how a division by zero is worded, and -- because CPython carries its own
Unicode -- which characters are digits, how they case, and how `repr` escapes
them.

gojja2 reproduces **CPython 3.11 through 3.14**, and is generated against
**3.13** — the pinned interpreter, which is what a caller who does not choose
gets:

```go
env, err := gojja2.New(gojja2.WithPythonVersion(gojja2.Python311))
```

Across those four, 66 of the 2,241 committed cases answer differently. Each
version is graded against its own recorded output, so the option is measured
rather than asserted.
[docs/guide.md](docs/guide.md#which-cpython) has the shape of it and
[docs/conformance.md](docs/conformance.md#which-cpython) the numbers.

## Conformance

**4707 of 4721 gradable cases (99.7%)** match CPython jinja2 — eight corpora
drawn from ten upstream projects, including Jinja's own test suite, MiniJinja's
fixtures, minja, llama.cpp, the chat templates real models ship, a documentation
theme and four project generators. Only their *inputs* are used; every expected
output is regenerated from the pinned CPython jinja2, because that is the
specification.

The five that differ are listed with reasons in `testdata/known_failures.txt`,
and a case on that list which starts passing fails the build. Two are Jinja's own
sandbox-escape tests, two are DeepSeek-R1's chat template hitting the
generator/list fork, and the fifth is `{% if 1e400 %}`; all three kinds are
explained in [docs/divergences.md](docs/divergences.md).

On top of the corpora, roughly a million generated templates have been rendered
by both implementations and compared, requiring them to agree on output,
exception class, message and line. `make soak N=200000` runs it seeded and
reproducible; `make fuzz TIME=5m` runs it coverage-guided.

Numbers in documentation drift, so these do not get to: `TestConformance` parses
the table in [docs/conformance.md](docs/conformance.md) and the figure in this
paragraph, and fails the build on any disagreement with what it just measured.

[docs/conformance.md](docs/conformance.md) has the per-corpus table, what each
corpus contributes and why, how a context is synthesised for templates that ship
without one, and the differential loop drawn.

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

[docs/contributing.md](docs/contributing.md) has the rest: how to tell a bug
from a deliberate divergence, where a conformance case has to live, how to
regenerate a golden, and why every one of these commands wants a cgroup cap
around it.
