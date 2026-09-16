# gojja2 — adversarial codebase audit

**Date:** 2026-09-16
**Commit:** b7c96ca "Escape what |format substitutes into Markup, and pprint Markup on one line"
**Scope:** the whole repository — ~9,500 lines of root package, ~4,000 of `value/`,
~2,700 of `internal/`, ~1,800 of `conformance/`, plus `Makefile`, `README.md`,
`docs/`, and the committed corpora.
**Method:** every file read; critical paths traced end to end; every claim below
that says MEASURED was reproduced in an isolated harness outside the repository,
under `systemd-run --scope -p MemoryMax=… -p MemorySwapMax=0`, so that an
allocation bomb reports as exit 137 instead of taking the machine down.
Behavioural divergences were adjudicated against the pinned oracle
(`.venv`, CPython 3.11.15 / jinja2 3.1.6 / markupsafe 3.0.3), which this project
defines as the specification.

---

## 1. Summary

| ID | Severity | Area | Issue | Site | Status |
|---|---|---|---|---|---|
| C1 | Critical | limits / optimizer | Templates panic and OOM at **compile** time, where no budget, context or bound exists | optimize.go:562 | CONFIRMED |
| C2 | Critical | autoescape | `SelectAutoescape` leaves **string templates unescaped**; jinja2 escapes them | environment.go:172 | CONFIRMED |
| C3 | Critical | autoescape | `SelectAutoescape` is case-sensitive on extensions; jinja2 calls this a security property | environment.go:177 | CONFIRMED |
| C4 | Critical | Go bridge | Cyclic caller data OOMs **before** the budget is constructed | convert.go:71,126 + template.go:41 | CONFIRMED |
| C5 | High | filters | Template-chosen sizes allocate before anything is charged → OOM | filters_web.go:353, filters.go:1249, filters_seq.go:380,351 | CONFIRMED |
| C6 | High | filters / methods | Six one-line templates panic out of `Render`/`FromString` | methods.go:628,660-666; filters.go:477,1116 | CONFIRMED |
| C7 | High | globals | `lipsum`, a default global, panics four distinct ways | globals.go:344,346,352 | CONFIRMED |
| C8 | High | filters | Individually-sound guards do not compose → 3.6 GB from two legal repeats | methods.go:309 | CONFIRMED |
| C9 | High | methods | `dict.update` silently discards every non-dict argument — **data loss** | methods.go:840-855 | CONFIRMED |
| C10 | High | limits | A context deadline cannot interrupt a filter: 1 s deadline returned after 17.44 s | limits.go:527-548 | CONFIRMED |
| C11 | High | filters | `urlencode` is O(n²) on bytes below 0x10, incl. `\n` and `\t` | filters_web.go:60-80 | CONFIRMED |
| C12 | High | inheritance | `{{ self.block }}` swallows the block's error and renders `""` | runtime.go:407-413 | CONFIRMED |
| C13 | Medium | globals | `range` length overflows int64 → `|length` reports `-1` | globals.go:29-40 | CONFIRMED |
| C14 | Medium | methods | `center`/`ljust`/`rjust` accept a fill CPython rejects → silent wrong output | methods.go:646 | CONFIRMED |
| C15 | Medium | Go bridge | Every exported method is callable **with arguments**; docs say "nullary" | convert.go:188-191,256 | CONFIRMED |
| C16 | Medium | Go bridge | Pointer-receiver methods are silently invisible to templates | convert.go:99-103 | CONFIRMED |
| C17 | Medium | Go bridge | A panicking Go method panics the render | convert.go:271 | CONFIRMED |
| C18 | Medium | environment | Template cache is unbounded, never evicted, and cannot be invalidated | environment.go:63,281-305 | CONFIRMED |
| C19 | Medium | optimizer | A folded *filter* result is never size-checked and is retained forever | optimize.go:110-114,407 | CONFIRMED |
| C20 | Medium | rendering | `{% include %}` fully buffers, contradicting the streaming promise | exec.go:531 | CONFIRMED |
| C21 | Low | docs | README conformance table is stale: 2589/2585 documented, 2591/2587 actual | README.md:§Conformance | CONFIRMED |
| C22 | Low | hygiene | Three dead functions and one dead parameter, none caught by `make check` | call.go:83,233; tests.go:265,268 | CONFIRMED |
| C23 | Low | docs | The same comment written twice, back to back, in two wordings | filters.go:489-497 | CONFIRMED |
| C24 | Low | DX | No CI exists, though `make check` is labelled "Everything CI should run" | Makefile:§check | CONFIRMED |
| C25 | Low | API | `WithMaxRecursion(0)` means *default*; `WithMaxIterations(0)` means *unbounded* | environment.go:214,230,241 | CONFIRMED |
| C26 | Low | autoescape | No `disabled_extensions`/`default`; divergent default extension list | environment.go:170-185 | CONFIRMED |

**Counts:** 4 Critical, 8 High, 8 Medium, 6 Low — 26 findings, all CONFIRMED by
execution or by direct comparison against the pinned oracle. No finding in this
report is speculative; the handful of hypotheses that did not survive testing are
recorded in §7 rather than promoted.

### What is genuinely sound

Said once, as instructed, because it is substantial and it shapes the diagnosis:

- **The value layer is exemplary.** `saturatingMul` (ops.go:364), `repeat`'s
  `MaxInt32` cap (ops.go:380,394) and `estimatePowBits`/`maxPowBits`
  (ops.go:592) bound every template-chosen size *before* allocating, and each
  carries a comment explaining the attack it stops. `{{ 10 ** 2000000000 }}`
  returns a clean `OverflowError`.
- **Dict hashing is correct and carefully argued** — `hashFloat` folds `1`,
  `1.0` and `True` onto one slot, and `encodeKey` length-prefixes so tuple keys
  cannot collide by concatenation (dict.go:233-259).
- **The parser is depth-bounded.** MEASURED: 1,000,000 nested parens and 100,000
  nested lists both return `expression or statement nests deeper than 1000
  levels` rather than overflowing the stack — which in Go would be unrecoverable.
- **The error model works exactly as documented.** MEASURED:
  `errors.Is(err, errs.UndefinedError)` → true, and so is
  `errors.Is(err, errs.TemplateRuntimeError)` through the modelled Python class
  hierarchy; `errors.Is(err, ErrTooManyIterations)` and
  `errors.Is(err, context.Canceled)` likewise.
- **`__class__` is inert by design** (classes.go) — no `__subclasses__`,
  `__globals__` or `__builtins__` behind it, and the reasoning is written down.
