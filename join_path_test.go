// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/mgilbir/gojja2"
)

// joinStyles are the Go spellings of the join_path overrides the oracle ran.
// "rel" resolves a name against the directory of the template asking for it,
// with a leading slash meaning the loader's root; "tag" appends the parent, so
// which parent was passed shows in the name that is loaded.
var joinStyles = map[string]func(name, parent string) string{
	"rel": func(name, parent string) string {
		if strings.HasPrefix(name, "/") {
			return name[1:]
		}
		dir := ""
		if i := strings.LastIndexByte(parent, '/'); i >= 0 {
			dir = parent[:i+1]
		}
		return dir + name
	},
	"tag": func(name, parent string) string { return name + "<" + parent },
}

// WithJoinPath is jinja2's Environment.join_path. Each case is what jinja2
// 3.1.6 rendered with a subclass overriding join_path the same way, and which
// names its loader was asked for, in order, across every render -- recorded
// verbatim from the oracle, one case per capped subprocess. The load log is what
// shows the cache: jinja2 keys it by the joined name, so a name loaded once is
// not loaded again, while one name joined against two parents is two templates.
//
// What the cases pin: the parent is the template whose source holds the tag --
// the child for a block it overrides, the macro's template for an include in a
// macro body, the caller's for an include in a {% call %} body -- and not the
// template the render started from; a template from a string joins nothing; a
// "none of the templates given were found" error names the candidates as
// written, and a single missing template is named as joined.
func TestJoinPathAgainstJinja2(t *testing.T) {
	for i, tc := range []struct {
		join, root, fromString string
		renders                int
		ctx                    map[string]any
		templates              map[string]string
		wantOuts, wantLoads    []string
	}{
		{
			join: `rel`, root: `pages/index.html`, fromString: ``, renders: 1, ctx: nil,
			templates: map[string]string{`pages/index.html`: `{% extends '/layouts/base.html' %}{% block c %}{% include 'part.html' %}{% endblock %}`, `layouts/base.html`: `[{% block c %}{% endblock %}|{% include 'part.html' %}]`, `pages/part.html`: `pages-part`, `layouts/part.html`: `layouts-part`},
			wantOuts:  []string{`[pages-part|layouts-part]`},
			wantLoads: []string{`pages/index.html`, `layouts/base.html`, `pages/part.html`, `layouts/part.html`},
		},
		{
			join: `rel`, root: `pages/m.html`, fromString: ``, renders: 1, ctx: nil,
			templates: map[string]string{`pages/m.html`: `{% import '/lib/m.html' as m %}{{ m.hi() }}|{% from '/lib/m.html' import hi %}{{ hi() }}`, `lib/m.html`: `{% macro hi() %}{% include 'h.html' %}{% endmacro %}`, `lib/h.html`: `lib-h`, `pages/h.html`: `pages-h`},
			wantOuts:  []string{`lib-h|lib-h`},
			wantLoads: []string{`pages/m.html`, `lib/m.html`, `lib/h.html`},
		},
		{
			join: `rel`, root: `pages/call.html`, fromString: ``, renders: 1, ctx: nil,
			templates: map[string]string{`pages/call.html`: `{% from '/lib/w.html' import wrap %}{% call wrap() %}{% include 'h.html' %}{% endcall %}`, `lib/w.html`: `{% macro wrap() %}<{{ caller() }}{% include 'h.html' %}>{% endmacro %}`, `lib/h.html`: `lib-h`, `pages/h.html`: `pages-h`},
			wantOuts:  []string{`<pages-hlib-h>`},
			wantLoads: []string{`pages/call.html`, `lib/w.html`, `pages/h.html`, `lib/h.html`},
		},
		{
			join: `rel`, root: `pages/sel.html`, fromString: ``, renders: 1, ctx: nil,
			templates: map[string]string{`pages/sel.html`: `{% include ['missing.html', 'part.html'] %}`, `pages/part.html`: `pages-part`, `part.html`: `root-part`},
			wantOuts:  []string{`pages-part`},
			wantLoads: []string{`pages/sel.html`, `pages/missing.html`, `pages/part.html`},
		},
		{
			join: `rel`, root: `pages/sel.html`, fromString: ``, renders: 1, ctx: nil,
			templates: map[string]string{`pages/sel.html`: `{% include ['nope1.html', 'nope2.html'] %}`},
			wantOuts:  []string{`TemplatesNotFound: none of the templates given were found: nope1.html, nope2.html`},
			wantLoads: []string{`pages/sel.html`, `pages/nope1.html`, `pages/nope2.html`},
		},
		{
			join: `rel`, root: `pages/one.html`, fromString: ``, renders: 1, ctx: nil,
			templates: map[string]string{`pages/one.html`: `{% include 'nope.html' %}`},
			wantOuts:  []string{`TemplateNotFound: pages/nope.html`},
			wantLoads: []string{`pages/one.html`, `pages/nope.html`},
		},
		{
			join: `rel`, root: `pages/one.html`, fromString: ``, renders: 1, ctx: nil,
			templates: map[string]string{`pages/one.html`: `a{% include 'nope.html' ignore missing %}b`},
			wantOuts:  []string{`ab`},
			wantLoads: []string{`pages/one.html`, `pages/nope.html`},
		},
		{
			join: `rel`, root: ``, fromString: `{% include 'pages/part.html' %}`, renders: 1, ctx: nil,
			templates: map[string]string{`pages/part.html`: `P{% include 'sub/x.html' %}`, `pages/sub/x.html`: `X{% include 'y.html' %}`, `pages/sub/y.html`: `Y`},
			wantOuts:  []string{`PXY`},
			wantLoads: []string{`pages/part.html`, `pages/sub/x.html`, `pages/sub/y.html`},
		},
		{
			join: `rel`, root: `pages/nest.html`, fromString: ``, renders: 1, ctx: nil,
			templates: map[string]string{`pages/nest.html`: `{% include 'sub/x.html' %}`, `pages/sub/x.html`: `X{% include 'y.html' %}`, `pages/sub/y.html`: `Y`},
			wantOuts:  []string{`XY`},
			wantLoads: []string{`pages/nest.html`, `pages/sub/x.html`, `pages/sub/y.html`},
		},
		{
			join: `rel`, root: `r.html`, fromString: ``, renders: 2, ctx: nil,
			templates: map[string]string{`r.html`: `{% include 'a/i.html' %}{% include 'b/i.html' %}{% include 'a/i.html' %}`, `a/i.html`: `{% include 'common.html' %}`, `b/i.html`: `{% include 'common.html' %}`, `a/common.html`: `A`, `b/common.html`: `B`},
			wantOuts:  []string{`ABA`, `ABA`},
			wantLoads: []string{`r.html`, `a/i.html`, `a/common.html`, `b/i.html`, `b/common.html`},
		},
		{
			join: `rel`, root: `pages/var.html`, fromString: ``, renders: 1, ctx: map[string]any{`n`: `part.html`},
			templates: map[string]string{`pages/var.html`: `{% include n %}|{% include [n] %}`, `pages/part.html`: `pages-part`},
			wantOuts:  []string{`pages-part|pages-part`},
			wantLoads: []string{`pages/var.html`, `pages/part.html`},
		},
		{
			join: `rel`, root: `pages/u.html`, fromString: ``, renders: 1, ctx: nil,
			templates: map[string]string{`pages/u.html`: `{% include nope_var %}`},
			wantOuts:  []string{`UndefinedError: 'nope_var' is undefined`},
			wantLoads: []string{`pages/u.html`},
		},
		{
			join: `tag`, root: `t`, fromString: ``, renders: 1, ctx: nil,
			templates: map[string]string{`t`: `{% include 'c' %}`, `c<t`: `joined`},
			wantOuts:  []string{`joined`},
			wantLoads: []string{`t`, `c<t`},
		},
		{
			join: `tag`, root: `t`, fromString: ``, renders: 1, ctx: nil,
			templates: map[string]string{`t`: `{% include ['c', 'd'] %}`, `d<t`: `second`},
			wantOuts:  []string{`second`},
			wantLoads: []string{`t`, `c<t`, `d<t`},
		},
		{
			join: `tag`, root: `t`, fromString: ``, renders: 1, ctx: nil,
			templates: map[string]string{`t`: `{% include ['c', 'd'] %}`},
			wantOuts:  []string{`TemplatesNotFound: none of the templates given were found: c, d`},
			wantLoads: []string{`t`, `c<t`, `d<t`},
		},
		{
			join: `rel`, root: `pages/sb.html`, fromString: ``, renders: 1, ctx: nil,
			templates: map[string]string{`pages/sb.html`: `{% extends '/layouts/sb.html' %}{% block c %}{{ super() }}{% endblock %}`, `layouts/sb.html`: `{% block c %}{% include 'part.html' %}{% endblock %}`, `pages/part.html`: `pages-part`, `layouts/part.html`: `layouts-part`},
			wantOuts:  []string{`layouts-part`},
			wantLoads: []string{`pages/sb.html`, `layouts/sb.html`, `layouts/part.html`},
		},
		{
			join: `rel`, root: `pages/imp.html`, fromString: ``, renders: 1, ctx: nil,
			templates: map[string]string{`pages/imp.html`: `{% import 'sub/m.html' as m %}{{ m.v }}`, `pages/sub/m.html`: `{% set v %}{% include 'leaf.html' %}{% endset %}`, `pages/sub/leaf.html`: `leaf`},
			wantOuts:  []string{`leaf`},
			wantLoads: []string{`pages/imp.html`, `pages/sub/m.html`, `pages/sub/leaf.html`},
		},
		{
			// From a string: no parent, so no join -- but the template it
			// loads has a name, and joins from it.
			join: `tag`, root: ``, fromString: `{% include "c" %}`, renders: 1, ctx: nil,
			templates: map[string]string{`c`: `plain{% include "d" %}`, `c<`: `joined-from-none`, `d<c`: `D`},
			wantOuts:  []string{`plainD`},
			wantLoads: []string{`c`, `d<c`},
		},
	} {
		var mu sync.Mutex
		var loads []string
		dict := gojja2.DictLoader(tc.templates)
		loader := gojja2.LoaderFunc(func(name string) (string, error) {
			mu.Lock()
			loads = append(loads, name)
			mu.Unlock()
			return dict.Load(name)
		})
		env := mustEnv(gojja2.WithLoader(loader), gojja2.WithJoinPath(joinStyles[tc.join]))
		var outs []string
		for range tc.renders {
			var tmpl *gojja2.Template
			var err error
			if tc.fromString != "" {
				tmpl, err = env.FromString(tc.fromString)
			} else {
				tmpl, err = env.GetTemplate(tc.root)
			}
			if err != nil {
				outs = append(outs, outcome("", err))
				continue
			}
			outs = append(outs, outcome(tmpl.RenderString(context.Background(), tc.ctx)))
		}
		if !reflect.DeepEqual(outs, tc.wantOuts) {
			t.Errorf("case %d (%s): renders\n got %q\nwant %q", i, tc.root+tc.fromString, outs, tc.wantOuts)
		}
		if !reflect.DeepEqual(loads, tc.wantLoads) {
			t.Errorf("case %d (%s): loads\n got %q\nwant %q", i, tc.root+tc.fromString, loads, tc.wantLoads)
		}
	}
}

