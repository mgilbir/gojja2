# Contributing

The short version: **CPython jinja2 is the specification, so start by asking
it.** Almost every change here begins with `make ask` and ends with a
conformance case.

## Setting up

```
make venv       # CPython + the pinned jinja2, into .venv
make test       # grades the committed corpus; needs neither network nor Python
make suites     # clone the ten upstreams (gitignored, a few minutes)
make import     # build the other seven corpora and record jinja2's answers
```

`make test` works on a fresh checkout because the first corpus ships with its
goldens. Everything else is optional until you want the full pass rate.

## The gate before every commit

```
make check
```

That is `fmt-check vet test memlimit race lint`, in CI's order, and it is
everything CI runs. If it passes and CI does not, that is a bug in the Makefile
and worth reporting on its own.

**Run it under a cgroup cap.** This suite deliberately renders hostile templates
— `{{ range(10000000000)|list }}`, `{{ "x" * 1000000000 }}` — and a bound that
is not yet in place means an allocation that does not stop:

```
systemd-run --user --scope -p MemoryMax=8G -p MemorySwapMax=0 -p CPUQuota=400% make check
```

Without the cap, a missing guard takes the machine instead of the process. With
it, the same bug is an exit code you can read, which is also the clean way to
*demonstrate* that a safety bound fires.

When you are reproducing a resource bug, put the probe in a throwaway module
outside this repository rather than in a `zz_test.go` here. A probe dropped in
the tree joins the package, so `go test ./...` gets killed too and the whole run
reports `Terminated` with no usable signal.

## I think I found a bug

Ask the oracle first. It is one command, and about a third of the time the
answer is that jinja2 does the same thing:

```
make ask T='{{ [1,2]|map("string")|last }}'
make ask T='{{ a + 1 }}' C='{"a": 41}'
```

Then decide which of three things you have:

```mermaid
flowchart TD
    Q["gojja2 and the oracle disagree"] --> D{"Is the difference<br/>reachable from a template?"}
    D -->|no| NOP["Not a bug. Write it down<br/>in the commit message if it cost you time."]
    D -->|yes| S{"Is it a safety control —<br/>a bound, a ceiling, a refusal?"}
    S -->|yes| L["Document it in limits.md.<br/>CPython has no budget to exceed,<br/>so there is nothing to match."]
    S -->|no| I{"Could gojja2 reasonably<br/>match CPython here?"}
    I -->|yes| FIX["**It is a bug.** Fix it,<br/>and add a corpus case."]
    I -->|no| DIV["Document it in divergences.md,<br/>assert it with a test, and add<br/>the case to known_failures.txt."]

    classDef bug fill:#fecaca,stroke:#b91c1c,color:#000
    class FIX bug
```

The default answer is the red one. `divergences.md` says so at the top —
"anything not listed here is a bug" — and the list is deliberately short. A new
entry needs a reason that would survive someone asking "why not just match it?",
and the reasons that have survived are: CPython's answer is unreachable from a
template, unreproducible between two runs of CPython itself, or a safety
control.

## Adding a conformance case

**Cases go in `tools/oracle/gen_corpus.py`, never as a loose `.jj2` file.**
`main()` starts with `shutil.rmtree(testdata/corpus)` and rewrites the whole
directory from the `CASES` list, so a hand-added file is deleted by the next
`make oracle` — reported only as a bland change in the case count. Nineteen were
lost that way once. The script's own comment marks where they were recovered
to.

```python
case("filters/attr_name_not_a_string", '{{ "ab"|attr(name=true) }}')
case("errshape/dyn_kwargs_names_the_filter", '{{ lst|join(**5) }}', lst=[1, 2])
```

The first argument is the path under `testdata/corpus`, the second the template,
and any keywords become the render context. Then:

```
systemd-run --user --scope -p MemoryMax=4G -p MemorySwapMax=0 make oracle
```

which regenerates the corpus *and* re-records every golden from CPython. Check
the diff: a golden that changed for a case you did not touch means something
else moved, and that is the interesting part.

`make oracle` also regenerates nine files from CPython itself — `arity.go`,
`method_arity.go`, `method_arity_other.go`, `entities.go`, `strclass.go`,
`casemap.go`, `utf8digest.go`, `value/decimaltable.go` and
`value/unicode_other.go`. Do not hand-edit those; change the generator.

**Which CPython generates them is pinned**, by `PYTHON_VERSION` in the Makefile
— 3.13 — and `make venv` asserts it, as does `TestDefaultVersionMatchesThePin`
against `value.DefaultPythonVersion` and the header of every generated file. That is part of the specification rather than a
convenience: CPython carries its own Unicode, so the interpreter decides the
case mappings, the decimal digits and how `repr` escapes them. It used to be
whatever `uv venv` found on the machine, which meant the specification was
chosen by accident and two contributors could regenerate different goldens.

The Unicode tables are the ones with a history worth knowing. Several of them
recorded *only where CPython differs from Go's own tables*, which is sound only
while the two agree about everything else — and nobody checked that. They do not
agree, and how far apart they are depends on the pin: Go 1.26 is Unicode
15.0.0, CPython 3.13 is 15.1.0 and 3.14 is 16.0.0. On the 3.14 pin that
assumption silently produced the wrong answer for 54 case mappings, 4,924
`isalpha` code points and 5,812 `isprintable` ones before the checks below
existed; on 3.13 the same gap is 622 and 627, which is smaller and just as
wrong. Every generator that compares against Go now reads Go's tables through
`tools/gocase` rather than assuming them.

