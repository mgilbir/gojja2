// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"github.com/mgilbir/gojja2/internal/ast"
	"github.com/mgilbir/gojja2/value"
)

// foldConstantPrints replaces a printed expression that is entirely literal,
// and that resolves to nothing, with the empty output it would produce.
//
// jinja2's code generator tries `str(child.as_const())` for every expression
// in a print tag and emits the result as literal text. as_const resolves a
// subscript through Environment.getitem, which swallows TypeError and
// LookupError and hands back Undefined -- whose str() is "". So a lookup that
// would raise at run time renders as nothing when it is written entirely in
// literals:
//
//	{{ 0[1:] }}          -> ""              folded, because it is all constant
//	{{ 0[1:] * -2 }}     -> TypeError       the product is not foldable, so
//	                                        the subscript runs for real
//	{% if 0[1:] %}       -> TypeError       only print tags take this path
//
// The scope is exactly that: print tags, whole-expression constants, and only
// when the answer is undefined. Folding a *successful* lookup would be
// behaviour-neutral but would share one container between renders, so it is
// left alone.
func foldConstantPrints(c *constEvaluator, body []ast.Stmt) {
	if c.env.finalize != nil {
		// finalize runs over every printed value; folding would have to
		// run it at compile time, and it may not be pure.
		return
	}
	walkOutputs(body, func(out *ast.Output, insideAutoescape bool) {
		if insideAutoescape {
			// Inside {% autoescape %} the escaping in force is not
			// known until the block runs, so the text this would
			// bake in could be wrong.
			return
		}
		for i, node := range out.Nodes {
			if _, isData := node.(*ast.TemplateData); isData {
				continue
			}
			v, ok := c.constEval(node)
			if !ok {
				continue
			}
			// StrictUndefined raises when printed, so a constant
			// that resolves to one has to stay a run-time failure.
			if v.IsUndefined() && v.UndefinedBehavior() == value.UndefinedStrict {
				continue
			}
			text := value.Str(v)
			if c.st.autoescape && !v.IsSafe() {
				text = escapeHTML(text)
			}
			// The result becomes literal template text, exactly as
			// jinja2 appends str(as_const()) to its output buffer,
			// so nothing escapes it a second time.
			out.Nodes[i] = &ast.TemplateData{Pos: ast.At(node.Line()), Data: text}
		}
	})
}

// foldConstantExpressions replaces any expression that evaluates to an
// immutable constant with that constant.
//
// This is the optimizer's own fold, which runs everywhere rather than only in
// print tags, and it is observable for a reason that has nothing to do with
// speed: folding an expression evaluates it at compile time through the
// swallowing lookup, so an error the unfolded form would have raised never
// happens. `{% if {1: 'a'}[1:] or 0.1 %}` folds to `0.1` and succeeds, while
// the same subscript outside a foldable expression raises.
//
// jinja2 folds containers too. Only immutable results are folded here, because
// sharing one list between every render of a template is a bug rather than a
// behaviour -- and it is not one any template can observe through folding
// alone.
func foldConstantExpressions(c *constEvaluator, body []ast.Stmt) {
	f := &constFolder{c: c}
	f.stmts(body)
}

type constFolder struct{ c *constEvaluator }

// foldable reports whether a constant result can stand in for the expression
// that produced it.
//
// Containers qualify, as they do in jinja2, because evaluating a folded
// constant copies it -- so nothing is shared between renders. Undefined does
// not: jinja2's optimizer refuses it, and the difference is observable in
// which errors survive.
func foldable(v value.Value) bool {
	switch v.Kind() {
	case value.KindNone, value.KindBool, value.KindInt, value.KindFloat,
		value.KindString, value.KindBytes,
		value.KindList, value.KindTuple, value.KindDict:
		return true
	}
	return false
}

func (f *constFolder) fold(e ast.Expr) ast.Expr {
	if e == nil {
		return nil
	}
	if _, isConst := e.(*ast.Const); !isConst {
		if v, ok := f.c.constEval(e); ok && foldable(v) {
			return &ast.Const{Pos: ast.At(e.Line()), Value: v}
		}
	}
	f.descend(e)
	return e
}

