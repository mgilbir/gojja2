// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

// Package parser turns a token stream into an AST.
//
// The grammar, the operator precedence and the error messages all follow
// jinja2's parser, because a template that fails to parse there must fail here
// with the same complaint on the same line.
package parser

import (
	"fmt"
	"strings"

	"github.com/mgilbir/gojja2/errs"
	"github.com/mgilbir/gojja2/internal/ast"
	"github.com/mgilbir/gojja2/internal/lexer"
	"github.com/mgilbir/gojja2/value"
)

// MaxNestingDepth bounds how deeply expressions and statements may nest.
//
// This is a safety control, not a tuning knob. Go grows goroutine stacks on
// demand, so a template of a million nested brackets parses happily and then
// takes the process down -- where CPython would have raised RecursionError
// long before. Templates are frequently attacker-supplied; nothing anyone
// writes on purpose nests a hundred deep, let alone this.
//
// What is bounded is the depth of the *tree*, one level per node on a
// root-to-leaf path, and that is the property the bound has to have: the
// parser is not the only thing that walks this tree recursively. The constant
// folder, the frame-local visitor, the dependency checker and the evaluator
// all descend it once per level, so an AST the parser accepted but they cannot
// walk is no safer than one the parser could not build.
//
// Counting call depth instead missed every construct that nests *iteratively*.
// `not not not ...`, `-----...`, `x|f|f|f...` and `a.b.c.d...` all build a tree
// as deep as they are long from a loop or a tail call, so a 500 KB template of
// `not ` was accepted by the parser and then overflowed the stack inside the
// constant folder -- at compile time, where there is no render to bound and no
// context to cancel.
const MaxNestingDepth = 1000

// Options controls the optional tags an environment enables.
type Options struct {
	// Do enables `{% do %}` (jinja2.ext.do).
	Do bool
	// LoopControls enables `{% break %}` and `{% continue %}`
	// (jinja2.ext.loopcontrols).
	LoopControls bool
}

// Parse lexes and parses a template.
func Parse(syn lexer.Syntax, opts Options, source, name string) (tmpl *ast.Template, err error) {
	tokens, lexErr := lexer.Tokenize(syn, source, name)
	p := &parser{tokens: tokens, name: name, source: source, opts: opts, lexErr: lexErr}
	defer func() {
		if r := recover(); r != nil {
			bail, ok := r.(parseError)
			if !ok {
				panic(r)
			}
			tmpl, err = nil, bail.err
		}
	}()
	body := p.subparse(nil)
	p.expect(kindRule(lexer.EOF))
	return &ast.Template{Body: body}, nil
}

// parseError carries a parse failure out through the recursive descent without
// threading an error return through every production.
type parseError struct{ err error }

type parser struct {
	tokens []lexer.Token
	pos    int
	name   string
	source string
	opts   Options

	// tagStack names the blocks currently open, innermost last. jinja2
	// reports it when a template ends without closing one.
	tagStack []string
	// endTokenStack holds, per open block, the tags that would close it.
	endTokenStack [][]rule
	// lexErr is a lexing failure that is only reported once the parser
	// asks for the token where it happened.
	lexErr error
	// depth is the current nesting of the recursive descent.
	depth int
}

// enter opens one level of the tree being built, failing cleanly instead of
// letting it grow past what anything walking it can descend. Every production
// that constructs a node calls it, and only those: a helper that merely passes
// an expression along adds no level and must not charge for one, or the bound
// would depend on how many layers of grammar a shape happens to traverse
// rather than on how deep the result is.
func (p *parser) enter() {
	p.depth++
	if p.depth > MaxNestingDepth {
		p.fail("expression or statement nests deeper than %d levels", MaxNestingDepth)
	}
}

func (p *parser) leave() { p.depth-- }

// chain counts the levels a loop opens.
//
// A left-associative run -- `a|f|f|f`, `a.b.c.d`, `1+x+x+x` -- is built by a
// loop rather than by recursion, but the tree it produces is as deep as the
// run is long. Each pass therefore opens a level, and the whole run is
// released together when the chain is complete, so that an expression nested
// *inside* one of its arguments sees the depth already spent.
type chain struct {
	p    *parser
	open int
}

