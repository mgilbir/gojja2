// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

// Package lexer turns template source into the token stream jinja2's parser
// expects.
package lexer

// Kind is a token type.
//
// The set and the names mirror jinja2's, because parse errors quote them:
// "unexpected 'end of print statement'" has to say exactly that.
type Kind uint8

const (
	EOF Kind = iota
	Data

	BlockBegin
	BlockEnd
	VariableBegin
	VariableEnd

	Name
	String
	Integer
	Float

	Add
	Sub
	Div
	FloorDiv
	Mul
	Mod
	Pow
	Tilde
	LBracket
	RBracket
	LParen
	RParen
	LBrace
	RBrace
	Eq
	Ne
	Gt
	Gteq
	Lt
	Lteq
	Assign
	Dot
	Colon
	Pipe
	Comma
	Semicolon
)

// symbols gives the source text of each operator token, and is also how those
// tokens are described in error messages.
var symbols = map[Kind]string{
	Add:       "+",
	Sub:       "-",
	Div:       "/",
	FloorDiv:  "//",
	Mul:       "*",
	Mod:       "%",
	Pow:       "**",
	Tilde:     "~",
	LBracket:  "[",
	RBracket:  "]",
	LParen:    "(",
	RParen:    ")",
	LBrace:    "{",
	RBrace:    "}",
	Eq:        "==",
	Ne:        "!=",
	Gt:        ">",
	Gteq:      ">=",
	Lt:        "<",
	Lteq:      "<=",
	Assign:    "=",
	Dot:       ".",
	Colon:     ":",
	Pipe:      "|",
	Comma:     ",",
	Semicolon: ";",
}

// operatorsByLength lists the operator spellings longest first, so that "**"
// wins over "*" and "//" over "/".
var operatorsByLength = []struct {
	text string
	kind Kind
}{
	{"//", FloorDiv},
	{"**", Pow},
	{"==", Eq},
	{"!=", Ne},
	{">=", Gteq},
	{"<=", Lteq},
	{"+", Add},
	{"-", Sub},
	{"/", Div},
	{"*", Mul},
	{"%", Mod},
	{"~", Tilde},
	{"[", LBracket},
	{"]", RBracket},
	{"(", LParen},
	{")", RParen},
	{"{", LBrace},
	{"}", RBrace},
	{">", Gt},
	{"<", Lt},
	{"=", Assign},
	{".", Dot},
	{":", Colon},
	{"|", Pipe},
	{",", Comma},
	{";", Semicolon},
}

// descriptions are the phrases jinja2 uses for non-operator tokens in parse
// errors.
var descriptions = map[Kind]string{
	BlockBegin:    "begin of statement block",
	BlockEnd:      "end of statement block",
	VariableBegin: "begin of print statement",
	VariableEnd:   "end of print statement",
	Data:          "template data / text",
	EOF:           "end of template",
	Name:          "name",
	String:        "string",
	Integer:       "integer",
	Float:         "float",
}

// Describe renders a token kind the way a parse error refers to it.
func (k Kind) Describe() string {
	if s, ok := symbols[k]; ok {
		return s
	}
	if s, ok := descriptions[k]; ok {
		return s
	}
	return "unknown"
}

// String is Describe, so a Kind formats usefully in diagnostics.
func (k Kind) String() string { return k.Describe() }

// Symbol returns the source spelling of an operator token, if it has one.
func (k Kind) Symbol() (string, bool) {
	s, ok := symbols[k]
	return s, ok
}

// Token is one lexed token.
//
// Value carries the token's text: the raw data for Data, the identifier for
// Name, and for String the *decoded* contents, with quotes removed and escape
// sequences resolved. Numbers keep their source spelling; the parser converts
// them, so that "0x1f" and "1_000" stay legible in errors.
type Token struct {
	Kind  Kind
	Value string
	Line  int
}

// Describe renders a token the way a parse error refers to it. A name is
// quoted by its own text, so "unexpected 'endfor'" names the tag the template
// actually wrote.
func (t Token) Describe() string {
	if t.Kind == Name {
		return t.Value
	}
	return t.Kind.Describe()
}
