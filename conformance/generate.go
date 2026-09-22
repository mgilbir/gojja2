// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package conformance

import (
	"encoding/json"
	"strings"
)

// A structured generator, not a byte mutator.
//
// Mutating template text at random almost always produces a syntax error, and
// two implementations agreeing that something is a syntax error is the least
// interesting thing they can agree on. Generating from the grammar instead
// means nearly every case renders, so divergence shows up in output rather
// than in error text.
//
// Choices are drawn from the fuzzer's input bytes rather than from a random
// source, so flipping one byte changes one decision and coverage-guided
// mutation does something useful. When the input runs out every choice takes
// its first alternative, which is the simplest one, so generation terminates.

// FuzzContextJSON is the context every generated template renders against.
//
// It is JSON so that both sides are handed byte-identical data: the Go decoder
// preserves key order and the int/float distinction, and the same bytes go to
// the oracle. Values cover the shapes that make filters behave differently --
// empty and non-empty, mixed types, unicode, nested, and markup.
const FuzzContextJSON = `{
  "n": 3, "m": 10, "neg": -4, "zero": 0, "one": 1,
  "f": 2.5, "fz": 0.0, "fneg": -1.5,
  "s": "Hello World", "blank": "", "uni": "héllo wörld",
  "t": "  the quick brown fox jumps over the lazy dog  ",
  "yes": true, "no": false, "nil": null,
  "lst": [3, 1, 2], "mix": [1, "a", 2.5, true, null], "e": [],
  "strs": ["banana", "Apple", "cherry", "Apple"],
  "d": {"b": 2, "a": 1, "C": 3}, "ed": {},
  "users": [
    {"name": "ana", "age": 30, "city": "Lisbon"},
    {"name": "bo", "age": 25, "city": "Porto"},
    {"name": "cy", "age": 30, "city": "Lisbon"}
  ],
  "nested": {"x": {"y": [1, 2]}},
  "html": "<b>a &amp; b</b>",
  "pairs": [[1, 2], [3, 4]]
}`

// FuzzTemplates are the auxiliary templates a generated case can reach, so
// inheritance, inclusion and importing are exercised rather than only
// producing TemplateNotFound.
func FuzzTemplates() map[string]string {
	return map[string]string{
		"base.txt": "[{% block a %}A{% endblock %}|{% block b %}B{% endblock %}]",
		"inc.txt":  "<{{ n|default('?') }}{{ item|default('') }}>",
		"mac.txt":  "{% macro m(x, y=2) %}({{ x }},{{ y }}){% endmacro %}{% set ex = 'E' %}",
	}
}

// names are the context bindings a generated expression may reference.
// "nope" is deliberately absent from the context so undefined paths are
// reached as often as defined ones.
var names = []string{
	"n", "m", "neg", "zero", "one", "f", "fz", "fneg",
	"s", "blank", "uni", "t", "yes", "no", "nil",
	"lst", "mix", "e", "strs", "d", "ed", "users", "nested", "html", "pairs",
	"nope",
}

// smallInts keep multiplication and exponentiation from asking for an
// allocation the size of the machine.
var smallInts = []string{"0", "1", "2", "3", "5", "-1", "-2"}

var intLiterals = []string{
	"0", "1", "2", "3", "7", "10", "-1", "-7", "255",
	"1_000", "0x1f", "0o17", "0b101", "2147483648", "9223372036854775808",
}

var floatLiterals = []string{"0.0", "1.5", "-2.5", "0.1", "1e3", "1e-5", "2.675", "1e16"}

var stringLiterals = []string{
	`'a'`, `'abc'`, `''`, `'A b-c'`, `'héllo'`, `'<x>&'`, `"it's"`, `'%s'`, `'a,b,c'`,
}

// deterministicFilters exclude anything whose output embeds an address or a
// random draw; those cannot be compared against a recording, not even against
// CPython's own.
var deterministicFilters = []string{
	"upper", "lower", "title", "capitalize", "trim", "length", "count",
	"list", "first", "last", "reverse", "sort", "sum", "min", "max",
	"abs", "int", "float", "round", "string", "escape", "safe", "forceescape",
	"striptags", "wordcount", "center", "indent", "truncate", "batch",
	"slice", "unique", "dictsort", "items", "default", "replace", "format",
	"urlencode", "tojson", "pprint", "filesizeformat", "wordwrap", "attr",
	"join", "map", "select", "reject", "selectattr", "rejectattr", "groupby",
	"xmlattr", "urlize", "e", "d",
}

// lazyFilters return generators in jinja2. Two things follow, and both are
// reasons to force them with |list: their repr is a memory address, which
// nothing can reproduce, and they defer their input checks until iteration,
// so an unused one hides an error gojja2 raises eagerly. Forcing them makes
// the comparison about the elements, which is the part that has an answer.
var lazyFilters = map[string]bool{
	"map": true, "select": true, "reject": true,
	"selectattr": true, "rejectattr": true, "unique": true, "items": true,
	"groupby": true, "slice": true, "batch": true, "reverse": true,
}

