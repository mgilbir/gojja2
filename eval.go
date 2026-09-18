// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"math"
	"strings"

	"github.com/mgilbir/gojja2/errs"
	"github.com/mgilbir/gojja2/internal/ast"
	"github.com/mgilbir/gojja2/value"
)

func (ex *exec) eval(e ast.Expr) (value.Value, error) {
	v, err := ex.evalInner(e)
	if err != nil {
		return value.Undefined, errs.At(err, ex.st.tmpl.name, e.Line())
	}
	return v, nil
}

func (ex *exec) evalInner(e ast.Expr) (value.Value, error) {
	switch n := e.(type) {
	case *ast.Const:
		// A constant holding a container came from folding a literal,
		// which must produce a fresh one per evaluation.
		switch n.Value.Kind() {
		case value.KindList, value.KindDict, value.KindTuple:
			return value.Copy(n.Value), nil
		}
		return n.Value, nil
	case *ast.TemplateData:
		// Literal markup from the template source is trusted.
		return markup(n.Data, ex.autoescape), nil
	case *ast.Name:
		return ex.evalName(n)
	case *ast.NSRef:
		return ex.evalNSRef(n)
	case *ast.Tuple:
		items, err := ex.evalAll(n.Items)
		if err != nil {
			return value.Undefined, err
		}
		return value.NewTuple(items...), nil
	case *ast.List:
		items, err := ex.evalAll(n.Items)
		if err != nil {
			return value.Undefined, err
		}
		return value.NewList(items...), nil
	case *ast.Dict:
		return ex.evalDict(n)
	case *ast.CondExpr:
		return ex.evalCond(n)
	case *ast.BinOp:
		return ex.evalBinOp(n)
	case *ast.UnaryOp:
		return ex.evalUnaryOp(n)
	case *ast.Concat:
		return ex.evalConcat(n)
	case *ast.Compare:
		return ex.evalCompare(n)
	case *ast.Getattr:
		return ex.evalGetattr(n)
	case *ast.Getitem:
		return ex.evalGetitem(n)
	case *ast.Slice:
		return value.Undefined, errs.New(errs.TemplateRuntimeError,
			"a slice is only valid inside a subscript")
	case *ast.Call:
		return ex.evalCall(n)
	case *ast.Filter:
		return ex.evalFilter(n)
	case *ast.Test:
		return ex.evalTest(n)
	}
	return value.Undefined, errs.New(errs.TemplateRuntimeError, "cannot evaluate %s", e.TypeName())
}

