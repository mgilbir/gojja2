# Scope

gojja2 implements the Jinja2 *template language*, with CPython's `jinja2` as the
specification for everything inside that boundary.

## In scope

Everything a template can express, including the parts that look Pythonic:

- multi-line tags, and expressions spanning lines inside them;
- trailing commas in list, tuple, dict and call literals;
- slicing with negative bounds and steps, chained comparisons, tuple
  unpacking in `for` and `set`;
- arbitrary-precision integers, Python's numeric tower, and `repr()`/`str()`
  as CPython renders them, because they reach the output;
- the full statement set: `for`/`else`, `if`/`elif`/`else`, `set` (inline and
  block), `with`, `filter`, `raw`, `macro`, `call`, `block`, `extends`,
  `include`, `import`, `from`, `do`, `break`/`continue` when enabled;
- template inheritance, `super()`, scoped and required blocks, namespaces;
- autoescaping with markupsafe semantics.

## Out of scope

- **Anything Python-specific that only exists because jinja2 compiles to
  Python bytecode.** `complex` results from `**` (see
  [divergences.md](divergences.md)) are the clearest case: reachable from a
  template, but not expressible in one.
- **Async rendering.** `{% for %}` over async iterables, `auto_await`,
  `render_async`. This is a property of the Python runtime, not of the
  template language.
- **The sandbox.** `SandboxedEnvironment`, attribute allow-lists, and the
  unsafe-callable machinery are about restricting access to *Python* objects,
  and reaching them through a Python object graph -- `__class__.__subclasses__`,
  `__builtins__`, `__globals__`. That graph has no counterpart here: `__class__`
  is implemented and is inert, exposing a name, a repr and equality and nothing
  onward, so the route those tests document does not exist to be closed.

  This used to be justified by saying that "Go's own type system draws that line
  differently". That was not true, and it is worth being precise about instead.
  Reflection draws no line at all: a struct in the render context exposes every
  exported field and method it has. What bounds that here is a policy rather
  than a type system --- `WithMethodPolicy`, which by default exposes only
  methods taking no arguments, so a template cannot choose what a host method is
  called with. Widening it is a decision the host makes explicitly.

  So: template authors are trusted to read any exported field of anything handed
  to them, and to call its no-argument methods. If that is more than they should
  have, hand the template a narrower value -- that is the boundary, and it is
  drawn by what the caller puts in the context, not by an allow-list here.
- **`Environment(optimized=False)`.** jinja2's optimizer can be turned off;
  gojja2's constant folding is always on. Measured rather than assumed: 3,000
  generated templates render identically with the flag either way, and a probe
  of the shapes where folding could show found exactly two families, both of
  which gojja2 already reproduces at jinja2's *default*.

  The first is the negative-power lift --- `{% set m = 2 %}{{ (-8) ** m }}` is
  -64 with the optimizer and 64 without it, because the fold turns `-8` into a
  constant whose repr carries the minus and the generated `-8 ** m` then reads
  as `-(8 ** m)`. The second is a constant that folds to an infinity:
  `{{ ('1e400')|float|int }}` is the `NameError` the folded `inf` produces with
  the optimizer, and an `OverflowError` from `int(inf)` without it. Both are in
  [divergences.md](divergences.md) already.

  So the flag is not a surface with its own conformance story; it is a way of
  asking for the two behaviours recorded there in the other order.
- **The i18n extension.** `{% trans %}`, `gettext`, `ngettext`.
- **Bytecode caches**, which have no meaning without Python bytecode.

The excluded items may return later; they are excluded now because each one
would be a large surface with its own conformance story, and none is needed to
render a template correctly.
