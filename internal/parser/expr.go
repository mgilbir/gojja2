// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package parser

import (
	"github.com/mgilbir/gojja2/internal/ast"
	"github.com/mgilbir/gojja2/internal/lexer"
	"github.com/mgilbir/gojja2/value"
)

// Precedence, weakest binding first:
//
//	a if b else c        conditional
//	or
//	and
//	not
//	== != < <= > >= in   comparison chains
//	+ -
//	~                    concatenation
//	* / // %
//	**
//	-x +x                unary
//	.x  x[i]  x(...)     postfix
//	|filter  is test     filters and tests
//
// `~` binding tighter than `+` but looser than `*` is jinja2's own choice, not
// Python's, and is one of the places a hand-rolled precedence table usually
// goes wrong.

type assignOpts struct {
	// nameOnly restricts the target to a bare identifier.
	nameOnly bool
	// noTuple parses a single primary rather than a tuple.
	noTuple bool
	// withNamespace allows `ns.attr` as a target.
	withNamespace bool
	// extraEnd marks tokens that terminate an undelimited tuple, such as
	// the `in` of a for loop.
	extraEnd []rule
}

// parseAssignTarget parses the left-hand side of an assignment.
//
// Targets are deliberately parsed with parsePrimary rather than the full
// expression grammar, so `{% set a.b = 1 %}` fails at the dot rather than
// silently parsing a getattr that could never be assigned.
func (p *parser) parseAssignTarget(opts assignOpts) ast.Expr {
	var target ast.Expr
	if opts.nameOnly {
		tok := p.expect(kindRule(lexer.Name))
		target = &ast.Name{Pos: ast.At(tok.Line), Name: tok.Value, Store: true}
	} else {
		if opts.noTuple {
			target = p.parsePrimary(opts.withNamespace)
		} else {
			target = p.parseTuple(tupleOpts{
				simplified:    true,
				extraEnd:      opts.extraEnd,
				withNamespace: opts.withNamespace,
			})
		}
		ast.SetStore(target)
	}

	if !ast.CanAssign(target) {
		p.failAt(target.Line(), "can't assign to %s", quote(target.TypeName()))
	}
	return target
}

func (p *parser) parseExpression(withCondExpr bool) ast.Expr {
	if withCondExpr {
		return p.parseCondExpr()
	}
	return p.parseOr()
}

func (p *parser) parseCondExpr() ast.Expr {
	line := p.current().Line
	expr := p.parseOr()
	for p.skipIf(nameRule("if")) {
		test := p.parseOr()
		var orElse ast.Expr
		if p.skipIf(nameRule("else")) {
			orElse = p.parseCondExpr()
		}
		expr = &ast.CondExpr{Pos: ast.At(line), Test: test, True: expr, False: orElse}
		line = p.current().Line
	}
	return expr
}

func (p *parser) parseOr() ast.Expr {
	line := p.current().Line
	left := p.parseAnd()
	for p.skipIf(nameRule("or")) {
		left = &ast.BinOp{Pos: ast.At(line), Op: ast.OpOr, Left: left, Right: p.parseAnd()}
		line = p.current().Line
	}
	return left
}

func (p *parser) parseAnd() ast.Expr {
	line := p.current().Line
	left := p.parseNot()
	for p.skipIf(nameRule("and")) {
		left = &ast.BinOp{Pos: ast.At(line), Op: ast.OpAnd, Left: left, Right: p.parseNot()}
		line = p.current().Line
	}
	return left
}

func (p *parser) parseNot() ast.Expr {
	if p.test(nameRule("not")) {
		line := p.next().Line
		return &ast.UnaryOp{Pos: ast.At(line), Op: ast.OpNot, Node: p.parseNot()}
	}
	return p.parseCompare()
}

// compareOps maps comparison tokens onto the operator names the AST uses.
var compareOps = map[lexer.Kind]string{
	lexer.Eq:   "eq",
	lexer.Ne:   "ne",
	lexer.Lt:   "lt",
	lexer.Lteq: "lteq",
	lexer.Gt:   "gt",
	lexer.Gteq: "gteq",
}

