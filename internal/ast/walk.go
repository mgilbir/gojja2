// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package ast

// Inspect calls fn for n and then, unless fn returned false, for every node
// beneath it.
//
// A walk that quietly skips a node is worse than no walk at all: a check built
// on it would report nothing and read as a clean bill of health. So the switch
// below is total over the node set, and walk_test.go proves it by reflection
// rather than by review -- it fills every child field of every node type with
// a sentinel and requires each one to come back. Add a node, or a field to an
// existing one, and that test fails until this function learns about it.
func Inspect(n Node, fn func(Node) bool) {
	if n == nil || !fn(n) {
		return
	}
	switch n := n.(type) {

	// --- statements ------------------------------------------------------

	case *Template:
		inspectStmts(n.Body, fn)
	case *Output:
		inspectExprs(n.Nodes, fn)
	case *For:
		Inspect(n.Target, fn)
		Inspect(n.Iter, fn)
		Inspect(n.Test, fn)
		inspectStmts(n.Body, fn)
		inspectStmts(n.Else, fn)
	case *If:
		Inspect(n.Test, fn)
		inspectStmts(n.Body, fn)
		for _, elif := range n.Elif {
			Inspect(elif, fn)
		}
		inspectStmts(n.Else, fn)
	case *Assign:
		Inspect(n.Target, fn)
		Inspect(n.Node, fn)
	case *AssignBlock:
		Inspect(n.Target, fn)
		Inspect(n.Filter, fn)
		inspectStmts(n.Body, fn)
	case *Macro:
		for _, a := range n.Args {
			Inspect(a, fn)
		}
		inspectExprs(n.Defaults, fn)
		inspectStmts(n.Body, fn)
	case *CallBlock:
		Inspect(n.Call, fn)
		for _, a := range n.Args {
			Inspect(a, fn)
		}
		inspectExprs(n.Defaults, fn)
		inspectStmts(n.Body, fn)
	case *FilterBlock:
		Inspect(n.Filter, fn)
		inspectStmts(n.Body, fn)
	case *With:
		inspectExprs(n.Targets, fn)
		inspectExprs(n.Values, fn)
		inspectStmts(n.Body, fn)
	case *Block:
		inspectStmts(n.Body, fn)
	case *Extends:
		Inspect(n.Template, fn)
	case *Include:
		Inspect(n.Template, fn)
	case *Import:
		Inspect(n.Template, fn)
	case *FromImport:
		Inspect(n.Template, fn)
	case *ExprStmt:
		Inspect(n.Node, fn)
	case *Scope:
		inspectStmts(n.Body, fn)
	case *AutoescapeBlock:
		Inspect(n.Value, fn)
		inspectStmts(n.Body, fn)
	case *Break, *Continue:
		// Leaves.

	// --- expressions -----------------------------------------------------

	case *Const, *TemplateData, *Name, *NSRef:
		// Leaves.
	case *Tuple:
		inspectExprs(n.Items, fn)
	case *List:
		inspectExprs(n.Items, fn)
	case *Pair:
		Inspect(n.Key, fn)
		Inspect(n.Value, fn)
	case *Dict:
		for _, p := range n.Items {
			Inspect(p, fn)
		}
	case *Keyword:
		Inspect(n.Value, fn)
	case *CondExpr:
		Inspect(n.Test, fn)
		Inspect(n.True, fn)
		Inspect(n.False, fn)
	case *BinOp:
		Inspect(n.Left, fn)
		Inspect(n.Right, fn)
	case *UnaryOp:
		Inspect(n.Node, fn)
	case *Concat:
		inspectExprs(n.Nodes, fn)
	case *Operand:
		Inspect(n.Expr, fn)
	case *Compare:
		Inspect(n.Expr, fn)
		for _, op := range n.Ops {
			Inspect(op, fn)
		}
	case *Getattr:
		Inspect(n.Node, fn)
	case *Getitem:
		Inspect(n.Node, fn)
		Inspect(n.Arg, fn)
	case *Slice:
		Inspect(n.Start, fn)
		Inspect(n.Stop, fn)
		Inspect(n.Step, fn)
	case *Call:
		Inspect(n.Node, fn)
		inspectArgs(&n.Args, fn)
	case *Filter:
		Inspect(n.Node, fn)
		inspectArgs(&n.Args, fn)
	case *Test:
		Inspect(n.Node, fn)
		inspectArgs(&n.Args, fn)
	}
}

// inspectArgs walks the argument list Call, Filter and Test share.
func inspectArgs(a *Args, fn func(Node) bool) {
	inspectExprs(a.Args, fn)
	for _, kw := range a.Kwargs {
		Inspect(kw, fn)
	}
	Inspect(a.DynArgs, fn)
	Inspect(a.DynKwargs, fn)
}

// InspectStmts walks a body, which is what a caller holding a template's
// statements has. Inspect would need a Template node wrapped around them, and
// allocating one per compile is a cost with nothing to show for it.
func InspectStmts(list []Stmt, fn func(Node) bool) { inspectStmts(list, fn) }

// inspectStmts and inspectExprs exist because a nil interface inside a slice
// is not the same as a nil slice, and Inspect has to be asked about each.
func inspectStmts(list []Stmt, fn func(Node) bool) {
	for _, s := range list {
		Inspect(s, fn)
	}
}

func inspectExprs(list []Expr, fn func(Node) bool) {
	for _, e := range list {
		Inspect(e, fn)
	}
}
