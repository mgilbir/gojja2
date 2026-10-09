// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/mgilbir/gojja2/errs"
	"github.com/mgilbir/gojja2/value"
)

// The answers below are CPython jinja2 3.1.6's, recorded verbatim from the
// oracle: a Python expression over `t = env.get_template("main")` evaluated in
// its own capped process, with repr() of the result or `Type: message` of the
// exception. A tuple answer in a comment is split across rows here.

const (
	hmM   = `{% macro m(a, b=2, c=b) %}{{ a }}-{{ b }}-{{ c }}{% endmacro %}`
	hmV   = `{% macro v(a) %}{{ a }}|{{ varargs }}{% endmacro %}`
	hmK   = `{% macro k(a) %}{{ a }}|{{ kwargs }}{% endmacro %}`
	hmC   = `{% macro c() %}[{{ caller() }}]{% endmacro %}`
	hmLib = `{% macro f() %}F{% endmacro %}{% set libvar = 1 %}`
)

func hostModuleEnv(t *testing.T, templates map[string]string, opts ...Option) *Environment {
	t.Helper()
	return mustNew(append([]Option{WithLoader(DictLoader(templates))}, opts...)...)
}

func hostModule(t *testing.T, env *Environment, name string, vars map[string]any) *Module {
	t.Helper()
	tmpl, err := env.GetTemplate(name)
	if err != nil {
		t.Fatalf("load %s: %v", name, err)
	}
	mod, err := tmpl.Module(context.Background(), vars)
	if err != nil {
		t.Fatalf("module %s: %v", name, err)
	}
	return mod
}

// pyAnswer is how the oracle prints a result: repr() of a value, or the
// exception's class and message.
func pyAnswer(v value.Value, err error) string {
	if err != nil {
		var e *errs.Error
		if errors.As(err, &e) {
			return e.Kind.String() + ": " + e.Msg
		}
		return "Go error: " + err.Error()
	}
	return value.Repr(v)
}

func kw(name string, v value.Value) value.Kwarg { return value.Kwarg{Name: name, Value: v} }

func ints(ns ...int) []value.Value {
	out := make([]value.Value, len(ns))
	for i, n := range ns {
		out[i] = value.Int(int64(n))
	}
	return out
}

