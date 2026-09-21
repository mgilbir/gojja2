// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"github.com/mgilbir/gojja2/internal/ast"
	"github.com/mgilbir/gojja2/internal/parser"
	"github.com/mgilbir/gojja2/syntax"
	"github.com/mgilbir/gojja2/value"
)

// Syntax returns the template's structure in the normalised form of the
// [syntax] package, for asking questions about it.
//
// This is the template as written, not as compiled: the constant folder has
// run over the engine's own tree, and whether `{{ xs[[]] }}` is a constant
// subscript is a fact about the optimizer rather than about the template. A
// query wants the second.
//
// It returns nil only if the source no longer parses, which cannot happen for a
// template that compiled.
func (t *Template) Syntax() *syntax.Node {
	tree, err := parser.Parse(t.env.syntax, t.env.parseOpts, t.source, t.name)
	if err != nil {
		return nil
	}
	return buildSyntax(tree)
}

type synBuilder struct{}

func buildSyntax(tree *ast.Template) *syntax.Node {
	var b synBuilder
	return b.stmt(tree)
}

func node(kind syntax.Kind, line int) *syntax.Node {
	return &syntax.Node{Kind: kind, Line: line}
}

func (b *synBuilder) edge(n *syntax.Node, role syntax.Role, child *syntax.Node) {
	if child == nil {
		return
	}
	n.Edges = append(n.Edges, syntax.Edge{Role: role, Node: child})
}

func (b *synBuilder) edges(n *syntax.Node, role syntax.Role, list []ast.Expr) {
	for _, e := range list {
		b.edge(n, role, b.expr(e))
	}
}

func (b *synBuilder) body(n *syntax.Node, role syntax.Role, list []ast.Stmt) {
	for _, s := range list {
		b.edge(n, role, b.stmt(s))
	}
}

func set(n *syntax.Node, key string, v any) {
	if n.Attrs == nil {
		n.Attrs = map[string]any{}
	}
	n.Attrs[key] = v
}

func (b *synBuilder) args(n *syntax.Node, a *ast.Args) {
	b.edges(n, syntax.RoleArg, a.Args)
	for _, kw := range a.Kwargs {
		k := node(syntax.KindKeyword, kw.Line())
		set(k, "name", kw.Key)
		b.edge(k, syntax.RoleValue, b.expr(kw.Value))
		b.edge(n, syntax.RoleKwarg, k)
	}
	b.edge(n, syntax.RoleDynArgs, b.expr(a.DynArgs))
	b.edge(n, syntax.RoleDynKw, b.expr(a.DynKwargs))
}

