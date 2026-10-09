// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/mgilbir/gojja2"
	"github.com/mgilbir/gojja2/value"
)

// Isolation: nothing a template changes may reach another render, another
// tenant or the host, whatever the value came from. Every test renders twice,
// or from two environments, and requires the second to see none of the first's
// changes -- and, within one render, requires one value reached two ways to be
// one value, as in jinja2.

// keeper is host state: a list it hands out from a method on every call.
type keeper struct{ list value.Value }

func (k keeper) List() value.Value { return k.list }

func TestIsolatedFromEverySource(t *testing.T) {
	newList := func() value.Value { return value.NewList(value.NewList(value.Int(1))) }
	for _, tc := range []struct {
		name  string
		setup func(env *gojja2.Environment) map[string]any
		src   string
		want  string // every render's output
	}{
		{name: "global list", src: `{% do g.append(9) %}{% do g[0].append(8) %}{{ g }}`, want: "[[1, 8], 9]",
			setup: func(env *gojja2.Environment) map[string]any { env.AddGlobal("g", newList()); return nil }},
		{name: "global dict", src: `{% do d.update(k=1) %}{{ d }}`, want: "{'k': 1}",
			setup: func(env *gojja2.Environment) map[string]any { env.AddGlobal("d", value.NewDict()); return nil }},
		{name: "global namespace", src: `{% set ns.n = ns.n + 1 %}{{ ns.n }}`, want: "1",
			setup: func(env *gojja2.Environment) map[string]any {
				v, _ := evalValue(t, `namespace(n=0)`)
				env.AddGlobal("ns", v)
				return nil
			}},
		{name: "global cycler", src: `{{ c.next() }}`, want: "a",
			setup: func(env *gojja2.Environment) map[string]any {
				v, _ := evalValue(t, `cycler("a", "b")`)
				env.AddGlobal("c", v)
				return nil
			}},
		{name: "global joiner", src: `[{{ j() }}]`, want: "[]",
			setup: func(env *gojja2.Environment) map[string]any {
				v, _ := evalValue(t, `joiner(",")`)
				env.AddGlobal("j", v)
				return nil
			}},
		{name: "global set", src: `{% do s.add(9) %}{{ s|sort }}`, want: "[1, 9]",
			setup: func(env *gojja2.Environment) map[string]any {
				s, _ := evalValue(t, `({1: 0}.keys() - [])`)
				env.AddGlobal("s", s)
				return nil
			}},
		{name: "global dict through its view", src: `{% set v = d.keys() %}{% do d.update(z=1) %}{{ v|list }}`, want: "['a', 'z']",
			setup: func(env *gojja2.Environment) map[string]any {
				env.AddGlobal("d", value.FromGo(map[string]any{"a": 1}))
				return nil
			}},
		{name: "global tuple holding a list", src: `{% do t[0].append(2) %}{{ t }}`, want: "([1, 2], 'x')",
			setup: func(env *gojja2.Environment) map[string]any {
				env.AddGlobal("t", value.NewTuple(value.NewList(value.Int(1)), value.String("x")))
				return nil
			}},
		{name: "value.Value in Render's variables", src: `{% do x.append(2) %}{{ x }}`, want: "[1, 2]",
			setup: func(*gojja2.Environment) map[string]any {
				return map[string]any{"x": value.NewList(value.Int(1))}
			}},
		{name: "value.Value nested in Go data", src: `{% do rec.l.append(2) %}{{ rec.l }}`, want: "[1, 2]",
			setup: func(*gojja2.Environment) map[string]any {
				return map[string]any{"rec": map[string]any{"l": value.NewList(value.Int(1))}}
			}},
		{name: "host function's cached result", src: `{% do cached().append(2) %}{{ cached() }}`, want: "[1, 2]",
			setup: func(env *gojja2.Environment) map[string]any {
				keep := value.NewList(value.Int(1))
				env.AddGlobal("cached", gojja2.Func("cached", func(*gojja2.State, *value.CallArgs) (value.Value, error) { return keep, nil }))
				return nil
			}},
		{name: "host filter's cached result", src: `{% do (x|cached).append(2) %}{{ x|cached }}`, want: "[1, 2]",
			setup: func(env *gojja2.Environment) map[string]any {
				keep := value.NewList(value.Int(1))
				env.AddFilter("cached", func(*gojja2.State, value.Value, *value.CallArgs) (value.Value, error) { return keep, nil })
				return map[string]any{"x": 0}
			}},
		{name: "host method's cached result", src: `{% do h.List().append(2) %}{{ h.List() }}`, want: "[1, 2]",
			setup: func(*gojja2.Environment) map[string]any {
				return map[string]any{"h": keeper{list: value.NewList(value.Int(1))}}
			}},
		{name: "FilterFunc's value.Value result", src: `{% do (x|kept).append(2) %}{{ x|kept }}`, want: "[1, 2]",
			setup: func(env *gojja2.Environment) map[string]any {
				keep := value.NewList(value.Int(1))
				env.AddFilter("kept", gojja2.FilterFunc("kept", func(any) value.Value { return keep }))
				return map[string]any{"x": 0}
			}},
		{name: "host filter folded into the tree", src: `{% set s = 1|setof %}{% do s.add(9) %}{{ s|sort }}`, want: "[1, 9]",
			setup: func(env *gojja2.Environment) map[string]any {
				keep, _ := evalValue(t, `({1: 0}.keys() - [])`)
				env.AddFilter("setof", func(*gojja2.State, value.Value, *value.CallArgs) (value.Value, error) { return keep, nil })
				return nil
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := mustEnv(gojja2.WithExtensions("do"))
			vars := tc.setup(env)
			tmpl, err := env.FromString(tc.src)
			if err != nil {
				t.Fatal(err)
			}
			for i := range 3 {
				got, err := tmpl.RenderString(context.Background(), vars)
				if err != nil || got != tc.want {
					t.Errorf("render %d = %q, %v; want %q -- an earlier render's change reached it", i+1, got, err, tc.want)
				}
			}
		})
	}
}