// TestModuleCallsMatchJinja: module.name(...) from the host binds and refuses
// arguments as Macro.__call__ does, wraps the result by the setting where the
// macro was defined, and reads the module's own globals, variables and state.
func TestModuleCallsMatchJinja(t *testing.T) {
	hi := Func("caller", func(*State, *value.CallArgs) (value.Value, error) { return value.String("hi"), nil })
	p := Func("caller", func(*State, *value.CallArgs) (value.Value, error) { return value.String("p"), nil })
	cases := []struct {
		id    string
		tpls  map[string]string
		main  string
		opts  []Option
		vars  map[string]any
		call  string
		args  value.CallArgs
		want  string
		twice string // a second call on the same module, when set
	}{
		// t.module.m(1)
		{id: "m_defaults", tpls: map[string]string{"main": hmM}, call: "m", args: value.CallArgs{Pos: ints(1)}, want: `'1-2-2'`},
		// t.module.m(1, b=5)
		{id: "m_kw", tpls: map[string]string{"main": hmM}, call: "m",
			args: value.CallArgs{Pos: ints(1), Kwargs: []value.Kwarg{kw("b", value.Int(5))}}, want: `'1-5-5'`},
		// t.module.m(1, c=9)
		{id: "m_kw_c", tpls: map[string]string{"main": hmM}, call: "m",
			args: value.CallArgs{Pos: ints(1), Kwargs: []value.Kwarg{kw("c", value.Int(9))}}, want: `'1-2-9'`},
		// t.module.m(a=7, b=8, c=9)
		{id: "m_all_kw", tpls: map[string]string{"main": hmM}, call: "m",
			args: value.CallArgs{Kwargs: []value.Kwarg{kw("a", value.Int(7)), kw("b", value.Int(8)), kw("c", value.Int(9))}}, want: `'7-8-9'`},
		// t.module.m()
		{id: "m_missing", tpls: map[string]string{"main": hmM}, call: "m", want: `'-2-2'`},
		// t.module.m(1, 2, 3, 4)
		{id: "m_toomany", tpls: map[string]string{"main": hmM}, call: "m", args: value.CallArgs{Pos: ints(1, 2, 3, 4)},
			want: `TypeError: macro 'm' takes not more than 3 argument(s)`},
		// t.module.m(1, z=1)
		{id: "m_badkw", tpls: map[string]string{"main": hmM}, call: "m",
			args: value.CallArgs{Pos: ints(1), Kwargs: []value.Kwarg{kw("z", value.Int(1))}},
			want: `TypeError: macro 'm' takes no keyword argument 'z'`},
		// t.module.m(1, caller=1)
		{id: "caller_twice", tpls: map[string]string{"main": hmM}, call: "m",
			args: value.CallArgs{Pos: ints(1), Kwargs: []value.Kwarg{kw("caller", value.Int(1))}},
			want: `TypeError: macro 'm' was invoked with two values for the special caller argument. This is most likely a bug.`},
		// t.module.v(1, 2, 3)
		{id: "varargs", tpls: map[string]string{"main": hmV}, call: "v", args: value.CallArgs{Pos: ints(1, 2, 3)}, want: `'1|(2, 3)'`},
		// t.module.k(1, z=3)
		{id: "kwargs", tpls: map[string]string{"main": hmK}, call: "k",
			args: value.CallArgs{Pos: ints(1), Kwargs: []value.Kwarg{kw("z", value.Int(3))}}, want: `"1|{'z': 3}"`},
		// t.module.k()
		{id: "kwargs_empty", tpls: map[string]string{"main": `{% macro k() %}{{ kwargs }}{{ varargs }}{% endmacro %}`},
			call: "k", want: `'{}()'`},
		// t.module.c(caller=lambda: 'hi')
		{id: "caller", tpls: map[string]string{"main": hmC}, call: "c",
			args: value.CallArgs{Kwargs: []value.Kwarg{kw("caller", hi)}}, want: `'[hi]'`},
		// t.module.c()
		{id: "caller_missing", tpls: map[string]string{"main": hmC}, call: "c", want: `UndefinedError: No caller defined`},
		// t.module.ce(lambda: 'p')
		{id: "caller_explicit", tpls: map[string]string{"main": `{% macro ce(caller=none) %}<{{ caller() }}>{% endmacro %}`},
			call: "ce", args: value.CallArgs{Pos: []value.Value{p}}, want: `'<p>'`},
		// (t.module.md(), t.module.md(4)) == ('3', '4')
		{id: "default_module_var", tpls: map[string]string{"main": `{% set d = 3 %}{% macro md(x=d) %}{{ x }}{% endmacro %}`},
			call: "md", want: `'3'`},
		// t.module.x()
		{id: "noncallable", tpls: map[string]string{"main": `{% set x = 42 %}`}, call: "x", want: `TypeError: 'int' object is not callable`},
		// t.module.nope
		{id: "not_exported", tpls: map[string]string{"main": `{% set x = 1 %}`}, call: "nope",
			want: `AttributeError: 'TemplateModule' object has no attribute 'nope'`},
		// t.module._h
		{id: "underscore", tpls: map[string]string{"main": `{% macro _h() %}{% endmacro %}`}, call: "_h",
			want: `AttributeError: 'TemplateModule' object has no attribute '_h'`},
		// t.module.a()
		{id: "macro_calls_macro", tpls: map[string]string{"main": `{% macro a() %}A{{ b() }}{% endmacro %}{% macro b() %}B{% endmacro %}`},
			call: "a", want: `'AB'`},
		// t.module.r(3)
		{id: "builtin_reexport", tpls: map[string]string{"main": `{% set r = range %}`}, call: "r",
			args: value.CallArgs{Pos: ints(3)}, want: `range(0, 3)`},
		// t.make_module({'v': 5}).r()
		{id: "macro_reads_var", tpls: map[string]string{"main": `{% macro r() %}{{ v }}{% endmacro %}`},
			vars: map[string]any{"v": 5}, call: "r", want: `'5'`},
		// env.globals['g'] = 'G'; t.module.r()
		{id: "macro_reads_global", tpls: map[string]string{"main": `{% macro r() %}{{ g }}{% endmacro %}`},
			vars: map[string]any{}, call: "r", want: `'G'`},
		// t.module.boom(0)
		{id: "macro_raises", tpls: map[string]string{"main": `{% macro boom(n) %}{{ 1 // n }}{% endmacro %}`},
			call: "boom", args: value.CallArgs{Pos: ints(0)}, want: `ZeroDivisionError: integer division or modulo by zero`},
		// t.module.u()
		{id: "macro_undefined", tpls: map[string]string{"main": `{% macro u() %}{{ nope.x }}{% endmacro %}`},
			call: "u", want: `UndefinedError: 'nope' is undefined`},
		// (t.module.cm(), t.module.bx) == ('C2', 2)
		{id: "extends_call", tpls: map[string]string{"main": `{% extends 'base' %}{% macro cm() %}C{{ bx }}{% endmacro %}`,
			"base": `{% set bx = 2 %}`}, call: "cm", want: `'C2'`},
		// (t.module.inc(), t.module.inc()) == ('1', '2')
		{id: "ns_persists", tpls: map[string]string{"main": `{% set ns = namespace(n=0) %}{% macro inc() %}{% set ns.n = ns.n + 1 %}{{ ns.n }}{% endmacro %}`},
			call: "inc", want: `'1'`, twice: `'2'`},
		// (t.module.s(), str(t.module)) == ('B', 'B')
		{id: "self_block", tpls: map[string]string{"main": `{% macro s() %}{{ self.b() }}{% endmacro %}{% block b %}B{% endblock %}`},
			call: "s", want: `'B'`},
		// autoescape=True: t.module.e('<')
		{id: "ae_true", tpls: map[string]string{"main": `{% macro e(x) %}{{ x }}<i>{% endmacro %}`}, opts: []Option{WithAutoescape(true)},
			call: "e", args: value.CallArgs{Pos: []value.Value{value.String("<")}}, want: `Markup('&lt;<i>')`},
		// autoescape=False: t.module.e('<')
		{id: "ae_false", tpls: map[string]string{"main": `{% macro e(x) %}{{ x }}<i>{% endmacro %}`},
			call: "e", args: value.CallArgs{Pos: []value.Value{value.String("<")}}, want: `'<<i>'`},
		// select_autoescape(["html"]) and a .txt template: t.module.e('<')
		{id: "ae_select_txt", tpls: map[string]string{"main.txt": `{% macro e(x) %}{{ x }}<i>{% endmacro %}`}, main: "main.txt",
			opts: []Option{WithAutoescapeFunc(SelectAutoescape("html"))},
			call: "e", args: value.CallArgs{Pos: []value.Value{value.String("<")}}, want: `'<<i>'`},
		// select_autoescape, main.html re-exporting lib.txt's macro:
		// (t.module.g('<'), t.module.h('<')) == ('<<i>', Markup('&lt;'))
		{id: "reexport_select_g", tpls: map[string]string{
			"main.html": `{% import 'lib.txt' as l %}{% set g = l.e %}{% macro h(x) %}{{ x }}{% endmacro %}`,
			"lib.txt":   `{% macro e(x) %}{{ x }}<i>{% endmacro %}`}, main: "main.html",
			opts: []Option{WithAutoescapeFunc(SelectAutoescape("html"))},
			call: "g", args: value.CallArgs{Pos: []value.Value{value.String("<")}}, want: `'<<i>'`},
		{id: "reexport_select_h", tpls: map[string]string{
			"main.html": `{% import 'lib.txt' as l %}{% set g = l.e %}{% macro h(x) %}{{ x }}{% endmacro %}`,
			"lib.txt":   `{% macro e(x) %}{{ x }}<i>{% endmacro %}`}, main: "main.html",
			opts: []Option{WithAutoescapeFunc(SelectAutoescape("html"))},
			call: "h", args: value.CallArgs{Pos: []value.Value{value.String("<")}}, want: `Markup('&lt;')`},
	}
	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			env := hostModuleEnv(t, tc.tpls, tc.opts...)
			env.AddGlobal("g", value.String("G"))
			main := tc.main
			if main == "" {
				main = "main"
			}
			mod := hostModule(t, env, main, tc.vars)
			args := tc.args
			if got := pyAnswer(mod.CallArgs(context.Background(), tc.call, &args)); got != tc.want {
				t.Errorf("got  %s\nwant %s", got, tc.want)
			}
			if tc.twice != "" {
				if got := pyAnswer(mod.CallArgs(context.Background(), tc.call, &args)); got != tc.twice {
					t.Errorf("second call: got  %s\nwant %s", got, tc.twice)
				}
			}
		})
	}
}

