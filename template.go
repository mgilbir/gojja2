// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"bufio"
	"context"
	"errors"
	"io"
	"strings"

	"github.com/mgilbir/gojja2/errs"
	"github.com/mgilbir/gojja2/internal/ast"
	"github.com/mgilbir/gojja2/value"
)

// Template is a compiled template.
type Template struct {
	env  *Environment
	name string
	// fromString marks a template compiled by Environment.FromString, which
	// has no name. The autoescape policy needs to tell that apart from a
	// named template whose name matches nothing: jinja2 passes None for the
	// first and escapes it by default.
	fromString bool
	source     string
	tree       *ast.Template
	blocks     map[string]*ast.Block
}

// Name returns the template's name, empty for one compiled from a string.
func (t *Template) Name() string { return t.name }

// Render renders the template into w.
//
// Output is streamed: w sees text as the template produces it, so a template
// that fails partway will already have written what came before. Callers that
// must not emit a partial document should render into a buffer, or use
// [Template.RenderString].
//
// `{% include %}` is the exception: an included template is rendered in full
// before its text is written on, because a context-free include has to reach
// the enclosing function's stream rather than the current buffer. Peak memory
// therefore tracks the largest include, not the size of the write buffer.
//
// ctx bounds the render. Cancel it, or give it a deadline, and the render stops
// at the next loop iteration, output write, or filter yield point, and returns
// an error wrapping ctx.Err().
//
// Those are the points at which the context is read, so how promptly a render
// stops depends on reaching one. The built-in filters that do sustained work
// call [State.Poll] for exactly this reason; one registered with [Environment.AddFilter]
// that loops without writing output should do the same, or it is a region
// nothing can interrupt.
func (t *Template) Render(ctx context.Context, w io.Writer, vars map[string]any) error {
	return t.RenderValues(ctx, w, t.env.valuesFromGo(vars))
}

// RenderString renders the template and returns the result.
//
// Unlike [Template.Render] it is all-or-nothing: a render that fails returns
// an empty string rather than the text produced before the failure.
func (t *Template) RenderString(ctx context.Context, vars map[string]any) (string, error) {
	var out strings.Builder
	if err := t.RenderValues(ctx, &out, t.env.valuesFromGo(vars)); err != nil {
		return "", err
	}
	return out.String(), nil
}

// RenderValues renders into w with variables that are already template values,
// skipping the conversion from Go.
func (t *Template) RenderValues(ctx context.Context, w io.Writer, vars map[string]value.Value) (err error) {
	// A bufio.Writer keeps the many small writes a template makes from
	// becoming many small syscalls, and gives the render one place to
	// flush from.
	bw := bufio.NewWriter(w)
	defer func() {
		// Flush either way: a render that failed has still produced
		// whatever came before the failure, and leaving it in the buffer
		// would make the amount w receives depend on where the buffer
		// happened to be.
		if ferr := bw.Flush(); err == nil {
			err = ferr
		}
	}()
	defer catchPanic(&err)
	return t.renderInto(&stringWriter{w: bw}, vars, 0, newBudget(ctx, t.env))
}

// catchPanic turns a panic into a render error.
//
// This is a backstop, not a licence. A template engine renders input its caller
// does not control, so unwinding the caller's goroutine is never the right
// answer to a bad template -- the render failed, so the render should say so.
// Every panic that reaches here is a bug in gojja2, and the message says so,
// with the panic value kept so a report can name it.
func catchPanic(err *error) {
	r := recover()
	if r == nil {
		return
	}
	e := errs.New(errs.TemplateRuntimeError,
		"internal error in gojja2 (please report this): %v", r)
	e.Cause = ErrInternal
	*err = e
}

// ErrInternal marks a render that failed because gojja2 panicked. Reaching it
// always means a bug here rather than in the template.
var ErrInternal = errors.New("gojja2: internal error")

func (e *Environment) valuesFromGo(vars map[string]any) map[string]value.Value {
	values := make(map[string]value.Value, len(vars))
	for k, v := range vars {
		values[k] = value.FromGoWith(v, e.methods)
	}
	return values
}

