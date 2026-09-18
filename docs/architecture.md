# How a template gets rendered

This is the map you want before changing anything. It covers the two paths a
template takes — compile, then render — the five ways a render nests into
another one, and the rule every allocation obeys.

It is deliberately short on prose. The shapes are the point: two of the four
Critical findings in the [2026-09-16 audit](audits/codebase-audit-2026-09-16.md)
were *shape* bugs, and both were found by drawing the second diagram below as a
table and noticing which rows disagreed.

## The packages

```mermaid
flowchart TD
    subgraph pub["github.com/mgilbir/gojja2 — the public API"]
        API["Environment · Template · State<br/>Loader · Filter · Test · Option"]
    end
    subgraph int["internal/ — not importable"]
        LEX["lexer<br/><i>source → []Token</i>"]
        PAR["parser<br/><i>[]Token → ast.Template</i>"]
        AST["ast<br/><i>node set mirroring jinja2's nodes</i>"]
    end
    VAL["value/<br/><i>the value model: Python's, not Go's</i><br/>Value · Dict · Seq · Undefined · ops · repr"]
    ERR["errs/<br/><i>the CPython exception classes,<br/>with their hierarchy</i>"]
    CONF["conformance/<br/><i>corpus grading, generator,<br/>differential fuzzer, shrinker</i>"]
    ORA["tools/oracle/<br/><i>CPython jinja2, driven from the Makefile</i>"]

    API --> LEX --> PAR --> AST
    API --> VAL
    API --> ERR
    VAL --> ERR
    CONF -.->|grades| API
    CONF -.->|asks| ORA

    classDef spec fill:#dbeafe,stroke:#1d4ed8,color:#000
    class ORA spec
```

`value/` and `errs/` are importable because a caller writing a filter or a custom
object needs them. `internal/` is not: the token stream and the parse tree are
graded against jinja2's own, which makes them a specification to conform to
rather than an API to depend on.

## Compiling

```mermaid
flowchart TD
    A["GetTemplate(name)"] --> C{"in the LRU?"}
    C -->|yes| HIT(["the cached *Template"])
    C -->|no| L["loader.Load(name)"]
    L --> CO
    B["FromString(source)<br/>FromNamedString(name, source)"] --> CO

    subgraph CO["compile() — environment.go:578, under defer catchPanic"]
        direction TB
        P1["parser.Parse<br/><i>lexer.Tokenize: whole source, all tokens up front</i><br/><i>then recursive descent, jinja2 precedence</i>"]
        P2["foldConstantExpressions<br/><i>the general fold, as jinja2's optimizer does</i>"]
        P3["foldConstantPrints<br/><i>print tags only; also accepts undefined results</i>"]
        P4["checkDependencies<br/><i>unknown filter/test names</i>"]
        P5["collectBlocks<br/><i>index by name, refuse duplicates</i>"]
        P1 --> P2 --> P3 --> P4 --> P5
    end
    CO --> T(["*Template — tree + blocks"])
    T -->|"GetTemplate only, on success"| PUT["cache.put"]

    classDef warn fill:#fde68a,stroke:#b45309,color:#000
    class P2,P3 warn
```

Two things about this path catch people out.

**Compilation takes no `context.Context` and has no budget.** The bounds in
[limits.md](limits.md) are *render* bounds. What protects compile time instead is
the parser's 1,000-level nesting cap and the optimizer's refusal to fold a
constant over 64 KiB — the amber boxes run real filters, so they are the part
that can do arbitrary work.

**Only `GetTemplate` caches**, and only on success. `FromString` compiles every
time, which is why it is the wrong thing to call in a loop.

## Rendering, and the five ways it nests

```mermaid
flowchart TD
    R["Render / RenderString / RenderValues<br/><i>defer catchPanic, bufio.Writer, flush either way</i>"]
    R --> NB["newBudget(ctx, env)<br/><i>iterations, output bytes, the context</i>"]
    NB --> RS["renderState → exec.execBody"]
    RS --> EXT{"{% extends %}?"}
    EXT -->|"yes"| PAR["park the parent on st.parent,<br/>then loop — never recurse"]
    PAR --> RS
    EXT -->|no| DONE(["done"])

    RS --> N["a nested render"]
    N --> I["{% include %}"]
    N --> M["{% import %} / {% from %}"]
    N --> MA["a macro call"]
    N --> BL["{% block %} / self.x"]

    classDef ok fill:#bbf7d0,stroke:#15803d,color:#000
    class DONE ok
```

The five routes are written separately, and that is where the bugs were. What
each one does with the three things that must not be dropped:

| construct | new `State`? | budget threaded | depth counted | output |
|---|---|---|---|---|
| `{% include %}` | yes, via `renderInto` | yes | `st.enter()` | **buffered in full**, then written on |
| `{% extends %}` | no — same `State` | yes | `st.enterExtends()` | parent body replaces the child's |
| `{% import %}` / `{% from %}` | yes, `tmpl.newState(vars, depth, budget)` | yes | `st.enter()` | buffered; kept as the module's `str()` |
| a macro call | no — same `State` | yes | `st.enter()` | captured |
| `{% block %}` / `self.x` | no — same `State` | yes | `st.enter()` | captured |