// TestModuleExportsMatchJinja: which names a module exports, what they hold,
// and what its body rendered.
func TestModuleExportsMatchJinja(t *testing.T) {
	cases := []struct {
		id    string
		tpls  map[string]string
		opts  []Option
		vars  map[string]any
		names string // sorted(vars(t.module)) less _body_stream and __name__
		body  string // str(t.module)
		get   map[string]string
	}{
		// t.module.x == 42; str(t.module) == 'body 42'
		{id: "set_top", tpls: map[string]string{"main": `{% set x = 42 %}body {{ x }}`},
			names: `['x']`, body: `body 42`, get: map[string]string{"x": `42`}},
		// ['a', 'b', 'cap', 'mm', 'x']: not an import, a from-import, an
		// underscore name, or a name bound in a loop or a block.
		{id: "names", tpls: map[string]string{"lib": hmLib, "main": `{% import 'lib' as lib %}{% from 'lib' import f %}{% set x = 1 %}{% set _h = 2 %}` +
			`{% macro mm() %}{% endmacro %}{% macro _p() %}{% endmacro %}` +
			`{% set a, b = 1, 2 %}{% set cap %}cc{% endset %}` +
			`{% for i in [1] %}{% set inloop = 1 %}{% endfor %}` +
			`{% block blk %}{% set inblock = 1 %}{% endblock %}`},
			names: `['a', 'b', 'cap', 'mm', 'x']`, get: map[string]string{"lib": "", "f": "", "_h": "", "inloop": "", "inblock": ""}},
		// ('F', 'F')
		{id: "import_used", tpls: map[string]string{"lib": hmLib, "main": `{% import 'lib' as lib %}{% set y = lib.f() %}{{ y }}`},
			names: `['y']`, body: `F`, get: map[string]string{"y": `'F'`}},
		// t.module.lib == 5: a later set exports the name again.
		{id: "set_after_import", tpls: map[string]string{"lib": "x", "main": `{% import 'lib' as lib %}{% set lib = 5 %}`},
			names: `['lib']`, get: map[string]string{"lib": `5`}},
		// (['bm', 'bx', 'c', 'cm'], '[child1]')
		{id: "extends", tpls: map[string]string{
			"main": `{% extends 'base' %}{% set c = 1 %}{% macro cm() %}C{% endmacro %}{% block b %}child{{ c }}{% endblock %}`,
			"base": `{% set bx = 2 %}[{% block b %}{% endblock %}]{% macro bm() %}B{% endmacro %}`},
			names: `['bm', 'bx', 'c', 'cm']`, body: `[child1]`},
		// (t.make_module({'v': 21}).z, str(...)) == (42, '21')
		{id: "make_module_vars", tpls: map[string]string{"main": `{% set z = v * 2 %}{{ v }}`}, vars: map[string]any{"v": 21},
			names: `['z']`, body: `21`, get: map[string]string{"z": `42`}},
		// autoescape=True: t.module.cap == Markup('<cc>');
		// str(t.module) == '&lt;'
		{id: "set_capture", tpls: map[string]string{"main": `{% set cap %}<cc>{% endset %}{{ '<' }}`}, opts: []Option{WithAutoescape(true)},
			names: `['cap']`, body: `&lt;`, get: map[string]string{"cap": `Markup('<cc>')`}},
		// t.module.e -> AttributeError: {% autoescape %} is a scope, so a
		// macro inside one is not exported.
		{id: "autoescape_block", tpls: map[string]string{"main": `{% autoescape true %}{% macro e(x) %}{{ x }}<i>{% endmacro %}{% endautoescape %}`},
			names: `[]`, get: map[string]string{"e": ""}},
		// []
		{id: "empty", tpls: map[string]string{"main": "text"}, names: `[]`, body: "text"},
	}
	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			mod := hostModule(t, hostModuleEnv(t, tc.tpls, tc.opts...), "main", tc.vars)
			names := make([]value.Value, 0)
			for _, n := range mod.Names() {
				names = append(names, value.String(n))
			}
			if got := value.Repr(value.NewList(names...)); got != tc.names {
				t.Errorf("names: got %s, want %s", got, tc.names)
			}
			if got := mod.String(); got != tc.body {
				t.Errorf("body: got %q, want %q", got, tc.body)
			}
			for name, want := range tc.get {
				v, ok := mod.Get(name)
				got := ""
				if ok {
					got = value.Repr(v)
				}
				if got != want {
					t.Errorf("Get(%q): got %q (ok=%v), want %q", name, got, ok, want)
				}
			}
		})
	}
}

