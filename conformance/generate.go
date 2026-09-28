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

// templateSets are the auxiliary templates a case can be given, drawn per case.
//
// They were one fixed map, so `base.txt` was always two flat blocks and
// `mac.txt` always one macro and one `{% set %}` -- and a whole family of rules
// lives in what a *pulled-in* template contains rather than in the template
// doing the pulling. An {% import %} target is discarded from the importer's
// exports, a base that itself extends makes a three-level chain, a block inside
// a loop is registered once and rendered where it stands, and a name beginning
// with an underscore is never exported at all. None of it could be generated.
//
// Every set defines the three names the generator writes -- base.txt, inc.txt
// and mac.txt -- so any draw works with any template; the extra names are
// reached only from inside the set.
var templateSets = []struct {
	name      string
	templates map[string]string
}{
	{"flat", map[string]string{
		"base.txt": "[{% block a %}A{% endblock %}|{% block b %}B{% endblock %}]",
		"inc.txt":  "<{{ n|default('?') }}{{ item|default('') }}>",
		"mac.txt":  "{% macro m(x, y=2) %}({{ x }},{{ y }}){% endmacro %}{% set ex = 'E' %}",
	}},
	// A module that imports another, and one that hides a name: what a
	// template sees through `{% import 'mac.txt' as mm %}` is the exports,
	// and both of those change them.
	{"modules", map[string]string{
		"base.txt": "[{% block a %}A{% endblock %}|{% block b %}B{% endblock %}]",
		"inc.txt":  "<{{ n|default('?') }}{{ ex|default('') }}>",
		"mac.txt": "{% import 'inner.txt' as sub %}{% set _hidden = 'h' %}" +
			"{% macro m(x, y=2) %}({{ x }},{{ sub.q }}){% endmacro %}{% set ex = 'E' %}",
		"inner.txt": "{% set q = 9 %}{% macro im(z) %}I{{ z }}{% endmacro %}",
	}},
	// A base that extends its own base, so `{% extends 'base.txt' %}` is a
	// three-level chain and super() has two levels to walk.
	{"chain", map[string]string{
		"grand.txt": "G[{% block a %}GA{% endblock %}|{% block b %}GB{% endblock %}]",
		"base.txt":  "{% extends 'grand.txt' %}{% block a %}B{{ super() }}{% endblock %}",
		"inc.txt":   "<{{ n|default('?') }}>",
		"mac.txt":   "{% macro m(x, y=2) %}({{ x }},{{ y }}){% endmacro %}{% set ex = 'E' %}",
	}},
	// Blocks where a reader would not put them: inside a loop, inside
	// another block, and one that is scoped.
	{"blocks", map[string]string{
		"base.txt": "[{% for i in [1, 2] %}{% block a scoped %}{{ i }}{% endblock %}{% endfor %}|" +
			"{% block b %}B{% block inner %}I{% endblock %}{% endblock %}]",
		"inc.txt": "<{{ n|default('?') }}{{ item|default('') }}>",
		"mac.txt": "{% macro m(x, y=2) %}({{ x }},{{ y }}){% endmacro %}{% set ex = 'E' %}",
	}},
	// A template that writes to the caller's names and one that renders
	// something conditional, which is what a capture around an include has
	// to carry.
	{"stateful", map[string]string{
		"base.txt": "[{% block a %}A{% endblock %}|{% block b %}B{% endblock %}]",
		"inc.txt":  "{% set item = 'from-inc' %}<{{ item }}{{ n|default('?') }}>",
		"mac.txt": "{% macro m(x, y=2) %}{% filter upper %}({{ x }},{{ y }}){% endfilter %}" +
			"{% endmacro %}{% set ex = 'E' %}{% macro caller_user() %}{{ caller() }}{% endmacro %}",
	}},
}

// FuzzTemplateSets is the list above, for a caller that wants to name one.
func FuzzTemplateSets() []string {
	out := make([]string, len(templateSets))
	for i, s := range templateSets {
		out[i] = s.name
	}
	return out
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
	// `x is filter` and `x is test` ask the environment's registries
	// whether the *name x holds* is one, so they read a string rather than
	// the value's shape. Nothing generated reached either.
	"filter", "test",
}

var testWithArg = map[string][]string{
	"divisibleby": {"2", "3"},
	"eq":          {"1", "'a'", "n"},
	"ne":          {"1", "'a'"},
	"lt":          {"5"}, "le": {"5"}, "gt": {"0"}, "ge": {"0"},
	// jinja2's aliases for three of those. An alias is where a table
	// drifts, and nothing generated wrote one.
	"equalto": {"1", "'a'"}, "greaterthan": {"0"}, "lessthan": {"5"},
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
	// do and loopControls are the drawn extensions, which decide whether
	// the tags they add may be written at all. Without them the tag is not a
	// tag: `{% do 1 %}` is "Encountered unknown tag 'do'".
	do           bool
	loopControls bool
	// fnLoops counts the loops open around this point *within the function
	// jinja2 would generate*, which is what a break or a continue binds to
	// -- so a macro body resets it where inLoop would carry through. A
	// break with nothing to bind does not compile under CPython
	// (docs/divergences.md), and generating one would report a divergence
	// per template on the message alone.
	fnLoops int
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
	// Undefined names the Undefined class the case renders under: "" for
	// jinja2's default, or "strict", "chainable" or "debug".
	//
	// The differential varied autoescape and nothing else for a long time,
	// so sixty thousand templates a run all rendered under the default
	// Undefined -- and the generator writes undefined names constantly.
	// Which class is in force decides whether printing one raises, whether
	// reaching through one chains, and what it prints, so three quarters of
	// that dimension went unasked.
	Undefined string
	// Trim, Lstrip and KeepTrailingNewline are the lexer's whitespace
	// settings, and they change what the template *means* rather than what
	// it prints: trim_blocks eats the newline after a block tag,
	// lstrip_blocks eats the indentation before one, and
	// keep_trailing_newline decides whether the file's last one survives.
	//
	// They were the last dimension the differential left fixed. Sixty
	// thousand templates a run all lexed under jinja2's defaults, while the
	// generator writes `{%- ... -%}` and bare tags constantly and the
	// interaction between an explicit marker and an implicit setting is
	// exactly where a whitespace rule goes wrong. The corpus varies them on
	// a handful of hand-written cases and nothing else did.
	Trim                bool
	Lstrip              bool
	KeepTrailingNewline bool
	// LineCommentPrefix, when set, is the prefix that makes the rest of a
	// line a comment -- jinja2's line_comment_prefix. See asLineStatements.
	LineCommentPrefix string
	// LineStatementPrefix, when set, is the prefix that makes a whole line a
	// statement -- jinja2's line_statement_prefix. See asLineStatements.
	LineStatementPrefix string
	// Delimiters is a non-default set of tag delimiters, or nil for jinja2's
	// own. See Delimiters.
	Delimiters *Delimiters
	// NewlineSequence is what every newline in the *template* is rendered
	// as: "" for jinja2's default "\n", or "\r\n" or "\r".
	//
	// It is a lexer setting like the three above, and the one the corpus had
	// no case for at all: jinja2 normalises the newlines it finds in data
	// *and* inside a string literal before the parser sees them, so it
	// changes what a literal is -- `{{ 'a\r\nb'|length }}` is 4 under
	// "\r\n" and 3 under "\n". The generator writes newlines constantly.
	NewlineSequence string
	// Extensions names the optional tags the environment enables: any of
	// "do" and "loopcontrols".
	//
	// They were the last statements the generator could not write. The
	// parser knows `break`, `continue` and `do`, and every one of them needs
	// an extension the soak never enabled -- so sixty thousand templates a
	// run held none of the three, and the corpus graded them on two
	// hand-written cases. The loop-control pair is the interesting half:
	// jinja2 emits Python's own keywords for them, so what a break does to a
	// loop's `{% else %}` branch, to `loop.index`, and to a filtered loop's
	// generator is decided by Python and not by jinja2.
	Extensions []string
	// Templates are the auxiliary templates this case renders against, and
	// TemplateSet names the draw. Both engines are handed this map, so what
	// `{% import 'mac.txt' %}` finds is part of the case rather than a
	// constant of the harness. See templateSets.
	Templates   map[string]string
	TemplateSet string
}

