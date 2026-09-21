// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"github.com/mgilbir/gojja2/errs"
	"github.com/mgilbir/gojja2/internal/ast"
	"github.com/mgilbir/gojja2/value"
)

// contextCall is what CPython prints for the callable in jinja2's generated
// code for a macro, a global or anything else a template calls by name: the
// call goes through Context.call, so that is the function a refused unpacking
// names. A filter or a test is called directly and names itself.
const contextCall = "jinja2.runtime.Context.call()"

// macroNameRepr is how jinja2 prints a macro in an argument error: `{name!r}`,
// where the name of the macro a `{% call %}` block builds is None rather than a
// string. Reporting it as ” named a macro that has no name at all.
func macroNameRepr(name string) string {
	if name == "" {
		return "None"
	}
	return value.Repr(value.String(name))
}

// filterCallee is the callable CPython would name for this filter.
//
// A host filter has no Python function behind it, and the template's own name
// for it is the only honest answer; a stock name the host has overridden is
// one of those, which is why the table is consulted only for the stock ones.
func (ex *exec) filterCallee(name string) string {
	if sig, ok := filterSignatures[name]; ok && ex.st.env.stockFilters[name] {
		return sig.pyCallName
	}
	return name + "()"
}

// testCallee is filterCallee for the test table.
func (ex *exec) testCallee(name string) string {
	if sig, ok := testSignatures[name]; ok && ex.st.env.stockTests[name] {
		return sig.pyCallName
	}
	return name + "()"
}

// evalArgs builds a call's argument list, expanding `*args` and `**kwargs`.
//
// callee is the function CPython would name in an unpacking error, which is
// the call site's name for it and not the one a binding error inside it uses.
func (ex *exec) evalArgs(a ast.Args, callee string) (*value.CallArgs, error) {
	out := &value.CallArgs{}
	for _, arg := range a.Args {
		v, err := ex.eval(arg)
		if err != nil {
			return nil, err
		}
		out.Pos = append(out.Pos, v)
	}
	if a.DynArgs != nil {
		v, err := ex.eval(a.DynArgs)
		if err != nil {
			return nil, err
		}
		seq, err := value.Iterate(v)
		if err != nil {
			// Unnamed, unlike the ** message below: jinja2 always
			// passes something before the star -- the filtered
			// value, or the macro being called -- so CPython
			// builds the list separately and has no callable to
			// name by the time it refuses.
			return nil, errs.New(errs.TypeError,
				"Value after * must be an iterable, not %s", v.TypeName())
		}
		for item := range seq {
			// `f(*range(10000000000))` builds the argument list before
			// the call happens, so the walk is charged as it goes.
			if err := ex.st.Step(1); err != nil {
				return nil, err
			}
			out.Pos = append(out.Pos, item)
		}
	}
	for _, kw := range a.Kwargs {
		v, err := ex.eval(kw.Value)
		if err != nil {
			return nil, err
		}
		out.Kwargs = append(out.Kwargs, value.Kwarg{Name: kw.Key, Value: v})
	}
	if a.DynKwargs != nil {
		v, err := ex.eval(a.DynKwargs)
		if err != nil {
			return nil, err
		}
		d, ok := v.Dict()
		if !ok {
			return nil, errs.New(errs.TypeError,
				"%s argument after ** must be a mapping, not %s",
				callee, v.TypeName())
		}
		// Only the keywords written out at the call site can collide
		// with this expansion. A mapping holds each key once -- a
		// Markup key and a plain one of the same text are one entry, as
		// they are in Python -- and the grammar allows a single ** per
		// call, which TestOnlyOneKeywordExpansion pins. So two entries
		// of this expansion cannot carry the same name.
		//
		// That is what makes the check a scan of a handful rather than
		// of everything merged so far. CallArgs.Kwarg scans all of it,
		// which is the right shape for a call written out and the wrong
		// one here, where the count is the caller's: it made `{{
		// dict(**ctx) }}` quadratic, three minutes and thirteen seconds
		// over a context of 300,000 keys, against twenty-two
		// milliseconds to walk the same dictionary with |items.
		written := out.Kwargs[:len(out.Kwargs):len(out.Kwargs)]
		for _, e := range d.Entries() {
			// Charged before the slice grows to hold it, which is
			// also what lets a deadline stop the merge: the entries
			// are the caller's and there may be a great many.
			if err := ex.st.Step(1); err != nil {
				return nil, err
			}
			if e.Key.Kind() != value.KindString {
				return nil, errs.New(errs.TypeError, "keywords must be strings")
			}
			name := e.Key.AsString()
			// The merge happens before the call, so a name given
			// twice is refused here rather than by the binding --
			// which words it differently, and without "keyword".
			for _, kw := range written {
				if kw.Name == name {
					return nil, errs.New(errs.TypeError,
						"%s got multiple values for keyword argument %s",
						callee, value.Repr(e.Key))
				}
			}
			out.Kwargs = append(out.Kwargs, value.Kwarg{Name: name, Value: e.Value})
		}
	}
	return out, nil
}