// filterArgs supplies arguments for the filters that need them, and plausible
// ones for those that merely accept them.
var filterArgs = map[string][]string{
	"join":           {`'-'`, `', '`, `'', attribute='name'`},
	"default":        {`'D'`, `'D', true`, `boolean=true`},
	"replace":        {`'a', 'b'`, `'o', '0', 1`},
	"format":         {`'x'`, `1, 2`},
	"round":          {`2`, `0, 'ceil'`, `1, 'floor'`},
	"int":            {``, `9`, `0, 16`},
	"float":          {``, `1.5`},
	"indent":         {`2`, `2, true`, `4, false, true`},
	"truncate":       {`10`, `12, true`, `15, false, '~'`},
	"center":         {`12`},
	"batch":          {`2`, `2, 'X'`},
	"slice":          {`3`, `2, 'X'`},
	"wordwrap":       {`10`, `12, false`},
	"sort":           {``, `true`, `attribute='name'`, `reverse=true, attribute='age'`},
	"dictsort":       {``, `true`, `by='value'`, `false, 'value', true`},
	"unique":         {``, `true`, `attribute='city'`},
	"min":            {``, `attribute='age'`},
	"max":            {``, `attribute='age'`},
	"sum":            {``, `attribute='age'`, `start=10`},
	"map":            {`'upper'`, `attribute='name'`, `attribute='nope', default='?'`},
	"select":         {`'odd'`, `'defined'`, ``},
	"reject":         {`'odd'`, `'none'`, ``},
	"selectattr":     {`'age', 'eq', 30`, `'name'`},
	"rejectattr":     {`'age', 'gt', 25`, `'name'`},
	"groupby":        {`'city'`, `'age'`},
	"attr":           {`'name'`, `'nope'`},
	"tojson":         {``, `indent=2`},
	"urlize":         {``, `10`, `target='_blank'`},
	"filesizeformat": {``, `true`},
	"truncate_":      {``},
}

var testNames = []string{
	"defined", "undefined", "none", "boolean", "integer", "float", "number",
	"string", "mapping", "sequence", "iterable", "callable", "odd", "even",
	"lower", "upper", "escaped", "true", "false",
}

var testWithArg = map[string][]string{
	"divisibleby": {"2", "3"},
	"eq":          {"1", "'a'", "n"},
	"ne":          {"1", "'a'"},
	"lt":          {"5"}, "le": {"5"}, "gt": {"0"}, "ge": {"0"},
	"in":     {"lst", "'abc'", "d"},
	"sameas": {"none", "true"},
	"filter": {},
	"test":   {},
}

var binaryOps = []string{"+", "-", "*", "/", "//", "%", "~"}
var compareOps = []string{"==", "!=", "<", "<=", ">", ">=", "in", "not in"}

// globals are the calls a generated expression may make. A cycler, a joiner
// and a namespace are reached through their attributes rather than printed
// whole: jinja2's reprs for the first two embed a memory address, which
// nothing can reproduce, and a template that prints one is a case with no
// answer rather than a case that fails.
var globals = []string{
	"range(3)", "range(1, 5)", "range(0, 6, 2)", "range(3, 0, -1)",
	"dict(a=1, b=2)", "namespace(v=1).v", "cycler('a','b').next()",
	"cycler('a','b').current", "joiner('-')()",
}

// chooser draws decisions from the fuzzer's input. Past the end of the input
// every decision is zero, so generation always terminates.
type chooser struct {
	b []byte
	i int
}

func (c *chooser) next() byte {
	if c.i >= len(c.b) {
		return 0
	}
	v := c.b[c.i]
	c.i++
	return v
}

func (c *chooser) intn(n int) int {
	if n <= 1 {
		return 0
	}
	return int(c.next()) % n
}

func (c *chooser) pick(options []string) string {
	if len(options) == 0 {
		return ""
	}
	return options[c.intn(len(options))]
}

// chance reports a one-in-n decision.
func (c *chooser) chance(n int) bool { return c.intn(n) == 0 }

func (c *chooser) exhausted() bool { return c.i >= len(c.b) }

type generator struct {
	c     *chooser
	b     strings.Builder
	depth int
	// inLoop counts the {% for %} bodies open around the current point, so
	// `loop` is only written where it resolves. Outside one it is an
	// undefined name, which is a far less interesting template than one
	// that asks a real loop for its index.
	inLoop int
	// inBlock marks a {% block %} body, where super() and self.<name>()
	// resolve.
	inBlock bool
	// recursive marks a {% for ... recursive %} body, where loop() calls
	// the loop again.
	recursive bool
}

// GeneratedCase is a template together with the environment it is meant to
// render in.
//
// Autoescaping is part of the case and not of the template, because it changes
// what five filters do and what the compiler is allowed to fold, and a
// template alone cannot say which it wanted. Leaving it off meant that whole
// half of the engine was never compared: every escaping divergence found so
// far was found by hand, because no seed could reach one.
type GeneratedCase struct {
	Source     string
	Autoescape bool
}

