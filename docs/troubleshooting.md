# Troubleshooting

What to do when a command in this repository fails or says less than you
expected. Each entry is a symptom you can search for, what causes it, and the
fix. For the commands themselves and what they are for, see
[contributing.md](contributing.md); for what bounds a render, see
[limits.md](limits.md).

## A test or gate was killed

**`Terminated`, exit status 143, or `Killed`, exit status 137, from a command
run under `systemd-run`.** The cgroup cap stopped it: 137 is the kernel's OOM
killer, 143 the scope being terminated. That is the cap working -- this suite
renders hostile templates on purpose, and an unbounded allocation is meant to
end as an exit code rather than as a frozen machine. Before reading it as a
regression, check the cap was big enough for what you ran:

| command | cap it needs |
|---|---|
| `go test ./...`, `make test` | 4G |
| `make soak`, `make fuzz`, `make oracle` | 4G -- the CPython oracle runs inside the same scope |
| `make check` | **8G**: its `go test -race ./...` step peaks around 6.4 GB in `./conformance`, and dies at 4G on a clean `main` |

Read the exit status directly. `systemd-run ... make check | tail` reports the
status of `tail`, which is 0, and hides the kill.

**The whole package reports `Terminated` with no failing test named.** A probe
file left in the repository root -- `zz_probe_test.go` -- joins the package, so
`go test ./...` runs it too and is killed with it. Put probes in a throwaway
module outside the repository, whose `go.mod` replaces
`github.com/mgilbir/gojja2` with this checkout's path; contributing.md, "The
gate before every commit", says why.

## A test passed but checked nothing

**`go test -v ./conformance` logs `corpus "minijinja" not present; run make
import to include it`, and `published figures not checked`.** The imported
corpora are gitignored and built by `make suites && make import`. Without them
`TestConformance` grades the committed corpus only, and does not check the
per-corpus table or the headline in `README.md` and `docs/conformance.md` --
which is why CI, which never imports, cannot catch a stale figure there.
`TestPublishedVersionFigures` needs only committed files and always runs.

**The differential and profile tests are skipped:
`conformance: no CPython jinja2 oracle available (run make venv)`.** They ask
a live CPython and there is none. `make venv` builds `.venv` with the pinned
interpreter and libraries. `GOJJA2_ORACLE_PYTHON` points them at an
interpreter elsewhere; one whose CPython, jinja2 or markupsafe differs from the
goldens' is refused with `the committed goldens were recorded under ...`,
because a differential against a different specification reports every
version difference as a gojja2 bug.

**`go test -run TestDifferential ./conformance` ran 3,000 templates, not
50,000.** Run directly, the soaks use their own defaults (3,000 and seed
20260916); `make soak` sets `GOJJA2_FUZZ_N=50000 GOJJA2_FUZZ_SEED=0`. Pass
`N=` and `SEED=` to make, or set the variables yourself.

## A published figure or golden is out of date

**`docs/conformance.md conformance table is out of date. expected row: ...`,
`does not state the measured headline. expected: ...`, or `diagram does not
draw the measured rate`.** You changed the corpus or a known failure. The test
prints the exact line it wants: paste it over the old one, in
`docs/conformance.md` and `README.md` both.

**`docs/conformance.md does not state the measured figure; it should say: ...`
from `TestPublishedVersionFigures`.** The same, for the version-matrix table in
"Which CPython?": a case was added or a per-version golden changed.

**`... is listed in testdata/known_failures.txt (...) but now passes; remove
the entry`.** Something you changed made a known divergence agree with CPython.
Delete the line -- and if the case was a documented divergence, the entry in
`docs/divergences.md` too.

**`corpus "own" has N case(s) with no golden; run make oracle`.** A case was
added without recording CPython's answer. Cases go in
`tools/oracle/gen_corpus.py`, never as loose `.jj2` files, and `make oracle`
writes both.

**`make oracle-check` exits 1.** A committed golden in `testdata/golden` no
longer matches what the pinned CPython renders for its case: the case changed,
or something underneath it did. `make oracle` and read the diff. It checks
`testdata/golden` only; the per-version and generated files are checked by
running `make oracle` and looking for any diff at all, which is what the
weekly oracle workflow does.

## Lint or format disagrees with CI

**`make lint` passes locally and CI's lint fails, or the reverse.** `make lint`
runs whatever `golangci-lint` is on your `PATH`; CI pins `v2.14.0` in
`.github/workflows/ci.yml`. Install that version.

**`make fmt-check` passes locally and CI's formatting step fails.** Your Go
toolchain is newer than `go.mod`'s, and the two `gofmt`s disagree on a corner.
Use the version `go.mod` names.

## A CI fuzz failure

**CI's `Fuzz` step fails with "fuzzing process hung or terminated unexpectedly",
and nothing reproduces locally.** That message means the fuzzing worker process
died -- Go's fuzzer has no per-input timeout -- so look for something `recover`
cannot catch: an allocation past memory, a stack overflow, a panic on another
goroutine. CI starts from a cold cache, and a warm local one explores
differently; reproduce it the same way:

```
GOCACHE=$(mktemp -d) systemd-run --user --scope -q -p MemoryMax=4G -p MemorySwapMax=0 -- \
  go test . -run xxx -fuzz '^FuzzRender$' -fuzztime 60s
```

When a run does fail it writes the input to `testdata/fuzz/<target>/<id>`;
`go test . -run '<target>/<id>'` replays it. Commit it as a seed once it is
fixed, so `go test` asks the question on every run.

## The oracle workflow failed

**The weekly `regenerate` job reports a diff.** Something under `make oracle`
produces different output than the committed files: a generator changed, the
pinned CPython moved, or a generated file was edited by hand. Run `make oracle`
locally and read the diff; never fix it by editing a generated file.

**The `releases` job reports a CPython it does not know.** A new interpreter
was released. Its report is what that interpreter would change; adding it is
the work of a pull request, not a regeneration -- a version in
`tools/oracle/pyversions.py` and `value/pyversion.go`, its goldens from `make
oracle`, and a named rule for each behaviour it moved. See "What watches the
oracle" in contributing.md.

## make mutate left the tree changed

**`git status` shows a file you did not touch after `make mutate` was
interrupted.** The tool writes the untouched copy to `tools/.mutate-restore/`
before each edit and puts it back on the next run, because an `atexit` hook
cannot run when the kernel kills the process. Run `make mutate` again, or copy
the file back from that directory.
