// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"errors"
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
	out *strings.Builder
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
	stream *strings.Builder
	// autoescape is per-frame so `{% autoescape %}` can change it for a
	// span without disturbing the rest of the render.
	autoescape bool

	// blockName and blockIndex locate the block being rendered, for super().
	blockName  string
	blockIndex int
	// loop is the innermost `loop` value, for recursive loop() calls.
	loop value.Value
}

// Loop control travels as sentinel errors, which keeps the happy path free of
// a status return on every statement.
var (
	errBreakLoop    = errors.New("break")
	errContinueLoop = errors.New("continue")
)

// child returns a frame sharing output but with its own scope.
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
		return ex.child(newScope(ex.sc)).execBody(n.Body)
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
			ex.out.WriteString(data.Data)
			continue
		}
		v, err := ex.eval(node)
		if err != nil {
			return err
		}
		text, err := ex.renderValue(v)
		if err != nil {
			return errs.At(err, ex.st.tmpl.name, node.Line())
		}
		ex.out.WriteString(text)
	}
	return nil
}

// renderValue turns a value into the text that reaches the output.
func (ex *exec) renderValue(v value.Value) (string, error) {
	if ex.st.env.finalize != nil {
		v = ex.st.env.finalize(v)
	}
	if v.IsUndefined() && v.UndefinedBehavior() == value.UndefinedStrict {
		return "", v.UndefinedError()
	}
	text := value.Str(v)
	if ex.autoescape && !v.IsSafe() {
		text = escapeHTML(text)
	}
	return text, nil
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

	if src.Len() == 0 {
		return ex.execBody(n.Else)
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

	for i := range src.Len() {
		loop.index = i
		// Each iteration gets a fresh scope, so a `{% set %}` in the
		// body does not carry into the next pass -- jinja2 rebinds
		// every body-assigned symbol from the enclosing scope at the
		// top of each iteration, which amounts to the same thing.
		body := ex.child(newScope(ex.sc))
		body.loop = loopValue
		body.sc.set("loop", loopValue)
		declareFrameLocals(body.sc, ex.st, n.Body)

		if err := body.assign(n.Target, src.At(i)); err != nil {
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
	return nil
}

// loopSourceFor resolves the sequence a loop walks, applying the `if` filter
// when there is one. A filtered loop must be materialised, because its length
// and indices count only the items that survive.
func (ex *exec) loopSourceFor(n *ast.For, iterable value.Value) (loopSource, error) {
	if n.Test == nil {
		return makeLoopSource(iterable)
	}
	seq, err := value.Iterate(iterable)
	if err != nil {
		return nil, err
	}
	var kept []value.Value
	for item := range seq {
		filterScope := ex.child(newScope(ex.sc))
		if err := filterScope.assign(n.Target, item); err != nil {
			return nil, err
		}
		ok, err := filterScope.truth(n.Test)
		if err != nil {
			return nil, err
		}
		if ok {
			kept = append(kept, item)
		}
	}
	return sliceSource(kept), nil
}

func (ex *exec) execAssign(n *ast.Assign) error {
	v, err := ex.eval(n.Node)
	if err != nil {
		return err
	}
	return ex.assign(n.Target, v)
}

func (ex *exec) execAssignBlock(n *ast.AssignBlock) error {
	inner := newScope(ex.sc)
	declareFrameLocals(inner, ex.st, n.Body)
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
	return ex.assign(n.Target, v)
}

func (ex *exec) execWith(n *ast.With) error {
	inner := newScope(ex.sc)
	sub := ex.child(inner)
	declareFrameLocals(inner, ex.st, n.Body)
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
	sub := ex.child(newScope(ex.sc))
	sub.autoescape = on
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
	values := make([]value.Value, len(defaults))
	for i, d := range defaults {
		v, err := ex.eval(d)
		if err != nil {
			return nil, err
		}
		values[i] = v
	}
	undeclared := findUndeclared(node.Body, "varargs", "kwargs", "caller")
	m := &macroObject{
		name:         name,
		node:         node,
		defaults:     values,
		defScope:     ex.sc,
		st:           ex.st,
		tmpl:         ex.st.tmpl,
		autoescape:   ex.autoescape,
		catchVarargs: undeclared["varargs"],
		catchKwargs:  undeclared["kwargs"],
		caller:       undeclared["caller"],
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
	declareFrameLocals(inner, ex.st, n.Body)
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
	out, err := ex.renderValue(v)
	if err != nil {
		return err
	}
	ex.out.WriteString(out)
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
			"Required block %s not found", value.Repr(value.String(n.Name)))
	}

	ref := &blockReference{st: ex.st, name: n.Name, index: 0}
	if n.Scoped {
		// A scoped block is handed its immediate frame's own bindings
		// -- the loop variable and `loop` -- on top of context.vars.
		// It is not given the whole enclosing chain, so a name the root
		// frame owns but has not assigned yet still resolves from the
		// render arguments.
		scoped := newScope(ex.st.contextVars)
		for name, v := range ex.sc.vars {
			scoped.set(name, v)
		}
		ref.sc = scoped
	}
	v, err := ref.render()
	if err != nil {
		return err
	}
	ex.out.WriteString(value.Str(v))
	return nil
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
	if err := ex.st.enter(); err != nil {
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
		if n.IgnoreMissing && errs.KindOf(err).DerivesFrom(errs.TemplateNotFound) {
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
		vars = ex.sc.flatten()
	}
	out, err := tmpl.render(vars, ex.st.depth)
	if err != nil {
		return err
	}
	target := ex.out
	if !n.WithContext {
		target = ex.stream
	}
	target.WriteString(out)
	return nil
}

// loadTemplateName resolves a single template name, refusing a list.
func (ex *exec) loadTemplateName(e ast.Expr) (*Template, error) {
	v, err := ex.eval(e)
	if err != nil {
		return nil, err
	}
	switch v.Kind() {
	case value.KindList, value.KindDict:
		return nil, errs.New(errs.TypeError, "unhashable type: '%s'", v.TypeName())
	case value.KindUndefined:
		return nil, v.UndefinedError()
	}
	return ex.st.env.GetTemplate(value.Str(v))
}

// loadTemplateExpr resolves the template named by an expression, which may be
// a name, a template object, or a list of candidates.
func (ex *exec) loadTemplateExpr(e ast.Expr) (*Template, error) {
	v, err := ex.eval(e)
	if err != nil {
		return nil, err
	}
	switch v.Kind() {
	case value.KindString:
		return ex.st.env.GetTemplate(v.AsString())
	case value.KindList, value.KindTuple:
		s, _ := v.Seq()
		return ex.st.env.selectTemplateValues(s.Items())
	case value.KindUndefined:
		return nil, v.UndefinedError()
	}
	return nil, errs.New(errs.TypeError, "template name must be a string, not %s", v.TypeName())
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
	for _, entry := range n.Names {
		v, ok := obj.GetAttr(entry.Name)
		if !ok {
			v = value.UndefinedHint(
				"the template %s (imported on line %d) does not export the requested name %s",
				value.Repr(value.String(ex.st.tmpl.name)), n.Line(),
				value.Repr(value.String(entry.Name)))
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
	tmpl, err := ex.loadTemplateExpr(nameExpr)
	if err != nil {
		return value.Undefined, err
	}
	if err := ex.st.enter(); err != nil {
		return value.Undefined, err
	}
	defer ex.st.leave()

	var vars map[string]value.Value
	if withContext {
		vars = ex.sc.flatten()
	}
	st := tmpl.newState(vars)
	st.depth = ex.st.depth
	var discard strings.Builder
	sub := &exec{st: st, sc: st.ctx, out: &discard, stream: &discard, autoescape: st.autoescape}
	if err := sub.execBody(tmpl.tree.Body); err != nil {
		return value.Undefined, err
	}
	return value.FromObject(&moduleObject{st: st, name: tmpl.name}), nil
}

// export records a top-level binding so an importing template can see it.
func (s *State) export(name string) {
	if strings.HasPrefix(name, "_") {
		return
	}
	for _, existing := range s.exported {
		if existing == name {
			return
		}
	}
	s.exported = append(s.exported, name)
}

// moduleObject is what `{% import %}` binds: the exported names of a rendered
// template.
type moduleObject struct {
	st   *State
	name string
}

func (m *moduleObject) GetAttr(name string) (value.Value, bool) {
	if strings.HasPrefix(name, "_") {
		return value.Undefined, false
	}
	return m.st.ctx.vars[name], m.st.ctx.vars[name] != value.Value{}
}

// A TemplateModule is deliberately attribute-only: jinja2's is not a mapping
// and not iterable, so `{{ module|tojson }}` and `{% for x in module %}` fail
// there and must fail here too.

func (m *moduleObject) TypeName() string { return "TemplateModule" }

func (m *moduleObject) Repr() string {
	return "<TemplateModule " + value.Repr(value.String(m.name)) + ">"
}
