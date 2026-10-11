// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/mgilbir/gojja2"
	"github.com/mgilbir/gojja2/errs"
	"github.com/mgilbir/gojja2/value"
)

// Stateless renders: every test runs the same thing twice and requires the
// second run to see nothing of the first, by default -- and, with the matching
// SharedState flag, requires jinja2's behaviour back, so each flag is shown to
// relax exactly one thing.

// hostCounter is host state a template can change only through a pointer method.
type hostCounter struct{ N int }

func (c *hostCounter) Inc() int { c.N++; return c.N }
func (c hostCounter) Peek() int { return c.N }
func (c hostCounter) Bump() int { c.N++; return c.N } // the copy's N, not the host's

func newShared(t *testing.T, s gojja2.SharedState) *gojja2.Environment {
	t.Helper()
	env, err := gojja2.New(gojja2.WithExtensions("do"), gojja2.WithSharedState(s))
	if err != nil {
		t.Fatal(err)
	}
	return env
}

func TestPreparedValuesAreCopiedUnlessShared(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		render func(t *testing.T, env *gojja2.Environment, vars map[string]value.Value) string
	}{
		{"RenderValues", func(t *testing.T, env *gojja2.Environment, vars map[string]value.Value) string {
			tmpl, err := env.FromString(`{% do xs.append(9) %}{% do d.update(k=1) %}{{ xs }}{{ d }}`)
			if err != nil {
				t.Fatal(err)
			}
			var out strings.Builder
			if err := tmpl.RenderValues(ctx, &out, vars); err != nil {
				t.Fatal(err)
			}
			return out.String()
		}},
		{"RenderBlockValues", func(t *testing.T, env *gojja2.Environment, vars map[string]value.Value) string {
			tmpl, err := env.FromString(`{% block b %}{% do xs.append(9) %}{% do d.update(k=1) %}{{ xs }}{{ d }}{% endblock %}`)
			if err != nil {
				t.Fatal(err)
			}
			var out strings.Builder
			if err := tmpl.RenderBlockValues(ctx, &out, "b", vars); err != nil {
				t.Fatal(err)
			}
			return out.String()
		}},
		{"EvalValues", func(t *testing.T, env *gojja2.Environment, vars map[string]value.Value) string {
			x, err := env.CompileExpression(`(xs.append(9), d.update(k=1), xs, d)[2:]`)
			if err != nil {
				t.Fatal(err)
			}
			v, err := x.EvalValues(ctx, vars)
			if err != nil {
				t.Fatal(err)
			}
			return value.Str(v)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, mode := range []struct {
				shared gojja2.SharedState
				second string // the second run's output
			}{
				{0, ""},
				{gojja2.ShareRenderValues, "grows"},
			} {
				env := newShared(t, mode.shared)
				vars := map[string]value.Value{"xs": value.NewList(value.Int(1)), "d": value.NewDict()}
				first := tc.render(t, env, vars)
				second := tc.render(t, env, vars)
				if mode.shared == 0 {
					if first != second {
						t.Errorf("stateless: second run saw the first's changes:\n first  %s\n second %s", first, second)
					}
					if got := value.Str(vars["xs"]); got != "[1]" {
						t.Errorf("stateless: caller's list is now %s, want [1]", got)
					}
				} else if first == second {
					t.Errorf("ShareRenderValues: second run %s did not see the first's append", second)
				}
			}
		})
	}
}

