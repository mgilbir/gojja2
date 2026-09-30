# Deliberate divergences from CPython jinja2

CPython's `jinja2` is the specification, and everywhere gojja2 disagrees with it
that is a bug. This file lists the handful of places where the disagreement is
intentional, and what gojja2 does instead. Each one is asserted by a test, so it
cannot quietly turn into something else.

**Anything not listed here is a bug.** Please report it with the template and
the output `make ask T='...'` gives.

## If you are porting templates, read this paragraph

Of the twenty-eight divergences below, **one** is worth going looking for:
jinja2's `map`, `select`, `reject`, `selectattr`, `rejectattr`, `unique` and
`items` return generators, and gojja2's return lists. A generator is always
truthy, so in jinja2 `{% if items|selectattr("active") %}` runs its body even
when nothing was selected. gojja2 answers the question the template asked.
Spelling it `|selectattr("active")|list` is exact in both, and is what jinja2's
own documentation tells you to write.

One other renders differently rather than failing, but only for a template that
asks `{{ self is iterable }}` -- `True` in jinja2, `False` here. Iterating
`self` raises under both.

Everything else on this page either fails under CPython too, was never
reproducible, or changes something outside the render. The table says which.

The bounds gojja2 puts on a render -- iterations, output, nesting, allocation --
are safety controls rather than behavioural choices, and they live in
[limits.md](limits.md).

## Every divergence, by whether it can bite you