// GenerateCase builds a template and the environment it renders under.
func GenerateCase(input []byte) GeneratedCase {
	g := &generator{c: &chooser{b: input}}
	// Drawn before the template so that one byte decides it, and so that
	// the same bytes after it generate the same template either way --
	// which is what makes shrinking a diverging case keep its setting.
	autoescape := g.c.chance(3)
	g.template()
	return GeneratedCase{Source: g.b.String(), Autoescape: autoescape}
}

// GenerateTemplate builds a template from fuzzer input, for callers that do
// not care which environment it was meant for.
func GenerateTemplate(input []byte) string { return GenerateCase(input).Source }

const (
	maxExprDepth = 4
	maxStmts     = 6
	maxBodyStmts = 3
)

func (g *generator) template() {
	// A template either extends a base and fills blocks, or is a plain
	// body. Both shapes need covering; the inheriting one is rarer because
	// it constrains everything else.
	if g.c.chance(12) {
		g.b.WriteString("{% extends 'base.txt' %}")
		for _, block := range []string{"a", "b"} {
			if g.c.chance(2) {
				continue
			}
			g.b.WriteString("{% block " + block + " %}")
			g.inBlock = true
			g.body(1)
			g.inBlock = false
			g.b.WriteString("{% endblock %}")
		}
		return
	}
	g.statements(maxStmts, 2)
}

func (g *generator) statements(count, depth int) {
	for range count {
		if g.c.exhausted() {
			return
		}
		g.stmt(depth)
	}
}

// body emits a short run of statements for the inside of a tag.
func (g *generator) body(depth int) {
	g.statements(1+g.c.intn(maxBodyStmts), depth)
}

// tag writes an opening delimiter, occasionally with whitespace control.
func (g *generator) open(kind string) {
	mark := ""
	switch g.c.intn(8) {
	case 0:
		mark = "-"
	case 1:
		mark = "+"
	}
	g.b.WriteString(kind + mark + " ")
}

func (g *generator) close(kind string) {
	mark := ""
	switch g.c.intn(8) {
	case 0:
		mark = "-"
	case 1:
		mark = "+"
	}
	g.b.WriteString(" " + mark + kind)
}

func (g *generator) stmt(depth int) {
	if depth <= 0 {
		g.b.WriteString("x")
		return
	}
	switch g.c.intn(15) {
	case 0:
		g.b.WriteString(g.c.pick([]string{"text ", "\n", " ", "a\nb", "<p>", "  "}))
	case 1, 2, 3:
		g.open("{{")
		g.b.WriteString(g.expr(maxExprDepth))
		g.close("}}")
	case 4, 5:
		g.ifStmt(depth)
	case 6, 7:
		g.forStmt(depth)
	case 8:
		g.setStmt(depth)
	case 9:
		g.withStmt(depth)
	case 10:
		g.macroStmt(depth)
	case 11:
		g.filterStmt(depth)
	case 12:
		g.b.WriteString(g.c.pick([]string{
			"{% raw %}{{ x }}{% endraw %}",
			"{#- a comment -#}",
			"{# c #}",
			"{% include 'inc.txt' %}",
			// A context-free include is deliberately absent: inside
			// a macro jinja2 turns the whole macro into a generator
			// that is never consumed, which is an artefact of its
			// compilation rather than a behaviour to match. See
			// docs/divergences.md.
			"{% include 'nope.txt' ignore missing %}",
			"{% import 'mac.txt' as mm %}{{ mm.m(1) }}{{ mm.ex }}",
			"{% from 'mac.txt' import m %}{{ m(1, 3) }}",
			// `with context` decides whether the loaded template
			// sees the caller's variables, which is visible in what
			// inc.txt prints for `n`. The `without context` form of
			// an *include* stays out for the reason given above:
			// inside a macro it turns the macro into a generator
			// nobody consumes, which is the divergence
			// docs/divergences.md records rather than matches.
			"{% include 'inc.txt' with context %}",
			"{% import 'mac.txt' as mm without context %}{{ mm.m(1) }}",
			"{% from 'mac.txt' import m with context %}{{ m(1) }}",
		}))
	case 13:
		g.autoescapeStmt(depth)
	default:
		g.open("{{")
		g.b.WriteString(g.expr(2))
		g.close("}}")
	}
}

// autoescapeStmt emits `{% autoescape %}`, which moves the escaping for its
// body -- and, given a name rather than a literal, leaves it unknowable until
// the render, so nothing inside may be folded. Both are worth generating: the
// two engines have to agree on which filter escaped and on what was folded
// under which setting.
func (g *generator) autoescapeStmt(depth int) {
	g.open("{%")
	g.b.WriteString("autoescape " + g.c.pick([]string{
		"true", "false", "yes", "n", "blank", "nil", "not no",
	}))
	g.close("%}")
	g.body(depth - 1)
	g.b.WriteString("{% endautoescape %}")
}

