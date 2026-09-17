// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

// Package errs models the exceptions CPython's jinja2 raises.
//
// Conformance is graded against the exception *class name* CPython reports, so
// every error gojja2 produces carries the Kind it would have had there --
// including the plain Python builtins (TypeError, ZeroDivisionError, ...) that
// jinja2 lets propagate out of the template unchanged.
package errs

import (
	"fmt"
	"strings"
)

// Kind is a Python exception class. Its String is the class name exactly as
// CPython spells it, because that is what the oracle records.
type Kind uint8

const (
	unknownKind Kind = iota

	// jinja2.exceptions
	TemplateError
	TemplateNotFound
	TemplatesNotFound
	TemplateSyntaxError
	TemplateAssertionError
	TemplateRuntimeError
	UndefinedError
	SecurityError
	FilterArgumentError

	// Python builtins that reach the caller through jinja2.
	TypeError
	ValueError
	ZeroDivisionError
	LookupError
	KeyError
	IndexError
	AttributeError
	NameError
	OverflowError
	StopIteration
	RecursionError
	ArithmeticError
	// UnicodeError and its two halves are what a codec raises. Python
	// derives them from ValueError, so a template catching that catches
	// these.
	UnicodeError
	UnicodeEncodeError
	UnicodeDecodeError
	// AssertionError is what a bare `assert` in jinja2 raises, which is
	// how do_truncate rejects a length shorter than its ellipsis.
	AssertionError
	Exception
)

var kindNames = [...]string{
	unknownKind:            "Exception",
	TemplateError:          "TemplateError",
	TemplateNotFound:       "TemplateNotFound",
	TemplatesNotFound:      "TemplatesNotFound",
	TemplateSyntaxError:    "TemplateSyntaxError",
	TemplateAssertionError: "TemplateAssertionError",
	TemplateRuntimeError:   "TemplateRuntimeError",
	UndefinedError:         "UndefinedError",
	SecurityError:          "SecurityError",
	FilterArgumentError:    "FilterArgumentError",
	TypeError:              "TypeError",
	ValueError:             "ValueError",
	ZeroDivisionError:      "ZeroDivisionError",
	LookupError:            "LookupError",
	KeyError:               "KeyError",
	IndexError:             "IndexError",
	AttributeError:         "AttributeError",
	NameError:              "NameError",
	OverflowError:          "OverflowError",
	StopIteration:          "StopIteration",
	RecursionError:         "RecursionError",
	UnicodeError:           "UnicodeError",
	UnicodeEncodeError:     "UnicodeEncodeError",
	UnicodeDecodeError:     "UnicodeDecodeError",
	ArithmeticError:        "ArithmeticError",
	AssertionError:         "AssertionError",
	Exception:              "Exception",
}

// parent mirrors the CPython/jinja2 class hierarchy, so Is can answer
// "would `except TemplateSyntaxError` have caught this?".
var parent = [...]Kind{
	TemplateNotFound:       TemplateError,
	TemplatesNotFound:      TemplateNotFound,
	TemplateSyntaxError:    TemplateError,
	TemplateAssertionError: TemplateSyntaxError,
	TemplateRuntimeError:   TemplateError,
	UndefinedError:         TemplateRuntimeError,
	SecurityError:          TemplateRuntimeError,
	FilterArgumentError:    TemplateRuntimeError,
	TemplateError:          Exception,
	TypeError:              Exception,
	ValueError:             Exception,
	ArithmeticError:        Exception,
	ZeroDivisionError:      ArithmeticError,
	OverflowError:          ArithmeticError,
	LookupError:            Exception,
	KeyError:               LookupError,
	IndexError:             LookupError,
	AttributeError:         Exception,
	NameError:              Exception,
	StopIteration:          Exception,
	RecursionError:         Exception,
	AssertionError:         Exception,
	UnicodeError:           ValueError,
	UnicodeEncodeError:     UnicodeError,
	UnicodeDecodeError:     UnicodeError,
}

func (k Kind) String() string {
	if int(k) < len(kindNames) && kindNames[k] != "" {
		return kindNames[k]
	}
	return "Exception"
}

// DerivesFrom reports whether k is want or a subclass of it.
func (k Kind) DerivesFrom(want Kind) bool {
	for k != unknownKind {
		if k == want {
			return true
		}
		if int(k) >= len(parent) || parent[k] == unknownKind {
			return false
		}
		k = parent[k]
	}
	return false
}

// Error is a template error carrying the CPython exception class it maps to.
//
// Error() returns the bare message, matching Python's str(exc), so it can be
// compared against the oracle without stripping decoration. Location lives in
// the fields instead.
type Error struct {
	Kind   Kind
	Msg    string
	Name   string // template the error was raised in
	Line   int    // 1-based; 0 when unknown
	Source string // template source, retained for error rendering
	Cause  error
	// Limit is the bound a RecursionError hit. The message reproduces
	// CPython's wording, which names where in *its* interpreter the stack
	// ran out and so cannot say this; a Go caller reads it from here.
	Limit int
}

func (e *Error) Error() string { return e.Msg }

func (e *Error) Unwrap() error { return e.Cause }

// Is lets errors.Is(err, errs.TypeError) work against a bare Kind target.
func (e *Error) Is(target error) bool {
	if k, ok := target.(Kind); ok {
		return e.Kind.DerivesFrom(k)
	}
	return false
}

// Error makes Kind usable as an errors.Is target.
func (k Kind) Error() string { return k.String() }

// Detail renders the error the way a human debugging a template wants it:
// class, message and location.
func (e *Error) Detail() string {
	var b strings.Builder
	b.WriteString(e.Kind.String())
	b.WriteString(": ")
	b.WriteString(e.Msg)
	if e.Name != "" || e.Line != 0 {
		b.WriteString("\n  in ")
		if e.Name != "" {
			b.WriteString(e.Name)
		} else {
			b.WriteString("<template>")
		}
		if e.Line != 0 {
			fmt.Fprintf(&b, ", line %d", e.Line)
		}
	}
	return b.String()
}

// New builds an error of the given kind.
func New(kind Kind, format string, args ...any) *Error {
	return &Error{Kind: kind, Msg: fmt.Sprintf(format, args...)}
}

// At locates err at name:line, filling in only the fields that are still
// unset so the innermost frame wins, and returns it.
//
// It edits the error in place rather than copying it. That is what makes the
// "only if unset" rule work -- the first frame to see an error is the one
// nearest where it was raised, and every frame outside that one must leave the
// location alone.
func At(err error, name string, line int) error {
	e, ok := err.(*Error)
	if !ok {
		return err
	}
	if e.Line == 0 {
		e.Line = line
	}
	if e.Name == "" {
		e.Name = name
	}
	return e
}

// KindOf reports the exception class err would have had in CPython.
func KindOf(err error) Kind {
	if e, ok := err.(*Error); ok {
		return e.Kind
	}
	return unknownKind
}