func (p *parser) chain() *chain { return &chain{p: p} }

// level charges for the node this pass of the loop is about to build.
func (c *chain) level() {
	c.p.enter()
	c.open++
}

// done releases the whole run. It is deferred, so it runs on the way out of a
// failed parse as well -- not that the parse continues, but leaving the
// counter consistent keeps the failure honest about where it happened.
func (c *chain) done() { c.p.depth -= c.open }

// --- token stream ------------------------------------------------------------

// rule matches a token by kind, and for names optionally by text. It is the
// Go form of jinja2's "name:endfor" / "block_end" token expressions.
type rule struct {
	kind lexer.Kind
	val  string
}

func nameRule(v string) rule     { return rule{kind: lexer.Name, val: v} }
func kindRule(k lexer.Kind) rule { return rule{kind: k} }
func nameRules(vs ...string) []rule {
	rs := make([]rule, len(vs))
	for i, v := range vs {
		rs[i] = nameRule(v)
	}
	return rs
}

// describe renders a rule the way an error message names it.
func (r rule) describe() string {
	if r.kind == lexer.Name && r.val != "" {
		return r.val
	}
	return r.kind.Describe()
}

func (p *parser) current() lexer.Token {
	tok := p.tokens[p.pos]
	if tok.Kind == lexer.EOF && p.lexErr != nil {
		// The stream ran out because lexing failed here, not because
		// the template ended.
		panic(parseError{err: p.lexErr})
	}
	return tok
}

func (p *parser) look() lexer.Token {
	if p.pos+1 < len(p.tokens) {
		return p.tokens[p.pos+1]
	}
	return p.tokens[len(p.tokens)-1]
}

func (p *parser) next() lexer.Token {
	tok := p.tokens[p.pos]
	if p.pos < len(p.tokens)-1 {
		p.pos++
	}
	return tok
}

func (p *parser) test(r rule) bool {
	tok := p.current()
	if tok.Kind != r.kind {
		return false
	}
	return r.val == "" || tok.Value == r.val
}

func (p *parser) testAny(rs ...rule) bool {
	for _, r := range rs {
		if p.test(r) {
			return true
		}
	}
	return false
}

func (p *parser) skipIf(r rule) bool {
	if p.test(r) {
		p.next()
		return true
	}
	return false
}

func (p *parser) expect(r rule) lexer.Token {
	if !p.test(r) {
		want := quote(r.describe())
		if p.current().Kind == lexer.EOF {
			p.failAt(p.current().Line, "unexpected end of template, expected %s.", want)
		}
		p.failAt(p.current().Line, "expected token %s, got %s",
			want, quote(p.current().Describe()))
	}
	return p.next()
}

// quote renders a string the way Python's %r does, which is how jinja2 quotes
// token names in its messages.
func quote(s string) string { return value.Repr(value.String(s)) }

// pyQuote renders a name the way CPython quotes one inside an error message.
func pyQuote(s string) string { return "'" + s + "'" }

func (p *parser) fail(format string, args ...any) {
	p.failAt(p.current().Line, format, args...)
}

func (p *parser) failAt(line int, format string, args ...any) {
	p.failKindAt(errs.TemplateSyntaxError, line, format, args...)
}

func (p *parser) failKindAt(kind errs.Kind, line int, format string, args ...any) {
	e := errs.New(kind, format, args...)
	e.Line = line
	e.Name = p.name
	e.Source = p.source
	panic(parseError{err: e})
}

// --- statements --------------------------------------------------------------

