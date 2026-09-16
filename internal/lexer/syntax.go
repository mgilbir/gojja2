// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package lexer

// Syntax is the configurable part of the template syntax: the delimiters and
// the whitespace policy. It mirrors the corresponding Environment options.
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
	return s
}
