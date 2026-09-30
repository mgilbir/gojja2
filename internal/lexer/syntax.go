// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package lexer

import "github.com/mgilbir/gojja2/value"

// Syntax is the configurable part of the template syntax: the delimiters, the
// whitespace policy and the interpreter being reproduced. It mirrors the
// corresponding Environment options.
type Syntax struct {
	BlockStart    string
	BlockEnd      string
	VariableStart string
	VariableEnd   string
	CommentStart  string
	CommentEnd    string

	// LineStatementPrefix and LineCommentPrefix are disabled when empty.
	LineStatementPrefix string
	LineCommentPrefix   string

	// TrimBlocks removes the first newline after a block or comment tag.
	TrimBlocks bool
	// LstripBlocks removes horizontal whitespace from the start of a line
	// up to a block or comment tag. Print tags are never affected.
	LstripBlocks bool
	// KeepTrailingNewline keeps the template's final newline, which is
	// otherwise dropped.
	KeepTrailingNewline bool
	// NewlineSequence is what every newline in template data is rendered
	// as. Defaults to "\n".
	NewlineSequence string
	// PythonVersion is the interpreter being reproduced, which the lexer
	// needs because the class it matches a *name* out of is `\w` plus
	// jinja2's frozen extras -- and `\w` is CPython's, so it moves between
	// releases. See value.NameClass.
	PythonVersion value.PythonVersion
}

// DefaultSyntax returns jinja2's default delimiters and whitespace policy.
func DefaultSyntax() Syntax {
	return Syntax{
		BlockStart:      "{%",
		BlockEnd:        "%}",
		VariableStart:   "{{",
		VariableEnd:     "}}",
		CommentStart:    "{#",
		CommentEnd:      "#}",
		NewlineSequence: "\n",
		PythonVersion:   value.DefaultPythonVersion,
	}
}

// withDefaults fills in any delimiter left empty, so a caller can override one
// setting without restating the rest.
func (s Syntax) withDefaults() Syntax {
	d := DefaultSyntax()
	if s.BlockStart == "" {
		s.BlockStart = d.BlockStart
	}
	if s.BlockEnd == "" {
		s.BlockEnd = d.BlockEnd
	}
	if s.VariableStart == "" {
		s.VariableStart = d.VariableStart
	}
	if s.VariableEnd == "" {
		s.VariableEnd = d.VariableEnd
	}
	if s.CommentStart == "" {
		s.CommentStart = d.CommentStart
	}
	if s.CommentEnd == "" {
		s.CommentEnd = d.CommentEnd
	}
	if s.NewlineSequence == "" {
		s.NewlineSequence = d.NewlineSequence
	}
	// The zero PythonVersion is not a version, so a caller that names no
	// interpreter gets the pin rather than an error from the first name it
	// lexes.
	if !s.PythonVersion.Known() {
		s.PythonVersion = d.PythonVersion
	}
	return s
}
