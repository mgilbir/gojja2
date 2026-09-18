# gojja2 documentation

Start from the question, not the filename.

| If you are asking… | Read |
|---|---|
| What is this, and how do I render a template? | [../README.md](../README.md) |
| How do I configure loaders, autoescaping, syntax, undefined values? | [guide.md](guide.md) |
| Will my jinja2 templates render the same here? | [divergences.md](divergences.md) — one divergence can change what a working template renders, and it is named in the first paragraph |
| Does it support *X*? Async? `{% trans %}`? The sandbox? | [scope.md](scope.md) |
| How correct is it, and how is that measured? | [conformance.md](conformance.md) |
| How do I add a conformance case, or reproduce the numbers? | [contributing.md](contributing.md), then [conformance.md](conformance.md) |
| I found something gojja2 gets wrong — now what? | [contributing.md](contributing.md) |
| Why did my render fail with `ErrOutputTooLarge`, or stop early? | [limits.md](limits.md) |
| How do I bound a render of a template I do not trust? | [limits.md](limits.md) |
| How do I add a filter, a test, a global, or expose my own type? | [extending.md](extending.md) |
| How does a template get from source to output? | [architecture.md](architecture.md) |
| I am about to change the engine — what shape is it? | [architecture.md](architecture.md) |
| What did an adversarial read of the code find? | [audits/](audits/README.md) — snapshots, not current state |

The Go API itself is documented in the source, as godoc. `go doc
github.com/mgilbir/gojja2` lists it; `go doc Environment`, `go doc State` and
`go doc value.Object` are the three worth reading in full. The runnable examples
in `example_test.go` are part of that documentation and are executed by
`go test`, so they cannot drift.

## Where a fact lives

One fact, one home. Everything else links to it. When two documents state the
same thing they eventually state it differently, and the reader has no way to
tell which one lost.

| Fact | Canonical home |
|---|---|
| What is in and out of scope | [scope.md](scope.md) |
| Where gojja2 deliberately differs from CPython jinja2 | [divergences.md](divergences.md) |
| What bounds a render, and which option adjusts it | [limits.md](limits.md) |
| The conformance corpora, the oracle, and the numbers | [conformance.md](conformance.md) |
| Which upstream projects are consulted, and under what licence | [../NOTICE](../NOTICE) |
| How the pieces fit, and what nesting must not drop | [architecture.md](architecture.md) |
| The contract a filter, test or global must honour | [extending.md](extending.md) |
| The signature and contract of anything exported | godoc |
| Which revision a generated corpus was built from | that corpus's own `SOURCES.md` |

## Glossary

The terms below mean one thing throughout, including in commit messages and in
test output. They are written down because they are consistent today and that is
worth keeping.

**oracle** — CPython `jinja2`, pinned in `.venv` at the version the Makefile
names. It *is* the specification: where gojja2 and the oracle disagree, gojja2 is
wrong. `make ask T='...'` asks it a question directly.

**corpus** — a directory of `.jj2` cases. `testdata/corpus` is gojja2's own and
is committed with its goldens; the rest are built by `make import` from pinned
upstream clones and are gitignored.

**golden** — the JSON record of what the oracle produced for one case. Goldens
are regenerated, never hand-edited.

**gradable** — a case that has an answer something could match. A case is
**ungradable** when the oracle itself cannot reproduce its own output between two
runs, which in practice means the output contains a memory address. Ungradable
cases are reported apart from the pass rate rather than counted against it.

**known failure** — a case listed in `testdata/known_failures.txt`, which gojja2
does not match and which is not going to be fixed. The list is an admission, not
a waiver: a case on it that starts passing fails the test, so the list can only
shrink deliberately.

**divergence** — a place where gojja2 deliberately differs from the oracle. Every
one is in [divergences.md](divergences.md) and is asserted by a test. Anything
not listed there is a bug.

**budget** — the per-render allowance of loop iterations and output bytes,
carried on `*State` and shared across `{% include %}`, `{% extends %}` and
`{% import %}`. A template-chosen size is charged against it *before* the
allocation, never after.

**profile** — a named environment configuration a corpus case can render under,
implemented once for the oracle and once for gojja2 so the two cannot drift.
`transformers` is the one that exists, and it is what chat templates expect.
