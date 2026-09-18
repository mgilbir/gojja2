# Audits

**Everything in this directory is a snapshot, not a specification.** Each file
records what was true on one day, at one commit. None of it is maintained, and
none of it should be read as a description of how gojja2 behaves now — for that,
read the code, or `docs/` outside this directory.

That warning is here because the alternative already happened. The 2026-09-16
audit sat in `docs/` for two days with thirty findings all marked `CONFIRMED`,
including four Critical ones that said the process dies. By the time anyone
looked again, twenty-eight were fixed and one had never been real — but nothing
on the page said so, so a contributor opening `docs/` to find work would have
started reproducing bugs that were gone.

## The rule

A finding here is trustworthy only if someone has re-run it. When a report is
superseded, the superseding one re-runs every finding of its predecessor and the
older report gains a **Status** column carrying the result. That is the whole
convention, and it is cheap: the re-run is what the report already tells you how
to do.

Correcting a finding counts. A finding that turns out to have been wrong is more
useful marked wrong than quietly deleted, because the reason it was wrong is
usually the interesting part.

**No editing of the body.** The banner and the status column are added; the
findings themselves are left as written, including the ones that were mistaken.
Rewriting an audit to match today's code destroys the only thing it is good for.

## What is here

| Report | Taken at | Covers | State |
|---|---|---|---|
| [codebase-audit-2026-09-16.md](codebase-audit-2026-09-16.md) | `fa584a9` | Every `.go` file, the Makefile, both workflows, the corpora | 30 findings, re-run 2026-09-18: 28 fixed, 1 partial, 1 not a defect |
| [docs-audit-2026-09-18.md](docs-audit-2026-09-18.md) | `9da6620` | Every reader-facing surface: README, `docs/`, NOTICE, `make help`, godoc, the workflows' comments | 22 findings |

An earlier codebase audit, describing commit `b7c96ca`, was superseded rather
than kept. It is still retrievable:

```
git show dd44811:docs/audits/codebase-audit-2026-09-16.md
```

## How these were produced

Both reports were adversarial reads with a common method, worth stating because
it is what makes the findings checkable rather than opinions:

- **The oracle settles behavioural questions.** CPython jinja2, pinned in
  `.venv`, is this project's specification. A claim about what jinja2 does is
  worth nothing until it has been run — which is exactly the step C28 of the
  2026-09-16 audit skipped, and exactly why it was wrong.
- **Resource claims run under a cgroup cap.** Each hostile template goes in a
  throwaway module outside the repository, one per invocation, under
  `systemd-run --user --scope -p MemoryMax=… -p MemorySwapMax=0`, and the exit
  status is the result: 137 is an OOM kill, 2 a Go stack overflow. A probe left
  in the repository joins the package and takes `go test ./...` down with it.
- **A guard that has not been seen to fail proves nothing.** Every check a report
  claims is load-bearing was broken on purpose, watched to fail, and restored.
- **Hypotheses that did not survive are recorded**, so the next reader does not
  spend the afternoon again.
