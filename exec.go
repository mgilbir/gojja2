// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"errors"
	"iter"
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
	blockName  string
	blockIndex int
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
	if ex.chunks != nil {
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
	if err := ex.st.budget.account(len(s)); err != nil {
		return err
	}
	// The nil check first: it is almost always nil, and comparing two
	// interface values is not free on a path that runs once per write.
	if ex.chunks != nil && w == ex.out {
		*ex.chunks++
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
	case *ast.Scope:
		inner := newScope(ex.sc)
		if err := declareFrameLocals(inner, ex.st, n, n.Body, inner.parent); err != nil {
			return err
		}
		return ex.child(inner).execBody(n.Body)
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
func (ex *exec) renderValue(v value.Value) (string, error) {
	if v.IsUndefined() && v.UndefinedBehavior() == value.UndefinedStrict {
		return "", v.UndefinedError()
	}
	text := value.Str(v)
	if ex.autoescape && !v.IsSafe() {
		// Output is str(x) plainly and escape(x) when autoescaping, so
		// a value that carries its own escaped form hands that over
		// here and only here -- `{{ m }}` is the module's body where
		// `{{ m ~ "" }}` is str(m), escaped as a whole.
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
		case *ast.Scope:
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

// hasFilterBlock reports whether body contains a {% filter %} anywhere, which
// is the only construct that asks how many pieces of output a frame holds.
func hasFilterBlock(body []ast.Stmt) bool {
	for _, stmt := range body {
		switch n := stmt.(type) {
		case *ast.FilterBlock:
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
		case *ast.Scope:
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

	loop := &loopObject{src: src, depth: depth}
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
	ran := false
	// A body that cannot let its scope outlive the iteration gets one
	// frame reused for the whole loop instead of one per pass. See
	// bodyRetainsScope: only a macro keeps a reference to the scope it was
	// written in, so only a body containing one has to be given a fresh
	// frame each time.
	var reuse *exec
	if !bodyRetainsScope(n.Body) {
		reuse = ex.child(newScope(ex.sc))
	}
	for loop.index = 0; src.has(loop.index); loop.index++ {
		if err := src.err(); err != nil {
			return err
		}
		if err := ex.st.budget.step(); err != nil {
			return err
		}
		ran = true
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
		if err := declareFrameLocals(body.sc, ex.st, n, n.Body, body.sc.parent); err != nil {
			return err
		}

		if err := body.assign(n.Target, src.at(loop.index)); err != nil {
			return err
		}
		err := body.execBody(n.Body)
		switch {
		case errors.Is(err, errBreakLoop):
			return nil
		case errors.Is(err, errContinueLoop):
			continue
		case err != nil:
			return err
		}
	}
	// A filter that failed part way stops the loop rather than ending it.
	if err := src.err(); err != nil {
		return err
	}
	// The else branch runs when nothing did, which is what jinja2 tracks
	// rather than asking the source how long it is -- asking would run a
	// filtered loop's test over every item before the first pass.
	if !ran {
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
	seq, err := value.Iterate(iterable)
	if err != nil {
		return nil, err
	}
	// Pulled one item at a time, as jinja2's filter generator is: the test
	// runs between the body's passes and therefore sees what the body did.
	nextItem, stop := iter.Pull(seq)
	_ = stop
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
	return &filteredSource{next: next}, nil
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

	v := markup(text, ex.autoescape)
	if n.Filter != nil {
		// The filter chain was parsed with a nil input; the captured
		// body is what flows into it.
		v, err = ex.applyFilterChain(n.Filter, v)
		if err != nil {
			return err
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
		catchVarargs:   undeclared["varargs"],
		catchKwargs:    undeclared["kwargs"],
		caller:         undeclared["caller"],
	}
	for _, p := range args {
		if p.Name == "caller" {
			m.explicitCaller = true
		}
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
	out, err := ex.renderValue(v)
	if err != nil {
		return err
	}
	if err := ex.write(out); err != nil {
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
		// A scoped block is handed its immediate frame's own bindings
		// -- the loop variable and `loop` -- on top of context.vars.
		// It is not given the whole enclosing chain, so a name the root
		// frame owns but has not assigned yet still resolves from the
		// render arguments.
		scoped := newScope(ex.st.contextVars)
		ex.sc.each(scoped.set)
		ref.sc = scoped
	}
	v, err := ref.render()
	if err != nil {
		return err
	}
	return ex.write(value.Str(v))
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
	if err := tmpl.renderInto(&buf, vars, ex.st.depth, ex.st.budget); err != nil {
		return err
	}
	// A context-free include writes into the enclosing *function's* stream
	// rather than this frame's buffer; see the note on exec.stream.
	target := ex.out
	if !n.WithContext {
		target = ex.stream
	}
	return ex.writeTo(target, buf.String())
}

// loadTemplateName resolves a single template name, refusing a list.
func (ex *exec) loadTemplateName(e ast.Expr) (*Template, error) {
	v, err := ex.eval(e)
	if err != nil {
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
		ex.st.export(n.Target)
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
			ex.st.export(entry.Alias)
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
	if err := tmpl.renderState(st, &body); err != nil {
		return value.Undefined, err
	}
	return value.FromObject(&moduleObject{st: st, name: tmpl.name, body: body.String()}), nil
}

// export records a top-level binding so an importing template can see it.
//
// jinja2 does not export a name beginning with an underscore, which is the one
// rule here; the set is what moduleObject answers from.
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