// subparse consumes template data, print tags and statements until one of the
// end rules is reached, or until the stream runs out when end is nil.
func (p *parser) subparse(end []rule) []ast.Stmt {
	var body []ast.Stmt
	var buffer []ast.Expr

	if end != nil {
		p.endTokenStack = append(p.endTokenStack, end)
		defer func() { p.endTokenStack = p.endTokenStack[:len(p.endTokenStack)-1] }()
	}

	// Adjacent data and print tags collapse into one Output node, so
	// `a{{ b }}c` produces a single node rather than three.
	flush := func() {
		if len(buffer) == 0 {
			return
		}
		body = append(body, &ast.Output{Pos: ast.At(buffer[0].Line()), Nodes: buffer})
		buffer = nil
	}

	for {
		tok := p.current()
		switch tok.Kind {
		case lexer.Data:
			if tok.Value != "" {
				buffer = append(buffer,
					&ast.TemplateData{Pos: ast.At(tok.Line), Data: tok.Value})
			}
			p.next()
		case lexer.VariableBegin:
			p.next()
			buffer = append(buffer, p.parseTuple(tupleOpts{}))
			p.expect(kindRule(lexer.VariableEnd))
		case lexer.BlockBegin:
			flush()
			p.next()
			if end != nil && p.testAny(end...) {
				return body
			}
			body = append(body, p.parseStatement()...)
			p.expect(kindRule(lexer.BlockEnd))
		default:
			flush()
			return body
		}
	}
}

// statementParsers maps a tag name to its production.
func (p *parser) parseStatement() []ast.Stmt {
	p.enter()
	defer p.leave()

	tok := p.current()
	if tok.Kind != lexer.Name {
		p.failAt(tok.Line, "tag name expected")
	}

	var parse func() []ast.Stmt
	switch tok.Value {
	case "for":
		parse = one(p.parseFor)
	case "if":
		parse = one(p.parseIf)
	case "block":
		parse = one(p.parseBlock)
	case "extends":
		parse = one(p.parseExtends)
	case "print":
		parse = one(p.parsePrint)
	case "macro":
		parse = one(p.parseMacro)
	case "include":
		parse = one(p.parseInclude)
	case "from":
		parse = one(p.parseFrom)
	case "import":
		parse = one(p.parseImport)
	case "set":
		parse = one(p.parseSet)
	case "with":
		parse = one(p.parseWith)
	case "autoescape":
		parse = one(p.parseAutoescape)
	case "call":
		parse = one(p.parseCallBlock)
	case "filter":
		parse = one(p.parseFilterBlock)
	case "do":
		if p.opts.Do {
			parse = one(p.parseDo)
		}
	case "break":
		if p.opts.LoopControls {
			parse = one(p.parseBreak)
		}
	case "continue":
		if p.opts.LoopControls {
			parse = one(p.parseContinue)
		}
	}
	if parse == nil {
		p.failUnknownTag(tok.Value, tok.Line)
	}

	p.tagStack = append(p.tagStack, tok.Value)
	defer func() { p.tagStack = p.tagStack[:len(p.tagStack)-1] }()
	return parse()
}

// one adapts a single-node production to the list-returning dispatch.
func one[T ast.Stmt](f func() T) func() []ast.Stmt {
	return func() []ast.Stmt { return []ast.Stmt{f()} }
}

// parseStatements parses a block body up to one of the end rules. With
// dropNeedle the matched end tag is consumed; otherwise it is left current.
func (p *parser) parseStatements(end []rule, dropNeedle bool) []ast.Stmt {
	// A leading colon is tolerated, for the Python-flavoured `{% if x: %}`.
	p.skipIf(kindRule(lexer.Colon))
	p.expect(kindRule(lexer.BlockEnd))
	body := p.subparse(end)

	if p.current().Kind == lexer.EOF {
		p.failEOF(end)
	}
	if dropNeedle {
		p.next()
	}
	return body
}

func (p *parser) parseFor() *ast.For {
	line := p.expect(nameRule("for")).Line
	target := p.parseAssignTarget(assignOpts{extraEnd: nameRules("in")})
	p.expect(nameRule("in"))
	iter := p.parseTuple(tupleOpts{noCondExpr: true, extraEnd: nameRules("recursive")})

	var test ast.Expr
	if p.skipIf(nameRule("if")) {
		test = p.parseExpression(true)
	}
	recursive := p.skipIf(nameRule("recursive"))

	body := p.parseStatements(nameRules("endfor", "else"), false)
	var orElse []ast.Stmt
	if p.next().Value != "endfor" {
		orElse = p.parseStatements(nameRules("endfor"), true)
	}
	return &ast.For{
		Pos: ast.At(line), Target: target, Iter: iter,
		Body: body, Else: orElse, Test: test, Recursive: recursive,
	}
}