func (f *constFolder) exprs(items []ast.Expr) {
	for i := range items {
		items[i] = f.fold(items[i])
	}
}

func (f *constFolder) args(a *ast.Args) {
	f.exprs(a.Args)
	for _, kw := range a.Kwargs {
		kw.Value = f.fold(kw.Value)
	}
	a.DynArgs = f.fold(a.DynArgs)
	a.DynKwargs = f.fold(a.DynKwargs)
}

func (f *constFolder) descend(e ast.Expr) {
	switch n := e.(type) {
	case *ast.Tuple:
		f.exprs(n.Items)
	case *ast.List:
		f.exprs(n.Items)
	case *ast.Dict:
		for _, p := range n.Items {
			p.Key, p.Value = f.fold(p.Key), f.fold(p.Value)
		}
	case *ast.CondExpr:
		n.Test, n.True, n.False = f.fold(n.Test), f.fold(n.True), f.fold(n.False)
	case *ast.BinOp:
		n.Left, n.Right = f.fold(n.Left), f.fold(n.Right)
	case *ast.UnaryOp:
		n.Node = f.fold(n.Node)
	case *ast.Concat:
		f.exprs(n.Nodes)
	case *ast.Compare:
		n.Expr = f.fold(n.Expr)
		for _, op := range n.Ops {
			op.Expr = f.fold(op.Expr)
		}
	case *ast.Getattr:
		n.Node = f.fold(n.Node)
	case *ast.Getitem:
		n.Node = f.fold(n.Node)
		if _, isSlice := n.Arg.(*ast.Slice); !isSlice {
			n.Arg = f.fold(n.Arg)
		}
	case *ast.Call:
		n.Node = f.fold(n.Node)
		f.args(&n.Args)
	case *ast.Filter:
		n.Node = f.fold(n.Node)
		f.args(&n.Args)
	case *ast.Test:
		n.Node = f.fold(n.Node)
		f.args(&n.Args)
	}
}

func (f *constFolder) stmts(body []ast.Stmt) {
	for _, stmt := range body {
		f.stmt(stmt)
	}
}

func (f *constFolder) stmt(stmt ast.Stmt) {
	switch n := stmt.(type) {
	case *ast.Output:
		for i, node := range n.Nodes {
			if _, isData := node.(*ast.TemplateData); isData {
				continue
			}
			n.Nodes[i] = f.fold(node)
		}
	case *ast.For:
		n.Iter, n.Test = f.fold(n.Iter), f.fold(n.Test)
		f.stmts(n.Body)
		f.stmts(n.Else)
	case *ast.If:
		n.Test = f.fold(n.Test)
		f.stmts(n.Body)
		for _, elif := range n.Elif {
			f.stmt(elif)
		}
		f.stmts(n.Else)
	case *ast.Assign:
		n.Node = f.fold(n.Node)
	case *ast.AssignBlock:
		n.Filter = f.fold(n.Filter)
		f.stmts(n.Body)
	case *ast.With:
		f.exprs(n.Values)
		f.stmts(n.Body)
	case *ast.Macro:
		f.exprs(n.Defaults)
		f.stmts(n.Body)
	case *ast.CallBlock:
		f.exprs(n.Defaults)
		f.stmts(n.Body)
	case *ast.FilterBlock:
		n.Filter = f.fold(n.Filter)
		f.stmts(n.Body)
	case *ast.Block:
		f.stmts(n.Body)
	case *ast.ExprStmt:
		n.Node = f.fold(n.Node)
	case *ast.Include:
		n.Template = f.fold(n.Template)
	case *ast.Import:
		n.Template = f.fold(n.Template)
	case *ast.FromImport:
		n.Template = f.fold(n.Template)
	case *ast.Extends:
		n.Template = f.fold(n.Template)
	case *ast.Scope:
		f.stmts(n.Body)
	case *ast.AutoescapeBlock:
		n.Value = f.fold(n.Value)
		f.stmts(n.Body)
	}
}

