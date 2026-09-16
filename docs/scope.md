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
  unsafe-callable machinery are about restricting access to *Python* objects.
  Go's own type system draws that line differently, so a faithful port would
  be a false reassurance.
- **The i18n extension.** `{% trans %}`, `gettext`, `ngettext`.
- **Bytecode caches**, which have no meaning without Python bytecode.

The excluded items may return later; they are excluded now because each one
would be a large surface with its own conformance story, and none is needed to
render a template correctly.
