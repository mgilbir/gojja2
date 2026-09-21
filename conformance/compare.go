// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package conformance

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/mgilbir/gojja2"
	"github.com/mgilbir/gojja2/errs"
)

// Expected is what CPython jinja2 did, however it was obtained: read from a
// recorded golden, or asked of a live oracle.
type Expected struct {
	OK     bool
	Output string
	Error  *GoldenError
}

// Equal reports whether two recorded answers say the same thing, which is how
// a per-version override is checked for having anything to record.
func (e Expected) Equal(o Expected) bool {
	if e.OK != o.OK || e.Output != o.Output {
		return false
	}
	if (e.Error == nil) != (o.Error == nil) {
		return false
	}
	if e.Error == nil {
		return true
	}
	return e.Error.Type == o.Error.Type && e.Error.Message == o.Error.Message
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

// ResourceError reports whether gojja2 stopped a render on one of its own
// safety bounds -- the iteration budget, the output budget, or the context
// the caller gave it -- rather than on the template's own terms.
//
// It is the mirror of the oracle's Resource flag. CPython jinja2 has no such
// bounds (see docs/divergences.md), so the two sides give up in different
// ways on a template that asks for unbounded work, and grading that would
// report a divergence where there is no conformance question to answer.
func ResourceError(err error) bool {
	return errors.Is(err, gojja2.ErrTooManyIterations) ||
		errors.Is(err, gojja2.ErrOutputTooLarge) ||
		errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, context.Canceled)
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
