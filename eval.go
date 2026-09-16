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
		return value.Add(left, right)
	case ast.OpSub:
		return value.Sub(left, right)
	case ast.OpMul:
		if err := ex.chargeRepeat(left, right); err != nil {
			return value.Undefined, err
		}
		return value.Mul(left, right)
	case ast.OpDiv:
		return value.Div(left, right)
	case ast.OpFloorDiv:
		return value.FloorDiv(left, right)
	case ast.OpMod:
		return value.Mod(left, right)
	case ast.OpPow:
		return value.Pow(left, right)
	}
	return value.Undefined, errs.New(errs.TemplateRuntimeError, "unknown operator %s", n.Op)
}

// chargeRepeat charges what `left * right` is about to allocate, when it is a
// repetition.
//
// value.repeat() caps a single result at 2**31 elements, which for a list of
// values is tens of gigabytes and for a string is two -- far past any budget
// the render has. The cap bounds one expression; the budget bounds the render,
// and it has to be consulted before the allocation rather than after, because
// after it the memory is already gone. `{{ "x" * 1000000000 }}` took the
// process down with an output budget of four kilobytes in force.
//
// Bytes are charged against the output budget and elements against the
// iteration budget, so each lands on the bound that measures the same unit.
func (ex *exec) chargeRepeat(left, right value.Value) error {
	size, isBytes, ok := value.RepeatSize(left, right)
	if !ok || size <= 0 {
		return nil
	}
	if isBytes {
		return ex.st.budget.account(clampToInt(size))
	}
	return ex.st.Step(clampToInt(size))
}

// clampToInt caps a saturated int64 at the widest int the budget can take. The
// budgets are far smaller than either, so clamping only affects a number that
// was already past every bound.
func clampToInt(n int64) int {
	if n > math.MaxInt32 {
		return math.MaxInt32
	}
	return int(n)
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
	var b strings.Builder
	for _, node := range n.Nodes {
		v, err := ex.eval(node)
		if err != nil {
			return value.Undefined, err
		}
		if v.IsUndefined() && v.UndefinedBehavior() == value.UndefinedStrict {
			return value.Undefined, v.UndefinedError()
		}
		text := value.Str(v)
		if ex.autoescape && !v.IsSafe() {
			text = escapeHTML(text)
		}
		b.WriteString(text)
	}
	return markup(b.String(), ex.autoescape), nil
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
		ok, err := compareStep(op.Op, left, right)
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

func compareStep(op string, left, right value.Value) (bool, error) {
	switch op {
	case "eq":
		return value.Equal(left, right), nil
	case "ne":
		return !value.Equal(left, right), nil
	case "lt":
		return value.Ordered("<", left, right)
	case "lteq":
		return value.Ordered("<=", left, right)
	case "gt":
		return value.Ordered(">", left, right)
	case "gteq":
		return value.Ordered(">=", left, right)
	case "in":
		return value.Contains(left, right)
	case "notin":
		ok, err := value.Contains(left, right)
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
			return value.Undefined, err
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

func (ex *exec) indexSequence(base, key value.Value) (value.Value, error) {
	i, ok := key.Int64()
	if !ok {
		if key.Kind() == value.KindString {
			if v, ok := lookupAttr(ex.st, base, key.AsString()); ok {
				return v, nil
			}
			return ex.st.Undefined(value.UndefinedAttr(base, key.AsString())), nil
		}
		return value.Undefined, errs.New(errs.TypeError,
			"%s indices must be integers or slices, not %s", base.TypeName(), key.TypeName())
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

func (ex *exec) evalSlice(base value.Value, n *ast.Slice) (value.Value, error) {
	bound := func(e ast.Expr) (*int, error) {
		if e == nil {
			return nil, nil
		}
		v, err := ex.eval(e)
		if err != nil {
			return nil, err
		}
		if v.IsNone() {
			return nil, nil
		}
		i, ok := v.Int64()
		if !ok {
			return nil, errs.New(errs.TypeError,
				"slice indices must be integers or None, not %s", v.TypeName())
		}
		idx := int(i)
		return &idx, nil
	}

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

	switch base.Kind() {
	case value.KindString:
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
		s, _ := base.Seq()
		idx, err := value.SliceIndices(s.Len(), start, stop, step)
		if err != nil {
			return value.Undefined, err
		}
		items := make([]value.Value, len(idx))
		for i, j := range idx {
			items[i] = s.At(j)
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
			return sl.Slice(start, stop, step)
		}
		if seq, ok := base.Interface().(value.Sequence); ok {
			idx, err := value.SliceIndices(seq.Len(), start, stop, step)
			if err != nil {
				return value.Undefined, err
			}
			items := make([]value.Value, len(idx))
			for i, j := range idx {
				items[i], _ = seq.GetIndex(j)
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
