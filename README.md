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
return tmpl.RenderTo(w, map[string]any{"user": user, "items": items})
```

Go values cross into templates by reflection: structs expose their exported
fields (by name or by `json` tag) and their nullary methods, slices become
lists, and maps become dicts. Errors carry the Python exception class jinja2
would have raised, so `errors.Is(err, errs.UndefinedError)` works.

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
| gojja2's own (committed, with goldens) | 264 | 264 |
| MiniJinja fixtures | 159 | 159 |
| Jinja's own test suite (harvested templates) | 658 | 656 |
| **total** | **1081** | **1079 (99.8%)** |

On top of that, roughly a million generated templates have been rendered by
both implementations and compared (see below).

The 2 that differ are listed, with reasons, in `testdata/known_failures.txt`;
a case on that list which starts passing fails the test, so the list can only
shrink deliberately. Both are Jinja's own sandbox-escape tests, which walk a
Python object graph out to `__subclasses__` and `__import__`. `__class__` *is*
implemented; these two go past it -- see
[docs/divergences.md](docs/divergences.md).

One further case is marked *ungradable* and left out of the table: it renders a
generator's memory address, which differs between two runs of CPython itself,
so jinja2 does not match it either. Nothing else is excluded -- a case gojja2
simply fails stays in the denominator.

Underneath, the pieces are graded separately against the real thing: CPython's
`repr()` over 3,200 floats and strings, every binary operator over a 39-value
pool (20,665 cases), jinja2's own token stream (113 cases) and its own parse
tree (100 cases).

The first corpus is committed with its goldens, so `go test ./...` grades
against CPython's answers on a fresh checkout with no network and no Python.
Run `make suites && make import` to add the other two.

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
```
