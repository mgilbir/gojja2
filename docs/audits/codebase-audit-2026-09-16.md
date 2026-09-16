# gojja2 — adversarial codebase audit

**Date:** 2026-09-16
**Commit:** fa584a9 "Run CI on every commit that reaches main, and gate fork pull requests"
**Scope:** the whole repository — every `.go` file read in full (~22,500 lines across the
root package, `value/`, `internal/`, `conformance/`), plus `Makefile`, `README.md`,
`docs/`, `.golangci.yml`, both GitHub workflows, `tools/oracle/`, and the committed
corpora.

**Supersedes** the audit recorded at commit `dd44811`, which described commit `b7c96ca`
and whose findings have since been largely fixed. That report is still retrievable with
`git show dd44811:docs/audits/codebase-audit-2026-09-16.md`. This is a fresh read, not a
re-check of that list; where a finding here resembles an old one it was rediscovered from
the code, and the old IDs are not carried over.

**Method.** Every claim marked CONFIRMED was reproduced. Behavioural claims were
adjudicated against the pinned oracle (`.venv`, CPython 3.11.15 / jinja2 3.1.6 /
markupsafe 3.0.3), which this project defines as the specification. Resource claims were
reproduced in a throwaway module outside the repository, each run under
`systemd-run --user --scope -p MemoryMax=… -p MemorySwapMax=0`, so that an allocation bomb
reports as exit 137 and a stack overflow as exit 2 rather than taking the machine down.
Hypotheses that did not survive are recorded in §7 rather than promoted.

---

## 1. Summary

| ID | Severity | Area | Issue | Site | Status |
|---|---|---|---|---|---|
| C1 | Critical | limits / inheritance | `{% import %}` renders with **no budget and no context** — a nested render with *no* allowance, not a fresh one | exec.go:636 | CONFIRMED |
| C2 | Critical | inheritance | `{% block x %}{{ self.x }}{% endblock %}` kills the process with a Go stack overflow | runtime.go:403,445 | CONFIRMED |
| C3 | Critical | value / compare | Ordered comparison of cyclic values kills the process; `==` is bounded, `<` is not | value/compare.go:160,208 | CONFIRMED |
| C4 | Critical | optimizer | `%` formatting at **compile** time is unbudgeted: a 42-byte template OOM-kills `FromString` at a 4 GB cap | optimize.go:404 | CONFIRMED |
| C5 | High | filters | `\|tojson` of `-inf` calls `Builder.Reset()` and destroys the document produced so far | filters_web.go:452 | CONFIRMED |
| C6 | High | lexer / DX | Lexing is quadratic in tag count when a delimiter is absent — 1.6 MB compiles in 34 s | internal/lexer/lexer.go:146 | CONFIRMED |
| C7 | High | value / limits | `StrSlice`/`StrIndex` allocate 8 bytes per input byte, uncharged — 8× past the output budget | value/str.go:30,40,62 | CONFIRMED |
| C8 | High | parser | The 1,000-level nesting bound does not apply to `not`, unary `-` or filter chains | internal/parser/expr.go:117,233 | CONFIRMED |
| C9 | High | globals / limits | `x in range(...)` is an uninterruptible linear scan; CPython's is O(1) | value/compare.go:336 | CONFIRMED |
| C10 | High | filters | `\|unique` is O(n²) and never polls the context | filters_seq.go:246 | CONFIRMED |
| C11 | High | filters | **No filter checks arity**; extra arguments are silently accepted or reinterpreted | filters.go:22 | CONFIRMED |
| C12 | High | CI | The "Test under a memory cap" job runs nothing: every package replays from the test cache | .github/workflows/ci.yml:111 | CONFIRMED |
| C13 | Medium | pyformat | `%*s` and `%.*f` are broken — the value is consumed before the star width | value/pyformat.go:102 | CONFIRMED |
| C14 | Medium | pyformat | A large width emits Go's `%!(NOVERB)%!(EXTRA string=x)` into the document | value/pyformat.go:314 | CONFIRMED |
| C15 | Medium | pyformat / limits | `%` allocation is uncharged at render time too: 4 KiB budget, 10 MB built, success reported | eval.go:211 | CONFIRMED |
| C16 | Medium | lexer | A float literal that overflows to `inf` is a syntax error; CPython renders `inf` | internal/lexer/number.go:182 | CONFIRMED |
| C17 | Medium | filters | `\|sum(start='')` concatenates where CPython raises `TypeError` | filters.go:1360 | CONFIRMED |
| C18 | Medium | autoescape | `"%c"\|safe % 60` emits an unescaped `<`; markupsafe raises | value/pyformat.go:344 | CONFIRMED |
| C19 | Medium | loader | `FSLoader` silently **remaps** `../x` to `x` instead of refusing it; the rejection loop is dead code | loader.go:75 | CONFIRMED |
| C20 | Medium | Go bridge | A method whose only result is `error` renders the error object and reports success | value/convert.go:459 | CONFIRMED |
| C21 | Medium | hygiene | `State.exported` / `State.export()` are write-only; the doc says imports read them | exec.go:647, template.go:203 | CONFIRMED |
| C22 | Low | errs | `Error.Stack []Frame` and `Frame` are never written or read — dead exported API | errs/errs.go:125,141 | CONFIRMED |
| C23 | Low | errs | `At` says "returns a copy of err" and mutates in place | errs/errs.go:190 | CONFIRMED |
| C24 | Low | DX | `make check` claims "Everything CI runs"; there is no `make lint`, and CI runs two more jobs | Makefile:241 | CONFIRMED |
| C25 | Low | docs | divergences.md says known_failures.txt has "only two entries"; it has four | docs/divergences.md:344 | CONFIRMED |
| C26 | Low | lexer | `"\Uffffffff"` renders U+FFFD; CPython raises "illegal Unicode character" | internal/lexer/strlit.go:142 | CONFIRMED |
| C27 | Low | hygiene | Four dead or write-only fragments the linter cannot see | see §3.7 | CONFIRMED |
| C28 | Low | autoescape | `SelectAutoescape("")` selects nothing; jinja2 builds the pattern `"."` | environment.go:260 | CONFIRMED |
| C29 | Low | API | `Globals()` hands out the live map; a caller can clobber `range` | environment.go:392 | CONFIRMED |
| C30 | Low | value | `SliceBounds` and `SliceIndices` duplicate the clamp logic verbatim | value/str.go:87,139 | CONFIRMED |

**Counts:** 4 Critical, 8 High, 9 Medium, 9 Low — 30 findings, all CONFIRMED by execution
or by direct comparison against the pinned oracle.

### What is genuinely sound

Said once, because it is substantial and it shapes the diagnosis.