func (ex *exec) evalAll(items []ast.Expr) ([]value.Value, error) {
	out := make([]value.Value, len(items))
	for i, item := range items {
		v, err := ex.eval(item)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

func (ex *exec) evalName(n *ast.Name) (value.Value, error) {
	if v, ok := ex.sc.lookup(n.Name); ok {
		return v, nil
	}
	switch n.Name {
	case "self":
		return value.FromObject(&templateReference{st: ex.st}), nil
	case "super":
		if ex.blockName != "" {
			// super() renders the next definition of the block
			// currently being rendered, one step toward the base.
			return value.FromObject(&blockReference{
				st: ex.st, name: ex.blockName, index: ex.blockIndex + 1,
			}), nil
		}
	}
	return ex.st.Undefined(value.NewUndefined(n.Name)), nil
}

func (ex *exec) evalNSRef(n *ast.NSRef) (value.Value, error) {
	base, ok := ex.sc.lookup(n.Name)
	if !ok {
		return ex.st.Undefined(value.NewUndefined(n.Name)), nil
	}
	return ex.getAttr(base, n.Attr)
}

func (ex *exec) evalDict(n *ast.Dict) (value.Value, error) {
	out := value.NewDict()
	d, _ := out.Dict()
	for _, pair := range n.Items {
		k, err := ex.eval(pair.Key)
		if err != nil {
			return value.Undefined, err
		}
		v, err := ex.eval(pair.Value)
		if err != nil {
			return value.Undefined, err
		}
		if err := d.Set(k, v); err != nil {
			return value.Undefined, err
		}
	}
	return out, nil
}

func (ex *exec) evalCond(n *ast.CondExpr) (value.Value, error) {
	ok, err := ex.truth(n.Test)
	if err != nil {
		return value.Undefined, err
	}
	if ok {
		return ex.eval(n.True)
	}
	if n.False == nil {
		// `a if b` with no else yields undefined, and jinja2 words the
		// failure that way if the result is then used.
		where := ""
		if name := ex.st.tmpl.name; name != "" {
			where = " in " + value.Repr(value.String(name))
		}
		return ex.st.Undefined(value.UndefinedHint(
			"the inline if-expression on line %d%s evaluated to false and no else"+
				" section was defined.", n.Line(), where)), nil
	}
	return ex.eval(n.False)
}

func (ex *exec) evalBinOp(n *ast.BinOp) (value.Value, error) {
	// and/or short-circuit and yield an operand, not a bool.
	switch n.Op {
	case ast.OpAnd:
		left, err := ex.eval(n.Left)
		if err != nil {
			return value.Undefined, err
		}
		ok, err := value.IsTrue(left)
		if err != nil || !ok {
			return left, err
		}
		return ex.eval(n.Right)
	case ast.OpOr:
		left, err := ex.eval(n.Left)
		if err != nil {
			return value.Undefined, err
		}
		ok, err := value.IsTrue(left)
		if err != nil {
			return value.Undefined, err
		}
		if ok {
			return left, nil
		}
		return ex.eval(n.Right)
	}

	left, err := ex.eval(n.Left)
	if err != nil {
		return value.Undefined, err
	}
	right, err := ex.eval(n.Right)
	if err != nil {
		return value.Undefined, err
	}
	switch n.Op {
	case ast.OpAdd:
		return value.Add(left, right, ex.st)
	case ast.OpSub:
		return value.Sub(left, right, ex.st)
	case ast.OpMul:
		return value.Mul(left, right, ex.st)
	case ast.OpDiv:
		return value.Div(left, right)
	case ast.OpFloorDiv:
		return value.FloorDiv(left, right)
	case ast.OpMod:
		return value.Mod(left, right, ex.st)
	case ast.OpPow:
		return value.Pow(left, right, ex.st)
	}
	return value.Undefined, errs.New(errs.TemplateRuntimeError, "unknown operator %s", n.Op)
}

func (ex *exec) evalUnaryOp(n *ast.UnaryOp) (value.Value, error) {
	v, err := ex.eval(n.Node)
	if err != nil {
		return value.Undefined, err
	}
	switch n.Op {
	case ast.OpNot:
		ok, err := value.IsTrue(v)
		if err != nil {
			return value.Undefined, err
		}
		return value.Bool(!ok), nil
	case ast.OpNeg:
		return value.Neg(v)
	case ast.OpPos:
		return value.Pos(v)
	}
	return value.Undefined, errs.New(errs.TemplateRuntimeError, "unknown operator %s", n.Op)
}

func (ex *exec) evalConcat(n *ast.Concat) (value.Value, error) {
	// Autoescaping alone does not make `~` produce Markup. jinja2 compiles
	// it to markup_join, which walks the operands and only switches to
	// joining as Markup once it meets one that already is; with none it
	// concatenates the str()s and returns a plain string, to be escaped at
	// output like any other value.
	//
	// Escaping regardless looked identical in `{{ a ~ b }}` and was wrong
	// for every other use of the result: `{{ (sv ~ n)|length }}` counted
	// the entities it had just introduced, `|upper` shouted them, and a
	// type error named Markup where CPython names str.
	parts := make([]value.Value, 0, len(n.Nodes))
	markup := false
	for _, node := range n.Nodes {
		v, err := ex.eval(node)
		if err != nil {
			return value.Undefined, err
		}
		if v.IsUndefined() && v.UndefinedBehavior() == value.UndefinedStrict {
			return value.Undefined, v.UndefinedError()
		}
		if v.IsSafe() {
			markup = true
		}
		parts = append(parts, v)
	}
	// One Markup operand escapes every other one, including those already
	// passed -- markup_join rejoins the whole sequence when it finds one.
	//
	// Except inside an {% autoescape %} whose argument is not a literal,
	// where jinja2 concatenates plainly whatever the setting says. Its
	// compiler picks the join for a volatile context with
	//
	//	(markup_join if context.eval_ctx.volatile else str_join)
	//
	// and an eval context is only ever volatile at *compile* time -- the
	// attribute the generated code reads is always False -- so `~` there
	// escapes nothing and returns a plain string, which the output then
	// escapes as a whole. That is upstream's, wording and all, and a
	// template can see it: the same `~` a line further out answers Markup.
	escaping := ex.autoescape && markup && !ex.volatileEscape

	var b strings.Builder
	for _, v := range parts {
		text := value.Str(v)
		if escaping && !v.IsSafe() {
			text = escapeHTML(text)
		}
		b.WriteString(text)
	}
	if escaping {
		return value.Safe(b.String()), nil
	}
	return value.String(b.String()), nil
}

// evalCompare walks a comparison chain, evaluating each operand once and
// stopping at the first false step, as Python does.
func (ex *exec) evalCompare(n *ast.Compare) (value.Value, error) {
	left, err := ex.eval(n.Expr)
	if err != nil {
		return value.Undefined, err
	}
	for _, op := range n.Ops {
		right, err := ex.eval(op.Expr)
		if err != nil {
			return value.Undefined, err
		}
		ok, err := compareStep(op.Op, left, right, ex.st)
		if err != nil {
			return value.Undefined, err
		}
		if !ok {
			return value.False, nil
		}
		left = right
	}
	return value.True, nil
}

func compareStep(op string, left, right value.Value, budget value.Budget) (bool, error) {
	switch op {
	case "eq":
		return value.EqualErr(left, right)
	case "ne":
		equal, err := value.EqualErr(left, right)
		return !equal, err
	case "lt":
		return value.Ordered("<", left, right)
	case "lteq":
		return value.Ordered("<=", left, right)
	case "gt":
		return value.Ordered(">", left, right)
	case "gteq":
		return value.Ordered(">=", left, right)
	case "in":
		return value.Contains(left, right, budget)
	case "notin":
		ok, err := value.Contains(left, right, budget)
		return !ok, err
	}
	return false, errs.New(errs.TemplateRuntimeError, "unknown comparison %q", op)
}

func (ex *exec) evalGetattr(n *ast.Getattr) (value.Value, error) {
	base, err := ex.eval(n.Node)
	if err != nil {
		return value.Undefined, err
	}
	return ex.getAttr(base, n.Attr)
}

// getAttr resolves `base.name`.
//
// jinja2 tries the attribute first and falls back to an item lookup, so
// `d.keys` finds a dict entry named "keys" only if there is no attribute of
// that name. A miss yields undefined, which is what makes
// `{{ missing.attr|default('x') }}` work.
//
// Reaching *through* an undefined is different: `{{ nope.attr }}` fails, even
// though `{{ nope }}` prints nothing, because Undefined.__getattr__ raises.
// Only ChainableUndefined keeps going.
func (ex *exec) getAttr(base value.Value, name string) (value.Value, error) {
	if name == "__class__" {
		return classOf(base), nil
	}
	if base.IsUndefined() {
		if base.UndefinedBehavior() == value.UndefinedChainable {
			return base, nil
		}
		return value.Undefined, base.UndefinedError()
	}
	if v, ok := lookupAttr(ex.st, base, name); ok {
		return v, nil
	}
	if v, ok := lookupItem(base, value.String(name)); ok {
		return v, nil
	}
	return ex.st.Undefined(value.UndefinedAttr(base, name)), nil
}

// lookupAttr resolves an attribute without falling back to item access.
//
// s is the render the lookup belongs to, and is nil when there is none -- a
// constant fold, or an error message being built. It is only used to bind a
// method that needs the render's budget; see statefulMethods.
func lookupAttr(s *State, base value.Value, name string) (value.Value, bool) {
	// __class__ is a real attribute of every Python object, found before
	// any __getattr__ hook, so it resolves even on an undefined.
	if name == "__class__" {
		return classOf(base), true
	}
	if obj, ok := base.Object(); ok {
		if v, ok := obj.GetAttr(name); ok {
			return v, true
		}
	}
	if fn, ok := builtinMethod(s, base, name); ok {
		return fn, true
	}
	// Numbers have a handful of real attributes -- some properties, some
	// methods -- and no table in builtinMethod, which only covers the
	// container types.
	if v, ok := numericAttr(s, base, name); ok {
		return v, true
	}
	return value.Undefined, false
}

func lookupItem(base value.Value, key value.Value) (value.Value, bool) {
	switch base.Kind() {
	case value.KindDict:
		d, _ := base.Dict()
		v, ok, err := d.Get(key)
		if err != nil {
			return value.Undefined, false
		}
		return v, ok
	case value.KindObject:
		if m, ok := base.Interface().(value.Mapping); ok {
			return m.GetItem(key)
		}
	}
	return value.Undefined, false
}

func (ex *exec) evalGetitem(n *ast.Getitem) (value.Value, error) {
	base, err := ex.eval(n.Node)
	if err != nil {
		return value.Undefined, err
	}
	if slice, ok := n.Arg.(*ast.Slice); ok {
		return ex.evalSlice(base, slice)
	}
	key, err := ex.eval(n.Arg)
	if err != nil {
		return value.Undefined, err
	}
	return ex.getItem(base, key)
}

// getItem resolves `base[key]`.
//
// jinja2 tries the subscript first and falls back to an attribute of the same
// name, the mirror of getAttr, which is why `{{ obj["field"] }}` reaches a Go
// struct field.
func (ex *exec) getItem(base, key value.Value) (value.Value, error) {
	if base.IsUndefined() {
		if base.UndefinedBehavior() == value.UndefinedChainable {
			return base, nil
		}
		return value.Undefined, base.UndefinedError()
	}

	switch base.Kind() {
	case value.KindList, value.KindTuple, value.KindString, value.KindBytes:
		return ex.indexSequence(base, key)
	case value.KindDict:
		d, _ := base.Dict()
		v, ok, err := d.Get(key)
		if err != nil {
			// An unhashable key is a TypeError, which getitem
			// catches like any other: `{{ d[[]] }}` is empty.
			return ex.st.Undefined(value.UndefinedElement(base, key)), nil
		}
		if ok {
			return v, nil
		}
	case value.KindObject:
		switch obj := base.Interface().(type) {
		case value.Mapping:
			if v, ok := obj.GetItem(key); ok {
				return v, nil
			}
		case value.Sequence:
			return ex.indexSequence(base, key)
		}
	}

	if key.Kind() == value.KindString {
		if v, ok := lookupAttr(ex.st, base, key.AsString()); ok {
			return v, nil
		}
		return ex.st.Undefined(value.UndefinedAttr(base, key.AsString())), nil
	}
	if i, ok := key.Int64(); ok {
		return ex.st.Undefined(value.UndefinedIndex(base, int(i))), nil
	}
	return ex.st.Undefined(value.UndefinedHint("%s object is not subscriptable",
		value.ObjectTypeRepr(base))), nil
}

// envGetItem is Environment.getitem: the item first, the attribute as a
// fallback when the key is a string, and undefined for anything it cannot
// resolve. Unlike a subscript in a template it never raises -- Python's
// version catches TypeError and LookupError -- which is why a filter's
// `attribute=` never reports a bad lookup as an error.
//
// Reaching the item first is the visible half: `{'items': 1}|attribute('items')`
// is the stored 1, where `x.items` is the method.
//
// s is the render the lookup belongs to, and is nil when there is none. Both
// callers matter: a fold has no undefined class to apply and no budget to bind
// a method against.
func envGetItem(s *State, base, key value.Value) value.Value {
	if v, ok := lookupItem(base, key); ok {
		return v
	}
	if v, ok := constIndex(base, key); ok {
		return v
	}
	if key.Kind() == value.KindString {
		if v, ok := lookupAttr(s, base, key.AsString()); ok {
			return v
		}
		return undefinedFor(s, value.UndefinedAttr(base, key.AsString()))
	}
	// Not a string, so the message says "element" and shows the key as
	// Python would repr it: `True`, not the 1 it indexes with.
	return undefinedFor(s, value.UndefinedHint("%s has no element %s",
		value.ObjectTypeRepr(base), value.Repr(key)))
}

// undefinedFor applies the render's Undefined class, when there is a render.
func undefinedFor(s *State, v value.Value) value.Value {
	if s == nil {
		return v
	}
	return s.Undefined(v)
}

func (ex *exec) indexSequence(base, key value.Value) (value.Value, error) {
	i, ok := key.Int64()
	if !ok {
		if key.Kind() == value.KindString {
			if v, ok := lookupAttr(ex.st, base, key.AsString()); ok {
				return v, nil
			}
			return ex.st.Undefined(value.UndefinedAttr(base, key.AsString())), nil
		}
		// jinja2's getitem catches TypeError and LookupError alike and
		// answers with an undefined, so a key the container cannot take
		// renders as nothing rather than raising -- `{{ xs[none] }}` is
		// empty, not an error.
		return ex.st.Undefined(value.UndefinedElement(base, key)), nil
	}

	switch base.Kind() {
	case value.KindString:
		s, ok := value.StrIndex(base.AsString(), int(i))
		if !ok {
			return ex.st.Undefined(value.UndefinedIndex(base, int(i))), nil
		}
		// Markup.__getitem__ returns Markup, so a slice of escaped text
		// is still escaped.
		if base.IsSafe() {
			return value.Safe(s), nil
		}
		return value.String(s), nil
	case value.KindBytes:
		raw := base.AsString()
		idx := int(i)
		if idx < 0 {
			idx += len(raw)
		}
		if idx < 0 || idx >= len(raw) {
			return ex.st.Undefined(value.UndefinedIndex(base, int(i))), nil
		}
		return value.Int(int64(raw[idx])), nil
	case value.KindList, value.KindTuple:
		s, _ := base.Seq()
		idx := int(i)
		if idx < 0 {
			idx += s.Len()
		}
		if idx < 0 || idx >= s.Len() {
			return ex.st.Undefined(value.UndefinedIndex(base, int(i))), nil
		}
		return s.At(idx), nil
	case value.KindObject:
		seq := base.Interface().(value.Sequence)
		idx := int(i)
		if idx < 0 {
			idx += seq.Len()
		}
		v, ok := seq.GetIndex(idx)
		if !ok {
			return ex.st.Undefined(value.UndefinedIndex(base, int(i))), nil
		}
		return v, nil
	}
	return ex.st.Undefined(value.UndefinedIndex(base, int(i))), nil
}

// sliceSeq selects the elements a slice covers, in order.
func sliceSeq(s *value.Seq, start, stop, step *int) ([]value.Value, error) {
	begin, stride, count, err := value.SliceSpan(s.Len(), start, stop, step)
	if err != nil {
		return nil, err
	}
	items := make([]value.Value, count)
	for i := range count {
		items[i] = s.At(begin + i*stride)
	}
	return items, nil
}

func (ex *exec) evalSlice(base value.Value, n *ast.Slice) (value.Value, error) {
	bound := func(e ast.Expr) (value.Value, error) {
		if e == nil {
			return value.None, nil
		}
		return ex.eval(e)
	}
	// The operands are evaluated here and converted later: Python builds
	// the slice object out of whatever they are -- slice(1.5, None) is a
	// perfectly good object -- and only the subscript that uses it
	// complains. So a base that cannot be sliced at all says so first.
	start, err := bound(n.Start)
	if err != nil {
		return value.Undefined, err
	}
	stop, err := bound(n.Stop)
	if err != nil {
		return value.Undefined, err
	}
	step, err := bound(n.Step)
	if err != nil {
		return value.Undefined, err
	}
	return sliceOf(base, start, stop, step)
}

// sliceIndex converts one slice operand, which only the branches that index
// with it may do -- see evalSlice.
func sliceIndex(v value.Value) (*int, error) {
	if v.IsNone() {
		return nil, nil
	}
	i, ok := v.Int64()
	if !ok {
		// An integer too big for the machine is still an integer, and
		// CPython clamps it: `"abcde"[:2**70]` is the whole string, not
		// an empty one and not an error. Only a value that is not a
		// whole number at all is refused.
		if b, whole := v.BigInt(); whole {
			idx := math.MaxInt
			if b.Sign() < 0 {
				idx = math.MinInt
			}
			return &idx, nil
		}
		return nil, errs.New(errs.TypeError,
			"slice indices must be integers or None or have an __index__ method")
	}
	idx := int(i)
	return &idx, nil
}

// sliceBounds converts all three operands together.
func sliceBounds(start, stop, step value.Value) (a, b, c *int, err error) {
	if a, err = sliceIndex(start); err != nil {
		return nil, nil, nil, err
	}
	if b, err = sliceIndex(stop); err != nil {
		return nil, nil, nil, err
	}
	if c, err = sliceIndex(step); err != nil {
		return nil, nil, nil, err
	}
	return a, b, c, nil
}

// sliceOf applies a resolved slice to a value.
//
// The evaluator and the constant folder both need this, and they used to have
// one implementation each. They drifted: only the evaluator learned that a
// tuple subclass slices as a tuple, so `{{ (x|groupby(k)|first)[::2] }}` was a
// tuple at run time and nothing at all when the whole expression was constant
// and therefore folded.
func sliceOf(base value.Value, startV, stopV, stepV value.Value) (value.Value, error) {
	// Converted per branch rather than up front, so that a base with no
	// subscript at all answers before the operands are judged.
	indices := func() (*int, *int, *int, error) { return sliceBounds(startV, stopV, stepV) }
	switch base.Kind() {
	case value.KindString:
		start, stop, step, err := indices()
		if err != nil {
			return value.Undefined, err
		}
		out, err := value.StrSlice(base.AsString(), start, stop, step)
		if err != nil {
			return value.Undefined, err
		}
		// Slicing a Markup string yields Markup, so a safe value stays
		// safe through `{{ x|safe }}[:10]`.
		if base.IsSafe() {
			return value.Safe(out), nil
		}
		return value.String(out), nil
	case value.KindList, value.KindTuple:
		start, stop, step, err := indices()
		if err != nil {
			return value.Undefined, err
		}
		s, _ := base.Seq()
		items, err := sliceSeq(s, start, stop, step)
		if err != nil {
			return value.Undefined, err
		}
		if base.Kind() == value.KindTuple {
			return value.NewTuple(items...), nil
		}
		return value.NewList(items...), nil
	case value.KindUndefined:
		if base.UndefinedBehavior() == value.UndefinedChainable {
			return base, nil
		}
		return value.Undefined, base.UndefinedError()
	case value.KindObject:
		if sl, ok := base.Interface().(value.Slicer); ok {
			start, stop, step, err := indices()
			if err != nil {
				return value.Undefined, err
			}
			return sl.Slice(start, stop, step)
		}
		// A tuple subclass slices through tuple's own __getitem__, so
		// the result is a tuple. It is asked after Slicer because a
		// type that answers slices for itself is not going through
		// tuple at all. Every other sequence slices to a list.
		if tv, ok := base.Interface().(value.TupleView); ok {
			start, stop, step, err := indices()
			if err != nil {
				return value.Undefined, err
			}
			s, _ := tv.AsTuple().Seq()
			items, err := sliceSeq(s, start, stop, step)
			if err != nil {
				return value.Undefined, err
			}
			return value.NewTuple(items...), nil
		}
		if seq, ok := base.Interface().(value.Sequence); ok {
			start, stop, step, err := indices()
			if err != nil {
				return value.Undefined, err
			}
			begin, stride, count, err := value.SliceSpan(seq.Len(), start, stop, step)
			if err != nil {
				return value.Undefined, err
			}
			items := make([]value.Value, count)
			for i := range count {
				items[i], _ = seq.GetIndex(begin + i*stride)
			}
			return value.NewList(items...), nil
		}
	case value.KindDict:
		// A slice is not hashable, so a mapping rejects it as a key
		// rather than as an unsupported operation.
		return value.Undefined, errs.New(errs.TypeError, "unhashable type: 'slice'")
	}
	return value.Undefined, errs.New(errs.TypeError,
		"'%s' object is not subscriptable", base.TypeName())
}