func (p *parser) parseCompare() ast.Expr {
	line := p.current().Line
	expr := p.parseMath1()
	var ops []*ast.Operand

	for {
		opLine := p.current().Line
		switch {
		case compareOps[p.current().Kind] != "":
			op := compareOps[p.next().Kind]
			ops = append(ops, &ast.Operand{Pos: ast.At(opLine), Op: op, Expr: p.parseMath1()})
		case p.skipIf(nameRule("in")):
			ops = append(ops, &ast.Operand{Pos: ast.At(opLine), Op: "in", Expr: p.parseMath1()})
		case p.test(nameRule("not")) && p.look().Kind == lexer.Name && p.look().Value == "in":
			p.next()
			p.next()
			ops = append(ops, &ast.Operand{Pos: ast.At(opLine), Op: "notin", Expr: p.parseMath1()})
		default:
			if len(ops) == 0 {
				return expr
			}
			return &ast.Compare{Pos: ast.At(line), Expr: expr, Ops: ops}
		}
		line = p.current().Line
	}
}

// math1Ops and math2Ops split the arithmetic operators either side of `~`.
var (
	math1Ops = map[lexer.Kind]ast.BinOpKind{lexer.Add: ast.OpAdd, lexer.Sub: ast.OpSub}
	math2Ops = map[lexer.Kind]ast.BinOpKind{
		lexer.Mul:      ast.OpMul,
		lexer.Div:      ast.OpDiv,
		lexer.FloorDiv: ast.OpFloorDiv,
		lexer.Mod:      ast.OpMod,
	}
)

func (p *parser) parseMath1() ast.Expr {
	line := p.current().Line
	left := p.parseConcat()
	for {
		op, ok := math1Ops[p.current().Kind]
		if !ok {
			return left
		}
		p.next()
		left = &ast.BinOp{Pos: ast.At(line), Op: op, Left: left, Right: p.parseConcat()}
		line = p.current().Line
	}
}

func (p *parser) parseConcat() ast.Expr {
	line := p.current().Line
	nodes := []ast.Expr{p.parseMath2()}
	for p.current().Kind == lexer.Tilde {
		p.next()
		nodes = append(nodes, p.parseMath2())
	}
	if len(nodes) == 1 {
		return nodes[0]
	}
	return &ast.Concat{Pos: ast.At(line), Nodes: nodes}
}

func (p *parser) parseMath2() ast.Expr {
	line := p.current().Line
	left := p.parsePow()
	for {
		op, ok := math2Ops[p.current().Kind]
		if !ok {
			return left
		}
		p.next()
		left = &ast.BinOp{Pos: ast.At(line), Op: op, Left: left, Right: p.parsePow()}
		line = p.current().Line
	}
}

// parsePow builds `**` chains left-associatively.
//
// This is not a mistake and not Python: `{{ 2 ** 3 ** 2 }}` is 64 in jinja2,
// where the same expression is 512 in Python. jinja2's parser folds into the
// left operand as it loops, and matching that matters more than matching the
// language it is named after.
func (p *parser) parsePow() ast.Expr {
	line := p.current().Line
	left := p.parseUnary(true)
	for p.current().Kind == lexer.Pow {
		p.next()
		left = &ast.BinOp{Pos: ast.At(line), Op: ast.OpPow, Left: left, Right: p.parseUnary(true)}
		line = p.current().Line
	}
	return left
}

// parseUnary handles the prefix signs. withFilter is false inside a sign, so
// `-x|abs` applies the filter to the negated value rather than to x.
func (p *parser) parseUnary(withFilter bool) ast.Expr {
	line := p.current().Line
	var node ast.Expr

	switch p.current().Kind {
	case lexer.Sub:
		p.next()
		node = &ast.UnaryOp{Pos: ast.At(line), Op: ast.OpNeg, Node: p.parseUnary(false)}
	case lexer.Add:
		p.next()
		node = &ast.UnaryOp{Pos: ast.At(line), Op: ast.OpPos, Node: p.parseUnary(false)}
	default:
		node = p.parsePrimary(false)
	}

	node = p.parsePostfix(node)
	if withFilter {
		node = p.parseFilterExpr(node)
	}
	return node
}

