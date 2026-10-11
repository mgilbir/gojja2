# How the suite checks itself

Most of this repository's tests compare gojja2 with CPython. This page is about
the checks on those checks: the tree a caller can query is compared with
jinja2's own byte for byte; the analysis and the render budget are broken on
purpose to see whether anything notices; and the error messages no case has
ever produced are counted, because a message never compared reads as agreement
on every interpreter. Reach for it when you are changing the syntax tree, the
dataflow analysis, the budget or the corpus tooling, or when one of these
tools reports something and you need to know what it means.

The how-to -- which target to run and when -- is in
[contributing.md](contributing.md#how-the-suite-checks-itself). What follows is
what each check proves and the history of what it found, kept because the
reason a check exists is usually a defect it caught.

## The exposed tree is compared to jinja2's, byte for byte

`Template.Syntax` gives a template's structure in the vocabulary of the `syntax`
package, so a caller can ask their own questions of it.
`tools/oracle/syntax_emit.py` writes **jinja2's** parse tree into the same
vocabulary, `make syntax` records it in `testdata/syntax.jsonl`, and
`TestSyntaxMatchesTheReference` requires the two to encode identically for every
committed case.

Byte equality is the strong form of the guarantee, and the reason the vocabulary
is normalised rather than faithful to either tree. An analysis that agrees proves
the two see eye to eye about *that* analysis; trees that agree prove it for every
question either will ever be asked, including the ones nobody has written. That
is what makes the tree worth exposing at all — a caller writing a query gets the
answer jinja2 would have given, and this is the evidence.

The normalisation lives in the two emitters, never in the comparison. jinja2 has
nine classes for binary operators and the vocabulary has one node with the
operator on it; jinja2 wraps `{% autoescape %}` in an explicit scope and gojja2
leaves it implicit, so the Go emitter adds it, because the body **is** a scope. A
difference that survives to the comparison is either a real disagreement about
what the parsers understood or a gap in the vocabulary. Both are worth finding,
and nothing is allowed to forgive one.

The scope and binding facts are compared the same way, and in the same file. The
engine gets them from `frameLocals`, the one implementation of jinja2's
first-mention rule in this codebase; the reference gets them from
`jinja2.idtracking`, the module jinja2's own code generator uses. Neither side
reimplements the rule, which is the part that is invisible until it is wrong.

Nodes are named by their position in a pre-order walk, which costs nothing to
agree on because the trees are already identical — a fact worth noticing: the
second comparison is only possible because the first one passes.

Two properties hold whatever the answer is, and so need no second opinion at
all -- which is what makes them the only checks a mistake *shared* by both
implementations cannot hide from. Analysing the same template twice must give
the same answer, and renaming its variables must rename the answer and change
nothing else. The analysis is entitled to recognise a handful of names --
`namespace`, `loop`, `caller`, the environment's globals -- and nothing else,
and the renaming is done on the tree rather than in the source so there is no
chance of rewriting the inside of a string literal and comparing two templates
that were never the same one.

Writing the rename was itself worth it: a macro's name, an import's target and a
from-import's aliases are bindings carried as *attributes* rather than as name
nodes, and a block's, a filter's, a test's and a keyword argument's names are not
bindings at all. The test has to know the difference, so it is written down.

`make mutate` asks the question a passing suite cannot answer. A suite that
passes says the code does what the tests ask; it does not say the tests ask for
much. So each place the analysis records something -- an effect applied, a
dependency drawn, a value emitted -- is taken out in turn, and anything that
still passes is a line no test constrains.

The tree the analysis runs on is mutated too, at every place `syntax_build.go`
records a Def, a Use, a Scope or a context name. Those are further upstream than
anything in `dataflow/`: a binding that goes unrecorded is a name the analysis
cannot see, which is indistinguishable to it from a name that does nothing.

129 mutations, all 129 *exercised*, none surviving: 54 over the analysis and
the tree it runs on, 75 over the budget below. The count of exercised ones is
reported separately because it used to be smaller than the total without saying
so: some sites are the only reader of a loop variable, so commenting the line
out left something declared and not used, the build failed, and the tool called
that "uncompilable" and counted it with the ones nothing survived. A site that
was never mutated is not a site nothing could break. Those sites now drop the
*recording* and keep the arguments -- `a.emit(a.expr(c))` becomes
`_ = a.expr(c)` -- which is both compilable and the more precise mutation, since
it removes what the site records without removing the traversal underneath it.

That rewrite has had to be widened once since, which is the same lesson a second
time. It matched `a.apply|taint|emit|depend` and not the `frames.go` family, so
`v.store(entry.Alias)` inside `for _, entry := range n.Names` still fell back to
removing the line, still did not compile, and was still reported as un-makeable
rather than measured -- for as long as the separate count existed to say so. It
is caught once it can be made. **When a tool reports a denominator, the thing to
check is how much of it was actually attempted.**

Four survivors have been found this way, each a line no test constrained: no
test covered a template named by an expression rather than a constant; none
covered the walk's default for an assignment *target* it does not recognise;
none covered the default for a *statement* kind it does not recognise; and none
covered the load an `ns.attr` target performs, which is what settles the name so
a later `{% set ns = ... %}` does not claim it. gojja2's own parser cannot build
the first three, and the tree type is public, so a caller can.

**That last survivor is closed, and it was not a missing test.** `frames.go`'s
`v.store(name)` for a name every branch of an `{% if %}` binds was reported here
as unconstrained -- by the corpus or by `make soak-syntax` -- with the note that
replacing it with `v.settle(name)` failed a soak, so the distinction was real.
That note was wrong: the two are the same statement once the branch is reached,
because the fallthrough settles as well, and a mutation that cannot change the
answer cannot fail anything. What the survivor actually meant is that **the
branch is never taken**. `go tool cover` put nothing on it across all 4,715
corpus cases there were then, and a `panic` in its place survived the whole
suite and 80,000 generated templates on two seeds.

It is unreachable by construction, not by accident. The condition wants a name
bound by *all three* arms -- body, elifs, else -- and an `{% elif %}` is a nested
`If`, which records what it writes through the pass that follows the sorted loop
rather than through `store()`. So the elif arm never reports a name as bound, no
matter what it contains, and the count stops at two every time. jinja2 3.1 does
not count either: `Symbols.branch_update` takes the union of what the arms wrote
and gives every name a load. The counting was jinja2 2.x's rule, carried across
and never exercised.

The branch is gone, and `control/branch_binding_*` now grades the rule that is in
force -- nine cases in a loop, a macro, a block, a filter block and at the root.
They are what makes the removal safe to have made: planting the arithmetic the
old rule wanted, `counts[name] >= 2`, fails four of them, along with
`TestSyntaxMatchesTheReference` and the syntax soak. Before them, nothing in the
repository could tell the two rules apart.

**A second survivor was redundancy, and removing it made a neighbour
measurable.** `steerEmit`'s document-level `a.apply(srcs, Steers)` ran when no
capture was collecting the body -- and every one of its five callers applies
Steers to the same symbols a line or two earlier, so it never changed an effect
in any shape tried. It was masking `forStmt`'s `a.apply(loopTest, Steers)`, which
was itself reported as a survivor. With the duplicate gone, planting that line
fails five tests instead of none. **Two survivors can be each other's reason**:
the fix for one was the answer to the other.

**And a third was not a survivor at all.** `canFailIn`'s needle in `mutate.py`
still read `func canFailIn(n *syntax.Node) bool {` after the function became a
method, so `str.replace` matched nothing, the file was written back unchanged,
and the suite passed on the original source. "Nothing noticed the change" and
"there was no change" print the same. The tool now raises on a needle it cannot
find rather than reporting the site.

### And the budget, for the same reason

The allocation bound is mutated too, and it is the one part of the engine that
belongs here. Everything a template *renders* is graded against CPython by five
thousand corpus cases and sixty thousand generated templates a run, which is a
far stronger check than mutating it would be. The budget has no counterpart in
CPython at all -- it is gojja2's invention -- so nothing outside this repository
can say whether it holds. That is the line: **mutate what has no oracle.**

Seventy-five places reserve memory or iterations before taking them. The mutation
removes the *charge*, not just its refusal, and the difference matters:
leaving the debit in place lets a later charge refuse instead, so sixteen sites
read as constrained under the weaker mutation and were not. A bound that only
ever fires after another one has already refused is not measured by anything.

Twenty-two survived when this was first run. Each is now driven by a template in
`TestEachBudgetChargeRefusesOnItsOwn` that **binds its result to a name instead
of printing it**: nothing downstream can charge it, so the site under test is the
only thing between the template and the allocation. Writing those found two
traps worth repeating -- `{{ "x" * 2097152 }}` and `{{ 1.5|round(1000) }}` are
constant-folded and never reach the code at all, and a context list is charged
as it is converted, so a per-item step has to be driven by a lazy `range()`
rather than by a list a test passes in.

Thirty-nine of the seventy there were then were all the tool could see until
2026-10-09. It
matched a charge written as a method on a State -- `st.ChargeBytes(n)` -- and the
value package charges through functions, `chargeItems(budget, 1)`, so every
charge there went unmeasured while the total read as complete. Taught the second
form, it found twelve survivors: eleven of those value-package sites, among them
`in` over a list, set arithmetic, a wide integer's `+` and `-` and a format
width, and one site the hot-path change had just moved into unpack. Each now has
a row in that test's table. Two of them can only be reached with any length
through a host object, which the table builds; two are walks that, uncharged,
run until something outside the process stops them, which is what the
mutation's own cap records as caught. The five since are the charges for copying what
reaches a render from outside it (see `value.Isolate`), each with its own row.

**None survive.** Four were closed by giving each site a template that reaches
it and nothing else. The last two could not be, and were closed by measuring
each the way it *can* be measured rather than the way the table does it.

`writeJSONString`'s block charge is a **deadline** instrument and not a size
bound: it consults the context every few thousand bytes so that escaping a long
string can be interrupted, which is what the 23MB render that ignored a 19ms
deadline was about. `TestStringFiltersYieldToTheDeadline` was already timing it,
against a bar too loose to decide -- take the charge out and the render stops at
57%, 67% and 64% of its full length over three runs, against a 60% bar, so the
defect was caught two times in three and reported as surviving on the third. It
is in `tightBar` at 30% now; the charge in place stops it at 13% every run.

`pad` adds the two halves of a centre together and charges the sum before it
builds either. No *budget* can tell that from the charges `repeatString` makes
anyway, because whatever the halves cost together they cost apart. The gate that
can is the other one: `ChargeBytes` refuses anything over `maxAllocBytes`
outright, before the budget is consulted at all, and that ceiling is per charge.
So a centre three billion wide asks for a sum over the ceiling whose halves are
each under it: with the charge the render is refused with an `OverflowError`
naming the sum and nothing is built, and without it the budget refuses one gate
later and a gigabyte and a half further on.
`TestPadChargesTheWholeCentreBeforeBuildingEitherHalf` asserts the *kind*,
because "it failed" is true of both. **A charge no bound can isolate may still
have a ceiling that can.**

One more survived for a while and was a finding about the *test*, not the
charge. `|urlencode`'s per-item step was measured by
`range(2000)|map("string")|list|batch(2)|urlencode` under a bound of a thousand
-- and `map` alone costs two thousand, so the bound was reached before urlencode
ran at all. The case passed, and it passed for the wrong reason. It asks for
`range(2000)|batch(2)|urlencode` under 4,500 now, which is *between* what building
the pairs costs (4,000) and what urlencode's own walk adds (1,000 more), because
every shape that hands it pairs has already paid for them. **A charge is only
measured by a bound the site itself has to cross.**

Two things about running it. A mutation that removes a bound is *meant* to let
the render allocate without one, so each measuring run gets a cap of its own --
otherwise the first such mutation takes the runner down and reports nothing.
And because the tool edits the working tree, it now writes the untouched copy to
`tools/.mutate-restore/` before each edit and restores whatever it finds there
on the next run: an `atexit` hook alone cannot answer the kernel's own killer,
and twice it left a mutated file behind. `make mutate ARGS=--budget` runs this
half alone, `ARGS=--analysis` the other.

### A generated table needs a guard that it is still complete

`method_arity.go` is generated from CPython, and it is the only thing that
refuses a method call of the wrong shape -- the bodies do not duplicate that
check. So a method missing from the table has no arity check at all, and nothing
said so, because the tool that writes the table reads the method maps as *text*.

`list.sort` is registered in `init()` rather than in the map literal (naming it
there is an initialisation cycle: it calls back into the evaluator), so the
generator has never seen it and `[2,1].sort(1, 2, 3)` was unchecked.
`TestEveryMethodHasASignature` iterates the maps at *run time* and asserts each
method has a signature, which is exactly the gap a text-parsing generator cannot
see. It found that one immediately.

Its wording could not have been generated either: CPython counts every argument
first, says "arguments" when any was positional and "keyword arguments" when none
was, and none of those messages carries a count where the generator looks for
one. So sort is checked by hand and listed in the test's allowlist with that
reason.

A related finding was recorded rather than acted on. Seventeen arity refusals in
`methods.go` are unreachable -- `checkMethodArity` runs first, and neutering all
seventeen produced nothing across the suite and a 12,000-template soak. They are
*not* dead code to delete: each guards a `arg(args, 0, ...)` lookup that would
otherwise leave a value unset, so removing one replaces an error with undefined
behaviour. They stay, as belt-and-braces over a rule enforced elsewhere -- the
same call as the open `frames.go` survivor above.

## Messages nothing has ever compared

`make mutate` asks what the suite fails to constrain. `make ungraded` asks a
narrower question with the same shape: **which error messages has no corpus case
ever produced?**

It runs the corpus under coverage on every interpreter, intersects the blocks
that never executed with the lines that build an error, and counts what is left.
Run `make ungraded` for today's count; the figures in the passes below are what
each pass measured at the time, and are history.

A message nothing produces is not evidence of anything -- it has never been
compared to CPython. It is worse than untested: it reads as *agreement in every
column of the version matrix*, because the matrix compares the cases the corpus
holds and nothing else. A message that is wrong on one interpreter and right on
another looks exactly like one that is right on all of them.

The first run found six bugs in the first seventy-eight shapes probed:
`bytes.hex` accepted a separator of any length and rejected a bytes one,
`|xmlattr` had its message inside out, nine numeric methods had no arity check at
all, `bytes.fromhex` skipped one whitespace character instead of six and reported
the wrong position, a dict-update element message had changed in 3.14 unnoticed,
and `selectattr` named itself in `rejectattr`'s error. The next hundred and
eighty-seven shapes found nothing, which is the other half of the result and is
why the number is recorded rather than chased to zero.

A site on the list is one of three things, and telling them apart is the work:

- **not reachable from a template** -- a configuration error, a budget refusal, a
  Go bridge complaint. Graded by Go tests, and not a claim about CPython.
- **an internal invariant** -- "unknown operator", "cannot execute". gojja2's own
  parser cannot build the node; `syntax.Node` is public, so a caller can.
- **reachable, and never probed.** The interesting kind. The only way to tell is
  to read the message and write the template that reaches it.

The count moving *up* is not automatically bad -- a new refusal starts ungraded
-- but it should move back down before the change lands.

A second pass over it (157 down to 145) found one bug and one thing worth
recording. The bug: **an integer too wide for a float64 answered an infinity**
where Python raises `OverflowError`. `//`, `%` and `**` went through the checked
coercion and were right, which is exactly what made `+`, `-`, `*`, `/`, `|float`,
`|filesizeformat`, `|sum` and both format paths look deliberate. Everything else
on the list already agreed, and what was missing was a case saying so -- which is
the point of the audit.

A third pass (110 down to 70, and `methods.go` from 40 to none) found four bugs
and 402 sites where there had been 417 -- because eighteen of what it read were
not messages at all. They were `arg(args, i, ...)` guards in method bodies,
unreachable since the generated arity table started refusing a call of the wrong
shape before the body runs, and each one a claim about CPython that nothing could
ever compare. `TestEveryMethodHasASignature` is what makes removing them safe;
seventeen of the same kind went earlier, and its comment records both batches.

The four bugs were in `str.format`. An integer in the mini-language that does not
fit is "Too many decimal digits in format string" -- for a width, a precision and
a replacement index alike -- and gojja2 multiplied and added without the check,
so `'{18446744073709551616}'` wrapped to 0 and printed the *first* argument. The
`[key]` step of a replacement field answered a `KeyError` -- what a mapping says
about a key it does not hold -- for every object that is not subscriptable at
all, indexed a bytes as though it were a str (a character, not the byte's
number), and reported a subscript error where an undefined should have raised its
own. And CPython 3.14 spells two of `str.maketrans`'s complaints a space short,
which no version column had ever compared.

The thing that looked like a fourth category was a mistake, and the mistake is
the more useful record. I wrote that `float.as_integer_ratio()` on an infinity
was **ungradable through CPython**, because every route to an infinity seemed to
go through a folded constant and jinja2's code generator writes one out as `inf`,
which is not a Python name. That is true of `1e400`. It is not true of `1e308`,
which writes out as `1e+308` -- so multiplying it through a *name* overflows
during the render and nothing is ever written as `inf`. Both sites are graded
now, and so are five more conversions behind them. **"No template can reach
this" is a claim about the templates tried so far**, and it belongs in a comment
next to the ones that were tried, not in a category.

A fourth pass (69 down to 50, and 402 sites down to 396) found two more bugs,
both of them the same shape as the 3.14 dict-update change: a message that had
never been produced and so had never been compared. lipsum's bounds go straight
to `random.randrange`, and CPython changed the empty-range wording in 3.12 --
gojja2 carried 3.11's for every interpreter, including the pinned one, and the one
Go test that asserted it asserted the same wrong thing. And `'%(a)s' % x` against
something that is not a dict: CPython's test is "supports subscripting", so
anything that does gets asked and answers for itself, where gojja2 gave the
blanket "format requires a mapping" that CPython reserves for what it cannot
subscript at all.

Nine more sites were guards behind the arity check -- the same category as the
eighteen above, one file over: a test or a filter that takes a required argument
is refused by `checkArity` before its body runs, on every route a test can be
called by, so the "divisibleby requires an argument" fallbacks inside them
answered something else and could not be reached to say it. `tests.go` no longer
imports `errs`.

A fifth pass came out of the *soak* rather than out of the list, once the
generated differential could draw custom delimiters and line statements: reading
the sites those findings landed near took it to 45 of 393. What that pass is
worth recording for is the shape of what it found -- `str.format`'s field parser
had one complaint where CPython has three, a nested field's own spec was being
dropped, two dict views raised where CPython compares them as sets, and the
lexer's "unexpected char" offset counted bytes where CPython counts code points.
**Every one of those was reachable from a template nobody had written**, which is
what an axis is for.

A sixth pass read the list itself rather than the code around it, and the useful
finding was not a bug but a *classification*: three of the sites it named were
the object arms of a lookup -- `'%(k)s' % m` and `m|random` over a Mapping that
is not a dict -- which nothing could reach until a dict view learned to hand out
its `mappingproxy`. A site can move from "unreachable from a template" to
"reachable and never probed" because something else was implemented, so the
classification is worth redoing rather than remembering. It also found
int.to_bytes accepting a signed value outside its range, and a class's
descriptors being answered as methods.

Two habits came out of that pass. **Read the line, not the message**: the same
words often appear at two or three sites, so a case that produces the message may
leave the listed site untouched -- `"subsection not found"` is raised by the
needle-missing branch *and* by the out-of-range window, and only the second was on
the list. And a non-finite float has to come from the *context*: `'inf'|float` is
a constant expression, jinja2 folds it and writes `inf` into its generated Python,
so the template fails with a `NameError` about jinja2's own output instead.

`make soak-syntax` asks the same three questions of templates nobody chose, and
then asks the engine whether the answers are true. Where the analysis says a
variable is never printed, the render gets a marker and the output must not hold
it; where it says a variable cannot break the render, values with nothing in
common must not change whether it does. That second half is the important one:
the differential proves the two implementations agree, and they are both written
here, so agreement is close to a statement about transcription. Rendering is not.

The generator walks the grammar rather than a list somebody thought of, which is
how it reaches an autoescape inside a macro inside a loop that assigns the name it
read. The oracle server answers `{"analyze": true}` as well as a render, so a
soak can ask both implementations as fast as it can generate: 50,000 templates
takes minutes rather than the hours a fresh interpreter per template would.

It found four faults in an afternoon, three of them in the reference and one in
the engine, and they were all the same fault: the reference had its own symbol
model. It reads the engine's now -- which is not a loss of independence, because
that model is already checked against jinja2's own idtracking for every
committed and imported template. What is written twice is the dataflow
reasoning, which is the thing the differential is for.

`make import` also writes reference trees and analyses for the imported corpora,
and `TestGeneratedCorporaAgree` grades them the same three ways. It skips when
they are absent, so it is a check a contributor gets rather than one CI enforces
-- the same bargain the imported corpora are on already.

It is worth running. The committed corpus is written in this repository and
therefore knows what it is testing; the 2,176 imported templates are chat
templates real models ship, Jinja's own suite, cookiecutter projects, minja and
llama.cpp. Every fault the scope and dataflow work has had since it was first
graded green was found there and nowhere else.

`TestNegativesSurviveRendering` is the check the other two cannot be, and it
earns its place: adding the `Required` effect, it found five variables the rule
had missed and then caught a regression the fix introduced, all before the change
left the branch. They
compare two implementations, and two implementations can be wrong the same way;
this one asks the engine. Where the analysis says a variable's value cannot be
printed, a marker no template contains is passed in and the output must not hold
it; where it says a variable cannot change the output at all, two values with
nothing in common must render the same bytes. A negative is the strongest claim
the analysis makes and the only one that can be checked directly.

Break the analysis — make `emit` do nothing — and the claims it makes jump from
31 to 1,113, most of them false, which is the shape of the failure this catches.

The `dataflow` package is graded the same way and for a different reason. It is
written entirely against the public `syntax.Tree`, so `make nameflow` and
`TestDataflowMatchesTheReference` are checking two things at once: that the
reasoning is right, and that the exposed tree is enough to do the reasoning with.
An analysis the engine could only write from the inside would mean the exposure
had failed.

Three checks stand behind the vocabulary itself, because comparing the two trees
cannot catch a field *neither* side writes down — two emitters that both forget
`ignore missing` agree perfectly and are both wrong:

- `TestEveryAttributeIsCarried` pairs templates that differ in exactly one
  attribute and requires the encodings to differ. One pair per attribute.
- `syntax_emit.check_fields` holds the vocabulary to jinja2's own field lists.
  Every field a node declares is carried or named as deliberately not, and the
  emitter refuses rather than drops: a `{% set %}` through an attribute, an
  extension's scope overlay, an eval-context option that is not `autoescape`.
- `TestEncodingTheSameMeans…` renders every pair of generated templates that
  share an encoding and requires the output to match.

If `make syntax` reports "no spelling for …", the vocabulary is missing a node.
Add it to `gojja2/syntax` and to both emitters rather than skipping the case: a
tree quietly missing a node compares equal for the wrong reason.