| divergence | what differs | can it change a correct template? |
|---|---|---|
| [Lazy sequence filters](#lazy-sequence-filters) | sequence filters return lists, not generators | **Yes** -- and toward what the author meant |
| [A constant that folds to an infinity](#a-constant-that-folds-to-an-infinity) | `{% if 1e400 %}` renders; jinja2 raises `NameError` | No -- the template is broken under CPython |
| [A folded infinity jinja2 writes out](#a-folded-infinity-jinja2-writes-out) | renders the number; jinja2 raises `NameError: name 'inf' is not defined` | No -- it renders where CPython cannot |
| [A macro with a repeated parameter name](#a-macro-with-a-repeated-parameter-name) | both refuse it; the wording differs | No -- only the message differs |
| [A keyword written twice in one call](#a-keyword-written-twice-in-one-call) | both refuse it; the wording differs | No -- only the message differs |
| [A break or a continue that binds to no loop](#a-break-or-a-continue-that-binds-to-no-loop) | both refuse it; the wording differs | No -- only the message differs |
| [Complex numbers](#complex-numbers) | `(-8) ** (1/3)` raises `ValueError`; jinja2 makes a `complex` | No -- nothing can consume the `complex` |
| [A macro containing a context-free include](#a-macro-containing-a-context-free-include) | the macro renders; jinja2 returns a generator repr | No -- the body never ran under CPython |
| [`{{ self\|list }}`](#-selflist-) | `TypeError`; jinja2 raises `KeyError: 0` | Only `self is iterable`, which answers differently |
| [`\N{...}` escapes](#n-escapes-in-string-literals) | refused; needs the Unicode name database | Only if you write `\N{...}` |
| [A lone surrogate in a string literal](#a-lone-surrogate-in-a-string-literal) | `'\ud800'` is U+FFFD here | Only if you write a surrogate escape that is not half of a pair |
| [Which codecs and handlers are known](#which-codecs-and-error-handlers-encode-and-decode-know) | utf-8, ascii, latin-1; jinja2 has ~100. Three error handlers are missing too | Only outside those three, or with `namereplace` or a surrogate handler on a decode |
| [Objects whose repr carries an address](#objects-whose-repr-carries-an-address) | a different address | No -- unreproducible in CPython too |
| [`\|pprint` of a value that contains itself](#pprint-of-a-value-that-contains-itself) | a different address | No -- likewise |
| [A comparison that runs out of stack under 3.14](#a-comparison-that-runs-out-of-stack-under-314) | the sentence is CPython's through 3.13; 3.14 names the stack it used | No -- only under 3.14, and only the wording |
| [The order a set prints in](#the-order-a-set-prints-in) | sorted, where CPython's is its hash order | No -- CPython's own order differs between runs |
| [lipsum() and random](#lipsum-and-random) | a different random draw | No -- likewise |
| [`is sameas` on two literals](#is-sameas-on-two-literals) | `1.5 is sameas(1.5)` is True here, False there; so is `==` on two bound methods of literals | Only for a literal-vs-literal `sameas` or method comparison, which is a tautology |
| [Comparison order inside a long sort](#comparison-order-inside-a-long-sort) | which pair a failing sort names | Only inside an error message, above 64 elements |
| [An `{% autoescape %}` flag that is a StrictUndefined](#an--autoescape--flag-that-is-a-strictundefined) | raises at the tag; jinja2 raises at the first output that escapes | Only a body whose output is empty or folded at compile time |
| [A macro's view of the loop it was defined in](#a-macros-view-of-the-loop-it-was-defined-in) | a macro keeps the values its loop had when it was defined; jinja2's reads them live, and `missing` after the loop | Only a macro that is called after its iteration has passed |
| [A `{% set %}` block writing to a name that was never set](#a--set--block-writing-to-a-name-that-was-never-set) | both raise `TypeError`; jinja2 names a sentinel of its own | No -- only the type in the message |
| [Python object introspection](#python-object-introspection) | `__doc__` is empty; two sandbox routes are absent | No |
| [`len()` of a very long range](#len-of-a-very-long-range) | nothing -- matched exactly, boundary included | No |
| [A render does not mutate the caller's data](#a-render-does-not-mutate-the-callers-data) | a template cannot write to your objects | Changes what the *host* sees after the render, not what renders |
| [finalize and a constant print](#finalize-and-a-constant-print) | a constant print is not finalized at compile time | Only with `WithFinalize` and autoescape |
| [Subscripting the `dict` global](#subscripting-the-dict-global) | `dict['k']` is undefined; in Python it is a generic alias | No -- it is a type annotation, not a lookup |
| [Which line an error inside a multi-line tag names](#which-line-an-error-inside-a-multi-line-tag-names) | the failing token's line, not the tag's | No -- same error, different line number |
| [What a missing template's error says](#what-a-missing-templates-error-says) | the message names the template, not a search path | No -- same error, same name |
| [No automatic template reload](#no-automatic-template-reload) | no `auto_reload`; use `ClearCache` | Changes when an edit is picked up |
| [The default autoescape extension set](#the-default-autoescape-extension-set) | adds `xhtml` to jinja2's three | Only ever escapes *more*, never less |

---

## The one that can change what a working template renders

### Lazy sequence filters

```jinja
{% if items|selectattr("active") %}...{% endif %}
```

jinja2's `map`, `select`, `reject`, `selectattr`, `rejectattr`, `unique` and
`items` return generators, and `reverse` returns Python's `reversed()` iterator
for anything but a string. gojja2's return lists. Anything that *walks* the
result forwards -- iterating it, `|list`, `|join`, `|first`, `|sort` -- behaves
identically. Five things do not:

| template | jinja2 | gojja2 |
|---|---|---|
| `{{ [1,2]\|map("string") }}` | `<generator object ... at 0x7f9c...>` | `['1', '2']` |
| `{% if items\|selectattr("active") %}` | always taken | taken when non-empty |
| `{{ items\|selectattr("active")\|length }}` | `TypeError: object of type 'generator' has no len()` | the count |
| `{{ items\|map("string")\|last }}` | `TypeError: 'generator' object is not reversible` | the last item |
| `{{ tools\|map(attribute="f")\|tojson }}` | `TypeError: Object of type generator is not JSON serializable` | the JSON |

The first cannot be matched by anyone: the address differs between two runs of
CPython itself, which is why the corpus case that prints one is marked
*ungradable* rather than failing.

The second, third and fourth could be matched, and are not. A generator is always
truthy, so in jinja2 `{% if items|selectattr("active") %}` runs its body even
when nothing was selected -- a long-standing footgun that the documentation
tells you to spell `|selectattr("active")|list` around. Reproducing it would
mean building the trap on purpose, and a template that guards a section on an
empty filter result would render the section. gojja2 answers the question the
template asked. This is the one divergence here that can change what a working
template renders, and it changes it toward what the author meant; if you are
porting templates, `|list` before `|length` or a truth test is exact in both.

The fifth is not hypothetical: DeepSeek-R1's own chat template, as vendored by
llama.cpp, writes `{{ tools | map(attribute='function') | tojson(indent=2) }}`,
which raises under CPython jinja2 and renders under gojja2. Both cases are in
the chat-templates corpus, listed in testdata/known_failures.txt.

A list is also built in full, which a lazy iterator is not: jinja2 answers
`{{ range(2**70)|reverse|first }}` by stepping `reversed()` once, and gojja2's
`|reverse` walks the whole range into a list and is stopped by the render
budget. `{{ range(2**70)|last }}` answers exactly on both.

A knock-on: a filter that raises does so at the point gojja2 applies it, where
jinja2 defers until the generator is consumed. The exception is the same; where
it surfaces can differ by a tag or two.

A second knock-on, visible only on CPython 3.14: that version names the count in
`too many values to unpack`, and only for a list, a tuple or a dict -- everything
else goes through the iterator path, which does not count. So a lazy filter's
result carries no count there and gojja2's list does:

```jinja
{% for a, b in [[1,2,3]|reverse] %}{% endfor %}
```

is `(expected 2)` on CPython 3.14 and `(expected 2, got 3)` here. `|sort` agrees,
because jinja2's sort returns a real list too. Faking it would mean pretending
the value is something other than what gojja2 holds.

## Where jinja2 raises and gojja2 renders

Each of these is a template that is already broken under CPython jinja2. gojja2 answers the question the template asked instead, or refuses for a reason of its own. If you are porting, none of these changes a template that works today.

### A constant that folds to an infinity

```jinja
{% if 1e400 %}y{% endif %}
```

CPython raises `NameError: name 'inf' is not defined`. jinja2 writes a folded
constant into its generated Python as that constant's repr, and
`repr(float("inf"))` is the bare word `inf`, which is not a literal.

It only bites when the constant is emitted as *code*. `{{ 1e400 }}` prints
`inf`, because the output folder converts it to text before it is written out;
`{% if 1e400 %}`, `{% set x = 1e400 %}` and `{{ ('1e400')|float|int }}` raise.

gojja2 renders the template. Reproducing the other answer would mean carrying a
poisoned constant through the optimizer so that *using* it raises a NameError
about a Python identifier that has no counterpart here. The corpus case is in
`testdata/known_failures.txt`.

### A macro with a repeated parameter name

```jinja
{% macro m(a, a) %}{% endmacro %}
```

CPython raises `SyntaxError: duplicate argument 'l_1_a' in function definition
(<template>, line 12)`. jinja2 compiles a macro to a Python function, and the
duplicate reaches the Python compiler as a duplicate parameter.

Look at what that error names: `l_1_a` is the identifier jinja2 *generated* for
the parameter, and line 12 is a line of the generated module, not of the
template. Neither exists here, and inventing them would mean building a decoy of
jinja2's code generator to report an error about it -- so this is the kind of
Python-specific artefact [scope.md](scope.md) excludes rather than a behaviour
to match.

gojja2 refuses the template too, from its own parser, with

```
TemplateSyntaxError: duplicate argument 'a' in function definition
```

which is CPython's wording with the parameter the template actually wrote.

Only the message differs now. It used to be more than that: gojja2 compiled the
macro and let the later parameter win, so a macro written against gojja2 worked
here and failed when the template was run under CPython — the one direction of
divergence that costs a template author something. The decision matches; the
identifier cannot.

The duplicate is refused in a *signature* and nowhere else, which is also
jinja2's rule: `{% for a, a in ... %}`, `{% set a, a = 1, 2 %}` and
`{% with a = 1, a = 2 %}` all let the later binding win, and a macro may be
redefined.

### A keyword written twice in one call

```jinja
{% macro m(a=1) %}{{ a }}{% endmacro %}{{ m(a=2, a=3) }}
```

CPython raises `SyntaxError: keyword argument repeated: a (<template>, line
23)`. jinja2 writes a call's keywords out as `name=value` pairs, so the
duplicate reaches the Python compiler -- and line 23 is a line of the module
jinja2 generated, not of the template.

gojja2 refuses the template too, with CPython's wording and the line the
template actually wrote:

```
SyntaxError: keyword argument repeated: a
```

As with [a repeated parameter name](#a-macro-with-a-repeated-parameter-name),
this was a *behavioural* divergence first: gojja2 bound the first keyword and
swept the second into `kwargs`, so `{{ m(a=2, a=3) }}` rendered `2` here and
failed to compile under CPython.

The refusal is placed where jinja2's is, which is not the parser. A node that
folds never reaches the generator, and jinja2's `as_const` collects keywords
through a dict comprehension -- where a repeated name quietly keeps the last. So

```jinja
{{ [1]|join(d='-', d='+') }}   renders 1 on both sides
{{ lst|join(d='-', d='+') }}   compiles on neither
```

and `{% extends %}` above a print tag takes it out of the generator's hands
entirely, so the repeated keyword below one is never seen. `fold/keyword_
repeated_*` grades all three.

### A break or a continue that binds to no loop

```jinja
{% for x in seq %}x{% else %}{% break %}{% endfor %}
```

With the `loopcontrols` extension enabled, CPython raises
`SyntaxError: 'break' outside loop (<template>, line 21)`.

jinja2 implements `{% break %}` and `{% continue %}` by emitting Python's own
keywords, so what binds one is a `for` in the function jinja2 *generated*, and
CPython refuses the module when nothing does. Line 21 is a line of that module.
gojja2 has none, so -- as with [a repeated parameter
name](#a-macro-with-a-repeated-parameter-name) -- it refuses the same templates
at the same point, with CPython's class and CPython's words, and stops there:

```
SyntaxError: 'break' outside loop
SyntaxError: 'continue' not properly in loop
```

The error carries the line *of the template*, which is where gojja2 reports every
compile error.

Which shapes bind nothing is jinja2's rule, and it is not the one a reader of the
template would draw. Measured against CPython jinja2:

| where the break sits | binds to the loop? | why |
|---|---|---|
| `{% if %}`, `{% filter %}`, `{% set v %}...{% endset %}`, `{% with %}`, `{% autoescape %}` | **yes** | emitted inline, so the `for` is still there |
| a loop's own `{% else %}` body | no | emitted *after* the loop |
| a `recursive` loop's `{% else %}` body | no | that loop and its else are a function of their own |
| `{% macro %}`, `{% block %}`, `{% call %}` | no | each compiles to a function |
| an inner loop's `{% else %}`, inside an outer loop | **yes, to the outer one** | the inner else body sits in the outer loop's body |

The inline half is graded by the corpus and passes. The refusals are admitted in
`testdata/known_failures.txt`, because only the message differs, and
`TestUnboundLoopControlIsRefused` pins each shape.

This was a *behavioural* divergence until the refusal was added, and in the
direction that costs a template author something. `{% break %}` outside a loop
rendered an error whose entire message was `break` -- the sentinel gojja2 passes
a break along as, reaching the caller. Worse, a break inside a macro body
propagated out of the call and broke the loop the macro was *called* from, so

```jinja
{% for x in seq %}{% macro m() %}{% break %}{% endmacro %}{{ m() }}{% endfor %}
```

rendered nothing here and would not compile under CPython.

### Complex numbers

```jinja
{{ (-8) ** (1/3) }}
```

CPython renders `(1.0000000000000002+1.7320508075688772j)`. A negative base
raised to a fractional power is the only way a template can reach Python's
`complex` type: there is no complex literal, no filter accepts or returns one,
and every arithmetic operator on one is a type error in any template that then
tries to use the result.

gojja2 raises `ValueError` rather than inventing an approximation that would be
wrong in a different way. Asserted by `TestOperatorsMatchCPython`.

### A macro containing a context-free include

```jinja
{% macro m(x) %}{% include "other.txt" without context %}{% endmacro %}{{ m(1) }}
```

`{% include ... without context %}` compiles, in jinja2, to a yield straight
into the enclosing function's output stream. Inside a macro that turns the
macro itself into a Python generator, which `{{ m(1) }}` then renders as
`<generator object root.<locals>.macro at 0x7f...>` -- an address, and a body
that never ran.

gojja2 renders the macro. The bypass itself *is* reproduced -- a context-free
include still escapes an enclosing `{% filter %}` buffer, which is covered by
the corpus -- but turning a function into an unconsumed generator is an
artefact of compiling to Python, not a property of the language.

### `{{ self|list }}`

`TemplateReference` defines `__getitem__` and nothing else, so Python's legacy
iteration protocol makes it iterable and `list(self)` asks for index 0 --
which is a block name lookup, and raises `KeyError: 0`. gojja2 answers
`TypeError: 'TemplateReference' object is not iterable`.

Reproducing the KeyError would mean a way for an Object to say "iterable, but
the first step fails", which nothing else here needs. Every template that
*iterates* `self` fails either way -- `|list`, `|join`, `|sort`, `{% for %}`
and `in` all raise, and only the wording differs. The same holds through a
`mappingproxy` over `self`, whose iteration is the wrapped object's.

One case answers rather than failing, though, so it is not only wording:

```jinja
{{ self is iterable }}
```

is `True` in jinja2, because the test only asks whether `iter()` accepts the
object, and `False` here. A template that branches on it takes the other
branch.

### `\N{...}` escapes in string literals

```jinja
{{ "\N{BULLET}" }}
```

Resolving a code point by its Unicode name needs the full name database: 32,647
stored names, about 500 KB once word-compressed, which is roughly two and a half
times every generated table gojja2 carries put together and about 7% onto every
binary that imports it. `golang.org/x/text/unicode/runenames` does not help --
it costs more still, returns `<CJK Ideograph>` where CPython computes
`CJK UNIFIED IDEOGRAPH-4E00`, and tracks a different Unicode version, so it
disagrees with CPython about 104,025 code points. Across the ten upstream
projects the corpora are drawn from, `\N{` appears twice, both times inside
Jinja's own test suite.

So gojja2 refuses every `\N{...}`, and **says that** rather than borrowing
CPython's wording:

```
\N{BULLET} needs the Unicode name database, which gojja2 does not carry;
every \N{...} is refused, including a correct name. Spell the character
with \uNNNN or \UNNNNNNNN, both of which are exact
```

CPython's own message there is `unknown Unicode character name`, which is its
answer for a name that does not *exist*. `BULLET` exists, so borrowing the
wording sent readers hunting a typo that was not there.

A **malformed** escape is wrong under CPython too, so that one keeps CPython's
`malformed \N character escape` exactly. The boundary between the two is not
where it looks: an empty name is malformed, while anything at all between the
braces is a name, including a single space. `testdata/corpus/errors/n_escape_malformed_*`
grades it.

Every other escape -- `\xNN`, `\uNNNN`, `\UNNNNNNNN`, octal, and the
single-character escapes -- is exact, including CPython's quirk that `"\é"`
decodes to the four characters `\xe9`.


### A lone surrogate in a string literal

```jinja
{{ '\ud800' == '\ufffd' }}     False on CPython, True here
```

A `\u` or `\U` escape can name a UTF-16 surrogate, U+D800 to U+DFFF, and in
Python that is a one-character string like any other. Two of them written side
by side are two such characters, not the one they would encode in UTF-16:
`'\ud83d\ude00'` has length 2 and is not the emoji. A Go string is UTF-8,
which has no encoding for a surrogate at all, so the lexer writes U+FFFD for
each. The length agrees, and so does everything that only counts or compares
lengths. What differs is everything that looks at the character: equality,
`|tojson` (`"\ud800"` there, `"\ufffd"` here), `|pprint`, and every encoder --
`'\ud800'.encode()` and `|urlencode` are "surrogates not allowed" on CPython
and three bytes of U+FFFD here.

Carrying one would mean a string representation that is not a Go string, for a
value no text source can contain: nothing read from a file, a context or a
decode is a lone surrogate, only an escape written for the purpose. The codec
entry above is the same limit reached from the other side. It is pinned by
`TestLoneSurrogateLiteral` rather than by corpus cases, because the corpus's
reference files are UTF-8 too and cannot hold CPython's answer.
### Which codecs and error handlers `.encode()` and `.decode()` know

```jinja
{{ "€"|string.encode("cp1252") }}
```

CPython ships about a hundred codecs. gojja2 implements the three a template
plausibly asks for -- `utf-8`, `ascii` and `latin-1`, under all the aliases
CPython accepts for them -- with CPython's own `UnicodeEncodeError` and
`UnicodeDecodeError` wording, positions counted in characters as CPython counts
them, and all but three of its error handlers.

Anything else raises the `LookupError` CPython raises for an encoding it does
not have:

| template | jinja2 | gojja2 |
|---|---|---|
| `{{ "é".encode("latin-1") }}` | `b'\xe9'` | the same |
| `{{ "€".encode("ascii", "xmlcharrefreplace") }}` | `b'&#8364;'` | the same |
| `{{ "é".encode("cp1252") }}` | `b'\xe9'` | `LookupError: unknown encoding: cp1252` |
| `{{ "é".encode("utf-16") }}` | `b'\xff\xfe\xe9\x00'` | `LookupError: unknown encoding: utf-16` |

The line is drawn at codecs that need a character table: `utf-8`, `ascii` and
`latin-1` are arithmetic, and the rest are data that would have to be generated
and carried. Refusing is the point -- encode used to ignore its argument
entirely and answer UTF-8 whatever was asked for, so a template asking for
latin-1 silently got two bytes where it wanted one.

#### The error handlers

The handlers are not one set, and CPython's own asymmetry is reproduced.
`xmlcharrefreplace` and `namereplace` are declared for an encode, and CPython's
callback refuses a `UnicodeDecodeError` by type rather than by name, so asking
for either on a `.decode()` is a `TypeError` there and here -- not the
`LookupError` an unregistered name gets.

| handler | `.encode()` | `.decode()` |
|---|---|---|
| `strict`, `ignore`, `replace` | yes | yes |
| `backslashreplace` | yes | yes -- one `\xNN` per byte of the error |
| `xmlcharrefreplace` | yes | `TypeError`, as in CPython |
| `namereplace` | **`LookupError`** -- jinja2 writes `b'\N{EURO SIGN}'` | `TypeError`, as in CPython |
| `surrogateescape`, `surrogatepass` | yes: nothing a Go string holds is a surrogate, so both are `strict`, which is what CPython's do for the same input | **`LookupError`** -- jinja2 answers with a lone surrogate |

`namereplace` is refused for the same reason as `\N{...}` above: it needs
CPython's Unicode name database, which is data to generate and carry rather
than arithmetic.

`surrogateescape` and `surrogatepass` exist to carry bytes that are not valid
UTF-8 through a decode, as lone surrogates in the range U+DC80-U+DCFF or as the
surrogate the bytes encode. A Go string is UTF-8 and cannot hold a lone
surrogate at all, so there is nothing to hand back; refusing is the honest
answer, and substituting anything else would be the silent wrong one. On an
encode the question never arises, because no character that reaches the encoder
is a surrogate -- which is exactly when CPython's own handlers fall back on
`strict`, so that direction matches.

#### These three are found when the template compiles

An error handler is looked up only when a character actually needs it. CPython
works that way and gojja2 matches, which makes these three the *latent* kind of
divergence: a template naming one compiles, renders, passes its tests, and
raises the first time a payload reaches the handler.
`.encode("ascii", "namereplace")` is fine for every ASCII string and fails on
the first accented letter; `.decode("utf-8", "surrogateescape")` is fine for
every well-formed input and fails on the first malformed byte, which is the
moment nobody wants a surprise.

So compiling a template looks for them and puts what it finds on
[`Template.Unsupported`](extending.md), whether or not anyone asked:

```go
tmpl, err := env.GetTemplate("page.html")
for _, u := range tmpl.Unsupported() {
    log.Printf("%s:%d: %s", u.Template, u.Line, u.Error())
}
```

`WithUnsupportedReport` routes each finding somewhere as it is found -- gojja2
has no logger of its own and writes to no stream -- and
`WithUnsupportedLeniency(RefuseUnsupported)` makes it a compile error instead,
so the template cannot reach production carrying a failure only some inputs
show. Reporting is the default: refusing would reject a template that works
today, having never handed its handler anything to do.

The check reads the handler where a template writes one -- positionally, as
`errors=`, or assembled from constants, since it runs after folding. A handler
that is genuinely dynamic, `s.encode("ascii", h)`, is not guessed at; that one
still surfaces as the `LookupError` it always did.

## Where the answer was never reproducible

jinja2 does not match these either: the output embeds a memory address, or a random draw. The conformance suite marks such cases *ungradable* and reports them apart from the pass rate, because a case nothing can pass is not a measure of anything.

### Objects whose repr carries an address

jinja2 gives most of the objects a template can reach a `__repr__` of their
own -- `Namespace`, `LoopContext`, `Macro`, `TemplateReference`,
`TemplateModule` -- and three of them none at all. `Cycler`, `Joiner` and
`BlockReference` fall back to Python's default, `<module.Qualname object at
0xADDR>`. gojja2 prints the same form with the address of its own object, on
the same terms as `|pprint` above: the form is reproducible and the address is
not, so the conformance suite treats it as ungradable.

```jinja
{{ joiner() }}                  <jinja2.utils.Joiner object at 0x...>
{% block b %}x{% endblock %}
{{ self.b }}                    <jinja2.runtime.BlockReference object at 0x...>
{{ self.b() }}                  x
```

Only a *call* renders a block. Printing the reference prints the object, which
is why `{% block x %}{{ self.x }}{% endblock %}` terminates.

### A comparison that runs out of stack under 3.14

```jinja
{% set l = [] %}{% set m = [] %}{% do l.append(m) %}{% do m.append(l) %}{{ l == m }}
```

A comparison names itself when the stack runs out, unlike the *call* path, whose
suffix 3.12 dropped:

```
3.11, 3.12, 3.13:  maximum recursion depth exceeded in comparison
3.14:              Stack overflow (used 8156 kB) in comparison
```

gojja2 says the first on every version. The number in 3.14's is the stack *this
machine* had -- it differs between two runs of CPython on two machines, and
there is nothing for gojja2 to compute it from. The corpus cannot hold the case
for the same reason a `|pprint` id cannot;
`conformance/recursion_compare_test.go` pins the sentence instead.

### `|pprint` of a value that contains itself

```jinja
{{ cyclic|pprint }}
```

CPython's `pprint` marks a container it has already entered as
`<Recursion on dict with id=131095544303808>`. The id is the object's address,
which differs between two runs of CPython itself, so this case is no more
gradable than `lipsum()` is. gojja2 prints the same form, with the address of
its own container -- everything but the number is identical:

```
jinja2: [<Recursion on list with id=135154434729280>,
         'a string long enough that pprint will not fit this on one line']
gojja2: [<Recursion on list with id=18132333323128>,
         'a string long enough that pprint will not fit this on one line']
```

A cyclic value reaches `pprint` at all only when its `repr` is too wide to
print on one line; a small one collapses to `{...}` first, which is exact.

This sentence was untrue for a while, and nothing noticed, because the corpus
cannot hold a case whose answer carries an id that changes between two runs of
CPython. `conformance/recursion_repr_test.go` asserts it outside the corpus
instead: it blanks the id and compares the rest against CPython's own output,
so the form is pinned exactly and only the number is allowed to differ.

### The order a set prints in

```jinja
{{ d.keys() - [] }}
```

`d.keys() - xs` is the only set arithmetic a template can write as an operator
-- jinja2's grammar has no `&` or `^` and `|` is the filter operator -- though
the result can be the left operand of another difference, and a set's seventeen
methods reach the rest: union, intersection, the three ..._update, add, remove,
pop and the others. The operations themselves match: the same elements, the
same refusals, the same messages.

The *order* it prints in does not, and cannot. A set is unordered and CPython's
repr follows its hash table, which string hashing randomises per process. Three
runs of the same expression on the same four keys:

```
{'b', 'delta', 'a', 'c'}
{'c', 'delta', 'a', 'b'}
{'a', 'b', 'delta', 'c'}
```

gojja2 sorts by each element's repr, so it prints `{'a', 'b', 'c', 'delta'}`
every time. Sorting by repr rather than by value is what makes it total: a set
may hold numbers and strings together, which Python cannot order and a repr can.

A set of one element, and the empty `set()`, have only one spelling either way
and are graded against CPython as usual -- as are the length, the membership
test, the truthiness, the equality, and `|list|sort`. Only the multi-element
repr is unreproducible, and `conformance.Comparable` screens it out of the
generated differential for the same reason it screens an address.

Two things follow the order rather than the repr, and both are it again rather
than a second divergence. `s.pop()` takes the first element in the set's own
order, so it takes a different one from CPython unless the set holds one thing;
the cases here pop until it is empty and compare the sorted result. And a
`|sort` that fails names the pair it happened to reach first, so
`{{ s.add(1) }}{{ s|list|sort }}` reports "'int' and 'str'" where CPython
reports "'str' and 'int'".

`|pprint` is the exception, and it is graded. pprint does not use the repr's
order: it sorts with `pprint._safe_key`, which is the elements' own ordering
wherever they have one, so a set of integers pretty-prints as `0, 1, 2` and a
set of strings in string order -- both reproducible, and both matched here
exactly. A set holding *mixed* types is not: `_safe_key` falls back to
`(type name, id(obj))`, and an address is an address. gojja2 keeps its repr
order for that case, which is at least the same one twice.

Of the two answers gojja2's is the reproducible one, which is a reason to prefer
it rather than a claim that CPython is wrong: an unordered collection has no
order to be right about.

### lipsum() and random

`lipsum()` and the `random` filter draw from a random source. Their output
cannot match CPython's and is excluded from conformance comparison.

The *shape* of `lipsum()`'s output is not excluded, and is reproduced: the
paragraph length, where the commas and full stops fall, the capital after each
stop, the rule that no word follows itself, and the single newline between HTML
paragraphs against the blank line between plain ones. `TestLipsumShape` checks
those invariants over enough paragraphs that a missing rule cannot hide behind
the randomness, and the distributions were compared against CPython's before it
was written.

## Differences you would have to construct on purpose to see

Real, asserted, and not reachable from a template anyone would write.

### `is sameas` on two literals

`sameas` is Python's `is`, and gojja2 answers it by identity for containers and
by value for everything else. That matches CPython wherever the two sides came
from anywhere but a literal:

```jinja
{% set a = 1.5 %}{% set b = 1.5 %}
{{ a is sameas(b) }}        True in both
{{ 1.5 is sameas(1.5) }}    True here, False on CPython
```

The difference is jinja2's generated code, not the language. One of the two
literals ends up inside a marshalled tuple, whose element is a distinct object
from the standalone constant, so CPython's `is` says no. The same expression
written over variables says yes on both. Modelling it would mean modelling
CPython's marshalling, and the expression it changes the answer to is a
tautology.

The singletons CPython really guarantees are matched: None, True, False, and
the empty tuple.

The same question is asked, less visibly, by `==` on two bound methods: CPython
says they are equal when their functions are the same and their receivers are
the same *object*. gojja2 compares a scalar receiver by value, which agrees for
a variable, for a small int CPython caches, and for everything a template writes
on purpose:

```jinja
{% set x = 'hello' %}{{ x.upper == x.upper }}   True in both
{{ (1).bit_length == (1).bit_length }}          True in both -- 1 is cached
{{ (300).bit_length == (300).bit_length }}      True here, False on CPython
{% set n = 300 %}{% set m = 300 %}{{ n.bit_length == m.bit_length }}  True in both
```

The third is two objects because jinja2 folds each literal into one of its own;
the fourth is one because CPython's compiler shares the constant between the two
assignments. Following it would mean modelling the small-int cache, the fold and
the compiler's constant table together.

### A NaN handed in from Go twice

A NaN is the only value whose identity a template can observe. Everywhere else
`is` implies `==`, so nothing that asks "the same object, or an equal one" --
which is what every container comparison asks -- can tell the two apart. A NaN
is equal to nothing, itself included, so for a NaN the two questions come apart:

```jinja
{% set a = 1e308 %}{% set b = a * 10 %}{% set x = b - b %}
{{ x == x }}        False -- the operator is float.__eq__
{{ [x] == [x] }}    True  -- the list compares its elements by identity first
{{ x in [x] }}      True
{{ {x: 1}[x] }}     1     -- a NaN hashes by identity, as CPython has since 3.10
```

gojja2 gives every NaN an identity where the value is made, so all of that, and
`|unique`, `list.index`, `list.count`, `loop.changed` and the dict views, answer
as CPython does -- including the other side of the rule, that two NaNs computed
separately are two objects and equal to neither each other nor themselves inside
a container.

What it cannot reproduce is sharing that happened before the values arrived. A
Go context is not a Python object graph:

```go
map[string]any{"n": math.NaN(), "ns": []any{math.NaN()}}
```

is two NaNs however it was written, because a `float64` carries no identity to
share -- and even `n := math.NaN(); map[string]any{"n": n, "ns": []any{n}}` is
two, for the same reason. Python would have had one object in both places, and
`{{ n in ns }}` is True there and False here. Within a render nothing is lost:
one context value is one object however often the template reads it, which is
what the four other cases in `TestNaNKeepsItsIdentityWithinARender` hold.

The corpus cannot show any of this either way -- its contexts are JSON, which
has no NaN -- so the Go test is where it is asserted, and
`numbers/nan_identity_*` grades the twenty-seven shapes a template can build for
itself.

### Comparison order inside a long sort

Sorting values that cannot be compared raises, and the message names the two
operands the sort happened to reach first. gojja2 reproduces CPython's order
exactly for lists of 64 elements or fewer, which is where CPython sorts a list
as a single run.

Above that, CPython splits the list into runs and merges them, and gojja2 uses
a stable sort of its own. The result is identical; only which pair a failing
comparison names can differ.

### An `{% autoescape %}` flag that is a StrictUndefined

`{% autoescape nope %}` stores what it is given and jinja2 asks for its truth
only where a run-time output has to decide whether to escape. Under
`StrictUndefined` the truth of an undefined raises, so the error comes from the
first such output inside the body, not from the tag:

```jinja
{% autoescape nope %}{{ '<' }}{% endautoescape %}     "<" on CPython, UndefinedError here
{% autoescape nope %}{% endautoescape %}              "" on CPython, UndefinedError here
{% autoescape nope %}{{ 1 }}{% endautoescape %}       "1" on CPython, UndefinedError here
{% set x = '<' %}{% autoescape nope %}{{ x }}{% endautoescape %}   UndefinedError in both
```

Anything in the body that escapes at run time raises in both, including raw
template text, so the difference is confined to a body whose output is empty or
was folded to a constant before the render. The flag's truth is asked once, at
the tag, because everything downstream reads a boolean; deferring it would mean
carrying a "raises when asked" flag through every one of the sites that read the
escaping setting. `testdata/corpus/divergence/autoescape_flag_strict_folded_output.jj2`
and its two siblings are listed in `testdata/known_failures.txt`.

### A macro's view of the loop it was defined in

jinja2 compiles a loop body into the function around it, so a macro written
inside a loop reads the loop's variables the way a Python closure reads a cell:
whatever they hold at the moment the macro is *called*. gojja2 builds a fresh
frame for every iteration, and a macro keeps the one it was defined in.

```jinja
{% set ns = namespace(f=[]) %}
{% for i in [1, 2] %}{% macro m() %}{{ i }}{% endmacro %}{% set _ = ns.f.append(m) %}{% endfor %}
{{ ns.f[0]() }}|{{ ns.f[1]() }}     "missing|missing" on CPython, "1|2" here
```

Called while the loop is still running, the same macro reads the *current*
iteration: `[{{ ns.f[0]() }}]` written after the append prints `[1][2]` on
CPython, where the macro from the first pass reads the second pass's value, and
`[1][1]` here. A name assigned in the body with `{% set %}` behaves the same way,
except that after the loop it is unbound and prints as nothing. A macro that is
called within the iteration that defined it, which is what a macro in a loop is
for, agrees.

The cause is jinja2's code generator, not the language: matching it means giving
a loop one shared frame, resetting the loop's names to jinja2's `missing`
sentinel when it ends, and letting that sentinel print. Nothing else in a
template can tell the two apart, so gojja2 keeps the frame per iteration, which
is the one that agrees whenever the macro is used where it was defined.
`testdata/corpus/divergence/macro_reads_the_loop_variable_after_the_loop.jj2` and
its two siblings are listed in `testdata/known_failures.txt`.

### A `{% set %}` block writing to a name that was never set

```jinja
{% set nosuch.v %}x{% endset %}
```

The two `{% set %}` forms are different operations, and this is the only place
they part company from jinja2.

`{% set ns.v = value %}` requires a namespace: jinja2's `visit_Assign` emits an
`isinstance` check for every `ns.attr` in the target *before* the code that
evaluates the value, and gojja2 does the same, so both raise
`cannot assign attribute on non-namespace object` -- and both raise it in
preference to whatever the value would have raised.

`{% set ns.v %}...{% endset %}` emits no such check. `visit_AssignBlock` writes a
bare `ref[attr] = ...`, so it is a plain item assignment: it **succeeds** on a
dict, and otherwise fails the way Python's `__setitem__` fails. gojja2 matches
that, naming the same type in the same words:

| the name holds | both engines say |
|---|---|
| a dict | nothing -- the key is set and the template renders |
| a namespace | nothing -- likewise |
| a list | `TypeError: list indices must be integers or slices, not str` |
| a tuple, int, float, bool, str, Markup, range, or None | `TypeError: '<type>' object does not support item assignment` |

The exception is a name **that was never set at all**. jinja2's generated code
holds the sentinel its resolver returns for an unknown name rather than an
`Undefined`, because nothing has read the name, and that sentinel's class is an
internal one:

```
jinja2: TypeError: '_MissingType' object does not support item assignment
gojja2: TypeError: 'Undefined' object does not support item assignment
```

Same error, same class, at the same point in the render. Matching the wording
exactly would mean naming a private class of jinja2's implementation that has no
counterpart here, so gojja2 names what it actually holds.
`testdata/corpus/errors/nsref_block_undefined.jj2` is listed in
`testdata/known_failures.txt` to keep it that way.

### Python object introspection

`__class__` is implemented. Every value answers it with a type object that has
a name, a repr and equality:

```jinja
{{ true.__class__ }}            <class 'bool'>
{{ nope.__class__.__name__ }}   Undefined
{{ ('x'|safe).__class__ }}      <class 'markupsafe.Markup'>
```

That is a statement about gojja2's own value model, and it is inert: there is
nothing behind it. One consequence is visible: under autoescape, CPython's
`escape()` finds `__html__` on the *class* of an imported module and calls it
unbound, so `{{ m.__class__ }}` raises where gojja2 prints the class. That is
an artifact of the class object existing at all.

`__doc__` is not. CPython answers it with the docstring of the built-in type --
several paragraphs of English that change between Python releases -- and gojja2
renders nothing, as it does for any attribute it has not got:

```jinja
{{ [1].__doc__ }}     "Built-in mutable sequence. ..." on CPython, "" here
```

Carrying those strings would mean carrying a copy of CPython's documentation
and keeping it in step with the version being compared against, to answer an
attribute that says nothing about the value. A 352-case sweep of attribute and
item lookup across every kind found this and nothing else.

`__mro__` is not implemented either, and that sweep did not find it because it
asked values rather than the class objects behind them:

```jinja
{{ (1).__class__.__mro__ }}     "(<class 'int'>, <class 'object'>)" on CPython, "" here
```

It belongs with `__subclasses__` rather than with `__class__`: the method
resolution order is the first step of the walk from a value to the interpreter's
builtins, which is the escape route the two sandbox tests above document. A class
object here has a name, a repr and equality, and nothing that leads anywhere.

Calling one is *not* the same decision, and is implemented: `{{ n.__class__() }}`
is `0` and `{{ s.__class__(lst) }}` is the list's repr, exactly as in Python.
Construction is the one thing a type object does that leads nowhere further into
the interpreter -- an int, a str or a list is an ordinary value -- so refusing it
bought no safety and cost conformance.

A type object also carries its class's methods, unbound, and that is implemented
too, for the same reason: a method descriptor leads back to the value it is
called on and no further.

```jinja
{{ d.__class__.items }}        "<method 'items' of 'dict' objects>"
{{ d.__class__.items(d) }}     "dict_items([('a', 1)])"
{{ s.__class__.upper('a') }}   "A"
{{ d.__class__.items() }}      TypeError: unbound method dict.items() needs an argument
{{ s.__class__.upper(1) }}     TypeError: descriptor 'upper' for 'str' objects doesn't
                               apply to a 'int' object
{{ s.__class__.upper('a','b') }}  TypeError: str.upper() takes no arguments (1 given)
```

The first argument is the instance and everything after it is the method's own,
so an arity error is reported by the method rather than by the descriptor -- and
a descriptor from the wrong class refuses before the method runs. A name the
class does not have is undefined rather than an error, which is what makes
`{{ n.__class__|dictsort }}` say `type object 'int' has no attribute 'items'`
while `{{ d.__class__|dictsort }}` reaches the descriptor and reports the
unbound-method call, matching CPython in both directions.

The same holds for the type objects of gojja2's own objects that stand for
built-in types -- `range`, `set`, the three dict views and `mappingproxy`:

```jinja
{{ range(3).__class__.index(range(3), 2) }}    "2"
{{ d.keys().__class__.isdisjoint(d.keys(), 'z') }}  "True"
{{ d.items().__class__.mapping }}  "<attribute 'mapping' of 'dict_items' objects>"
```

Five corners of it are not implemented, and each is in
`testdata/known_failures.txt`:

```jinja
{{ s.__class__.__len__ }}          a slot wrapper on CPython, undefined here
{{ n.__class__.__abs__(lst) }}     a slot wrapper words its refusal differently:
                                   "requires a 'int' object but received a 'list'"
{{ lst.__class__.nope }}           the generic alias list['nope'] on CPython
{{ yes.__class__.conjugate(yes, 1) }}  "int.conjugate()" there, "bool" here
{{ c.__class__.next(c) }}          Cycler.next on CPython, undefined here
```

The first two are dunders, which gojja2 does not expose on a value either, so
the type object has none to hand out. The third is the generic-alias divergence
recorded above for `list[...]`, reached through jinja2's attribute fallback
instead of through a subscript -- and the reason a name a class does not have is
undefined here. The fourth is an arity message: bool defines no methods of its
own, so CPython's descriptor is int's and says so, where gojja2's delegates to
the receiver's own bound method and words it after the receiver.

The fifth is the classes jinja2 writes in Python -- `Cycler`, `LoopContext`,
`Joiner` and `Macro`. Their methods are plain Python functions, which is a
different object from a built-in's method descriptor: its repr is `<function
Cycler.next at 0x...>`, and it accepts any `self` at all and fails *inside the
body* -- `Cycler.next(1)` is "'int' object has no attribute 'current'", because
that is the first thing the body asks of its receiver. Reproducing that means
reproducing each method's body line by line against an arbitrary receiver, for
a spelling (`loop.__class__.cycle(loop, 'a')`) nothing writes in place of
`loop.cycle('a')`. The type objects for these classes carry no methods, and the
name is undefined.

A markupsafe `Markup` is left out of the same feature for a different reason:
the methods it inherits from str are str's descriptors, but the ones it
overrides are plain Python functions whose repr carries a memory address, which
no corpus and no differential can grade.

Of the methods Markup adds to str, `striptags()` and `unescape()` are
implemented and graded. The class method `Markup.escape` and the `__html__`
protocol method are not: `{{ ('a'|safe).escape('<') }}` and
`{{ ('a'|safe).__html__() }}` answer an undefined attribute where jinja2 answers
`Markup('&lt;')` and `Markup('a')`.

Everything else a type object answers matches: `__name__`, `__qualname__`,
`__module__`, its repr, equality with another type object *and with the class
global it is* (`{{ d.__class__ == dict }}` is true), calling it, and its
behaviour as a dict key or in `unique`. Ordering two of them is a `TypeError` on
both sides.

Two of Jinja's sandbox-escape tests go further, and those are not implemented:

```jinja
{{ foo.__class__.__subclasses__() }}
{{ "{0.__call__.__builtins__[__import__]}" | attr("format")(x) }}
```

The first wants the list of a class's subclasses; the second walks from a bound
method to the interpreter's builtins and out to `__import__`. Neither has a
counterpart in Go -- there is no class hierarchy to enumerate and no import
machinery to reach. Matching them would mean building a decoy of the escape
route the tests exist to document, which would be worse than not having one:
the next reader would have to work out that the ladder leads nowhere.

These two are in `testdata/known_failures.txt`, alongside the two DeepSeek-R1
chat templates recorded above under "Lazy sequence filters", and this is the
one place where not matching CPython is the point.

Why there is no sandbox at all — and what draws the line instead, which is not
what most readers assume — belongs to [scope.md](scope.md#out-of-scope). This
section is only about what `__class__` answers.

Note what is *not* in this category. `str.format`'s replacement fields take
attribute and index accessors -- `"{0.foo}"`, `"{user[id]}"` -- and that is an
ordinary documented feature, implemented and matched. The tests above only use
it as a route to `__class__`.

### `len()` of a very long range

```jinja
{{ range(-9223372036854775808, 0)|length }}
```

CPython raises `OverflowError: Python int too large to convert to C ssize_t`,
because `len()` narrows its result to a `Py_ssize_t`. gojja2 reproduces that,
including the wording and the exact boundary: a length of `2**63-1` is returned
and `2**63` raises.

Internally the length is still computed exactly, in arbitrary precision, and
only clamped where a Go `int` is required -- iterating or ordinary indexing.
Asserted by `TestRangeLengthDoesNotOverflow` and graded against CPython over
1,452 start/stop/step combinations.

That clamp was once described here as unobservable, on the grounds that a loop
over a range that long is stopped by the render budget long before the count
matters. It is not, and probing found two ways to see it:

- CPython's `reversed()` of a sequence is `__len__` and `__getitem__`, and
  `range_reverse` computes the first element arithmetically *without* narrowing
  the length -- so `{{ range(2 ** 70)|last }}` answers `1180591620717411303423`
  where `|length` on the same range raises. gojja2 walked to it and spent the
  whole iteration budget. It now indexes in arbitrary precision, through
  `value.BigSequence`; graded by `globals/range_wide_last` and
  `TestLastIndexesRatherThanWalking`.
- every filter whose jinja2 implementation builds a list narrows the length
  first -- `PyObject_LengthHint` for `list()` and `sorted()`, `len()` for
  `random.choice()` -- so `|list`, `|sort`, `|slice`, `|groupby` and `|random`
  over such a range raise the `OverflowError` above and never walk. gojja2 asks
  the same question before it materialises; graded by the `errors/wide_range_*`
  cases. The ordering is CPython's too: `seq = list(value)` is `do_slice`'s
  first statement, so `{{ range(2 ** 70)|slice(0) }}` is the OverflowError and
  not the ZeroDivisionError.

What remains observable is the lazy-filter fork below: jinja2's `|reverse`
answers an iterator, which `|first` reads in constant time, while gojja2's
answers a list -- and a list of 2**70 elements cannot exist, so
`{{ range(2 ** 70)|reverse|first }}` runs out of budget here where CPython
prints the last element. That is the same choice as everything else in
[Lazy sequence filters](#lazy-sequence-filters), reached from the other end.

## Differences in how the host is treated

These do not change what a template renders. They change what the *caller* sees around the render, which is worth knowing before you port a deployment rather than a template.

### A render does not mutate the caller's data

```jinja
{% do xs.append(9) %}
```

`{% do %}` is jinja2's `do` extension and is off by default in both engines --
`gojja2.New(gojja2.WithExtensions("do"))` turns it on, and without it both
report the same `Encountered unknown tag 'do'.` On a bare environment
`{% set _ = xs.append(9) %}` does the same thing, and everything below holds for
either spelling.

jinja2 hands a template the caller's real objects, so a template that appends
to a list appends to *your* list, and the next render starts from the longer
one:

```python
xs = [1]
t.render(xs=xs)   # [1, 9]
t.render(xs=xs)   # [1, 9, 9]   -- and xs is now [1, 9, 9]
```

gojja2 converts what it is given, so the same template renders `[1, 9]` both
times and the caller's slice is still `[1]`. Maps and nested containers are the
same. This is deliberate: a template is usually the least trusted part of a
program, and several filters mutate in place in jinja2 -- `do_indent` extends
the list it is handed -- so the alternative is a template reaching into the
host's state by accident.

Two things are still shared, on purpose:

| what | shared? | why |
|---|---|---|
| a slice, map or nested container | no, converted | a template cannot corrupt the caller |
| a global registered on the Environment | **yes** | it lives on the Environment, as in jinja2 |
| a host object exposed by pointer | **yes** | its methods are the point; copying it would break every stateful object |

This holds for `Render` and `RenderString`, which convert what they are given.
It does **not** hold for `RenderValues`, whose whole purpose is to skip that
conversion: the values handed to it are used as they are, so
`{% set _ = lst.append(9) %}`, `{% set _ = d.update(x) %}` and
`{% set d.v %}...{% endset %}` write through to the caller's value and stay
written -- after the render, and for every later render given the same values.
That is the documented trade for skipping the conversion, and it is why a
`vars` map prepared once and reused across requests should be built per render
instead if the templates are not trusted.

A list written *in the template* is rebuilt per render for the same reason: it
is part of the compiled tree, which every render of that template shares --
including renders on other goroutines at the same time. `concurrency_test.go`
pins all of it, and the version without the rebuild fails there with one
goroutine's values appearing in another's output.

### finalize and a constant print

With `WithFinalize` **and** autoescaping on, a print whose expression is
entirely constant renders differently:

```jinja
{% autoescape true %}{{ 'a' }}{% endautoescape %}
```

jinja2 gives `<Markup('a')>` and gojja2 gives `&lt;&#39;a&#39;&gt;`, for a
hook spelled `lambda v: "<%r>" % (v,)`.

jinja2 has two orders here, and which one applies depends on whether the
expression folded. At runtime its code generator emits
`escape(environment.finalize(value))`; at compile time `_output_child_to_const`
does the reverse, `finalize(escape(const))`, and emits the result without
escaping it again. So the same expression is finalized before escaping when it
is written as a variable and after escaping when it is written as a literal.

gojja2 uses the runtime order for both, because it does not run `finalize` at
compile time at all: the hook is arbitrary Go supplied by the embedding
program, it may not be pure, and folding would call it once per compile rather
than once per render. Reproducing the asymmetry would mean accepting that
trade to copy a wart.

Without a finalize hook, or without autoescaping, the two agree. Asserted by
the tests in `finalize_test.go`.

### Subscripting the `dict` global

```jinja
{{ dict['k'] }}   {{ dict[0] }}   {{ dict.nosuch }}
```

Python 3.9 made a builtin type subscriptable as a *type annotation*: `dict[str]`
is a `types.GenericAlias`, not a lookup, and its repr is `dict[str]`. So
CPython renders `dict['k']` for the first two, and jinja2's attribute fallback
turns the third into `dict['nosuch']` as well.

gojja2's `dict` is a callable that builds a dict, and nothing here models
generic aliases, so all three are undefined and render empty. Calling it --
which is what the global is for -- is identical in both.

The other class globals are unaffected: `range['k']` raises in CPython too,
because only a handful of builtins accept the annotation form.

`__class__` reaches the same two classes a second way, and diverges the same
way. `lst` is `[1]` and `d` is `{'a': 1}` here, from the context rather than
written as literals, because both engines fold a constant expression and a fold
hides the difference:

```jinja
{{ lst.__class__['a'] }}   {{ lst.__class__[1:] }}   {{ d.__class__.a }}
```

CPython renders `list['a']`, `list[slice(1, None, None)]` and `dict['a']` --
the third because jinja2 retries a missing attribute as an item. gojja2 renders
the first and third empty and raises `type 'list' is not subscriptable` on the
slice.

Every other class reachable from a value agrees exactly, including two wordings
CPython reserves for a type object:

| expression | CPython |
| --- | --- |
| `{{ f[1:] }}` | `'float' object is not subscriptable` |
| `{{ f.__class__[1:] }}` | `type 'float' is not subscriptable` |
| `{{ f\|dictsort }}` | `'float' object has no attribute 'items'` |
| `{{ n.__class__\|dictsort }}` | `type object 'int' has no attribute 'items'` |

Five filters reach the second of those -- `dictsort`, `xmlattr`, `wordwrap`,
`wordwrap(wrapstring=...)` and `urlize(rel=...)`. A 161-shape sweep over every
subject the template generator writes and every accessor it can follow one with
found the generic alias above and nothing else.

### A folded infinity jinja2 writes out

jinja2's optimizer folds a constant and its code generator writes the result
into the generated Python **as its repr**. A float infinity reprs as `inf`,
which is not a Python name, so the module raises as soon as that line runs:

```jinja
{% set v = 'inf'|float %}{{ v }}
{{ x|default('inf'|float) }}
```

Both raise `NameError: name 'inf' is not defined` on CPython at render. gojja2
answers `inf`.

It depends on where the constant lands, not on the value: `{{ 'inf'|float }}`,
`{{ ('inf'|float) + 1 }}` and `{{ 1e400 }}` all print `inf` on both sides,
because a print puts the value in the module's constant table rather than
writing it as source. A `{% set %}` and a filter's default argument are written
out. `nan` does the same thing for the same reason.

This is one of the few places gojja2 renders where CPython cannot, so it is
listed in `testdata/known_failures.txt` rather than fixed: reproducing it would
mean refusing a number a template legitimately computed, to match a limitation
of the other implementation's code generator.

### Which line an error inside a multi-line tag names

A tag's expression may span lines, and both engines parse and render those the
same. They differ on which line an error *inside* one is reported at:

```jinja
{% if true
   and x|nope %}X{% endif %}
```

jinja2 says line 1, gojja2 says line 2.

gojja2 names the line of the token that failed. jinja2 names the line its code
generator attributed the surrounding statement to, which for an `{% if %}`,
`{% set %}` or `{% with %}` is where the tag opened. The two agree everywhere a
tag fits on one line -- which is every single-line template -- and they agree
for an error in a tag's *body* rather than its expression.

They also agree in the places jinja2 happens to attribute to the expression
instead of the statement: a print tag (`{{\n nosuch|nope\n}}` is line 2 in
both), a loop filter (`{% for i in [1]\n if i|nope %}` is line 2 in both) and a
macro's defaults. Matching jinja2 everywhere would mean reproducing where its
code generator emits each line marker, which is a fact about its compiler
rather than about the template -- the same kind of detail as the recursion
wordings in [limits.md](limits.md#a-bound-on-nesting-depth).

Both engines raise the same error with the same message; only the line number
attached to it differs. Asserted by the tests in `multiline_test.go`.

### What a missing template's error says

Both raise `TemplateNotFound` for the same names. jinja2's `FileSystemLoader`
adds the directories it looked in:

```
TemplateNotFound: 'missing.html' not found in search path: '/srv/app/templates'
```

gojja2's `FSLoader` reports the template name alone, because there is no search
path to name: it wraps an `fs.FS`, which may be a `zip.Reader`, an
`embed.FS`, a `fstest.MapFS` or anything else with no filesystem path behind it
at all. `DictLoader`, `ChoiceLoader` and `PrefixLoader` have the same shape in
jinja2 and report the name alone there too.

The error's class and the name it carries are identical, so anything that
branches on the error -- `errors.Is(err, errs.TemplateNotFound)`,
`{% include ... ignore missing %}`, `ChoiceLoader` falling through -- behaves
the same. Only the human-readable detail differs, and only for the filesystem
loader. Asserted by `TestFSLoaderMissNamesOnlyTheTemplate`.

### No automatic template reload

jinja2's `Environment` takes `auto_reload=True` and recompiles a template whose
source has changed. gojja2 does not, because its `Loader` interface returns
source and nothing else -- there is no freshness to consult, and inventing one
would mean every loader implementing a second method.

`Environment.ClearCache` and `Environment.ForgetTemplate` are the supported way
to pick up an edit; the template cache is otherwise a bounded LRU, defaulting to
the same 400 entries jinja2 uses.

### The default autoescape extension set

```go
gojja2.SelectAutoescape()        // html, htm, xml, xhtml
```

jinja2's `select_autoescape()` defaults to `html, htm, xml`. gojja2 adds
`xhtml`.

The extension set only ever turns escaping *on* -- a name matching nothing
falls through to `Default`, which is `false` -- so a superset is strictly safer
than jinja2's, and it is what gojja2 shipped before the set was normalised.
Narrowing it to match would make this package escape less than it used to, which
is the wrong direction for the one setting whose failure mode is cross-site
scripting.

Everything else about the policy follows jinja2 exactly, including the parts
that are easy to get wrong: matching ignores case on both the template name and
the configured extensions, a leading dot is optional, matching happens on a
whole extension rather than on any trailing substring, and a template compiled
from a string is escaped by default (jinja2's `default_for_string=True`).
Asserted by the tests in `autoescape_test.go`.

## The bounds gojja2 puts on a render

Moved to [limits.md](limits.md): the iteration and output budget, the nesting
bound, the allocation ceiling, how deep a value may be before printing it fails,
and how promptly a cancelled render stops. They are safety controls rather than
behavioural choices -- CPython has no budget to exceed, so there is nothing
there to match -- and they are read by a different person for a different
reason.
