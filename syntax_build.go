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
// [syntax] package, together with what is known about its names, for asking
// questions about it.
//
// This is the template as written, not as compiled: the constant folder has
// run over the engine's own tree, and whether `{{ xs[[]] }}` is a constant
// subscript is a fact about the optimizer rather than about the template. A
// query wants the second.
//
// Each call builds a fresh tree, and the [syntax.Info] it comes with is keyed
// by that tree's nodes -- which is why the two are returned together rather
// than separately.
//
// It returns nil only if the source no longer parses, which cannot happen for a
// template that compiled.
func (t *Template) Syntax() *syntax.Tree {
	parsed, err := parser.Parse(t.env.syntax, t.env.parseOpts, t.source, t.name)
	if err != nil {
		return nil
	}
	b := &synBuilder{
		tmpl: t,
		info: &syntax.Info{
			Defs:    map[*syntax.Node]*syntax.Symbol{},
			Uses:    map[*syntax.Node]*syntax.Symbol{},
			Scopes:  map[*syntax.Node][]*syntax.Symbol{},
			Context: map[string]*syntax.Symbol{},
		},
	}
	return &syntax.Tree{Root: b.stmt(parsed), Info: b.info}
}

// synFrame is one scope while the tree is being built.
type synFrame struct {
	node  *syntax.Node
	owned map[string]*syntax.Symbol
	// refs is every name mentioned at this frame's own level, which is what
	// decides whether a nested frame's store makes a new binding or aliases
	// this one.
	refs map[string]bool
}

type synBuilder struct {
	tmpl   *Template
	info   *syntax.Info
	frames []*synFrame
}

// pushFrame opens a scope. Names the construct itself binds -- a loop target, a
// macro parameter -- are declared by the caller before the body is walked;
// declareBody then adds the names the body claims.
func (b *synBuilder) pushFrame(scope *syntax.Node) {
	b.frames = append(b.frames, &synFrame{node: scope, owned: map[string]*syntax.Symbol{}})
	b.info.Scopes[scope] = nil
}

func (b *synBuilder) popFrame() { b.frames = b.frames[:len(b.frames)-1] }

// declareBody claims the names jinja2 says this frame owns.
//
// frameLocals is the one implementation of that rule in the codebase, and it is
// graded by the conformance suite: a frame owns a name whose first mention at
// its own level is a write, which is why `{{ x }}{% set x = 1 %}` reads the
// caller's x and the same pair with the read inside a loop does not.
func (b *synBuilder) declareBody(body []ast.Stmt) {
	names := frameLocals(body)
	f := b.frames[len(b.frames)-1]
	f.refs = names.refs
	for _, name := range names.owns {
		// jinja2's Symbols.store asks the enclosing symbol table for a
		// reference before settling on a new binding, so a name an
		// enclosing frame binds *or merely mentions* is the same storage
		// seen from in here rather than a fresh one. A root-level
		// `{{ m }}` anywhere in the template, even inside a branch that
		// never runs, is enough -- while a read inside a nested frame is
		// not, because that is a different symbol table. This is the same
		// rule declareFrameLocals applies at render time.
		if s := b.enclosingSymbol(name); s != nil {
			f.owned[name] = s
			continue
		}
		b.declare(name, syntax.SymLocal)
	}
}

// enclosingSymbol is the binding a nested frame's store aliases, or nil when
// nothing above it binds or mentions the name.
func (b *synBuilder) enclosingSymbol(name string) *syntax.Symbol {
	for i := len(b.frames) - 2; i >= 0; i-- {
		if s, ok := b.frames[i].owned[name]; ok {
			return s
		}
	}
	for i := len(b.frames) - 2; i >= 0; i-- {
		if b.frames[i].refs[name] {
			return b.resolveFrom(i, name)
		}
	}
	return nil
}

// resolveFrom is resolve as seen from frame i, for the aliasing rule above.
func (b *synBuilder) resolveFrom(i int, name string) *syntax.Symbol {
	for ; i >= 0; i-- {
		if s, ok := b.frames[i].owned[name]; ok {
			return s
		}
	}
	return b.contextSymbol(name)
}

