// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"errors"
	"iter"
	"runtime"
	"strings"

	"github.com/mgilbir/gojja2/errs"
	"github.com/mgilbir/gojja2/internal/ast"
	"github.com/mgilbir/gojja2/value"
)

// exec walks the AST, writing output as it goes.
//
// A tree-walking interpreter rather than a bytecode compiler: jinja2 is the
// specification and its semantics are defined by what its generated Python
// does, so keeping the evaluator shaped like the tree keeps the correspondence
// checkable.
type exec struct {
	st  *State
	sc  *scope
	out writer
	// stream is the output of the enclosing *function* -- the template
	// root, a block, or a macro body. It differs from out only inside a
	// {% filter %} or a block {% set %}, which buffer within a function.
	//
	// jinja2 compiles `{% include ... without context %}` to a yield
	// straight into the function's own stream, so its output escapes any
	// such buffer: `{% filter escape %}{% include "x" without context %}`
	// leaves the included text unescaped, and emits it first. That is an
	// artefact of jinja2 caching a context-free module's body, but it is
	// observable, so it is reproduced.
	stream writer
	// autoescape is per-frame so `{% autoescape %}` can change it for a
	// span without disturbing the rest of the render.
	autoescape bool
	// volatileEscape marks the span of an {% autoescape %} whose argument
	// is not a literal. jinja2 calls that a volatile eval context, and one
	// operator behaves differently inside it; see evalConcat. It is
	// lexical and never resets: a nested block, a loop body and a macro
	// defined in here are all still inside it.
	volatileEscape bool

	// blockName and blockIndex locate the block being rendered, for super().
	// blockScope is the scope it was entered with -- the loop variable and
	// `loop` for a `scoped` block, nil otherwise -- which super() has to
	// render the parent with: jinja2 builds its BlockReference from the
	// *current* context, so a scoped block's super() sees what the block
	// itself sees. Without it `{% block a scoped %}{{ super() }}` inside a
	// parent's `{% for %}` rendered nothing.
	blockName  string
	blockIndex int
	blockScope *scope
	// loop is the innermost `loop` value, for recursive loop() calls.
	loop value.Value
	// chunks counts the pieces written into this frame's output, which is
	// what jinja2's concat numbers when one of them is not a string. It is
	// a pointer because child shares a frame's output by copying the exec:
	// a loop body writing into its parent's stream has to advance the
	// parent's count, not a copy of it.
	chunks *int
}

// Loop control travels as sentinel errors, which keeps the happy path free of
// a status return on every statement.
var (
	errBreakLoop    = errors.New("break")
	errContinueLoop = errors.New("continue")
)

// child returns a frame sharing output but with its own scope.
// pyVersion is the interpreter this render reproduces; see State.PythonVersion.
func (ex *exec) pyVersion() PythonVersion { return ex.st.PythonVersion() }

func (ex *exec) child(sc *scope) *exec {
	next := *ex
	next.sc = sc
	return &next
}

