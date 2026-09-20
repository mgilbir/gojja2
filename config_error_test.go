// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mgilbir/gojja2"
	"github.com/mgilbir/gojja2/errs"
)

// New reports a configuration it cannot honour instead of accepting it. Each
// of these used to be taken silently, and showed up later -- or not at all.

func TestNewRefusesAConfigurationItCannotHonour(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts []gojja2.Option
		want string
	}{
		{
			"unknown extension",
			[]gojja2.Option{gojja2.WithExtensions("loopcontrol")},
			`unknown extension "loopcontrol"`,
		},
		{
			"unknown extension beside a known one",
			[]gojja2.Option{gojja2.WithExtensions("do", "nosuch")},
			`unknown extension "nosuch"`,
		},
		{
			"newline sequence that is not one",
			[]gojja2.Option{gojja2.WithNewlineSequence("x")},
			"newline sequence",
		},
		{
			"empty newline sequence",
			[]gojja2.Option{gojja2.WithNewlineSequence("")},
			"newline sequence",
		},
		{
			"block and variable openings collide",
			[]gojja2.Option{gojja2.WithBlockDelimiters("{{", "}}")},
			"block and variable start strings are both",
		},
		{
			"variable and comment openings collide",
			[]gojja2.Option{gojja2.WithVariableDelimiters("{#", "#}")},
			"variable and comment start strings are both",
		},
		{
			// jinja2's own check is a chained comparison and never
			// compares these two, so it accepts this; a template
			// cannot be read two ways, so it is refused here.
			"block and comment openings collide",
			[]gojja2.Option{gojja2.WithCommentDelimiters("{%", "%}")},
			"block and comment start strings are both",
		},
		{
			"all three collide",
			[]gojja2.Option{
				gojja2.WithBlockDelimiters("@", "@"),
				gojja2.WithVariableDelimiters("@", "@"),
				gojja2.WithCommentDelimiters("@", "@"),
			},
			"start strings are both",
		},
		{
			"negative truncate leeway",
			[]gojja2.Option{gojja2.WithPolicies(gojja2.Policies{TruncateLeeway: -1})},
			"leeway must not be negative",
		},
	} {
		env, err := gojja2.New(tc.opts...)
		if err == nil {
			t.Errorf("%s: New succeeded, want an error", tc.name)
			continue
		}
		if env != nil {
			t.Errorf("%s: New returned an environment alongside its error", tc.name)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v\n  does not mention %q", tc.name, err, tc.want)
		}
		if !errors.Is(err, errs.TemplateError) {
			t.Errorf("%s: %v is not a TemplateError", tc.name, err)
		}
	}
}

// And the configurations that are fine stay fine -- including the ones that
// look like collisions and are not.
func TestNewAcceptsWhatItShould(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts []gojja2.Option
	}{
		{"no options", nil},
		{"known extensions", []gojja2.Option{gojja2.WithExtensions("do", "loopcontrols")}},
		{"qualified extension names", []gojja2.Option{
			gojja2.WithExtensions("jinja2.ext.do", "jinja2.ext.loopcontrols")}},
		{"the three newline sequences", []gojja2.Option{gojja2.WithNewlineSequence("\r\n")}},
		{"carriage return", []gojja2.Option{gojja2.WithNewlineSequence("\r")}},
		{"distinct custom delimiters", []gojja2.Option{
			gojja2.WithBlockDelimiters("<%", "%>"),
			gojja2.WithVariableDelimiters("<<", ">>"),
			gojja2.WithCommentDelimiters("<#", "#>")}},
		// A line-statement prefix may equal a delimiter: jinja2 allows
		// it and renders it, and so does this.
		{"line statement prefix equals the block opening", []gojja2.Option{
			gojja2.WithLineStatementPrefix("%"),
			gojja2.WithBlockDelimiters("%", "%")}},
		{"line comment prefix equals the block opening", []gojja2.Option{
			gojja2.WithLineCommentPrefix("%"),
			gojja2.WithBlockDelimiters("%", "%")}},
		{"zero leeway", []gojja2.Option{gojja2.WithPolicies(gojja2.Policies{})}},
		// An empty delimiter means "leave this one alone", which is how
		// one can be overridden without restating the rest.
		{"empty delimiters fall back to the defaults", []gojja2.Option{
			gojja2.WithBlockDelimiters("", "")}},
	} {
		env, err := gojja2.New(tc.opts...)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if env == nil {
			t.Errorf("%s: no environment and no error", tc.name)
			continue
		}
		// Usable, not merely constructed.
		tmpl, err := env.FromString("ok")
		if err != nil {
			t.Errorf("%s: FromString: %v", tc.name, err)
			continue
		}
		if out, err := tmpl.RenderString(context.Background(), nil); err != nil || out != "ok" {
			t.Errorf("%s: render = %q, %v", tc.name, out, err)
		}
	}
}

// The first refusal wins and the rest are not applied.
func TestNewStopsAtTheFirstBadOption(t *testing.T) {
	_, err := gojja2.New(
		gojja2.WithExtensions("nosuch"),
		gojja2.WithNewlineSequence("also bad"),
	)
	if err == nil {
		t.Fatal("New succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "unknown extension") {
		t.Errorf("got %v, want the first option's error", err)
	}
}