- **Conformance is real and it is measured.** `TestConformance` reports 2587/2591 (99.8%),
  exactly the README's table, and the table is asserted rather than transcribed. A 30,000-template
  differential soak against the live CPython oracle (seed 7) passed with zero divergences
  in 38 s. The corpus-plus-oracle machinery is the best part of this project.
- **The value layer's arithmetic is exemplary.** `saturatingMul` (value/ops.go:393),
  `estimatePowBits` (value/ops.go:650), exact int/float ordering through `big.Rat`
  (value/compare.go:240), CPython's `float_divmod` (value/ops.go:458) and the
  `maxInt64AsFloat` boundary note (value/value.go:130) are all correct for the right
  reasons, and each carries the reason.
- **Concurrency is clean.** 8 goroutines × 200 renders across inheritance, include, import,
  macros and mutating list methods, and 8 × 500 renders of one shared `*Template`, are
  race-free under `-race`. The template cache is properly locked and folded constants are
  copied per evaluation, so nothing is shared that should not be.
- **The comments are load-bearing.** Almost every non-obvious decision in this codebase
  says *why*, and the reasons are specific and checkable. That is rare, and it is what made
  this audit possible at this depth.

The findings below are concentrated in exactly the places that story does *not* cover:
compile time, recursion depth, and anything a hand-written corpus would not think to write.

---

## 2. System map

### Packages

```
gojja2            public API: Environment, Template, State, Loader, filters, tests, globals
 ├── errs         Python exception classes + hierarchy (Kind, *Error)
 ├── value        the value model: Value, Seq, Dict, Undefined, ops, repr, Go bridge
 └── internal
     ├── lexer    source -> []Token   (syntax config, whitespace control, string literals)
     ├── parser   []Token -> ast.Template (recursive descent, jinja2 precedence)
     └── ast      node set mirroring jinja2's `nodes`
conformance       oracle-backed corpus grading, generator, differential fuzzer, shrinker
tools/oracle      CPython jinja2 harness (untracked by Go; driven from the Makefile)
```

### Real execution paths

**Compile** — `Environment.FromString` / `FromNamedString` / `GetTemplate`
→ `compile` (environment.go:502), which is `defer catchPanic` then:

1. `parser.Parse` → `lexer.Tokenize` (whole source, all tokens up front) → recursive descent.
2. `foldConstantExpressions` — the general fold, over every expression in the tree.
3. `foldConstantPrints` — a second, print-tag-only fold that also accepts undefined results.
4. `checkDependencies` — unknown filter/test names, with `{% if %}` softening the check.
5. `collectBlocks` — index blocks by name, refusing duplicates.

`GetTemplate` consults a bounded LRU first (cache.go), and stores on success only.

**Render** — `Template.Render` / `RenderString` → `RenderValues` → `renderInto`
(template.go:128), which builds a `State`, wraps the writer in a `bufio.Writer`, and walks
the tree with `exec` (exec.go). `{% extends %}` does not recurse: it parks the parent on
`st.parent` and `renderInto` loops.

**Nested renders** take three different routes, and they are not equivalent:

| construct | new State | budget threaded | depth counted | output |
|---|---|---|---|---|
| `{% include %}` | yes, via `renderInto` | **yes** | yes (`st.enter`) | fully buffered, then written on |
| `{% extends %}` | no — same State | yes | yes (`enterExtends`) | parent body replaces child's |
| `{% import %}` / `{% from %}` | yes, **hand-built** | **no** (C1) | yes | discarded |
| `{% macro %}` call | no — same State | yes | yes | captured |
| `{% block %}` / `self.x` | no — same State | yes | **no** (C2) | captured |

That table is the shape of C1 and C2: three routes, each written separately, and the two
that were written by hand rather than through `renderInto` are the two that are missing a
guard.

### Key invariants, and where they are enforced

| invariant | enforced at | holes |
|---|---|---|
| A render costs ≤ `maxIterations` steps | `budget.step` / `chargeSteps`, called from `runLoop`, `materialize`, `evalArgs`, `unpack`, `extend` | C1 (import), C9 (`in range`), C10 (`unique`) |
| A render writes ≤ `maxOutputBytes` | `budget.account`, from `exec.writeTo` and `State.ChargeBytes` | C7 (`StrSlice`), C15 (`%`) |
| A template-chosen size is charged **before** allocation | `State.ChargeBytes`/`ChargeItems`, `repeatStringN` | C4, C7, C15 |
| Recursion cannot exhaust the stack | parser `MaxNestingDepth`, `State.maxRecursion`, `maxCompareDepth`, `maxToGoDepth` | C2, C3, C8 |
| A panic never escapes | `catchPanic` at both entry points, `tryConstEval`, `methodObject.call` | C2, C3 — a *stack overflow* is fatal, not a panic |
| Compile time is bounded | `maxFoldedConst`, `maxFoldBytes`, the fold-attempt budget | C4 (`%`), C6 (lexer) — neither is covered |
| Output is escaped under autoescape | `renderValue`, `evalConcat`, per-filter | C18 |
| CPython is the specification | corpus + oracle + differential fuzzer | C11 (arity is structurally unreachable by both) |

---

## 3. Findings

### 3.1 Critical

---

**C1 — `{% import %}` renders with no budget and no context.**
`exec.go:636`

`importModule` builds its sub-render's `State` by hand:

```go
st := tmpl.newState(vars)
st.depth = ex.st.depth
var discard strings.Builder
sub := &exec{st: st, sc: st.ctx, out: &discard, stream: &discard, autoescape: st.autoescape}
```

`newState` does not set `budget`; only `renderInto` does (`template.go:131`). So `st.budget`
is nil for the whole imported render, and every guard degrades to a no-op — `budget.step`,
`budget.account` and `budget.tick` all begin `if b == nil { return nil }`. `State.Context()`
likewise falls back to `context.Background()` when the budget is nil.

This is worse than the "fresh allowance" the code was written to avoid. docs/divergences.md
says the budget "is shared across `{% include %}` and `{% extends %}`, so a nested render
cannot start a fresh allowance" — it does not mention import, which is the one place it is
not shared at all.

**Scenario (CONFIRMED).** `bomb.txt` is `{% for i in range(100000000) %}{% endfor %}`,
default environment (10,000,000 iteration budget):

```
{% include "bomb.txt" %}        -> 4.2 s, error "render exceeded 10000000 loop iterations"
{% import "bomb.txt" as m %}ok  -> 42.1 s, output "ok", err=<nil>
```

All 100,000,000 iterations ran and the render reported success. With
`WithoutLimits()` and a 2-second `context.WithTimeout`, the same import over `range(1e9)`
was still running when the harness was killed at 90 s: the deadline is never consulted.

**Direction.** Route `importModule` through `renderInto`, or at minimum set
`st.budget = ex.st.budget` beside `st.depth = ex.st.depth`. Better: make `newState`
require the budget so the omission cannot recur — a nil budget should be reachable only
from constant folding, which builds its own.