func (p *parser) parseIf() *ast.If {
	root := &ast.If{Pos: ast.At(p.expect(nameRule("if")).Line)}
	node := root
	for {
		node.Test = p.parseTuple(tupleOpts{noCondExpr: true})
		node.Body = p.parseStatements(nameRules("elif", "else", "endif"), false)

		tok := p.next()
		switch {
		case tok.Kind == lexer.Name && tok.Value == "elif":
			node = &ast.If{Pos: ast.At(p.current().Line)}
			root.Elif = append(root.Elif, node)
			continue
		case tok.Kind == lexer.Name && tok.Value == "else":
			root.Else = p.parseStatements(nameRules("endif"), true)
		}
		return root
	}
}

func (p *parser) parseSet() ast.Stmt {
	line := p.next().Line
	target := p.parseAssignTarget(assignOpts{withNamespace: true})
	if p.skipIf(kindRule(lexer.Assign)) {
		return &ast.Assign{Pos: ast.At(line), Target: target, Node: p.parseTuple(tupleOpts{})}
	}
	filter := p.parseFilter(nil, false)
	body := p.parseStatements(nameRules("endset"), true)
	return &ast.AssignBlock{Pos: ast.At(line), Target: target, Filter: filter, Body: body}
}

func (p *parser) parseWith() *ast.With {
	node := &ast.With{Pos: ast.At(p.next().Line)}
	for p.current().Kind != lexer.BlockEnd {
		if len(node.Targets) > 0 {
			p.expect(kindRule(lexer.Comma))
		}
		target := p.parseAssignTarget(assignOpts{})
		node.Targets = append(node.Targets, target)
		p.expect(kindRule(lexer.Assign))
		node.Values = append(node.Values, p.parseExpression(true))
	}
	node.Body = p.parseStatements(nameRules("endwith"), true)
	return node
}

func (p *parser) parseAutoescape() *ast.AutoescapeBlock {
	node := &ast.AutoescapeBlock{Pos: ast.At(p.next().Line)}
	node.Value = p.parseExpression(true)
	node.Body = p.parseStatements(nameRules("endautoescape"), true)
	return node
}

func (p *parser) parseBlock() *ast.Block {
	node := &ast.Block{Pos: ast.At(p.next().Line)}
	node.Name = p.expect(kindRule(lexer.Name)).Value
	node.Scoped = p.skipIf(nameRule("scoped"))
	node.Required = p.skipIf(nameRule("required"))

	// Django allows hyphens in block names and Jinja does not; say so
	// rather than reporting a bare unexpected '-'.
	if p.current().Kind == lexer.Sub {
		p.fail("Block names in Jinja have to be valid Python identifiers and may not" +
			" contain hyphens, use an underscore instead.")
	}

	node.Body = p.parseStatements(nameRules("endblock"), true)

	if node.Required {
		for _, stmt := range node.Body {
			out, ok := stmt.(*ast.Output)
			if !ok || !allWhitespaceData(out.Nodes) {
				p.fail("Required blocks can only contain comments or whitespace")
			}
		}
	}
	// `{% endblock name %}` may repeat the block's name.
	p.skipIf(nameRule(node.Name))
	return node
}

func allWhitespaceData(nodes []ast.Expr) bool {
	for _, n := range nodes {
		data, ok := n.(*ast.TemplateData)
		if !ok || strings.TrimSpace(data.Data) != "" {
			return false
		}
	}
	return true
}

func (p *parser) parseExtends() *ast.Extends {
	line := p.next().Line
	return &ast.Extends{Pos: ast.At(line), Template: p.parseExpression(true)}
}

