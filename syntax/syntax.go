// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

// Package syntax is a template's structure, in a form built for asking
// questions about it.
//
// This is deliberately not gojja2's own tree, and deliberately not jinja2's.
// The engine's tree is shaped by what the evaluator needs and is free to keep
// changing; jinja2's is shaped by what its code generator needs, and matching it
// node for node would be a compatibility surface -- which is a promise to
// reproduce a data structure rather than a behaviour, and the wrong thing to
// spend the effort on.
//
// What this is instead is a single normalised vocabulary that both trees can be
// written into, so a question asked of one gets the same answer from the other.
// jinja2 has nine classes for binary operators and this has one node with an
// operator on it; jinja2 carries call arguments as four fields and this carries
// them as labelled edges. Neither difference changes an answer, and collapsing
// them is what lets the two be compared at all.
//
// # Shape
//
// Every node is the same type. A [Node] has a [Kind], a handful of attributes,
// and children reached through labelled edges -- so `{% if c %}a{% else %}b{% endif %}`
// is one node of kind [KindIf] with a [RoleTest] edge and two [RoleBody] edges
// that are told apart by their labels rather than by their position.
//
// That uniformity is the point. A query can walk anything without knowing the
// node set, the labels give it the context a position would not ([RoleTest] is
// a condition wherever it appears), and the whole thing has an obvious
// canonical encoding -- which is what makes "the same conclusions from either
// tree" a thing that can be checked rather than asserted.
package syntax

// Kind is what a node is.
//
// The set is closed and each member is spelled out, because the spelling is
// part of the canonical form: tools/oracle/syntax_emit.py writes jinja2's tree
// using these exact strings, and the two are compared byte for byte.
type Kind string

// Statements.
const (
	KindTemplate   Kind = "template"
	KindOutput     Kind = "output"
	KindIf         Kind = "if"
	KindFor        Kind = "for"
	KindAssign     Kind = "assign"
	KindAssignBlk  Kind = "assign_block"
	KindMacro      Kind = "macro"
	KindCallBlock  Kind = "call_block"
	KindFilterBlk  Kind = "filter_block"
	KindWith       Kind = "with"
	KindBlock      Kind = "block"
	KindExtends    Kind = "extends"
	KindInclude    Kind = "include"
	KindImport     Kind = "import"
	KindFromImport Kind = "from_import"
	KindExprStmt   Kind = "expr_stmt"
	KindScope      Kind = "scope"
	KindAutoescape Kind = "autoescape"
	KindBreak      Kind = "break"
	KindContinue   Kind = "continue"
)

// Expressions.
const (
	KindConst   Kind = "const"
	KindText    Kind = "text"
	KindName    Kind = "name"
	KindNSRef   Kind = "nsref"
	KindTuple   Kind = "tuple"
	KindList    Kind = "list"
	KindDict    Kind = "dict"
	KindPair    Kind = "pair"
	KindKeyword Kind = "keyword"
	KindCond    Kind = "cond"
	KindBinOp   Kind = "binop"
	KindUnaryOp Kind = "unaryop"
	KindConcat  Kind = "concat"
	KindCompare Kind = "compare"
	KindOperand Kind = "operand"
	KindGetattr Kind = "getattr"
	KindGetitem Kind = "getitem"
	KindSlice   Kind = "slice"
	KindCall    Kind = "call"
	KindFilter  Kind = "filter"
	KindTest    Kind = "test"
)

// Role labels the edge from a parent to a child: what that child *is* to its
// parent, rather than where it happens to sit.
//
// This is what carries the context a query needs. A name under [RoleTest] is
// being used to decide something; the same name under [RoleBody] of an
// [KindOutput] is being printed. Asking "is this expression a condition"
// otherwise means knowing, for every node kind, which of its fields are
// conditions -- which is exactly the knowledge a normalised tree exists to
// remove the need for.
type Role string

const (
	RoleBody     Role = "body"     // statements that run
	RoleElse     Role = "else"     // the other statements that run
	RoleElif     Role = "elif"     // the next arm of an if chain
	RoleTest     Role = "test"     // a condition: chooses, rather than contributes
	RoleTarget   Role = "target"   // what is being bound
	RoleValue    Role = "value"    // what it is being bound to, or what is printed
	RoleIter     Role = "iter"     // the sequence a loop walks
	RoleFilter   Role = "filter"   // a filter applied to a captured body
	RoleSubject  Role = "subject"  // what a filter, test or attribute applies to
	RoleCallee   Role = "callee"   // what a call calls
	RoleArg      Role = "arg"      // a positional argument
	RoleKwarg    Role = "kwarg"    // a keyword argument
	RoleDynArgs  Role = "dynargs"  // `*args`
	RoleDynKw    Role = "dynkw"    // `**kwargs`
	RoleParam    Role = "param"    // a declared parameter
	RoleDefault  Role = "default"  // a parameter's default
	RoleLeft     Role = "left"     // a binary operator's left operand
	RoleRight    Role = "right"    // ... and its right
	RoleOperand  Role = "operand"  // a unary operator's operand, or a comparison's
	RoleItem     Role = "item"     // a member of a container
	RoleKey      Role = "key"      // a mapping key
	RoleIndex    Role = "index"    // a subscript
	RoleStart    Role = "start"    // a slice's bounds
	RoleStop     Role = "stop"     //
	RoleStep     Role = "step"     //
	RoleTemplate Role = "template" // the template another one names
	RoleThen     Role = "then"     // a conditional expression's chosen value
	RoleOther    Role = "other"    // ... and its rejected one
)

// Edge is one labelled link to a child.
type Edge struct {
	Role Role
	Node *Node
}

// Node is one piece of a template's structure.
//
// Attrs holds the scalars a kind needs -- an operator's spelling, a name, a
// literal's value, whether an import carries the context. Which keys a kind
// uses is fixed and documented by the builder; a key that is absent is absent
// rather than empty, so the canonical form of a node says exactly what is
// known about it.
type Node struct {
	Kind  Kind
	Attrs map[string]any
	Edges []Edge

	// Line is where the construct starts in the source, for a caller
	// reporting something. It is deliberately not part of the canonical
	// form: two parsers may agree completely about a template's structure
	// and still disagree about which line a node begins on.
	Line int
}

// Child returns the first child reached by role, or nil.
func (n *Node) Child(role Role) *Node {
	for _, e := range n.Edges {
		if e.Role == role {
			return e.Node
		}
	}
	return nil
}

// Children returns every child reached by role, in order.
func (n *Node) Children(role Role) []*Node {
	var out []*Node
	for _, e := range n.Edges {
		if e.Role == role {
			out = append(out, e.Node)
		}
	}
	return out
}

// Attr returns a string attribute, or "".
func (n *Node) Attr(key string) string {
	if n == nil || n.Attrs == nil {
		return ""
	}
	s, _ := n.Attrs[key].(string)
	return s
}

// Walk calls fn for n and then, unless fn returned false, for every node
// beneath it. The parent's edge role is passed with each child, so a query can
// tell a condition from a printed value without tracking context itself.
func Walk(n *Node, fn func(n *Node, role Role) bool) {
	walk(n, "", fn)
}

func walk(n *Node, role Role, fn func(*Node, Role) bool) {
	if n == nil || !fn(n, role) {
		return
	}
	for _, e := range n.Edges {
		walk(e.Node, e.Role, fn)
	}
}
