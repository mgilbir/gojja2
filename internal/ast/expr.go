// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package ast

import "github.com/mgilbir/gojja2/value"

// Const is a literal.
type Const struct {
	Pos
	Value value.Value
}

// TemplateData is literal text between tags.
//
// It is separate from Const because autoescaping must not touch it: the markup
// in a template is the author's, already trusted, while a Const string that
// happens to contain a tag is data.
type TemplateData struct {
	Pos
	Data string
}

// Name is a variable reference. Store marks the assignment side of a binding.
type Name struct {
	Pos
	Name  string
	Store bool
}

// NSRef is `ns.attr` on the assignment side of a `set`, which mutates the
// namespace object rather than rebinding a local.
type NSRef struct {
	Pos
	Name string
	Attr string
}

// Tuple is a comma-separated group, which may also be an unpacking target.
type Tuple struct {
	Pos
	Items []Expr
	Store bool
}

// List is `[...]`.
type List struct {
	Pos
	Items []Expr
}

// Pair is one `key: value` in a dict literal.
type Pair struct {
	Pos
	Key   Expr
	Value Expr
}

// Dict is `{...}`.
type Dict struct {
	Pos
	Items []*Pair
}

// Keyword is one `name=value` argument at a call site.
type Keyword struct {
	Pos
	Key   string
	Value Expr
}

// CondExpr is `a if test else b`. Else is nil when the else branch was
// omitted, in which case a false test yields undefined.
type CondExpr struct {
	Pos
	Test  Expr
	True  Expr
	False Expr
}

// BinOpKind identifies a binary operator.
type BinOpKind uint8

const (
	OpAdd BinOpKind = iota
	OpSub
	OpMul
	OpDiv
	OpFloorDiv
	OpMod
	OpPow
	OpAnd
	OpOr
)

var binOpNames = [...]string{
	OpAdd:      "add",
	OpSub:      "sub",
	OpMul:      "mul",
	OpDiv:      "div",
	OpFloorDiv: "floordiv",
	OpMod:      "mod",
	OpPow:      "pow",
	OpAnd:      "and",
	OpOr:       "or",
}

func (k BinOpKind) String() string { return binOpNames[k] }

// BinOp is a binary operation. And and Or short-circuit and yield one of their
// operands rather than a bool, as they do in Python.
type BinOp struct {
	Pos
	Op    BinOpKind
	Left  Expr
	Right Expr
}

// UnaryOpKind identifies a prefix operator.
type UnaryOpKind uint8

const (
	OpNot UnaryOpKind = iota
	OpNeg
	OpPos
)

var unaryOpNames = [...]string{OpNot: "not", OpNeg: "neg", OpPos: "pos"}

func (k UnaryOpKind) String() string { return unaryOpNames[k] }

// UnaryOp is a prefix operation.
type UnaryOp struct {
	Pos
	Op   UnaryOpKind
	Node Expr
}

// Concat is a run of `~` operands, joined after stringifying each.
type Concat struct {
	Pos
	Nodes []Expr
}

// Operand is one `op expr` step of a comparison chain.
type Operand struct {
	Pos
	// Op is one of eq, ne, lt, lteq, gt, gteq, in, notin.
	Op   string
	Expr Expr
}

// Compare is a chained comparison: `a < b <= c` holds the first operand and
// then one Operand per step, each evaluated at most once.
type Compare struct {
	Pos
	Expr Expr
	Ops  []*Operand
}

// Getattr is `node.attr`.
type Getattr struct {
	Pos
	Node Expr
	Attr string
}

// Getitem is `node[arg]`.
type Getitem struct {
	Pos
	Node Expr
	Arg  Expr
}

// Slice is the `a:b:c` inside a subscript. A nil field means the bound was
// omitted, which is not the same as it being None.
type Slice struct {
	Pos
	Start Expr
	Stop  Expr
	Step  Expr
}

// Args is the argument list shared by calls, filters and tests.
type Args struct {
	Args   []Expr
	Kwargs []*Keyword
	// DynArgs and DynKwargs are the `*args` and `**kwargs` forms.
	DynArgs   Expr
	DynKwargs Expr
}

// Call is `node(...)`.
type Call struct {
	Pos
	Node Expr
	Args
}

// Filter is `node|name(...)`. Node is nil for the leading filter of a
// `{% filter %}` block or a block `set`, where the input is the captured body.
type Filter struct {
	Pos
	Node Expr
	Name string
	Args
}

// Test is `node is name(...)`.
type Test struct {
	Pos
	Node Expr
	Name string
	Args
}

func (*Const) exprNode()        {}
func (*TemplateData) exprNode() {}
func (*Name) exprNode()         {}
func (*NSRef) exprNode()        {}
func (*Tuple) exprNode()        {}
func (*List) exprNode()         {}
func (*Dict) exprNode()         {}
func (*Pair) exprNode()         {}
func (*Keyword) exprNode()      {}
func (*CondExpr) exprNode()     {}
func (*BinOp) exprNode()        {}
func (*UnaryOp) exprNode()      {}
func (*Concat) exprNode()       {}
func (*Compare) exprNode()      {}
func (*Operand) exprNode()      {}
func (*Getattr) exprNode()      {}
func (*Getitem) exprNode()      {}
func (*Slice) exprNode()        {}
func (*Call) exprNode()         {}
func (*Filter) exprNode()       {}
func (*Test) exprNode()         {}

func (*Const) TypeName() string        { return "const" }
func (*TemplateData) TypeName() string { return "templatedata" }
func (*Name) TypeName() string         { return "name" }
func (*NSRef) TypeName() string        { return "nsref" }
func (*Tuple) TypeName() string        { return "tuple" }
func (*List) TypeName() string         { return "list" }
func (*Dict) TypeName() string         { return "dict" }
func (*Pair) TypeName() string         { return "pair" }
func (*Keyword) TypeName() string      { return "keyword" }
func (*CondExpr) TypeName() string     { return "condexpr" }
func (n *BinOp) TypeName() string      { return n.Op.String() }
func (n *UnaryOp) TypeName() string    { return n.Op.String() }
func (*Concat) TypeName() string       { return "concat" }
func (*Compare) TypeName() string      { return "compare" }
func (*Operand) TypeName() string      { return "operand" }
func (*Getattr) TypeName() string      { return "getattr" }
func (*Getitem) TypeName() string      { return "getitem" }
func (*Slice) TypeName() string        { return "slice" }
func (*Call) TypeName() string         { return "call" }
func (*Filter) TypeName() string       { return "filter" }
func (*Test) TypeName() string         { return "test" }

// reservedNames cannot be assigned to, because they are literals rather than
// identifiers even though the lexer produces a name token for them.
var reservedNames = map[string]bool{
	"true": true, "false": true, "none": true,
	"True": true, "False": true, "None": true,
}

// CanAssign reports whether an expression is a legal assignment target.
func CanAssign(e Expr) bool {
	switch n := e.(type) {
	case *Name:
		return !reservedNames[n.Name]
	case *NSRef:
		return true
	case *Tuple:
		for _, item := range n.Items {
			if !CanAssign(item) {
				return false
			}
		}
		return true
	}
	return false
}

// SetStore marks an expression as the target of an assignment.
func SetStore(e Expr) {
	switch n := e.(type) {
	case *Name:
		n.Store = true
	case *Tuple:
		n.Store = true
		for _, item := range n.Items {
			SetStore(item)
		}
	}
}