---

**C2 — a self-referential block kills the process.**
`runtime.go:403` (`blockReference.render`), `runtime.go:445` (`Str`), `exec.go:459`

`blockReference.Str()` renders the block so that `{{ self.body }}` works. Nothing bounds
that: `render()` never calls `State.enter()`, unlike `execInclude`, `callMacro` and
`execExtends`. A block that prints itself therefore recurses through
`execBody → execOutput → eval → getAttr → renderValue → value.Str → Str → render` forever.

A Go stack overflow is a `fatal error`, not a panic: `catchPanic` cannot see it, and
docs/divergences.md's "A backstop on panics" promise does not hold.

**Scenario (CONFIRMED).** `{% block x %}{{ self.x }}{% endblock %}` under
`MemoryMax=2G`:

```
runtime: goroutine stack exceeds 1000000000-byte limit
fatal error: stack overflow
exit status 2
```

`{% block x %}{{ self.x() }}{% endblock %}` (the explicit call) does the same.

CPython jinja2 renders the first as
`<jinja2.runtime.BlockReference object at 0x…>` — its `BlockReference` has no `__str__`, so
printing it gives the repr and no recursion happens. Giving it one is a divergence that is
not recorded in docs/divergences.md, and it is the vehicle for this bug.

**Direction.** Bracket `blockReference.render()` with `st.enter()`/`st.leave()`, exactly as
`callMacro` does. Separately, decide whether `{{ self.x }}` should render at all, and if it
should, say so in docs/divergences.md — it is a real behavioural difference either way.

---

**C3 — ordered comparison of cyclic values kills the process.**
`value/compare.go:160` (`compare`), `value/compare.go:208` (`compareSeq`)

`equalDepth` is depth-bounded, and the comment above `maxCompareDepth` explains precisely
why: "a Go stack overflow cannot be recovered". Its sibling `compare`/`compareSeq` pair,
which implements `<`, `<=`, `>`, `>=` and every sort, carries no depth parameter and no
bound. Two *distinct* cyclic structures have no fixed point, so the mutual recursion never
terminates.

A cyclic value is reachable from a **default** environment — no extension needed, because
`{% set _ = a.append(b) %}` is an ordinary assignment.

**Scenario (CONFIRMED).** Each run under `MemoryMax=2G`:

```
{% set a=[] %}{% set b=[] %}{% set _=a.append(b) %}{% set _=b.append(a) %}{{ a == b }}
    -> err "maximum recursion depth exceeded in comparison"      (correct)
{% set a=[] %}{% set b=[] %}{% set _=a.append(b) %}{% set _=b.append(a) %}{{ a < b }}
    -> fatal error: stack overflow, exit status 2
{% set a=[] %}{% set b=[] %}{% set _=a.append(b) %}{% set _=b.append(a) %}{{ [a,b]|sort }}
    -> fatal error: stack overflow, exit status 2
```

CPython raises `RecursionError: maximum recursion depth exceeded in comparison` for both of
the failing cases — the same message gojja2 already produces for `==`. The expected
behaviour is unambiguous and the fix is mechanical.

**Direction.** Thread a depth through `compare`/`compareSeq` the way `equalDepth` does, and
raise `RecursionMessageComparison` at the same wall. Note that `value.Copy`
(value/convert.go:635) and `writeRepr` (value/repr.go:68) are unbounded in the same way;
`Copy` is currently only reached from constant folding, where a cycle cannot occur, but
nothing in its signature says so.

---

**C4 — `%` formatting at compile time is unbudgeted, and OOM-kills `FromString`.**
`optimize.go:404` (`constBinOp`, `case ast.OpMod`), `value/pyformat.go`

`constBinOp` guards exactly one operator against building something enormous at compile
time:

```go
case ast.OpMul:
    if size, _, isRepeat := value.RepeatSize(left, right); isRepeat && size > maxFoldedConst {
        return value.Undefined, false
    }
...
case ast.OpMod:
    out, err = value.Mod(left, right)          // no guard
```

`value.Mod` on a string is `FormatPercent`, which lives in `value/` and therefore has no
`*State`, no budget and no ceiling. It builds the whole result and only then is
`constSizeOK(out)` consulted — by which time the memory is committed. The fold-attempt
budget (`maxFoldSteps`, `maxFoldBytes`, optimize.go:520) exists for exactly this class and
`FormatPercent` never consults it.

**Scenario (CONFIRMED).** A 42-byte template, compiled by an environment configured with
`WithMaxOutputBytes(4096)` and `WithMaxIterations(1000)`:

```go
env.FromString(`{{ ("%(a)10000000s" * 200) % {"a": "x"} }}`)
```

Killed (exit 137) under `MemoryMax=256M`, and killed again under `MemoryMax=4G`. The `*`
is folded first (800 bytes, well under the cap), then the `%` expands 200 directives at
10 MB each. `FromString` never returns.

This is the precise failure the `maxFoldedConst` comment describes —
"`{{ "x" * 1000000000 }}` allocated a gigabyte before anything asked for the template to be
rendered" — with `%` in place of `*`.

**Direction.** Two possible shapes, and the second is the one worth having:
(a) gate `case ast.OpMod` on a computed output size the way `OpMul` is gated; (b) give
`FormatPercent` a charge callback (or move it behind a `State`-aware wrapper in the root
package) so that both the fold budget and the render budget reach it. (b) also fixes C15.

### 3.2 High

---

**C5 — `|tojson` of `-inf` destroys the document.**
`filters_web.go:447-456`

```go
case value.KindFloat:
    f := v.AsFloat()
    if f != f || f > 1e308 || f < -1e308 {
        b.WriteString(map[bool]string{true: "NaN", false: "Infinity"}[f != f])
        if f < 0 {
            b.Reset()                        // <- the whole builder, not this element
            b.WriteString("-Infinity")
        }
        return nil
    }
```

`b` is the accumulator for the *entire* JSON document, not for this element. `Reset()`
discards everything written before it.

**Scenario (CONFIRMED).**

```
{{ [1, -1e308*10, 2]|tojson }}      CPython: [1, -Infinity, 2]      gojja2: -Infinity, 2]
{{ {"a": 1, "z": -1e308*10}|tojson }}                               gojja2: -Infinity}
```

Silent wrong output, and the shape that matters: `|tojson` is what a template uses to embed
data in a `<script>` block, so this emits a truncated, syntactically broken payload with no
error.

**Direction.** Write `"-Infinity"` directly rather than writing `"Infinity"` and then
retracting it. The whole branch collapses to a three-way switch on NaN / +Inf / -Inf.

---

**C6 — lexing is quadratic in tag count when a delimiter is absent.**
`internal/lexer/lexer.go:146` (`findTag`)