func (ex *exec) evalCall(n *ast.Call) (value.Value, error) {
	callee, err := ex.eval(n.Node)
	if err != nil {
		return value.Undefined, err
	}
	args, err := ex.evalArgs(n.Args, contextCall)
	if err != nil {
		return value.Undefined, err
	}
	return ex.invoke(callee, args)
}

// invoke calls a value.
func (ex *exec) invoke(callee value.Value, args *value.CallArgs) (value.Value, error) {
	if callee.IsUndefined() {
		return value.Undefined, callee.UndefinedError()
	}
	if m, ok := callee.Interface().(*macroObject); ok {
		return ex.callMacro(m, args)
	}
	// A stateful callable is handed the render, so it can charge the budget
	// for whatever it is about to do.
	if fn, ok := callee.Interface().(statefulCaller); ok {
		return fn.callWith(ex.st, args)
	}
	if fn, ok := callee.Interface().(value.Caller); ok {
		return fn.Call(args)
	}
	return value.Undefined, errs.New(errs.TypeError,
		"'%s' object is not callable", callee.TypeName())
}

// callMacro binds arguments and renders a macro body.
//
// The binding order is jinja2's, and so are the refusals: a macro accepts
// extra positional or keyword arguments only if its body reads `varargs` or
// `kwargs`, and passing them otherwise is a TypeError rather than something
// silently dropped.
func (ex *exec) callMacro(m *macroObject, args *value.CallArgs) (value.Value, error) {
	if err := ex.st.enter(); err != nil {
		return value.Undefined, err
	}
	defer ex.st.leave()

	params := m.node.Args
	sc := newScope(m.defScope)

	// Keyword arguments are consumed as they are matched; whatever is left
	// decides between `kwargs` and an error.
	remaining := make([]value.Kwarg, len(args.Kwargs))
	copy(remaining, args.Kwargs)
	take := func(name string) (value.Value, bool) {
		for i, kw := range remaining {
			if kw.Name == name {
				remaining = append(remaining[:i], remaining[i+1:]...)
				return kw.Value, true
			}
		}
		return value.Undefined, false
	}

	consumed := min(len(args.Pos), len(params))
	bound := make(map[string]bool, len(params))
	for i := range consumed {
		sc.set(params[i].Name, args.Pos[i])
		bound[params[i].Name] = true
	}

	foundCaller := false
	if consumed != len(params) {
		for _, param := range params[consumed:] {
			if v, ok := take(param.Name); ok {
				sc.set(param.Name, v)
				bound[param.Name] = true
				if param.Name == "caller" {
					foundCaller = true
				}
			}
		}
	} else {
		foundCaller = m.explicitCaller
	}

	if m.caller && !foundCaller {
		caller, ok := take("caller")
		if !ok || caller.IsNone() {
			caller = ex.st.Undefined(value.UndefinedHint("No caller defined"))
		}
		sc.set("caller", caller)
		bound["caller"] = true
	}

	if m.catchKwargs {
		extra := value.NewDict()
		d, _ := extra.Dict()
		for _, kw := range remaining {
			d.SetString(kw.Name, kw.Value)
		}
		sc.set("kwargs", extra)
	} else if len(remaining) > 0 {
		for _, kw := range remaining {
			if kw.Name == "caller" {
				return value.Undefined, errs.New(errs.TypeError,
					"macro %s was invoked with two values for the special"+
						" caller argument. This is most likely a bug.",
					macroNameRepr(m.name))
			}
		}
		return value.Undefined, errs.New(errs.TypeError,
			"macro %s takes no keyword argument %s",
			macroNameRepr(m.name),
			value.Repr(value.String(remaining[0].Name)))
	}

	if m.catchVarargs {
		sc.set("varargs", value.NewTuple(args.Pos[consumed:]...))
	} else if len(args.Pos) > len(params) {
		return value.Undefined, errs.New(errs.TypeError,
			"macro %s takes not more than %d argument(s)",
			macroNameRepr(m.name), len(params))
	}

	// Defaults fill the parameters still unbound; the rest stay undefined.
	//
	// The expression is evaluated here, at the call, and in the macro's own
	// frame -- jinja2 compiles defaults into the macro body, so they run
	// once per call and see the parameters bound before them. Two things
	// turn on that: `{% macro m(v=[]) %}` gets a fresh list every call
	// rather than sharing one, and `{% macro m(a, b=2, c=b) %}` resolves
	// c to 2 rather than to an undefined.
	firstDefault := len(params) - len(m.defaults)
	defEx := ex.child(sc)
	for i, param := range params {
		if bound[param.Name] {
			continue
		}
		if i >= firstDefault {
			v, err := defEx.eval(m.defaults[i-firstDefault])
			if err != nil {
				return value.Undefined, err
			}
			sc.set(param.Name, v)
			continue
		}
		sc.set(param.Name, ex.st.Undefined(value.NewUndefined(param.Name)))
	}

	if err := declareFrameLocals(sc, ex.st, m.node, m.node.Body, sc.parent); err != nil {
		return value.Undefined, err
	}

	// The two halves of a macro's escaping come from different places, and
	// jinja2 says why in Macro.__call__: "whether a macro is safe depends
	// not on the escape mode when it was defined, but rather when it was
	// used".
	//
	//   - The text the body prints escapes by the setting where the macro
	//     was *written*. jinja2 compiles that in: the body of a macro
	//     defined outside {% autoescape %} emits str(...), not escape(...),
	//     whatever the call site does.
	//   - Whether the result is Markup is decided at the *call*, because
	//     Macro.__call__ takes the caller's eval context and wraps on that.
	//
	// The filters inside the body follow the call too, through the render
	// state that {% autoescape %} moves for the dynamic extent of its body.
	// So a macro written outside the block and called inside it prints by
	// its own escaping and is trusted by the block's, and the two settings
	// can differ within one call -- which is the pair CPython produces.
	autoescape := m.autoescape
	prevTmpl := ex.st.tmpl
	ex.st.tmpl = m.tmpl
	text, err := ex.captureFunction(sc, func(sub *exec) error {
		sub.autoescape = autoescape
		sub.volatileEscape = m.volatileEscape
		// super() in the body means the block the macro was *written*
		// in, not the one that called it: a macro defined at template
		// level has no super() however deep in a block it is invoked,
		// and one defined inside a block keeps that block's super()
		// wherever it travels to.
		sub.blockName, sub.blockIndex = m.blockName, m.blockIndex
		return sub.execBody(m.node.Body)
	})
	ex.st.tmpl = prevTmpl
	if err != nil {
		return value.Undefined, err
	}
	return markup(text, ex.autoescape), nil
}