// Delimiters is what opens and closes a tag. jinja2 lets all six be configured,
// and the lexer's whole job is finding them: a custom set changes what is data,
// where whitespace control attaches, and which of three openings a `{` starts.
// The corpus varies them on thirteen hand-written cases and the soak did not
// vary them at all.
//
// A drawn set is applied by rewriting the finished template rather than by
// threading six strings through every arm of the generator. That is the same
// template either way -- both engines are handed the identical bytes, which is
// all the comparison needs -- and it keeps the arms readable.
type Delimiters struct {
	BlockStart, BlockEnd     string
	VarStart, VarEnd         string
	CommentStart, CommentEnd string
}

// asLineStatements rewrites every `{% ... %}` tag as a line statement, which is
// the form the setting makes available: `{% if x %}` becomes "\n# if x\n".
//
// Two things stay as they were. A `{% raw %}` block, because jinja2 handles raw
// in its block scanner and `# raw` is an unknown tag there -- and everything
// inside one, because that is data. And the whitespace-control markers go with
// the tag they were on: a line statement has nowhere to put them. That costs
// this case the interaction between a marker and the setting, which is one
// reason the axis is drawn rather than always on.
//
// Rewriting the finished template rather than teaching every arm to write two
// forms, for the same reason the delimiters are: both engines are handed the
// same bytes.
func asLineStatements(text, prefix, commentPrefix string) string {
	if prefix == "" && commentPrefix == "" {
		return text
	}
	var b strings.Builder
	raw := false
	for i := 0; i < len(text); {
		// A comment goes the same way, when a comment prefix was drawn:
		// `{# c #}` becomes "\n## c\n". The rest of the line after the
		// prefix is the comment, so the newline after it is what ends
		// one -- and a comment holding a newline of its own cannot be
		// written this way at all, which is why the text is checked.
		if commentPrefix != "" && !raw && strings.HasPrefix(text[i:], "{#") {
			if end := strings.Index(text[i:], "#}"); end >= 0 {
				inner := strings.TrimSpace(strings.Trim(
					text[i+2:i+end], "-+ \t"))
				if !strings.ContainsAny(inner, "\r\n") {
					b.WriteString("\n" + commentPrefix + " " + inner + "\n")
					i += end + 2
					continue
				}
			}
		}
		if prefix == "" || !strings.HasPrefix(text[i:], "{%") {
			b.WriteByte(text[i])
			i++
			continue
		}
		end := strings.Index(text[i:], "%}")
		if end < 0 {
			b.WriteString(text[i:])
			break
		}
		tag := text[i : i+end+2]
		inner := strings.TrimSpace(strings.Trim(strings.TrimSuffix(
			strings.TrimPrefix(tag, "{%"), "%}"), "-+ \t"))
		name, _, _ := strings.Cut(inner, " ")
		switch {
		case raw:
			b.WriteString(tag)
			raw = name != "endraw"
		case name == "raw":
			b.WriteString(tag)
			raw = true
		case inner == "":
			b.WriteString(tag)
		default:
			b.WriteString("\n" + prefix + " " + inner + "\n")
		}
		i += end + 2
	}
	return b.String()
}

// delimiterSets are drawn against, weighted toward jinja2's own: a custom set
// is the unusual configuration, and a run where most templates used one would
// spend itself on the lexer and leave everything else thinner.
//
// The sequences are ones a generated template never writes by itself, so the
// rewrite cannot turn data into a delimiter: `[[` would, because a nested list
// literal starts with it.
var delimiterSets = []*Delimiters{
	nil, nil, nil, nil, nil, nil,
	{BlockStart: "<%", BlockEnd: "%>", VarStart: "<<", VarEnd: ">>",
		CommentStart: "<#", CommentEnd: "#>"},
	{BlockStart: "[%", BlockEnd: "%]", VarStart: "${", VarEnd: "}$",
		CommentStart: "[#", CommentEnd: "#]"},
}

// Rewrite puts text in these delimiters, or returns it unchanged for the
// default set.
func (d *Delimiters) Rewrite(text string) string {
	if d == nil {
		return text
	}
	return strings.NewReplacer(
		"{%", d.BlockStart, "%}", d.BlockEnd,
		"{{", d.VarStart, "}}", d.VarEnd,
		"{#", d.CommentStart, "#}", d.CommentEnd).Replace(text)
}

