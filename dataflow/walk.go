// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package dataflow

import "github.com/mgilbir/gojja2/syntax"

// The walk reads the tree and nothing else. Where a value is *consumed*
// decides what it does, and the edge label says which: a child under
// [syntax.RoleTest] is steering, one under [syntax.RoleValue] of an output is
// printed. There is no table of "which field of which node is a condition"
// below, because the tree already carries that.

// expr answers what a value derives from, and applies Steers itself at the one
// steering position that lives inside an expression: a conditional's test.
//
// Everything else is data. `{{ x|length }}` and `{{ x is defined }}` both put a
// value derived from x into the document, so both are printed; Steers is for
// *choosing* between alternatives, which is what `{% if %}`, a loop's length
// and `a if c else b` do. The line has to be drawn somewhere, and drawing it at
// steering positions is the one place two implementations can agree on it
// without comparing notes.
func (a *analyzer) expr(n *syntax.Node) symset {
	out := symset{}
	if n == nil {
		return out
	}
	switch n.Kind {
	case syntax.KindName:
		if s := a.tree.Info.Uses[n]; s != nil {
			out[s] = true
			if _, ns := a.namespaces[s]; ns {
				// Reached here rather than through a field access,
				// so another name now refers to the same object
				// and every field is in play. See namespace.go.
				a.aliased[s] = true
			}
		}
		return out

	case syntax.KindNSRef:
		// A namespace read. Following the field is a later layer; until
		// then the honest answer is that anything could be in it.
		if s := a.tree.Info.Uses[n]; s != nil {
			out[s] = true
			a.effects[s] |= Opaque
		}
		return out

	case syntax.KindConst, syntax.KindText:
		return out

	case syntax.KindCond:
		a.apply(a.expr(n.Child(syntax.RoleTest)), Steers)
		out.add(a.expr(n.Child(syntax.RoleThen)))
		out.add(a.expr(n.Child(syntax.RoleOther)))
		return out

	case syntax.KindGetattr:
		if ns := a.namespaceOf(n.Child(syntax.RoleSubject)); ns != nil {
			if f := a.namespaceField(ns, n.Attr("attr")); f != nil {
				out[f] = true
				return out
			}
		}
		out.add(a.expr(n.Child(syntax.RoleSubject)))
		return out

	case syntax.KindGetitem:
		// Subscripting something that cannot be subscripted raises, and so
		// does a key of the wrong type.
		defer func() { a.apply(out, Required) }()
		// `data[key]` reads out of data, whatever key turns out to be, so
		// the result derives from data and the answer to "can data reach
		// the output" is a plain yes. Which *part* of data is read is not
		// a static fact, but that is a different question, and answering
		// it with Opaque used to weaken an answer that was never in doubt.
		out.add(a.expr(n.Child(syntax.RoleSubject)))
		if idx := n.Child(syntax.RoleIndex); idx != nil {
			// The key chooses among the container's values; it is not
			// one of them. That is steering, the same thing a
			// condition does, so it does not join the result. It can
			// also stop the render -- an unhashable key, or one of a
			// type the container cannot take.
			a.apply(a.expr(idx), Steers|Required)
		}
		return out

	case syntax.KindFilter:
		defer func() { a.apply(out, Required) }()
		out.add(a.expr(n.Child(syntax.RoleSubject)))
		if n.Attr("name") == "attr" {
			// `obj|attr(name)` is a computed lookup like `obj[name]`:
			// the result comes out of obj, and name picks which part.
			for _, arg := range n.Children(syntax.RoleArg) {
				// A name that is not a string stops the render.
				a.apply(a.expr(arg), Steers|Required)
			}
			return out
		}
		out.add(a.callArgs(n))
		return out

	case syntax.KindPair:
		// A mapping key that cannot be hashed stops the render -- and it
		// is still part of the mapping, so printing the mapping prints it.
		key := a.expr(n.Child(syntax.RoleKey))
		a.apply(key, Required)
		out.add(key)
		out.add(a.expr(n.Child(syntax.RoleValue)))
		return out

	case syntax.KindCall:
		// Arity, type, a callee that is not callable.
		defer func() { a.apply(out, Required) }()
		out.add(a.callArgs(n))
		callee := n.Child(syntax.RoleCallee)
		if callee != nil && callee.Kind == syntax.KindName {
			sym := a.tree.Info.Uses[callee]
			if _, known := a.macroParams[sym]; known {
				out.add(a.callMacro(sym, n))
				return out
			}
			if callee.Attr("name") == "namespace" {
				a.taint(out)
				return out
			}
		}
		// A call whose body this cannot see: a method on a value, a
		// global, a macro held in a variable.
		//
		// Giving up here was costing more than it bought. `{{ msg.strip() }}`
		// reads out of msg and puts the result in the document, which is
		// not in doubt, and answering "might" about it was the single
		// largest source of unknowns left -- every one of them on real
		// chat templates.
		//
		// What the giving-up was actually for is mutation. `{{ l.append(x) }}`
		// renders nothing and leaves x inside l, so a later `{{ l }}`
		// prints x, and a rule that only followed results would miss it
		// and report a negative that is not true. That is a dependency,
		// not a reason to stop: the receiver may now hold the arguments,
		// so it gets an edge from them.
		//
		// The cost is over-reporting. `{{ s.split(sep) }}` does not
		// mutate s, and sep now inherits whatever s does. That is the
		// safe direction -- a variable reported as printed when it is not
		// is a worse answer, not a wrong one.
		recv := a.expr(callee)
		if callee != nil && callee.Kind == syntax.KindGetattr {
			recv = a.expr(callee.Child(syntax.RoleSubject))
		}
		for s := range recv {
			a.depend(s, out)
		}
		out.add(recv)
		return out
	}

	// Everything else -- operators, comparisons, tests, attribute access,
	// containers, slices, concatenation -- is ordinary data flow: the result
	// derives from every operand. A node kind added later lands here, which
	// is the sound default.
	for _, e := range n.Edges {
		out.add(a.expr(e.Node))
	}
	if canRaise(n.Kind) {
		a.apply(out, Required)
	}
	return out
}