// TestModuleCreationFails: make_module raises what the render raises.
func TestModuleCreationFails(t *testing.T) {
	// t.make_module() -> ZeroDivisionError: integer division or modulo by zero
	tmpl, err := hostModuleEnv(t, map[string]string{"main": `{% set q = 1 // 0 %}`}).GetTemplate("main")
	if err != nil {
		t.Fatal(err)
	}
	mod, err := tmpl.Module(context.Background(), nil)
	if got := pyAnswer(value.Undefined, err); mod != nil || got != "ZeroDivisionError: integer division or modulo by zero" {
		t.Fatalf("got %v, %s", mod, got)
	}
}

// TestModuleModulesAreFresh: each Module is its own make_module, so state one
// accumulates is not another's -- t.module.p(), t.module.p(),
// t.make_module().p() is ('1', '2', '1').
func TestModuleModulesAreFresh(t *testing.T) {
	env := hostModuleEnv(t, map[string]string{"main": `{% set l = [] %}{% macro p() %}{{ l.append(1) or l|length }}{% endmacro %}`})
	a, b := hostModule(t, env, "main", nil), hostModule(t, env, "main", nil)
	var got []string
	for _, m := range []*Module{a, a, b} {
		got = append(got, pyAnswer(m.Call(context.Background(), "p")))
	}
	if want := []string{`'1'`, `'2'`, `'1'`}; !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// TestModuleCallConvertsArguments: Call's positional arguments are converted as
// render variables are, so a macro cannot write to the caller's data -- where
// jinja2, given the list itself, appends to it. CallArgs passes values through.
func TestModuleCallConvertsArguments(t *testing.T) {
	env := hostModuleEnv(t, map[string]string{"main": `{% macro ap(l) %}{{ l.append(1) }}{{ l|length }}{% endmacro %}`})
	mod := hostModule(t, env, "main", nil)
	goList := []any{0}
	if got := pyAnswer(mod.Call(context.Background(), "ap", goList)); got != `'None2'` {
		t.Fatalf("got %s", got)
	}
	if len(goList) != 1 {
		t.Fatalf("Call wrote to the caller's slice: %v", goList)
	}
	l := value.NewList(value.Int(0))
	if got := pyAnswer(mod.CallArgs(context.Background(), "ap", &value.CallArgs{Pos: []value.Value{l}})); got != `'None2'` {
		t.Fatalf("got %s", got)
	}
	if got := value.Repr(l); got != `[0, 1]` {
		t.Fatalf("CallArgs list after the call: %s, want [0, 1] (jinja2's answer)", got)
	}
}

// TestModuleVarsAreNotRetained: a module's variables are converted before
// Module returns, so a caller changing its map afterwards changes nothing --
// and is not racing with a later call that reads it.
func TestModuleVarsAreNotRetained(t *testing.T) {
	env := hostModuleEnv(t, map[string]string{"main": `{% macro r() %}{{ v }}{% endmacro %}`})
	vars := map[string]any{"v": "before"}
	mod := hostModule(t, env, "main", vars)
	vars["v"] = "after"
	if got := pyAnswer(mod.Call(context.Background(), "r")); got != `'before'` {
		t.Fatalf("got %s", got)
	}
}

// TestModuleCallIsBounded: each call is bounded by its own ctx and by the
// environment's limits, starting afresh -- the module was made under a context
// that is over by the time it is called, and that must not fail the call.
func TestModuleCallIsBounded(t *testing.T) {
	env := hostModuleEnv(t, map[string]string{"main": `{% macro spin(n) %}{% for i in range(n) %}{% endfor %}ok{% endmacro %}`},
		WithMaxIterations(1000))
	tmpl, err := env.GetTemplate("main")
	if err != nil {
		t.Fatal(err)
	}
	made, cancel := context.WithCancel(context.Background())
	mod, err := tmpl.Module(made, nil)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	// Under the limit, twice: each call has its own allowance.
	for range 2 {
		if got := pyAnswer(mod.Call(context.Background(), "spin", 900)); got != `'ok'` {
			t.Fatalf("got %s", got)
		}
	}
	if _, err := mod.Call(context.Background(), "spin", 5000); !errors.Is(err, ErrTooManyIterations) {
		t.Fatalf("over the iteration limit: %v", err)
	}
	// The context is read every few thousand steps, as in a render, so the
	// rest of this needs a module with no iteration limit to reach it.
	unbounded := hostModuleEnv(t, map[string]string{"main": `{% macro spin(n) %}{% for i in range(n) %}{% endfor %}ok{% endmacro %}`},
		WithoutLimits())
	big := hostModule(t, unbounded, "main", nil)
	gone, stop := context.WithCancel(context.Background())
	stop()
	if _, err := big.Call(gone, "spin", int64(1)<<40); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled ctx: %v", err)
	}
	deadline, done := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer done()
	start := time.Now()
	if _, err := big.Call(deadline, "spin", int64(1)<<40); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline: %v", err)
	}
	if el := time.Since(start); el > 5*time.Second {
		t.Fatalf("deadline honoured only after %v", el)
	}
	// A refusal a global dropped still fails the call, as it fails a render --
	// here with no write after it to report it instead.
	swEnv := hostModuleEnv(t, map[string]string{"main": `{% set sw = swallow %}`}, WithMaxIterations(1000))
	swEnv.AddGlobal("swallow", Func("swallow", func(s *State, _ *value.CallArgs) (value.Value, error) {
		_ = s.Step(5000)
		return value.String("x"), nil
	}))
	sw := hostModule(t, swEnv, "main", nil)
	if _, err := sw.Call(context.Background(), "sw"); !errors.Is(err, ErrTooManyIterations) {
		t.Fatalf("dropped refusal: %v", err)
	}
	// And the module still works after a failed call.
	if got := pyAnswer(mod.Call(context.Background(), "spin", 1)); got != `'ok'` {
		t.Fatalf("after failures: %s", got)
	}
}