func (g *generator) ifStmt(depth int) {
	g.open("{%")
	g.b.WriteString("if " + g.expr(2))
	g.close("%}")
	g.body(depth - 1)
	if g.c.chance(3) {
		g.open("{%")
		g.b.WriteString("elif " + g.expr(2))
		g.close("%}")
		g.body(depth - 1)
	}
	if g.c.chance(2) {
		g.b.WriteString("{% else %}")
		g.body(depth - 1)
	}
	g.b.WriteString("{% endif %}")
}

func (g *generator) forStmt(depth int) {
	target, iterable := "i", g.c.pick([]string{
		"lst", "strs", "mix", "e", "d", "users", "range(3)", "nope", "s", "pairs",
	})
	if iterable == "pairs" && g.c.chance(2) {
		target = "a, b"
	}

	g.open("{%")
	g.b.WriteString("for " + target + " in " + iterable)
	if g.c.chance(4) {
		g.b.WriteString(" if " + g.expr(1))
	}
	// A recursive loop is its own shape: the body may call loop() to
	// descend, and `loop.depth` only moves in one.
	recursive := g.c.chance(6)
	if recursive {
		g.b.WriteString(" recursive")
	}
	g.close("%}")

	g.inLoop++
	wasRecursive := g.recursive
	g.recursive = g.recursive || recursive
	if recursive && g.c.chance(2) {
		g.b.WriteString("{{ loop(" + g.c.pick([]string{"[]", "lst", "i", "[i]"}) + ") }}")
	}
	g.body(depth - 1)
	g.recursive = wasRecursive
	g.inLoop--

	if g.c.chance(4) {
		g.b.WriteString("{% else %}empty")
	}
	g.b.WriteString("{% endfor %}")
}

func (g *generator) setStmt(depth int) {
	switch g.c.intn(6) {
	case 0:
		g.b.WriteString("{% set v = " + g.expr(3) + " %}{{ v }}")
	case 1:
		g.b.WriteString("{% set p, q = " + g.c.pick([]string{"1, 2", "lst[0], lst[1]", "pairs[0]"}) + " %}{{ p }}{{ q }}")
	case 2:
		g.b.WriteString("{% set ns = namespace(total=0) %}{% for i in lst %}{% set ns.total = ns.total + i %}{% endfor %}{{ ns.total }}")
	case 3, 4:
		g.namespaceStmt()
	default:
		g.b.WriteString("{% set v %}")
		g.body(depth - 1)
		g.b.WriteString("{% endset %}{{ v }}")
	}
}

// namespaceStmt writes the namespace shapes the accumulator above never reaches.
//
// Coverage said so: the code that gives up on a namespace once it has been
// handed somewhere was reached by three statements in ten thousand, and the code
// that builds one from a mapping was reached by none. Those are the shapes where
// the analysis has to stop being precise, so they are the ones worth generating.
func (g *generator) namespaceStmt() {
	switch g.c.intn(7) {
	case 0:
		// Two names for one object: a write through either reaches the other.
		g.b.WriteString("{% set ns = namespace(v=0) %}{% set other = ns %}" +
			"{% set ns.v = " + g.expr(2) + " %}{{ other.v }}{{ ns.v }}")
	case 1:
		// Handed to a macro, which is the same thing by another route.
		g.b.WriteString("{% macro nsm(o) %}{{ o.v }}{% endmacro %}" +
			"{% set ns = namespace(v=" + g.expr(2) + ") %}{{ nsm(ns) }}")
	case 2:
		// Built from a mapping, so the fields cannot be told apart.
		g.b.WriteString("{% set ns = namespace(**" +
			g.c.pick([]string{"d", "{'v': 1}", "dict(v=2)"}) + ") %}{{ ns.v }}")
	case 3:
		// Assigned twice, which is one namespace to jinja2's symbol table
		// and two objects at render time.
		g.b.WriteString("{% set ns = namespace(a=" + g.expr(2) + ") %}" +
			"{% for i in lst %}{% set ns = namespace(b=i) %}{{ ns.b }}{% endfor %}{{ ns.a }}")
	case 4:
		// Fields that are never read, and one that is.
		g.b.WriteString("{% set ns = namespace(a=" + g.expr(2) + ", b=" +
			g.expr(2) + ") %}{{ ns.a }}")
	case 5:
		// A field written on something that is not a namespace at all.
		g.b.WriteString("{% set " + g.c.pick([]string{"d", "given"}) +
			".v = " + g.expr(2) + " %}")
	default:
		// The same target with a body, which is a different operation:
		// an item assignment, so it lands in a dict and raises the way
		// Python's __setitem__ does on everything else.
		//
		// Only names the context defines. An undefined one is the single
		// case where jinja2's message has no counterpart here -- it names
		// the sentinel its resolver returns -- and docs/divergences.md
		// records that rather than matching it.
		if g.c.intn(3) == 0 {
			g.b.WriteString("{% set ns = namespace() %}{% set ns.v %}" +
				g.expr(2) + "{% endset %}{{ ns.v }}")
			break
		}
		g.b.WriteString("{% set " +
			g.c.pick([]string{"d", "ed", "lst", "s", "n", "nil"}) +
			".v %}" + g.expr(2) + "{% endset %}")
	}
}

