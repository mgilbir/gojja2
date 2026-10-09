// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/mgilbir/gojja2"
	"github.com/mgilbir/gojja2/errs"
	"github.com/mgilbir/gojja2/value"
)

// TestRenderBlock holds RenderBlock to CPython jinja2 3.1.6's
//
//	"".join(t.blocks[block](t.new_context(vars)))
//
// on the same templates, loaded by name from a DictLoader. Every expectation is
// the oracle's answer, output or exception class and message, verbatim: what
// a block sees when nothing outside it has run, super() with no parent
// registered, a block only the parent defines, `self`, loops, required and
// scoped blocks, and autoescaping.
func TestRenderBlock(t *testing.T) {
	for _, tc := range []struct {
		name       string
		templates  map[string]string
		tmpl       string
		block      string
		vars       string
		autoescape bool
		strict     bool
		want       string
		errKind    errs.Kind
		errMsg     string
	}{
		{name: "autoescape_false_tag", templates: map[string]string{"t": "{% autoescape false %}{% block c %}{{ s }}{% endblock %}{% endautoescape %}"}, tmpl: "t", block: "c", vars: "{\"s\": \"<b>\"}", autoescape: true, want: "&lt;b&gt;"},
		{name: "autoescape_false_tag_full", templates: map[string]string{"t": "{% autoescape false %}{% block c %}{{ s }}{% endblock %}{% endautoescape %}"}, tmpl: "t", block: "c", vars: "{\"s\": \"<b>\"}", autoescape: true, want: "&lt;b&gt;"},
		{name: "autoescape_on", templates: map[string]string{"t": "{% block c %}{{ s }}{% endblock %}"}, tmpl: "t", block: "c", vars: "{\"s\": \"<b>\"}", autoescape: true, want: "&lt;b&gt;"},
		{name: "block_set_leak", templates: map[string]string{"t": "{% block a %}{% set z = 1 %}{{ z }}{% endblock %}"}, tmpl: "t", block: "a", vars: "{}", want: "1"},
		{name: "child_toplevel_set", templates: map[string]string{"base": "<html>{% block content %}Hi {{ name }}{% endblock %}|{% block header %}H{% endblock %}</html>", "c": "{% extends 'base' %}{% set name = 'S' %}{% block content %}{{ name }}{% endblock %}"}, tmpl: "c", block: "content", vars: "{}", want: ""},
		{name: "dynamic_extends", templates: map[string]string{"t": "{% extends layout %}{% block content %}X{% endblock %}"}, tmpl: "t", block: "content", vars: "{}", want: "X"},
		{name: "inblock_import", templates: map[string]string{"m": "{% macro x() %}X{% endmacro %}", "t": "{% block content %}{% from 'm' import x %}<{{ x() }}>{% endblock %}"}, tmpl: "t", block: "content", vars: "{}", want: "<X>"},
		{name: "loop_scoped", templates: map[string]string{"t": "{% for i in [1,2] %}{% block row scoped %}({{ i }}){% endblock %}{% endfor %}"}, tmpl: "t", block: "row", vars: "{}", want: "()"},
		{name: "loop_scoped_loopidx", templates: map[string]string{"t": "{% for i in [1,2] %}{% block row scoped %}({{ loop.index }}){% endblock %}{% endfor %}"}, tmpl: "t", block: "row", vars: "{}", errKind: errs.UndefinedError, errMsg: "'loop' is undefined"},
		{name: "loop_scoped_var", templates: map[string]string{"t": "{% for i in [1,2] %}{% block row scoped %}({{ i }}){% endblock %}{% endfor %}"}, tmpl: "t", block: "row", vars: "{\"i\": 9}", want: "(9)"},
		{name: "loop_unscoped", templates: map[string]string{"t": "{% for i in [1,2] %}{% block row %}({{ i }}{{ loop.index }}){% endblock %}{% endfor %}"}, tmpl: "t", block: "row", vars: "{}", errKind: errs.UndefinedError, errMsg: "'loop' is undefined"},
		{name: "nested_child_overrides_inner_outer", templates: map[string]string{"c": "{% extends 't' %}{% block inner %}X{{ super() }}{% endblock %}", "t": "{% block outer %}A{% block inner %}B{% endblock %}C{% endblock %}"}, tmpl: "c", block: "outer", vars: "{}", errKind: errs.KeyError, errMsg: "'outer'"},
		{name: "nested_inner", templates: map[string]string{"t": "{% block outer %}A{% block inner %}B{% endblock %}C{% endblock %}"}, tmpl: "t", block: "inner", vars: "{}", want: "B"},
		{name: "nested_outer", templates: map[string]string{"t": "{% block outer %}A{% block inner %}B{% endblock %}C{% endblock %}"}, tmpl: "t", block: "outer", vars: "{}", want: "ABC"},
		{name: "parent_only", templates: map[string]string{"base": "<html>{% block content %}Hi {{ name }}{% endblock %}|{% block header %}H{% endblock %}</html>", "child": "{% extends 'base' %}{% block content %}[{{ super() }}]{% endblock %}"}, tmpl: "child", block: "header", vars: "{}", errKind: errs.KeyError, errMsg: "'header'"},
		{name: "plain", templates: map[string]string{"base": "<html>{% block content %}Hi {{ name }}{% endblock %}|{% block header %}H{% endblock %}</html>"}, tmpl: "base", block: "content", vars: "{\"name\": \"Bob\"}", want: "Hi Bob"},
		{name: "required_base", templates: map[string]string{"t": "{% block r required %}{% endblock %}"}, tmpl: "t", block: "r", vars: "{}", want: ""},
		{name: "required_child", templates: map[string]string{"c": "{% extends 't' %}{% block r %}R{% endblock %}", "t": "{% block r required %}{% endblock %}"}, tmpl: "c", block: "r", vars: "{}", want: "R"},
		{name: "self_other", templates: map[string]string{"t": "{% block a %}A{{ v }}{% endblock %}{% block b %}[{{ self.a() }}]{% endblock %}"}, tmpl: "t", block: "b", vars: "{\"v\": 1}", want: "[A1]"},
		{name: "self_parent_only", templates: map[string]string{"base": "<html>{% block content %}Hi {{ name }}{% endblock %}|{% block header %}H{% endblock %}</html>", "c": "{% extends 'base' %}{% block content %}[{{ self.header() }}]{% endblock %}"}, tmpl: "c", block: "content", vars: "{}", errKind: errs.UndefinedError, errMsg: "'jinja2.runtime.TemplateReference object' has no attribute 'header'"},
		{name: "self_parent_only_render", templates: map[string]string{"base": "<html>{% block content %}Hi {{ name }}{% endblock %}|{% block header %}H{% endblock %}</html>", "c": "{% extends 'base' %}{% block content %}[{{ self.header() }}]{% endblock %}"}, tmpl: "c", block: "header", vars: "{}", errKind: errs.KeyError, errMsg: "'header'"},
		{name: "self_super", templates: map[string]string{"base": "<html>{% block content %}Hi {{ name }}{% endblock %}|{% block header %}H{% endblock %}</html>", "c": "{% extends 'base' %}{% block content %}[{{ self.content.super() }}]{% endblock %}"}, tmpl: "c", block: "content", vars: "{\"name\": \"Bob\"}", errKind: errs.UndefinedError, errMsg: "there is no parent block called 'content'."},
		{name: "super_child", templates: map[string]string{"base": "<html>{% block content %}Hi {{ name }}{% endblock %}|{% block header %}H{% endblock %}</html>", "child": "{% extends 'base' %}{% block content %}[{{ super() }}]{% endblock %}"}, tmpl: "child", block: "content", vars: "{\"name\": \"Bob\"}", errKind: errs.UndefinedError, errMsg: "there is no parent block called 'content'."},
		{name: "super_child_strict", templates: map[string]string{"base": "<html>{% block content %}Hi {{ name }}{% endblock %}|{% block header %}H{% endblock %}</html>", "child": "{% extends 'base' %}{% block content %}[{{ super() }}]{% endblock %}"}, tmpl: "child", block: "content", vars: "{\"name\": \"Bob\"}", strict: true, errKind: errs.UndefinedError, errMsg: "there is no parent block called 'content'."},
		{name: "super_full_ref", templates: map[string]string{"base": "<html>{% block content %}Hi {{ name }}{% endblock %}|{% block header %}H{% endblock %}</html>", "child": "{% extends 'base' %}{% block content %}[{{ super() }}]{% endblock %}"}, tmpl: "child", block: "content", vars: "{\"name\": \"Bob\"}", errKind: errs.UndefinedError, errMsg: "there is no parent block called 'content'."},
		{name: "super_in_parent_tmpl", templates: map[string]string{"base": "{% block content %}<{{ super() }}>{% endblock %}"}, tmpl: "base", block: "content", vars: "{}", errKind: errs.UndefinedError, errMsg: "there is no parent block called 'content'."},
		{name: "toplevel_import", templates: map[string]string{"m": "{% macro x() %}X{% endmacro %}", "t": "{% from 'm' import x %}{% block content %}<{{ x() }}>{% endblock %}"}, tmpl: "t", block: "content", vars: "{}", errKind: errs.UndefinedError, errMsg: "'x' is undefined"},
		{name: "toplevel_macro", templates: map[string]string{"t": "{% macro m() %}M{% endmacro %}{% block content %}<{{ m() }}>{% endblock %}"}, tmpl: "t", block: "content", vars: "{}", errKind: errs.UndefinedError, errMsg: "'m' is undefined"},
		{name: "toplevel_set", templates: map[string]string{"t": "{% set title = 'T' %}{% block content %}<{{ title }}>{% endblock %}"}, tmpl: "t", block: "content", vars: "{}", want: "<>"},
		{name: "toplevel_set_strict", templates: map[string]string{"t": "{% set title = 'T' %}{% block content %}<{{ title }}>{% endblock %}"}, tmpl: "t", block: "content", vars: "{}", strict: true, errKind: errs.UndefinedError, errMsg: "'title' is undefined"},
		{name: "toplevel_set_var_shadow", templates: map[string]string{"t": "{% set title = 'T' %}{% block content %}<{{ title }}>{% endblock %}"}, tmpl: "t", block: "content", vars: "{\"title\": \"V\"}", want: "<V>"},
		{name: "unknown", templates: map[string]string{"base": "<html>{% block content %}Hi {{ name }}{% endblock %}|{% block header %}H{% endblock %}</html>"}, tmpl: "base", block: "nope", vars: "{}", errKind: errs.KeyError, errMsg: "'nope'"},
	} {
		opts := []gojja2.Option{gojja2.WithLoader(gojja2.DictLoader(tc.templates)), gojja2.WithAutoescape(tc.autoescape)}
		if tc.strict {
			opts = append(opts, gojja2.WithUndefined(value.UndefinedStrict))
		}
		tmpl, err := mustEnv(opts...).GetTemplate(tc.tmpl)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		dec := json.NewDecoder(strings.NewReader(tc.vars))
		dec.UseNumber()
		var raw map[string]any
		if err := dec.Decode(&raw); err != nil {
			t.Fatal(err)
		}
		got, err := tmpl.RenderBlockString(context.Background(), tc.block, jsonInts(raw).(map[string]any))
		if tc.errMsg == "" {
			if err != nil || got != tc.want {
				t.Errorf("%s: %q, %v; want %q", tc.name, got, err, tc.want)
			}
			continue
		}
		var e *errs.Error
		if !errors.As(err, &e) || e.Kind != tc.errKind || e.Msg != tc.errMsg {
			t.Errorf("%s: %q, %v; want %s %q", tc.name, got, err, tc.errKind, tc.errMsg)
		}
	}
}