// callArgs is every argument of a call, filter or test as one derived set.
func (a *analyzer) callArgs(n *syntax.Node) symset {
	out := symset{}
	for _, role := range []syntax.Role{syntax.RoleArg, syntax.RoleKwarg,
		syntax.RoleDynArgs, syntax.RoleDynKw} {
		for _, c := range n.Children(role) {
			out.add(a.expr(c))
		}
	}
	return out
}

// callMacro binds a call's arguments to the macro's parameters and returns what
// the macro's body would print.
func (a *analyzer) callMacro(sym *syntax.Symbol, call *syntax.Node) symset {
	if a.inMacro[sym] {
		// Recursive. The fixpoint already carries effects around the
		// cycle, and re-entering would not terminate.
		return symset{}
	}
	a.inMacro[sym] = true
	defer delete(a.inMacro, sym)

	params := a.macroParams[sym]
	args := call.Children(syntax.RoleArg)
	for i, p := range params {
		srcs := symset{}
		if i < len(args) {
			srcs.add(a.expr(args[i]))
		}
		for _, kw := range call.Children(syntax.RoleKwarg) {
			if kw.Attr("name") == p.Name {
				srcs.add(a.expr(kw.Child(syntax.RoleValue)))
			}
		}
		a.depend(p, srcs)
	}
	return a.macroOut[sym]
}

// captureBody is what a block set's or filter block's body would have printed.
func (a *analyzer) captureBody(n *syntax.Node) symset {
	a.capture = append(a.capture, symset{})
	a.stmts(n)
	out := a.capture[len(a.capture)-1]
	a.capture = a.capture[:len(a.capture)-1]
	return out
}

func (a *analyzer) stmts(n *syntax.Node) {
	for _, c := range n.Children(syntax.RoleBody) {
		a.stmt(c)
	}
}

// scopeSymbol looks a name up the chain of enclosing scopes, which is how a
// macro is found: its name is an attribute of the macro node rather than a name
// node, so nothing in Defs points at it.
func (a *analyzer) scopeSymbol(name string) *syntax.Symbol {
	for i := len(a.scopes) - 1; i >= 0; i-- {
		for _, s := range a.tree.Info.Scopes[a.scopes[i]] {
			if s.Name == name {
				return s
			}
		}
	}
	return nil
}

func (a *analyzer) enterScope(n *syntax.Node) bool {
	if _, ok := a.tree.Info.Scopes[n]; !ok {
		return false
	}
	a.scopes = append(a.scopes, n)
	return true
}

func (a *analyzer) leaveScope(entered bool) {
	if entered {
		a.scopes = a.scopes[:len(a.scopes)-1]
	}
}