func (g *generator) withStmt(depth int) {
	g.b.WriteString("{% with w = " + g.expr(2) + " %}{{ w }}")
	g.body(depth - 1)
	g.b.WriteString("{% endwith %}")
}

func (g *generator) macroStmt(depth int) {
	g.b.WriteString("{% macro mm(x")
	if g.c.chance(2) {
		g.b.WriteString(", y=" + g.c.pick(smallInts))
	}
	g.b.WriteString(") %}")
	g.body(depth - 1)
	g.b.WriteString("[{{ x }}]{% endmacro %}")

	if g.c.chance(3) {
		g.b.WriteString("{% call mm(1) %}called{% endcall %}")
		return
	}
	// A macro that renders its caller, which is the other half of {% call %}
	// and reaches jinja2's caller machinery rather than a plain macro call.
	if g.c.chance(4) {
		g.b.WriteString("{% macro wrap() %}<{{ caller() }}>{% endmacro %}" +
			"{% call wrap() %}" + g.c.pick([]string{"body", "{{ x|default('d') }}", ""}) +
			"{% endcall %}")
		return
	}
	// *args and **kwargs reach jinja2's dyn_args and dyn_kwargs, a binding
	// path of their own: they are unpacked after the positional arguments
	// are counted, so what they collide with is decided there.
	g.b.WriteString("{{ mm(" + g.c.pick([]string{
		"1", "'a'", "lst", "1, 2",
		"*lst", "*[1]", "**d", "**{'x': 1}", "1, **{'y': 2}", "*[1], **{'y': 2}",
	}) + ") }}")
}

func (g *generator) filterStmt(depth int) {
	g.b.WriteString("{% filter " + g.c.pick([]string{"upper", "trim", "lower|trim", "escape"}) + " %}")
	g.body(depth - 1)
	g.b.WriteString("{% endfilter %}")
}

// --- expressions -------------------------------------------------------------

func (g *generator) expr(depth int) string {
	if depth <= 0 || g.c.exhausted() {
		return g.atom()
	}
	switch g.c.intn(17) {
	case 15, 16:
		return g.methodCall(depth)
	case 0, 1, 2, 3:
		return g.atom()
	case 4:
		return g.binary(depth)
	case 14:
		if g.c.chance(3) {
			return g.wrongArity()
		}
		return g.percentFormat()
	case 5:
		return g.c.pick([]string{"not ", "-", "+"}) + g.expr(depth-1)
	case 6:
		return g.comparison(depth)
	case 7, 8:
		return g.filtered(depth)
	case 9:
		return g.tested(depth)
	case 10:
		return g.subscript(depth)
	case 11:
		return g.expr(depth-1) + " " + g.c.pick([]string{"and", "or"}) + " " + g.expr(depth-1)
	case 12:
		return g.expr(depth-1) + " if " + g.expr(1) + " else " + g.expr(depth-1)
	default:
		return "(" + g.expr(depth-1) + ")"
	}
}

func (g *generator) binary(depth int) string {
	op := g.c.pick(binaryOps)
	// Repetition and exponentiation take a small literal on the right, so a
	// generated template cannot ask for a terabyte of list.
	if g.c.chance(6) {
		return g.expr(depth-1) + " ** " + g.c.pick([]string{"0", "1", "2", "3"})
	}
	if op == "*" {
		return g.expr(depth-1) + " * " + g.c.pick(smallInts)
	}
	return g.expr(depth-1) + " " + op + " " + g.expr(depth-1)
}

// wrongArity calls a filter or test with arguments it does not take.
//
// Nothing else generates one: the argument lists come from a hand-curated
// table of *correct* calls, and the imported corpora are templates written by
// people who got the arity right. So the whole argument-validation surface --
// too many, too few, a name the function does not have, a name it already has
// a value for -- was invisible to both gates while the pass rate sat at 99.8%,
// and 85 of 96 probed cases diverged.
func (g *generator) wrongArity() string {
	name := g.c.pick(arityNames)
	shape := g.c.pick([]string{
		"(1, 2, 3, 4, 5)", "(1, 2, 3)", "(zzzz=1)", "()", "(1, zzzz=2)",
	})
	if g.c.chance(4) {
		return g.expr(1) + " is " + g.c.pick(testNames) + shape
	}
	out := g.expr(1) + "|" + name + shape
	if lazyFilters[name] {
		// A call that is accepted returns a generator in jinja2 and a
		// list here, so it is forced for the same reason every other
		// arm forces one: printing a generator compares two memory
		// addresses. A call that is refused fails before this.
		out += "|list"
	}
	return out
}

// arityNames are filters with a fixed signature, so a call can be too long for
// them. The lazy sequence filters take *args and are left out.
var arityNames = []string{
	"upper", "lower", "title", "capitalize", "trim", "string", "replace",
	"center", "indent", "truncate", "wordwrap", "wordcount", "striptags",
	"urlencode", "filesizeformat", "pprint", "tojson", "abs", "int", "float",
	"round", "sum", "length", "list", "items", "first", "last", "join",
	"reverse", "sort", "dictsort", "unique", "min", "max", "batch", "slice",
	"groupby", "default", "attr", "escape", "forceescape", "safe", "xmlattr",
}

