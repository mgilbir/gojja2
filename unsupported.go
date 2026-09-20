// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"fmt"
	"strings"

	"github.com/mgilbir/gojja2/errs"
	"github.com/mgilbir/gojja2/internal/ast"
	"github.com/mgilbir/gojja2/value"
)

// Unsupported is a construct a template asks for that gojja2 cannot answer the
// way CPython jinja2 does.
//
// It exists because the interesting ones are *latent*. A codec error handler is
// looked up only when a character actually needs it -- CPython works that way
// too, and gojja2 matches -- so a template naming a handler gojja2 does not
// have compiles, renders, passes its tests, and then raises the first time a
// payload reaches the handler. `{{ s|string.encode("ascii", "namereplace") }}`
// is fine for every ASCII string and fails on the first accented letter, and
// `.decode("utf-8", "surrogateescape")` is fine for every well-formed input and
// fails on the first malformed byte, which is exactly the moment nobody wants a
// surprise.
//
// Compiling a template finds these and puts them on [Template.Unsupported].
// [WithUnsupportedReport] routes them somewhere as they are found, and
// [WithUnsupportedLeniency] can make them fatal at compile time instead.
type Unsupported struct {
	// Template is the name the template was compiled under, empty for
	// [Environment.FromString].
	Template string
	// Line is the 1-based line the construct is on.
	Line int
	// Construct is how the template spells it, such as
	// `.encode(..., "namereplace")`.
	Construct string
	// Why says what gojja2 does instead, and when.
	Why string
}

// Error renders the finding as the compile error RefuseUnsupported raises.
func (u Unsupported) Error() string { return u.Construct + " " + u.Why }

// UnsupportedLeniency chooses what compiling a template does about a construct
// gojja2 cannot honour the way jinja2 does.
//
// Unlike [DelimiterLeniency] and [AutoescapeLeniency], the lenient value is the
// zero one. Those two describe a configuration, which nobody has written yet
// when the default applies; this describes a template, and templates already
// exist. Refusing by default would reject one that works today because its
// data has never reached the handler -- so the default reports and renders, and
// refusing is asked for by name.
type UnsupportedLeniency int

const (
	// ReportUnsupported compiles the template and records the construct on
	// [Template.Unsupported]. It is the default.
	ReportUnsupported UnsupportedLeniency = iota
	// RefuseUnsupported fails compilation instead, so the template cannot
	// reach production carrying a divergence that only some inputs show.
	RefuseUnsupported
)

// WithUnsupportedLeniency chooses whether a construct gojja2 cannot honour is
// reported or refused. See [UnsupportedLeniency].
func WithUnsupportedLeniency(l UnsupportedLeniency) Option {
	return func(e *Environment) error {
		switch l {
		case ReportUnsupported, RefuseUnsupported:
			e.unsupportedLeniency = l
			return nil
		}
		return fmt.Errorf("gojja2: unknown UnsupportedLeniency %d", int(l))
	}
}

// WithUnsupportedReport sets a function called once for each construct found
// while compiling a template, in source order. It is called before compilation
// returns, and before the error when the leniency is [RefuseUnsupported].
//
// gojja2 has no logger of its own and writes to no stream, so this is how a
// finding reaches one:
//
//	env, err := gojja2.New(gojja2.WithUnsupportedReport(func(u gojja2.Unsupported) {
//		log.Printf("%s:%d: %s", u.Template, u.Line, u.Error())
//	}))
//
// The same findings are on [Template.Unsupported] whether or not this is set.
func WithUnsupportedReport(fn func(Unsupported)) Option {
	return func(e *Environment) error {
		e.unsupportedReport = fn
		return nil
	}
}

// Unsupported returns the constructs found when this template was compiled, in
// source order, or nil when there were none. The slice is the template's own;
// callers must not modify it.
func (t *Template) Unsupported() []Unsupported { return t.unsupported }

