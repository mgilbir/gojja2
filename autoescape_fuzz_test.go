// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mgilbir/gojja2"
)

// xssSentinel is a value no template text would contain by accident.
//
// The check is whether it reaches the output *as written*. Anything else --
// counting angle brackets, comparing against an escaped form -- confuses the
// template's own text with the data it was given, and a template is entitled to
// contain markup of its own.
const xssSentinel = `<zqx7 f="g">&'"`

// autoescapeExempt reports a template allowed to put the sentinel through
// unescaped.
//
// Escaping is not a property of the engine alone: a template can ask for raw
// output and jinja2 gives it. |safe says so outright, Markup is safe by
// construction, and the tags that capture and re-emit text can carry it. Naming
// them is coarse -- a template merely containing the word "safe" is skipped --
// and coarse the right way round: a name that appears here only costs coverage,
// while one that does not appear and should have would fail a build over
// behaviour jinja2 documents.
func autoescapeExempt(src string) bool {
	for _, name := range []string{"safe", "Markup", "autoescape"} {
		if strings.Contains(src, name) {
			return true
		}
	}
	return false
}

// Under autoescape, a plain string's markup must not reach the output as
// written.
//
// This is the property autoescaping exists for, and it is the one a template
// engine must not get wrong even once: every path that can carry a value to the
// output has to escape it, and there are many -- a print tag, a filter that
// passes its input through, a macro's return, a block, an include, a `{% set %}`
// captured and re-emitted. Any one of them forgetting is an injection.
//
// The corpus seeds every shape the grammar has, so this asks the question of
// all of them at once, and keeps asking it of whatever the fuzzer builds next.
func FuzzAutoescape(f *testing.F) {
	fuzzSeeds(f)
	f.Fuzz(func(t *testing.T, src string) {
		if len(src) > 64<<10 || autoescapeExempt(src) {
			t.Skip("oversized or allowed to emit raw markup")
		}
		env := gojja2.New(
			gojja2.WithAutoescape(true),
			gojja2.WithMaxOutputBytes(fuzzMaxOutput),
			gojja2.WithMaxIterations(fuzzMaxIterations),
			gojja2.WithLoader(gojja2.DictLoader(map[string]string{
				"inner":  `[{% block b %}inner{% endblock %}]`,
				"parent": `{% block b %}parent{% endblock %}`,
			})),
		)
		tmpl, err := env.FromString(src)
		if err != nil {
			return
		}
		// Every name the seeds reach for carries the sentinel, so a
		// template that prints anything at all prints it.
		vars := fuzzVars()
		for k := range vars {
			vars[k] = xssSentinel
		}
		vars["xss"] = xssSentinel

		ctx, cancel := context.WithTimeout(context.Background(), fuzzDeadline)
		defer cancel()
		out, err := tmpl.RenderString(ctx, vars)
		if err != nil {
			return
		}
		if strings.Contains(out, xssSentinel) {
			t.Fatalf("autoescaping let a plain string through unescaped:\n"+
				"  template %q\n  output   %q", src, out)
		}
	})
}

// The sentinel really is escaped when it should be, so the target above is
// asking a question that has a wrong answer.
//
// A fuzz target that can only pass is worth nothing, and this one skips any
// template naming |safe -- so without this, deleting the check and deleting
// escaping itself would look identical.
func TestAutoescapeSentinelIsEscaped(t *testing.T) {
	env := gojja2.New(gojja2.WithAutoescape(true))
	for _, src := range []string{
		`{{ xss }}`,
		`{{ xss|upper }}`,
		`{% for c in [xss] %}{{ c }}{% endfor %}`,
		`{% macro m(v) %}{{ v }}{% endmacro %}{{ m(xss) }}`,
		`{% set v = xss %}{{ v }}`,
		`{{ [xss]|join(",") }}`,
		`{{ xss|replace("q", "Q") }}`,
		`{% block b %}{{ xss }}{% endblock %}`,
	} {
		tmpl, err := env.FromString(src)
		if err != nil {
			t.Errorf("%s: compile: %v", src, err)
			continue
		}
		out, err := tmpl.RenderString(context.Background(),
			map[string]any{"xss": xssSentinel})
		if err != nil {
			t.Errorf("%s: %v", src, err)
			continue
		}
		if strings.Contains(out, xssSentinel) {
			t.Errorf("%s let the sentinel through unescaped: %q", src, out)
		}
		if !strings.Contains(out, "&lt;") {
			t.Errorf("%s did not produce the escaped form either: %q -- the "+
				"sentinel may not be reaching the output at all, which "+
				"would make the fuzz target vacuous", src, out)
		}
	}
}