// percentFormat builds a printf-style conversion and an argument that suits it.
//
// `%` between two generated expressions almost never puts a real conversion on
// the left, so the whole surface -- flags, width, precision, verb -- went
// uncovered by the corpus and by the generator alike. It diverged from CPython
// in thousands of combinations while the pass rate stayed at 99.8%, which is
// what §4.4 of the audit is about: the rate measures agreement on the cases
// that exist.
//
// The widths are deliberately small. A soak runs millions of these and must
// not ask for a gigabyte of padding.
func (g *generator) percentFormat() string {
	spec := percentSpecs[g.c.intn(len(percentSpecs))]
	return "'[" + spec.format + "]' % " + g.c.pick(spec.args)
}

// percentSpecs pairs a format string with the arguments Python accepts for it.
var percentSpecs = []struct {
	format string
	args   []string
}{
	{"%s", []string{"'ab'", "1", "1.5", "none", "true", "lst", "d", "'é'"}},
	{"%r", []string{"'ab'", "1", "1.5", "none", "lst"}},
	{"%a", []string{"'é'", "'ab'", "1.5"}},
	{"%05s", []string{"'x'", "1", "none"}},
	{"%-8s|", []string{"'x'", "lst"}},
	{"%.2s", []string{"'abcdef'", "'éüö'"}},
	{"%8.3s|", []string{"'abcdef'"}},
	{"%d", []string{"42", "-42", "0", "1.7", "-1.7", "true", "2**70"}},
	{"%i", []string{"42", "-42", "1.7"}},
	{"%05d", []string{"42", "-42", "0"}},
	{"%+d", []string{"42", "-42"}},
	{"% d", []string{"42", "-42"}},
	{"%-6d|", []string{"42", "-42"}},
	{"%.4d", []string{"42", "0", "-7"}},
	{"%.0d", []string{"0", "5"}},
	{"%x", []string{"255", "-255", "0", "true", "2**70"}},
	{"%X", []string{"255", "-255"}},
	{"%o", []string{"8", "-8", "0"}},
	{"%#x", []string{"255", "0"}},
	{"%#o", []string{"8", "0"}},
	{"%#010X", []string{"255"}},
	{"%f", []string{"1.5", "-1.5", "0.0", "42", "1e-7"}},
	{"%.3f", []string{"1.5", "-1.5", "2.675"}},
	{"%08.2f", []string{"1.5", "-1.5"}},
	{"%+08.3f", []string{"1.5", "-1.5"}},
	{"%e", []string{"1.5", "-1.5", "0.0"}},
	{"%E", []string{"123456.789"}},
	{"%g", []string{"1.5", "1e-7", "123456789.0"}},
	{"%G", []string{"1e-7"}},
	{"%c", []string{"65", "97", "'x'", "0"}},
	{"%5c|", []string{"65"}},
	{"%%", []string{"()"}},
	{"%s-%s", []string{"('a', 'b')", "(1, lst)"}},
	{"%(a)s", []string{"d", "dict(a=1)"}},
	{"%(a)-6.2f|", []string{"dict(a=1.5)"}},
	{"%*s|", []string{"(4, 'x')", "(-4, 'x')"}},
	{"%.*f", []string{"(3, 1.5)", "(0, 1.5)"}},
	{"%*.*f|", []string{"(8, 2, 1.5)"}},
}

func (g *generator) comparison(depth int) string {
	out := g.expr(depth - 1)
	for range 1 + g.c.intn(2) {
		out += " " + g.c.pick(compareOps) + " " + g.expr(depth-1)
	}
	return out
}

// dynFilterArgs are the *args and **kwargs forms a filter call can take, which
// bind through jinja2's own path rather than the positional one.
var dynFilterArgs = []string{
	"join(*['-'])", "join(**{'d': '-'})", "default(*['x'])", "default(**{'boolean': true})",
	"round(*[1])", "replace(*['a', 'b'])", "indent(**{'width': 2})",
}

func (g *generator) filtered(depth int) string {
	out := g.expr(depth - 1)
	for range 1 + g.c.intn(2) {
		if g.c.chance(12) {
			out += "|" + g.c.pick(dynFilterArgs)
			continue
		}
		name := g.c.pick(deterministicFilters)
		out += "|" + name
		if args, ok := filterArgs[name]; ok && len(args) > 0 {
			if arg := g.c.pick(args); arg != "" {
				out += "(" + arg + ")"
			}
		}
		if lazyFilters[name] {
			// Printing a generator would compare two memory
			// addresses, so the elements are forced.
			out += "|list"
		}
	}
	return out
}

func (g *generator) tested(depth int) string {
	if g.c.chance(3) {
		for name, args := range testWithArg {
			if len(args) == 0 {
				continue
			}
			return g.expr(depth-1) + " is " + name + "(" + g.c.pick(args) + ")"
		}
	}
	negate := ""
	if g.c.chance(4) {
		negate = "not "
	}
	return g.expr(depth-1) + " is " + negate + g.c.pick(testNames)
}