// renderInto is the render every entry point funnels through.
//
// The depth and the budget both have to cross the template boundary: an
// `{% include %}` renders into a fresh State, so a counter that started at
// zero each time would never fire and a self-including template would take the
// stack out instead.
func (t *Template) renderInto(out writer, vars map[string]value.Value, depth int, b *budget) error {
	st := t.newState(vars, depth, b)
	ex := &exec{st: st, sc: st.ctx, out: out, stream: out, autoescape: st.autoescape}

	if err := ex.execBody(t.tree.Body); err != nil {
		return err
	}
	if err := st.takeDeferred(); err != nil {
		return err
	}
	// A template that extends renders nothing itself beyond whatever came
	// before the extends tag; the parent is rendered afterwards, with the
	// blocks the child registered. The loop handles a chain of any depth.
	for st.parent != nil {
		parent := st.parent
		st.parent = nil
		prev := st.tmpl
		st.tmpl = parent
		err := ex.execBody(parent.tree.Body)
		st.tmpl = prev
		if err != nil {
			return err
		}
		if err := st.takeDeferred(); err != nil {
			return err
		}
	}
	return nil
}

// writer is where an exec sends output. A *strings.Builder satisfies it, which
// is what a {% filter %} buffer or a captured macro body uses; the root of a
// render uses a stringWriter over the caller's io.Writer.
type writer interface {
	WriteString(s string) (int, error)
}

// stringWriter adapts an io.Writer to the writer interface.
type stringWriter struct{ w io.Writer }

func (s *stringWriter) WriteString(str string) (int, error) { return io.WriteString(s.w, str) }

// blockEntry is one definition of a block in the inheritance chain. Index 0 of
// a name's slice is the most derived definition, which is the one that
// renders; super() steps toward the base.
type blockEntry struct {
	tmpl *Template
	node *ast.Block
}

// State is the per-render state a filter, test or global may need.
type State struct {
	env  *Environment
	tmpl *Template // the template whose body is executing
	root *Template // the template the render started from

	globals *scope
	// ctx is the template's root frame: the render arguments sit in a
	// scope beneath it, so a top-level `{% set %}` shadows an argument of
	// the same name from the start of the render.
	ctx *scope
	// contextVars is that underlying scope -- jinja2's context.vars. A
	// top-level assignment is written here as well as into the frame, and
	// blocks resolve against it, which is why a block sees an argument the
	// root frame has shadowed but not yet assigned.
	contextVars *scope

	blocks     map[string][]blockEntry
	autoescape bool
	parent     *Template

	// exports holds the names a top-level binding made visible, which is
	// what `{% import %}` exposes and what `{% from %}` looks in.
	exports map[string]bool
	// depth bounds include/extends/macro nesting.
	depth int
	// deferred holds an error raised somewhere that could not return one.
	//
	// Python's __str__ may raise; Go's Strer cannot. Rendering a block
	// through `{{ self.body }}` goes through Str, so the only options were
	// to discard the block's error -- which silently rendered "" and
	// reported success -- or to park it here and have the statement loop
	// pick it up. Parking it catches every path that stringifies a value,
	// including filters and `~`, rather than only the print tag.
	deferred error
	// budget bounds the work of the whole render. It is shared with every
	// nested render, so an {% include %} cannot start a fresh allowance.
	budget *budget
}

// Context returns the context the render was started with. A filter or global
// that does its own work should consult it, and stop when it is done.
func (s *State) Context() context.Context {
	if s.budget == nil || s.budget.ctx == nil {
		return context.Background()
	}
	return s.budget.ctx
}

// Step charges n units of work against the render's budget, and reports an
// error once the budget is spent or the context is done. A filter that walks a
// sequence of caller-controlled length should call it.
func (s *State) Step(n int) error {
	// A nil State reaches here from constant folding, which runs without a
	// render and so has no budget to charge.
	if s == nil || s.budget == nil {
		return nil
	}
	return s.budget.chargeSteps(n)
}

// Poll consults the render's context without charging anything against the
// budget, and reports an error once it is cancelled or expired.
//
// It is the yield point for a filter, test or global doing sustained work that
// neither iterates a sequence nor writes output. The context is only read from
// inside the budget, so a call that does neither is a region nothing can
// interrupt: a single filter once overran a one-second deadline by seventeen
// seconds, and the error it eventually returned was the output bound rather
// than the deadline. Calling this every few thousand units of work is cheap --
// it is an increment and a comparison until the counter wraps round.
func (s *State) Poll() error {
	if s == nil || s.budget == nil {
		return nil
	}
	return s.budget.tick()
}