func TestModuleCallsStartFresh(t *testing.T) {
	ctx := context.Background()
	src := `{% set ns = namespace(xs=[]) %}{% set top = [] %}` +
		`{% macro add(x) %}{% do ns.xs.append(x) %}{% do top.append(x) %}{{ ns.xs }}{{ top }}{% endmacro %}`
	for _, tc := range []struct {
		shared gojja2.SharedState
		want   []string
	}{
		{0, []string{"['q']['q']", "['q']['q']"}},
		{gojja2.ShareModuleState, []string{"['q']['q']", "['q', 'q']['q', 'q']"}},
	} {
		env := newShared(t, tc.shared)
		tmpl, err := env.FromString(src)
		if err != nil {
			t.Fatal(err)
		}
		mod, err := tmpl.Module(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		held, _ := mod.Get("top")
		for i, want := range tc.want {
			v, err := mod.Call(ctx, "add", "q")
			if err != nil {
				t.Fatal(err)
			}
			if value.Str(v) != want {
				t.Errorf("shared=%d call %d: got %s, want %s", tc.shared, i, value.Str(v), want)
			}
		}
		if tc.shared == 0 && value.Str(held) != "[]" {
			t.Errorf("a value Get returned was changed by a later call: %s", value.Str(held))
		}
	}
}

// TestModuleCallStartsFreshInANestedScope: a macro defined inside a loop closes
// over the loop's scope, not the module's top level, and what it changes there
// is module state too.
func TestModuleCallStartsFreshInANestedScope(t *testing.T) {
	ctx := context.Background()
	env := newShared(t, 0)
	tmpl, err := env.FromString(`{% set ns = namespace(m=none) %}` +
		`{% for xs in [[]] %}{% macro add(x) %}{% do xs.append(x) %}{{ xs }}{% endmacro %}{% set ns.m = add %}{% endfor %}` +
		`{% set m = ns.m %}`)
	if err != nil {
		t.Fatal(err)
	}
	mod, err := tmpl.Module(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		v, err := mod.Call(ctx, "m", "q")
		if err != nil || value.Str(v) != "['q']" {
			t.Errorf("call %d = %s, %v; want ['q']", i, value.Str(v), err)
		}
	}
}

func TestModuleCallStartsFreshAfterAFailure(t *testing.T) {
	ctx := context.Background()
	env := newShared(t, 0)
	tmpl, err := env.FromString(`{% set top = [] %}{% macro add(x) %}{% do top.append(x) %}{{ 1 // x }}{% endmacro %}`)
	if err != nil {
		t.Fatal(err)
	}
	mod, err := tmpl.Module(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mod.Call(ctx, "add", 0); err == nil {
		t.Fatal("1 // 0 did not fail")
	}
	if v, _ := mod.Get("top"); value.Str(v) != "[]" {
		t.Errorf("a failed call's append survived it: %s", value.Str(v))
	}
}

func TestModuleCallArgsAreCopiedUnlessShared(t *testing.T) {
	ctx := context.Background()
	for _, shared := range []gojja2.SharedState{0, gojja2.ShareRenderValues} {
		env := newShared(t, shared)
		tmpl, err := env.FromString(`{% macro add(xs) %}{% do xs.append(9) %}{% endmacro %}`)
		if err != nil {
			t.Fatal(err)
		}
		mod, err := tmpl.Module(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		xs := value.NewList(value.Int(1))
		if _, err := mod.CallArgs(ctx, "add", &value.CallArgs{Pos: []value.Value{xs}}); err != nil {
			t.Fatal(err)
		}
		want := "[1]"
		if shared != 0 {
			want = "[1, 9]"
		}
		if value.Str(xs) != want {
			t.Errorf("shared=%d: caller's list is %s, want %s", shared, value.Str(xs), want)
		}
	}
}

func TestPointerMethodsAreRefusedUnlessAllowed(t *testing.T) {
	ctx := context.Background()
	for _, src := range []string{
		`{{ c.Inc() }}`,
		`{{ [3, 1]|sort(attribute="x") if false else [c]|map(attribute="Inc")|map("string")|list }}{{ c.Inc() }}`,
		`{% set f = c.Inc %}{{ f() }}`,
	} {
		env := newShared(t, 0)
		tmpl, err := env.FromString(src)
		if err != nil {
			t.Fatal(err)
		}
		c := &hostCounter{}
		_, err = tmpl.RenderString(ctx, map[string]any{"c": c})
		if !errors.Is(err, gojja2.ErrSharedState) {
			t.Errorf("%s: err = %v, want one wrapping ErrSharedState", src, err)
		}
		if !errors.Is(err, errs.TemplateRuntimeError) {
			t.Errorf("%s: err = %v, want a TemplateRuntimeError", src, err)
		}
		if err != nil && !strings.Contains(err.Error(), "WithSharedState(AllowPointerMethods)") {
			t.Errorf("%s: the refusal does not name the option that allows it: %v", src, err)
		}
		if c.N != 0 {
			t.Errorf("%s: the refused method ran: N = %d", src, c.N)
		}
	}

	// Methods declared on the value receive a copy of the struct, so they
	// run whatever the setting, through a pointer or not.
	env := newShared(t, 0)
	tmpl, err := env.FromString(`{{ c.Peek() }} {{ c.Bump() }} {{ c.Peek() }}`)
	if err != nil {
		t.Fatal(err)
	}
	c := &hostCounter{N: 4}
	got, err := tmpl.RenderString(ctx, map[string]any{"c": c})
	if err != nil || got != "4 5 4" || c.N != 4 {
		t.Errorf("value methods: got %q, %v, N=%d", got, err, c.N)
	}

	// Allowed, the pointer method is the host's object, as in jinja2.
	env = newShared(t, gojja2.AllowPointerMethods)
	tmpl, err = env.FromString(`{{ c.Inc() }}`)
	if err != nil {
		t.Fatal(err)
	}
	c = &hostCounter{}
	for want := range []string{"1", "2"} {
		got, err := tmpl.RenderString(ctx, map[string]any{"c": c})
		if err != nil || got != []string{"1", "2"}[want] {
			t.Errorf("AllowPointerMethods: got %q, %v", got, err)
		}
	}
	if c.N != 2 {
		t.Errorf("AllowPointerMethods: N = %d, want 2", c.N)
	}
}

func TestMethodPolicySeesThePointerReceiver(t *testing.T) {
	var seen []string
	policy := func(m value.MethodInfo) bool {
		if m.PointerReceiver {
			seen = append(seen, m.Name)
		}
		return m.NumIn == 0
	}
	env, err := gojja2.New(gojja2.WithMethodPolicy(policy), gojja2.WithSharedState(gojja2.AllowPointerMethods))
	if err != nil {
		t.Fatal(err)
	}
	tmpl, err := env.FromString(`{{ c.Inc() }}{{ c.Peek() }}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tmpl.RenderString(context.Background(), map[string]any{"c": &hostCounter{}}); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 || seen[0] != "Inc" {
		t.Errorf("pointer receivers reported to the policy: %v, want [Inc]", seen)
	}
}

func TestUnknownSharedStateIsRefused(t *testing.T) {
	if _, err := gojja2.New(gojja2.WithSharedState(1 << 7)); err == nil {
		t.Error("New accepted a SharedState bit that names nothing")
	}
	if _, err := gojja2.New(gojja2.WithSharedState(gojja2.ShareAll)); err != nil {
		t.Errorf("ShareAll refused: %v", err)
	}
}

// TestPreparedValuesUnderConcurrency is for -race: one prepared vars map and
// one module, used from many goroutines at once.
func TestPreparedValuesUnderConcurrency(t *testing.T) {
	ctx := context.Background()
	env := newShared(t, 0)
	tmpl, err := env.FromString(`{% do xs.append(9) %}{{ xs|length }}`)
	if err != nil {
		t.Fatal(err)
	}
	modTmpl, err := env.FromString(`{% set top = [] %}{% macro add() %}{% do top.append(1) %}{{ top|length }}{% endmacro %}`)
	if err != nil {
		t.Fatal(err)
	}
	mod, err := modTmpl.Module(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	vars := map[string]value.Value{"xs": value.NewList(value.Int(1))}
	var wg sync.WaitGroup
	errc := make(chan error, 64)
	for range 32 {
		wg.Go(func() {
			var out strings.Builder
			if err := tmpl.RenderValues(ctx, &out, vars); err != nil || out.String() != "2" {
				errc <- errors.New("RenderValues: " + out.String())
			}
			if v, err := mod.Call(ctx, "add"); err != nil || value.Str(v) != "1" {
				errc <- errors.New("Module.Call: " + value.Str(v))
			}
		})
	}
	wg.Wait()
	close(errc)
	for err := range errc {
		t.Error(err)
	}
}