// undefinedKinds are drawn against, default-weighted: the others shift the whole
// run toward error paths, which is where the interesting answers are but not
// where every template should end up.
//
// StrictUndefined was absent from this list for as long as a folded constant
// disagreed about *when* it raises. Under strict a folded lookup becomes a
// strict undefined, and asking one for its truthiness or its text raises while
// folding -- which jinja2 lets out of from_string, so the template does not
// compile. gojja2 abandoned the fold instead and left the expression for the
// render, and for `~` it did worse: value.Str answers "" for every undefined,
// so `{{ (0.0).a ~ 1 }}` folded to "1" and the operand was not merely folded at
// the wrong moment but folded away. With the fold agreeing about the phase, the
// axis draws it like any other.
var undefinedKinds = []string{"", "", "", "strict", "chainable", "debug"}

// GenerateCase builds a template and the environment it renders under.
func GenerateCase(input []byte) GeneratedCase { return generateCase(input, true) }

// GenerateDefaultCase is GenerateCase under jinja2's *default* environment: the
// settings are still drawn, so the same bytes give the same template, and then
// dropped -- along with the delimiter and line-prefix rewrites that depend on
// them.
//
// It is for the properties that ask about the template rather than about the
// environment. TestEncodingTheSameMeansRenderingTheSame compares two templates
// that encode to the same tree and requires the same output: a difference in
// *settings* is not a missing distinction in the vocabulary, so the settings
// have to be either in the key or out of the draw. In the key they left 16 pairs
// to compare where there had been 890, because eleven settings agreeing by
// chance is rare; out of the draw they leave the question the property is
// actually asking.
func GenerateDefaultCase(input []byte) GeneratedCase { return generateCase(input, false) }

func generateCase(input []byte, withEnvironment bool) GeneratedCase {
	g := &generator{c: &chooser{b: input}}
	// Drawn before the template so that one byte decides it, and so that
	// the same bytes after it generate the same template either way --
	// which is what makes shrinking a diverging case keep its setting.
	autoescape := g.c.chance(3)
	undefined := g.c.pick(undefinedKinds)
	// Each is drawn on its own rather than as one of eight combinations, so
	// that a run holds every pair of them and not just the pairs a table
	// happened to list.
	trim := g.c.chance(3)
	lstrip := g.c.chance(3)
	keepNewline := g.c.chance(3)
	// Drawn before the template because the generator has to know: a tag an
	// extension did not add is a syntax error, not a statement.
	g.do = g.c.chance(3)
	g.loopControls = g.c.chance(3)
	// Weighted toward the default, which is what almost every template in
	// the world runs under.
	newline := g.c.pick([]string{"", "", "", "", "\r\n", "\r"})
	delims := delimiterSets[g.c.intn(len(delimiterSets))]
	// The line-statement prefix is the other lexer mode jinja2 has, and the
	// one the soak reached last: a whole line is a statement, which changes
	// where a tag ends and what the whitespace settings have to work with.
	linePrefix := g.c.pick([]string{"", "", "", "", "", "#", "%"})
	lineComment := g.c.pick([]string{"", "", "", "", "", "##", "//"})
	// Weighted toward the flat set, which is what the soak had always used,
	// so the other four are an addition rather than a replacement.
	set := templateSets[g.c.intn(len(templateSets)+3)%len(templateSets)]
	var extensions []string
	if g.do {
		extensions = append(extensions, "do")
	}
	if g.loopControls {
		extensions = append(extensions, "loopcontrols")
	}
	g.template()
	if !withEnvironment {
		return GeneratedCase{Source: g.b.String()}
	}
	return GeneratedCase{
		Source: delims.Rewrite(
			asLineStatements(g.b.String(), linePrefix, lineComment)),
		Autoescape:          autoescape,
		Undefined:           undefined,
		Trim:                trim,
		Lstrip:              lstrip,
		KeepTrailingNewline: keepNewline,
		Extensions:          extensions,
		NewlineSequence:     newline,
		Delimiters:          delims,
		LineStatementPrefix: linePrefix,
		LineCommentPrefix:   lineComment,
		Templates:           set.templates,
		TemplateSet:         set.name,
	}
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
	switch g.c.intn(16) {
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
			// A list of candidates, and one whose first entry is
			// missing: jinja2 takes the first that loads.
			"{% include ['nope.txt', 'inc.txt'] %}",
			"{% include ['nope.txt'] ignore missing %}",
			"{% extends ['nope.txt', 'base.txt'] %}{% block a %}A{% endblock %}",
			"{% import 'mac.txt' as mm without context %}{{ mm.m(1) }}",
			"{% from 'mac.txt' import m with context %}{{ m(1) }}",
		}))
	case 13:
		g.autoescapeStmt(depth)
	case 14:
		g.tagStmt()
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