func (p *parser) parsePrimary(withNamespace bool) ast.Expr {
	tok := p.current()
	switch tok.Kind {
	case lexer.Name:
		p.next()
		switch tok.Value {
		case "true", "True":
			return &ast.Const{Pos: ast.At(tok.Line), Value: value.True}
		case "false", "False":
			return &ast.Const{Pos: ast.At(tok.Line), Value: value.False}
		case "none", "None":
			return &ast.Const{Pos: ast.At(tok.Line), Value: value.None}
		}
		if withNamespace && p.current().Kind == lexer.Dot {
			p.next()
			attr := p.expect(kindRule(lexer.Name))
			return &ast.NSRef{Pos: ast.At(tok.Line), Name: tok.Value, Attr: attr.Value}
		}
		return &ast.Name{Pos: ast.At(tok.Line), Name: tok.Value}

	case lexer.String:
		// Adjacent string literals concatenate, as they do in Python.
		p.next()
		text := tok.Value
		for p.current().Kind == lexer.String {
			text += p.next().Value
		}
		return &ast.Const{Pos: ast.At(tok.Line), Value: value.String(text)}

	case lexer.Integer:
		p.next()
		v, err := lexer.ParseInteger(tok.Value)
		if err != nil {
			p.failAt(tok.Line, "%s", err.Error())
		}
		return &ast.Const{Pos: ast.At(tok.Line), Value: v}

	case lexer.Float:
		p.next()
		v, err := lexer.ParseFloat(tok.Value)
		if err != nil {
			p.failAt(tok.Line, "%s", err.Error())
		}
		return &ast.Const{Pos: ast.At(tok.Line), Value: v}

	case lexer.LParen:
		p.next()
		node := p.parseTuple(tupleOpts{explicitParens: true})
		p.expect(kindRule(lexer.RParen))
		return node

	case lexer.LBracket:
		return p.parseList()

	case lexer.LBrace:
		return p.parseDict()
	}

	p.failAt(tok.Line, "unexpected %s", quote(tok.Describe()))
	return nil // unreachable: failAt panics
}

type tupleOpts struct {
	// simplified restricts elements to primaries, for assignment targets.
	simplified bool
	// noCondExpr forbids `a if b else c` at the top level, so that the
	// `if` of a for loop's filter is not swallowed.
	noCondExpr bool
	// extraEnd lists tokens that close an undelimited tuple.
	extraEnd []rule
	// explicitParens records that parentheses opened this tuple, which is
	// the only context where an empty tuple is legal.
	explicitParens bool
	withNamespace  bool
}

// parseTuple parses a comma-separated group, collapsing to the single element
// when there is no comma. A tuple has no delimiters of its own, so the caller
// has to say what ends it.
func (p *parser) parseTuple(opts tupleOpts) ast.Expr {
	line := p.current().Line
	parseItem := func() ast.Expr {
		if opts.simplified {
			return p.parsePrimary(opts.withNamespace)
		}
		return p.parseExpression(!opts.noCondExpr)
	}

	var items []ast.Expr
	isTuple := false
	for {
		if len(items) > 0 {
			p.expect(kindRule(lexer.Comma))
		}
		if p.isTupleEnd(opts.extraEnd) {
			break
		}
		items = append(items, parseItem())
		if p.current().Kind != lexer.Comma {
			break
		}
		isTuple = true
		line = p.current().Line
	}

	if !isTuple {
		if len(items) > 0 {
			return items[0]
		}
		// Nothing at all is only a tuple when parentheses said so.
		if !opts.explicitParens {
			p.fail("Expected an expression, got %s", quote(p.current().Describe()))
		}
	}
	return &ast.Tuple{Pos: ast.At(line), Items: items}
}

func (p *parser) isTupleEnd(extraEnd []rule) bool {
	switch p.current().Kind {
	case lexer.VariableEnd, lexer.BlockEnd, lexer.RParen:
		return true
	}
	return p.testAny(extraEnd...)
}

func (p *parser) parseList() *ast.List {
	tok := p.expect(kindRule(lexer.LBracket))
	var items []ast.Expr
	for p.current().Kind != lexer.RBracket {
		if len(items) > 0 {
			p.expect(kindRule(lexer.Comma))
		}
		// The check repeats so that a trailing comma is allowed.
		if p.current().Kind == lexer.RBracket {
			break
		}
		items = append(items, p.parseExpression(true))
	}
	p.expect(kindRule(lexer.RBracket))
	return &ast.List{Pos: ast.At(tok.Line), Items: items}
}