Three tripwires guard the result, and each recomputes over every code point so a
drift shows up as a test failure rather than as one wrong character:

| test | covers | regenerate with |
|---|---|---|
| `TestCaseMappingMatchesCPython` | upper, lower, title, casefold and the three case predicates | `make casemap` |
| `TestDecimalValuesMatchCPython` | which characters `int()` and `float()` read as digits | `make decimal` |
| `TestUTF8DecodeMatchesCPython` | every way the UTF-8 decoder can refuse a byte | `make utf8` |

If one fails after a toolchain upgrade or a `PYTHON_VERSION` bump, regenerate
and read the diff: it is telling you either that Unicode moved or that the
pinned CPython did.

The differential harness talks to a live interpreter rather than to the
goldens, and `GOJJA2_ORACLE_PYTHON` points it at one: useful for a checkout
whose virtualenv lives elsewhere, and -- aimed at a path that does not exist --
for proving the suite really does grade the committed corpus with no Python.
Whatever it names is checked on startup against the interpreter and libraries
the goldens record, because an oracle on another CPython is a different
specification and a differential run against one reports every version
difference as a gojja2 bug.

Three matrix targets regenerate everything that is stored per version, each by
asking every interpreter through `uv run --python`, so none of them needs a
hand-built environment: `make unicode-matrix` for the Unicode tables,
`make arity-matrix` for the built-in method wordings, and `make golden-matrix`
for `testdata/golden-<version>`. All three are part of `make oracle`, which
means a `PYTHON_VERSION` bump rotates which version needs no overrides without
anyone editing a list. `TestEveryPythonVersion` grades the whole corpus
against each one.

If your case changes the corpus count, `TestConformance` will tell you the exact
table row it wants. Paste it into `docs/conformance.md`; the README's headline
sentence is checked too, and the test prints that as well.

## Changing an existing golden

Don't, directly. `testdata/golden/*.json` is what CPython produced; if it needs
to change, either the case changed or the pinned jinja2 did. `make oracle-check`
verifies the committed goldens still match without rewriting them, which is the
one to reach for when you want to know whether something moved underneath you.

Bumping `JINJA_VERSION` means regenerating every corpus wholesale, never
piecemeal — the goldens are one version's answers, and a mixture is not a
specification.

## Writing a filter, test, global or object

[extending.md](extending.md), which has the contract and runnable examples. The
short version: charge a template-chosen size *before* you allocate it, and call
`State.Poll` in any loop that does sustained work without writing output.

## Hunting for divergences

`make soak N=200000` generates templates from the grammar and requires both
implementations to agree on output, exception class, message and line.
`make fuzz TIME=5m` does the same, coverage-guided. Both need `make venv`.

`make fuzz-props TIME=5m` needs nothing but Go, and is what CI runs. It cannot
say what a template *means* -- only CPython can say that -- so it checks what
the engine owes every input regardless of meaning: it does not panic, it does
not run past its deadline, it does not write past its bound, it does not return
an error with no Kind on it, and -- under autoescape -- it does not let a plain
string's markup reach the output as written. That is a weaker question asked far more
often, and it has found what the differential fuzzer structurally cannot: a
constant expression that hung `FromString`, where there is no render, no
context and nothing for CPython to disagree with.

Two things to know before you trust a result:

- A divergence whose CPython side is `MemoryError`, `RecursionError` or a
  timeout is the oracle's own sandbox refusing, not a finding.
- A CPython side containing `object at 0x...` is a repr with an address in it,
  and is ungradable for the same reason `lipsum()` is.

## Documentation

Docs are graded like code. `go test ./...` runs four guards over them:

| guard | requires |
|---|---|
| `TestDocLinksResolve` | every relative link resolves, and every `#anchor` names a real heading |
| `TestNoDocIsOrphaned` | every `.md` is reachable by a link from another one |
| `TestNoGodocLinkSyntaxInMarkdown` | no `[Identifier]`, which is godoc syntax and renders as literal brackets |
| `TestExtendingDocExamplesAreReal` | every definition shown in `extending.md` exists verbatim in `example_test.go` |

`TestConformance` additionally checks every published figure against what it just
measured. [docs/README.md](README.md) names a canonical home for each fact worth
stating once; if you find yourself writing something down for the second time,
link instead.

## Sending it

CI runs on every pull request and on every commit that reaches `main`.

```mermaid
stateDiagram-v2
    [*] --> Trusted: branch in this repository
    [*] --> Waiting: pull request from a fork
    Waiting --> Trusted: a maintainer reads the diff<br/>and adds `safe-to-test`
    Trusted --> Waiting: a new commit is pushed<br/><i>(revoke-approval.yml strips the label)</i>
    Trusted --> [*]: fmt · vet · test · GOMEMLIMIT · race · lint
    Waiting --> [*]: nothing runs
```

A fork's pull request runs nothing until somebody has looked at it, because CI
executes the code in the pull request. The label is attached to the pull request
rather than to a commit, which is the well-known hole in label-based gating — get
the label on a harmless diff, then push whatever you like — so a push strips it
again and the approval always refers to code someone actually read.

Commit messages here say *why*, at length, and name what was checked. Look at
`git log` before writing one.
