// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"errors"
	"strings"

	"github.com/mgilbir/gojja2/errs"
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
	walkOutputs(c, body, func(out *ast.Output, escaping bool) {
		for i, node := range out.Nodes {
			if _, isData := node.(*ast.TemplateData); isData {
				continue
			}
			v, ok := c.tryConstEval(node)
			if !ok {
				continue
			}
			// StrictUndefined raises when printed, so a constant
			// that resolves to one has to stay a run-time failure.
			if v.IsUndefined() && v.UndefinedBehavior() == value.UndefinedStrict {
				continue
			}
			text := value.Str(v)
			if escaping && !v.IsSafe() {
				text = escapeHTML(text)
			}
			// The text becomes part of the compiled template and is
			// kept for as long as it is cached, so it obeys the same
			// cap as any other folded constant. Leaving it for runtime
			// renders the same bytes without retaining them.
			if len(text) > maxFoldedConst {
				continue
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
	f := &constFolder{c: c, envAutoescape: c.st.autoescape}
	f.stmts(body)
}

// constFolder walks a template folding constant expressions, carrying the
// escaping in force where it stands.
//
// jinja2 runs its optimizer from the code generator, once per node, with that
// node's eval context -- so a fold inside {% autoescape %} uses the block's
// setting, and a block whose expression is not constant makes the context
// *volatile*, at which point the optimizer is not run at all. Both matter
// here: five filters read the setting, so folding one under the wrong value
// bakes the wrong text into the template.
type constFolder struct {
	c *constEvaluator
	// envAutoescape is the template's own setting, which a {% block %}
	// body returns to; see stmt.
	envAutoescape bool
}

// foldable reports whether a constant result can stand in for the expression
// that produced it.
//
// This is jinja2's has_safe_repr, and it looks *inside* a container: a list is
// foldable only when every element is, a dict only when every key and value
// are. The types it accepts are exact, so a tuple subclass -- what |groupby
// yields -- is not one of them, and neither is anything else an object.
//
// The recursion is not decoration. A list holding group tuples reads as a
// perfectly ordinary list from the outside, and folding it swallowed a
// TypeError that jinja2 raises:
//
//	{% with w = blank if (-1)[-2:] else d|batch(2)|list|groupby('c')|list %}
//
// jinja2 cannot fold the else branch, so it evaluates the condition at run
// time, where a slice of an int is a plain Python subscript and raises.
// Folding it here answered the else branch and rendered nothing at all.
//
// Undefined is absent for the same reason it is there: jinja2's optimizer
// refuses it, and which errors survive depends on it.
func foldable(v value.Value) bool { return foldableDepth(v, 0) }

// maxFoldableDepth bounds the walk. A constant is built from literals, so its
// depth is the parser's nesting depth, but the bound belongs here rather than
// resting on that.
const maxFoldableDepth = 64

func foldableDepth(v value.Value, depth int) bool {
	if depth > maxFoldableDepth {
		return false
	}
	switch v.Kind() {
	case value.KindNone, value.KindBool, value.KindInt, value.KindFloat,
		value.KindString:
		// A Markup is a str subclass Python names, and it is on the
		// list. Bytes is not: nothing writes a bytes literal in a
		// template, and jinja2 would refuse one.
		return true
	case value.KindList, value.KindTuple:
		s, ok := v.Seq()
		if !ok {
			return false
		}
		for i := range s.Len() {
			if !foldableDepth(s.At(i), depth+1) {
				return false
			}
		}
		return true
	case value.KindDict:
		d, ok := v.Dict()
		if !ok {
			return false
		}
		for _, k := range d.Keys() {
			item, found, err := d.Get(k)
			if err != nil || !found {
				return false
			}
			if !foldableDepth(k, depth+1) || !foldableDepth(item, depth+1) {
				return false
			}
		}
		return true
	case value.KindObject:
		// range is the one object Python can write back as a literal,
		// and jinja2 lists it.
		_, isRange := v.Interface().(*rangeObject)
		return isRange
	}
	return false
}

func (f *constFolder) fold(e ast.Expr) ast.Expr {
	if e == nil {
		return nil
	}
	if f.c.volatile {
		// Nothing is folded where the escaping is not yet known,
		// exactly as jinja2 skips its optimizer there.
		return e
	}
	if _, isConst := e.(*ast.Const); !isConst {
		if v, ok := f.c.tryConstEval(e); ok && foldable(v) && constSizeOK(v) {
			return &ast.Const{Pos: ast.At(e.Line()), Value: v}
		}
	}
	f.descend(e)
	return liftNegativePowerBase(e)
}

// liftNegativePowerBase reproduces a shape jinja2's *code generator* produces,
// which its parser does not.
//
// A constant reaches the generated Python source as its repr, and a negative
// number's repr begins with a minus -- so `{{ (-8) ** m }}` is written out as
// `-8 ** m`, which Python reads as `-(8 ** m)`, unary minus binding looser than
// `**`. The parentheses the template author wrote are long gone by then, and
// the answer changes sign: -64 rather than 64.
//
// It only happens to a power that survives to run time. A foldable one is
// computed by the optimizer, in Python, with the grouping intact -- which is
// why `{{ (-8) ** 2 }}` is 64 while `{% set m = 2 %}{{ (-8) ** m }}` is -64,
// and why this runs after the fold above has had its chance. A base that is
// not a literal keeps its parentheses in the generated source and so is not
// affected: `{% set x = -8 %}{{ x ** m }}` is 64.
func liftNegativePowerBase(e ast.Expr) ast.Expr {
	b, ok := e.(*ast.BinOp)
	if !ok || b.Op != ast.OpPow {
		return e
	}
	if _, constExponent := b.Right.(*ast.Const); constExponent {
		// Both sides constant means jinja2 folded the power itself,
		// in Python, with the grouping intact -- there is no generated
		// source for the minus to escape from. Reaching here with a
		// constant exponent means *this* fold refused where jinja2's
		// would not have: `{{ (-8) ** 1.5 }}` is a complex number
		// there, which is its own divergence, and rewriting it into
		// -(8 ** 1.5) would answer a real number instead of raising.
		return e
	}
	c, ok := b.Left.(*ast.Const)
	if !ok || !c.Value.IsNumber() {
		return e
	}
	negative, err := value.Ordered("<", c.Value, value.Int(0))
	if err != nil || !negative {
		return e
	}
	positive, err := value.Neg(c.Value)
	if err != nil {
		return e
	}
	b.Left = &ast.Const{Pos: ast.At(b.Left.Line()), Value: positive}
	return &ast.UnaryOp{Pos: ast.At(b.Line()), Op: ast.OpNeg, Node: b}
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
		// A {% block %} body is compiled on its own, against a fresh
		// eval context built from the environment -- so a constant
		// folded inside one does not see an enclosing {% autoescape %},
		// even though the same expression left unfolded sees it at run
		// time. That split is jinja2's, and the two halves are
		// observable against each other, so both are reproduced.
		savedEsc, savedVol := f.c.st.autoescape, f.c.volatile
		f.c.st.autoescape, f.c.volatile = f.envAutoescape, false
		f.stmts(n.Body)
		f.c.st.autoescape, f.c.volatile = savedEsc, savedVol
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
		f.autoescapeBody(n)
	}
}

// autoescapeBody folds the body of an {% autoescape %} block under the
// escaping that block establishes.
//
// A constant expression -- which is nearly always a literal true or false --
// is resolved here and lent to the evaluator for the length of the body, so a
// filter folded inside sees the setting it would see at run time. Anything
// else leaves the setting unknowable until the render, and jinja2 answers that
// by not folding at all rather than by guessing; so does this.
func (f *constFolder) autoescapeBody(n *ast.AutoescapeBlock) {
	on, ok := f.c.tryConstEval(n.Value)
	if !ok {
		saved := f.c.volatile
		f.c.volatile = true
		f.stmts(n.Body)
		f.c.volatile = saved
		return
	}
	truth, err := value.IsTrue(on)
	if err != nil {
		saved := f.c.volatile
		f.c.volatile = true
		f.stmts(n.Body)
		f.c.volatile = saved
		return
	}
	saved := f.c.st.autoescape
	f.c.st.autoescape = truth
	f.stmts(n.Body)
	f.c.st.autoescape = saved
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
		// Joined in one pass rather than accumulated pairwise, and
		// charged as it goes.
		//
		// `~` is n-ary in the tree, so folding it pairwise copied the
		// whole result once per operand: quadratic in their number, and
		// bounded only by the 64KiB a folded constant may reach --
		// about two gigabytes of copying. `{{ 'a' ~ 'a' ~ ... }}` with
		// 64,000 operands took 385 milliseconds to compile where the
		// same chain over a name took 31, and FromString takes no
		// context to be stopped by.
		var b strings.Builder
		for _, item := range items {
			text := value.Str(item)
			if c.st.ChargeBytes(int64(len(text))) != nil {
				return value.Undefined, false
			}
			b.WriteString(text)
		}
		// Str-joined and plain, which is what pairwise Concat produced:
		// it built a String from two Str()s and dropped any Markup.
		return value.String(b.String()), true

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
		out, err = value.Add(left, right, c.st)
	case ast.OpSub:
		out, err = value.Sub(left, right, c.st)
	case ast.OpMul:
		// The fold budget is passed in, so the multiplication refuses
		// itself before allocating rather than being pre-screened here
		// -- which is what `%` never was.
		out, err = value.Mul(left, right, c.st)
	case ast.OpDiv:
		out, err = value.Div(left, right)
	case ast.OpFloorDiv:
		out, err = value.FloorDiv(left, right)
	case ast.OpMod:
		out, err = value.Mod(left, right, c.st)
	case ast.OpPow:
		out, err = value.Pow(left, right, c.st)
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
		holds, err := compareStep(op.Op, left, right, c.st)
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
	// volatile means the escaping where this fold sits is not known until
	// the render, which is what `{% autoescape x %}` produces. jinja2
	// refuses to fold a filter or a test in such a context -- see
	// constFilter -- and skips its optimizer there entirely.
	volatile bool
}

// Folding runs at compile time, where there is no render and therefore nothing
// a caller has bounded. It still executes real filters, so it needs a budget of
// its own or `{{ 1.5|round(2000000000) }}` allocates its way through the
// machine inside FromString, with WithMaxIterations and WithMaxOutputBytes both
// set and both powerless because they start later.
//
// The allowance is spent per fold *attempt*, not per template. Folding is
// observable -- a print tag that folds to undefined renders "" where the
// unfolded form raises -- so whether an expression folds must not depend on how
// many expressions happened to precede it in the file.
const (
	maxFoldSteps = 100_000
	maxFoldBytes = maxFoldedConst
)

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
		budget: &budget{
			maxSteps:  maxFoldSteps,
			maxOutput: maxFoldBytes,
			// The caller's ceiling applies at compile time too, so
			// folding refuses exactly what the render would. The
			// fold's own output budget bounds it either way: a
			// caller who removed the ceiling still cannot fold a
			// constant past 64 KiB.
			maxIntBits: env.maxIntBits,
		},
	}
	return &constEvaluator{env: env, st: st}
}

