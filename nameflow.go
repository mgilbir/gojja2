// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"sort"

	"github.com/mgilbir/gojja2/internal/ast"
	"github.com/mgilbir/gojja2/internal/parser"
)

// Which context variables can reach a template's output, and how.
//
// The question a caller actually has about a variable is not "is it mentioned"
// but what happens if they pass it: does its value end up in the document, or
// does it only decide which of several documents is produced, or can it not
// affect the result at all. Those are different questions and the answers
// differ -- `{% if flag %}hi{% endif %}` never prints flag, and
// `{% set y = x %}{{ y }}` prints x without ever naming it in an output tag.
//
// So this is dataflow rather than a syntactic scan. Every binding is a node in
// a graph, roles attach where a value is consumed, and they propagate backwards
// along the edges to whatever the value came from.
//
// The scoping is not reinvented here: frameLocals already implements jinja2's
// first-mention rule, which is what decides whether `{{ x }}{% set x = 1 %}`
// reads the caller's x (it does) or the frame's (it does not), and it is graded
// by the conformance suite. Getting that wrong changes the answer silently.
//
// Soundness has a direction. Where this cannot see -- a computed lookup, a
// namespace, a template pulled in by a name it cannot resolve -- the answer is
// Unknown rather than a guess. A variable reported without Unknown is a real
// answer in both directions; one with it may reach the output by a route that
// was not followed. Nothing here ever says "cannot reach the output" about a
// variable that might.
//
// tools/oracle/nameflow.py is the same analysis over jinja2's own AST, and
// conformance/nameflow_test.go requires the two to agree on every committed
// case. That is the point of writing it twice: the answer has to be a property
// of the template rather than of either tree's shape.

type varRole uint8

const (
	roleOutput varRole = 1 << iota
	roleFlow
)

type idset map[int]bool

func (s idset) add(o idset) {
	for k := range o {
		s[k] = true
	}
}

// varSym is one storage location: a context variable, or a binding in a frame.
type varSym struct {
	id      int
	name    string
	context bool
	roles   varRole
	deps    idset
	unknown bool
}

type nfFrame struct {
	owned map[string]*varSym
}

type nameflow struct {
	syms    []*varSym
	context map[string]*varSym
	frames  []*nfFrame
	tmpl    *Template

	// capture is the stack of block-set bodies being collected: while one is
	// open, output becomes a value instead of a document.
	capture []idset

	macros      map[string]*ast.Macro
	macroFrames map[string]map[string]*varSym
	macroOut    map[string]idset
	inMacro     map[string]bool

	// opaqueSink records that the whole context can be handed to a template
	// this did not follow, which puts every context variable in play.
	opaqueSink bool
}

func newNameflow(t *Template) *nameflow {
	return &nameflow{
		context:     map[string]*varSym{},
		tmpl:        t,
		macros:      map[string]*ast.Macro{},
		macroFrames: map[string]map[string]*varSym{},
		macroOut:    map[string]idset{},
		inMacro:     map[string]bool{},
	}
}

func (a *nameflow) newSym(name string, context bool) *varSym {
	s := &varSym{id: len(a.syms), name: name, context: context, deps: idset{}}
	a.syms = append(a.syms, s)
	return s
}

func (a *nameflow) contextSym(name string) *varSym {
	if s, ok := a.context[name]; ok {
		return s
	}
	s := a.newSym(name, true)
	a.context[name] = s
	return s
}

// resolve is what a read of name sees: the innermost frame that owns it, or
// the caller's variables.
func (a *nameflow) resolve(name string) *varSym {
	for i := len(a.frames) - 1; i >= 0; i-- {
		if s, ok := a.frames[i].owned[name]; ok {
			return s
		}
	}
	return a.contextSym(name)
}

// push claims the names a frame owns before walking it, because jinja2 decides
// ownership for the whole frame up front rather than as the walk arrives.
func (a *nameflow) push(key any, body []ast.Stmt) {
	_ = key
	f := &nfFrame{owned: map[string]*varSym{}}
	if body != nil {
		// frameLocals rather than the template's cache: the tree analysed
		// here is parsed fresh, so its nodes are not the ones the cache is
		// keyed on and every entry would be a leak.
		names := frameLocals(body)
		for _, n := range names.owns {
			f.owned[n] = a.newSym(n, false)
		}
	}
	a.frames = append(a.frames, f)
}