func (b *synBuilder) stmt(s ast.Stmt) *syntax.Node {
	switch n := s.(type) {
	case *ast.Template:
		out := node(syntax.KindTemplate, n.Line())
		b.body(out, syntax.RoleBody, n.Body)
		return out

	case *ast.Output:
		out := node(syntax.KindOutput, n.Line())
		b.edges(out, syntax.RoleValue, n.Nodes)
		return out

	case *ast.If:
		out := node(syntax.KindIf, n.Line())
		b.edge(out, syntax.RoleTest, b.expr(n.Test))
		b.body(out, syntax.RoleBody, n.Body)
		// jinja2 hangs elif branches off the outermost if as further If
		// nodes; so does gojja2. They stay nested rather than being
		// flattened, because that is what both trees say.
		for _, elif := range n.Elif {
			b.edge(out, syntax.RoleElse, b.stmt(elif))
		}
		b.body(out, syntax.RoleElse, n.Else)
		return out

	case *ast.For:
		out := node(syntax.KindFor, n.Line())
		set(out, "recursive", n.Recursive)
		b.edge(out, syntax.RoleTarget, b.expr(n.Target))
		b.edge(out, syntax.RoleIter, b.expr(n.Iter))
		b.edge(out, syntax.RoleTest, b.expr(n.Test))
		b.body(out, syntax.RoleBody, n.Body)
		b.body(out, syntax.RoleElse, n.Else)
		return out

	case *ast.Assign:
		out := node(syntax.KindAssign, n.Line())
		b.edge(out, syntax.RoleTarget, b.expr(n.Target))
		b.edge(out, syntax.RoleValue, b.expr(n.Node))
		return out

	case *ast.AssignBlock:
		out := node(syntax.KindAssignBlk, n.Line())
		b.edge(out, syntax.RoleTarget, b.expr(n.Target))
		b.edge(out, syntax.RoleFilter, b.expr(n.Filter))
		b.body(out, syntax.RoleBody, n.Body)
		return out

	case *ast.Macro:
		out := node(syntax.KindMacro, n.Line())
		set(out, "name", n.Name)
		for _, p := range n.Args {
			b.edge(out, syntax.RoleParam, b.expr(p))
		}
		b.edges(out, syntax.RoleDefault, n.Defaults)
		b.body(out, syntax.RoleBody, n.Body)
		return out

	case *ast.CallBlock:
		out := node(syntax.KindCallBlock, n.Line())
		b.edge(out, syntax.RoleCallee, b.expr(n.Call))
		for _, p := range n.Args {
			b.edge(out, syntax.RoleParam, b.expr(p))
		}
		b.edges(out, syntax.RoleDefault, n.Defaults)
		b.body(out, syntax.RoleBody, n.Body)
		return out

	case *ast.FilterBlock:
		out := node(syntax.KindFilterBlk, n.Line())
		b.edge(out, syntax.RoleFilter, b.expr(n.Filter))
		b.body(out, syntax.RoleBody, n.Body)
		return out

	case *ast.With:
		out := node(syntax.KindWith, n.Line())
		for i := range n.Targets {
			b.edge(out, syntax.RoleTarget, b.expr(n.Targets[i]))
			if i < len(n.Values) {
				b.edge(out, syntax.RoleValue, b.expr(n.Values[i]))
			}
		}
		b.body(out, syntax.RoleBody, n.Body)
		return out

	case *ast.Block:
		out := node(syntax.KindBlock, n.Line())
		set(out, "name", n.Name)
		set(out, "scoped", n.Scoped)
		set(out, "required", n.Required)
		b.body(out, syntax.RoleBody, n.Body)
		return out

	case *ast.Extends:
		out := node(syntax.KindExtends, n.Line())
		b.edge(out, syntax.RoleTemplate, b.expr(n.Template))
		return out

	case *ast.Include:
		out := node(syntax.KindInclude, n.Line())
		set(out, "with_context", n.WithContext)
		set(out, "ignore_missing", n.IgnoreMissing)
		b.edge(out, syntax.RoleTemplate, b.expr(n.Template))
		return out

	case *ast.Import:
		out := node(syntax.KindImport, n.Line())
		set(out, "target", n.Target)
		set(out, "with_context", n.WithContext)
		b.edge(out, syntax.RoleTemplate, b.expr(n.Template))
		return out

	case *ast.FromImport:
		out := node(syntax.KindFromImport, n.Line())
		set(out, "with_context", n.WithContext)
		names := make([][2]string, 0, len(n.Names))
		for _, nm := range n.Names {
			names = append(names, [2]string{nm.Name, nm.Alias})
		}
		set(out, "names", names)
		b.edge(out, syntax.RoleTemplate, b.expr(n.Template))
		return out

	case *ast.ExprStmt:
		out := node(syntax.KindExprStmt, n.Line())
		b.edge(out, syntax.RoleValue, b.expr(n.Node))
		return out

	case *ast.Scope:
		out := node(syntax.KindScope, n.Line())
		b.body(out, syntax.RoleBody, n.Body)
		return out

	case *ast.AutoescapeBlock:
		out := node(syntax.KindAutoescape, n.Line())
		b.edge(out, syntax.RoleValue, b.expr(n.Value))
		b.body(out, syntax.RoleBody, n.Body)
		// Wrapped in a scope, which gojja2's own tree leaves implicit and
		// jinja2's states. The body *is* a scope -- a `{% set %}` inside an
		// autoescape block does not escape it -- so the vocabulary says so
		// rather than making every query know the rule.
		outer := node(syntax.KindScope, n.Line())
		b.edge(outer, syntax.RoleBody, out)
		return outer

	case *ast.Break:
		return node(syntax.KindBreak, n.Line())

	case *ast.Continue:
		return node(syntax.KindContinue, n.Line())
	}
	return nil
}