`findTag` runs `strings.Index(l.src[from:], c.delim)` for each of `{#`, `{%` and `{{` at
every tag. A delimiter that does not occur in the template makes its `Index` scan to the
end of the source *on every call*, so a template with k tags and no comments costs O(n·k).

**Scenario (CONFIRMED), and the cause isolated:**

| source | bytes | compile |
|---|---|---|
| `{{1}}` × 20,000 | 100 KB | 0.16 s |
| `{{1}}` × 40,000 | 200 KB | 0.57 s |
| `{{1}}` × 80,000 | 400 KB | 2.17 s |
| `{{1}}` × 160,000 | 800 KB | 8.66 s |
| `{{1}}{#c#}{% if 1 %}{% endif %}` × 160,000 | **4.96 MB** | **0.48 s** |

Doubling the input quadruples the time in the first group; the second group, where all
three delimiters occur every few bytes, is linear and 6× larger for 1/18th the time. A
1.6 MB template of print tags takes 34.5 s to compile.

This compounds with the fact that **compilation takes no `context.Context` and has no
budget of any kind**: `Environment.FromString` on attacker-supplied source cannot be
bounded or cancelled by the caller at all.

**Direction.** Find the next occurrence of each delimiter once and cache it, invalidating
only when the cursor passes it — or scan for the shared `{` prefix and dispatch. Separately,
consider whether `compile` should take a context; the README's cancellation story stops at
`Render`, and the lexer is where an unbounded input is first touched.

---

**C7 — `StrSlice`/`StrIndex` allocate 8 bytes per input byte, uncharged.**
`value/str.go:30` (`runeOffsets`), `:40` (`StrIndex`), `:62` (`StrSlice`)

`runeOffsets` builds `make([]int, 0, len(s)+1)` — an `int` per *byte* — and `StrSlice`
calls it unconditionally, with no ASCII fast path. `SliceIndices` then materialises the
selected index list as well. None of it is charged: `alloc.go`'s doctrine covers sizes the
template *names*, and this one is derived from the input string's length.

**Scenario (CONFIRMED).** Default environment (256 MiB output budget). Under
`MemoryMax=700M`:

```
{% set s = "x" * 100000000 %}{{ s[0] }}ok     -> 277 ms, 200 MB allocated, succeeds
{% set s = "x" * 100000000 %}{{ s[0:1] }}     -> OOM-killed, exit 137
{% set s = "é" * 50000000 %}{{ s[1] }}   -> 1,000 MB allocated for a 100 MB string
```

The repetition itself is charged correctly (100 MB of a 256 MiB budget). Taking a
**one-character slice** of the result then allocates 800 MB that the budget never sees — an
8× amplification of whatever the budget did allow.

**Direction.** Give `StrSlice` the ASCII fast path `StrIndex` already has, and for the
multi-byte case walk the string rather than materialising every offset (a forward unit-step
slice needs two positions, not n). Where an offset table is genuinely needed, charge it.

---

**C8 — the nesting bound does not apply to non-bracket nesting.**
`internal/parser/expr.go:117` (`parseNot`), `:233` (`parseUnary`), `:82` (`parseCondExpr`), `:435` (`parseFilterExpr`)

`p.enter()` is called from `parseStatement`, `parseExpression` and `parsePrimary`. Chains
that recurse *without* passing through any of them are uncounted: `parseNot` calls itself,
`parseUnary` calls itself, `parseCondExpr` calls itself for the `else` branch. Left-deep
chains built by `parsePostfix`/`parseFilterExpr` are not recursive in the parser but produce
an AST of depth n that every later walker — `constFolder.descend`, `frameVisitor.expr`,
`depChecker.expr`, and `exec.eval` at render time — descends recursively.

**Scenario (CONFIRMED).**

```
{{ [[[…2000 deep…]]] }}          -> "expression or statement nests deeper than 1000 levels"
{{ not not not … ×50000 … 1 }}   -> accepted
{{ 1|abs|abs|abs … ×50000 }}     -> accepted
{{ -------- … ×50000 … 1 }}      -> accepted
```

MaxNestingDepth's own comment says it exists because "a template of a million nested
brackets parses happily and then takes the process down — where CPython would have raised
RecursionError long before" and that "templates are frequently attacker-supplied". The
bound is 50× exceeded by three spellings that cost 1–4 bytes each.

**Direction.** Move `enter`/`leave` down to the productions that actually recurse
(`parseNot`, `parseUnary`, `parseCondExpr`), and bound the *chain length* in
`parsePostfix`/`parseFilterExpr`/`parseConcat` against the same limit, since chain length is
tree depth for every consumer.

---

**C9 — `x in range(...)` is an uninterruptible linear scan.**
`value/compare.go:336` (`Contains`, `case Sequence`), `globals.go:68` (`rangeObject.Len`)

```go
case Sequence:
    for i := range o.Len() {
        v, ok := o.GetIndex(i)
        if ok && Equal(item, v) { return true, nil }
    }
```

`rangeObject.Len()` saturates at `math.MaxInt` for a range longer than an int, and this loop
walks it one element at a time. There is no `State` in `value.Contains`, so no budget
charge and no context check — `Contains` is not reachable from `State.Poll`.

CPython's `range.__contains__` is O(1) for an integer argument.

**Scenario (CONFIRMED).** `{{ -1 in range(9223372036854775807) }}` with a **3-second**
`context.WithTimeout` was still running when the harness was killed at 90 s. `{% if x in
range(n) %}` over a caller-supplied `n` is an ordinary-looking template.

**Direction.** Give `rangeObject` a `Contains`-like fast path (arithmetic membership), and
give `value.Contains` a charge/poll hook for the general `Sequence`/`Iterable` arms — the
same treatment `materialize` already has.

---

**C10 — `|unique` is quadratic and never polls.**
`filters_seq.go:246`

`seen` is a `[]value.Value` scanned linearly for every item, so n distinct items cost
n²/2 `value.Equal` calls. jinja2 uses a set. `materialize` charges n steps; the n²
comparisons that follow are charged nothing and call neither `Step` nor `Poll`.

**Scenario (CONFIRMED).** `{{ range(60000)|list|unique|length }}` with a 3-second deadline
was still running at 90 s. 60,000 items is a small list.

docs/divergences.md promises that "the built-in filters that do sustained work without
writing output poll as they go, so a cancelled render stops within microseconds". `urlencode`
and `urlize` do; `unique`, `sort`, `groupby`, `min` and `max` do not.

**Direction.** Key `seen` by `hashKey` (the machinery already exists — `CheckHashable` is
called on every key anyway, so the hash is computed and thrown away). Add `Poll` to the
sustained-work filters, and add a linearity guard beside `TestURLEncodeIsLinear`, which is
the right shape of test and currently guards one filter.

---

**C11 — no filter checks its arity.**
`filters.go:22` (`registerDefaultFilters`), contrast `tests.go:75` (`addTest`)