func (g *generator) subscript(depth int) string {
	// Parenthesised, because `x|filter` followed by `.name` would be read
	// as the dotted filter name `filter.name` rather than as an attribute
	// of the filtered value -- a template that does not mean what the
	// generator intended is a wasted case.
	base := "(" + g.expr(depth-1) + ")"
	switch g.c.intn(6) {
	case 0:
		return base + "[" + g.c.pick([]string{"0", "1", "-1", "5", "'a'", "'nope'", "n"}) + "]"
	case 1:
		return base + "." + g.c.pick([]string{"a", "b", "name", "nope", "0"})
	case 2:
		return base + "[" + g.c.pick([]string{"1:", ":2", "1:2", "::2", "::-1", ":", "-2:"}) + "]"
	case 3:
		return base + "|attr(" + g.c.pick([]string{"'a'", "'name'", "'nope'"}) + ")"
	default:
		return base
	}
}

func (g *generator) atom() string {
	switch g.c.intn(11) {
	case 0:
		return g.c.pick(intLiterals)
	case 1:
		return g.c.pick(floatLiterals)
	case 2:
		return g.c.pick(stringLiterals)
	case 3:
		return g.c.pick([]string{"true", "false", "none", "True", "False", "None"})
	case 4, 5, 6:
		return g.c.pick(names)
	case 7:
		// Bounded: an atom that could contain another atom without a
		// depth of its own would generate arbitrarily deep literals.
		if g.depth >= maxExprDepth {
			return "[]"
		}
		g.depth++
		defer func() { g.depth-- }()
		return "[" + g.list(2) + "]"
	case 8:
		return g.c.pick([]string{
			"{'a': 1}", "{}", "{1: 'a', 2: 'b'}", "{'a': 1, 'b': [1,2]}",
			"{1: 'a', 1.0: 'b'}", "{(1,2): 'x'}",
		})
	case 9:
		if g.inLoop > 0 {
			return g.c.pick(loopAttrs)
		}
		if g.inBlock {
			return g.c.pick([]string{"self.a()", "self.b()", "super()"})
		}
		return g.c.pick(globals)
	default:
		return g.c.pick(globals)
	}
}

// loopAttrs are what `loop` offers inside a {% for %}. cycle and changed are
// calls rather than attributes, and previtem and nextitem are undefined at the
// ends -- which is the part worth generating.
var loopAttrs = []string{
	"loop.index", "loop.index0", "loop.revindex", "loop.revindex0",
	"loop.first", "loop.last", "loop.length", "loop.depth", "loop.depth0",
	"loop.previtem", "loop.nextitem", "loop.cycle('a', 'b')",
	"loop.cycle(1, 2, 3)", "loop.changed(i)", "loop.changed(1)",
	"loop", "loop|string",
}

// methodCall writes a call to one of Python's own methods on a receiver of the
// right type.
//
// Coverage said nothing here was reached: the generator emitted filters, tests,
// operators and subscripts, and not one method call, so two thirds of
// methods.go had never seen a generated template. The receiver is matched to
// the method on purpose -- `lst.upper()` is an AttributeError and grades only
// the lookup, while `s.replace(...)` grades the argument handling, which is
// where the behaviour is.
//
// The mutating ones are safe to generate only because a case now decodes its
// context per render; before that, one `lst.append(9)` poisoned every later
// comparison in the run.
func (g *generator) methodCall(depth int) string {
	switch g.c.intn(10) {
	case 0, 1, 2:
		return g.c.pick(strReceivers) + "." + g.c.pick(strMethods)
	case 3:
		return g.formatCall()
	case 4, 5:
		return g.c.pick(seqReceivers) + "." + g.c.pick(seqMethods)
	case 6:
		return g.c.pick(seqReceivers) + "." + g.c.pick(seqMutators)
	case 7, 8:
		return g.c.pick(dictReceivers) + "." + g.c.pick(dictMethods)
	default:
		return g.c.pick(dictReceivers) + "." + g.c.pick(dictMutators)
	}
}

// formatCall writes a str.format or str.format_map, whose field parser and
// spec expander are the largest thing in methods.go that nothing reached.
func (g *generator) formatCall() string {
	if g.c.chance(6) {
		return g.c.pick([]string{
			`'{a}'.format_map(d)`, `'{b}'.format_map(d)`,
			`'{nope}'.format_map(d)`, `'{a}{b}'.format_map(ed)`,
			`'{}'.format_map(d)`,
		})
	}
	return g.c.pick(formatCalls)
}

// The receivers are context names of the matching type, so the call is about
// the method rather than about the lookup failing.
var (
	strReceivers  = []string{"s", "t", "uni", "blank", "html", "'a,b,c'", "'Ab1'", "' x\ty '"}
	seqReceivers  = []string{"lst", "strs", "mix", "e", "pairs", "[3,1,2]"}
	dictReceivers = []string{"d", "ed", "nested"}
)