// parseImportContext reads the trailing `with context` / `without context`.
func (p *parser) parseImportContext(def bool) bool {
	if p.testAny(nameRule("with"), nameRule("without")) &&
		p.look().Kind == lexer.Name && p.look().Value == "context" {
		withContext := p.next().Value == "with"
		p.next()
		return withContext
	}
	return def
}

func (p *parser) parseInclude() *ast.Include {
	node := &ast.Include{Pos: ast.At(p.next().Line)}
	node.Template = p.parseExpression(true)
	if p.test(nameRule("ignore")) && p.look().Kind == lexer.Name && p.look().Value == "missing" {
		node.IgnoreMissing = true
		p.next()
		p.next()
	}
	node.WithContext = p.parseImportContext(true)
	return node
}

func (p *parser) parseImport() *ast.Import {
	node := &ast.Import{Pos: ast.At(p.next().Line)}
	node.Template = p.parseExpression(true)
	p.expect(nameRule("as"))
	node.Target = p.parseAssignTarget(assignOpts{nameOnly: true}).(*ast.Name).Name
	node.WithContext = p.parseImportContext(false)
	return node
}

func (p *parser) parseFrom() *ast.FromImport {
	node := &ast.FromImport{Pos: ast.At(p.next().Line)}
	node.Template = p.parseExpression(true)
	p.expect(nameRule("import"))

	sawContext := false
	parseContext := func() bool {
		v := p.current().Value
		if (v == "with" || v == "without") &&
			p.look().Kind == lexer.Name && p.look().Value == "context" {
			node.WithContext = p.next().Value == "with"
			p.next()
			sawContext = true
			return true
		}
		return false
	}

	for {
		if len(node.Names) > 0 {
			p.expect(kindRule(lexer.Comma))
		}
		if p.current().Kind != lexer.Name {
			p.expect(kindRule(lexer.Name))
		}
		if parseContext() {
			break
		}
		target := p.parseAssignTarget(assignOpts{nameOnly: true}).(*ast.Name)
		if strings.HasPrefix(target.Name, "_") {
			p.failKindAt(errs.TemplateAssertionError, target.Line(),
				"names starting with an underline can not be imported")
		}
		entry := ast.ImportName{Name: target.Name, Alias: target.Name}
		if p.skipIf(nameRule("as")) {
			entry.Alias = p.parseAssignTarget(assignOpts{nameOnly: true}).(*ast.Name).Name
		}
		node.Names = append(node.Names, entry)
		if parseContext() || p.current().Kind != lexer.Comma {
			break
		}
	}
	if !sawContext {
		node.WithContext = false
	}
	return node
}

// parseSignature reads a macro or call-block parameter list.
//
// Two things are refused here that Python refuses of a function signature, and
// they are refused in different places there. jinja2's own parser catches a
// non-default after a default and raises TemplateSyntaxError; a *duplicate*
// parameter it does not look at, so the duplicate reaches the Python compiler
// when jinja2 compiles the macro to a function, and comes out as a Python
// SyntaxError naming the identifier jinja2 generated -- `l_1_a` for a parameter
// the template called `a`.
//
// That generated name cannot be reproduced here and there is nothing to gain by
// trying: scope.md excludes the artefacts of compiling to Python. What matters
// is that the template is refused rather than quietly compiled with the later
// parameter winning, because a macro written here and ported to jinja2 would
// fail there. So the refusal is gojja2's own, in CPython's wording with the
// template's own name for the parameter.
func (p *parser) parseSignature() (args []*ast.Name, defaults []ast.Expr) {
	p.expect(kindRule(lexer.LParen))
	seen := map[string]bool{}
	for p.current().Kind != lexer.RParen {
		if len(args) > 0 {
			p.expect(kindRule(lexer.Comma))
		}
		arg := p.parseAssignTarget(assignOpts{nameOnly: true}).(*ast.Name)
		if seen[arg.Name] {
			p.failAt(arg.Line(), "duplicate argument %s in function definition",
				pyQuote(arg.Name))
		}
		seen[arg.Name] = true
		if p.skipIf(kindRule(lexer.Assign)) {
			defaults = append(defaults, p.parseExpression(true))
		} else if len(defaults) > 0 {
			p.fail("non-default argument follows default argument")
		}
		args = append(args, arg)
	}
	p.expect(kindRule(lexer.RParen))
	return args, defaults
}