// constEval evaluates the literal subset of the expression grammar: literals,
// containers of literals, and attribute or item access over them. Anything
// else -- an operator, a filter, a call, a name -- reports false, which is
// what keeps the fold as narrow as jinja2's.
func (c *constEvaluator) constEval(e ast.Expr) (value.Value, bool) {
	switch n := e.(type) {
	case *ast.Const:
		return n.Value, true

	case *ast.List:
		items, ok := c.constEvalAll(n.Items)
		if !ok {
			return value.Undefined, false
		}
		return value.NewList(items...), true

	case *ast.Tuple:
		items, ok := c.constEvalAll(n.Items)
		if !ok {
			return value.Undefined, false
		}
		return value.NewTuple(items...), true

	case *ast.Dict:
		out := value.NewDict()
		d, _ := out.Dict()
		for _, pair := range n.Items {
			k, ok := c.constEval(pair.Key)
			if !ok {
				return value.Undefined, false
			}
			v, ok := c.constEval(pair.Value)
			if !ok {
				return value.Undefined, false
			}
			if err := d.Set(k, v); err != nil {
				return value.Undefined, false
			}
		}
		return out, true

	case *ast.Getattr:
		base, ok := c.constEval(n.Node)
		// Environment.getattr swallows a missing attribute, but not an
		// Undefined receiver: that raises, so the fold is abandoned and
		// the lookup happens for real at run time.
		if !ok || base.IsUndefined() {
			return value.Undefined, false
		}
		return constGetAttr(base, n.Attr), true

	case *ast.Getitem:
		base, ok := c.constEval(n.Node)
		if !ok || base.IsUndefined() {
			return value.Undefined, false
		}
		if slice, isSlice := n.Arg.(*ast.Slice); isSlice {
			return c.constGetSlice(base, slice)
		}
		key, ok := c.constEval(n.Arg)
		if !ok {
			return value.Undefined, false
		}
		return constGetItem(base, key), true

	case *ast.BinOp:
		return c.constBinOp(n)

	case *ast.UnaryOp:
		return c.constUnaryOp(n)

	case *ast.Concat:
		items, ok := c.constEvalAll(n.Nodes)
		if !ok {
			return value.Undefined, false
		}
		out := value.String("")
		for _, item := range items {
			out = value.Concat(out, item)
		}
		return out, true

	case *ast.Compare:
		return c.constCompare(n)

	case *ast.Filter:
		return c.constFilter(n)

	case *ast.Test:
		return c.constTest(n)

	case *ast.CondExpr:
		test, ok := c.constEval(n.Test)
		if !ok {
			return value.Undefined, false
		}
		truth, err := value.IsTrue(test)
		if err != nil {
			return value.Undefined, false
		}
		if truth {
			return c.constEval(n.True)
		}
		if n.False == nil {
			return value.Undefined, false
		}
		return c.constEval(n.False)
	}
	return value.Undefined, false
}

// constBinOp folds an operator over constants. An operation that raises is not
// constant, which is how `{{ 0[1:] * -2 }}` keeps its runtime TypeError while
// `{{ 0[1:] }}` renders nothing.
func (c *constEvaluator) constBinOp(n *ast.BinOp) (value.Value, bool) {
	// and/or short-circuit, so the unused side need not be constant.
	if n.Op == ast.OpAnd || n.Op == ast.OpOr {
		left, ok := c.constEval(n.Left)
		if !ok {
			return value.Undefined, false
		}
		truth, err := value.IsTrue(left)
		if err != nil {
			return value.Undefined, false
		}
		if truth == (n.Op == ast.OpAnd) {
			return c.constEval(n.Right)
		}
		return left, true
	}

	left, ok := c.constEval(n.Left)
	if !ok {
		return value.Undefined, false
	}
	right, ok := c.constEval(n.Right)
	if !ok {
		return value.Undefined, false
	}

	var out value.Value
	var err error
	switch n.Op {
	case ast.OpAdd:
		out, err = value.Add(left, right)
	case ast.OpSub:
		out, err = value.Sub(left, right)
	case ast.OpMul:
		// Checked before the multiplication, not after: the point is to
		// not make the allocation at all.
		if size, _, isRepeat := value.RepeatSize(left, right); isRepeat && size > maxFoldedConst {
			return value.Undefined, false
		}
		out, err = value.Mul(left, right)
	case ast.OpDiv:
		out, err = value.Div(left, right)
	case ast.OpFloorDiv:
		out, err = value.FloorDiv(left, right)
	case ast.OpMod:
		out, err = value.Mod(left, right)
	case ast.OpPow:
		out, err = value.Pow(left, right)
	default:
		return value.Undefined, false
	}
	if err != nil {
		return value.Undefined, false
	}
	if !constSizeOK(out) {
		return value.Undefined, false
	}
	return out, true
}