Every default filter reads the arguments it wants through `arg(args, i, name)` and ignores
everything else. Extra positional arguments are not merely dropped — several filters
*reinterpret* them, because position `i` means something different than the template author
intended.

The asymmetry is striking: `tests.go` has a purpose-built `addTest(env, name, pyName,
maxArgs, fn)` wrapper that reproduces CPython's arity message verbatim, including the C
builtin's special phrasing for `callable`. Nothing equivalent exists for filters.

**Scenario (CONFIRMED).** 96 probes (48 filters × {5 extra positional args, one unknown
keyword}), graded against the pinned oracle; 85 diverge. A sample:

```
{{ ["a","b"]|upper(1,2,3,4,5) }}   CPython TypeError: do_upper() takes 1 positional argument but 6 were given
                                   gojja2  ['A', 'B']
{{ ["a","b"]|upper(zzz=1) }}       CPython TypeError: unexpected keyword argument 'zzz'
                                   gojja2  ['A', 'B']
{{ ["a","b"]|min(1,2,3,4,5) }}     CPython TypeError: do_min() takes from 2 to 4 …
                                   gojja2  UndefinedError: str object has no element 2
{{ ["a","b"]|slice(1,2,3,4,5) }}   CPython TypeError: sync_do_slice() takes from 2 to 3 …
                                   gojja2  [['a', 'b', 2]]
{{ "x"|center(5, "ab") }}          CPython TypeError: do_center() takes from 1 to 2 …
                                   gojja2  '  x  '
```

The last is the most instructive: `str.center`'s *method* form correctly refuses a
two-character fill (`fillCharArg`, methods.go:700, with a comment explaining why silently
returning the wrong length is worse than refusing) while the *filter* form silently accepts
a fill argument jinja2 does not have at all.

**Why the corpus and the fuzzer both miss this** is in §4.4.

**Direction.** A `filters.go` counterpart to `addTest`: one table of
`{name, pyName, maxPositional, allowedKwargs}` and a wrapper that raises CPython's message.
The signatures can be generated from jinja2's own `inspect.signature` in `tools/oracle/`,
which removes the transcription risk entirely.

---

**C12 — CI's "Test under a memory cap" step runs nothing.**
`.github/workflows/ci.yml:111-114`

```yaml
- name: Test under a memory cap
  run: go test ./...
  env:
    GOMEMLIMIT: 1GiB
```

`GOMEMLIMIT` is read by the Go runtime at startup, not through `os.Getenv`, so it does not
participate in the `go test` cache key. The step replays the previous step's results.

**Scenario (CONFIRMED).**

```
$ go clean -testcache && go test ./...
ok github.com/mgilbir/gojja2 4.522s   … real 0m6.441s
$ GOMEMLIMIT=1GiB go test ./...
ok github.com/mgilbir/gojja2 (cached) … real 0m0.134s
```

Every package reports `(cached)`. Zero test code executes.

There is a second problem behind the first: `GOMEMLIMIT` is a **soft** limit. The Go runtime
responds to it by collecting more aggressively, not by failing an allocation. The step's
comment says "without a memory cap a regression would look like a slow job rather than a
failure, so the runner is given one it can actually hit" — a soft limit produces exactly the
slow job the comment is trying to avoid.

**Direction.** Add `-count=1` so the step runs, and make the cap hard — `ulimit -v`, a
`systemd-run --scope -p MemoryMax=`, or a container memory limit — so an allocation
regression reports as a kill rather than as a slow job. The probes in this audit are all
run that way and it works well.

### 3.3 Medium — `%` formatting

---

**C13 — `%*s` and `%.*f` are broken.** `value/pyformat.go:82-121`

The value is taken from the argument stream *before* the star width:

```go
default:
    if arg, err = takeArg(); err != nil { … }     // consumes positional[0]
}
if conv.starWidth {
    w, err := takeStarInt(&positional, &next)     // consumes positional[1]
```

Python consumes the width first, then the value.

**Scenario (CONFIRMED).**

```
{{ "%*s" % (5, "x") }}    CPython '    x'     gojja2 TypeError: * wants int
{{ "%.*f" % (3, 1.5) }}   CPython '1.500'     gojja2 TypeError: * wants int
```

Both spellings are unusable. **Direction:** resolve `starWidth`/`starPrec` before the
`arg` switch, and drop the duplicated `takeArg()` calls inside the star blocks.

---

**C14 — a large width emits Go's formatter diagnostics into the document.**
`value/pyformat.go:314` (`apply`, via `goVerb`)

`conversion.goVerb` reassembles the directive as a Go format string and hands it to
`fmt.Sprintf`. Go's `parsenum` refuses a width whose running value passes 10⁶ and swallows
the rest of the directive, producing `%!(NOVERB)`.

**Scenario (CONFIRMED).**

```
{{ "%200000000s" % "x" }}   CPython 200,000,000 spaces + 'x'
                            gojja2  "%!(NOVERB)%!(EXTRA string=x)"
```

Rendered into the document, with no error. `{{ "%1500000000s" % "x" }}` is worse: it is
constant, so the 28-byte junk is **folded into the compiled template** and every render
emits it.

Below that threshold (up to ~10⁷) the width is honoured and the allocation is real; see C15.

**Direction.** Implement the padding directly rather than delegating the whole directive to
`fmt` — the width/precision/flag handling is a dozen lines and removes both the ceiling and
the leak of Go-specific diagnostics into rendered output.

---

**C15 — `%` allocation is uncharged at render time.** `eval.go:211`

`evalBinOp` charges `OpMul` through `chargeRepeat` and charges nothing for `OpMod`.

**Scenario (CONFIRMED).** Environment with `WithMaxOutputBytes(4096)`:

```
{% set z = fmtstr % "a" %}{{ z|length }}   with fmtstr = "%9999999s"
-> renders "9999999", err=<nil>
```

A 10 MB string was built inside a render whose entire output allowance is 4 KiB, and the
render reported success. Same root cause as C4; one fix covers both.

### 3.4 Medium — conformance

---

**C16 — a float literal that overflows to `inf` is a syntax error.**
`internal/lexer/number.go:182`

```go
f, err := strconv.ParseFloat(strings.ReplaceAll(text, "_", ""), 64)
if err != nil { return …, TemplateSyntaxError("invalid float literal %q", text) }
```

`strconv.ParseFloat` returns `±Inf` *together with* `ErrRange` for an out-of-range literal.
Discarding the value on error turns Python's `inf` into a compile failure.

**Scenario (CONFIRMED).** `{{ 1e999 }}` → CPython `inf`, gojja2
`TemplateSyntaxError: invalid float literal "1e999"`. Same for `1e400` and
`1.7976931348623157e309`. The whole template fails to compile.