// evalValue is the value of a jinja expression, for building host values only a
// template can make.
func evalValue(t *testing.T, expr string) (value.Value, error) {
	t.Helper()
	x, err := mustEnv().CompileExpression(expr)
	if err != nil {
		t.Fatal(err)
	}
	v, err := x.Eval(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	return v, nil
}

// TestOneRenderSeesOneCopy is the other half: within a render, a global or a
// host value reached twice is one value, so a change through one reference is
// seen through the other -- through an include and an import too, which build
// States of their own.
func TestOneRenderSeesOneCopy(t *testing.T) {
	env := mustEnv(gojja2.WithExtensions("do"), gojja2.WithLoader(gojja2.DictLoader{
		"main": `{% do g.append(1) %}{% include "inc" %}|{% import "lib" as lib %}{{ lib.show() }}` +
			`|{% do h.List().append(2) %}{{ h.List() }}|{{ g is sameas g }}`,
		"inc": `{{ g }}`,
		"lib": `{% macro show() %}{{ g }}{% endmacro %}`,
	}))
	env.AddGlobal("g", value.NewList())
	tmpl, err := env.GetTemplate("main")
	if err != nil {
		t.Fatal(err)
	}
	vars := map[string]any{"h": keeper{list: value.NewList()}}
	for i := range 2 {
		got, err := tmpl.RenderString(context.Background(), vars)
		if want := "[1]|[1]|[2]|True"; err != nil || got != want {
			t.Errorf("render %d = %q, %v; want %q", i+1, got, err, want)
		}
	}
}

// TestTenantsDoNotShareGlobals renders through two overlays of one parent,
// which share its globals table until one writes to it, and through the parent.
func TestTenantsDoNotShareGlobals(t *testing.T) {
	parent := mustEnv(gojja2.WithExtensions("do"))
	parent.AddGlobal("g", value.NewList())
	a, err := parent.Overlay()
	if err != nil {
		t.Fatal(err)
	}
	b, err := parent.Overlay()
	if err != nil {
		t.Fatal(err)
	}
	write, _ := a.FromString(`{% do g.append("A's secret") %}{{ g }}`)
	if got, _ := write.RenderString(context.Background(), nil); got != `["A's secret"]` {
		t.Fatalf("tenant A rendered %q", got)
	}
	for name, env := range map[string]*gojja2.Environment{"tenant B": b, "parent": parent, "tenant A again": a} {
		read, _ := env.FromString(`{{ g }}`)
		if got, _ := read.RenderString(context.Background(), nil); got != "[]" {
			t.Errorf("%s saw %q", name, got)
		}
	}
}

// TestHostValuesAreNotWritten checks the host's own values after renders that
// changed what they were given.
func TestHostValuesAreNotWritten(t *testing.T) {
	global, arg, kept := value.NewList(value.Int(1)), value.NewList(value.Int(1)), value.NewList(value.Int(1))
	env := mustEnv(gojja2.WithExtensions("do"))
	env.AddGlobal("g", global)
	env.AddGlobal("f", gojja2.Func("f", func(*gojja2.State, *value.CallArgs) (value.Value, error) { return kept, nil }))
	tmpl, err := env.FromString(`{% do g.append(2) %}{% do x.append(2) %}{% do f().append(2) %}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tmpl.RenderString(context.Background(), map[string]any{"x": arg}); err != nil {
		t.Fatal(err)
	}
	for name, v := range map[string]value.Value{"global": global, "argument": arg, "function result": kept} {
		if got := value.Repr(v); got != "[1]" {
			t.Errorf("the host's %s became %s", name, got)
		}
	}
}

// TestIsolationUnderConcurrency renders one template, over one environment
// whose globals every render changes, from many goroutines; under -race it is
// the check that no render writes anything another reads.
func TestIsolationUnderConcurrency(t *testing.T) {
	env := mustEnv(gojja2.WithExtensions("do"))
	env.AddGlobal("g", value.NewList())
	env.AddGlobal("d", value.NewDict())
	tmpl, err := env.FromString(`{% do g.append(who) %}{% do d.update(k=who) %}{{ g }}{{ d }}`)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Go(func() {
			who := fmt.Sprint(i)
			got, err := tmpl.RenderString(context.Background(), map[string]any{"who": who})
			want := fmt.Sprintf("['%s']{'k': '%s'}", who, who)
			if err != nil || got != want {
				t.Errorf("goroutine %d: %q, %v; want %q", i, got, err, want)
			}
		})
	}
	wg.Wait()
}

// TestModuleKeepsItsOwnCopyOfAGlobal: a module's state outlives one call, and
// a global its macros change is part of that state -- the second call sees the
// first call's change, as in jinja2 -- but another module, like another render,
// starts from the environment's.
func TestModuleKeepsItsOwnCopyOfAGlobal(t *testing.T) {
	env := mustEnv(gojja2.WithExtensions("do"))
	env.AddGlobal("g", value.NewList())
	tmpl, err := env.FromString(`{% macro add(x) %}{% do g.append(x) %}{{ g }}{% endmacro %}`)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for range 2 {
		mod, err := tmpl.Module(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		first, err := mod.Call(ctx, "add", 1)
		if err != nil || first.AsString() != "[1]" {
			t.Fatalf("first call = %v, %v; want [1]", first, err)
		}
		second, err := mod.Call(ctx, "add", 2)
		if err != nil || second.AsString() != "[1, 2]" {
			t.Errorf("second call = %v, %v; want [1, 2], the first call's change kept", second, err)
		}
	}
}

// TestIsolationKeepsAliasesAndCycles: a global that holds one container twice
// is copied into one copy held twice, and one that holds itself is copied into
// one that holds itself -- not into two, and not forever.
func TestIsolationKeepsAliasesAndCycles(t *testing.T) {
	d := value.NewDict()
	l := value.NewList()
	cyc := value.NewList()
	cs, _ := cyc.Seq()
	cs.Append(cyc)
	env := mustEnv(gojja2.WithExtensions("do"))
	env.AddGlobal("pair", value.NewList(d, d, l, l))
	env.AddGlobal("cyc", cyc)
	tmpl, err := env.FromString(`{% do pair[0].update(k=1) %}{% do pair[2].append(2) %}` +
		`{{ pair[1] }}{{ pair[3] }}{{ pair[0] is sameas pair[1] }}|{{ cyc[0] is sameas cyc }}{{ cyc }}`)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		got, err := tmpl.RenderString(context.Background(), nil)
		if want := "{'k': 1}[2]True|True[[...]]"; err != nil || got != want {
			t.Errorf("render %d = %q, %v; want %q", i+1, got, err, want)
		}
	}
}

// TestModuleKeepsItsOwnCopyOfAHostValue: a host function that returns the list
// it keeps hands a module one copy of it for the module's whole life, as it
// would be one object in jinja2, so the second call sees the first's change.
func TestModuleKeepsItsOwnCopyOfAHostValue(t *testing.T) {
	keep := value.NewList()
	env := mustEnv(gojja2.WithExtensions("do"))
	env.AddGlobal("f", gojja2.Func("f", func(*gojja2.State, *value.CallArgs) (value.Value, error) { return keep, nil }))
	tmpl, err := env.FromString(`{% macro add(x) %}{% do f().append(x) %}{{ f() }}{% endmacro %}`)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	mod, err := tmpl.Module(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if v, err := mod.Call(ctx, "add", 1); err != nil || v.AsString() != "[1]" {
		t.Fatalf("first call = %v, %v", v, err)
	}
	if v, err := mod.Call(ctx, "add", 2); err != nil || v.AsString() != "[1, 2]" {
		t.Errorf("second call = %v, %v; want [1, 2]", v, err)
	}
	if got := value.Repr(keep); got != "[]" {
		t.Errorf("the host's list became %s", got)
	}
}

// TestIsolationOfSetsAndViews: adding to a set is idempotent, so a render that
// added to the environment's own set would look the same the second time --
// the set itself is what has to be checked. And a view held beside its dict in
// one global is a view of the render's copy of that dict, not of the original.
func TestIsolationOfSetsAndViews(t *testing.T) {
	set, _ := evalValue(t, `({1: 0}.keys() - [])`)
	d := value.FromGo(map[string]any{"a": 1})
	view, _ := evalValueWith(t, `d.keys()`, map[string]value.Value{"d": d})
	env := mustEnv(gojja2.WithExtensions("do"))
	env.AddGlobal("s", set)
	env.AddGlobal("dv", value.NewList(d, view))
	tmpl, err := env.FromString(`{% do s.add(9) %}{% do dv[0].update(z=1) %}{{ dv[1]|list }}`)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := tmpl.RenderString(context.Background(), nil); err != nil || got != "['a', 'z']" {
		t.Errorf("render = %q, %v; want the view to show the render's own change", got, err)
	}
	read, _ := env.FromString(`{{ s|length }}|{{ dv[1]|list }}`)
	if got, err := read.RenderString(context.Background(), nil); err != nil || got != "1|['a']" {
		t.Errorf("after a render that changed them, the globals read %q, %v; want 1|['a']", got, err)
	}
}

// evalValueWith is evalValue over values.
func evalValueWith(t *testing.T, expr string, vars map[string]value.Value) (value.Value, error) {
	t.Helper()
	x, err := mustEnv().CompileExpression(expr)
	if err != nil {
		t.Fatal(err)
	}
	v, err := x.EvalValues(context.Background(), vars)
	if err != nil {
		t.Fatal(err)
	}
	return v, nil
}

// TestIsolationOfProxiesAndGroups: a mapping proxy held beside its dict is a
// proxy of the render's copy, and a |groupby pair's list of items is the
// render's own.
func TestIsolationOfProxiesAndGroups(t *testing.T) {
	d := value.FromGo(map[string]any{"a": 1})
	proxy, _ := evalValueWith(t, `d.keys().mapping`, map[string]value.Value{"d": d})
	group, _ := evalValue(t, `([{"k": 1}]|groupby("k"))[0]`)
	env := mustEnv(gojja2.WithExtensions("do"))
	env.AddGlobal("dp", value.NewList(d, proxy))
	env.AddGlobal("grp", group)
	tmpl, err := env.FromString(`{% do dp[0].update(z=1) %}{{ dp[1]|length }}{% do grp[1].append(0) %}{{ grp[1]|length }}`)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		if got, err := tmpl.RenderString(context.Background(), nil); err != nil || got != "22" {
			t.Errorf("render %d = %q, %v; want 22", i+1, got, err)
		}
	}
}

// bigConfig is a global of n records, each a dict holding a list.
func bigConfig(n int) value.Value {
	m := map[string]any{}
	for i := range n {
		m[fmt.Sprintf("k%d", i)] = map[string]any{"name": fmt.Sprint(i), "tags": []any{"a"}}
	}
	return value.FromGo(m)
}

// TestLazyGlobalCopies holds the lazy copy of a dict global to the eager one:
// what is read before the copy fills is what the fill holds, a change reaches
// only the render's copy at any depth, and the copy charges what it will hold.
func TestLazyGlobalCopies(t *testing.T) {
	env := mustEnv(gojja2.WithExtensions("do"))
	site := bigConfig(50)
	env.AddGlobal("site", site)
	tmpl, err := env.FromString(
		`{% set a = site.k7 %}{% do a.tags.append("b") %}{{ site.k7.tags }}` +
			`|{% for k, v in site|dictsort %}{% if k == "k7" %}{{ v is sameas a }}{{ v.tags is sameas a.tags }}{% endif %}{% endfor %}` +
			`|{{ site|length }}{% do site.update(new=1) %}{{ site|length }}`)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		got, err := tmpl.RenderString(context.Background(), nil)
		if want := "['a', 'b']|TrueTrue|5051"; err != nil || got != want {
			t.Errorf("render %d = %q, %v; want %q", i+1, got, err, want)
		}
	}
	d, _ := site.Dict()
	k7, _ := d.GetString("k7")
	k7d, _ := k7.Dict()
	tags, _ := k7d.GetString("tags")
	if d.Len() != 50 || value.Repr(tags) != "['a']" {
		t.Errorf("the global became len %d, tags %s", d.Len(), value.Repr(tags))
	}
}

// TestLazyGlobalCopyIsCharged: the copy of a global is charged when it is made,
// for every entry it will hold, so a large global read once is bounded like a
// large argument.
func TestLazyGlobalCopyIsCharged(t *testing.T) {
	env := mustEnv(gojja2.WithMaxIterations(1000))
	env.AddGlobal("g", bigConfig(2000))
	tmpl, err := env.FromString(`{% set n = g|length %}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tmpl.RenderString(context.Background(), nil); !errors.Is(err, gojja2.ErrTooManyIterations) {
		t.Errorf("err = %v, want ErrTooManyIterations", err)
	}
}

// TestGlobalsLeavingTheRenderAreWhole: an expression's result and a module's
// exports go back to the host, and must be complete copies when they do --
// read after their context is over, and independent of the global.
func TestGlobalsLeavingTheRenderAreWhole(t *testing.T) {
	env := mustEnv()
	site := bigConfig(5000)
	env.AddGlobal("site", site)
	ctx, cancel := context.WithCancel(context.Background())
	x, err := env.CompileExpression("site")
	if err != nil {
		t.Fatal(err)
	}
	v, err := x.Eval(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	tmpl, err := env.FromString(`{% set exported = site %}`)
	if err != nil {
		t.Fatal(err)
	}
	mod, err := tmpl.Module(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	// The host changes its global after both have returned, as it may
	// between renders. A copy made in full is a snapshot and does not see
	// it; a lazy one, filled when first read, would.
	g, _ := site.Dict()
	g.SetString("k0", value.String("changed after"))
	exported, _ := mod.Get("exported")
	for name, got := range map[string]value.Value{"Eval": v, "Module": exported} {
		d, ok := got.Dict()
		if !ok {
			t.Fatalf("%s gave a %s", name, got.TypeName())
		}
		if n := len(d.Keys()); n != 5000 {
			t.Errorf("%s: read after its context ended, %d keys; want 5000", name, n)
		}
		if k0, _ := d.GetString("k0"); k0.IsString() {
			t.Errorf("%s: saw the host's later change, so it was not copied before it was handed out", name)
		}
	}
}

// TestLazyGlobalsUnderConcurrency: one global, copied lazily by many renders at
// once, each changing its copy at depth; under -race nothing is shared.
func TestLazyGlobalsUnderConcurrency(t *testing.T) {
	env := mustEnv(gojja2.WithExtensions("do"))
	env.AddGlobal("site", bigConfig(50))
	tmpl, err := env.FromString(`{% do site.k3.tags.append(who) %}{% do site.update(w=who) %}{{ site.k3.tags }}{{ site.w }}`)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Go(func() {
			who := fmt.Sprint(i)
			got, err := tmpl.RenderString(context.Background(), map[string]any{"who": who})
			if want := fmt.Sprintf("['a', '%s']%s", who, who); err != nil || got != want {
				t.Errorf("goroutine %d: %q, %v; want %q", i, got, err, want)
			}
		})
	}
	wg.Wait()
}