- **The conformance apparatus is unusually honest**: goldens regenerated from
  CPython, a known-failures list that fails the build if an entry starts
  passing, and ungradable cases excluded with a stated reason. `go test ./...`
  passes on a clean tree.

The defects below are overwhelmingly *not* in the parts this project treats as
its subject matter. They cluster in the layer above it.

---

## 2. System map

### Real execution paths

```
                       ┌──────────────── COMPILE (no budget, no context) ───────────────┐
Environment.FromString │ parser.Parse → foldConstantExpressions → foldConstantPrints    │
Environment.GetTemplate│        ↓ (cache, unbounded)      ↑ RUNS REAL FILTERS ← C1      │
                       │ checkDependencies → collectBlocks → *Template                  │
                       └───────────────────────────────────────────────────────────────┘
                                             ↓
                       ┌──────────────── RENDER ───────────────────────────────────────┐
Template.Render        │ valuesFromGo(vars)  ← C4: runs BEFORE the budget exists        │
Template.RenderValues  │ newBudget(ctx, env) ← every documented bound starts HERE       │
                       │ renderInto → newState → exec.execBody                          │
                       │   exec.write → budget.account → tick → checkContext ← C10      │
                       │   runLoop    → budget.step    → tick → checkContext            │
                       └───────────────────────────────────────────────────────────────┘
```

The single most important structural fact in this codebase is the position of
that middle line. `newBudget` is called in `Template.RenderValues`
(template.go:63). Everything above it — template compilation, constant folding,
and the conversion of the caller's own Go values — runs with **no budget, no
context and no bound of any kind**. C1 and C4 are both consequences of that one
placement, and they are the two findings a reader should take away.

### Key invariants, and where they actually hold

| Invariant | Claimed | Enforced |
|---|---|---|
| A render cannot exceed `maxIterations` | environment.go:223 | Only in `budget.step`, reached from `runLoop`, `materialize`, `evalArgs`, `unpack`, `list.extend`. Not from any global, most filters, or compile time. |
| A render cannot exceed `maxOutputBytes` | environment.go:234 | Only in `exec.writeTo`. The allocation that produces the text is never bounded (C5, C8). |
| A render stops when the context is cancelled | README, template.go:37 | Only at `budget.tick`, every 4096 units. A filter that neither iterates nor writes is uninterruptible (C10). |
| Recursion is bounded | environment.go:50 | `State.enter`, correctly threaded through include/extends/macro. **Holds.** |
| Template names cannot escape the loader root | loader.go:341 | `safeJoin`, rejecting `..` after `path.Clean` and backslash normalisation. **Holds.** |
| Autoescaping follows the configured policy | environment.go:164 | Holds for *named* templates only (C2, C3). |

---

## 3. Findings

### C1 — Critical — Templates panic and OOM at compile time
**`optimize.go:562`**, with `template.go:179` and `environment.go:347`.

`constFilter` invokes real filters at compile time on constant arguments:

```go
out, err := fn(c.st, input, args)          // optimize.go:562
```

`c.st` is a `*State` with a nil budget — `State.Step` documents this explicitly:
*"A nil State reaches here from constant folding, which runs without a render and
so has no budget to charge."* The filter therefore runs with every safety control
switched off, and the results of §C5–C8 all become reachable from
`Environment.FromString` alone.

**Failure scenario.** A service accepts a user-supplied template and validates it
by compiling it — a natural and recommended thing to do, since `FromString`
returns a `TemplateSyntaxError` for bad input. No rendering, no context, no
variables:

MEASURED, `env.FromString(src)` and nothing else:

| `src` | result |
|---|---|
| `{{ "a"\|indent(-1) }}` | **COMPILE PANIC** `strings: negative Repeat count` |
| `{{ "a"\|center(4611686018427387904) }}` | **COMPILE PANIC** `makeslice: len out of range` |
| `{{ "10"\|int(0, 99999) }}` | **COMPILE PANIC** `invalid number base 99999` |
| `{{ 1.5\|round(2000000000) }}` | **OOM KILL**, exit 137 at `MemoryMax=512M` |

The environment was `New(WithMaxOutputBytes(4096), WithMaxIterations(10000))` —
the bounds were configured, and configured tightly. They are simply on the other
side of the failure. The README's claim that *"an attacker-supplied template
cannot spend the process"* is defeated before the render it describes begins.

**Recommended direction.** Constant folding is an optimisation; it must not be
able to fail worse than not folding. Give the fold a real budget (a small, fixed
one is fine — it is compile time, not render time) and recover panics from
`constFilter`/`constTest`, abandoning the fold on either. Both are ~10 lines and
strictly reduce behaviour to "compute it later, under the render's budget",
which is exactly what `maxFoldedConst` already does for `*` (optimize.go:413-425).

---

### C2 — Critical — `SelectAutoescape` leaves string templates unescaped
**`environment.go:172-185`.**

The pinned specification, `jinja2/utils.py`:

```python
def select_autoescape(enabled_extensions=("html","htm","xml"),
                      disabled_extensions=(), default_for_string=True, default=False):
    def autoescape(template_name):
        if template_name is None:
            return default_for_string          # ← True
```

gojja2 has no `default_for_string`. A template compiled with `FromString` has
name `""`, matches no suffix, and is therefore **not escaped**.

**Failure scenario.** The README's own quickstart configures
`WithAutoescapeFunc(SelectAutoescape(".html"))`. A developer later renders a
fragment from a string — an email body, a preview, a snippet — and every
user-controlled value in it is emitted raw.

MEASURED, `env = New(WithAutoescapeFunc(SelectAutoescape(".html")))`,
`vars = {"evil": "<script>alert(1)</script>"}`:

```
FromString(`{{ evil }}`)              -> "<script>alert(1)</script>"      UNESCAPED
FromNamedString("page.html", `{{ evil }}`) -> "&lt;script&gt;alert(1)&lt;/script&gt;"
```

Under CPython jinja2 the first case escapes. This is a cross-site-scripting
divergence from the project's own stated specification, in its own recommended
configuration.

**Recommended direction.** Add `default_for_string` (defaulting to `true`, as
jinja2 does) and have `escapes("")` consult it. Because gojja2 models an unnamed
template as `""` rather than `nil`, the distinction between "no name" and "a name
that matches nothing" needs to be made explicit rather than inferred from
emptiness.

---

### C3 — Critical — `SelectAutoescape` is case-sensitive on extensions
**`environment.go:177-181`.**

```go
lower := strings.ToLower(name)
for _, ext := range extensions {
        if strings.HasSuffix(lower, ext) {     // ext is NOT lowered
```