func (a *nameflow) pop() { a.frames = a.frames[:len(a.frames)-1] }

// bindLocal declares a name the frame construct itself binds -- a loop target,
// a macro parameter -- which frameLocals does not report because it is not an
// assignment inside the body.
func (a *nameflow) bindLocal(name string) *varSym {
	f := a.frames[len(a.frames)-1]
	s := a.newSym(name, false)
	f.owned[name] = s
	return s
}

func (a *nameflow) apply(srcs idset, r varRole) {
	for id := range srcs {
		a.syms[id].roles |= r
	}
}

func (a *nameflow) taint(srcs idset) {
	for id := range srcs {
		a.syms[id].unknown = true
	}
}

// emit records that a value reaches the document, unless a block-set is
// capturing it into a variable instead.
func (a *nameflow) emit(srcs idset) {
	if n := len(a.capture); n > 0 {
		a.capture[n-1].add(srcs)
		return
	}
	a.apply(srcs, roleOutput)
}

// --- expressions -------------------------------------------------------
//
// expr answers "what does this value derive from", and applies roleFlow itself
// at the one control position that lives inside an expression: a conditional's
// test. Everything else is data. `{{ x|length }}` and `{{ x is defined }}` both
// put a value derived from x into the document, so both are output; roleFlow is
// for *choosing* between alternatives, which is what `{% if %}`, a loop's
// length and `a if c else b` do. The line has to be drawn somewhere, and
// control positions are the one place two implementations can agree on it
// without comparing notes.

func (a *nameflow) exprs(list []ast.Expr) idset {
	out := idset{}
	for _, e := range list {
		out.add(a.expr(e))
	}
	return out
}

func (a *nameflow) argsOf(args *ast.Args) idset {
	out := a.exprs(args.Args)
	for _, kw := range args.Kwargs {
		out.add(a.expr(kw.Value))
	}
	out.add(a.expr(args.DynArgs))
	out.add(a.expr(args.DynKwargs))
	return out
}

func (a *nameflow) expr(e ast.Expr) idset {
	out := idset{}
	if e == nil {
		return out
	}
	switch n := e.(type) {
	case *ast.Name:
		if n.Store {
			return out
		}
		out[a.resolve(n.Name).id] = true
		return out

	case *ast.NSRef:
		// A namespace read. Following the field is the last layer; until
		// then the honest answer is that anything could be in it.
		s := a.resolve(n.Name)
		s.unknown = true
		out[s.id] = true
		return out

	case *ast.Const, *ast.TemplateData:
		return out

	case *ast.CondExpr:
		a.apply(a.expr(n.Test), roleFlow)
		out.add(a.expr(n.True))
		out.add(a.expr(n.False))
		return out

	case *ast.Getitem:
		out.add(a.expr(n.Node))
		if _, constant := n.Arg.(*ast.Const); !constant {
			// A computed key: which part of the container is read is
			// not a static fact, so the whole of it is in play.
			a.taint(out)
			out.add(a.expr(n.Arg))
		}
		return out

	case *ast.Filter:
		// Node is nil for the leading filter of a {% filter %} block or a
		// block set, whose input is the captured body.
		out.add(a.expr(n.Node))
		out.add(a.argsOf(&n.Args))
		if n.Name == "attr" {
			a.taint(out)
		}
		return out

	case *ast.Call:
		out.add(a.argsOf(&n.Args))
		if fn, ok := n.Node.(*ast.Name); ok {
			if _, known := a.macros[fn.Name]; known && !fn.Store {
				out.add(a.callMacro(fn.Name, n))
				return out
			}
			if fn.Name == "namespace" && !fn.Store {
				a.taint(out)
				return out
			}
		}
		// A call whose body this cannot see: a global, a macro held in a
		// variable, getattr. The result derives from the arguments and
		// from whatever it closed over, which is not visible.
		out.add(a.expr(n.Node))
		a.taint(out)
		return out
	}

	// Everything else -- operators, comparisons, tests, attribute access,
	// containers, slices, concatenation -- is ordinary data flow: the result
	// derives from every operand.
	//
	// This reuses ast.Inspect rather than enumerating those node types again.
	// Inspect's switch is total and proven so by reflection, so a node added
	// later is reached here as data flow, which is the sound default. Only the
	// nodes handled above are stopped at.
	ast.Inspect(e, func(nd ast.Node) bool {
		if nd == ast.Node(e) {
			return true
		}
		switch nd.(type) {
		case *ast.Name, *ast.NSRef, *ast.CondExpr, *ast.Getitem,
			*ast.Filter, *ast.Call:
			out.add(a.expr(nd.(ast.Expr)))
			return false
		}
		return true
	})
	return out
}