// TestModuleCallLeavesNothingBehind: a call that fails -- even by a panic, which
// is a bug in gojja2 and is reported as ErrInternal -- leaves the module as the
// next call needs it. A render that fails is over; a module is called again.
func TestModuleCallLeavesNothingBehind(t *testing.T) {
	env := hostModuleEnv(t, map[string]string{
		"lib": `{% macro boom() %}{{ explode() }}{% endmacro %}`,
		"main": `{% import 'lib' as l %}{% set boom = l.boom %}{% set who = name %}` +
			`{% macro bad() %}{% for x in [1, 0] if 1 // x %}{{ loop|length }}{{ 1 // 0 }}{% endfor %}{% endmacro %}` +
			`{% macro good() %}{% for x in [7] %}{{ x }}{% endfor %}{% endmacro %}`,
	})
	env.AddGlobal("explode", Func("explode", func(*State, *value.CallArgs) (value.Value, error) { panic("planted") }))
	env.AddGlobal("name", Func("name", func(s *State, _ *value.CallArgs) (value.Value, error) { return value.String(s.Name()), nil }))
	mod := hostModule(t, env, "main", nil)

	if _, err := mod.Call(context.Background(), "boom"); !errors.Is(err, ErrInternal) {
		t.Fatalf("a panic in a call: %v", err)
	}
	if got := pyAnswer(mod.Call(context.Background(), "who")); got != `'main'` {
		t.Errorf("after a panic the executing template is %s", got)
	}

	if got := pyAnswer(mod.Call(context.Background(), "bad")); got != "ZeroDivisionError: integer division or modulo by zero" {
		t.Fatalf("bad: %s", got)
	}
	if got := pyAnswer(mod.Call(context.Background(), "good")); got != `'7'` {
		t.Errorf("after a loop body failed, good() is %s", got)
	}
}

