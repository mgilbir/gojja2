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
			// jinja2's _output_child_to_const says it in its own
			// docstring: "Any other exception will also be
			// evaluated at runtime for easier debugging." Only
			// Impossible means "not constant" there; anything else
			// defers the child. So a refusal recorded here is
			// discarded, and the general fold below -- which walks
			// bottom-up, as the optimizer does -- is what decides
			// whether the template compiles. Without this the print
			// pass named the outer test where CPython names the
			// operand inside the branch.
			refusalBefore := c.refusal
			v, ok := c.tryConstEval(node)
			c.refusal = refusalBefore
			if !ok {
				continue
			}
			// StrictUndefined raises when printed, so a constant
			// that resolves to one has to stay a run-time failure.
			if v.IsUndefined() && v.UndefinedBehavior() == value.UndefinedStrict {
				continue
			}
			// StrFor, not Str: a container's text is its repr, and
			// repr escapes by isprintable, which the interpreter
			// decides. Folding with the pin's tables baked
			// `{{ ['\u1c89'] }}` as an escape under 3.14, where the
			// character is assigned and prints as itself -- the
			// unfolded path was already right.
			text := value.StrFor(v, c.pyVersion())
			if escaping && !v.IsSafe() {
				// A value whose own __html__ cannot be called
				// stays a run-time failure, for the same reason
				// a StrictUndefined does: folding it would
				// swallow the error. The Markup *class* is the
				// one value like that, and the render
				// differential found it folded here while the
				// unfolded path already raised.
				if value.HTMLRefusal(v) != nil {
					continue
				}
				if html, ok := value.HTML(v); ok {
					text = html
				} else {
					text = escapeHTML(text)
				}
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
		mergeAdjacentData(out)
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
func foldConstantExpressions(c *constEvaluator, body []ast.Stmt, blocks []*ast.Block) {
	f := &constFolder{
		dep:           &depChecker{env: c.env, name: c.name, source: c.source},
		c:             c,
		envAutoescape: c.st.autoescape,
		// jinja2's have_extends: the flag is armed by an {% extends %}
		// anywhere in the tree, and only fires once one has been seen
		// at the root. See stmt's Output and Extends arms.
		outputChecked: containsExtends(body),
		rootlevel:     true,
		toplevel:      true,
	}
	f.stmts(body)
	// A {% block %} body is generated after the whole root body, from the
	// flat list the pre-pass collected -- so a fold that refuses inside one
	// loses to any refusal in the root body, however late it stands, and to
	// one in an earlier block. Walking the bodies where they are written
	// named the block's subscript where jinja2 names the {% set %} below it.
	for _, blk := range blocks {
		f.block(blk)
	}
}

// containsExtends reports whether the tree holds an {% extends %}, wherever it
// stands -- jinja2's `node.find(nodes.Extends) is not None`.
func containsExtends(body []ast.Stmt) bool {
	found := false
	ast.InspectStmts(body, func(n ast.Node) bool {
		if _, ok := n.(*ast.Extends); ok {
			found = true
		}
		return !found
	})
	return found
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
	// outputChecked and knownExtends are jinja2's require_output_check
	// and has_known_extends, which decide whether a print tag reaches the
	// code generator at all -- and so whether its expression is folded.
	// rootlevel is the frame flag that arms the second one.
	outputChecked bool
	knownExtends  bool
	rootlevel     bool
	// toplevel is jinja2's frame flag of that name: whether these
	// statements are compiled into the template's own function, which is
	// the one thing {% extends %} requires.
	toplevel bool
	// soft is jinja2's soft_frame: inside an {% if %} an unknown filter
	// name is left for the render rather than refused here. It belongs to
	// the dependency check, which this walk drives -- see check.
	soft bool
	// dep looks up the filter and test names in each expression this walk
	// has just folded. jinja2 does both from its code generator, node by
	// node, so a template with two faults names the one the generator
	// reaches first: `{{ 1|nosuch }}{{ (0 ** 0)[7] and 0 }}` names the
	// filter and the two swapped name the subscript. Two passes could not
	// do that, however carefully their walks were kept in step.
	dep *depChecker
}

// check looks up the filter and test names in one expression, at the point the
// generator would have written it out. A refusal joins the fold's own in the
// single slot the compile reads, so the first in this walk is the one reported.
func (f *constFolder) check(e ast.Expr) {
	if e == nil || f.c.refusal != nil {
		return
	}
	f.dep.expr(e, f.soft)
	if f.dep.err != nil {
		f.c.refusal = f.dep.err
	}
	if f.c.lateRefusal == nil {
		f.c.lateRefusal = repeatedKeyword(e, f.c.name, f.c.source)
	}
}

// repeatedKeyword reports the first call, filter or test in an expression that
// names a keyword twice.
//
// jinja2 writes a call's keywords out as `name=value` pairs, so CPython's own
// compiler refuses the generated module -- `{{ m(a=1, a=2) }}` does not compile
// there and rendered here, the direction of divergence that costs a template
// author something. A node that folded first is gone by now, which is what
// makes `{{ [1]|join(d='-', d='+') }}` render on both sides.
func repeatedKeyword(e ast.Expr, name, source string) error {
	var refusal error
	ast.Inspect(e, func(n ast.Node) bool {
		if refusal != nil {
			return false
		}
		var args *ast.Args
		switch v := n.(type) {
		case *ast.Call:
			args = &v.Args
		case *ast.Filter:
			args = &v.Args
		case *ast.Test:
			args = &v.Args
		default:
			return true
		}
		seen := make(map[string]bool, len(args.Kwargs))
		for _, kw := range args.Kwargs {
			if seen[kw.Key] {
				// CPython's wording, with the line of the
				// template rather than of the module it was
				// compiled into. See docs/divergences.md.
				err := errs.New(errs.SyntaxError,
					"keyword argument repeated: %s", kw.Key)
				err.Line, err.Name, err.Source = n.Line(), name, source
				refusal = err
				return false
			}
			seen[kw.Key] = true
		}
		return true
	})
	return refusal
}

// foldCheck is what the generator does to an expression it writes: fold it,
// which may refuse, and then look up the names left in it, which may refuse.
func (f *constFolder) foldCheck(e ast.Expr) ast.Expr {
	e = f.fold(e)
	f.check(e)
	return e
}

// foldCheckAll is foldCheck over a list, in order.
func (f *constFolder) foldCheckAll(list []ast.Expr) {
	for i, e := range list {
		list[i] = f.foldCheck(e)
	}
}

// fail records a refusal the walk itself raises, rather than a fold or a name.
func (f *constFolder) fail(err error) {
	if err != nil && f.c.refusal == nil {
		f.c.refusal = err
	}
}

// soft folds a branch's body: jinja2's Frame.soft(), which clears rootlevel
// and keeps toplevel. That is what makes `{% if x %}{% extends %}{% endif %}`
// legal while leaving the print tags below it compiled.
func (f *constFolder) softBody(body []ast.Stmt) {
	savedRoot, savedSoft := f.rootlevel, f.soft
	f.rootlevel, f.soft = false, true
	f.stmts(body)
	f.rootlevel, f.soft = savedRoot, savedSoft
}

// nested folds the body of a construct that opens a scope: jinja2's
// Frame.inner(), which clears toplevel too, so an {% extends %} inside one is
// refused rather than obeyed.
func (f *constFolder) nested(body []ast.Stmt) {
	savedTop, savedRoot, savedSoft := f.toplevel, f.rootlevel, f.soft
	f.toplevel, f.rootlevel, f.soft = false, false, false
	f.stmts(body)
	f.toplevel, f.rootlevel, f.soft = savedTop, savedRoot, savedSoft
}

// block folds one {% block %} body.
//
// It is compiled on its own, against a fresh eval context built from the
// environment -- so a constant folded inside one does not see an enclosing
// {% autoescape %}, even though the same expression left unfolded sees it at
// run time. That split is jinja2's, and the two halves are observable against
// each other, so both are reproduced. A nested block is not folded from here
// either: it has its own entry in the list.
func (f *constFolder) block(n *ast.Block) {
	savedEsc, savedVol := f.c.st.autoescape, f.c.volatile
	f.c.st.autoescape, f.c.volatile = f.envAutoescape, false
	f.detached(n.Body)
	f.c.st.autoescape, f.c.volatile = savedEsc, savedVol
}

// detached folds a body that is compiled on its own: a {% block %}, a macro,
// and the body of a {% set %} with one. jinja2 clears require_output_check for
// each of the three, because none of them writes to the template's own stream
// -- so what they print is compiled, and folded, even below a known extends.
func (f *constFolder) detached(body []ast.Stmt) {
	saved := f.outputChecked
	f.outputChecked = false
	f.nested(body)
	f.outputChecked = saved
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
			item, found := d.GetKnown(k)
			if !found {
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
	// Children first, which is what jinja2's optimizer does -- its
	// generic_visit calls NodeTransformer.generic_visit before it tries
	// as_const on the node itself. The folded *result* is the same either
	// way, because folding is deterministic; what differs is which refusal
	// is reached. Top-down, an untaken branch is never evaluated, so
	// `{% set v = 1 if [1] else (3 if (0b101)[::2] else 4) %}` folded to 1
	// here and did not compile there.
	f.descend(e)
	if _, isConst := e.(*ast.Const); !isConst {
		if v, ok := f.c.tryConstEval(e); ok && foldable(v) && constSizeOK(v) {
			return &ast.Const{Pos: ast.At(e.Line()), Value: v}
		}
	}
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
	negative, err := value.Ordered("<", c.Value, value.Int(0), value.DefaultPythonVersion)
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
	if f.c.refusal != nil {
		// The compile is going to fail with what is already recorded;
		// walking on could only spend time.
		return
	}
	switch n := stmt.(type) {
	case *ast.Output:
		if f.outputChecked && f.knownExtends {
			// Below an {% extends %} at the root, the child's own
			// body prints nothing, and jinja2's generator leaves
			// the whole tag out rather than guarding it -- so its
			// expressions are neither folded nor looked up, and an
			// error either would have raised never happens. Run
			// time already agrees: see execOutput.
			return
		}
		for i, node := range n.Nodes {
			if _, isData := node.(*ast.TemplateData); isData {
				continue
			}
			n.Nodes[i] = f.foldCheck(node)
		}
	case *ast.For:
		// `loop` is bound by the loop itself, so assigning it anywhere
		// inside would leave the two fighting over one name.
		if line, found := findLoopStore(n); found {
			f.dep.failAt(line, "Can't assign to special loop variable in for-loop target")
			f.fail(f.dep.err)
			return
		}
		// The test first: it becomes a function of its own, written
		// before the loop that calls it, so
		// `{% for i in [1]|nosuchA if 1|nosuchB %}` names nosuchB. It
		// is part of that function, so it is refused even inside a
		// branch that cannot be taken; the iterable is evaluated where
		// the loop is written, and softens with everything there.
		savedSoft := f.soft
		f.soft = false
		n.Test = f.foldCheck(n.Test)
		f.soft = savedSoft
		n.Iter = f.foldCheck(n.Iter)
		f.nested(n.Body)
		f.nested(n.Else)
	case *ast.If:
		// An if softens its whole subtree, condition and body alike.
		savedSoft := f.soft
		f.soft = true
		n.Test = f.foldCheck(n.Test)
		f.soft = savedSoft
		f.softBody(n.Body)
		for _, elif := range n.Elif {
			// An elif is an If of its own and softens its own body;
			// wrapping it here would clear a flag twice.
			f.stmt(elif)
		}
		f.softBody(n.Else)
	case *ast.Assign:
		n.Node = f.foldCheck(n.Node)
	case *ast.AssignBlock:
		// The body is buffered first and the filter applied to what it
		// left, in that order -- so `{% set q | nosuchA %}{{ 1|nosuchC
		// }}{% endset %}` names nosuchC. The filter is resolved with
		// the buffer's frame rather than the one the block sits in.
		f.detached(n.Body)
		f.unsoftened(func() { n.Filter = f.foldCheck(n.Filter) })
	case *ast.With:
		f.foldCheckAll(n.Values)
		f.nested(n.Body)
	case *ast.Macro:
		f.dep.checkCallerDefault(n.Body, n.Args, n.Defaults, n.Line())
		f.fail(f.dep.err)
		// Defaults are part of the macro's signature, generated with
		// the body rather than at the point of definition.
		f.unsoftened(func() { f.foldCheckAll(n.Defaults) })
		f.detached(n.Body)
	case *ast.CallBlock:
		f.dep.checkCallerDefault(n.Body, n.Args, n.Defaults, n.Line())
		f.fail(f.dep.err)
		// The block becomes a macro -- signature, then body -- and only
		// then is the call itself written, so `{% call m(1|nosuchA) %}
		// {{ 1|nosuchC }}{% endcall %}` names nosuchC. The call is made
		// where the block is written, so it softens; the block's own
		// parameters belong to its signature.
		f.unsoftened(func() { f.foldCheckAll(n.Defaults) })
		f.detached(n.Body)
		// The call cannot fold to a constant -- its callee is a name --
		// but its arguments can, and a refusal inside one belongs here.
		if call, ok := f.foldCheck(n.Call).(*ast.Call); ok {
			n.Call = call
		}
	case *ast.FilterBlock:
		// The body is buffered first and the filter applied to what it
		// left. The filter naming the block is resolved where the block
		// is generated, which happens whether or not the branch runs.
		f.nested(n.Body)
		f.unsoftened(func() { n.Filter = f.foldCheck(n.Filter) })
	case *ast.Block:
		// Nothing: where a block is *written* the generator only calls
		// it. Its body is folded by block, after the root body.
	case *ast.ExprStmt:
		n.Node = f.foldCheck(n.Node)
	case *ast.Include:
		n.Template = f.foldCheck(n.Template)
	case *ast.Import:
		n.Template = f.foldCheck(n.Template)
	case *ast.FromImport:
		n.Template = f.foldCheck(n.Template)
	case *ast.Extends:
		// Which template a render extends has to be settled once, for
		// the whole render. Reached from a frame, it would depend on
		// control flow: gojja2 let `{% for i in [] %}{% extends %}`
		// through, so an empty sequence silently skipped the
		// inheritance and a non-empty one applied it.
		if !f.toplevel {
			f.dep.failAt(n.Line(), "cannot use extend from a non top-level scope")
			f.fail(f.dep.err)
			return
		}
		n.Template = f.foldCheck(n.Template)
		// Only an extends the root body reaches unconditionally is
		// known: one inside an {% if %} leaves the print tags below it
		// compiled, and guarded at run time instead.
		if f.rootlevel {
			f.knownExtends = true
		}
	case *ast.Scope:
		f.nested(n.Body)
	case *ast.AutoescapeBlock:
		n.Value = f.foldCheck(n.Value)
		f.autoescapeBody(n)
	}
}

// unsoftened runs one slot outside any branch's softening: what a {% filter %}
// names, and what a macro's signature holds, is generated whether or not the
// branch around it can be taken.
func (f *constFolder) unsoftened(fn func()) {
	saved := f.soft
	f.soft = false
	fn()
	f.soft = saved
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
		f.nested(n.Body)
		f.c.volatile = saved
		return
	}
	truth, err := value.IsTrue(on)
	if err != nil {
		saved := f.c.volatile
		f.c.volatile = true
		f.nested(n.Body)
		f.c.volatile = saved
		return
	}
	saved := f.c.st.autoescape
	f.c.st.autoescape = truth
	f.nested(n.Body)
	f.c.st.autoescape = saved
}

// constEval evaluates the literal subset of the expression grammar: literals,
// containers of literals, and attribute or item access over them. Anything
// else -- an operator, a filter, a call, a name -- reports false, which is
// what keeps the fold as narrow as jinja2's.
// constEval folds an expression, giving any undefined it produces the
// environment's Undefined class.
//
// The stamp belongs here, at every level of the recursion, rather than once on
// the way out: an undefined is an operand as well as a result, and a filter
// applied to one inside the same fold has to see the class too. Without it
// `{{ none.missing|string }}` folded to "" under DebugUndefined, because the
// filter ran against a plain jinja2.Undefined and the stamp came afterwards.
func (c *constEvaluator) constEval(e ast.Expr) (value.Value, bool) {
	v, ok := c.constEvalNode(e)
	// The comparison keeps the common case allocation-free: an undefined
	// already of the environment's class is left exactly as it is.
	if ok && v.IsUndefined() && v.UndefinedBehavior() != c.env.undefined {
		v = v.WithBehavior(c.env.undefined)
	}
	return v, ok
}

// chainOrDefer answers an attribute or item lookup whose receiver this fold has
// already reduced to an undefined.
//
// Environment.getattr and Environment.getitem swallow a missing name, but not
// an Undefined receiver: that raises, so the fold is normally abandoned and the
// lookup happens for real at run time. A ChainableUndefined answers itself
// instead of raising, and there the run time cannot always stand in: a *slice*
// of a value that has none -- `((2.5)[1:2])[0]` -- is a hard TypeError at run
// time, so the undefined the fold produced would never exist there to chain
// from, and gojja2 raised where jinja2 prints nothing. An attribute route to
// the same shape worked, which is why this only ever showed through a slice.
func (c *constEvaluator) chainOrDefer(base value.Value) (value.Value, bool) {
	if c.env.undefined == value.UndefinedChainable {
		return base, true
	}
	return value.Undefined, false
}

func (c *constEvaluator) constEvalNode(e ast.Expr) (value.Value, bool) {
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
			if err := d.Set(k, v, c.pyVersion()); err != nil {
				return value.Undefined, false
			}
		}
		return out, true

	case *ast.Getattr:
		base, ok := c.constEval(n.Node)
		if !ok {
			return value.Undefined, false
		}
		if base.IsUndefined() {
			return c.chainOrDefer(base)
		}
		return constGetAttr(base, n.Attr, c.pyVersion()), true

	case *ast.Getitem:
		base, ok := c.constEval(n.Node)
		if !ok {
			return value.Undefined, false
		}
		// The *argument* decides whether there is a fold at all, before
		// the base's undefinedness decides what the fold answers.
		// jinja2 folds a node only when every part of it is constant --
		// `Name.as_const` is Impossible -- so `((3)[-2:])[n]` is left
		// for the render, where a slice bypasses Environment.getitem
		// and raises. Chaining on the base first folded it to an
		// undefined under ChainableUndefined, and the comparison above
		// it to False: a TypeError swallowed at compile time.
		if slice, isSlice := n.Arg.(*ast.Slice); isSlice {
			if !c.constSliceBounds(slice) {
				return value.Undefined, false
			}
			if base.IsUndefined() {
				return c.chainOrDefer(base)
			}
			return c.constGetSlice(base, slice)
		}
		key, ok := c.constEval(n.Arg)
		if !ok {
			return value.Undefined, false
		}
		if base.IsUndefined() {
			return c.chainOrDefer(base)
		}
		return constGetItem(base, key), true

	case *ast.BinOp:
		return c.constBinOp(n)

	case *ast.UnaryOp:
		return c.constUnaryOp(n)

	case *ast.Concat:
		items, ok := c.constConcatItems(n.Nodes)
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
			text := value.StrFor(item, c.pyVersion())
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
			return c.refuse(err)
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

// constConcatItems folds `~`'s operands, refusing at the first one that cannot
// give its text.
//
// jinja2 writes this as `"".join(str(x.as_const(...)) for x in self.nodes)`,
// and the generator is what makes it observable: each operand is converted
// *before* the next one is folded, so a StrictUndefined in the first position
// raises even when a later operand is not constant at all. Folding them all
// first and converting afterwards loses that --
// `{{ (2147483648)[-2:] ~ 'x'.__class__(1, 2, 3, 4) }}` abandoned the whole
// fold over the unfoldable right side and let the render report a TypeError,
// where CPython refuses the template over the left one.
func (c *constEvaluator) constConcatItems(nodes []ast.Expr) ([]value.Value, bool) {
	out := make([]value.Value, 0, len(nodes))
	for _, n := range nodes {
		v, ok := c.constEval(n)
		if !ok {
			return nil, false
		}
		if err := value.StrictRefusal(v); err != nil {
			c.refuse(err)
			return nil, false
		}
		out = append(out, v)
	}
	return out, true
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
			return c.refuse(err)
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
		out, err = value.Sub(left, right, c.st, c.st.PythonVersion())
	case ast.OpMul:
		// The fold budget is passed in, so the multiplication refuses
		// itself before allocating rather than being pre-screened here
		// -- which is what `%` never was.
		out, err = value.Mul(left, right, c.st)
	case ast.OpDiv:
		out, err = value.Div(left, right, c.pyVersion())
	case ast.OpFloorDiv:
		out, err = value.FloorDiv(left, right, c.pyVersion())
	case ast.OpMod:
		out, err = value.Mod(left, right, c.st, c.pyVersion())
	case ast.OpPow:
		out, err = value.Pow(left, right, c.st, c.pyVersion())
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
		holds, err := compareStep(op.Op, left, right, c.st, c.pyVersion())
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
// pyVersion is the interpreter this fold reproduces. Constant folding runs
// answers that a render would otherwise produce, so it has to agree with the
// render about every rule that differs by interpreter -- a fold that used the
// default while the environment chose otherwise would make `{{ 1/0 }}` and
// `{{ x/0 }}` word the same failure two ways.
func (c *constEvaluator) pyVersion() value.PythonVersion { return c.env.pyVersion }

type constEvaluator struct {
	env *Environment
	st  *State
	// name and source place a refusal in the template, the way the
	// dependency check places its own.
	name   string
	source string
	// volatile means the escaping where this fold sits is not known until
	// the render, which is what `{% autoescape x %}` produces. jinja2
	// refuses to fold a filter or a test in such a context -- see
	// constFilter -- and skips its optimizer there entirely.
	volatile bool
	// refusal is a StrictUndefined that the fold asked a question only it
	// can answer with an error: its truthiness, or its text. jinja2 lets
	// that error out of `from_string`, so the template does not compile at
	// all, and this is how it gets from the fold to the caller.
	//
	// The first one wins and folding stops, because the template is not
	// going to be returned either way and a second refusal would only
	// change which of two broken expressions is named.
	refusal error
	// lateRefusal is a repeated keyword argument, which is a SyntaxError
	// out of the *generated module* -- so CPython raises it only once the
	// whole module has been written, and every refusal the generator itself
	// makes wins. It is kept aside for that reason and reported last.
	lateRefusal error
}

// refuse records a refusal that must end the compile, and reports the
// expression as unfoldable so every caller unwinds the way it already does.
//
// Only three folds may call it, and the set is jinja2's rather than a choice:
// its `BinExpr.as_const`, `Compare.as_const`, `Filter.as_const` and the rest
// wrap themselves in `except Exception: raise Impossible()`, so an error there
// means "not constant" and the expression is left for the render. `Concat`,
// `And`, `Or` and `CondExpr` have no such guard, so an error raised inside them
// travels straight out of the optimizer. That is why `{{ (0.0).a ~ 1 }}` does
// not compile under StrictUndefined while `{{ (0.0).a + 1 }}` compiles and
// fails at render.
func (c *constEvaluator) refuse(err error) (value.Value, bool) {
	if c.volatile {
		// Where the escaping is not yet known, a refusal does not
		// escape: `{% autoescape nil %}{{ (0.0).a ~ 1 }}` fails at
		// render on the undefined name in the tag, not at compile time
		// on the concatenation. All four refusing folds behave that way
		// -- jinja2's Concat.as_const checks the flag itself, because
		// whether its result is Markup depends on the answer, and the
		// other three are not reached there at all. Ordinary folding
		// continues: `{% autoescape x %}{{ {'a': 1} }}` is still baked
		// with the environment's setting, which escape/volatile_folds_
		// constant grades.
		return value.Undefined, false
	}
	if c.refusal == nil {
		c.refusal = err
	}
	return value.Undefined, false
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
func newConstEvaluator(env *Environment, name, source string, fromString bool) *constEvaluator {
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
	return &constEvaluator{env: env, st: st, name: name, source: source}
}

// tryConstEval is the only way a fold may begin.
//
// It resets the fold allowance, and it recovers: an optimisation must never
// fail worse than not optimising. A filter that panics on its arguments took
// FromString down with it, before any render existed to bound -- now the
// expression is simply left for runtime, where the render's budget applies.
func (c *constEvaluator) tryConstEval(e ast.Expr) (v value.Value, ok bool) {
	if c.refusal != nil {
		// The compile is already going to fail. Folding on would only
		// spend time and could record a second refusal over the first.
		return value.Undefined, false
	}
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
		// Last wins. jinja2 folds a call's keywords through a dict
		// comprehension, `{k.key: k.value.as_const() ...}`, where a
		// repeated name keeps the last quietly -- and a repeated name
		// in a node that does *not* fold is a SyntaxError out of the
		// generated module, so this is the only place one survives:
		// `{{ [1]|join(d='-', d='+') }}` renders where
		// `{{ lst|join(d='-', d='+') }}` does not compile.
		setKwarg(out, kw.Key, v)
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
// setKwarg binds a keyword in a folded call, replacing one of the same name.
func setKwarg(out *value.CallArgs, name string, v value.Value) {
	for i := range out.Kwargs {
		if out.Kwargs[i].Name == name {
			out.Kwargs[i].Value = v
			return
		}
	}
	out.Kwargs = append(out.Kwargs, value.Kwarg{Name: name, Value: v})
}

func (c *constEvaluator) updateKwargs(out *value.CallArgs, v value.Value) bool {
	set := func(k, item value.Value) bool {
		if k.Kind() != value.KindString {
			return false
		}
		setKwarg(out, k.AsString(), item)
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
func constGetAttr(base value.Value, name string, py value.PythonVersion) value.Value {
	if v, ok := lookupAttr(nil, base, name); ok {
		return v
	}
	if v, ok := lookupItem(base, value.String(name), py); ok {
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
	case value.KindObject:
		// A range, or a groupby group: an object that presents a
		// sequence is indexed like one. jinja2 reaches these through
		// Environment.getitem like everything else, so
		// `[range(3)]|map(attribute=1)` is 1 -- where this knew only the
		// built-in kinds and answered undefined, which a `default=` then
		// covered up. Found by the coverage-guided fuzzer, on
		// `[range(3)]|groupby(1, 2, 3)`.
		seq, ok := base.Interface().(value.Sequence)
		if !ok {
			return value.Undefined, false
		}
		idx := int(i)
		if idx < 0 {
			idx += seq.Len()
		}
		return seq.GetIndex(idx)
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
	case value.KindBytes:
		// A bytes indexes to the byte *value*, not to a one-byte bytes:
		// `b'b,c'[1]` is 44. The run-time subscript has always said so;
		// this path had no arm for it at all, so every integer
		// attribute over a bytes was undefined -- which is what
		// `|groupby(1)` over bytes grouped on.
		raw := base.AsString()
		idx := int(i)
		if idx < 0 {
			idx += len(raw)
		}
		if idx < 0 || idx >= len(raw) {
			return value.Undefined, false
		}
		return value.Int(int64(raw[idx])), true
	}
	return value.Undefined, false
}

// constSliceBounds reports whether every bound a slice carries is constant,
// which is what decides whether the subscript can be folded at all. jinja2's
// Slice.as_const asks the same of each one.
func (c *constEvaluator) constSliceBounds(slice *ast.Slice) bool {
	for _, e := range []ast.Expr{slice.Start, slice.Stop, slice.Step} {
		if e == nil {
			continue
		}
		if _, ok := c.constEval(e); !ok {
			return false
		}
	}
	return true
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
		// The same owner/key undefined the bad-bound branch below
		// produces, for the same reason: a hint carries the text but
		// renders as "undefined value printed: ..." under
		// DebugUndefined, where jinja2 names the slice.
		return value.UndefinedSlice(base, start, stop, step), true
	}
	out, err := sliceOf(base, start, stop, step, c.pyVersion())
	switch {
	case err == nil:
		return out, true
	case errors.Is(err, errs.TypeError), errors.Is(err, errs.LookupError):
		// What Environment.getitem swallows, it swallows here too --
		// and it swallows it into the same shape, an undefined naming
		// the owner and the subscript. A hint would carry the right
		// message and still render as "undefined value printed: ..."
		// under DebugUndefined, where jinja2 names the slice.
		return value.UndefinedSlice(base, start, stop, step), true
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
		case value.BigSlicer, value.Slicer, value.TupleView, value.Sequence:
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
				// The escaping in force is not only what the
				// folded text is escaped *with* -- it is what
				// the fold itself runs under, because five
				// filters read it. The general fold sets it from
				// its own walk; this one has to as well, and did
				// not need to while it ran second over a tree
				// that pass had already folded.
				saved := c.st.autoescape
				c.st.autoescape = escaping
				fn(n, escaping)
				c.st.autoescape = saved
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

// mergeAdjacentData joins literal runs that folding left next to one another,
// so that `x{{ 1 }}y` is one piece of output rather than three -- which is what
// jinja2's code generator emits for it, and so what its output is numbered by.
//
// Each run is joined once. Appending to the previous node instead is quadratic
// in the length of the run, and a template that is mostly constant prints is
// one long run: two megabytes of them took thirteen seconds to compile, which
// the linearity guard caught.
func mergeAdjacentData(out *ast.Output) {
	isData := func(n ast.Expr) bool { _, ok := n.(*ast.TemplateData); return ok }
	merged := out.Nodes[:0]
	for i := 0; i < len(out.Nodes); {
		if !isData(out.Nodes[i]) {
			merged = append(merged, out.Nodes[i])
			i++
			continue
		}
		j := i + 1
		for j < len(out.Nodes) && isData(out.Nodes[j]) {
			j++
		}
		first := out.Nodes[i].(*ast.TemplateData)
		if j-i > 1 {
			var b strings.Builder
			for _, n := range out.Nodes[i:j] {
				b.WriteString(n.(*ast.TemplateData).Data)
			}
			first = &ast.TemplateData{Pos: ast.At(first.Line()), Data: b.String()}
		}
		merged = append(merged, first)
		i = j
	}
	out.Nodes = merged
}
