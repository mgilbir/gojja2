// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"context"
	"testing"

	"github.com/mgilbir/gojja2"
	"github.com/mgilbir/gojja2/value"
)

// htmlAndStr is a value whose own escaped form differs from its str, which is
// the only way to tell |forceescape from |escape apart on this point.
//
// jinja2's do_forceescape is `escape(str(value.__html__()))` -- it asks for the
// escaped form and then escapes *that* -- while do_escape returns the escaped
// form unchanged. Every value a template can build has str(x.__html__()) ==
// str(x), so a corpus case cannot separate them: a module's body is its str
// either way. A Go value supplied through the public API can, and does.
type htmlAndStr struct{}

func (htmlAndStr) GetAttr(string) (value.Value, bool) { return value.Undefined, false }
func (htmlAndStr) TypeName() string                   { return "htmlAndStr" }
func (htmlAndStr) Str() string                        { return "plain" }
func (htmlAndStr) Repr() string                       { return "htmlAndStr()" }
func (htmlAndStr) HTML() string                       { return "<i>html</i>" }

func TestForceEscapeAsksForHTMLFirst(t *testing.T) {
	env, err := gojja2.New()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ src, want string }{
		// forceescape escapes the __html__ form, so its tags come back
		// as entities.
		{"{{ v|forceescape }}", "&lt;i&gt;html&lt;/i&gt;"},
		// escape hands the __html__ form over untouched.
		{"{{ v|e }}", "<i>html</i>"},
		// string never asks for it at all.
		{"{{ v|string }}", "plain"},
	} {
		tpl, err := env.FromString(c.src)
		if err != nil {
			t.Fatal(err)
		}
		got, err := tpl.RenderString(context.Background(),
			map[string]any{"v": value.FromObject(htmlAndStr{})})
		if err != nil {
			t.Fatalf("%s: %v", c.src, err)
		}
		if got != c.want {
			t.Errorf("%s = %q, want %q", c.src, got, c.want)
		}
	}
}