jinja2 lowers both sides — `tuple(f".{x.lstrip('.').lower()}" for x in …)` — and
its docstring states the reason outright: *"For security reasons this function
operates case insensitive."*

**Failure scenario.** A developer writes `SelectAutoescape(".HTML")`, or
`SelectAutoescape(".Html")`, or reads extensions from a config file that happens
to carry them uppercase. Escaping is silently off for every template. There is no
error and no warning; the only symptom is unescaped output.

MEASURED:

```
SelectAutoescape(".HTML") on "page.HTML" -> "<script>alert(1)</script>"   UNESCAPED
SelectAutoescape("html")  on "page.html" -> "&lt;script&gt;…"             escaped
```

jinja2 escapes in both cases. Note also that gojja2's bare `HasSuffix` matches
without a dot boundary, so `SelectAutoescape("tml")` matches `page.html` here and
would not there — over-matching, the safer direction, but still a divergence.

**Recommended direction.** Lower-case and dot-normalise the extensions once when
building the closure, as jinja2 does.

---

### C4 — Critical — Cyclic caller data OOMs before the budget exists
**`value/convert.go:71-83,126-141`, reached from `template.go:41`.**

`FromGo` converts maps and slices **eagerly** and has no cycle detection. Structs
are wrapped lazily, which is why this is easy to miss.

```go
func (t *Template) Render(ctx context.Context, w io.Writer, vars map[string]any) error {
        return t.RenderValues(ctx, w, valuesFromGo(vars))     // template.go:41
}
```

`valuesFromGo` is evaluated as an *argument*, so it completes before
`RenderValues` builds the budget (template.go:63). Cancellation, `WithMaxIterations`
and `WithMaxOutputBytes` are all structurally downstream.

**Failure scenario.** Any self-referential structure in the context — a graph, a
parent-pointer tree, a node that links back to its container, a config map that
references itself:

```go
m := map[string]any{"k": "v"}
m["self"] = m
tmpl.RenderString(ctx, map[string]any{"m": m})   // template is just {{ m.k }}
```

MEASURED: **OOM KILL, exit 137** at `MemoryMax=1G`. The template is one attribute
access; the data killed the process before the template was looked at.

**Recommended direction.** Convert containers lazily, as structs already are —
this also removes an eager deep copy of every map and slice on every render, which
is a performance win independent of the bug. If eager conversion is kept, carry a
`map[uintptr]Value` of visited containers and a depth cap. Separately, move
`valuesFromGo` inside `RenderValues` after `newBudget`, so conversion is charged
like everything else.

---

### C5 — High — Template-chosen sizes allocate before anything is charged
**`filters_web.go:353-354`, `filters.go:1249`, `filters_seq.go:380`, `filters_seq.go:351`.**

Four filters size an allocation directly from an integer the template chooses,
with no charge against the budget:

```go
pad    = strings.Repeat(" ", indent*(depth+1))          // filters_web.go:353  tojson
rounded = strconv.FormatFloat(f, 'f', precision, 64)     // filters.go:1249     round
for i := range count { … }                               // filters_seq.go:380  slice
for len(batch) < size { batch = append(batch, fill) }    // filters_seq.go:351  batch
```

MEASURED, each in its own capped scope, env =
`New(WithMaxOutputBytes(4096), WithMaxIterations(10000))`:

| template | result | CPython |
|---|---|---|
| `{{ [1]\|tojson(2000000000) }}` | **OOM KILL** 137 | renders |
| `{{ 1.5\|round(2000000000) }}` | **OOM KILL** 137 | `'1.5'` |
| `{{ []\|slice(100000000)\|length }}` | **OOM KILL** 137 | renders |
| `{{ [1]\|batch(100000000, 0)\|length }}` | **OOM KILL** 137 | renders |

`round` is the sharpest of these: CPython returns `1.5` instantly, and gojja2
formats a two-billion-digit decimal. `tojson`'s `indent*(depth+1)` can also
overflow `int` and reach `strings.Repeat` with a negative count, which panics.

**Recommended direction.** These are the same bug four times, and the codebase
already contains its fix in `eval.go:231` (`chargeRepeat`). Charge
`s.Step(n)`/`budget.account(n)` on the computed size *before* the allocation, and
clamp as `clampToInt` does. See design tension §4.2 for why patching four sites is
not sufficient.

---

### C6 — High — Six one-line templates panic out of the public API
**`methods.go:628,660-666`; `filters.go:477,1116`.**

`value/ops.go` guards `strings.Repeat`-shaped hazards rigorously; `methods.go`
and `filters.go` call the same primitives with unvalidated widths:

```go
return value.String(sign + strings.Repeat("0", width-n) + s)   // methods.go:628  zfill
return s + strings.Repeat(fill, missing)                       // methods.go:660  ljust
return strings.Repeat(fill, left) + s + …                      // methods.go:666  center
prefix = strings.Repeat(" ", width)                            // filters.go:477  indent
strconv.ParseInt(s, base, 64)                                  // filters.go:1116 int
```

MEASURED — a panic, not an error, escaping `FromString`/`Render`:

| template | gojja2 | CPython |
|---|---|---|
| `{{ "a".center(9223372036854775807) }}` | PANIC `makeslice: len out of range` | `MemoryError` |
| `{{ "a".ljust(4611686018427387904) }}` | PANIC `makeslice` | `MemoryError` |
| `{{ "1".zfill(4611686018427387904) }}` | PANIC `makeslice` | `MemoryError` |
| `{{ "a"\|center(4611686018427387904) }}` | PANIC `makeslice` | `MemoryError` |
| `{{ "a"\|indent(4611686018427387904) }}` | PANIC `makeslice` | `MemoryError` |
| `{{ "a"\|indent(-1) }}` | PANIC `strings: negative Repeat count` | **`'a'`** |
| `{{ "10"\|int(0, 99999) }}` | PANIC `invalid number base 99999` | **`'10'`** |

The last two matter most: CPython renders them successfully. A template that works
in production under jinja2 crashes the Go process that replaces it — which is
precisely the migration this project exists to support.

Note that `filterCenter` routes through `pad()`, which *does* guard the negative
case (`missing <= 0`), while `filterIndent` calls `strings.Repeat` directly and
does not. The guard exists; it is just not everywhere.

**Failure scenario.** A panic crosses `Render` and unwinds the caller's
goroutine. In an HTTP server whose handler has no `recover`, the process dies; in
one that does, a single template kills one request and leaves `bufio` state
half-flushed (`template.go:67` flushes only on the normal return path).