// --- statements --------------------------------------------------------

func (a *nameflow) stmts(body []ast.Stmt) {
	for _, s := range body {
		a.stmt(s)
	}
}

// bind records that target now holds a value derived from srcs.
func (a *nameflow) bind(target ast.Expr, srcs idset) {
	switch t := target.(type) {
	case *ast.Name:
		a.resolve(t.Name).deps.add(srcs)
	case *ast.NSRef:
		// A namespace field. Until the field is followed, a write into a
		// namespace makes the namespace opaque rather than silently lost.
		s := a.resolve(t.Name)
		s.deps.add(srcs)
		s.unknown = true
	case *ast.Tuple:
		// Unpacking: which element lands where is not tracked, so every
		// target derives from the whole right-hand side.
		for _, item := range t.Items {
			a.bind(item, srcs)
		}
	case *ast.List:
		for _, item := range t.Items {
			a.bind(item, srcs)
		}
	default:
		a.taint(srcs)
	}
}

// captureBody is what a block set or a filter block's body would have printed.
func (a *nameflow) captureBody(body []ast.Stmt) idset {
	a.capture = append(a.capture, idset{})
	a.stmts(body)
	out := a.capture[len(a.capture)-1]
	a.capture = a.capture[:len(a.capture)-1]
	return out
}

// callMacro binds a call's arguments to the macro's parameters and returns what
// the macro's body would print.
func (a *nameflow) callMacro(name string, call *ast.Call) idset {
	if a.inMacro[name] {
		// Recursive. The fixpoint below already carries roles around the
		// cycle, and re-entering would not terminate.
		return idset{}
	}
	a.inMacro[name] = true
	defer delete(a.inMacro, name)

	m := a.macros[name]
	frame := a.macroFrames[name]
	for i, param := range m.Args {
		srcs := idset{}
		if i < len(call.Args.Args) {
			srcs.add(a.expr(call.Args.Args[i]))
		}
		for _, kw := range call.Kwargs {
			if kw.Key == param.Name {
				srcs.add(a.expr(kw.Value))
			}
		}
		if len(srcs) > 0 {
			if s, ok := frame[param.Name]; ok {
				s.deps.add(srcs)
			}
		}
	}
	return a.macroOut[name]
}