func (b *synBuilder) expr(e ast.Expr) *syntax.Node {
	if e == nil {
		return nil
	}
	switch n := e.(type) {
	case *ast.Const:
		out := node(syntax.KindConst, n.Line())
		set(out, "value", constAttr(n.Value))
		return out

	case *ast.TemplateData:
		out := node(syntax.KindText, n.Line())
		set(out, "value", n.Data)
		return out

	case *ast.Name:
		out := node(syntax.KindName, n.Line())
		set(out, "name", n.Name)
		set(out, "store", n.Store)
		return out

	case *ast.NSRef:
		out := node(syntax.KindNSRef, n.Line())
		set(out, "name", n.Name)
		set(out, "attr", n.Attr)
		return out

	case *ast.Tuple:
		out := node(syntax.KindTuple, n.Line())
		set(out, "store", n.Store)
		b.edges(out, syntax.RoleItem, n.Items)
		return out

	case *ast.List:
		out := node(syntax.KindList, n.Line())
		b.edges(out, syntax.RoleItem, n.Items)
		return out

	case *ast.Dict:
		out := node(syntax.KindDict, n.Line())
		for _, p := range n.Items {
			b.edge(out, syntax.RoleItem, b.expr(p))
		}
		return out

	case *ast.Pair:
		out := node(syntax.KindPair, n.Line())
		b.edge(out, syntax.RoleKey, b.expr(n.Key))
		b.edge(out, syntax.RoleValue, b.expr(n.Value))
		return out

	case *ast.Keyword:
		out := node(syntax.KindKeyword, n.Line())
		set(out, "name", n.Key)
		b.edge(out, syntax.RoleValue, b.expr(n.Value))
		return out

	case *ast.CondExpr:
		out := node(syntax.KindCond, n.Line())
		b.edge(out, syntax.RoleTest, b.expr(n.Test))
		b.edge(out, syntax.RoleThen, b.expr(n.True))
		b.edge(out, syntax.RoleOther, b.expr(n.False))
		return out

	case *ast.BinOp:
		// jinja2 has a class per operator and this has one node with the
		// operator on it. The spelling is the operator as a template writes
		// it, which is the one form both sides can agree on.
		out := node(syntax.KindBinOp, n.Line())
		set(out, "op", n.Op.String())
		b.edge(out, syntax.RoleLeft, b.expr(n.Left))
		b.edge(out, syntax.RoleRight, b.expr(n.Right))
		return out

	case *ast.UnaryOp:
		out := node(syntax.KindUnaryOp, n.Line())
		set(out, "op", n.Op.String())
		b.edge(out, syntax.RoleOperand, b.expr(n.Node))
		return out

	case *ast.Concat:
		out := node(syntax.KindConcat, n.Line())
		b.edges(out, syntax.RoleItem, n.Nodes)
		return out

	case *ast.Compare:
		out := node(syntax.KindCompare, n.Line())
		b.edge(out, syntax.RoleLeft, b.expr(n.Expr))
		for _, op := range n.Ops {
			b.edge(out, syntax.RoleOperand, b.expr(op))
		}
		return out

	case *ast.Operand:
		out := node(syntax.KindOperand, n.Line())
		set(out, "op", n.Op)
		b.edge(out, syntax.RoleValue, b.expr(n.Expr))
		return out

	case *ast.Getattr:
		out := node(syntax.KindGetattr, n.Line())
		set(out, "attr", n.Attr)
		b.edge(out, syntax.RoleSubject, b.expr(n.Node))
		return out

	case *ast.Getitem:
		out := node(syntax.KindGetitem, n.Line())
		b.edge(out, syntax.RoleSubject, b.expr(n.Node))
		b.edge(out, syntax.RoleIndex, b.expr(n.Arg))
		return out

	case *ast.Slice:
		out := node(syntax.KindSlice, n.Line())
		b.edge(out, syntax.RoleStart, b.expr(n.Start))
		b.edge(out, syntax.RoleStop, b.expr(n.Stop))
		b.edge(out, syntax.RoleStep, b.expr(n.Step))
		return out

	case *ast.Call:
		out := node(syntax.KindCall, n.Line())
		b.edge(out, syntax.RoleCallee, b.expr(n.Node))
		b.args(out, &n.Args)
		return out

	case *ast.Filter:
		out := node(syntax.KindFilter, n.Line())
		set(out, "name", n.Name)
		b.edge(out, syntax.RoleSubject, b.expr(n.Node))
		b.args(out, &n.Args)
		return out

	case *ast.Test:
		out := node(syntax.KindTest, n.Line())
		set(out, "name", n.Name)
		b.edge(out, syntax.RoleSubject, b.expr(n.Node))
		b.args(out, &n.Args)
		return out
	}
	return nil
}

// constAttr renders a literal in a form both encoders produce identically.
// Only the kinds a template can write appear here; anything else would be a
// constant the parser invented, which it does not.
func constAttr(v value.Value) any {
	switch v.Kind() {
	case value.KindNone, value.KindUndefined:
		return nil
	case value.KindBool:
		return v.AsBool()
	case value.KindInt:
		if n, ok := v.Int64(); ok {
			return n
		}
		// A literal too wide for int64 goes as its decimal digits, which
		// is what the other side writes too: neither JSON nor a float can
		// carry it without changing it.
		if b, ok := v.BigInt(); ok {
			return b.String()
		}
		return value.Repr(v)
	case value.KindFloat:
		// As digits rather than as a JSON number: encoding/json writes
		// float64(1) as `1` where Python writes `1.0`, and that difference
		// belongs to the encoders rather than to the template. Repr is the
		// spelling gojja2 already reproduces exactly.
		return map[string]any{"f": value.Repr(v)}
	case value.KindString:
		return v.AsString()
	}
	return value.Repr(v)
}