func (p *parser) parseDict() *ast.Dict {
	tok := p.expect(kindRule(lexer.LBrace))
	var items []*ast.Pair
	for p.current().Kind != lexer.RBrace {
		if len(items) > 0 {
			p.expect(kindRule(lexer.Comma))
		}
		if p.current().Kind == lexer.RBrace {
			break
		}
		key := p.parseExpression(true)
		p.expect(kindRule(lexer.Colon))
		val := p.parseExpression(true)
		items = append(items, &ast.Pair{Pos: ast.At(key.Line()), Key: key, Value: val})
	}
	p.expect(kindRule(lexer.RBrace))
	return &ast.Dict{Pos: ast.At(tok.Line), Items: items}
}

// parsePostfix consumes attribute access, subscripts and calls.
func (p *parser) parsePostfix(node ast.Expr) ast.Expr {
	for {
		switch p.current().Kind {
		case lexer.Dot, lexer.LBracket:
			node = p.parseSubscript(node)
		case lexer.LParen:
			node = p.parseCall(node)
		default:
			return node
		}
	}
}

// parseFilterExpr consumes the filter and test suffixes, which bind looser
// than postfix but tighter than arithmetic.
func (p *parser) parseFilterExpr(node ast.Expr) ast.Expr {
	for {
		switch {
		case p.current().Kind == lexer.Pipe:
			node = p.parseFilter(node, false)
		case p.test(nameRule("is")):
			node = p.parseTest(node)
		case p.current().Kind == lexer.LParen:
			// A filter or test may itself be called.
			node = p.parseCall(node)
		default:
			return node
		}
	}
}

func (p *parser) parseSubscript(node ast.Expr) ast.Expr {
	tok := p.next()

	if tok.Kind == lexer.Dot {
		attr := p.current()
		p.next()
		switch attr.Kind {
		case lexer.Name:
			return &ast.Getattr{Pos: ast.At(tok.Line), Node: node, Attr: attr.Value}
		case lexer.Integer:
			// `x.0` indexes rather than fetching an attribute.
			v, err := lexer.ParseInteger(attr.Value)
			if err != nil {
				p.failAt(attr.Line, "%s", err.Error())
			}
			return &ast.Getitem{
				Pos:  ast.At(tok.Line),
				Node: node,
				Arg:  &ast.Const{Pos: ast.At(attr.Line), Value: v},
			}
		}
		p.failAt(attr.Line, "expected name or number")
	}

	if tok.Kind == lexer.LBracket {
		var args []ast.Expr
		for p.current().Kind != lexer.RBracket {
			if len(args) > 0 {
				p.expect(kindRule(lexer.Comma))
			}
			args = append(args, p.parseSubscribed())
		}
		p.expect(kindRule(lexer.RBracket))
		arg := args[0]
		if len(args) != 1 {
			arg = &ast.Tuple{Pos: ast.At(tok.Line), Items: args}
		}
		return &ast.Getitem{Pos: ast.At(tok.Line), Node: node, Arg: arg}
	}

	p.failAt(tok.Line, "expected subscript expression")
	return nil
}

// parseSubscribed parses one subscript element, which may be a slice. An
// omitted bound stays nil, which is not the same as an explicit None.
func (p *parser) parseSubscribed() ast.Expr {
	line := p.current().Line
	var start, stop, step ast.Expr

	if p.current().Kind == lexer.Colon {
		p.next()
	} else {
		node := p.parseExpression(true)
		if p.current().Kind != lexer.Colon {
			return node // a plain index, not a slice
		}
		p.next()
		start = node
	}

	if p.current().Kind != lexer.Colon && !p.testAny(
		kindRule(lexer.RBracket), kindRule(lexer.Comma)) {
		stop = p.parseExpression(true)
	}
	if p.current().Kind == lexer.Colon {
		p.next()
		if !p.testAny(kindRule(lexer.RBracket), kindRule(lexer.Comma)) {
			step = p.parseExpression(true)
		}
	}
	return &ast.Slice{Pos: ast.At(line), Start: start, Stop: stop, Step: step}
}