func (a *nameflow) stmt(s ast.Stmt) {
	switch n := s.(type) {
	case *ast.Output:
		for _, item := range n.Nodes {
			a.emit(a.expr(item))
		}

	case *ast.If:
		a.apply(a.expr(n.Test), roleFlow)
		a.stmts(n.Body)
		for _, elif := range n.Elif {
			a.stmt(elif)
		}
		a.stmts(n.Else)

	case *ast.For:
		// The iterable is evaluated outside the loop's own frame, and its
		// length decides how many times the body runs -- so it can change
		// the output without appearing in it.
		srcs := a.expr(n.Iter)
		a.apply(srcs, roleFlow)
		a.push(n, n.Body)
		a.bindLoopTargets(n.Target)
		a.bind(n.Target, srcs)
		// `loop` is supplied by the loop, not by the caller, and what it
		// reports -- index, length, first, last -- is derived from the
		// sequence being walked.
		a.bindLocal("loop").deps.add(srcs)
		a.apply(a.expr(n.Test), roleFlow)
		a.stmts(n.Body)
		a.stmts(n.Else)
		a.pop()

	case *ast.Assign:
		a.bind(n.Target, a.expr(n.Node))

	case *ast.AssignBlock:
		srcs := a.captureBody(n.Body)
		srcs.add(a.expr(n.Filter))
		a.bind(n.Target, srcs)

	case *ast.FilterBlock:
		srcs := a.captureBody(n.Body)
		srcs.add(a.expr(n.Filter))
		a.emit(srcs)

	case *ast.Macro:
		a.push(n, n.Body)
		for _, param := range n.Args {
			a.bindLocal(param.Name)
		}
		// varargs, kwargs and caller are supplied by the call site, not by
		// the caller's variables.
		for _, supplied := range []string{"varargs", "kwargs", "caller"} {
			if _, ok := a.frames[len(a.frames)-1].owned[supplied]; !ok {
				a.bindLocal(supplied)
			}
		}
		frame := map[string]*varSym{}
		for k, v := range a.frames[len(a.frames)-1].owned {
			frame[k] = v
		}
		a.macroFrames[n.Name] = frame
		// Defaults align with the tail of Args.
		off := len(n.Args) - len(n.Defaults)
		for i, d := range n.Defaults {
			a.bind(n.Args[off+i], a.expr(d))
		}
		a.macroOut[n.Name] = a.captureBody(n.Body)
		a.pop()
		a.macros[n.Name] = n

	case *ast.CallBlock:
		a.push(n, n.Body)
		for _, param := range n.Args {
			a.bindLocal(param.Name)
		}
		a.stmts(n.Body)
		a.pop()
		a.emit(a.expr(n.Call))

	case *ast.With:
		type pair struct {
			target ast.Expr
			srcs   idset
		}
		pairs := make([]pair, 0, len(n.Targets))
		for i, v := range n.Values {
			if i < len(n.Targets) {
				pairs = append(pairs, pair{n.Targets[i], a.expr(v)})
			}
		}
		a.push(n, n.Body)
		for _, p := range pairs {
			a.bindLoopTargets(p.target)
			a.bind(p.target, p.srcs)
		}
		a.stmts(n.Body)
		a.pop()

	case *ast.Block:
		a.push(n, n.Body)
		a.stmts(n.Body)
		a.pop()

	case *ast.Scope:
		a.push(n, n.Body)
		a.stmts(n.Body)
		a.pop()

	case *ast.AutoescapeBlock:
		// Whether output is escaped changes what is rendered, so the setting
		// steers the result without appearing in it. The body is a frame of
		// its own -- `{% autoescape %}{% set m = 1 %}{% endautoescape %}`
		// claims m the way any other scope does.
		a.apply(a.expr(n.Value), roleFlow)
		a.push(n, n.Body)
		a.stmts(n.Body)
		a.pop()

	case *ast.Import:
		// `{% import 'x' as m %}` binds m here. Without that it reads as a
		// variable the caller was expected to supply.
		a.expr(n.Template)
		s := a.bindLocal(n.Target)
		s.unknown = true
		a.opaqueSink = true

	case *ast.FromImport:
		a.expr(n.Template)
		for _, nm := range n.Names {
			b := a.bindLocal(nm.Alias)
			b.unknown = true
		}
		a.opaqueSink = true

	case *ast.Extends, *ast.Include:
		// Another template receives this context and can print any of it,
		// so until the edge is followed every context variable is in play.
		// The template reference itself is ordinary data.
		ast.Inspect(s, func(nd ast.Node) bool {
			if e, ok := nd.(ast.Expr); ok {
				a.expr(e)
				return false
			}
			return true
		})
		a.opaqueSink = true

	case *ast.ExprStmt:
		a.expr(n.Node)

	case *ast.Break, *ast.Continue:
		// Leaves.

	default:
		// A statement nobody taught this about must not read as "nothing
		// happens here", so its expressions are walked and treated as
		// opaque.
		ast.Inspect(s, func(nd ast.Node) bool {
			if e, ok := nd.(ast.Expr); ok {
				a.taint(a.expr(e))
				return false
			}
			if st, ok := nd.(ast.Stmt); ok && st != s {
				a.stmt(st)
				return false
			}
			return true
		})
	}
}