// strMethods are str's own, spelled with arguments that reach the branches:
// counts that run off the end, separators that are not strings, widths that are
// negative, and the whitespace/explicit fork in split and rsplit.
var strMethods = []string{
	"upper()", "lower()", "title()", "capitalize()", "swapcase()", "casefold()",
	"strip()", "strip('H')", "lstrip()", "rstrip('d ')",
	"split()", "split(',')", "split(',', 1)", "split('')", "split(None, 1)",
	"rsplit()", "rsplit(',')", "rsplit(',', 1)", "rsplit(None, 2)",
	"splitlines()", "splitlines(true)",
	"join(strs)", "join(lst)", "join([])", "join('ab')", "join(mix)",
	"replace('l', 'L')", "replace('l', 'L', 1)", "replace('', '-')", "replace('x', 'y')",
	"count('l')", "count('l', 2)", "count('l', 2, 4)", "count('')",
	"find('o')", "find('o', 5)", "index('o')", "index('zz')",
	"rfind('o')", "rindex('o')",
	"startswith('He')", "startswith(('a', 'He'))", "endswith('ld')",
	"zfill(10)", "zfill(0)", "zfill(-1)",
	"expandtabs()", "expandtabs(4)", "expandtabs(0)",
	"center(20)", "center(20, '-')", "ljust(20, '.')", "rjust(3)",
	"partition(' ')", "rpartition(' ')", "partition('zz')",
	"removeprefix('He')", "removesuffix('ld')",
	"isascii()", "isprintable()", "istitle()", "isidentifier()",
	"isdecimal()", "isdigit()", "isnumeric()", "isalpha()", "isalnum()",
	"isupper()", "islower()", "isspace()",
	"encode()", "encode('ascii')",
	"translate({72: 'X'})", "translate({})",
	"maketrans('ab', 'xy')", "maketrans({'a': 'z'})",
}

// formatCalls reach the field parser -- positional, named, attribute, index --
// and the spec expander, including a nested spec.
var formatCalls = []string{
	`'{}'.format(1)`, `'{} {}'.format(1, 2)`, `'{0}{0}'.format('a')`,
	`'{1}'.format(1)`, `'{}'.format()`,
	`'{x}'.format(x=1)`, `'{x}'.format(y=1)`,
	`'{0[1]}'.format(lst)`, `'{0[a]}'.format(d)`, `'{0[9]}'.format(lst)`,
	`'{0.imag}'.format(1)`, `'{0.nope}'.format(1)`,
	`'{:>10}'.format('a')`, `'{:<10}'.format('a')`, `'{:^10}'.format('a')`,
	`'{:-^10}'.format('a')`, `'{:.2f}'.format(1.5)`, `'{:+d}'.format(3)`,
	`'{:08.3f}'.format(1.5)`, `'{:x}'.format(255)`, `'{:#o}'.format(8)`,
	`'{:e}'.format(1.5)`, `'{:%}'.format(0.5)`, `'{:,}'.format(1000)`,
	`'{!r}'.format('a')`, `'{!s}'.format(1)`, `'{!a}'.format('é')`,
	`'{!q}'.format(1)`, `'{{}}'.format()`, `'{'.format()`, `'}'.format()`,
	`'{:{}}'.format(1, '>5')`, `'{:{w}}'.format(1, w=5)`,
	`'{:s}'.format(1)`, `'{:d}'.format('a')`, `'{:z}'.format(1)`,
}

// seqMethods are the ones that only read.
var seqMethods = []string{
	"index(1)", "index(99)", "index(1, 1)", "count(1)", "count('a')",
}

// seqMutators write, and every one of them returns None -- so what they are
// worth is what the receiver looks like afterwards, which the surrounding
// template prints.
var seqMutators = []string{
	"append(9)", "append([1])", "extend([1])", "extend('ab')", "extend(1)",
	"insert(0, 9)", "insert(99, 9)", "insert(-1, 9)",
	"pop()", "pop(0)", "pop(99)", "remove(1)", "remove(99)",
	"reverse()", "clear()", "sort()", "sort(reverse=true)",
}

var dictMethods = []string{
	"items()", "keys()", "values()", "items()|list", "keys()|list",
	"get('a')", "get('nope')", "get('nope', 7)", "get('a', 7)",
	"copy()",
}

var dictMutators = []string{
	"pop('a')", "pop('nope')", "pop('nope', 7)",
	"setdefault('a', 9)", "setdefault('new', 9)",
	"update({'z': 1})", "update(pairs)", "update(1)",
	"clear()", "popitem()",
}

func (g *generator) list(n int) string {
	parts := make([]string, 0, n)
	for range 1 + g.c.intn(n) {
		parts = append(parts, g.atom())
	}
	return strings.Join(parts, ", ")
}

// FuzzContext decodes the shared context for handing to gojja2.
func FuzzContext() (json.RawMessage, error) {
	var check any
	if err := json.Unmarshal([]byte(FuzzContextJSON), &check); err != nil {
		return nil, err
	}
	return json.RawMessage(FuzzContextJSON), nil
}