// parseCallArgs parses a parenthesised argument list.
func (p *parser) parseCallArgs() ast.Args {
	tok := p.expect(kindRule(lexer.LParen))
	var out ast.Args
	requireComma := false

	ensure := func(ok bool) {
		if !ok {
			p.failAt(tok.Line, "invalid syntax for function call expression")
		}
	}

	for p.current().Kind != lexer.RParen {
		if requireComma {
			p.expect(kindRule(lexer.Comma))
			if p.current().Kind == lexer.RParen {
				break // trailing comma
			}
		}

		switch {
		case p.current().Kind == lexer.Mul:
			ensure(out.DynArgs == nil && out.DynKwargs == nil)
			p.next()
			out.DynArgs = p.parseExpression(true)
		case p.current().Kind == lexer.Pow:
			ensure(out.DynKwargs == nil)
			p.next()
			out.DynKwargs = p.parseExpression(true)
		case p.current().Kind == lexer.Name && p.look().Kind == lexer.Assign:
			ensure(out.DynKwargs == nil)
			key := p.next().Value
			p.next()
			val := p.parseExpression(true)
			out.Kwargs = append(out.Kwargs,
				&ast.Keyword{Pos: ast.At(val.Line()), Key: key, Value: val})
		default:
			ensure(out.DynArgs == nil && out.DynKwargs == nil && len(out.Kwargs) == 0)
			out.Args = append(out.Args, p.parseExpression(true))
		}
		requireComma = true
	}
	p.expect(kindRule(lexer.RParen))
	return out
}

func (p *parser) parseCall(node ast.Expr) *ast.Call {
	line := p.current().Line
	return &ast.Call{Pos: ast.At(line), Node: node, Args: p.parseCallArgs()}
}

// parseFilter consumes a `|name(...)` chain. startInline enters the chain
// without a leading pipe, for `{% filter %}` and block `set`, where the input
// is the captured body rather than an expression.
func (p *parser) parseFilter(node ast.Expr, startInline bool) ast.Expr {
	for p.current().Kind == lexer.Pipe || startInline {
		if !startInline {
			p.next()
		}
		tok := p.expect(kindRule(lexer.Name))
		name := p.parseDottedName(tok.Value)

		var args ast.Args
		if p.current().Kind == lexer.LParen {
			args = p.parseCallArgs()
		}
		node = &ast.Filter{Pos: ast.At(tok.Line), Node: node, Name: name, Args: args}
		startInline = false
	}
	return node
}

// parseDottedName reads the `a.b.c` form filter and test names may take.
func (p *parser) parseDottedName(first string) string {
	name := first
	for p.current().Kind == lexer.Dot {
		p.next()
		name += "." + p.expect(kindRule(lexer.Name)).Value
	}
	return name
}

// testArgStarts are the token kinds that may begin a bare test argument, as in
// `x is divisibleby 3`.
var testArgStarts = map[lexer.Kind]bool{
	lexer.Name: true, lexer.String: true, lexer.Integer: true, lexer.Float: true,
	lexer.LParen: true, lexer.LBracket: true, lexer.LBrace: true,
}

func (p *parser) parseTest(node ast.Expr) ast.Expr {
	tok := p.next() // the `is`
	negated := p.skipIf(nameRule("not"))

	name := p.parseDottedName(p.expect(kindRule(lexer.Name)).Value)

	var args ast.Args
	switch {
	case p.current().Kind == lexer.LParen:
		args = p.parseCallArgs()
	case testArgStarts[p.current().Kind] &&
		!p.testAny(nameRule("else"), nameRule("or"), nameRule("and")):
		if p.test(nameRule("is")) {
			p.fail("You cannot chain multiple tests with is")
		}
		// A bare argument stops at postfix, so `x is sameas y.z` reads
		// the attribute but `x is sameas y|f` does not take the filter.
		arg := p.parsePostfix(p.parsePrimary(false))
		args.Args = []ast.Expr{arg}
	}

	var result ast.Expr = &ast.Test{Pos: ast.At(tok.Line), Node: node, Name: name, Args: args}
	if negated {
		result = &ast.UnaryOp{Pos: ast.At(tok.Line), Op: ast.OpNot, Node: result}
	}
	return result
}