// maxFoldedConst bounds a constant the optimizer is willing to build.
//
// Folding runs at compile time, where there is no render and so no budget to
// charge: `{{ "x" * 1000000000 }}` allocated a gigabyte before anything asked
// for the template to be rendered, and `{{ "x" * 60000 + "x" * 60000 }}`
// doubles for every level a template nests. Refusing to fold leaves the
// expression for runtime, where the render's budget bounds it -- the result is
// the same, it is just not computed early.
//
// Nothing written on purpose builds a 64 KiB constant this way; the padding
// and separator lines repetition is actually used for are three orders of
// magnitude below it.
const maxFoldedConst = 1 << 16

// constSizeOK reports whether a folded value is small enough to keep.
func constSizeOK(v value.Value) bool {
	switch v.Kind() {
	case value.KindString, value.KindBytes:
		return len(v.AsString()) <= maxFoldedConst
	case value.KindList, value.KindTuple:
		if s, ok := v.Seq(); ok {
			return s.Len() <= maxFoldedConst
		}
	}
	return true
}

func (c *constEvaluator) constUnaryOp(n *ast.UnaryOp) (value.Value, bool) {
	operand, ok := c.constEval(n.Node)
	if !ok {
		return value.Undefined, false
	}
	switch n.Op {
	case ast.OpNot:
		truth, err := value.IsTrue(operand)
		if err != nil {
			return value.Undefined, false
		}
		return value.Bool(!truth), true
	case ast.OpNeg:
		out, err := value.Neg(operand)
		return out, err == nil
	case ast.OpPos:
		out, err := value.Pos(operand)
		return out, err == nil
	}
	return value.Undefined, false
}

func (c *constEvaluator) constCompare(n *ast.Compare) (value.Value, bool) {
	left, ok := c.constEval(n.Expr)
	if !ok {
		return value.Undefined, false
	}
	for _, op := range n.Ops {
		right, ok := c.constEval(op.Expr)
		if !ok {
			return value.Undefined, false
		}
		holds, err := compareStep(op.Op, left, right)
		if err != nil {
			return value.Undefined, false
		}
		if !holds {
			return value.False, true
		}
		left = right
	}
	return value.True, true
}

func (c *constEvaluator) constEvalAll(items []ast.Expr) ([]value.Value, bool) {
	out := make([]value.Value, len(items))
	for i, item := range items {
		v, ok := c.constEval(item)
		if !ok {
			return nil, false
		}
		out[i] = v
	}
	return out, true
}

// constEvaluator folds expressions at compile time, with the environment it
// needs to resolve filters and tests.
type constEvaluator struct {
	env *Environment
	st  *State
}

// newConstEvaluator builds an evaluator for a template being compiled.
func newConstEvaluator(env *Environment, name string, fromString bool) *constEvaluator {
	placeholder := &Template{env: env, name: name, fromString: fromString}
	globals := &scope{vars: env.globals}
	st := &State{
		env:        env,
		tmpl:       placeholder,
		root:       placeholder,
		globals:    globals,
		ctx:        newScope(globals),
		blocks:     map[string][]blockEntry{},
		autoescape: env.escapes(name, fromString),
	}
	return &constEvaluator{env: env, st: st}
}

// evalContextFilters read the autoescape setting, so jinja2 refuses to fold
// them while autoescaping is on -- the compile-time and render-time settings
// can differ once {% autoescape %} is in play.
var evalContextFilters = map[string]bool{
	"replace": true, "join": true, "xmlattr": true, "urlize": true, "tojson": true,
}