func (p *parser) parseCallBlock() *ast.CallBlock {
	node := &ast.CallBlock{Pos: ast.At(p.next().Line)}
	if p.current().Kind == lexer.LParen {
		node.Args, node.Defaults = p.parseSignature()
	}
	call, ok := p.parseExpression(true).(*ast.Call)
	if !ok {
		p.failAt(node.Line(), "expected call")
	}
	node.Call = call
	node.Body = p.parseStatements(nameRules("endcall"), true)
	return node
}

func (p *parser) parseFilterBlock() *ast.FilterBlock {
	node := &ast.FilterBlock{Pos: ast.At(p.next().Line)}
	node.Filter = p.parseFilter(nil, true)
	node.Body = p.parseStatements(nameRules("endfilter"), true)
	return node
}

func (p *parser) parseMacro() *ast.Macro {
	node := &ast.Macro{Pos: ast.At(p.next().Line)}
	node.Name = p.parseAssignTarget(assignOpts{nameOnly: true}).(*ast.Name).Name
	node.Args, node.Defaults = p.parseSignature()
	node.Body = p.parseStatements(nameRules("endmacro"), true)
	return node
}

func (p *parser) parsePrint() *ast.Output {
	node := &ast.Output{Pos: ast.At(p.next().Line)}
	for p.current().Kind != lexer.BlockEnd {
		if len(node.Nodes) > 0 {
			p.expect(kindRule(lexer.Comma))
		}
		node.Nodes = append(node.Nodes, p.parseExpression(true))
	}
	return node
}

func (p *parser) parseDo() *ast.ExprStmt {
	line := p.next().Line
	return &ast.ExprStmt{Pos: ast.At(line), Node: p.parseTuple(tupleOpts{})}
}

func (p *parser) parseBreak() *ast.Break {
	return &ast.Break{Pos: ast.At(p.next().Line)}
}

func (p *parser) parseContinue() *ast.Continue {
	return &ast.Continue{Pos: ast.At(p.next().Line)}
}

// --- failure messages --------------------------------------------------------

func (p *parser) failUnknownTag(name string, line int) {
	p.failUnexpected(&name, p.endTokenStack, line)
}

func (p *parser) failEOF(end []rule) {
	stack := append(append([][]rule(nil), p.endTokenStack...), end)
	p.failUnexpected(nil, stack, p.current().Line)
}

// failUnexpected builds jinja2's guidance for an unknown tag or a premature
// end of template: what was expected here, whether the tag would have been
// valid somewhere in the enclosing nesting, and which block is still open.
func (p *parser) failUnexpected(name *string, stack [][]rule, line int) {
	expected := map[string]bool{}
	for _, rules := range stack {
		for _, r := range rules {
			expected[r.describe()] = true
		}
	}

	var lookingFor string
	if len(stack) > 0 {
		parts := make([]string, 0, len(stack[len(stack)-1]))
		for _, r := range stack[len(stack)-1] {
			parts = append(parts, quote(r.describe()))
		}
		lookingFor = strings.Join(parts, " or ")
	}

	var message []string
	if name == nil {
		message = append(message, "Unexpected end of template.")
	} else {
		message = append(message, fmt.Sprintf("Encountered unknown tag %s.", quote(*name)))
	}

	if lookingFor != "" {
		if name != nil && expected[*name] {
			message = append(message,
				"You probably made a nesting mistake. Jinja is expecting this tag,"+
					" but currently looking for "+lookingFor+".")
		} else {
			message = append(message,
				"Jinja was looking for the following tags: "+lookingFor+".")
		}
	}
	if len(p.tagStack) > 0 {
		message = append(message, fmt.Sprintf(
			"The innermost block that needs to be closed is %s.",
			quote(p.tagStack[len(p.tagStack)-1])))
	}
	p.failAt(line, "%s", strings.Join(message, " "))
}