// tagStmt emits a statement that is a tag rather than an expression in braces.
//
// `{% print %}` needs no extension and is jinja2's other Output node: a
// comma-separated *list* of expressions, with comma rules of its own. `{% do %}`
// and the loop controls are only tags when the drawn extensions added them.
func (g *generator) tagStmt() {
	// print twice, so a run without either extension still draws it.
	kinds := []string{"print", "print"}
	if g.do {
		kinds = append(kinds, "do")
	}
	if g.loopControls && g.fnLoops > 0 {
		kinds = append(kinds, "break", "continue")
	}
	switch g.c.pick(kinds) {
	case "do":
		g.open("{%")
		// A mutating call is what `do` is for, and it is the shape that
		// makes the *next* statement's answer depend on this one.
		g.b.WriteString("do " + g.c.pick([]string{
			g.expr(2),
			"lst.append(" + g.c.pick(smallInts) + ")",
			"d.update(k=1)",
			"lst.sort()",
			"nope.nothing",
		}))
		g.close("%}")
	case "break":
		g.open("{%")
		g.b.WriteString("break")
		g.close("%}")
	case "continue":
		g.open("{%")
		g.b.WriteString("continue")
		g.close("%}")
	default:
		g.open("{%")
		g.b.WriteString("print " + g.expr(2))
		for range g.c.intn(3) {
			g.b.WriteString(", " + g.expr(1))
		}
		g.close("%}")
	}
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
	g.fnLoops++
	wasRecursive := g.recursive
	g.recursive = g.recursive || recursive
	if recursive && g.c.chance(2) {
		g.b.WriteString("{{ loop(" + g.c.pick([]string{"[]", "lst", "i", "[i]"}) + ") }}")
	}
	// A guarded loop control, written here rather than left to the statement
	// table: that table only lands inside a loop by chance, and this is the
	// shape where the `{% else %}` branch, `loop.index` and a filtered loop's
	// generator each answer differently. Which side of the body it sits on
	// matters -- a control before the body leaves the rest of the pass
	// unreached, which is what makes jinja2 run the else branch.
	control := ""
	if g.loopControls && g.c.chance(3) {
		control = "{% if " + g.expr(1) + " %}{% " +
			g.c.pick([]string{"break", "continue"}) + " %}{% endif %}"
	}
	before := control != "" && g.c.chance(2)
	if before {
		g.b.WriteString(control)
	}
	g.body(depth - 1)
	if control != "" && !before {
		g.b.WriteString(control)
	}
	g.recursive = wasRecursive
	g.fnLoops--
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
	// A macro body is a function of its own, so a loop outside it does not
	// bind a break inside it -- CPython refuses the generated module.
	outerLoops := g.fnLoops
	g.fnLoops = 0
	g.body(depth - 1)
	g.fnLoops = outerLoops
	g.b.WriteString("[{{ x }}]{% endmacro %}")

	if g.c.chance(3) {
		g.b.WriteString("{% call mm(1) %}called{% endcall %}")
		return
	}
	// A call block with a *signature*: the macro calls caller(...) and the
	// arguments bind into the block's own frame, which is a scope nothing
	// else here builds.
	if g.c.chance(4) {
		g.b.WriteString("{% macro takes() %}<{{ caller(" +
			g.c.pick([]string{"1", "1, 2", "'a'", ""}) + ") }}>{% endmacro %}" +
			"{% call(" + g.c.pick([]string{"p", "p, q", "p=9"}) + ") takes() %}" +
			g.c.pick([]string{"{{ p }}", "{{ p|default('-') }}", "body"}) +
			"{% endcall %}")
		return
	}
	// A parameter named after one of the three specials. A macro gets
	// `caller`, `kwargs` or `varargs` only when its body reads one without
	// binding it first, and never when it has declared a parameter of that
	// name -- two rules that were both missing here, and that nothing could
	// write: every macro this wrote took `x` and `y`. The shapes that do not
	// compile are as much of the family as the ones that do, so they are
	// written too.
	if g.c.chance(5) {
		name := g.c.pick([]string{"caller", "kwargs", "varargs"})
		param := name
		if g.c.chance(2) {
			param += "=" + g.c.pick(smallInts)
		}
		body := g.c.pick([]string{
			"{{ " + name + " }}", "body",
			"{% set " + name + " = 1 %}{{ " + name + " }}",
			"{{ " + name + "|default('-') }}",
			"{% for " + name + " in [1] %}{{ " + name + " }}{% endfor %}",
		})
		if g.c.chance(3) {
			// The same names in a {% call %} block's signature, where
			// the block is the macro and the rule is the same one.
			g.b.WriteString("{% macro takes() %}<{{ caller(1) }}>{% endmacro %}" +
				"{% call(" + param + ") takes() %}" + body + "{% endcall %}")
			return
		}
		g.b.WriteString("{% macro sp(" + param + ") %}" + body + "{% endmacro %}" +
			"[{{ sp(" + g.c.pick([]string{"1", "", "1, 2", "1, z=2"}) + ") }}]")
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

// filterStmt writes a `{% filter %}` block, or the `{% set v | f %}` form, which
// is the same filter applied to a captured body and assigned instead of printed
// -- and the one where a filter that answers something other than a string keeps
// its value rather than failing the write.
//
// The filters carry arguments half the time. Bare names were all this wrote, so
// the argument binding of a *block* filter -- which is the same path a `|f(x)`
// takes and a different call site -- was never generated.
func (g *generator) filterStmt(depth int) {
	// A filter that answers something other than a string is only written in
	// the `{% set %}` form. jinja2 puts a filter *block's* result into its
	// output buffer as it stands and joins the buffer at the end, so one that
	// is not a string fails at the join -- naming an index into that buffer,
	// and after whatever the rest of the template did. Both are artefacts of
	// jinja2's code generator rather than behaviour to match, and
	// docs/divergences.md records them; generating it fills a run with the
	// same known divergence. The assigning form keeps the value and is
	// compared like anything else.
	f := g.c.pick([]string{
		"upper", "trim", "lower|trim", "escape",
		"replace('a', 'b')", "indent(2, true)", "truncate(5, true)",
		"center(9)", "wordwrap(4)", "default('d')", "join('-')",
	})
	if g.c.chance(3) {
		g.b.WriteString("{% set fv | " + g.c.pick([]string{
			f, "length", "list", "map('upper')|list", "int", "round(1)",
			"count", "first", "last", "wordcount",
		}) + " %}")
		g.body(depth - 1)
		g.b.WriteString("{% endset %}[{{ fv }}]")
		return
	}
	g.b.WriteString("{% filter " + f + " %}")
	g.body(depth - 1)
	g.b.WriteString("{% endfilter %}")
}

// --- expressions -------------------------------------------------------------

func (g *generator) expr(depth int) string {
	if depth <= 0 || g.c.exhausted() {
		return g.atom()
	}
	switch g.c.intn(17) {
	case 15:
		return g.methodCall(depth)
	case 16:
		if g.c.chance(3) {
			return g.classObject()
		}
		return g.methodCall(depth)
	case 0, 1, 2, 3:
		return g.atom()
	case 4:
		return g.binary(depth)
	case 14:
		switch {
		case g.c.chance(3):
			return g.wrongArity()
		case g.c.chance(3):
			return g.demandingFilter()
		case g.c.chance(3):
			return g.unvisited()
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

// unvisited draws the shapes `go tool cover` says a soak has never produced.
//
// Each one is a function the corpus grades and the render differential had never
// executed, which is a different gap from an untested one: the corpus pins the
// shape somebody thought of, and the soak is what puts it next to everything
// else. The list is meant to shrink -- an entry that stops being unvisited is
// one the rest of the generator now reaches on its own.
func (g *generator) unvisited() string {
	return g.c.pick([]string{
		// A range's three attributes, narrow and wide. rangeObject.bound
		// was at 0%: nothing generated asked a range for its bounds.
		"range(3).start", "range(1, 9, 2).stop", "range(1, 9, 2).step",
		"range(2 ** 70).start", "range(2 ** 70).stop", "range(2 ** 70).step",
		"range(2 ** 70)[1]", "range(-5, -1).start",
		// |pprint of a string too long for one line, which is the only
		// route to wordChunks, pformatString and splitLinesKeepingEnds --
		// a short repr is written whole and none of them run.
		"(t ~ t ~ t)|pprint", "(t ~ '\n' ~ t ~ '\n' ~ t)|pprint",
		"([t, t, t])|pprint", "(s ~ '\n' ~ s)|pprint",
		"({'k': t ~ t ~ t})|pprint",
		// markupsafe's __mod__ wraps each argument in a helper that
		// defines __str__, __repr__, __int__ and __float__ and nothing
		// else, so which conversion a spec asks for decides what happens.
		// markupConvert was at 0%.
		"('%s'|safe) % html", "('%r'|safe) % s", "('%a'|safe) % uni",
		"('%d'|safe) % '42'", "('%f'|safe) % '1.5'", "('%x'|safe) % 255",
		"('%c'|safe) % 60", "('%d'|safe) % 'zz'",
		// tuple.index, which the seq methods reach only through a list.
		"((1, 2, 1)).index(1)", "((1, 2)).index(9)", "((1, 2, 1)).index(1, 1)",
		// A groupby group's attributes and its tuple shape.
		"(users|groupby('city'))[0].grouper",
		"(users|groupby('city'))[0].list|length",
		"(users|groupby('city'))[0][0]",
		"(users|groupby('city'))[0]|list",
		// Two dict views compared, which is dictView.EqualsErr.
		"(d.keys() == d.keys())", "(d.items() == nested.items())",
		"(d.values() == d.values())", "(d.keys() == nested.keys())",
		// A non-ASCII decimal digit through the numeric parsers, which is
		// the only route to runeZero.
		"'\u0664\u0665'|int", "'\u0664.\u0665'|float",
		"'\u0664'|int(-1)", "('\u0664\u0665' ~ '')|int",
		// A format spec whose separators have to be counted into the
		// width, which is groupWidth and padGrouped.
		"'{:015,d}'.format(1234567)", "'{:015_x}'.format(1234567890)",
		"'{:020,.2f}'.format(1234567.891)", "'{:_>20_b}'.format(255)",
		// An `attribute=` whose part is another script's digit, which is
		// the only route to pyDigitValue: `int(x) if x.isdigit() else x`
		// accepts those and int() reads them, so "\u0664" is the index 4.
		"pairs|map(attribute='\u0664')|list", "users|selectattr('\u0664')|list",
		"pairs|map(attribute='0.\u0664')|list", "users|sort(attribute='\u0664')|list",
		// An underscore inside a number a `%` conversion is asked to read,
		// which has to sit between two digits. Only a *Markup* format
		// reads a string at all -- markupsafe's helper defines __int__ and
		// __float__, so `%d` coerces there and refuses a str everywhere
		// else.
		"('%d'|safe) % '1_0'", "('%d'|safe) % '_10'",
		"('%f'|safe) % '1_0.5'", "('%d'|safe) % '1__0'",
		"('%d'|safe) % '\u0664_\u0665'",
		// A groupby group hashed or compared as the tuple it is, which is
		// groupObject.AsTuple.
		"{(users|groupby('city'))[0]: 1}",
		"((users|groupby('city'))[0] == ((1, 2)))",
		"[(users|groupby('city'))[0]]|unique|list",
	})
}

// Two shapes are deliberately absent from unvisited, and stay at 0% under every
// soak: |pprint of a container that holds itself, whose output embeds an address
// (safeRepr and recursionID -- the corpus pins the shape, and docs/divergences.md
// records why nothing can grade the number), and a structure nested past
// maxPPrintDepth, which is a thousand levels and not something a generated
// template writes. A soak cannot answer for either.

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
	if g.c.chance(5) {
		// A *bytes* format is a different function in CPython --
		// PyBytes_Format -- with its own verbs, its own mapping rule and
		// its own wording, and the generator only ever wrote str ones.
		// Its arguments have to be bytes too, so they are drawn from a
		// list of their own rather than from the spec's.
		bs := bytesPercentSpecs[g.c.intn(len(bytesPercentSpecs))]
		return "('[" + bs.format + "]'.encode()) % " + g.c.pick(bs.args)
	}
	return "'[" + spec.format + "]' % " + g.c.pick(spec.args)
}

// bytesPercentSpecs is percentSpecs for a bytes format. `%s` is not a bytes
// verb -- `%b` is -- and an argument must be a bytes for the ones that take
// text, which is why this cannot reuse the list above.
var bytesPercentSpecs = []struct {
	format string
	args   []string
}{
	{"%b", []string{"'ab'.encode()", "s.encode()", "''.encode()"}},
	{"%s", []string{"'ab'.encode()", "1", "lst"}},
	{"%a", []string{"'ab'.encode()", "1.5", "none"}},
	{"%r", []string{"'ab'.encode()", "lst"}},
	{"%d", []string{"42", "-42", "1.7", "true"}},
	{"%x", []string{"255", "0"}},
	{"%f", []string{"1.5", "-1.5"}},
	{"%c", []string{"65", "'a'.encode()", "300", "'ab'.encode()", "lst"}},
	{"%08.2f", []string{"1.5"}},
	{"%%", []string{"1", "''.encode()"}},
	{"%(k)b", []string{"{'k': 'v'.encode()}", "d", "''.encode()"}},
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
		op := g.c.pick(compareOps)
		if (op == "in" || op == "not in") && g.c.chance(4) {
			// A dict *view* as the container: `k in d.keys()` looks
			// its item up rather than scanning, so it hashes it and
			// an unhashable one is a TypeError where an items or a
			// values view answers False. None of that was reached by
			// the differential at all -- the views' Contains,
			// HashesItems and Unhashable sat at 0%.
			return out + " " + op + " " + g.c.pick([]string{
				"d.keys()", "d.items()", "d.values()",
				"nested.keys()", "pairs|list", "d|list",
				// A *set*, which is what a view difference
				// answers and the only way a template can hold
				// one. value/set.go was at 0% under the
				// differential, and a set hashes what it is
				// asked about -- which is where the "internal
				// error in gojja2" on `{{ {} in d.keys() - 'a' }}`
				// was hiding.
				"(d.keys() - 'a')", "(d.items() - pairs)",
				"(d.keys() - [])", "(d.keys() - d)",
			})
		}
		out += " " + op + " " + g.expr(depth-1)
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

// demandingFilter pairs a filter with a subject that reaches its work.
//
// These filters were already generated and their insides still were not: every
// subject the generator builds is short, flat and shallow, so `|wordwrap` never
// met a word longer than its width, `|pprint` never met a structure deep enough
// to break across lines, and `|round` never met a halfway case. Six functions
// behind wordwrap, seven behind pprint and six behind round had never run.
//
// The pairing is the point. A filter's arguments are already varied above; what
// was missing is something for them to act on.
func (g *generator) demandingFilter() string {
	switch g.c.intn(4) {
	case 0:
		return g.c.pick(wrappable) + "|wordwrap(" + g.c.pick([]string{
			"5", "8", "11", "8, false", "8, true", "4, true, '\n'",
			"8, false, none, true", "1", "0",
		}) + ")"
	case 1:
		return g.c.pick(printable) + "|pprint"
	case 2:
		return g.c.pick(roundable) + "|round(" + g.c.pick([]string{
			"", "0", "1", "2", "-1", "-2", "20", "0, 'ceil'", "0, 'floor'",
			"1, 'common'", "2, 'ceil'", "-1, 'floor'",
		}) + ")"
	default:
		return g.c.pick(wrappable) + "|" + g.c.pick([]string{
			"truncate(5)", "truncate(5, true)", "truncate(8, false, '~')",
			"truncate(8, true, '~', 2)", "truncate(0)",
			"indent(2)", "indent(2, true)", "indent(2, true, true)",
			"center(30)", "wordcount", "striptags", "urlize", "urlize(10)",
		})
	}
}

// wrappable are subjects with something for a wrapper to do: words longer than
// any width generated, hyphens to break at (or not), newlines already in place,
// and the empty string.
var wrappable = []string{
	"'antidisestablishmentarianism'",
	"'a-very-long-hyphenated-thing-indeed'",
	"'short and-then a-really-long-hyphenated-word at the end'",
	"'line one\nline two\nline three'",
	"'no-break' ~ 'ing-here-at-all'",
	"t", "s", "uni", "blank", "html",
	"'tabs\tand  double  spaces'",
	"'a b c d e f g h i j k l m n o p'",
}

// printable are subjects deep or long enough for pprint to lay out rather than
// print on one line -- including a list that contains itself, which is only
// buildable now that the generator can call append.
var printable = []string{
	"nested", "users", "d", "pairs",
	"[[[[[1, 2]]]]]",
	"{'a': {'b': {'c': {'d': [1, 2, 3]}}}}",
	"[1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20]",
	"['a long string that will not fit on one line with the others', 1, 2]",
	"{'k': 'a long string that will not fit on one line with the others'}",
	"lst", "e", "mix",
}

// roundable include the halfway cases, which are the whole of round's
// difficulty: Python rounds half to even, and the naive implementation does not.
var roundable = []string{
	"0.5", "1.5", "2.5", "-0.5", "-1.5", "-2.5",
	"1.15", "2.675", "0.125", "1.005",
	"1234.5678", "-1234.5678", "0.0", "-0.0",
	"f", "fz", "fneg", "n", "zero", "neg",
}

// classObject writes a `__class__` chain, which nothing generated reached.
//
// classes.go was entirely at 0% under the render differential while the corpus
// graded it: the generator had no way to write `__class__` at all, so the class
// object never met a filter, a comparison or a subscript. The answers are stable
// -- `<class 'int'>`, `int` -- which is what makes them worth generating rather
// than screening out.
//
// `__mro__` is deliberately absent: it is not implemented, for the reason
// docs/divergences.md gives, and generating a known divergence would fail every
// soak rather than teach anything.
func (g *generator) classObject() string {
	// `list` and `dict` are the two classes here that Python 3.9 made
	// subscriptable as a type annotation, so `lst.__class__['a']` is the
	// generic alias `list['a']` on CPython and undefined here -- the
	// divergence docs/divergences.md records for the `dict` global,
	// reached a second way. jinja2's attribute fallback puts `.a` in the
	// same position. So a class object from one of those subjects is never
	// handed onwards bare; it is either called or reduced to a string.
	subject := g.c.pick([]string{
		"n", "s", "yes", "nil", "f", "uni", "html", "nope",
		"(1.5)", "(1)", "'x'", "none", "true",
		"namespace()", "cycler('a','b')", "joiner('-')", "range(3)",
		// Parenthesised: `'x'|safe.__class__` is the dotted filter name
		// `safe.__class__`, not the class of a Markup, and the arm that
		// was meant to reach markupsafe.Markup reached nothing at all.
		"('x'|safe)", "('ab'.encode())",
	})
	if g.c.chance(3) {
		// The builtins with a __class_getitem__, which answer a generic
		// alias under a subscript rather than refusing it. A tuple
		// belongs here: `tuple['a']` is `tuple[str]` just as `list['a']`
		// is, and drawing it as an ordinary subject let a soak reach the
		// divergence through `|sum(attribute='age')`.
		subject = g.c.pick([]string{
			"lst", "d", "[1]", "{}", "dict(a=1)", "((1, 2))",
		})
	}
	chain := subject + ".__class__"
	// Calling one of these classes gives an instance whose repr embeds a
	// memory address -- jinja2 prints `<jinja2.utils.Joiner object at 0x...>`
	// -- so for them the class is compared, subscripted or reduced and never
	// called. Printing the class itself is fine; it is the instance that has
	// no gradable answer. A soak drew
	// `joiner('-').__class__('42')|upper|map(attribute='nope', default='?')|list`
	// and compared one address's worth of characters against another's.
	opaque := map[string]bool{
		"namespace()": true, "cycler('a','b')": true, "joiner('-')": true,
	}

	// Calling a class object is the rest of what a type object does, and the
	// render differential reached none of it: every constructor in classes.go
	// sat at 0% under a soak while the corpus graded it. Arguments are kept
	// small on purpose -- `bytes(n)` allocates what it is told, and a soak is
	// not the place to find that out.
	if !opaque[subject] && g.c.chance(2) {
		return chain + g.c.pick([]string{
			"()", "()", "(5)", "('42')", "(s)", "(f)", "(n)", "(yes)",
			"(nil)", "([1])", "('ab')", "(lst)", "(d)", "('x', 2)",
			"('10', 2)", "(zz=1)", "(1, 2, 3, 4)",
		})
	}
	// Two class objects compare by the class they name, and a class object
	// compares against the class *global* it is -- which is a second thing
	// nothing generated reached.
	if g.c.chance(4) {
		// Parenthesised, because a filter binds tighter than `==`: an
		// outer `|dictsort` would land on the right operand alone and
		// call a method on a bare class object, which is the unbound
		// method divergence rather than anything about comparing. That
		// escaped a 60,000-template soak once already.
		return "(" + chain + g.c.pick([]string{
			" == " + g.c.pick([]string{"n", "s", "lst", "d"}) + ".__class__",
			" != n.__class__", " == dict", " == range", " == namespace",
			" in [n.__class__, s.__class__]",
		}) + ")"
	}
	// A container hands the class object onwards just as a bare one does, and
	// an outer arm then does whatever it likes with what it walks -- so the
	// arm is open only to the classes for which every such operation agrees.
	//
	// Two soaks found the two that do not. A generic class is a GenericAlias
	// under a subscript, which `|sum(attribute='age')` performs:
	// `[[1].__class__, [1].__class__]|unique|list|sum(attribute='age')`
	// reached `list['age']`. And markupsafe's Markup carries an `__html__`
	// that an autoescaping `|join` calls unbound:
	// `[('x'|safe).__class__, n.__class__]|unique|list|join(*['-'])` is
	// "Markup.__html__() missing 1 required positional argument". Both are
	// divergences docs/divergences.md records and the corpus pins.
	//
	// The list is therefore an allowlist rather than a set of exclusions: a
	// class not named here is handed on bare, called, compared or reduced by
	// the arms above and below, where the shape is known.
	containerSafe := map[string]bool{
		"n": true, "f": true, "yes": true, "nil": true, "none": true,
		"true": true, "(1)": true, "(1.5)": true, "range(3)": true,
	}
	if containerSafe[subject] && g.c.chance(6) {
		return g.c.pick([]string{
			"{" + chain + ": 1}",
			"[" + chain + ", n.__class__]|unique|list",
			// |list, never |length: a lazy filter answers a
			// generator in jinja2, and asking one for a length is
			// the divergence docs/divergences.md records rather
			// than anything about hashing a class object.
			"[" + chain + ", " + chain + "]|unique|list",
		})
	}
	if g.c.chance(4) {
		return g.unboundMethod()
	}
	// A *bare* class object is never handed onwards: `list` and `dict` are
	// generic aliases under a subscript, which is a documented divergence,
	// and jinja2's attribute fallback puts a plain `.a` in the same
	// position. Printing one is covered by the corpus instead, where the
	// shape is pinned rather than left to an outer arm to choose.
	return chain + g.c.pick([]string{
		".__name__", ".__name__|upper", "|string", "|length",
	})
}

// unboundMethod builds a call on a type object's method with no instance --
// `dict.items(d)`, which is `d.items()`, and `str.upper('a')`, which is `'A'`.
//
// The class and the method are drawn together so the method always exists.
// A name a class does *not* have is two documented divergences rather than
// anything about descriptors: jinja2's attribute fallback answers a generic
// alias for `list` and `dict` (`list['nope']`, whose call is the constructor),
// and markupsafe overrides str's methods with plain functions whose repr
// carries a memory address. The corpus pins both; a soak cannot grade an
// address. Dunders are left out for the same reason the corpus records them as
// a divergence: CPython answers a slot wrapper and gojja2 answers undefined.
//
// The receiver is drawn independently of the class, so the descriptor's
// refusal -- a receiver of the wrong class, or none at all -- is generated
// alongside the call that works, and with it the arity error that comes from
// the method rather than from the descriptor. Only methods that leave their
// receiver alone are drawn: `list.append(lst, 1)` would edit the context a
// later arm in the same template still reads, which is a difference about how
// each side copies a context and not about the descriptor.
func (g *generator) unboundMethod() string {
	classes := []struct {
		subject string
		methods []string
	}{
		{"s", []string{"upper", "lower", "strip", "split", "count", "index",
			"find", "startswith", "encode", "replace", "title", "maketrans"}},
		{"lst", []string{"count", "index", "copy"}},
		{"d", []string{"items", "keys", "values", "get", "copy", "fromkeys"}},
		{"((1, 2))", []string{"count", "index"}},
		{"('ab'.encode())", []string{"hex", "decode", "upper", "count", "fromhex"}},
		{"n", []string{"bit_length", "to_bytes", "conjugate", "from_bytes"}},
		{"f", []string{"is_integer", "hex", "conjugate", "fromhex"}},
		{"yes", []string{"bit_length", "conjugate"}},
	}
	class := classes[g.c.intn(len(classes))]
	recv := g.c.pick([]string{
		"", "d", "s", "lst", "n", "f", "'x'", "[1]", "{}", "((1, 2))",
		"('ab'.encode())", "d, 'a'", "s, 'x'", "lst, 1", "s, 1, 2",
		"d, 'a', 0", "n, 2",
	})
	return class.subject + ".__class__." +
		class.methods[g.c.intn(len(class.methods))] + "(" + recv + ")"
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
	switch g.c.intn(11) {
	case 0, 1, 2:
		return g.c.pick(strReceivers) + "." + g.c.pick(strMethods)
	case 3:
		return g.formatCall()
	case 4, 5:
		return g.c.pick(seqReceivers) + "." + g.c.pick(seqMethods)
	case 6:
		return g.c.pick(seqReceivers) + "." + g.c.pick(seqMutators)
	case 7:
		return g.c.pick(dictReceivers) + "." + g.c.pick(dictMethods)
	case 8:
		return g.c.pick(dictReceivers) + "." + g.c.pick(dictMutators)
	case 9:
		return g.c.pick(numReceivers) + "." + g.c.pick(numMethods)
	default:
		// Bytes exist here only because a template can make them: the
		// shared context is JSON, and JSON has no bytes value. So the
		// receiver is always an encode(), which is also the one method
		// that gets from a str to a bytes at all.
		return g.c.pick(bytesReceivers) + "." + g.c.pick(bytesMethods)
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
	if g.c.chance(3) {
		return g.formatSpecCall()
	}
	return g.c.pick(formatCalls)
}

// formatSpecCall composes a spec out of its parts rather than drawing a whole
// one from a table, because what the mini-language gets wrong is the
// *combinations*: a separator's legality depends on the presentation type, a
// width has to be counted with the separators in it, and two of the parts
// complain about each other while the spec is still being read. A table of
// finished specs reached none of that -- the grouped width, the grouped
// mantissa and 'g' choosing a shape were all unvisited until this arm existed.
func (g *generator) formatSpecCall() string {
	var spec strings.Builder
	if g.c.chance(4) {
		spec.WriteString(g.c.pick([]string{"_>", ".>", "0<", "*^", "=", ">", "<", "^"}))
	}
	if g.c.chance(3) {
		spec.WriteString(g.c.pick([]string{"+", "-", " "}))
	}
	if g.c.chance(6) {
		spec.WriteString("z")
	}
	if g.c.chance(4) {
		spec.WriteString("#")
	}
	if g.c.chance(3) {
		spec.WriteString("0")
	}
	if g.c.chance(2) {
		spec.WriteString(g.c.pick([]string{"5", "15", "0", "1"}))
	}
	if g.c.chance(2) {
		// Both separators and both orders: the pair is refused, and which
		// message it gets depends on whether they differ.
		spec.WriteString(g.c.pick([]string{",", "_", ",_", "_,", ",,", "__"}))
	}
	if g.c.chance(2) {
		// A bare dot is "Format specifier missing precision", and a signed
		// or spaced one is too -- so the dot is drawn apart from its digits.
		spec.WriteString("." + g.c.pick([]string{"0", "2", "30", "", "-5", " 5"}))
	}
	if g.c.chance(2) {
		spec.WriteString(g.c.pick([]string{
			"d", "f", "F", "e", "E", "g", "G", "%", "n", "s", "c",
			"b", "o", "x", "X", "q",
		}))
	}
	return "'{:" + spec.String() + "}'.format(" + g.c.pick([]string{
		"1", "0", "1234567890", "-1234567", "1.5", "0.0001", "1e20", "1e-20",
		"-0.0", "123456.789", "'a'", "true", "none", "lst",
	}) + ")"
}

// The receivers are context names of the matching type, so the call is about
// the method rather than about the lookup failing.
var (
	strReceivers = []string{"s", "t", "uni", "blank", "html", "'a,b,c'", "'Ab1'", "' x\ty '",
		// Code points whose case mapping or case predicate changed
		// between 3.11 and 3.14, so a casing method over one is a
		// question the version axis can answer differently.
		// value/unicode_compat.go -- the whole of the per-interpreter
		// override path -- was at 0% under the differential, which
		// meant the absolute tables were graded by the corpus alone.
		// U+019B and U+A7CD gained an uppercase in 3.14, U+1C89 and
		// U+A7CB a lowercase, U+10D50 a fold.
		"'\u019b\u0264'", "'\u1c89\u1c8a'", "'\ua7cb\ua7cd'",
		"'\ua7da\ua7db\ua7dc'", "'\U00010d50\U00010d70'",
		"'\u019bA\u1c89 b'"}
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

// numReceivers are ints and floats, in both flavours: a literal in parentheses
// as Python requires, and a context name that carries the same kind.
var numReceivers = []string{
	"n", "m", "neg", "zero", "one", "f", "fz", "fneg",
	"(255)", "(0)", "(-1)", "(2.5)", "(0.0)", "(-0.0)",
}

// numMethods are int's and float's own. from_bytes and fromhex are
// classmethods, which Python lets an instance call, and that is the only route
// to them from a template: there is no `int` global to call them on.
var numMethods = []string{
	"bit_length()", "bit_count()", "as_integer_ratio()", "conjugate()",
	"real", "imag", "numerator", "denominator",
	"to_bytes()", "to_bytes(2, 'big')", "to_bytes(2, 'little')",
	"to_bytes(1, 'big')", "to_bytes(0, 'big')", "to_bytes(2, 'sideways')",
	"to_bytes(-1, 'big')",
	"from_bytes('ab'.encode(), 'big')", "from_bytes('ab'.encode(), 'nope')",
	// An *iterable* of integers, which int.from_bytes takes as readily as a
	// bytes and which nothing generated: fromBytesSource's walk sat at one
	// branch out of six.
	"from_bytes(lst)", "from_bytes([1, 2])", "from_bytes(range(3))",
	"from_bytes(strs)", "from_bytes(d)", "from_bytes([300])",
	"is_integer()", "hex()", "fromhex('0x1p3')", "fromhex('nope')",
}

// bytesReceivers are the ways a template can get a bytes at all.
var bytesReceivers = []string{
	"'ab'.encode()", "''.encode()", "uni.encode()", "s.encode()",
	"'a,b,c'.encode()", "' x '.encode()",
}

// bytesMethods mirror the str ones, plus the two that only bytes have.
var bytesMethods = []string{
	"upper()", "lower()", "title()", "capitalize()", "swapcase()",
	"strip()", "strip('a'.encode())", "lstrip()", "rstrip()",
	"split()", "split(','.encode())", "split(','.encode(), 1)", "split('')",
	"rsplit()", "rsplit(','.encode())", "splitlines()",
	"join(['a'.encode(), 'b'.encode()])", "join([])", "join(strs)",
	"replace('a'.encode(), 'X'.encode())", "replace('a'.encode(), 'X'.encode(), 1)",
	"count('a'.encode())", "find('b'.encode())", "index('z'.encode())",
	"rfind('b'.encode())", "startswith('a'.encode())", "endswith('c'.encode())",
	"partition(','.encode())", "rpartition(','.encode())",
	"removeprefix('a'.encode())", "removesuffix('c'.encode())",
	"center(10)", "center(10, '-'.encode())", "ljust(10)", "rjust(10)",
	"zfill(10)", "expandtabs()", "expandtabs(4)",
	"isalpha()", "isdigit()", "isalnum()", "isspace()", "isascii()",
	"islower()", "isupper()", "istitle()",
	"hex()", "hex('-')", "fromhex('4142')", "fromhex('zz')",
	"decode()", "decode('ascii')", "decode('nope')",
	"maketrans('a'.encode(), 'z'.encode())",
	"translate(none)", "translate(none, 'a'.encode())",
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
