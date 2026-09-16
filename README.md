# gojja2

A pure Go implementation of the [Jinja2](https://jinja.palletsprojects.com/)
template language, built to be behaviourally identical to CPython's `jinja2`.

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