**Direction.** Accept the value when `errors.Is(err, strconv.ErrRange)`; reject only a
genuine syntax failure. (Note the mirror case for integers is already handled: `ParseInteger`
uses `big.Int`.)

---

**C17 — `|sum(start='')` concatenates where CPython raises.** `filters.go:1360`

`filterSum` folds with `value.Add`, which concatenates strings. Python's builtin `sum`
refuses a `str` start specifically.

**Scenario (CONFIRMED).** `{{ ['a','b']|sum(start='') }}` → CPython
`TypeError: sum() can't sum strings [use ''.join(seq) instead]`, gojja2 `ab`.
(`sum(start=[])` over lists matches: both render `[1, 2]`.)

**Direction.** Refuse a `str` start with CPython's wording. Worth noting the quadratic
shape too: repeated `Add` over n strings is O(total²) and uncharged.

---

**C18 — `"%c"|safe % 60` emits an unescaped `<`.** `value/pyformat.go:344`

The `'c'` arm returns `string(rune(n))` directly, bypassing the `text()` closure that
escapes every other conversion in a Markup format string.

**Scenario (CONFIRMED).** With autoescape on, `{{ "%c"|safe % 60 }}` renders `<`.
markupsafe raises `TypeError: %c requires int or char`, because its escape helper does not
satisfy `%c`'s integer protocol.

A single `<` is not an exploit by itself, but it is a raw control character crossing an
escaping boundary in the one filter chain whose entire purpose is to mark a *template* safe
without marking its *arguments* safe (the comment at methods.go:408 makes exactly that
point). **Direction:** match CPython and refuse `%c` in a Markup format.

### 3.5 Medium — boundaries and the Go bridge

---

**C19 — `FSLoader` remaps traversal instead of refusing it, and the guard is dead code.**
`loader.go:75` (`safeJoin`)

```go
name = strings.TrimPrefix(path.Clean("/"+strings.ReplaceAll(name, "\\", "/")), "/")
if name == "" || name == "." { return "", false }
for _, part := range strings.Split(name, "/") {
    if part == ".." { return "", false }          // unreachable
}
```

`path.Clean` on a rooted path resolves away *every* `..`, so no element of `name` can be
`".."` by the time the loop runs. The loop has never executed and cannot. The function's own
doc says it works by "refusing any name that would escape it"; it works by rewriting.

**Scenario (CONFIRMED).** `FSLoader{FS: fs, Root: "tpl"}` over a MapFS containing
`secret.html` and `tpl/secret.html`:

```
{% include "../secret.html" %}          -> renders tpl/secret.html
{% include "x/../../secret.html" %}     -> renders tpl/secret.html
{% include "..\\secret.html" %}         -> renders tpl/secret.html
```

The confinement itself holds — nothing outside `Root` is reachable — so this is not a
traversal vulnerability. It is an affordance defect and a maintenance hazard: a template
that asks for a file it should not get is silently served a *different* file, and the line
a reader would point at as "the security control" is not one.

**Direction.** Either refuse a name containing a `..` element **before** cleaning (so the
guard is real and the failure is honest), or delete the loop and rewrite the comment to say
that names are normalised into `Root`. The first is better: "not found" is the right answer
to `../secret.html`.

---

**C20 — a Go method returning only `error` renders the error and reports success.**
`value/convert.go:456-466`

```go
switch len(out) {
case 0:  return None, nil
case 1:  return FromGoWith(out[0].Interface(), m.expose), nil     // an error lands here
}
// two or more: the last result is checked for error
```

A `func (T) Fail() error` has `NumOut == 1`, so its non-nil error is converted like any
other value.

**Scenario (CONFIRMED).**

```
{{ h.Fail() }}   -> "<errors.errorString object>", err=<nil>     // error swallowed
{{ h.Pair() }}   -> "", err=kaboom                                // (string, error): correct
{{ h.Panics() }} -> "", err=Panics() panicked: host blew up       // correct
```

The single-result case is the odd one out, and it is the shape a host writes for a
validating accessor.

**Direction.** Check the last result for `error` regardless of arity; a lone `error` result
should fail the render (or render nothing) rather than be reflected into an object.

---

**C21 — `State.exported` and `State.export()` are write-only.**
`exec.go:647`, `template.go:201-203`

The field's doc comment says: "exported lists the names a top-level `{% set %}` bound, in
order, so `{% import %}` can expose them." Nothing reads it. `moduleObject.GetAttr`
(exec.go:666) resolves against `st.ctx.vars` directly, and re-implements the underscore
filter that `export()` also applies.

`export()` is called from four places and does a linear scan of the slice on every top-level
assignment, so it is not free. `unused` cannot see it because the field *is* written.

**Direction.** Delete both, or make `moduleObject` read `exported` — which is the better
answer, because "the names this module exports, in order" is a real concept and having it
defined in exactly one place is the point.

### 3.6 Low — documentation

**C22 — dead exported API described as a feature.** `errs/errs.go:125,141`
`Error.Stack []Frame` and the `Frame` type ("one entry of a template traceback") are never
written and never read. There is no template traceback. Delete, or build it — `errs.At`
already sees every frame boundary.

**C23 — `errs.At` says "returns a copy" and mutates.** `errs/errs.go:190`
"At returns a copy of err located at name:line, filling in only the fields that are still
unset" — it type-asserts to `*Error`, assigns `e.Line`/`e.Name` in place and returns the
same pointer. The `if == 0` guards make it idempotent, so no bug follows today, but the
comment tells a reader it is safe to share an `*Error` across renders and it is not.

**C24 — `make check` is no longer what CI runs.** `Makefile:241`
`check: fmt-check vet test race ## Everything CI runs`. CI also runs a `lint` job
(golangci-lint v2.12.2) and the memory-cap step, and there is no `make lint` target at all
even though `.golangci.yml` is committed and a lint failure has already been fixed once
(commit 9e51626). README:223 repeats the claim. A contributor cannot run CI locally.

**C25 — divergences.md contradicts itself and the README.** `docs/divergences.md:344`
"These are the only two entries in `testdata/known_failures.txt`" — the file has four
failure entries (two sandbox-escape, two DeepSeek `map|tojson`) plus four ungradable ones,
and line 77 of the same document says the DeepSeek pair is listed there. README:110 says
four. The sentence was not updated when the DeepSeek entries were added.

**C26 — `\Uffffffff` renders U+FFFD.** `internal/lexer/strlit.go:142`
`readHexEscape` accumulates into an `int` and returns `rune(v)`; eight hex digits overflow
`int32`, so the `r > 0x10FFFF` check sees a negative number and passes. CPython raises
`TemplateSyntaxError: illegal Unicode character`; gojja2 renders the replacement character.
Range-check `v` before the conversion.