**Recommended direction.** Validate width/base arguments against CPython's own
behaviour — clamp negatives to zero (`indent`), return `ValueError` for a bad
base, and return `OverflowError`/`MemoryError` for a width past the budget. A
`recover` at the `Render` boundary is worth adding as a backstop, but it is not a
substitute: it would convert C1's compile-time OOM into nothing at all.

---

### C7 — High — `lipsum`, a default global, panics four ways
**`globals.go:319-357`.**

```go
paragraphs := make([]string, 0, n)                   // :344  n < 0 → panic
count := lo + rand.IntN(hi-lo)                       // :346  hi-lo overflow → panic
words := make([]string, 0, count)                    // :347  count < 0 → panic
text = strings.ToUpper(text[:1]) + text[1:] + "."    // :352  text == "" → panic
```

MEASURED, against the oracle:

| template | CPython | gojja2 |
|---|---|---|
| `{{ lipsum(1, true, 0, 1) }}` | `<p>.</p>` | **PANIC** `slice bounds out of range [:1] with length 0` |
| `{{ lipsum(1, true, 0, 0) }}` | `ValueError: empty range for randrange()` | **PANIC** same |
| `{{ lipsum(1, true, -5, -1) }}` | `<p>.</p>` | **PANIC** `makeslice: cap out of range` |
| `{{ lipsum(1, true, MIN_INT, MAX_INT) }}` | renders | **PANIC** `invalid argument to IntN` |
| `{{ lipsum(-1) }}` | `''` | **PANIC** `makeslice: cap out of range` |
| `{{ lipsum(100000000) }}` | renders (large) | **OOM KILL** 137 |

`{{ lipsum(1, true, 0, 1) }}` is not a hostile template. It reads as "one
paragraph of at most one word" and it crashes the process.

There is a causal chain worth naming: line 340 "repairs" an argument CPython
rejects —

```go
if hi <= lo { hi = lo + 1 }
```

— and that repair is exactly what manufactures the `count == 0` case that then
panics at line 352. Silently fixing up an invalid argument turned a clean
`ValueError` into a crash.