// tryConstEval is the only way a fold may begin.
//
// It resets the fold allowance, and it recovers: an optimisation must never
// fail worse than not optimising. A filter that panics on its arguments took
// FromString down with it, before any render existed to bound -- now the
// expression is simply left for runtime, where the render's budget applies.
func (c *constEvaluator) tryConstEval(e ast.Expr) (v value.Value, ok bool) {
	defer func() {
		if r := recover(); r != nil {
			v, ok = value.Undefined, false
		}
	}()
	c.st.budget.resetAllowance()
	return c.constEval(e)
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
	// jinja2's Filter.as_const opens with `if eval_ctx.volatile: raise
	// Impossible()`. Five filters read the escaping, and in a volatile
	// context there is no value to read -- so none of them fold, and an
	// expression containing one is left for the render even where the rest
	// of it is constant.
	if c.volatile {
		return value.Undefined, false
	}
	if contextFilters[n.Name] {
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
	// The arity is checked here as well as at render time, because a fold
	// that skipped the check would answer a call CPython refuses -- and
	// answer it at compile time, so the refusal never happened at all.
	if sig, known := filterSignatures[n.Name]; known && c.env.stockFilters[n.Name] {
		if checkArity(sig, args) != nil {
			return value.Undefined, false
		}
	}
	out, err := fn(c.st, input, args)
	if err != nil {
		return value.Undefined, false
	}
	return out, true
}

// constTest folds `x is name(...)` when every operand is constant.
func (c *constEvaluator) constTest(n *ast.Test) (value.Value, bool) {
	// A test folds under the same rule as a filter; see constFilter.
	if c.volatile {
		return value.Undefined, false
	}
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
	if sig, known := testSignatures[n.Name]; known && c.env.stockTests[n.Name] {
		if checkArity(sig, args) != nil {
			return value.Undefined, false
		}
	}
	out, err := fn(c.st, input, args)
	if err != nil {
		return value.Undefined, false
	}
	return value.Bool(out), true
}

// constArgs evaluates a call's arguments, reporting false if any depends on
// the context.
//
// jinja2's args_as_const folds `*` and `**` too, and it folds them the way
// Python builds an argument list rather than the way a call site checks one:
// the star form is `list.extend` and the double-star form is `dict.update`.
// That is visible. `{{ [1,2]|join(**["db"]) }}` renders `1b2`, because
// dict.update takes an iterable of pairs, while the same expression over a
// name raises -- there the call really happens, and `**` there demands a
// mapping. Refusing to fold these left a subscript beside one raising at run
// time where jinja2 had already resolved it away.
func (c *constEvaluator) constArgs(a ast.Args) (*value.CallArgs, bool) {
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
	if a.DynArgs != nil {
		v, ok := c.constEval(a.DynArgs)
		if !ok || !c.extendPos(out, v) {
			return nil, false
		}
	}
	if a.DynKwargs != nil {
		v, ok := c.constEval(a.DynKwargs)
		if !ok || !c.updateKwargs(out, v) {
			return nil, false
		}
	}
	return out, true
}

// extendPos is `args.extend(value)`: anything that does not iterate abandons
// the fold, as the exception jinja2 catches there does.
func (c *constEvaluator) extendPos(out *value.CallArgs, v value.Value) bool {
	seq, err := value.Iterate(v)
	if err != nil {
		return false
	}
	for item := range seq {
		// `f(*range(1000000000))` builds the list before the call, so the
		// walk is charged -- folding has a budget of its own precisely
		// because there is no render here to bound it.
		if c.st.Step(1) != nil {
			return false
		}
		out.Pos = append(out.Pos, item)
	}
	return true
}

// updateKwargs is `kwargs.update(value)`.
//
// dict.update takes a mapping or an iterable of pairs, and a repeated name
// replaces the earlier value in place rather than being a second binding --
// so `join(d="-", **{"d": "+"})` folds to "+" where the unfolded call raises
// "got multiple values". A key that is not a string is left to the call, which
// is where CPython refuses it.
func (c *constEvaluator) updateKwargs(out *value.CallArgs, v value.Value) bool {
	set := func(k, item value.Value) bool {
		if k.Kind() != value.KindString {
			return false
		}
		name := k.AsString()
		for i := range out.Kwargs {
			if out.Kwargs[i].Name == name {
				out.Kwargs[i].Value = item
				return true
			}
		}
		out.Kwargs = append(out.Kwargs, value.Kwarg{Name: name, Value: item})
		return true
	}
	if d, ok := v.Dict(); ok {
		for _, e := range d.Entries() {
			if !set(e.Key, e.Value) {
				return false
			}
		}
		return true
	}
	seq, err := value.Iterate(v)
	if err != nil {
		return false
	}
	for item := range seq {
		if c.st.Step(1) != nil {
			return false
		}
		pair, err := value.Iterate(item)
		if err != nil {
			return false
		}
		var kv []value.Value
		for got := range pair {
			if len(kv) == 2 {
				return false // length 3 or more; 2 is required
			}
			kv = append(kv, got)
		}
		if len(kv) != 2 || !set(kv[0], kv[1]) {
			return false
		}
	}
	return true
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

// constGetItem is envGetItem with no render behind it.
func constGetItem(base, key value.Value) value.Value {
	return envGetItem(nil, base, key)
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
	bound := func(e ast.Expr) (value.Value, bool) {
		if e == nil {
			return value.None, true
		}
		return c.constEval(e)
	}
	start, constStart := bound(slice.Start)
	stop, constStop := bound(slice.Stop)
	step, constStep := bound(slice.Step)
	if !constStart || !constStop || !constStep {
		return value.Undefined, false
	}

	// The same slicing the evaluator does, so that folding an expression
	// and running it cannot answer differently -- they had a copy each and
	// the copies drifted. A value the slice does not apply to fails the
	// subscript, which Environment.getitem turns into undefined.
	if base.Kind() == value.KindUndefined || base.Kind() == value.KindDict ||
		!sliceable(base) {
		return value.UndefinedHint("%s is not subscriptable", value.ObjectTypeRepr(base)), true
	}
	out, err := sliceOf(base, start, stop, step)
	switch {
	case err == nil:
		return out, true
	case errors.Is(err, errs.TypeError), errors.Is(err, errs.LookupError):
		// What Environment.getitem swallows, it swallows here too.
		return value.UndefinedHint("invalid slice"), true
	default:
		// Anything else -- a step of zero is a ValueError -- comes back
		// out of getitem and reaches the template, so the fold is
		// refused and the expression is left for the render. jinja2
		// does the same by catching every exception around its own
		// output folding and falling back to run time.
		return value.Undefined, false
	}
}

// sliceable reports whether sliceOf has a rule for this value, which is what
// constGetSlice needs to tell "no rule" from "the rule said no".
func sliceable(v value.Value) bool {
	switch v.Kind() {
	case value.KindString, value.KindBytes, value.KindList, value.KindTuple:
		return true
	case value.KindObject:
		switch v.Interface().(type) {
		case value.Slicer, value.TupleView, value.Sequence:
			return true
		}
	}
	return false
}

// walkOutputs visits every print tag in a template body, reporting the
// escaping that applies where it sits.
//
// jinja2 settles that at compile time, from the frame's eval context, and the
// two ways it can differ from the template's own setting are both reproduced.
// A {% autoescape %} with a constant argument sets it for the body. One with
// an expression does *not*: it only marks the context volatile, leaving the
// enclosing setting in place for anything folded inside -- so a constant print
// there is baked with the setting the block was supposed to replace, and the
// block's own value never reaches it. A {% block %} body is compiled against a
// fresh context and so returns to the environment's setting.
func walkOutputs(c *constEvaluator, body []ast.Stmt, fn func(*ast.Output, bool)) {
	env := c.st.autoescape
	var walk func([]ast.Stmt, bool)
	walk = func(body []ast.Stmt, escaping bool) {
		for _, stmt := range body {
			switch n := stmt.(type) {
			case *ast.Output:
				fn(n, escaping)
			case *ast.For:
				walk(n.Body, escaping)
				walk(n.Else, escaping)
			case *ast.If:
				walk(n.Body, escaping)
				for _, elif := range n.Elif {
					walk(elif.Body, escaping)
				}
				walk(n.Else, escaping)
			case *ast.AssignBlock:
				walk(n.Body, escaping)
			case *ast.With:
				walk(n.Body, escaping)
			case *ast.Macro:
				walk(n.Body, escaping)
			case *ast.CallBlock:
				walk(n.Body, escaping)
			case *ast.FilterBlock:
				walk(n.Body, escaping)
			case *ast.Block:
				walk(n.Body, env)
			case *ast.Scope:
				walk(n.Body, escaping)
			case *ast.AutoescapeBlock:
				inner, volatile := blockEscaping(c, n, escaping)
				saved := c.volatile
				c.volatile = c.volatile || volatile
				walk(n.Body, inner)
				c.volatile = saved
			}
		}
	}
	walk(body, env)
}

// blockEscaping is the setting an {% autoescape %} establishes at compile
// time, and whether it leaves the context volatile.
//
// A constant argument sets the escaping for the body. Anything else sets
// nothing: the enclosing value stays in place for whatever is still folded
// inside, and the context becomes volatile, which stops a filter folding
// there at all.
func blockEscaping(c *constEvaluator, n *ast.AutoescapeBlock, enclosing bool) (escaping, volatile bool) {
	v, ok := c.tryConstEval(n.Value)
	if !ok {
		return enclosing, true
	}
	truth, err := value.IsTrue(v)
	if err != nil {
		return enclosing, true
	}
	return truth, false
}