**C27 — four fragments no linter can see.**
- `value.Hashable` (value/dict.go:262) is exported and called only by tests; `CheckHashable`
  is the real entry point.
- `methodJoin`'s `total` accumulator (methods.go:286,300) is incremented and never read —
  a leftover from before `ChargeBytes` was introduced two lines later.
- `depChecker.checkCallerDefault`'s first branch (depcheck.go:131) is a `continue` that
  changes nothing; the function is two lines of logic wearing six.
- `writeStringRepr`'s `utf8.RuneError` arm (value/repr.go:256) says "show it as a byte" and
  writes the replacement character instead.

`.golangci.yml`'s own preamble says `unused` was enabled because "three had accumulated" —
it cannot catch a write-only field (C21, C22) or an exported-but-unused symbol in a library
package. Worth knowing the limit of the guard that was just installed.

**C28 — `SelectAutoescape("")` and `(".")` select nothing.** `environment.go:260`
`normalizeExtensions` skips an extension that is empty after `TrimLeft(".")`. jinja2's
`f".{x.lstrip('.').lower()}"` produces the pattern `"."`, which matches a name ending in a
dot. Escaping *less* than jinja2 is the direction this codebase treats as a security
property everywhere else (environment.go:193, docs/divergences.md §default extension set).

**C29 — `Globals()` hands out the live map.** `environment.go:392`
Documented as "must not be mutated", and a caller who does can replace `range` for every
template in the environment (CONFIRMED: `env.Globals()["range"] = …` makes `{{ range }}`
render the substitute). The doc discharges the obligation; a copy, or no accessor at all,
would remove it.

**C30 — `SliceBounds` and `SliceIndices` duplicate the clamp.** `value/str.go:87,139`
The `lower`/`upper`/`clamp` block is written twice, character for character. Two sources of
truth for Python slice semantics, one of which is used by `range` and the other by
everything else.

---

## 4. Design tensions

### 4.1 The budget is a render concept living above a value layer that cannot see it

`value/` holds every primitive that allocates from a template-chosen size — `repeat`,
`FormatPercent`, `StrSlice`, `SliceIndices`, `Dict.Set` — and it cannot import the root
package, so it cannot reach a `*State`. The chosen answer is that each *caller* charges
before calling: `evalBinOp` → `chargeRepeat` → `value.Mul`, `pad` → `ChargeBytes` →
`repeatString`. That works exactly as far as someone enumerated the call sites, which is why
C4, C7 and C15 are all the same bug wearing different hats — `%` was never enumerated, and
`StrSlice`'s cost is not derived from a number the template names, so it did not look like a
member of the category.

The comment at the top of `alloc.go` states the rule beautifully and then leaves its
enforcement to vigilance: "Nearly every resource defect in this engine has had one shape."
It still does.

**The alternative I would weigh:** make the charge a property of the *allocation* rather
than of the call site. Give `value` a small `Budget` interface (one method, `Charge(n
int64) error`) with a nil-safe no-op implementation, and pass it into the handful of
primitives that size from data. The root package's `*State` satisfies it; constant folding
passes its fold budget; tests pass nil. The compile-time and render-time holes close
together, and "did anyone remember to charge this?" becomes a signature question instead of
a review question.

### 4.2 Compile time has a budget that does not cover what compile time does

`newConstEvaluator` builds a `budget` with `maxFoldSteps`/`maxFoldBytes` and resets it per
fold attempt — careful, well-reasoned work (optimize.go:510-523). But that budget is only
consulted by code that goes through `State.Step`/`ChargeBytes`, and the two most expensive
things `compile` does go through neither: the lexer (C6, quadratic and unbounded) and
`FormatPercent` (C4, OOM). Meanwhile `compile` takes no `context.Context` at all, so a
caller who has a deadline for `Render` has none for `GetTemplate`.

The README's safety story is entirely about renders: "Every render takes a
`context.Context` and stops when it is cancelled". A service that compiles user-supplied
templates — which the sandbox discussion in docs/scope.md assumes is a real use — has no
control over the phase where the input is first touched.

**The alternative:** `compile(ctx, source, name)` with the same budget type, charged by the
lexer per token and by the folder as it already is. It is a breaking API change, which is
the argument against; `GetTemplateContext`/`FromStringContext` beside the existing pair is
the compromise.

### 4.3 Recursion is bounded in four places and unbounded in six

Today: `parser.MaxNestingDepth` (statements and primaries only — C8),
`State.maxRecursion` (include/extends/macro/recursive-loop), `value.maxCompareDepth`
(`==` only — C3), `value.maxToGoDepth` (the Go bridge). Unbounded: `compare`/`compareSeq`,
`blockReference.render`, `writeRepr`, `hash` over nested tuples, `value.Copy`, and the four
AST walkers that descend a left-deep chain.

Four counters with four different limits, four different failure modes (TemplateSyntaxError,
RecursionError, RecursionError-in-comparison, silent nil) and no shared notion of "how deep
are we". Each was added when a specific overflow was found, which is why the coverage looks
like a list of past incidents rather than a policy — and why C2 and C3 sit right next to
guards that would have caught them.

**The alternative:** one depth counter on the render state that every recursive descent
enters through, with the *class* of error decided at the entry point rather than the depth.
The cost is threading it into `value/`, which §4.1 argues for anyway.

### 4.4 "CPython is the specification" is enforced only where the corpus can reach

The conformance machinery is excellent and it measures a real thing — 2,591 cases from eight
independent sources, regenerated goldens, a differential fuzzer, a shrinker. A 30,000-template
soak against the live oracle passed clean while this audit was running.

And nine of my fifty hand-written probes diverged.

They diverge because the generator emits only *well-formed* calls. `conformance/generate.go`
carries a hand-curated `filterArgs` table of plausible argument lists — `"center": {`12`}`,
`"batch": {`2`, `2, 'X'`}` — so it never produces `|center(12, '*')` or `|upper(1,2,3)`. The
imported corpora are real templates written by people who got the arity right. So the entire
argument-validation surface (C11: 85 of 96 probes diverge) is structurally invisible to both
gates, and so are the edges nobody writes on purpose: `1e999` (C16), `%*s` (C13),
`-Infinity` in JSON (C5), `sum(start='')` (C17).

The measurement is honest about what it measures. The risk is reading 99.8% as coverage when
it is *agreement on the cases that exist*.

**The alternative I would weigh:** derive the negative space from the oracle rather than
inventing it. `tools/oracle/` already imports jinja2; `inspect.signature` over
`jinja2.filters.FILTERS` and `TESTS` yields every arity and keyword name, from which a
corpus of *wrong* calls generates mechanically. The same trick covers value-shape edges:
sweep each filter over the existing 39-value pool (the operator corpus already does this)
rather than over hand-picked inputs. Both are cheap and both would have found C11, C13 and
C17 before I did.

### 4.5 Streaming is promised, buffering is what happens