// Without the option, and for names the host asks for directly, nothing is
// joined: jinja2's get_template joins only when it is given a parent, and its
// default join_path returns the name unchanged.
func TestJoinPathOnlyFromTemplates(t *testing.T) {
	var asked []string
	join := func(name, parent string) string {
		asked = append(asked, name+"|"+parent)
		return name
	}
	env := mustEnv(gojja2.WithJoinPath(join), gojja2.WithLoader(gojja2.DictLoader{
		"a": `{% include "b" %}`, "b": "B",
	}))
	if _, err := env.GetTemplate("a"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.SelectTemplate([]string{"nope", "b"}); err != nil {
		t.Fatal(err)
	}
	if len(asked) != 0 {
		t.Fatalf("GetTemplate and SelectTemplate joined %q", asked)
	}
	tmpl, err := env.GetTemplate("a")
	if err != nil {
		t.Fatal(err)
	}
	if out, err := tmpl.RenderString(context.Background(), nil); err != nil || out != "B" {
		t.Fatalf("render: %q, %v", out, err)
	}
	if want := []string{"b|a"}; !reflect.DeepEqual(asked, want) {
		t.Errorf("asked %q, want %q", asked, want)
	}
	// A name that is not a string is not handed to the hook.
	asked = nil
	tmpl, err = env.FromNamedString("n", `{% include [none, 1, "b"] %}`)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := tmpl.RenderString(context.Background(), nil); err != nil || out != "B" {
		t.Fatalf("render: %q, %v", out, err)
	}
	if want := []string{"b|n"}; !reflect.DeepEqual(asked, want) {
		t.Errorf("asked %q, want %q", asked, want)
	}
}