// TestModuleCallIsSerialised: concurrent calls on one module are safe and each
// sees the shared state move by exactly one. Run under -race.
func TestModuleCallIsSerialised(t *testing.T) {
	env := hostModuleEnv(t, map[string]string{"main": `{% set ns = namespace(n=0) %}{% macro inc() %}{% set ns.n = ns.n + 1 %}{{ ns.n }}{% endmacro %}`})
	mod := hostModule(t, env, "main", nil)
	var wg sync.WaitGroup
	seen := make([]string, 50)
	for i := range seen {
		wg.Go(func() {
			v, err := mod.Call(context.Background(), "inc")
			if err != nil {
				t.Error(err)
			}
			seen[i] = v.AsString()
			// Neither takes the lock: a call changes nothing they read.
			_, _ = mod.Get("ns")
			_ = mod.Names()
		})
	}
	wg.Wait()
	slices.Sort(seen)
	if seen = slices.Compact(seen); len(seen) != 50 {
		t.Fatalf("calls overlapped: %d distinct answers", len(seen))
	}
}

// TestModuleExportsOutliveTheirRender reads a record a module exported after
// the context that made the module is over. A render converts its context
// lazily, filling a record when something first needs it -- but a module's
// exports go back to the host, so they must be whole when Module returns: a
// record filled later would be charged to a finished render, and a cancelled
// one would come back short with no error.
func TestModuleExportsOutliveTheirRender(t *testing.T) {
	rec := make(map[string]any, 10000)
	for i := range 10000 {
		rec[fmt.Sprintf("k%05d", i)] = i
	}
	tmpl, err := mustNew().FromString(`{% set exported = rec %}`)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	mod, err := tmpl.Module(ctx, map[string]any{"rec": rec})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	v, ok := mod.Get("exported")
	if !ok {
		t.Fatal("exported is not exported")
	}
	d, ok := v.Dict()
	if !ok {
		t.Fatalf("exported is a %s", v.TypeName())
	}
	if n := len(d.Keys()); n != 10000 {
		t.Errorf("read after the module's context ended, the record has %d keys; want 10000", n)
	}
}