// execCallBlock runs `{% call %}`: the block body becomes a `caller` macro the
// invoked macro can render.
func (ex *exec) execCallBlock(n *ast.CallBlock) error {
	// jinja2 builds the caller as an unnamed macro, so `caller.name` is
	// None inside the macro that renders it.
	callerMacro, err := ex.makeMacro("", &ast.Macro{
		Pos:      ast.At(n.Line()),
		Name:     "caller",
		Args:     n.Args,
		Defaults: n.Defaults,
		Body:     n.Body,
	}, n.Args, n.Defaults)
	if err != nil {
		return err
	}

	callee, err := ex.eval(n.Call.Node)
	if err != nil {
		return err
	}
	args, err := ex.evalArgs(n.Call.Args, contextCall)
	if err != nil {
		return err
	}

	// jinja2 hands the block body to the macro as a `caller` keyword
	// argument, which is why invoking a macro that already has one is a
	// reported bug rather than a silent overwrite.
	args.Kwargs = append(args.Kwargs,
		value.Kwarg{Name: "caller", Value: value.FromObject(callerMacro)})

	v, err := ex.invoke(callee, args)
	if err != nil {
		return err
	}
	text, err := ex.renderValue(v)
	if err != nil {
		return err
	}
	return ex.write(text)
}

// --- filters and tests -------------------------------------------------------

func (ex *exec) evalFilter(n *ast.Filter) (value.Value, error) {
	if n.Node == nil {
		return value.Undefined, errs.New(errs.TemplateRuntimeError,
			"filter %s has no input", value.Repr(value.String(n.Name)))
	}
	input, err := ex.eval(n.Node)
	if err != nil {
		return value.Undefined, err
	}
	return ex.applyFilter(n, input)
}

func (ex *exec) applyFilter(n *ast.Filter, input value.Value) (value.Value, error) {
	fn, ok := ex.st.env.filters[n.Name]
	if !ok {
		// Compile-time checking skips soft frames, so an unknown name
		// can still reach here; jinja2 words the late failure with a
		// trailing "found." to distinguish the two.
		return value.Undefined, errs.New(errs.TemplateRuntimeError,
			"No filter named %s found.", value.Repr(value.String(n.Name)))
	}
	args, err := ex.evalArgs(n.Args, ex.filterCallee(n.Name))
	if err != nil {
		return value.Undefined, err
	}
	if sig, known := filterSignatures[n.Name]; known && ex.st.env.stockFilters[n.Name] {
		if err := checkArity(sig, args); err != nil {
			return value.Undefined, errs.At(err, ex.st.tmpl.name, n.Line())
		}
	}
	out, err := fn(ex.st, input, args)
	if err != nil {
		return value.Undefined, errs.At(err, ex.st.tmpl.name, n.Line())
	}
	return out, nil
}