**Recommended direction.** Reject `hi <= lo` with `ValueError` as CPython does
(removing the panic's cause), clamp `n`/`lo`/`hi` to non-negative, use
`saturatingSub` for `hi-lo`, and guard `text == ""`. See design tension §4.1 for
why `lipsum` cannot charge the budget for the `n = 100000000` case.

---

### C8 — High — Individually-sound guards do not compose
**`methods.go:296-310`.**

```go
return value.String(strings.Replace(r.AsString(), old, new, count))
```

MEASURED, **default environment** (256 MiB output budget, 10M iterations):

```
{{ ("a" * 60000)|replace("a", "b" * 60000) }}   ->  OOM KILL, exit 137 at MemoryMax=1G
```

Every existing guard passes. Each `*` is charged by `chargeRepeat`. Each result is
60,000 bytes — under `maxFoldedConst` (65,536), so both fold. And their product,
3.6 GB, is charged by nothing, because no guard is looking at the *combination*.

This is the most instructive finding in the report: it shows that the
site-by-site approach to bounding (§4.2) fails even when every site it covers is
individually correct.

**Recommended direction.** Charge `len(s)/len(old) * len(new)` before the call.
More generally, see §4.2 — the durable fix is a budget-aware string builder that
every filter writes through, so size is charged at the point of growth rather than
at each author's discretion.

---

### C9 — High — `dict.update` silently discards non-dict arguments
**`methods.go:840-855`.**

```go
if other, ok := arg(args, 0, ""); ok {
        if od, ok := other.Dict(); ok {      // ← anything else falls through
                …
        }
}                                            // ← and is discarded, with no error
```

MEASURED against the oracle:

| template | CPython | gojja2 |
|---|---|---|
| `{% set d={} %}{{ d.update([("a",1)]) }}{{ d }}` | `None{'a': 1}` | **`None{}`** |
| `{% set d={} %}{{ d.update(5) }}{{ d }}` | `TypeError: 'int' object is not iterable` | `None{}` |
| `{% set d={} %}{{ d.update("ab") }}{{ d }}` | `ValueError: dictionary update sequence element #0 has length 1; 2 is required` | `None{}` |

The first row is **silent data loss** — the worst outcome for a project whose
thesis is byte-identical output. A template that builds a dict from a list of
pairs produces an empty dict, renders successfully, and reports nothing.

There is an internal inconsistency here too: `globalDict` (globals.go:171-189)
handles the iterable-of-pairs form correctly, with a comment explaining it. Two
places in this codebase build a dict from pairs; only one of them works.

**Recommended direction.** Reuse `globalDict`'s pair-walking path in
`methodDictUpdate` — the correct implementation already exists twenty lines away.

---

### C10 — High — A context deadline cannot interrupt a filter
**`limits.go:527-548`.**

The context is consulted only from `budget.tick`, which is reached only from
`step()` and `account()`. A filter that neither iterates nor writes never reaches
it, and nothing else polls.

MEASURED, deadline = **1 second** in both cases:

| template | returned after | error |
|---|---|---|
| `{% for i in range(100000000) %}{% endfor %}` | 0.00 s | `render exceeded 1000 loop iterations` |
| `{{ ("\n" * 300000)\|urlencode }}` | **17.44 s** | `render wrote more than 1048576 bytes of output` |

The second overran its deadline by more than 17×, and the error it eventually
returned was the *output* budget — evaluated after the filter had already
finished — not the deadline. The deadline never fired at all.

The README states: *"Every render takes a `context.Context` and stops when it is
cancelled."* `Template.Render`'s doc is more precise — *"stops at the next loop
iteration or output write"* — and is technically accurate, but neither conveys
that a single filter call is an unbounded, uninterruptible region.

**Recommended direction.** Document the granularity honestly in the README, and
give long-running filters a way to yield — `State.Step` already exists and is the
natural hook; `quoteURL` and its peers should call it per chunk. A caller who
needs a hard deadline currently has no way to get one, which is worth saying in
`docs/divergences.md`.

---

### C11 — High — `urlencode` is quadratic on bytes below 0x10
**`filters_web.go:60-80`.**

```go
b.WriteByte('%')
b.WriteString(strings.ToUpper(strconv.FormatUint(uint64(c), 16)))
if c < 0x10 {
        text := b.String()        // ← copies the ENTIRE buffer …
        b.Reset()
        b.WriteString(text[:len(text)-1] + "0" + text[len(text)-1:])   // … and again
}
```

To insert one leading zero, the whole accumulated string is copied twice. `\n`
(0x0A) and `\t` (0x09) are below 0x10, so ordinary text triggers it.

MEASURED, `{{ ("\n" * N)|urlencode }}`:

| N | time |
|---|---|
| 5,000 | 0.009 s |
| 10,000 | 0.021 s |
| 20,000 | 0.072 s |
| 40,000 | 0.256 s |

Time quadruples as N doubles — clean O(n²). Control, identical output length but
a byte at or above 0x10: `{{ (" " * 40000)|urlencode }}` → **0.001 s**, 256×
faster.

**Failure scenario.** A template urlencoding a multi-line user field. At N = 10⁶
this is ~160 s of CPU for 3 MB of output — far under the 256 MiB output budget,
touching the iteration budget not at all, and (per C10) uninterruptible.

**Recommended direction.** One line: `fmt.Fprintf(&b, "%%%02X", c)`, or write the
two hex digits directly. No buffer rewriting.

---

### C12 — High — `{{ self.block }}` swallows the block's error
**`runtime.go:407-413`.**

```go
func (b *blockReference) Str() string {
        v, err := b.render()
        if err != nil {
                return ""          // ← the error is discarded
        }
        return value.Str(v)
}
```

MEASURED:

```
[{{ self.b }}]{% if false %}{% block b %}{{ 1/0 }}{% endblock %}{% endif %}
  gojja2 -> out="[]"  err=<nil>
```

A `ZeroDivisionError` vanished, the render reported success, and the output is
silently short. jinja2's `BlockReference.__str__` propagates the exception.

This is the only swallowed error of its kind in the tree — a sweep for
`if err != nil { return "" }` across all packages returns this one site — so it
is an isolated defect rather than a pattern, and correspondingly cheap to fix.

**Recommended direction.** `Str()` cannot return an error, which is the root of
the problem. Either render the block eagerly where an error can still propagate,
or carry the failure on the `blockReference` and surface it at the next point that
can return an error. The interface constraint is the real finding; the `return ""`
is its symptom.

---

### C13 — Medium — `range` length overflows int64
**`globals.go:29-40`.**

```go
return int((r.stop - r.start + r.step - 1) / r.step)
```

For `range(-9223372036854775808, 9223372036854775807)` the numerator overflows and
wraps negative.

MEASURED:

| expression | gojja2 | CPython |
|---|---|---|
| `range(MIN, MAX)\|length` | **`-1`** | `18446744073709551615` |
| `range(MIN, MAX)\|list\|length` | `0` | (huge) |
| `{% for i in range(MIN, MAX) %}x{% endfor %}` | 0 iterations | (huge) |

A negative `Len()` is worse than a wrong number: it reaches
`make([]T, 0, n)`-shaped call sites in `objectSource` consumers. Nothing found
reaches one today, but the invariant "`Len()` is non-negative" is assumed
throughout `runtime.go` and is not guaranteed here.

**Recommended direction.** Compute the length in `big.Int`, or detect overflow and
saturate at `math.MaxInt`. Given the iteration budget bounds the loop anyway,
saturating is sufficient and keeps `loop.length` monotone.

---

### C14 — Medium — `center`/`ljust`/`rjust` accept a fill CPython rejects
**`methods.go:646`.**

```go
if v, ok := arg(args, 1, "fillchar"); ok && v.Kind() == value.KindString {
        fill = v.AsString()          // any length accepted; non-strings ignored
}
```

MEASURED against the oracle:

| template | CPython | gojja2 |
|---|---|---|
| `{{ "a".center(10, "ab") }}` | `TypeError: The fill character must be exactly one character long` | **`"ababababaababababab"`** (19 chars) |
| `{{ "a".center(10, 5) }}` | `TypeError: The fill character must be a unicode character, not int` | **`"    a     "`** |

The first is silently wrong output at the wrong *length* — a `center(10)` that
returns 19 characters. A template laying out fixed-width text produces corrupt
output with no error.

**Recommended direction.** Raise `TypeError` with CPython's wording for a
non-string fill and for a fill whose length in code points is not 1. Note that
`pad` measures with `value.StrLen` (code points) already, so the check should too.

---

### C15 — Medium — Every exported method is callable, with arguments
**`value/convert.go:188-191`, `256-283`.**

```go
// Methods with no arguments and one result read as attributes.
if m := o.rv.MethodByName(name); m.IsValid() {          // ← filters on neither
```

The comment describes a restriction the code does not implement, and the README
repeats it: *"structs expose their exported fields … and their nullary methods."*
`methodObject.Call` accepts any arity (`t.NumIn() != len(args.Pos)` is an arity
*match*, not a limit of zero).

MEASURED, value-receiver struct in the context:

```
{{ user.Secret() }}    -> "s3cr3t-token"
{{ user.Wipe(true) }}  -> "wiped=true"        ← arguments accepted, method mutates
{{ user.Secret }}      -> "<bound method Secret>"
```

**Failure scenario.** A host passes a domain object to a template — the ordinary
use of this library. Every exported method on it is template-callable, including
ones with side effects, with arguments the template chooses. If template authors
are less trusted than Go authors (the usual arrangement, and the reason jinja2
ships `SandboxedEnvironment`), this is a privilege boundary that neither the docs
nor `docs/scope.md` acknowledges. See design tension §4.4.

**Recommended direction.** Decide the intended contract and make code and docs
agree. If "nullary, one result" is the intent, enforce `t.NumIn() == 0 &&
t.NumOut() >= 1` at lookup. If arbitrary methods are intended, say so prominently
and provide an opt-in allow-list.

---

### C16 — Medium — Pointer-receiver methods are silently invisible
**`value/convert.go:99-103`.**

```go
case reflect.Pointer, reflect.Interface:
        return fromReflect(rv.Elem())     // ← the pointer's method set is dropped
```

The pointer is dereferenced before the struct is wrapped, so `*T`'s method set —
which in Go is where methods usually live — never reaches `structObject`.

MEASURED, identical templates:

| receiver | context value | `{{ user.Secret() }}` |
|---|---|---|
| `func (a *Account) Secret()` | `&Account{}` | `'Account object' has no attribute 'Secret'` |
| `func (a ValAccount) Secret()` | `ValAccount{}` | `"s3cr3t-token"` |

So the documented feature is absent exactly where Go programmers will expect it,
and present where they may not want it (C15). Which methods a template can call
depends on receiver style — a detail invisible from the template and undocumented.

**Recommended direction.** Retain the original `reflect.Value` (or its address)
when wrapping a struct, so `MethodByName` sees the pointer's method set. This
interacts with C15: fixing C16 *widens* the exposure C15 describes, so the two
should be resolved together and in that order.

---

### C17 — Medium — A panicking Go method panics the render
**`value/convert.go:271`.** `out := m.fn.Call(in)` has no `recover`.

MEASURED: a context method whose body panics → `{{ user.Boom() }}` propagates
`method exploded` out of `RenderString`.

A method reached from a template is being called with template-chosen arguments
(C15), so it can be driven into states its author never tested — a nil map write,
an index out of range, a failed type assertion.

**Recommended direction.** Recover around `m.fn.Call` and convert the panic into a
template error naming the method.

---

### C18 — Medium — The template cache is unbounded and cannot be invalidated
**`environment.go:63`, `281-305`.**

```go
cache map[string]*Template          // no bound, no eviction, no API to clear
```

The pinned jinja2 defaults, read from `.venv`: `cache_size = 400` (a bounded LRU)
and `auto_reload = True`. gojja2 has neither, and exposes no `ClearCache`,
`Invalidate` or `Reload` — the full method set on `*Environment` is `Policies`,
`AddFilter`, `AddTest`, `AddGlobal`, `Globals`, `FromString`, `FromNamedString`,
`GetTemplate`, `SelectTemplate`.

Two consequences:

1. **Growth.** Every distinct name ever loaded is retained for the life of the
   process. With `{% include %}` over an attacker-influenced name and a loader
   that can serve many, memory grows without bound. This compounds C19: folded
   constants live in the cached tree.
2. **Staleness.** The README's own example — `FSLoader{FS: os.DirFS("templates")}`
   — will never observe an edit to a template file. There is no supported way to
   make it, short of discarding the whole `Environment`.

**Recommended direction.** A bounded LRU with a `WithCacheSize` option, plus a
`ClearCache()`. Auto-reload needs a loader-level modification time, which is a
larger change; documenting the absence is the minimum.

A related but benign observation: two goroutines calling `GetTemplate` for the
same uncached name will both compile it, and the second overwrites the first. The
work is wasted but the result is correct, and the locking is otherwise sound.

---

### C19 — Medium — A folded filter result is never size-checked
**`optimize.go:110-114`, `407`.**

`constSizeOK` is applied only inside `constBinOp` (optimize.go:407). The general
fold path checks `foldable(v)` — kind only, not size:

```go
if v, ok := f.c.constEval(e); ok && foldable(v) {
        return &ast.Const{Pos: ast.At(e.Line()), Value: v}
}
```

So `{{ "x"|center(10000000) }}` bakes a 10 MB constant into the compiled
`*Template` at compile time, where no budget exists (C1), and — because the
template is cached forever (C18) — retains it for the life of the process.

`maxFoldedConst` exists and its comment explains precisely this hazard for `*`.
It simply is not consulted on the filter path.

**Recommended direction.** Apply `constSizeOK` in `fold()` rather than only in
`constBinOp`, so it covers every folded result regardless of which evaluator
produced it.

---

### C20 — Medium — `{% include %}` fully buffers, contradicting the streaming promise
**`exec.go:531-541`.**

```go
var buf strings.Builder
if err := tmpl.renderInto(&buf, vars, ex.st.depth, ex.st.budget); err != nil {
```

README: *"Output is streamed to `w` as the template produces it."*
`Template.Render`'s doc repeats it. An `{% include %}` materialises the entire
included render in memory first.

The buffering is deliberate — a context-free include must write into the enclosing
function's stream (exec.go:535-540), which requires having the text in hand — and
the double-charging against the budget is documented at limits.go:506-512. The
finding is the **documentation**, which promises streaming without qualification:
a page built from a dozen includes holds each one whole, and peak memory tracks
the largest include rather than the write buffer.

**Recommended direction.** Qualify the README sentence. The `with context` case
could stream directly into `ex.out` without changing semantics, if the memory
matters.

---

### C21 — Low — The README conformance table is stale
**`README.md`, §Conformance.**

MEASURED — `go test ./conformance/... -run TestConformance -v` on this checkout:

```
conformance: 2587/2591 gradable cases match CPython jinja2 (99.8%);
             4 known divergence(s), 4 ungradable
```

The README's table totals **2589 gradable / 2585 matching**. Reconciling row by
row against the case files on disk and the 4 ungradable entries in
`known_failures.txt` (1 minijinja, 3 minja):

| corpus | README | on disk | gradable | agrees? |
|---|---|---|---|---|
| gojja2's own (committed) | 269 | 271 | 271 | **no, −2** |
| MiniJinja fixtures | 159 | 160 | 159 | yes |
| Jinja's own suite | 658 | 658 | 658 | yes |
| minja | 162 | 165 | 162 | yes |
| llama.cpp | 281 | 281 | 281 | yes |
| chat templates | 810 | 810 | 810 | yes |
| theme | 84 | 84 | 84 | yes |
| cookiecutter | 166 | 166 | 166 | yes |

The drift is confined to the first row: two cases were added to the committed
corpus without updating the table. Both pass, so the matching column moved with
it. The percentage is unchanged at 99.8%.

This is minor in itself, but the first row is the one a reader can verify on a
fresh checkout with no network and no Python — the row the README singles out for
exactly that reason — which makes it the one most worth keeping exact.

**Recommended direction.** Have the conformance test emit the table, or add a
`make` target that regenerates it, so the numbers cannot drift silently.

---

### C22 — Low — Dead code and a dead parameter
- `call.go:233` `isDeclared` — defined, **zero** callers.
- `tests.go:265` `stringOf` — zero callers.
- `tests.go:268` `joinStrings` — zero callers, and its comment asserts
  *"is used by filters that assemble output from parts."*
- `call.go:83` `invoke(callee, args, at ast.Expr)` — `at` is never read, and the
  doc comment says *"The node is only used to name the callee in an error."*

`stringOf` and `joinStrings` also sit in `tests.go`, which implements jinja2
*tests*; they are string helpers that belong elsewhere if they belong anywhere.

**Recommended direction.** Delete all four. See C24 — a linter would have.

---

### C23 — Low — The same comment written twice
**`filters.go:489-497`.** Two consecutive comment blocks explain jinja2's
`s += newline` / `splitlines()` behaviour in two different wordings, the second
apparently a revision of the first that was added rather than substituted. Nine
lines where four were intended.

---

### C24 — Low — No CI exists
`make check` is defined as `fmt-check vet test` and its help text reads
*"Everything CI should run"* — but there is no `.github/`, no workflow file, and
no linter configuration anywhere in the tree.

Consequences: the conformance guarantee the README relies on (*"a case on that
list which starts passing fails the test, so the list can only shrink
deliberately"*) holds only if a human remembers to run it; C21 is the observable
result. `go vet` alone catches none of C22.

**Recommended direction.** A workflow running `make check` plus
`go test ./conformance/...`, and `staticcheck` or `golangci-lint` with the
`unused` pass enabled.

---

### C25 — Low — `0` means opposite things across one option family
- `WithMaxRecursion(0)` → **restores the default** of 100 (environment.go:214-221).
- `WithMaxIterations(0)` → **removes the bound** (environment.go:230).
- `WithMaxOutputBytes(0)` → **removes the bound** (environment.go:241).

Each is documented at its own definition, so a reader who consults the right
doc-comment is not misled. But the three read as a family, and the value that
means "be safe, use the default" in one means "disable the safety control" in the
other two. A zero-valued config struct — the ordinary way this gets wired from
YAML or flags — silently disables two of three bounds.

**Recommended direction.** Make `0` mean "default" for all three and require an
explicit negative (or a `WithNoLimits()`) to disable, so the quiet path is the
safe one.

---

### C26 — Low — `SelectAutoescape` is missing half its specification
Beyond C2 and C3, gojja2's `SelectAutoescape` has no `disabled_extensions` and no
`default` parameter. jinja2's docstring documents a configuration that is
therefore inexpressible here:

```python
select_autoescape(disabled_extensions=('txt',), default_for_string=True, default=True)
```

— "escape everything except `.txt`", the safe-by-default posture. In gojja2 a
caller wanting it must hand-write the closure, at which point `SelectAutoescape`
offers nothing.

The default extension list also differs: gojja2 uses `.html, .htm, .xml, .xhtml`,
jinja2 uses `html, htm, xml`. gojja2's is the safer set, but it is still a
divergence from the stated specification and is not recorded in
`docs/divergences.md`.

---

## 4. Design tensions

### 4.1 — The extension point for globals cannot be made safe

```go
func Func(name string, fn func(args *value.CallArgs) (value.Value, error)) value.Value
type Filter func(s *State, v value.Value, args *value.CallArgs) (value.Value, error)
```

A `Filter` receives `*State` and can call `s.Step(n)`. A global receives only
`*CallArgs`. **A global therefore cannot charge the budget — not by oversight, but
by signature.** Every global is structurally exempt from `WithMaxIterations` and
`WithMaxOutputBytes`, and so is every global a *user* registers with `AddGlobal`.
C7's `{{ lipsum(100000000) }}` cannot be fixed without changing this type.

*The alternative I would weigh:* give globals the same `*State`-first signature as
filters. It is a breaking change to a pre-1.0 API, and the cost is real — every
`AddGlobal` caller updates. The version to do it in is this one. A non-breaking
half-measure (a parallel `StatefulFunc`) leaves the unsafe spelling as the obvious
one, which is how the current situation arose.

### 4.2 — Bounding is a site-by-site habit, not a rule

`eval.go:219-240` is worth reading in full. `chargeRepeat` exists, and its comment
records the incident that produced it:

> *"`{{ "x" * 1000000000 }}` took the process down with an output budget of four
> kilobytes in force."*

The lesson was learned, written down, and applied **to one operator**. Meanwhile
`tojson`, `round`, `slice`, `batch`, `lipsum`, `indent`, `center`, `ljust`,
`zfill` and `replace` all still allocate from a template-chosen size and charge
nothing (C5–C8). The same is true of `materialize`, `evalArgs` and `unpack`, which
*are* charged — each after its own incident, each in its own idiom.

C8 is the proof that this cannot converge: `("a"*60000)|replace("a","b"*60000)`
passes *every* guard that exists and still allocates 3.6 GB, because the guards
bound individual operations and the hazard is in their composition.

*The alternative I would weigh:* stop charging at call sites and make the budget
structural. A `budget.Builder` — a `strings.Builder` that charges on every write
and returns an error past the bound — handed to every filter through `*State`,
turns "remember to charge" into "you cannot allocate output without charging". The
cost is touching every filter once; the benefit is that the next filter someone
adds is bounded by construction. The present design requires every future
contributor to independently rediscover the comment at eval.go:219.

### 4.3 — The budget is a render concept, but compile time does real work

`newBudget` lives in `RenderValues`. Yet `Environment.compile` runs the optimizer,
which runs **real filters** (C1), and `Template.Render` runs `valuesFromGo`, which
walks **the caller's whole object graph** (C4). Both are unbounded, and both are
positioned so that no option or context the caller sets can reach them.

The architecture treats "render" as the unit of work to be bounded. The actual
units of attacker-influenced work are three: *compile a template*, *convert a
context*, *render*. Only the last has a budget.

*The alternative I would weigh:* make the budget an `Environment`-level facility
with a compile-time allowance and a per-conversion allowance alongside the render
allowance. The narrower fix — a fixed internal budget for folding, a cycle check
in `FromGo` — closes C1 and C4 and is what I would do first; but it leaves the
conceptual gap, and the next thing added at compile time will fall into it again.

### 4.4 — The sandbox is declined for a reason the bridge contradicts

`docs/scope.md` excludes the sandbox, and argues it well:

> *"`SandboxedEnvironment`, attribute allow-lists, and the unsafe-callable
> machinery are about restricting access to *Python* objects. Go's own type system
> draws that line differently, so a faithful port would be a false reassurance."*

The premise is that Go's type system already draws the line. It does not.
`structObject.GetAttr` reaches `MethodByName` on any exported method, and
`methodObject.Call` invokes it with template-chosen arguments (C15). Reflection
erases exactly the boundary the argument leans on.

The conclusion may still be right — a Python-shaped sandbox would indeed be a
false reassurance in Go. But the stated reason is not the true one, and a reader
making a trust decision from `scope.md` will conclude that passing a struct to a
template is as safe as passing it to `encoding/json`. It is not: `json` reads
fields, templates call methods.

*The alternative I would weigh:* an opt-in allow-list at the bridge —
`WithExposedMethods(...)`, or honouring a `gojja2:"expose"` tag — which is a small,
Go-shaped mechanism rather than a port of jinja2's. Failing that, `scope.md`
should say plainly that template authors are trusted to the same level as Go
authors, and the README should carry that where people configuring a loader will
see it.

### 4.5 — Two layers, two standards

Everything in §1's "genuinely sound" list is in `value/` and `internal/`.
Everything in §3's Critical and High rows is in the root package's filter, method
and global layer.

`value/ops.go` bounds `repeat` at `MaxInt32`, saturates its multiplication, and
estimates `**`'s bit width before computing it. `methods.go`, forty lines of call
stack away, hands an unvalidated `int64` straight to `strings.Repeat`.

The difference is not competence — it is the same author — it is that the value
layer was built as a model, with invariants, while the filter layer was built as a
catalogue, one entry at a time, each mapped to its CPython counterpart. The
catalogue is graded rigorously for *output* by the conformance corpus, and not at
all for *resource behaviour*, because CPython's answer to
`{{ 1.5|round(2000000000) }}` is `1.5` and gojja2's is a dead process — which no
output-comparison harness can see.

*The alternative I would weigh:* extend the differential harness to compare
*resource outcomes*, not just bytes. The soak infrastructure already runs the
oracle "under a memory cap and a per-render timeout" (README); applying the same
cap to the gojja2 side and flagging any case where CPython returns and gojja2 does
not would have found C5, C6, C7 and C8 automatically. That is the highest-leverage
single change in this report: it converts an entire defect class from "found by
audit" to "found by CI".

---

## 5. Expectation gaps

| # | Expected | Found |
|---|---|---|
| 1 | `FromString` parses and returns an error for bad input | It executes filters, and can panic or exhaust memory (C1) |
| 2 | Configured limits bound the work a template can cause | They bound only the render, which is the third of three unbounded phases (C1, C4, §4.3) |
| 3 | `SelectAutoescape` is the safe default, as the README implies | It silently disables escaping for string templates and uppercase extensions (C2, C3) |
| 4 | Passing data to a template is inert, like `json.Marshal` | A cyclic map kills the process before the template runs (C4) |
| 5 | Structs expose "their nullary methods" (README) | Every exported method, with arguments — but only value receivers (C15, C16) |
| 6 | A cancelled context stops the render | It stops at the next loop iteration or write; a filter is uninterruptible (C10) |
| 7 | Output is streamed to `w` (README) | Except `{% include %}`, which buffers each included render whole (C20) |
| 8 | A render either succeeds or returns an error | It may panic, from seven distinct sites (C6, C7, C17) |
| 9 | `dict.update` behaves like Python's | Non-dict arguments are silently discarded (C9) |
| 10 | `{{ self.block }}` renders the block or fails | It renders `""` and reports success (C12) |
| 11 | The conformance table reflects the suite | Stale by two cases, in the one row verifiable offline (C21) |
| 12 | `make check` is what CI runs | There is no CI (C24) |
| 13 | A newcomer can build and test from the docs | True — `make venv`, `make test` work as written. The undocumented requirement is Go **1.26.5** exactly, from `go.mod`'s patch-level directive; the README's Development section does not mention a toolchain version. |

---

## 6. Open questions

These cannot be settled from the code alone:

1. **Who writes the templates?** Nearly every Critical and High finding is
   severity-weighted by the answer. If templates are trusted first-party assets,
   C1/C5–C8 are robustness bugs. If they are user-supplied — which the README's
   *"an attacker-supplied template cannot spend the process"* promises support for
   — they are remote denial-of-service, and C15 is privilege escalation.
2. **Is the budget a safety boundary or a backstop?** limits.go:427-433 calls the
   defaults "backstops … for a caller who passes `context.Background()`", but
   environment.go:50-59 and the README call them safety controls against attacker
   templates. Those imply different fixes: a backstop tolerates the gaps in §4.3;
   a safety boundary does not.
3. **Is the `Environment`/`Func` API frozen?** §4.1's fix is breaking. Whether
   that is acceptable determines whether globals can ever be bounded.
4. **Was the pointer-receiver behaviour (C16) a decision or an accident?** Nothing
   in the code or docs indicates intent, and the answer changes whether the fix is
   "expose pointer methods" or "document that only value receivers are exposed".
5. **Why is there no CI** when the Makefile anticipates it? A deliberate choice
   pending a public repository is different from an oversight, and only the latter
   is a finding worth acting on urgently.

---

## 7. Hypotheses that did not survive

Recorded so they are not re-investigated, and because two of them are where the
codebase is stronger than it first appears:

- **Parser stack overflow from deep nesting** — refuted. Bounded at 1000 levels;
  1,000,000 nested parens returns a clean error.
- **`errors.Is(err, errs.UndefinedError)` cannot compile**, since `Kind` is a
  `uint8` constant — refuted. `errs.go:162` gives `Kind` an `Error()` method;
  the README idiom compiles and returns `true`, including through the modelled
  class hierarchy.
- **`{{ 10 ** 2000000000 }}` OOMs** — refuted. `estimatePowBits` rejects it with
  `OverflowError` before computing.
- **Path traversal via `{% include %}`** — refuted. `safeJoin` normalises
  backslashes, `path.Clean`s, and rejects any remaining `..` segment.
- **`__class__` opens a sandbox escape** — refuted. `classObject` exposes only
  `__name__`, `__qualname__` and `__module__`; there is no route onward.
- **Comparing `value.Value` with `==` panics on uncomparable payloads**
  (exec.go:665, tests.go:211) — not reachable today: every `obj` payload a
  template can construct is a pointer. It remains a latent hazard for a
  user-supplied `Object` implemented on an uncomparable type, but no repro
  exists, so it is not filed as a finding.
- **`{{ "x" * 1000000000 }}` OOMs** — refuted, and worth noting as the one place
  this exact hazard *is* handled: `chargeRepeat` catches it. That it is handled
  here and nowhere else is finding §4.2.

---

## 8. Suggested order of work

Grouped by what each change costs, not by severity alone:

1. **C2, C3** — autoescaping. Two small, self-contained fixes to a security
   property, with the specification already written down in `.venv`.
2. **C1** — recover panics and add a fixed budget in `constFilter`/`constTest`.
   Small, and it closes the compile-time exposure for the whole filter catalogue
   at once rather than filter by filter.
3. **C4** — move `valuesFromGo` inside `RenderValues`, add cycle detection.
4. **C9, C12, C14** — silent wrong output. Cheap, and they are the failures this
   project's own thesis says matter most.
5. **C6, C7, C11** — the individual panics and the quadratic. Mechanical.
6. **§4.5** — extend the differential harness to compare resource outcomes. This
   is the change that stops the class from recurring; everything above it is
   cleanup that the harness would have found on its own.
7. **C5, C8, §4.1, §4.2** — the structural budget work, once the harness can tell
   you whether it worked.