// TestRenderBlockIsBounded checks a block renders under the environment's
// limits and the caller's context, and that a block calling itself meets the
// recursion bound rather than the end of the goroutine's stack.
func TestRenderBlockIsBounded(t *testing.T) {
	env := mustEnv(gojja2.WithMaxIterations(1000), gojja2.WithLoader(gojja2.DictLoader{
		"loop": `{% block b %}{% for i in range(n) %}.{% endfor %}{% endblock %}`,
		"self": `{% block b %}{{ self.b() }}{% endblock %}`,
	}))
	loop, err := env.GetTemplate("loop")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := loop.RenderBlockString(context.Background(), "b", map[string]any{"n": 1_000_000}); !errors.Is(err, gojja2.ErrTooManyIterations) {
		t.Errorf("unbounded loop: %v, want ErrTooManyIterations", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	unbounded, err := mustEnv(gojja2.WithMaxIterations(1<<40), gojja2.WithLoader(gojja2.DictLoader{
		"loop": `{% block b %}{% for i in range(n) %}{% endfor %}{% endblock %}`,
	})).GetTemplate("loop")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := unbounded.RenderBlockString(ctx, "b", map[string]any{"n": 100_000}); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: %v, want context.Canceled", err)
	}
	self, err := env.GetTemplate("self")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := self.RenderBlockString(context.Background(), "b", nil); errs.KindOf(err) != errs.RecursionError {
		t.Errorf("self-recursive block: %v, want a RecursionError", err)
	}
}

// TestRenderBlockWritesNothingOnFailure checks the block is complete before a
// byte reaches the writer.
func TestRenderBlockWritesNothingOnFailure(t *testing.T) {
	tmpl, err := mustEnv().FromString(`{% block b %}before{{ 1 // 0 }}{% endblock %}`)
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := tmpl.RenderBlock(context.Background(), &out, "b", nil); errs.KindOf(err) != errs.ZeroDivisionError || out.Len() != 0 {
		t.Errorf("wrote %q, err %v; want nothing and a ZeroDivisionError", out.String(), err)
	}
	// RenderBlockValues runs the same block, with values as they are.
	out.Reset()
	ok, err := mustEnv().FromString(`{% block b %}{{ xs|sum }}{% endblock %}`)
	if err != nil {
		t.Fatal(err)
	}
	err = ok.RenderBlockValues(context.Background(), &out, "b", map[string]value.Value{
		"xs": value.NewList(value.Int(1), value.Int(2)),
	})
	if err != nil || out.String() != "3" {
		t.Errorf("RenderBlockValues = %q, %v; want 3", out.String(), err)
	}
	if err := ok.RenderBlockValues(context.Background(), &out, "nope", nil); errs.KindOf(err) != errs.KeyError {
		t.Errorf("RenderBlockValues of an unknown block: %v, want a KeyError", err)
	}
}

// TestRenderBlockConcurrent renders one block of one template from many
// goroutines; under -race it is the check that a block render shares nothing.
func TestRenderBlockConcurrent(t *testing.T) {
	tmpl, err := mustEnv(gojja2.WithLoader(gojja2.DictLoader{
		"base":  `<{% block c %}base{% endblock %}>`,
		"child": `{% extends "base" %}{% block c %}{{ who }}:{{ self.d() }}{% endblock %}{% block d %}{{ who|upper }}{% endblock %}`,
	})).GetTemplate("child")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for _, who := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		wg.Go(func() {
			got, err := tmpl.RenderBlockString(context.Background(), "c", map[string]any{"who": who})
			if want := who + ":" + strings.ToUpper(who); err != nil || got != want {
				t.Errorf("%s: %q, %v; want %q", who, got, err, want)
			}
		})
	}
	wg.Wait()
}

// TestRenderBlockFailsAfterADroppedRefusal is TestRenderFailsAfterADroppedRefusal
// for a block: a global that drops a refused charge leaves the budget spent, and
// the block must not be reported as rendered.
func TestRenderBlockFailsAfterADroppedRefusal(t *testing.T) {
	env := mustEnv(gojja2.WithMaxIterations(100))
	env.AddGlobal("sloppy", gojja2.Func("sloppy", func(s *gojja2.State, _ *value.CallArgs) (value.Value, error) {
		_ = s.Step(1000) // refused, and dropped
		return value.String("output the budget refused"), nil
	}))
	// Bound, not printed, so no later write re-reports the refusal.
	tmpl, err := env.FromString(`{% block b %}{% set _ = sloppy() %}{% endblock %}`)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := tmpl.RenderBlockString(context.Background(), "b", nil); !errors.Is(err, gojja2.ErrTooManyIterations) {
		t.Errorf("rendered %q, %v; want ErrTooManyIterations", out, err)
	}
}
