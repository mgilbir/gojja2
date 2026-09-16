// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package conformance

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/mgilbir/gojja2/errs"
)

// Expected is what CPython jinja2 did, however it was obtained: read from a
// recorded golden, or asked of a live oracle.
type Expected struct {
	OK     bool
	Output string
	Error  *GoldenError
}

// Expected converts a recorded golden.
func (g *Golden) Expected() Expected {
	return Expected{OK: g.OK, Output: g.Output, Error: g.Error}
}

// Expected converts a live oracle answer.
func (r *OracleResult) Expected() Expected {
	return Expected{OK: r.OK, Output: r.Output, Error: r.Error}
}

// Divergence describes one way gojja2 disagreed with CPython.
type Divergence struct {
	// Kind groups divergences so a shrinker can tell whether a reduction
	// still reproduces the same problem.
	Kind   string
	Detail string
}

func (d *Divergence) String() string { return d.Kind + ": " + d.Detail }

// Divergence kinds.
const (
	KindOutput          = "output"
	KindUnexpectedError = "unexpected-error"
	KindMissingError    = "missing-error"
	KindErrorClass      = "error-class"
	KindErrorMessage    = "error-message"
	KindErrorLine       = "error-line"
	KindPanic           = "panic"
)

// Compare grades one render against CPython's. It returns nil when they agree.
func Compare(want Expected, out string, err error) *Divergence {
	switch {
	case want.OK && err != nil:
		return &Divergence{KindUnexpectedError, fmt.Sprintf(
			"jinja2 rendered %q\ngojja2 raised %s: %v", want.Output, errs.KindOf(err), err)}

	case want.OK:
		if out == want.Output {
			return nil
		}
		return &Divergence{KindOutput, fmt.Sprintf(
			"jinja2: %q\ngojja2: %q", want.Output, out)}

	case err == nil:
		return &Divergence{KindMissingError, fmt.Sprintf(
			"jinja2 raised %s: %s\ngojja2 rendered %q",
			want.Error.Type, want.Error.Message, out)}

	default:
		got := errs.KindOf(err).String()
		if got != want.Error.Type {
			return &Divergence{KindErrorClass, fmt.Sprintf(
				"jinja2: %s: %s\ngojja2: %s: %v",
				want.Error.Type, want.Error.Message, got, err)}
		}
		if err.Error() != want.Error.Message {
			return &Divergence{KindErrorMessage, fmt.Sprintf(
				"(%s)\njinja2: %s\ngojja2: %s", got, want.Error.Message, err.Error())}
		}
		var e *errs.Error
		if want.Error.Lineno != 0 && errors.As(err, &e) && e.Line != want.Error.Lineno {
			return &Divergence{KindErrorLine, fmt.Sprintf(
				"jinja2 says line %d, gojja2 says line %d", want.Error.Lineno, e.Line)}
		}
		return nil
	}
}

// addressRe matches the repr of a Python object that embeds its address.
var addressRe = regexp.MustCompile(`(?i)0x[0-9a-f]{6,}`)

// Comparable reports whether a result can be graded at all.
//
// Some renders are not reproducible by anything, CPython included: a repr that
// embeds a memory address differs between two runs of the same interpreter,
// and a result that hit the oracle's time or memory limit says nothing about
// the language. Grading those would produce noise that looks like findings.
func Comparable(r *OracleResult) bool {
	if r.Resource {
		return false
	}
	text := r.Output
	if r.Error != nil {
		text = r.Error.Message
	}
	lower := strings.ToLower(text)
	return !addressRe.MatchString(text) &&
		!strings.Contains(lower, "<generator object") &&
		!strings.Contains(lower, " object at ")
}