// contextFilters take the render context in jinja2 and are therefore never
// folded, whatever their arguments. That is load-bearing: an expression
// containing one stays unfolded, so a failing subscript beside it still raises
// instead of being resolved away at compile time.
var contextFilters = map[string]bool{
	"random": true, "map": true,
	"select": true, "reject": true, "selectattr": true, "rejectattr": true,
}

// constFilter folds `x|name(...)` when every operand is constant.
//
// jinja2 does the same from Expr.as_const, which is why
// `{% set w = 3[1:]|urlencode %}` renders nothing rather than raising: the
// subscript is resolved through the swallowing lookup during folding.
func (c *constEvaluator) constFilter(n *ast.Filter) (value.Value, bool) {
	if n.Node == nil {
		return value.Undefined, false
	}
	if contextFilters[n.Name] {
		return value.Undefined, false
	}
	if c.st.autoescape && evalContextFilters[n.Name] {
		return value.Undefined, false
	}
	fn, ok := c.env.filters[n.Name]
	if !ok {
		return value.Undefined, false
	}
	input, ok := c.constEval(n.Node)
	if !ok {
		return value.Undefined, false
	}
	args, ok := c.constArgs(n.Args)
	if !ok {
		return value.Undefined, false
	}
	out, err := fn(c.st, input, args)
	if err != nil {
		return value.Undefined, false
	}
	return out, true
}

// constTest folds `x is name(...)` when every operand is constant.
func (c *constEvaluator) constTest(n *ast.Test) (value.Value, bool) {
	fn, ok := c.env.tests[n.Name]
	if !ok {
		return value.Undefined, false
	}
	input, ok := c.constEval(n.Node)
	if !ok {
		return value.Undefined, false
	}
	args, ok := c.constArgs(n.Args)
	if !ok {
		return value.Undefined, false
	}
	out, err := fn(c.st, input, args)
	if err != nil {
		return value.Undefined, false
	}
	return value.Bool(out), true
}

// constArgs evaluates a call's arguments, reporting false if any depends on
// the context. The * and ** forms are not folded, as jinja2 does not fold them
// either.
func (c *constEvaluator) constArgs(a ast.Args) (*value.CallArgs, bool) {
	if a.DynArgs != nil || a.DynKwargs != nil {
		return nil, false
	}
	out := &value.CallArgs{}
	for _, arg := range a.Args {
		v, ok := c.constEval(arg)
		if !ok {
			return nil, false
		}
		out.Pos = append(out.Pos, v)
	}
	for _, kw := range a.Kwargs {
		v, ok := c.constEval(kw.Value)
		if !ok {
			return nil, false
		}
		out.Kwargs = append(out.Kwargs, value.Kwarg{Name: kw.Key, Value: v})
	}
	return out, true
}

// constGetAttr mirrors Environment.getattr, which never raises.
//
// Constant folding runs at compile time, so there is no render to charge and
// lookupAttr gets a nil State.
func constGetAttr(base value.Value, name string) value.Value {
	if v, ok := lookupAttr(nil, base, name); ok {
		return v
	}
	if v, ok := lookupItem(base, value.String(name)); ok {
		return v
	}
	return value.UndefinedAttr(base, name)
}

// constGetItem mirrors Environment.getitem, which swallows the failure.
func constGetItem(base, key value.Value) value.Value {
	if v, ok := lookupItem(base, key); ok {
		return v
	}
	if v, ok := constIndex(base, key); ok {
		return v
	}
	if key.Kind() == value.KindString {
		return constGetAttr(base, key.AsString())
	}
	if i, ok := key.Int64(); ok {
		return value.UndefinedIndex(base, int(i))
	}
	return value.UndefinedHint("%s has no element %s",
		value.ObjectTypeRepr(base), value.Repr(key))
}