// capture runs fn with output redirected into a fresh buffer. The enclosing
// function's stream is left alone, because a buffer is not a function.
func (ex *exec) capture(sc *scope, fn func(*exec) error) (string, error) {
	var buf strings.Builder
	sub := *ex
	sub.sc = sc
	sub.out = &buf
	sub.chunks = nil
	// The template whose code runs in the buffer decides, and not only the
	// frame it was called from: a macro imported from a template that has a
	// filter block is counted although its caller's template has none.
	if ex.chunks != nil || (ex.st.tmpl != nil && ex.st.tmpl.countsChunks) {
		sub.chunks = new(int)
	}
	if err := fn(&sub); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// captureFunction is capture for a new function body -- a macro or a block --
// where the stream moves with the buffer.
func (ex *exec) captureFunction(sc *scope, fn func(*exec) error) (string, error) {
	return ex.capture(sc, func(sub *exec) error {
		sub.stream = sub.out
		return fn(sub)
	})
}

// write sends text to this frame's output, charging it against the render's
// budget first. Every byte a template produces goes through here.
func (ex *exec) write(s string) error { return ex.writeTo(ex.out, s) }

// chunkCount is how many pieces this frame's output already holds.
func (ex *exec) chunkCount() int {
	if ex.chunks == nil {
		return 0
	}
	return *ex.chunks
}

// writeTo is write aimed somewhere other than this frame's output, which only
// a context-free {% include %} needs.
func (ex *exec) writeTo(w writer, s string) error {
	// The nil check first: it is almost always nil, and comparing two
	// interface values is not free on a path that runs once per write.
	if ex.chunks != nil && w == ex.out {
		*ex.chunks++
	}
	return ex.writeToUncounted(w, s)
}

// writeToUncounted is writeTo for text whose pieces were already counted, as
// the output of an include or a block is by the frame that rendered it.
func (ex *exec) writeToUncounted(w writer, s string) error {
	if err := ex.st.budget.account(len(s)); err != nil {
		return err
	}
	_, err := w.WriteString(s)
	return err
}

func (ex *exec) execBody(body []ast.Stmt) error {
	for _, stmt := range body {
		if err := ex.execStmt(stmt); err != nil {
			return err
		}
	}
	return nil
}

func (ex *exec) execStmt(stmt ast.Stmt) error {
	err := ex.execStmtInner(stmt)
	if err != nil && !errors.Is(err, errBreakLoop) && !errors.Is(err, errContinueLoop) {
		err = errs.At(err, ex.st.tmpl.name, stmt.Line())
	}
	return err
}

func (ex *exec) execStmtInner(stmt ast.Stmt) error {
	switch n := stmt.(type) {
	case *ast.Output:
		return ex.execOutput(n)
	case *ast.If:
		return ex.execIf(n)
	case *ast.For:
		return ex.execFor(n)
	case *ast.Assign:
		return ex.execAssign(n)
	case *ast.AssignBlock:
		return ex.execAssignBlock(n)
	case *ast.With:
		return ex.execWith(n)
	case *ast.Macro:
		return ex.execMacro(n)
	case *ast.CallBlock:
		return ex.execCallBlock(n)
	case *ast.FilterBlock:
		return ex.execFilterBlock(n)
	case *ast.Block:
		return ex.execBlock(n)
	case *ast.Extends:
		return ex.execExtends(n)
	case *ast.Include:
		return ex.execInclude(n)
	case *ast.Import:
		return ex.execImport(n)
	case *ast.FromImport:
		return ex.execFromImport(n)
	case *ast.ExprStmt:
		_, err := ex.eval(n.Node)
		return err
	case *ast.AutoescapeBlock:
		return ex.execAutoescape(n)
	case *ast.Break:
		return errBreakLoop
	case *ast.Continue:
		return errContinueLoop
	}
	return errs.New(errs.TemplateRuntimeError, "cannot execute %s", stmt.TypeName())
}

// execOutput writes a run of template data and print expressions.
//
// Output is suppressed once an `{% extends %}` has run, because the child's
// own body no longer contributes anything: only its blocks do.
func (ex *exec) execOutput(n *ast.Output) error {
	if ex.st.parent != nil {
		return nil
	}
	for _, node := range n.Nodes {
		if data, ok := node.(*ast.TemplateData); ok {
			// Literal template text is the author's markup and is
			// never escaped.
			if err := ex.write(data.Data); err != nil {
				return err
			}
			continue
		}
		v, err := ex.eval(node)
		if err != nil {
			return err
		}
		text, err := ex.renderPrint(v)
		if err != nil {
			return errs.At(err, ex.st.tmpl.name, node.Line())
		}
		if err := ex.write(text); err != nil {
			return err
		}
	}
	return nil
}

// renderPrint turns the value of a print tag into the text that reaches the
// output.
//
// It is the only path finalize runs on. jinja2 applies it from visit_Output
// and nowhere else, so a construct that produces text by *capturing* it --
// a {% filter %} block, a {% call %} block -- hands over what it captured
// untouched. Running it there too applied it twice to anything printed inside
// such a block, and once to a block containing no print at all:
// `{% filter upper %}plain{% endfilter %}` came back finalized.
func (ex *exec) renderPrint(v value.Value) (string, error) {
	if ex.st.env.finalize != nil {
		v = ex.st.env.finalize(v)
	}
	return ex.renderValue(v)
}

// renderValue turns a value into the text that reaches the output.
// renderValueRaw is str(v) with the Undefined class applied and no escaping:
// what Python's str() does to a value, including raising for a StrictUndefined.
func (ex *exec) renderValueRaw(v value.Value) (string, error) {
	if err := value.StrictRefusal(v); err != nil {
		return "", err
	}
	return value.StrFor(v, ex.pyVersion()), nil
}

func (ex *exec) renderValue(v value.Value) (string, error) {
	text, err := ex.renderValueRaw(v)
	if err != nil {
		return "", err
	}
	if ex.autoescape && !v.IsSafe() {
		// Output is str(x) plainly and escape(x) when autoescaping, so
		// a value that carries its own escaped form hands that over
		// here and only here -- `{{ m }}` is the module's body where
		// `{{ m ~ "" }}` is str(m), escaped as a whole.
		if err := value.HTMLRefusal(v); err != nil {
			return "", err
		}
		if html, ok := value.HTML(v); ok {
			text = html
		} else {
			text = escapeHTML(text)
		}
	}
	return text, nil
}

// bodyRetainsScope reports whether running body could leave something holding
// a reference to the frame it ran in.
//
// A macro closes over the scope it was written in -- that is what makes its
// free names resolve there -- and a {% call %} block is compiled into one. Any
// other construct that binds names builds its own scope and reads through the
// parent chain, so nothing else outlives the frame; a `{% block scoped %}`
// copies the bindings it needs rather than keeping the scope.
//
// The walk is over the whole body, not its top level: a macro inside a nested
// loop captures that loop's scope, whose parent chain reaches this one.
func bodyRetainsScope(body []ast.Stmt) bool {
	for _, stmt := range body {
		switch n := stmt.(type) {
		case *ast.Macro, *ast.CallBlock:
			return true
		case *ast.For:
			if bodyRetainsScope(n.Body) || bodyRetainsScope(n.Else) {
				return true
			}
		case *ast.If:
			if bodyRetainsScope(n.Body) || bodyRetainsScope(n.Else) {
				return true
			}
			for _, elif := range n.Elif {
				if bodyRetainsScope(elif.Body) {
					return true
				}
			}
		case *ast.With:
			if bodyRetainsScope(n.Body) {
				return true
			}
		case *ast.FilterBlock:
			if bodyRetainsScope(n.Body) {
				return true
			}
		case *ast.AssignBlock:
			if bodyRetainsScope(n.Body) {
				return true
			}
		case *ast.Block:
			if bodyRetainsScope(n.Body) {
				return true
			}
		case *ast.AutoescapeBlock:
			if bodyRetainsScope(n.Body) {
				return true
			}
		}
	}
	return false
}

// hasFilterBlock reports whether body contains a construct that needs to know
// how many pieces of output the frame holds: a {% filter %}, which asks, and an
// {% include %} or {% extends %}, whose template may contain one and is
// numbered from where this frame had got to.
func hasFilterBlock(body []ast.Stmt) bool {
	for _, stmt := range body {
		switch n := stmt.(type) {
		case *ast.FilterBlock, *ast.Include, *ast.Extends:
			return true
		case *ast.For:
			if hasFilterBlock(n.Body) || hasFilterBlock(n.Else) {
				return true
			}
		case *ast.If:
			if hasFilterBlock(n.Body) || hasFilterBlock(n.Else) {
				return true
			}
			for _, elif := range n.Elif {
				if hasFilterBlock(elif.Body) {
					return true
				}
			}
		case *ast.With:
			if hasFilterBlock(n.Body) {
				return true
			}
		case *ast.AssignBlock:
			if hasFilterBlock(n.Body) {
				return true
			}
		case *ast.Macro:
			if hasFilterBlock(n.Body) {
				return true
			}
		case *ast.CallBlock:
			if hasFilterBlock(n.Body) {
				return true
			}
		case *ast.Block:
			if hasFilterBlock(n.Body) {
				return true
			}
		case *ast.AutoescapeBlock:
			if hasFilterBlock(n.Body) {
				return true
			}
		}
	}
	return false
}

func (ex *exec) execIf(n *ast.If) error {
	ok, err := ex.truth(n.Test)
	if err != nil {
		return err
	}
	if ok {
		// `if` introduces no scope: a `{% set %}` inside it is visible
		// afterwards, which is jinja2's behaviour and not Python's.
		return ex.execBody(n.Body)
	}
	for _, elif := range n.Elif {
		ok, err := ex.truth(elif.Test)
		if err != nil {
			return err
		}
		if ok {
			return ex.execBody(elif.Body)
		}
	}
	return ex.execBody(n.Else)
}

func (ex *exec) truth(e ast.Expr) (bool, error) {
	v, err := ex.eval(e)
	if err != nil {
		return false, err
	}
	return value.IsTrue(v)
}

func (ex *exec) execFor(n *ast.For) error {
	iterable, err := ex.eval(n.Iter)
	if err != nil {
		return err
	}
	return ex.runLoop(n, iterable, 1)
}

// runLoop drives one level of a for loop. depth is 1 for the outermost pass
// and increases when a recursive loop calls loop().
func (ex *exec) runLoop(n *ast.For, iterable value.Value, depth int) error {
	src, err := ex.loopSourceFor(n, iterable)
	if err != nil {
		return err
	}

	loop := &loopObject{src: src, depth: depth, undefined: ex.st.env.undefined}
	if n.Recursive {
		loop.recurse = func(items value.Value, depth int) (value.Value, error) {
			// A recursive loop can descend forever on cyclic data,
			// so it is bounded by the same counter as include and
			// macro nesting rather than by the Go stack.
			if err := ex.st.enter(); err != nil {
				return value.Undefined, err
			}
			defer ex.st.leave()
			text, err := ex.capture(ex.sc, func(sub *exec) error {
				return sub.runLoop(n, items, depth)
			})
			if err != nil {
				return value.Undefined, err
			}
			return markup(text, ex.autoescape), nil
		}
	}
	loopValue := value.FromObject(loop)

	// The cursor lives on the loop object rather than in this loop, because
	// `loop` is the iterator: a body that consumes it -- `{{ loop|list }}`
	// -- advances this walk, and the walk has to see that.
	// completed is jinja2's own iteration indicator, and it says more than
	// "the loop ran": jinja2 writes it at the *end* of the loop body, so a
	// pass that left early through break or continue never clears it. The
	// else branch therefore runs unless some pass reached the body's end --
	// `{% for i in seq %}{% continue %}{% else %}E{% endfor %}` prints E.
	// Setting it on entry instead reads as the same thing until loopcontrols
	// is enabled, which is why it was that for so long.
	completed := false
	// broke records a `{% break %}`, which ends the walk without ending the
	// statement.
	broke := false
	// A body that cannot let its scope outlive the iteration gets one
	// frame reused for the whole loop instead of one per pass. See
	// bodyRetainsScope: only a macro keeps a reference to the scope it was
	// written in, so only a body containing one has to be given a fresh
	// frame each time.
	var reuse *exec
	if !bodyRetainsScope(n.Body) {
		reuse = ex.child(newScope(ex.sc))
	}
	frameNamesForBody := ex.st.root.frameLocalsOf(n, n.Body)
	for loop.index = 0; src.has(loop.index); loop.index++ {
		if err := src.err(); err != nil {
			return err
		}
		if err := ex.st.budget.step(); err != nil {
			return err
		}
		// Each iteration gets a fresh scope, so a `{% set %}` in the
		// body does not carry into the next pass -- jinja2 rebinds
		// every body-assigned symbol from the enclosing scope at the
		// top of each iteration, which amounts to the same thing.
		var body *exec
		if reuse != nil {
			// Reset to exactly what ex.child(newScope(ex.sc)) would
			// have built, in the frame already allocated.
			sc := reuse.sc
			*sc = scope{parent: ex.sc}
			*reuse = *ex
			reuse.sc = sc
			body = reuse
		} else {
			body = ex.child(newScope(ex.sc))
		}
		body.loop = loopValue
		body.sc.set("loop", loopValue)
		if err := declareFrameNames(body.sc, ex.st, frameNamesForBody, body.sc.parent); err != nil {
			return err
		}

		if err := body.assign(n.Target, src.at(loop.index)); err != nil {
			return err
		}
		err := body.execBody(n.Body)
		switch {
		case errors.Is(err, errBreakLoop):
			// A break leaves the loop but not the statement: the
			// else branch still asks whether any pass finished, so
			// breaking out of the first one runs it.
			broke = true
		case errors.Is(err, errContinueLoop):
			continue
		case err != nil:
			return err
		default:
			completed = true
		}
		if broke {
			break
		}
	}
	// A body that consumed the loop *object* pulls through this same source
	// -- `{{ loop|length }}`, `{{ dict(loop) }}`, `{% for a, b in loop %}`,
	// even `{{ loop|string }}`, whose repr asks for the length -- and a
	// `{% break %}` would then leave the failure unreported. jinja2 raises it
	// where the consumption happened, so it is taken here rather than left to
	// the check below, which a break deliberately skips.
	if err := ex.st.takeLoopFailure(); err != nil {
		return err
	}
	// A filter that failed part way stops the loop rather than ending it.
	// A break stops it *before* that pull, so there is nothing to report.
	if err := src.err(); err != nil && !broke {
		return err
	}
	// The else branch runs when no pass finished, which is what jinja2
	// tracks rather than asking the source how long it is -- asking would run
	// a filtered loop's test over every item before the first pass.
	if !completed {
		return ex.execBody(n.Else)
	}
	return nil
}

// loopSourceFor resolves the sequence a loop walks, applying the `if` filter
// when there is one. A filtered loop must be materialised, because its length
// and indices count only the items that survive.
func (ex *exec) loopSourceFor(n *ast.For, iterable value.Value) (loopSource, error) {
	if n.Test == nil {
		return makeLoopSource(ex.st, iterable)
	}
	seq, err := liveValues(iterable)
	if err != nil {
		return nil, err
	}
	// Pulled one item at a time, as jinja2's filter generator is: the test
	// runs between the body's passes and therefore sees what the body did --
	// including what it did to the list being walked, which is why the walk
	// is live rather than over a snapshot.
	nextItem, stop := iter.Pull(seq)
	// The test runs between pulls and can resize the source, so the same
	// guard the unfiltered loop gets applies here -- checked after every
	// pull rather than once a pass, because a test that answers false
	// pulls again without the body running. See sizeGuard.
	guard := sizeGuard(iterable)
	if guard == nil {
		guard = func() error { return nil }
	}
	next := func() (value.Value, bool, error) {
		for {
			item, ok := nextItem()
			if err := guard(); err != nil {
				return value.Undefined, false, err
			}
			if !ok {
				return value.Undefined, false, nil
			}
			if err := ex.st.budget.step(); err != nil {
				return value.Undefined, false, err
			}
			filterScope := ex.child(newScope(ex.sc))
			if err := filterScope.assign(n.Target, item); err != nil {
				return value.Undefined, false, err
			}
			ok, err := filterScope.truth(n.Test)
			if err != nil {
				return value.Undefined, false, err
			}
			if ok {
				// jinja2 compiles a filtered loop into a
				// function that unpacks the target and yields
				// it straight back: `for a, b in fiter: if
				// cond: yield (a, b)`. So with a tuple target
				// the loop walks tuples, whatever the source
				// held -- which is what `loop.previtem`
				// reports and what an operator on it names.
				// Without a filter there is no such function
				// and the items are the source's own.
				if _, unpacks := n.Target.(*ast.Tuple); unpacks {
					repacked, err := filterScope.eval(n.Target)
					if err != nil {
						return value.Undefined, false, err
					}
					item = repacked
				}
				return item, true, nil
			}
		}
	}
	src := &filteredSource{next: next, report: ex.st.noteLoopFailure}
	// The pull holds a coroutine, which is released only when the sequence
	// runs out or stop is called. A loop that ends early -- `{% break %}`, or
	// a body that raises -- leaves it parked for good, one per render. It is
	// not stopped when the loop ends because the loop object can outlive the
	// loop, and what it has not yet pulled is still its to pull; it is stopped
	// once nothing can reach the source any more.
	runtime.AddCleanup(src, func(stop func()) { stop() }, stop)
	return src, nil
}

func (ex *exec) execAssign(n *ast.Assign) error {
	// Before the value, not with the assignment: see checkNamespaceTargets.
	// `{% set %}` with a body does not do this, and neither does jinja2.
	if err := ex.checkNamespaceTargets(n.Target); err != nil {
		return err
	}
	v, err := ex.eval(n.Node)
	if err != nil {
		return err
	}
	return ex.assign(n.Target, v)
}

func (ex *exec) execAssignBlock(n *ast.AssignBlock) error {
	inner := newScope(ex.sc)
	if err := declareFrameLocals(inner, ex.st, n, n.Body, inner.parent); err != nil {
		return err
	}
	text, err := ex.capture(inner, func(sub *exec) error {
		return sub.execBody(n.Body)
	})
	if err != nil {
		return err
	}

	// Two wraps, and jinja2 uses a different context for each. The filter's
	// *input* is `Markup(concat(buf))` when the frame this tag was compiled
	// in escapes -- visit_Filter decides that at compile time -- while the
	// *result* is `(Markup if context.eval_ctx.autoescape else identity)`,
	// which is the runtime context. With no filter there is only the
	// second. The two differ inside a block, whose own eval context is
	// fresh while the context's is whatever the parent has in force.
	v := markup(text, ex.st.autoescape)
	if n.Filter != nil {
		v = markup(text, ex.autoescape)
		// The filter chain was parsed with a nil input; the captured
		// body is what flows into it.
		v, err = ex.applyFilterChain(n.Filter, v)
		if err != nil {
			return err
		}
		// jinja2 wraps the *result* under autoescape --
		// `(Markup if autoescape else identity)(filter(...))` -- so a
		// filter that answers something other than a string leaves a
		// Markup of its str() behind, not the value. `{% set v | length
		// %}abc{% endset %}{{ v + 1 }}` is a TypeError there and was 4
		// here, and `{% set v | list %}` printed escaped quotes where
		// jinja2's Markup prints them as they are. With escaping off
		// the value keeps its type, which is what identity() means.
		// The *runtime* eval context decides, not the setting this tag
		// was compiled under: jinja2 writes `(Markup if
		// context.eval_ctx.autoescape else identity)(...)`, and the two
		// differ inside a block, whose own eval context is fresh while
		// the context's is whatever the parent has in force. A `{% set
		// v | list %}` in a block the base wrapped in `{% autoescape
		// false %}` keeps a plain string, which the block's own print
		// then escapes.
		if ex.st.autoescape {
			// Markup(x) stringifies, so a StrictUndefined raises here
			// rather than being assigned: `{% set v | first %}{%
			// endset %}` over an empty body is "No first item" under
			// strict *and* autoescape, and nothing at all without
			// escaping, where identity() keeps the undefined.
			text, err := ex.renderValueRaw(v)
			if err != nil {
				return err
			}
			v = value.Safe(text)
		}
	}
	// nsItem, not nsAttr: a `{% set %}` with a body assigns an *item*, and
	// does not check for a namespace first. See assignItem.
	return ex.assignWith(n.Target, v, nsItem)
}

func (ex *exec) execWith(n *ast.With) error {
	inner := newScope(ex.sc)
	sub := ex.child(inner)
	if err := declareFrameLocals(inner, ex.st, n, n.Body, inner.parent); err != nil {
		return err
	}
	for i, target := range n.Targets {
		// Values are evaluated in the enclosing scope, so
		// `{% with a = a %}` refers to the outer a.
		v, err := ex.eval(n.Values[i])
		if err != nil {
			return err
		}
		if err := sub.assign(target, v); err != nil {
			return err
		}
	}
	return sub.execBody(n.Body)
}

func (ex *exec) execAutoescape(n *ast.AutoescapeBlock) error {
	on, err := ex.truth(n.Value)
	if err != nil {
		return err
	}
	inner := newScope(ex.sc)
	// The body is a frame of its own -- jinja2 compiles {% autoescape %} as
	// a Scope -- so the names it assigns are its locals, and a read of one
	// *before* the assignment runs is undefined rather than a fall-through
	// to the context. Without this, `{% autoescape x %}{% for a in xs if m %}
	// {% endfor %}{% from "t" import m %}{% endautoescape %}` read the
	// context's m in the loop and ran it, where jinja2 reads nothing.
	if err := declareFrameLocals(inner, ex.st, n, n.Body, inner.parent); err != nil {
		return err
	}
	sub := ex.child(inner)
	sub.autoescape = on
	if _, constant := n.Value.(*ast.Const); !constant {
		sub.volatileEscape = true
	}
	// The setting moves on the state as well as on the exec, and for the
	// *dynamic* extent of the body rather than its lexical one. The two
	// are not the same thing and jinja2 uses both: text escapes by the
	// setting where it was written -- which for a macro body is where the
	// macro was defined -- while a filter is handed the context's eval
	// context and so escapes by the setting in force at the call. A join
	// inside a block or a macro invoked from here follows the block.
	//
	// Without this the state's copy never moved at all, and the five
	// filters that read it -- join, replace, xmlattr, urlize, tojson --
	// escaped by the template's setting wherever they were written: a join
	// inside {% autoescape false %} still escaped its delimiter, leaving a
	// visible `&amp;` in output that was meant not to be escaped.
	saved := ex.st.autoescape
	ex.st.autoescape = on
	defer func() { ex.st.autoescape = saved }()
	return sub.execBody(n.Body)
}

func (ex *exec) execMacro(n *ast.Macro) error {
	m, err := ex.makeMacro(n.Name, n, n.Args, n.Defaults)
	if err != nil {
		return err
	}
	ex.sc.set(n.Name, value.FromObject(m))
	if ex.sc == ex.st.ctx {
		ex.st.contextVars.set(n.Name, value.FromObject(m))
		ex.st.export(n.Name)
	}
	return nil
}

func (ex *exec) makeMacro(name string, node *ast.Macro, args []*ast.Name, defaults []ast.Expr) (*macroObject, error) {
	undeclared := findUndeclared(node.Body, "varargs", "kwargs", "caller")
	// A parameter of that name is the macro's own, and jinja2 leaves the
	// special out rather than binding over it: `{% macro m(kwargs) %}
	// {{ kwargs }}{% endmacro %}{{ m(1) }}` prints 1, not an empty dict.
	// It is jinja2's skip_special_params, and `caller` works the same way
	// one line further down -- the declared parameter takes the argument,
	// and its default if there is none.
	declared := map[string]bool{}
	for _, p := range args {
		declared[p.Name] = true
	}
	m := &macroObject{
		name:       name,
		node:       node,
		defaults:   defaults,
		defScope:   ex.sc,
		st:         ex.st,
		tmpl:       ex.st.tmpl,
		autoescape: ex.autoescape,
		// Volatility is lexical too, so a macro written inside a
		// volatile block keeps it wherever it is called from.
		volatileEscape: ex.volatileEscape,
		blockName:      ex.blockName,
		blockIndex:     ex.blockIndex,
		blockScope:     ex.blockScope,
		catchVarargs:   undeclared["varargs"] && !declared["varargs"],
		catchKwargs:    undeclared["kwargs"] && !declared["kwargs"],
		// The attribute jinja2 exposes as `accesses_caller`, which is
		// set for a declared `caller` too; what it must not do is add a
		// second parameter of that name. See bindMacroArgs.
		caller:         undeclared["caller"],
		explicitCaller: declared["caller"],
	}
	return m, nil
}

func (ex *exec) execFilterBlock(n *ast.FilterBlock) error {
	inner := newScope(ex.sc)
	if err := declareFrameLocals(inner, ex.st, n, n.Body, inner.parent); err != nil {
		return err
	}
	text, err := ex.capture(inner, func(sub *exec) error {
		return sub.execBody(n.Body)
	})
	if err != nil {
		return err
	}
	v, err := ex.applyFilterChain(n.Filter, markup(text, ex.autoescape))
	if err != nil {
		return err
	}
	// jinja2 writes a filter block's result into the output buffer as it
	// stands and joins the buffer at the end, so a filter that answers
	// with something other than a string fails there rather than being
	// rendered: `{% filter length %}abc{% endfilter %}` is a TypeError,
	// not "3". Only the writing form is affected -- `{% set s | length %}`
	// assigns the value and keeps it an int.
	//
	// The index jinja2 names is the position in *its* output buffer,
	// which depends on how its code generator grouped the surrounding
	// nodes rather than on anything about the template; see
	// docs/divergences.md.
	if v.Kind() != value.KindString {
		return errs.New(errs.TypeError,
			"sequence item %d: expected str instance, %s found", ex.chunkCount(), v.TypeName())
	}
	// Written as it stands, *not* through the output path: jinja2 appends the
	// filter's result to the buffer rather than emitting it, so it is neither
	// escaped nor finalized. It usually makes no difference, because a filter
	// over a Markup answers Markup -- but `{% filter join('-') %}` answers a
	// plain str holding the body's escapes, and escaping it again turned
	// `&#39;` into `&amp;#39;`.
	if err := ex.write(value.Str(v)); err != nil {
		return err
	}
	return nil
}

func (ex *exec) execBlock(n *ast.Block) error {
	if ex.st.parent != nil {
		// In a child template the block definition only registers; the
		// parent decides where it renders.
		return nil
	}
	chain := ex.st.blocks[n.Name]
	if len(chain) == 0 {
		return errs.New(errs.TemplateRuntimeError, "no block named %q", n.Name)
	}
	if chain[0].node.Required && len(chain) == 1 {
		return errs.New(errs.TemplateRuntimeError,
			"Required block %s not found", value.ReprFor(value.String(n.Name), ex.pyVersion()))
	}

	ref := &blockReference{st: ex.st, name: n.Name, index: 0}
	if n.Scoped {
		// A scoped block is handed the bindings of every enclosing
		// frame -- loop variables and `loop` at each level, `with`
		// targets, macro arguments -- on top of context.vars, nearest
		// frame winning: jinja2 derives the context from
		// Symbols.dump_stores, which walks the whole chain of frames.
		// The root frame is left out, so a name it owns but has not
		// assigned yet still resolves from the render arguments.
		scoped := newScope(ex.st.contextVars)
		var frames []*scope
		for cur := ex.sc; cur != nil && cur != ex.st.ctx; cur = cur.parent {
			frames = append(frames, cur)
		}
		if len(frames) == 0 {
			frames = append(frames, ex.sc)
		}
		for i := len(frames) - 1; i >= 0; i-- {
			frames[i].each(scoped.set)
		}
		ref.sc = scoped
	}
	// The tag yields the block's pieces into this frame's stream, so the block
	// counts into this frame's count -- unlike self.b() or super(), which
	// join their pieces into a value first.
	ref.chunks = ex.chunks
	v, err := ref.render()
	if err != nil {
		return err
	}
	return ex.writeToUncounted(ex.out, value.Str(v))
}

func (ex *exec) execExtends(n *ast.Extends) error {
	if ex.st.parent != nil {
		return errs.New(errs.TemplateRuntimeError, "extended multiple times")
	}
	// extends resolves through get_template, not select_template, so a
	// list of candidates is an unhashable cache key rather than a choice.
	// include is the tag that accepts a list.
	parent, err := ex.loadTemplateName(n.Template)
	if err != nil {
		return err
	}
	if err := ex.st.enterExtends(); err != nil {
		return err
	}
	// The parent's blocks go behind the child's, so the most derived
	// definition stays at index 0 and super() walks toward the base.
	for name, blk := range parent.blocks {
		ex.st.blocks[name] = append(ex.st.blocks[name], blockEntry{tmpl: parent, node: blk})
	}
	ex.st.parent = parent
	return nil
}

func (ex *exec) execInclude(n *ast.Include) error {
	tmpl, err := ex.loadTemplateExpr(n.Template)
	if err != nil {
		if n.IgnoreMissing && errors.Is(err, errs.TemplateNotFound) {
			return nil
		}
		return err
	}
	if err := ex.st.enter(); err != nil {
		return err
	}
	defer ex.st.leave()

	var vars map[string]value.Value
	if n.WithContext {
		// An include sees the including template's whole frame, loop
		// variables included, not just its top-level context.
		var err error
		if vars, err = ex.sc.flatten(); err != nil {
			return err
		}
	}
	var buf strings.Builder
	// jinja2 yields an include's pieces into the includer's own stream, so
	// they are counted there: the included template numbers a filter block
	// from where this frame had got to, and advances the count as it goes.
	if err := tmpl.renderInto(&buf, vars, ex.st.depth, ex.st.budget, ex.chunks); err != nil {
		return err
	}
	// A context-free include writes into the enclosing *function's* stream
	// rather than this frame's buffer; see the note on exec.stream.
	target := ex.out
	if !n.WithContext {
		target = ex.stream
	}
	return ex.writeToUncounted(target, buf.String())
}

// loadTemplateName resolves a single template name, refusing a list.
func (ex *exec) loadTemplateName(e ast.Expr) (*Template, error) {
	v, err := ex.eval(e)
	if err != nil {
		return nil, err
	}
	// jinja2's _load_template checks for a loader before it looks at the
	// name at all, so an environment with no loader reports itself and not
	// the unhashable list or the undefined below it. Both of those were
	// reported ahead of it here.
	if err := ex.st.env.requireLoader(); err != nil {
		return nil, err
	}
	switch v.Kind() {
	case value.KindList, value.KindDict:
		// jinja2 puts the name into its template cache key, which is a
		// tuple, so 3.14 reports the tuple as the key and the name as
		// the unhashable part.
		return nil, value.ErrUnhashable("tuple", v, ex.pyVersion(), value.AsDictKey)
	case value.KindUndefined:
		return nil, v.UndefinedError()
	}
	return ex.st.env.GetTemplate(value.Str(v))
}

// loadTemplateExpr is jinja2's get_or_select_template, which is what
// `{% include %}` compiles to: a string or an undefined is a single name, and
// everything else is a selection.
//
// There is no type check of its own. A number reaches select_template and fails
// as something that cannot be iterated; a None fails as an empty selection
// before anything asks whether it could be. Refusing both at the door with
// "template name must be a string" named a rule jinja2 does not have.
func (ex *exec) loadTemplateExpr(e ast.Expr) (*Template, error) {
	v, err := ex.eval(e)
	if err != nil {
		return nil, err
	}
	switch {
	case v.IsString():
		return ex.st.env.GetTemplate(v.AsString())
	case v.IsUndefined():
		// get_or_select_template treats an undefined as a single name,
		// so this goes through get_template -- and its loader check
		// comes first. A *selection* does not: an empty list is refused
		// by select_template before any name is looked up, which is why
		// only this branch asks.
		if err := ex.st.env.requireLoader(); err != nil {
			return nil, err
		}
		return nil, v.UndefinedError()
	}
	return ex.st.env.selectTemplateValue(v)
}

func (ex *exec) execImport(n *ast.Import) error {
	module, err := ex.importModule(n.Template, n.WithContext)
	if err != nil {
		return err
	}
	ex.sc.set(n.Target, module)
	if ex.sc == ex.st.ctx {
		ex.st.contextVars.set(n.Target, module)
		ex.st.unexport(n.Target)
	}
	return nil
}

func (ex *exec) execFromImport(n *ast.FromImport) error {
	module, err := ex.importModule(n.Template, n.WithContext)
	if err != nil {
		return err
	}
	obj, _ := module.Object()
	mod, _ := obj.(*moduleObject)
	for _, entry := range n.Names {
		v, ok := obj.GetAttr(entry.Name)
		if !ok {
			// jinja2 names the template the name was asked *of* --
			// `included_template.__name__` -- not the one doing the
			// asking. Reading the importer's name reported '' for
			// every template compiled from a string.
			v = value.UndefinedHint(
				"the template %s (imported on line %d) does not export the requested name %s",
				value.ReprFor(value.String(mod.name), ex.pyVersion()), n.Line(),
				value.ReprFor(value.String(entry.Name), ex.pyVersion()))
			v = ex.st.Undefined(v)
		}
		ex.sc.set(entry.Alias, v)
		if ex.sc == ex.st.ctx {
			ex.st.contextVars.set(entry.Alias, v)
			ex.st.unexport(entry.Alias)
		}
	}
	return nil
}

// importModule renders a template for its definitions rather than its output,
// returning an object exposing the names it exported.
func (ex *exec) importModule(nameExpr ast.Expr, withContext bool) (value.Value, error) {
	// {% import %} and {% from %} compile to get_template, not to
	// get_or_select_template: there is no selection here, so a name that is
	// not a string goes to the loader as it is and comes back as
	// TemplateNotFound naming it.
	tmpl, err := ex.loadTemplateName(nameExpr)
	if err != nil {
		return value.Undefined, err
	}
	if err := ex.st.enter(); err != nil {
		return value.Undefined, err
	}
	defer ex.st.leave()

	var vars map[string]value.Value
	if withContext {
		var err error
		if vars, err = ex.sc.flatten(); err != nil {
			return value.Undefined, err
		}
	}
	// The budget crosses this boundary exactly as it does an include's:
	// share it, or the imported template renders with no bound and no
	// context at all.
	st := tmpl.newState(vars, ex.st.depth, ex.st.budget)
	// The body is kept, not discarded: a TemplateModule's str() is what the
	// imported template rendered, so `{% import "t" as m %}{{ m }}` prints
	// t's output. gojja2 threw it away and printed the repr instead.
	//
	// It goes through renderState rather than straight to execBody, because
	// rendering a template is not the same as running its body. A template
	// that extends emits nothing from its own body -- its output comes from
	// the parent, rendered afterwards with the blocks the child registered
	// -- so running the body alone gave an extending module an empty string
	// and no exports from anywhere up the chain. renderState is the one
	// place that knows this, and {% include %} was already using it, which
	// is why an include of the same template was right and an import of it
	// was not.
	var body strings.Builder
	if err := tmpl.renderState(st, &body, nil); err != nil {
		return value.Undefined, err
	}
	return value.FromObject(&moduleObject{st: st, name: tmpl.name, body: body.String()}), nil
}

// export records a top-level binding so an importing template can see it.
//
// jinja2 does not export a name beginning with an underscore, which is the one
// rule here; the set is what moduleObject answers from.
// unexport removes a name from the exports, which is what binding it with an
// {% import %} does: jinja2's generator writes `context.exported_vars.discard`
// for every top-level import target, so a module that imports another does not
// re-export it -- `{% import 'inner' as sub %}` leaves `m.sub` undefined in the
// template that imports *it*, and discards the name even where an earlier
// `{% set sub = ... %}` had exported it. A later `{% set %}` adds it back.
func (s *State) unexport(name string) {
	delete(s.exports, name)
}

func (s *State) export(name string) {
	if strings.HasPrefix(name, "_") {
		return
	}
	if s.exports == nil {
		s.exports = make(map[string]bool, 8)
	}
	s.exports[name] = true
}

// moduleObject is what `{% import %}` binds: the exported names of a rendered
// template.
type moduleObject struct {
	st   *State
	name string
	body string
}

func (m *moduleObject) GetAttr(name string) (value.Value, bool) {
	// The exported list decides, rather than the frame being read directly.
	// It was written by every top-level binding and read by nothing, so the
	// comment on State.exported -- "so `{% import %}` can expose them" --
	// described something that was not happening, and the underscore rule
	// it applies was duplicated here to make up for it.
	if !m.st.exports[name] {
		return value.Undefined, false
	}
	return m.st.ctx.get(name)
}

// A TemplateModule is deliberately attribute-only: jinja2's is not a mapping
// and not iterable, so `{{ module|tojson }}` and `{% for x in module %}` fail
// there and must fail here too.

func (m *moduleObject) TypeName() string { return "TemplateModule" }

func (m *moduleObject) QualifiedName() string { return "jinja2.environment.TemplateModule" }

func (m *moduleObject) Repr() string {
	return "<TemplateModule " + value.Repr(value.String(m.name)) + ">"
}

// Str and HTML are the module's __str__ and __html__: both are the body it
// rendered. The body was produced under the imported template's own escaping,
// so handing it over as markup is what keeps it from being escaped twice.
func (m *moduleObject) Str() string  { return m.body }
func (m *moduleObject) HTML() string { return m.body }