// declare binds a name in the innermost frame, or returns the binding it
// already has there -- a loop target the body also assigns is one symbol, not
// two.
func (b *synBuilder) declare(name string, kind syntax.SymbolKind) *syntax.Symbol {
	f := b.frames[len(b.frames)-1]
	if s, ok := f.owned[name]; ok {
		return s
	}
	s := &syntax.Symbol{Name: name, Kind: kind, Scope: f.node}
	f.owned[name] = s
	b.info.Scopes[f.node] = append(b.info.Scopes[f.node], s)
	return s
}

// resolve is what an occurrence of name refers to: the innermost frame that
// owns it, or something from outside the template.
func (b *synBuilder) resolve(name string) *syntax.Symbol {
	for i := len(b.frames) - 1; i >= 0; i-- {
		if s, ok := b.frames[i].owned[name]; ok {
			return s
		}
	}
	return b.contextSymbol(name)
}

// contextSymbol is the one symbol standing for a name that comes from outside
// the template.
func (b *synBuilder) contextSymbol(name string) *syntax.Symbol {
	if s, ok := b.info.Context[name]; ok {
		return s
	}
	kind := syntax.SymContext
	if _, global := b.tmpl.env.globals[name]; global {
		// Supplied by the environment, so the template is not asking the
		// caller for it.
		kind = syntax.SymGlobal
	}
	s := &syntax.Symbol{Name: name, Kind: kind}
	b.info.Context[name] = s
	return s
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
		b.pushFrame(out)
		b.declareBody(n.Body)
		b.body(out, syntax.RoleBody, n.Body)
		b.popFrame()
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
		// The sequence is read in the enclosing scope; everything else
		// belongs to the loop's own. The children are built in that order
		// and attached afterwards, because the edge order is the canonical
		// form and must not follow the evaluation order.
		iter := b.expr(n.Iter)
		b.pushFrame(out)
		b.declareTargets(n.Target, syntax.SymTarget)
		// `loop` is the loop's, not the caller's.
		b.declare("loop", syntax.SymProvided)
		b.declareBody(n.Body)
		target := b.expr(n.Target)
		test := b.expr(n.Test)
		b.edge(out, syntax.RoleTarget, target)
		b.edge(out, syntax.RoleIter, iter)
		b.edge(out, syntax.RoleTest, test)
		b.body(out, syntax.RoleBody, n.Body)
		b.body(out, syntax.RoleElse, n.Else)
		b.popFrame()
		return out

	case *ast.Assign:
		out := node(syntax.KindAssign, n.Line())
		b.edge(out, syntax.RoleTarget, b.expr(n.Target))
		b.edge(out, syntax.RoleValue, b.expr(n.Node))
		return out

	case *ast.AssignBlock:
		out := node(syntax.KindAssignBlk, n.Line())
		// The target binds in the enclosing scope; the captured body is a
		// scope of its own.
		b.edge(out, syntax.RoleTarget, b.expr(n.Target))
		b.edge(out, syntax.RoleFilter, b.expr(n.Filter))
		b.pushFrame(out)
		b.declareBody(n.Body)
		b.body(out, syntax.RoleBody, n.Body)
		b.popFrame()
		return out

	case *ast.Macro:
		out := node(syntax.KindMacro, n.Line())
		set(out, "name", n.Name)
		b.pushFrame(out)
		for _, p := range n.Args {
			b.declare(p.Name, syntax.SymParam)
		}
		// Supplied by the call rather than by the caller's variables.
		for _, given := range []string{"varargs", "kwargs", "caller"} {
			b.declare(given, syntax.SymProvided)
		}
		b.declareBody(n.Body)
		for _, p := range n.Args {
			b.edge(out, syntax.RoleParam, b.expr(p))
		}
		b.edges(out, syntax.RoleDefault, n.Defaults)
		b.body(out, syntax.RoleBody, n.Body)
		b.popFrame()
		return out

	case *ast.CallBlock:
		out := node(syntax.KindCallBlock, n.Line())
		callee := b.expr(n.Call)
		b.pushFrame(out)
		for _, p := range n.Args {
			b.declare(p.Name, syntax.SymParam)
		}
		b.declareBody(n.Body)
		b.edge(out, syntax.RoleCallee, callee)
		for _, p := range n.Args {
			b.edge(out, syntax.RoleParam, b.expr(p))
		}
		b.edges(out, syntax.RoleDefault, n.Defaults)
		b.body(out, syntax.RoleBody, n.Body)
		b.popFrame()
		return out

	case *ast.FilterBlock:
		out := node(syntax.KindFilterBlk, n.Line())
		b.edge(out, syntax.RoleFilter, b.expr(n.Filter))
		b.pushFrame(out)
		b.declareBody(n.Body)
		b.body(out, syntax.RoleBody, n.Body)
		b.popFrame()
		return out

	case *ast.With:
		out := node(syntax.KindWith, n.Line())
		// Values are read outside the block; targets bind inside it.
		values := make([]*syntax.Node, len(n.Values))
		for i, v := range n.Values {
			values[i] = b.expr(v)
		}
		b.pushFrame(out)
		for _, t := range n.Targets {
			b.declareTargets(t, syntax.SymTarget)
		}
		b.declareBody(n.Body)
		for i := range n.Targets {
			b.edge(out, syntax.RoleTarget, b.expr(n.Targets[i]))
			if i < len(values) {
				b.edge(out, syntax.RoleValue, values[i])
			}
		}
		b.body(out, syntax.RoleBody, n.Body)
		b.popFrame()
		return out

	case *ast.Block:
		out := node(syntax.KindBlock, n.Line())
		set(out, "name", n.Name)
		set(out, "scoped", n.Scoped)
		set(out, "required", n.Required)
		b.pushFrame(out)
		b.declareBody(n.Body)
		b.body(out, syntax.RoleBody, n.Body)
		b.popFrame()
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
		b.pushFrame(out)
		b.declareBody(n.Body)
		b.body(out, syntax.RoleBody, n.Body)
		b.popFrame()
		return out

	case *ast.AutoescapeBlock:
		// Wrapped in a scope, which gojja2's own tree leaves implicit and
		// jinja2's states. The body *is* a scope -- a `{% set %}` inside an
		// autoescape block does not escape it -- so the vocabulary says so
		// rather than making every query know the rule.
		//
		// jinja2 opens a frame on the wrapper as well as on the block, and
		// the wrapper's is empty because the only thing at its level is the
		// block itself. Both are recorded, because a scope that exists on
		// one side and not the other is a disagreement about the template.
		// The wrapper is the frame that owns what the body assigns, which
		// is where jinja2 puts it: its Scope visits the block's statements,
		// and the block itself then aliases them. gojja2's evaluator nests
		// the two the other way round with the same effect, and the
		// vocabulary records the specification's shape.
		outer := node(syntax.KindScope, n.Line())
		b.pushFrame(outer)
		b.declareBody(n.Body)

		out := node(syntax.KindAutoescape, n.Line())
		b.edge(out, syntax.RoleValue, b.expr(n.Value))
		b.pushFrame(out)
		b.declareBody(n.Body)
		b.body(out, syntax.RoleBody, n.Body)
		b.popFrame()

		b.edge(outer, syntax.RoleBody, out)
		b.popFrame()
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
		sym := b.resolve(n.Name)
		if n.Store {
			b.info.Defs[out] = sym
		} else {
			b.info.Uses[out] = sym
		}
		return out

	case *ast.NSRef:
		// `{% set ns.x = 1 %}` mutates the namespace rather than rebinding
		// it, so this reads ns and writes through it. It is recorded as a
		// use: the binding it touches is the namespace's field, which is
		// not a name in any scope.
		out := node(syntax.KindNSRef, n.Line())
		set(out, "name", n.Name)
		set(out, "attr", n.Attr)
		b.info.Uses[out] = b.resolve(n.Name)
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

// declareTargets binds the names an assignment target introduces, which may be
// a tuple when a loop unpacks.
func (b *synBuilder) declareTargets(target ast.Expr, kind syntax.SymbolKind) {
	switch t := target.(type) {
	case *ast.Name:
		b.declare(t.Name, kind)
	case *ast.Tuple:
		for _, item := range t.Items {
			b.declareTargets(item, kind)
		}
	case *ast.List:
		for _, item := range t.Items {
			b.declareTargets(item, kind)
		}
	}
}