Every row now threads the budget and counts the depth. Two used not to, and this
table is how you would have seen it: `{% import %}` built its `State` by hand and never
copied the budget, so an imported template rendered with *no* bound and no
context at all — not a fresh allowance, none — and `{% block %}` never entered
the depth counter, so `{% block x %}{{ self.x }}{% endblock %}` recursed until
the goroutine stack was exhausted. A Go stack overflow is a fatal error rather
than a panic, so `catchPanic` never saw it and the process died.

**When you add a sixth route, fill in this row first.**

### What holds output before writing it

Six constructs capture their own body rather than streaming it. Peak memory
tracks the largest of them, not the write buffer.

```mermaid
flowchart LR
    X["exec.execBody"] --> INC["{% include %}<br/><i>exec.go:616</i>"]
    X --> FIL["{% filter %}<br/><i>exec.go:519</i>"]
    X --> SET["block {% set %}<br/><i>exec.go:404</i>"]
    X --> FOR["recursive {% for %}<br/><i>exec.go:279</i>"]
    X --> MAC["macro body<br/><i>call.go:291</i>"]
    X --> BLK["{% block %} body<br/><i>runtime.go:568</i>"]

    INC & FIL & SET & FOR & MAC & BLK --> BUF[["strings.Builder"]]
    BUF --> W["ex.writeTo → budget.account → bufio.Writer → w"]

    classDef buf fill:#fde68a,stroke:#b45309,color:#000
    class BUF buf
```

A filter, an assignment and a function each need finished text before they can
act on it, so each buffers. `{% filter %}`, block `{% set %}` and the recursive
`{% for %}` use `capture`; the macro and block bodies use `captureFunction`,
which also moves the *stream*, because a body is a function and not merely a
buffer. That distinction is what makes `{% include ... without context %}` escape
an enclosing `{% filter %}` — it writes to `ex.stream` rather than `ex.out`,
reproducing a jinja2 quirk that the differential fuzzer found.

Output is charged against the budget wherever it lands, so text that passes
through two buffers is counted twice. The buffers are the memory the bound
exists to protect, so they are what has to be counted.

## Charge before you allocate

Every size a template chooses goes through this, and getting it wrong is the
single most repeated defect in this codebase's history.

```mermaid
flowchart TD
    T["a template names a size<br/><code>s * n</code> · <code>x|center(n)</code> · <code>x|indent(n)</code> · <code>x|slice(n)</code> · <code>lipsum(n)</code>"]
    T --> SAT["saturate, do not clamp"]
    SAT --> C{"State.ChargeBytes(n)<br/>State.ChargeItems(n)"}
    C -->|"over the hard ceiling, 2**31"| O["OverflowError<br/><i>refused outright</i>"]
    C -->|"over the render budget"| E["ErrOutputTooLarge /<br/>ErrTooManyIterations"]
    C -->|"within budget"| A["allocate"]
    A --> P{"a long walk that<br/>writes no output?"}
    P -->|yes| Q["State.Poll() each pass<br/><i>this is where cancellation lands</i>"]
    P -->|no| D(["write / return"])
    Q --> D

    N["State is nil during constant folding"] -.->|"Step handles it"| C

    classDef bad fill:#fecaca,stroke:#b91c1c,color:#000
    classDef ok fill:#bbf7d0,stroke:#15803d,color:#000
    class O,E bad
    class D ok
```

Four rules, each of which has been broken at least once:

1. **Charge first.** Charging after the allocation charges for memory that is
   already gone.
2. **Charge the total, before splitting it.** Guards do not compose:
   `("a"*60000)|replace("a","b"*60000)` passed every individual guard and
   allocated 3.6 GB, and `center` charged its two halves separately, each of
   which cleared the ceiling while their sum did not.
3. **Saturate, never clamp.** Clamping a huge count down to the ceiling turns
   "refuse" into "allocate the maximum" — the same failure shape as an overflow
   wrapping negative and reading as a tiny allocation.
4. **Compile time is not exempt.** Constant folding runs real filters, with its
   own budget, reset per fold attempt — folding is observable, so it must not
   depend on how many expressions preceded it.

The hard ceiling sits *above* the budget because a zero or negative budget means
unbounded, and unbounded must still not mean "allocate 2\*\*63 bytes".

`conformance/budget_test.go` grades this generatively, and found two of the four
on its first run.

## Where the specification lives

Nowhere in this repository. CPython `jinja2`, pinned in `.venv`, is the
specification; `tools/oracle/` drives it and `conformance/` grades against it.
See the README for the corpora and the numbers, and `make ask T='...'` for
putting a question to it directly.