func (a *analyzer) stmt(n *syntax.Node) {
	if n == nil {
		return
	}
	entered := a.enterScope(n)
	defer a.leaveScope(entered)

	switch n.Kind {
	case syntax.KindTemplate, syntax.KindScope, syntax.KindBlock:
		a.stmts(n)

	case syntax.KindOutput:
		for _, c := range n.Children(syntax.RoleValue) {
			a.emit(a.expr(c))
		}

	case syntax.KindIf:
		a.apply(a.expr(n.Child(syntax.RoleTest)), Steers)
		a.stmts(n)
		for _, c := range n.Children(syntax.RoleElse) {
			a.stmt(c)
		}

	case syntax.KindFor:
		// The sequence's length decides how many times the body runs, so
		// it can change the output without appearing in it; its elements
		// reach the output through the target.
		srcs := a.expr(n.Child(syntax.RoleIter))
		// Iterating something that is not iterable raises.
		a.apply(srcs, Steers|Required)
		a.bind(n.Child(syntax.RoleTarget), srcs)
		// `loop` reports on the sequence, so it derives from it too.
		if loop := a.scopeSymbol("loop"); loop != nil {
			a.depend(loop, srcs)
		}
		a.apply(a.expr(n.Child(syntax.RoleTest)), Steers)
		a.stmts(n)
		for _, c := range n.Children(syntax.RoleElse) {
			a.stmt(c)
		}

	case syntax.KindAssign:
		target, value := n.Child(syntax.RoleTarget), n.Child(syntax.RoleValue)
		if isNamespaceCall(value) && target != nil && target.Kind == syntax.KindName {
			a.declareNamespace(a.tree.Info.Symbol(target), value)
			break
		}
		a.bind(target, a.expr(value))

	case syntax.KindAssignBlk:
		srcs := a.captureBody(n)
		srcs.add(a.expr(n.Child(syntax.RoleFilter)))
		a.bind(n.Child(syntax.RoleTarget), srcs)

	case syntax.KindFilterBlk:
		srcs := a.captureBody(n)
		srcs.add(a.expr(n.Child(syntax.RoleFilter)))
		a.emit(srcs)

	case syntax.KindMacro:
		sym := a.scopeSymbol(n.Attr("name"))
		var params []*syntax.Symbol
		for _, s := range a.tree.Info.Scopes[n] {
			if s.Kind == syntax.SymParam {
				params = append(params, s)
			}
		}
		if sym != nil {
			a.macroParams[sym] = params
		}
		// Defaults align with the tail of the parameters, and a parameter
		// holding its default is how an omitted argument reaches the body.
		defaults := n.Children(syntax.RoleDefault)
		if off := len(params) - len(defaults); off >= 0 {
			for i, d := range defaults {
				a.depend(params[off+i], a.expr(d))
			}
		} else {
			for _, d := range defaults {
				a.expr(d)
			}
		}
		out := a.captureBody(n)
		if sym != nil {
			a.macroOut[sym] = out
		}

	case syntax.KindCallBlock:
		a.stmts(n)
		a.emit(a.expr(n.Child(syntax.RoleCallee)))

	case syntax.KindWith:
		values := n.Children(syntax.RoleValue)
		for i, target := range n.Children(syntax.RoleTarget) {
			if i < len(values) {
				a.bind(target, a.expr(values[i]))
			}
		}
		a.stmts(n)

	case syntax.KindAutoescape:
		// Whether output is escaped changes what is rendered.
		a.apply(a.expr(n.Child(syntax.RoleValue)), Steers)
		a.stmts(n)

	case syntax.KindExtends, syntax.KindInclude:
		// The named template is rendered with these variables, so what it
		// does with them is what this one does with them. Which template
		// that is steers the output -- two names render two documents --
		// even though the name itself is never printed.
		// A name that is not a string, or names nothing, stops the render.
		a.apply(a.expr(n.Child(syntax.RoleTemplate)), Steers|Required)
		name, named := constTemplateName(n)
		if !named {
			// The name is computed, so which template runs is not a
			// static fact and it could print anything it is handed.
			a.opaqueSink = true
			break
		}
		if n.Kind == syntax.KindInclude && !withContext(n) {
			// `{% include "x" without context %}` hands it nothing.
			break
		}
		a.inherit(name)

	case syntax.KindImport, syntax.KindFromImport:
		// An import binds names here rather than rendering anything, and
		// -- unlike include -- it does not pass the context by default.
		// One that does not cannot see the caller's variables at all, so
		// it cannot print them: a real answer rather than a shrug.
		a.apply(a.expr(n.Child(syntax.RoleTemplate)), Steers|Required)
		name, named := constTemplateName(n)
		if !named {
			if withContext(n) {
				a.opaqueSink = true
			}
			break
		}
		if withContext(n) {
			a.inherit(name)
		}
		// What the import binds is a module or a macro from one. Calling
		// it reaches code this does not follow, so the binding is opaque
		// -- which is about the binding, not about the caller's variables.
		for _, bound := range importedNames(n) {
			a.effects[a.lookup(bound)] |= Opaque
		}

	case syntax.KindExprStmt:
		a.expr(n.Child(syntax.RoleValue))

	case syntax.KindBreak, syntax.KindContinue:
		// Leaves.

	default:
		// A statement nobody taught this about must not read as "nothing
		// happens here".
		for _, e := range n.Edges {
			a.taint(a.expr(e.Node))
		}
	}
}

// canRaise are the expression kinds whose operands can stop a render: the
// arithmetic and comparison operators, and `is`. A bare print, an assignment, a
// container literal and an attribute access are deliberately absent -- jinja2
// answers Undefined for a missing attribute rather than raising, and printing
// an Undefined is what the undefined policy is for.
func canRaise(k syntax.Kind) bool {
	switch k {
	case syntax.KindBinOp, syntax.KindUnaryOp, syntax.KindCompare,
		syntax.KindOperand, syntax.KindTest:
		return true
	}
	return false
}
