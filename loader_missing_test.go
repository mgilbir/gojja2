// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mgilbir/gojja2"
)

// An environment with no loader reports itself, rather than reporting every
// template as missing.
//
// jinja2 raises TypeError("no loader for this environment specified"), and the
// difference from TemplateNotFound is visible because it is the one failure
// `ignore missing` does not swallow. `{% include "x" ignore missing %}` on a
// loaderless environment rendered as nothing at all here -- quietly, which is
// the worst way for a misconfiguration to present.
//
// Found by fuzzing the whitespace settings, of all things: the probe built its
// environments without a loader, and this was the only thing the whole sweep
// disagreed with CPython about.
func TestNoLoaderIsAMisconfiguration(t *testing.T) {
	for _, src := range []string{
		`A{% include 'nope.txt' ignore missing %}B`,
		`A{% include 'nope.txt' %}B`,
		`{% extends 'nope.txt' %}`,
		`{% import 'nope.txt' as m %}`,
		`{% from 'nope.txt' import x %}`,
	} {
		tmpl, err := gojja2.New().FromString(src)
		if err != nil {
			t.Errorf("%s: compile: %v", src, err)
			continue
		}
		out, err := tmpl.RenderString(context.Background(), nil)
		if err == nil {
			t.Errorf("%s rendered %q on an environment with no loader", src, out)
			continue
		}
		if want := "no loader for this environment specified"; !strings.Contains(err.Error(), want) {
			t.Errorf("%s = %v, want a message containing %q (CPython's)", src, err, want)
		}
	}
}

// With a loader, nothing changes: a template that is genuinely missing is
// missing, and `ignore missing` still ignores it.
func TestMissingWithALoaderIsStillMissing(t *testing.T) {
	env := gojja2.New(gojja2.WithLoader(gojja2.DictLoader(map[string]string{"a": "A"})))
	for _, tc := range []struct{ src, want, wantErr string }{
		{`A{% include 'nope.txt' ignore missing %}B`, "AB", ""},
		{`[{% include 'a' %}]`, "[A]", ""},
		{`A{% include 'nope.txt' %}B`, "", "nope.txt"},
		{`{% extends 'nope.txt' %}`, "", "nope.txt"},
	} {
		tmpl, err := env.FromString(tc.src)
		if err != nil {
			t.Errorf("%s: compile: %v", tc.src, err)
			continue
		}
		got, err := tmpl.RenderString(context.Background(), nil)
		if tc.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("%s = %q, %v; want an error naming %q", tc.src, got, err, tc.wantErr)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s = %q, want %q", tc.src, got, tc.want)
		}
	}
}
