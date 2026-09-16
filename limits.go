// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"context"
	"errors"

	"github.com/mgilbir/gojja2/errs"
)

// Errors a render returns when it runs out of budget. They are wrapped, so a
// caller uses errors.Is: a context deadline surfaces as context.DeadlineExceeded
// and a cancellation as context.Canceled, exactly as elsewhere in Go.
var (
	// ErrTooManyIterations reports that a render exceeded the loop
	// iteration budget. See [WithMaxIterations].
	ErrTooManyIterations = errors.New("gojja2: too many loop iterations")
	// ErrOutputTooLarge reports that a render wrote more than the output
	// budget allows. See [WithMaxOutputBytes].
	ErrOutputTooLarge = errors.New("gojja2: rendered output too large")
)

// Default budgets. CPython's jinja2 has neither; see docs/divergences.md.
//
// They are backstops for a caller who passes context.Background(), which is
// why they are generous rather than tight: a context deadline is the precise
// tool, and these only have to stop a render that would otherwise run until
// the machine gives up. Ten million iterations is a few seconds of work, and
// no template anyone writes on purpose comes near either number.
const (
	defaultMaxIterations  = 10_000_000
	defaultMaxOutputBytes = 256 << 20 // 256 MiB
)

// checkInterval is how often the context is consulted. Reading it on every
// iteration would put a mutex acquisition in the inner loop of every template;
// every few thousand is often enough that a cancelled render stops promptly.
const checkInterval = 1 << 12

// budget bounds the work of a single render.
//
// It is created once by the outermost render and threaded through every
// nested one. That matters: {% include %} builds a fresh State, so a budget
// that lived only on the State would reset at each include and bound nothing
// -- the mistake the recursion depth counter made before it was threaded the
// same way.
type budget struct {
	done      <-chan struct{}
	ctx       context.Context
	steps     int64
	maxSteps  int64
	written   int64
	maxOutput int64
	// sinceCheck counts steps and writes since the context was last read.
	sinceCheck int
}

func newBudget(ctx context.Context, env *Environment) *budget {
	if ctx == nil {
		ctx = context.Background()
	}
	return &budget{
		done:      ctx.Done(),
		ctx:       ctx,
		maxSteps:  env.maxIterations,
		maxOutput: env.maxOutputBytes,
	}
}

// step charges one unit of work: a loop iteration, or one item pulled out of a
// sequence a filter is materialising.
func (b *budget) step() error {
	if b == nil {
		return nil
	}
	b.steps++
	if b.maxSteps > 0 && b.steps > b.maxSteps {
		return b.abort(ErrTooManyIterations,
			"render exceeded %d loop iterations", b.maxSteps)
	}
	return b.tick()
}

// steps charges n units at once, for a filter that already knows how many
// items it is about to walk.
func (b *budget) chargeSteps(n int) error {
	if b == nil {
		return nil
	}
	if n <= 1 {
		return b.step()
	}
	b.steps += int64(n)
	if b.maxSteps > 0 && b.steps > b.maxSteps {
		return b.abort(ErrTooManyIterations,
			"render exceeded %d loop iterations", b.maxSteps)
	}
	b.sinceCheck += n
	return b.tick()
}

// account charges n bytes of output.
//
// Every write is counted, wherever it lands: text captured by {% filter %}, a
// block {% set %} or a macro body is charged when it is captured and again
// when the captured text is written on. That over-counts a value that passes
// through two buffers, and it is deliberate -- the buffers are the memory the
// bound exists to protect, so they are what has to be counted.
func (b *budget) account(n int) error {
	if b == nil {
		return nil
	}
	b.written += int64(n)
	if b.maxOutput > 0 && b.written > b.maxOutput {
		return b.abort(ErrOutputTooLarge,
			"render wrote more than %d bytes of output", b.maxOutput)
	}
	b.sinceCheck += n
	return b.tick()
}

// tick consults the context, but only every checkInterval units.
func (b *budget) tick() error {
	b.sinceCheck++
	if b.sinceCheck < checkInterval {
		return nil
	}
	b.sinceCheck = 0
	return b.checkContext()
}

// checkContext reports a cancelled or expired context.
func (b *budget) checkContext() error {
	if b.done == nil {
		return nil
	}
	select {
	case <-b.done:
		err := b.ctx.Err()
		return b.abort(err, "render stopped: %s", err)
	default:
		return nil
	}
}

// abort builds the error a spent budget returns. The Kind is jinja2's
// TemplateRuntimeError because that is the class a caller of jinja2 would
// catch; cause is what errors.Is sees.
func (b *budget) abort(cause error, format string, args ...any) error {
	e := errs.New(errs.TemplateRuntimeError, format, args...)
	e.Cause = cause
	return e
}
