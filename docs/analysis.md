# Asking questions about a template

gojja2 can tell you about a template without rendering it: its structure, in a
vocabulary graded against jinja2's own parse tree; which templates it pulls in;
who binds each name it reads; and, through the `dataflow` package, what it does
with each variable you pass -- whether the value can reach the output, whether
it only steers what is rendered, and whether it can make the render fail.

Reach for this when you are building a tool around templates: a dependency
tracker, a linter, a check that a context carries everything a template needs,
a redaction audit. For rendering itself, see [guide.md](guide.md).

## The structure

`Template.Syntax` returns the template's structure in the vocabulary of the
[`syntax`](https://pkg.go.dev/github.com/mgilbir/gojja2/syntax) package: one node
type, a closed set of kinds, and children reached through **labelled edges**.

```go
tmpl, _ := env.FromString(`{% if admin %}{{ name }}{% endif %}`)

syntax.Walk(tmpl.Syntax().Root, func(n *syntax.Node, role syntax.Role) bool {
    if role == syntax.RoleTest && n.Kind == syntax.KindName {
        fmt.Println(n.Attr("name"), "decides something")
    }
    return true
})
```

The labels are what make a query short. `RoleTest` is a condition wherever it
appears, so asking "which names decide something" needs no knowledge of the node
set — where a positional tree would mean knowing that a condition is the first
field of an `if`, the third of a `for` and the test of a conditional expression.

It is the template **as written**, not as compiled: the constant folder has run
over the engine's own tree, and whether `{{ xs[[]] }}` is a constant subscript is
a fact about the optimizer rather than about the template.

### Which templates it names

`Template.ReferencedTemplates` is jinja2's `meta.find_referenced_templates`: the
name in every `{% extends %}`, `{% include %}`, `{% import %}` and `{% from %}`,
in order, wherever the tag sits, with each candidate of a literal list listed
separately. A name decided at render time -- a variable, a concatenation, a
conditional -- is listed with `Dynamic` set and no name, so a dependency tracker
learns that a dependency exists even when it cannot say on what.

```go
for _, ref := range tmpl.ReferencedTemplates() {
    if ref.Dynamic {
        log.Printf("line %d: a template chosen at render time", ref.Line)
        continue
    }
    deps = append(deps, ref.Name)
}
```

### What the names mean

The tree comes with `Info`, which says who binds each name, what each read
resolves to, and which nodes introduce a scope:

```go
tree := tmpl.Syntax()
for name, sym := range tree.Info.Context {
    if sym.Kind == syntax.SymContext {
        fmt.Println(name, "must be supplied by the caller")
    }
}
```

These are kept beside the tree rather than on its nodes, the way `go/types`
keeps them beside `go/ast`. Some of them are not properties of a node at all —
which scope owns a name is a property of a (scope, name) pair — and keeping the
tree free of them keeps it immutable, which matters because one compiled
template is walked from many goroutines.

They are also the part a caller could not work out for themselves. jinja2
decides per frame, on a name's **first mention**, whether it belongs to the frame
or resolves from the caller: `{{ x }}{% set x = 1 %}` reads your `x`, and
`{% for i in [1] %}{{ x }}{% endfor %}{% set x = 1 %}` does not, because the root
frame claimed `x` before the loop ran. `Info` is graded against jinja2's own
`idtracking` for every committed case, so it is that rule rather than a second
reading of it.

The vocabulary is deliberately neither gojja2's internal tree nor jinja2's.
Matching jinja2's node classes would be a promise to reproduce a data structure
rather than a behaviour. What is promised instead is stronger and more useful:
gojja2's tree and jinja2's own parse tree encode to **byte-identical** canonical
form for every committed conformance case, so a query written against this
reaches the conclusion jinja2 would have reached. See
[docs/contributing.md](contributing.md) for how that is checked.

## What a template does with your variables

The [`dataflow`](https://pkg.go.dev/github.com/mgilbir/gojja2/dataflow) package
is a worked query over that tree. It answers, for each variable, whether its
**value can reach the output**, whether it only **steers** what is rendered, or
neither:

```go
tree := tmpl.Syntax()
for name, e := range dataflow.Analyze(tree).Context(tree) {
    fmt.Println(name, e&dataflow.Printed != 0, e&dataflow.Steers != 0)
}
```

There is a third, `Required`: the render can **fail** because of this variable.
`Printed` and `Steers` describe the document; `Required` describes whether there
is one. `{% set unused = x + 1 %}` prints nothing and steers nothing, and passing
a string for `x` still stops the render dead — so reading "cannot affect the
output" as "need not be passed" would be a mistake. It is an over-approximation:
the operation *can* raise, not that it will, so most variables have it and the
useful signal is its absence.

The effects are independent and a variable can have none: `{% set unused = x %}`
with nothing reading `unused` means `x` cannot change the output *or* break the
render. That negative is the useful part, and it is why this is a dataflow
analysis rather than a scan for names — `{% set y = x %}{{ y }}` prints `x` without ever naming
it at an output position, and `{{ "yes" if flag else "no" }}` prints neither
operand while `flag` decides which.

One environment setting changes the answer, and the analysis has to be told:
under `StrictUndefined`, *reading* a name that was not passed raises rather than
rendering empty, so the arm of `{% if c %}{{ nope }}{% endif %}` can stop the
render and `c` decides whether it does. Pass `dataflow.WithStrictUndefined()`
when the environment uses that class. It widens `Required` coarsely -- an arm
that reads any name is an arm that can fail -- which is the honest shape of the
setting; without it the same template's `c` only steers, which is the right
answer for every other class, `ChainableUndefined` and `DebugUndefined`
included.

`Opaque` means the answer has no reliable negative — a route the analysis could
not follow. Nothing is ever reported as unable to reach the output when it might.
It is rarer than it sounds: across the chat templates real models ship, under 1%
of variables come back opaque.

A call the analysis cannot see into — `{{ msg.strip() }}`, or a macro held in a
variable — is not one of those routes. The result comes out of the receiver and
the arguments, which is plain enough. What needs care is that a call can also
*change* its receiver: `{% do items.append(x) %}` renders nothing and leaves `x`
inside `items`, so a later `{{ items }}` prints it. That is recorded as a
dependency, which costs some precision in the safe direction — `{{ s.split(sep) }}`
does not mutate `s`, but `sep` inherits whatever `s` does.

### Namespaces

`{% set %}` inside a loop does not escape it, which is why `namespace()` exists
and why it is the shape worth following — accumulate then print is the case most
worth tracing:

```jinja
{% set ns = namespace(total=0) %}
{% for row in rows %}{% set ns.total = ns.total + row %}{% endfor %}
{{ ns.total }}
```

Each field is followed separately, so `rows` comes back as printed rather than
"might be", and `{% set ns = namespace(a=p, b=q) %}{{ ns.a }}` says `p` is
printed and `q` cannot affect the output at all.

That holds only while the namespace itself is never handed anywhere.
`{% set other = ns %}`, or passing it to a macro or a filter, makes two names for
one object, and following that is alias analysis — getting it subtly wrong would
mean reporting a real negative that is not true. So a namespace read anywhere but
as the subject of a field access collapses back to `Opaque`. The test is crude
and deliberately in the safe direction.

### Following `{% include %}` and friends

Give it a resolver and it reads the templates a template pulls in, which is how
a variable that only the *other* template mentions gets reported at all:

```go
flow := dataflow.Analyze(tree, dataflow.WithResolver(func(name string) *syntax.Tree {
    other, err := env.GetTemplate(name)
    if err != nil {
        return nil
    }
    return other.Syntax()
}))
```

The defaults differ and the difference is worth knowing: `{% include %}` hands
over the context, and `{% import %}` and `{% from … import %}` do not. An import
without context cannot see your variables at all, so it cannot print them —
a real negative rather than a shrug. A name that is not a constant
(`{% include page %}`) stays opaque, because which template runs is not a static
fact; `page` is reported as steering the output, since two names render two
documents.

Without a resolver, a reference to another template makes everything opaque. A
cycle terminates rather than recurring.

`Flow.Derives` is the dependency graph the verdict was propagated over, exposed
because the interesting question is not always "can this be printed" — "what
would change if I stopped passing x" and "which of these bindings is dead" are
the same graph asked differently.

**It reads only the public tree.** Everything in the package works from
`syntax.Tree` and its `Info`, which is the demonstration that the exposed tree is
enough to reason with: an answer it can reach is an answer you can reach. It is
graded against an independent implementation over jinja2's own AST for every
committed case.