func constIndex(base, key value.Value) (value.Value, bool) {
	i, ok := key.Int64()
	if !ok {
		return value.Undefined, false
	}
	switch base.Kind() {
	case value.KindList, value.KindTuple:
		s, _ := base.Seq()
		idx := int(i)
		if idx < 0 {
			idx += s.Len()
		}
		if idx < 0 || idx >= s.Len() {
			return value.Undefined, false
		}
		return s.At(idx), true
	case value.KindString:
		s, found := value.StrIndex(base.AsString(), int(i))
		if !found {
			return value.Undefined, false
		}
		// Markup.__getitem__ returns Markup, folded or not.
		if base.IsSafe() {
			return value.Safe(s), true
		}
		return value.String(s), true
	}
	return value.Undefined, false
}

func (c *constEvaluator) constGetSlice(base value.Value, slice *ast.Slice) (value.Value, bool) {
	bound := func(e ast.Expr) (*int, bool, bool) {
		if e == nil {
			return nil, true, true
		}
		v, ok := c.constEval(e)
		if !ok {
			return nil, false, false
		}
		if v.IsNone() {
			return nil, true, true
		}
		i, fits := v.Int64()
		if !fits {
			return nil, false, true
		}
		idx := int(i)
		return &idx, true, true
	}
	start, okStart, constStart := bound(slice.Start)
	stop, okStop, constStop := bound(slice.Stop)
	step, okStep, constStep := bound(slice.Step)
	if !constStart || !constStop || !constStep {
		return value.Undefined, false
	}
	if !okStart || !okStop || !okStep {
		return value.UndefinedHint("invalid slice"), true
	}

	switch base.Kind() {
	case value.KindString:
		out, err := value.StrSlice(base.AsString(), start, stop, step)
		if err != nil {
			return value.UndefinedHint("invalid slice"), true
		}
		if base.IsSafe() {
			return value.Safe(out), true
		}
		return value.String(out), true
	case value.KindList, value.KindTuple:
		s, _ := base.Seq()
		idx, err := value.SliceIndices(s.Len(), start, stop, step)
		if err != nil {
			return value.UndefinedHint("invalid slice"), true
		}
		items := make([]value.Value, len(idx))
		for i, j := range idx {
			items[i] = s.At(j)
		}
		if base.Kind() == value.KindTuple {
			return value.NewTuple(items...), true
		}
		return value.NewList(items...), true
	}
	// Everything else -- an int, a float, a dict -- fails the subscript,
	// and Environment.getitem turns that into undefined.
	return value.UndefinedHint("%s is not subscriptable", value.ObjectTypeRepr(base)), true
}

// walkOutputs visits every print tag in a template body, reporting whether it
// sits inside an {% autoescape %} block.
func walkOutputs(body []ast.Stmt, fn func(*ast.Output, bool)) {
	walkOutputsIn(body, false, fn)
}

func walkOutputsIn(body []ast.Stmt, insideAutoescape bool, fn func(*ast.Output, bool)) {
	for _, stmt := range body {
		switch n := stmt.(type) {
		case *ast.Output:
			fn(n, insideAutoescape)
		case *ast.For:
			walkOutputsIn(n.Body, insideAutoescape, fn)
			walkOutputsIn(n.Else, insideAutoescape, fn)
		case *ast.If:
			walkOutputsIn(n.Body, insideAutoescape, fn)
			for _, elif := range n.Elif {
				walkOutputsIn(elif.Body, insideAutoescape, fn)
			}
			walkOutputsIn(n.Else, insideAutoescape, fn)
		case *ast.AssignBlock:
			walkOutputsIn(n.Body, insideAutoescape, fn)
		case *ast.With:
			walkOutputsIn(n.Body, insideAutoescape, fn)
		case *ast.Macro:
			walkOutputsIn(n.Body, insideAutoescape, fn)
		case *ast.CallBlock:
			walkOutputsIn(n.Body, insideAutoescape, fn)
		case *ast.FilterBlock:
			walkOutputsIn(n.Body, insideAutoescape, fn)
		case *ast.Block:
			walkOutputsIn(n.Body, insideAutoescape, fn)
		case *ast.Scope:
			walkOutputsIn(n.Body, insideAutoescape, fn)
		case *ast.AutoescapeBlock:
			walkOutputsIn(n.Body, true, fn)
		}
	}
}
