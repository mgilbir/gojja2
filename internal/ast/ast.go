// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

// Package ast is the parsed form of a template.
//
// The node set mirrors jinja2's `nodes` module closely enough that parse errors
// can name nodes the way jinja2 does -- "can't assign to 'getattr'" quotes the
// lowercased class name -- and so that behaviour can be compared construct by
// construct rather than only through rendered output.
package ast

// Node is any parsed construct.
type Node interface {
	// Line is the 1-based source line the construct starts on.
	Line() int
	// TypeName is jinja2's class name for this node, lowercased. It
	// appears verbatim in parse errors.
	TypeName() string
}

// Stmt is a statement: something that runs rather than something that has a
// value.
type Stmt interface {
	Node
	stmtNode()
}

// Expr is an expression.
type Expr interface {
	Node
	exprNode()
}

// Pos carries the source line every node has. The field is spelled L so that
// the Line accessor, which is what the Node interface requires, can keep the
// obvious name.
type Pos struct{ L int }

func (p Pos) Line() int { return p.L }

// At builds the embedded position for a node on the given line.
func At(line int) Pos { return Pos{L: line} }

// --- statements --------------------------------------------------------------

// Template is a whole parsed template.
type Template struct {
	Pos
	Body []Stmt
}

// Output prints a run of expressions. The parser gathers adjacent template
// data and print tags into one Output, so `a{{ b }}c` is a single node.
type Output struct {
	Pos
	Nodes []Expr
}

// For is a loop. Else runs when the sequence was empty, Test filters items,
// and Recursive allows the body to call loop() to descend.
type For struct {
	Pos
	Target    Expr
	Iter      Expr
	Body      []Stmt
	Else      []Stmt
	Test      Expr
	Recursive bool
}

// If holds one condition. Additional `elif` branches hang off Elif, each with
// its own condition and body; only the outermost If carries Else.
type If struct {
	Pos
	Test Expr
	Body []Stmt
	Elif []*If
	Else []Stmt
}

// Assign is `{% set target = value %}`.
type Assign struct {
	Pos
	Target Expr
	Node   Expr
}

// AssignBlock is the block form, `{% set x %}...{% endset %}`, optionally with
// a filter chain applied to the captured output.
type AssignBlock struct {
	Pos
	Target Expr
	Filter Expr // nil when no filter was given
	Body   []Stmt
}

// Macro is `{% macro name(args) %}`.
type Macro struct {
	Pos
	Name string
	Args []*Name
	// Defaults align with the tail of Args: the last len(Defaults) of them.
	Defaults []Expr
	Body     []Stmt
}

// CallBlock is `{% call(args) macro() %}...{% endcall %}`, whose body becomes
// the caller() the invoked macro can render.
type CallBlock struct {
	Pos
	Call     *Call
	Args     []*Name
	Defaults []Expr
	Body     []Stmt
}

// FilterBlock is `{% filter upper %}...{% endfilter %}`.
type FilterBlock struct {
	Pos
	Body   []Stmt
	Filter Expr
}

// With is `{% with a = 1, b = 2 %}`.
type With struct {
	Pos
	Targets []Expr
	Values  []Expr
	Body    []Stmt
}

// Block is a named, overridable section. Scoped exposes the enclosing scope's
// variables to overrides; Required forces a child template to provide a body.
type Block struct {
	Pos
	Name     string
	Body     []Stmt
	Scoped   bool
	Required bool
}

// Extends names the parent template.
type Extends struct {
	Pos
	Template Expr
}

// Include renders another template inline.
type Include struct {
	Pos
	Template      Expr
	WithContext   bool
	IgnoreMissing bool
}

// Import binds another template's exported names to a single target.
type Import struct {
	Pos
	Template    Expr
	Target      string
	WithContext bool
}

// ImportName is one name in a `{% from ... import a, b as c %}` list.
type ImportName struct {
	Name string
	// Alias is the local name; equal to Name when no `as` was given.
	Alias string
}

// FromImport binds selected names from another template.
type FromImport struct {
	Pos
	Template    Expr
	Names       []ImportName
	WithContext bool
}

// ExprStmt evaluates an expression and discards it: `{% do x.append(1) %}`.
type ExprStmt struct {
	Pos
	Node Expr
}

// Scope isolates the variables its body assigns.
type Scope struct {
	Pos
	Body []Stmt
}

// AutoescapeBlock is `{% autoescape true %}`, which switches escaping for the
// span of its body.
type AutoescapeBlock struct {
	Pos
	Value Expr
	Body  []Stmt
}

// Break and Continue are the loop controls, available when the extension that
// provides them is enabled.
type (
	Break struct{ Pos }

	Continue struct{ Pos }
)

func (*Template) stmtNode()        {}
func (*Output) stmtNode()          {}
func (*For) stmtNode()             {}
func (*If) stmtNode()              {}
func (*Assign) stmtNode()          {}
func (*AssignBlock) stmtNode()     {}
func (*Macro) stmtNode()           {}
func (*CallBlock) stmtNode()       {}
func (*FilterBlock) stmtNode()     {}
func (*With) stmtNode()            {}
func (*Block) stmtNode()           {}
func (*Extends) stmtNode()         {}
func (*Include) stmtNode()         {}
func (*Import) stmtNode()          {}
func (*FromImport) stmtNode()      {}
func (*ExprStmt) stmtNode()        {}
func (*Scope) stmtNode()           {}
func (*AutoescapeBlock) stmtNode() {}
func (*Break) stmtNode()           {}
func (*Continue) stmtNode()        {}

func (*Template) TypeName() string        { return "template" }
func (*Output) TypeName() string          { return "output" }
func (*For) TypeName() string             { return "for" }
func (*If) TypeName() string              { return "if" }
func (*Assign) TypeName() string          { return "assign" }
func (*AssignBlock) TypeName() string     { return "assignblock" }
func (*Macro) TypeName() string           { return "macro" }
func (*CallBlock) TypeName() string       { return "callblock" }
func (*FilterBlock) TypeName() string     { return "filterblock" }
func (*With) TypeName() string            { return "with" }
func (*Block) TypeName() string           { return "block" }
func (*Extends) TypeName() string         { return "extends" }
func (*Include) TypeName() string         { return "include" }
func (*Import) TypeName() string          { return "import" }
func (*FromImport) TypeName() string      { return "fromimport" }
func (*ExprStmt) TypeName() string        { return "exprstmt" }
func (*Scope) TypeName() string           { return "scope" }
func (*AutoescapeBlock) TypeName() string { return "scope" }
func (*Break) TypeName() string           { return "break" }
func (*Continue) TypeName() string        { return "continue" }