// Env returns the environment the render is running under.
func (s *State) Env() *Environment { return s.env }

// Name returns the name of the template currently executing.
func (s *State) Name() string { return s.tmpl.name }

// Autoescape reports whether the render is currently escaping output.
func (s *State) Autoescape() bool { return s.autoescape }

// Resolve looks a name up in the template context.
func (s *State) Resolve(name string) (value.Value, bool) { return s.ctx.lookup(name) }

// Undefined builds an undefined value under the environment's policy.
func (s *State) Undefined(v value.Value) value.Value {
	return v.WithBehavior(s.env.undefined)
}

// newState builds the per-render state for one template.
//
// depth and budget are parameters rather than fields a caller fills in
// afterwards, because a nested render that forgets the budget does not get a
// fresh allowance -- it gets none at all, and no context either, since the
// context is read through the budget. `{% import %}` built its State by hand
// and omitted it, so an imported template ran unbounded and uninterruptible
// while the including one was bounded. Making both arguments is what stops a
// third construction site from doing it again.
func (t *Template) newState(vars map[string]value.Value, depth int, b *budget) *State {
	globals := &scope{vars: t.env.globals}
	// The render arguments get a scope of their own, below the one the
	// template writes into. A top-level `{% set %}` then shadows an
	// argument of the same name from the start of the render, which is
	// what makes `{% for %}{{ x }}{% endfor %}{% set x = 1 %}` render
	// nothing even when x was passed in.
	arguments := newScope(globals)
	for k, v := range vars {
		arguments.vars[k] = v
	}
	ctx := newScope(arguments)

	blocks := make(map[string][]blockEntry, len(t.blocks))
	for name, node := range t.blocks {
		blocks[name] = []blockEntry{{tmpl: t, node: node}}
	}

	st := &State{
		env:         t.env,
		tmpl:        t,
		root:        t,
		globals:     globals,
		ctx:         ctx,
		contextVars: arguments,
		blocks:      blocks,
		autoescape:  t.env.escapes(t.name, t.fromString),
		depth:       depth,
		budget:      b,
	}
	declareFrameLocals(ctx, st, t.tree.Body)
	return st
}

// RecursionMessage is what CPython reports when the interpreter runs out of
// stack, and therefore what a template that recurses without a base case must
// report here.
//
// The wording names where inside CPython the limit was hit, which says nothing
// about a Go program -- but the actionable half is true in both, and anyone
// diffing the two implementations should not have to filter this out. The
// configured limit, which our own wording used to carry, is on the error's
// Limit field instead.
const RecursionMessage = "maximum recursion depth exceeded while calling a Python object"

// RecursionMessageComparison is the variant CPython produces when the stack
// ran out inside a comparison, which is what an inheritance cycle hits.
//
// It is defined in the value package, which raises it for a comparison that
// descends too far, and re-exported here so there is one spelling of it.
const RecursionMessageComparison = value.RecursionMessageComparison

// enter bounds template recursion. The limit is a safety control: a template
// that includes itself would otherwise exhaust the stack.
func (s *State) enter() error {
	s.depth++
	if s.depth > s.env.maxRecursion {
		e := errs.New(errs.RecursionError, "%s", RecursionMessage)
		e.Limit = s.env.maxRecursion
		return e
	}
	return nil
}

// enterExtends is enter for an {% extends %} chain, which CPython reports with
// its comparison wording because the cycle is detected while matching names.
func (s *State) enterExtends() error {
	err := s.enter()
	var e *errs.Error
	if errors.As(err, &e) && e.Kind == errs.RecursionError {
		e.Msg = RecursionMessageComparison
	}
	return err
}

func (s *State) leave() { s.depth-- }

// deferError records an error raised where none could be returned. The first
// one wins: it is the cause, and the ones after it are likely consequences.
func (s *State) deferError(err error) {
	if s != nil && s.deferred == nil {
		s.deferred = err
	}
}

// takeDeferred returns and clears any parked error.
func (s *State) takeDeferred() error {
	if s == nil {
		return nil
	}
	err := s.deferred
	s.deferred = nil
	return err
}