`Render`'s doc says output is streamed and names `{% include %}` as "the exception". It is
not the exception: `{% filter %}`, block `{% set %}`, every macro body, every block render
and `self.x` all capture into a `strings.Builder`. The budget compensates by counting
captured bytes twice — a deliberate and well-argued choice (limits.go:104-110) — but nothing
bounds the *number* of live buffers, only the total bytes through them. A template nesting
`{% filter %}` inside a macro inside a block holds three copies of overlapping text, and the
depth that allows is `maxRecursion` = 100.

This is the tension with the least evidence of harm behind it — I could not build a case
where it bites that the byte budget did not already stop. It is here because the
documentation and the implementation disagree about what the engine's memory profile is, and
that disagreement is what a caller sizes a container from.

---

## 5. Expectation gaps

| I expected | I found |
|---|---|
| The nesting bound protects against stack exhaustion, as its comment says. | Two one-line templates kill the process by stack overflow (C2, C3), and the bound itself does not apply to `not`/unary/filter chains (C8). |
| `{% import %}` shares the render's budget, like `{% include %}` and `{% extends %}`. | It has *no* budget and no context (C1). divergences.md lists the two that work and is silent on the one that does not. |
| A panic anywhere becomes a render error — divergences.md, "A backstop on panics". | True for panics. A Go stack overflow is a `fatal error`, not a panic; `recover` cannot see it, and two templates reach it. |
| `make check` runs what CI runs, as both the Makefile and README say. | No `make lint` target exists; CI runs golangci-lint and a memory-cap job that `make check` does not (C24). |
| The "Test under a memory cap" CI step tests under a memory cap. | It replays the test cache and executes nothing (C12). |
| `SelectAutoescape` escaping *less* than jinja2 is treated as a bug — the code says so twice. | `SelectAutoescape("")` escapes nothing where jinja2 escapes names ending in a dot (C28). |
| `safeJoin` refuses names that escape the root, as its doc says. | It rewrites them, and the refusal loop is unreachable (C19). |
| README: structs "expose … their methods that take no arguments". | They are exposed as bound-method objects: `{{ h.Greet }}` renders `<bound method Greet>` and you must write `{{ h.Greet() }}`. This matches Python, but the sentence invites the first spelling and nothing says so. |
| A host method's error reaches the template author. | Only if the method returns two or more results (C20). |
| Built-in filters poll so a cancelled render stops promptly — divergences.md says so. | `urlencode` and `urlize` do. `unique`, `sort`, `min`, `max`, `groupby` and `value.Contains` do not (C9, C10). |
| `|tojson` produces valid JSON or an error. | A negative infinity silently truncates the document to a fragment (C5). |
| A caller-registered filter has the same duties as a built-in one. | `State.ChargeBytes` and `ChargeItems` are exported, load-bearing, and mentioned in neither the README nor `docs/`. Only `Poll` is documented. |
| Compiling a template is cheap and bounded. | A 1.6 MB template takes 34.5 s (C6); a 42-byte one OOM-kills the process (C4). `compile` takes no context. |

---

## 6. Open questions

Things the code alone cannot settle:

1. **Is `{{ self.x }}` rendering the block a deliberate divergence?** CPython prints
   `<jinja2.runtime.BlockReference object at 0x…>`. runtime.go:438 argues for rendering, and
   the argument is good, but it is not in docs/divergences.md and it is what makes C2
   reachable. Which behaviour is intended decides whether the fix is a depth guard or a
   removal.
2. **What is a caller-registered filter's contract?** `Step`, `Poll`, `ChargeBytes` and
   `ChargeItems` are all exported and all load-bearing. Is a third-party filter that ignores
   them a bug in the filter or a gap in this package? The answer belongs in the README either
   way.
3. **Should `FSLoader` refuse traversal or normalise it?** Both are defensible. The current
   code does one and documents the other.
4. **Was `%` deliberately left out of the fold budget**, or was `*` simply the operator that
   got reported first? `constBinOp` guards `OpMul` with a specific, commented check and
   `OpMod` with nothing; the shape suggests the latter, but the choice may have been made.
5. **Is the lazy-sequence divergence still the right trade** now that `|unique` turns out to
   be quadratic (C10)? The argument in divergences.md is about *semantics* and is
   convincing; the performance half of "anything that consumes the result behaves
   identically" is not true for `unique`, and a generator-shaped implementation would not
   have that problem.
6. **How much does compile-time cost matter for the intended deployment?** If templates are
   always trusted and compiled once at startup, C4 and C6 are hygiene. If `FromString` ever
   sees user input — which docs/scope.md's sandbox discussion implies it might — they are the
   two most serious findings in this report after C1.
7. **Is `errs.Frame` a feature someone started?** Building a real template traceback would be
   valuable and `errs.At` already stands at every frame boundary; leaving a dead exported type
   in place is the worst of the options.

---

## 7. Hypotheses that did not survive

Recorded so the next reader does not spend the time again.

- **Constant folding and `~` lose Markup.** `value.Concat` stringifies both sides and drops
  the safe flag, where `evalConcat` escapes each operand and returns Markup — so I expected
  `{{ ("<b>"|safe) ~ "x" }}` to fold to an escaped result. It does, and **so does jinja2**:
  `Concat.as_const` joins with `str()` and loses Markup identically. Verified against the
  oracle with autoescape on and off; no divergence.
- **`value.repeat`'s `int64(len(v.str))*n > MaxInt32` check overflows.** It does — `"xx" *
  2**62` wraps negative and would reach `make` with a negative capacity. It is unreachable:
  every caller pre-charges through `RepeatSize`, whose `saturatingMul` is correct (I checked
  the `a > MaxInt64/n` boundary algebraically and it never admits an overflowing product).
  `{{ "xx" * 4611686018427387904 }}` returns a clean budget error. Latent, not a defect.
- **Concurrent rendering races.** 8 goroutines × 200 renders over inheritance, include,
  import, macros and list mutation, plus 8 × 500 renders of one shared `*Template` that
  mutates a list and sorts: clean under `-race`.
- **The README's conformance table has drifted.** It has not: `TestConformance` reports
  2587/2591, exactly the table, and asserts it.
- **`moduleObject.GetAttr` compares `value.Value` with `==` and could panic on an
  uncomparable payload.** Every `obj` a `Value` can hold is a pointer or a comparable struct;
  I found no `FromObject` call with an uncomparable value. Fragile, not broken.
- **`{% for x in l %}{% set _ = l.append(1) %}{% endfor %}` loops forever**, as it does in
  Python. It does not: `runLoop` snapshots the slice header, so an append is invisible to the
  running loop. A divergence, but in the safe direction and not one I could make produce
  wrong output.
- **`tojson` of `+inf` is also broken.** Only the negative branch calls `Reset`.