// bindLoopTargets declares the names a loop or with-block binds itself, which
// frameLocals does not report: they are bound by the construct rather than by
// an assignment inside the body.
func (a *nameflow) bindLoopTargets(target ast.Expr) {
	switch t := target.(type) {
	case *ast.Name:
		if _, ok := a.frames[len(a.frames)-1].owned[t.Name]; !ok {
			a.bindLocal(t.Name)
		}
	case *ast.Tuple:
		for _, item := range t.Items {
			a.bindLoopTargets(item)
		}
	case *ast.List:
		for _, item := range t.Items {
			a.bindLoopTargets(item)
		}
	}
}

// --- propagation and the answer ----------------------------------------

// propagate pushes roles backwards along the dependency edges to a fixpoint.
//
// A value that reaches the output means everything it was derived from reaches
// the output; the same for flow, and for not having been understood. The graph
// can hold cycles -- a loop that accumulates into a variable it also reads --
// so this iterates rather than recursing.
func (a *nameflow) propagate() {
	for changed := true; changed; {
		changed = false
		for _, s := range a.syms {
			for dep := range s.deps {
				d := a.syms[dep]
				roles := d.roles | s.roles
				unknown := d.unknown || s.unknown
				if roles != d.roles || unknown != d.unknown {
					d.roles, d.unknown = roles, unknown
					changed = true
				}
			}
		}
	}
}

// Variable is what a template does with one of the caller's variables.
//
// Output and Flow are not exclusive and neither implies the other. A variable
// with both printed and decided something; one with neither was read but
// provably cannot change the output, which is a real answer -- `{% set unused =
// x %}` with nothing reading unused is the simplest case.
//
// Unknown means the analysis could not follow every route the variable could
// take, so it has no reliable negative: it may reach the output anyway. A
// variable reported without Unknown is a real answer in both directions.
type Variable struct {
	// Name is the variable as the template names it.
	Name string
	// Output reports that the variable's value can appear in the output,
	// possibly transformed on the way.
	Output bool
	// Flow reports that it can change the output without appearing in it --
	// a condition, a loop's length, an autoescape setting.
	Flow bool
	// Unknown reports that some route was not followed: a computed lookup, a
	// namespace, a template included under a name that is not a constant.
	Unknown bool
}

// Variables reports what the template does with each of the caller's variables.
//
// The names are the ones the template resolves from the context rather than
// every name it mentions: a loop's target, a macro's parameter and anything a
// `{% set %}` binds are the template's own, and jinja2's first-mention rule
// decides the cases where that is not obvious.
//
// The result is sorted by name.
func (t *Template) Variables() []Variable {
	// The template's own tree has been through the constant folder, and the
	// folder changes shapes this analysis reads: `{{ xs[[]] }}` becomes a
	// constant subscript, which looks like a key it can resolve when it is
	// nothing of the sort. Whether a variable can affect the output is a fact
	// about the template, not about how much the optimizer managed to prove,
	// so this reads the source as written.
	//
	// Re-parsing cannot fail here: the template compiled, so it parses.
	tree, err := parser.Parse(t.env.syntax, t.env.parseOpts, t.source, t.name)
	if err != nil {
		return nil
	}

	a := newNameflow(t)
	a.push(tree, tree.Body)
	a.stmts(tree.Body)
	a.pop()
	a.propagate()

	out := make([]Variable, 0, len(a.context))
	for name, s := range a.context {
		// The environment's globals -- range, dict, lipsum and their
		// neighbours -- are not the caller's variables. A template reading
		// one is not asking for anything.
		if _, global := t.env.globals[name]; global {
			continue
		}
		out = append(out, Variable{
			Name:   name,
			Output: s.roles&roleOutput != 0,
			Flow:   s.roles&roleFlow != 0,
			// A template that hands the whole context to another one
			// puts every variable in play.
			Unknown: s.unknown || a.opaqueSink,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