// applyFilterChain feeds a captured body into a filter chain whose innermost
// input was left nil by the parser, as in `{% filter upper %}`.
func (ex *exec) applyFilterChain(e ast.Expr, input value.Value) (value.Value, error) {
	f, ok := e.(*ast.Filter)
	if !ok {
		return value.Undefined, errs.New(errs.TemplateRuntimeError, "expected a filter")
	}
	if f.Node != nil {
		inner, err := ex.applyFilterChain(f.Node, input)
		if err != nil {
			return value.Undefined, err
		}
		return ex.applyFilter(f, inner)
	}
	return ex.applyFilter(f, input)
}

func (ex *exec) evalTest(n *ast.Test) (value.Value, error) {
	fn, ok := ex.st.env.tests[n.Name]
	if !ok {
		return value.Undefined, errs.New(errs.TemplateRuntimeError,
			"No test named %s found.", value.Repr(value.String(n.Name)))
	}
	input, err := ex.eval(n.Node)
	if err != nil {
		return value.Undefined, err
	}
	args, err := ex.evalArgs(n.Args, ex.testCallee(n.Name))
	if err != nil {
		return value.Undefined, err
	}
	if sig, known := testSignatures[n.Name]; known && ex.st.env.stockTests[n.Name] {
		if err := checkArity(sig, args); err != nil {
			return value.Undefined, errs.At(err, ex.st.tmpl.name, n.Line())
		}
	}
	ok, err = fn(ex.st, input, args)
	if err != nil {
		return value.Undefined, errs.At(err, ex.st.tmpl.name, n.Line())
	}
	return value.Bool(ok), nil
}

// --- assignment --------------------------------------------------------------

// assign binds a value to a target, unpacking tuples.
func (ex *exec) assign(target ast.Expr, v value.Value) error {
	switch t := target.(type) {
	case *ast.Name:
		ex.sc.set(t.Name, v)
		if ex.sc == ex.st.ctx {
			// A top-level assignment also lands in context.vars,
			// which is what a block body resolves against.
			ex.st.contextVars.set(t.Name, v)
			ex.st.export(t.Name)
		}
		return nil

	case *ast.NSRef:
		// The target's type is checked before it is used, so a name that
		// resolves to undefined reports the namespace error rather than
		// the undefined one -- the fix is to create a namespace either
		// way, and that is what the message should say.
		base, _, err := ex.sc.lookup(t.Name)
		if err != nil {
			return err
		}
		ns, ok := base.Interface().(*namespaceObject)
		if !ok {
			return errs.New(errs.TemplateRuntimeError,
				"cannot assign attribute on non-namespace object")
		}
		ns.SetAttr(t.Attr, v)
		return nil

	case *ast.Tuple:
		return ex.unpack(t, v)
	}
	return errs.New(errs.TemplateRuntimeError, "cannot assign to %s", target.TypeName())
}

func (ex *exec) unpack(t *ast.Tuple, v value.Value) error {
	seq, err := value.Iterate(v)
	if err != nil {
		return errs.New(errs.TypeError, "cannot unpack non-iterable %s object", v.TypeName())
	}
	var items []value.Value
	for item := range seq {
		// The length check below happens only once the whole iterable is
		// in hand, so `{% set a, b = range(10000000000) %}` would
		// allocate its way to the error without this.
		if err := ex.st.Step(1); err != nil {
			return err
		}
		items = append(items, item)
	}
	switch {
	case len(items) < len(t.Items):
		return errs.New(errs.ValueError,
			"not enough values to unpack (expected %d, got %d)", len(t.Items), len(items))
	case len(items) > len(t.Items):
		if ex.pyVersion().UnpackErrorNamesTheCount() {
			return errs.New(errs.ValueError,
				"too many values to unpack (expected %d, got %d)",
				len(t.Items), len(items))
		}
		return errs.New(errs.ValueError,
			"too many values to unpack (expected %d)", len(t.Items))
	}
	for i, target := range t.Items {
		if err := ex.assign(target, items[i]); err != nil {
			return err
		}
	}
	return nil
}

// escapeHTML is markupsafe's escape, kept here as the name the renderer uses.
func escapeHTML(s string) string { return value.EscapeHTML(s) }