// codecHandlerGaps are the codec error handlers CPython has and gojja2 cannot
// answer, by the direction they are asked in.
//
// Only the genuine gaps are listed. `xmlcharrefreplace` and `namereplace` on a
// *decode* are not here: CPython refuses those by type as well, and gojja2
// raises the same TypeError, so a template using one is wrong in both and says
// so identically. The three below are the ones CPython answers and gojja2
// cannot -- see docs/divergences.md for why each is out of reach.
var codecHandlerGaps = map[string]map[string]string{
	"encode": {
		"namereplace": "needs the Unicode name database, which gojja2 does not " +
			"carry; it renders until a character the codec cannot hold reaches " +
			"it and then raises LookupError. Spell the character with \\uNNNN, " +
			"or use backslashreplace, which is exact here",
	},
	"decode": {
		"surrogateescape": "answers with a lone surrogate, which a Go string " +
			"cannot hold; it renders until a byte fails to decode and then " +
			"raises LookupError. Use replace or backslashreplace, both of " +
			"which are exact here",
		"surrogatepass": "answers with a lone surrogate, which a Go string " +
			"cannot hold; it renders until a byte fails to decode and then " +
			"raises LookupError. Use replace or backslashreplace, both of " +
			"which are exact here",
	},
}

// findUnsupported walks a compiled tree for constructs gojja2 cannot honour.
//
// It runs after constant folding, so a handler assembled from constants --
// `"name" ~ "replace"` -- is a literal by the time it gets here. A handler that
// is genuinely dynamic, `s.encode("ascii", h)`, cannot be judged before the
// render and is not guessed at; that one still surfaces as the LookupError it
// always did.
func findUnsupported(body []ast.Stmt, source, name string) []Unsupported {
	// Every template pays for this, and almost none can be carrying a
	// finding: a gap is only ever reached through `.encode(` or `.decode(`,
	// and the attribute name in a Getattr is the source's own text. So a
	// template whose source does not contain "code" cannot match, and
	// skipping it is sound rather than a guess.
	//
	// The substring is the one both words share, and it is one pass rather
	// than two for a reason that was measured: searching for "encode" and
	// then "decode" cost nearly twice as much, because a template's source
	// is full of 'e' -- `users`, `name`, `endif` -- and every one of them
	// is a false start. Walking unconditionally is worse still. On a 12 KiB
	// template: +2.65% to always walk, +1.65% for the two words, +0.92% for
	// this.
	//
	// `s["enc" ~ "ode"](...)` does not sneak past. That is a Getitem, which
	// is not a method call gojja2 dispatches, and the check below only ever
	// matches a Getattr.
	if !strings.Contains(source, "code") {
		return nil
	}
	var out []Unsupported
	ast.InspectStmts(body, func(n ast.Node) bool {
		call, ok := n.(*ast.Call)
		if !ok {
			return true
		}
		attr, ok := call.Node.(*ast.Getattr)
		if !ok {
			return true
		}
		gaps, ok := codecHandlerGaps[attr.Attr]
		if !ok {
			return true
		}
		// The handler is the second positional argument or the keyword
		// `errors`, which is how codecArgs reads it.
		handler, line := handlerArg(call)
		if why, bad := gaps[handler]; bad {
			out = append(out, Unsupported{
				Template:  name,
				Line:      line,
				Construct: fmt.Sprintf("%s(..., %q)", "."+attr.Attr, handler),
				Why:       why,
			})
		}
		return true
	})
	return out
}

// handlerArg reads the error handler a call names, when it names one as a
// literal at all.
func handlerArg(call *ast.Call) (handler string, line int) {
	if len(call.Args.Args) > 1 {
		if c, ok := call.Args.Args[1].(*ast.Const); ok && c.Value.IsString() {
			return value.Str(c.Value), c.Line()
		}
	}
	for _, kw := range call.Kwargs {
		if kw.Key != "errors" {
			continue
		}
		if c, ok := kw.Value.(*ast.Const); ok && c.Value.IsString() {
			return value.Str(c.Value), c.Line()
		}
	}
	return "", 0
}

// reportUnsupported hands each finding to the environment's hook and, when the
// leniency says so, turns the first into the error that fails the compile.
func (e *Environment) reportUnsupported(found []Unsupported, name, source string) error {
	for _, u := range found {
		if e.unsupportedReport != nil {
			e.unsupportedReport(u)
		}
	}
	if e.unsupportedLeniency != RefuseUnsupported || len(found) == 0 {
		return nil
	}
	first := found[0]
	// TemplateAssertionError is what jinja2 raises for a template that
	// parses and then cannot be compiled -- an unknown filter, a bad
	// block -- and this is the same kind of finding.
	err := errs.New(errs.TemplateAssertionError, "%s", first.Error())
	err.Line, err.Name, err.Source = first.Line, name, source
	return err
}
