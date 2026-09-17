// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"context"
	"errors"
	"testing"
	"testing/fstest"

	"github.com/mgilbir/gojja2"
)

// A template name reaches a loader from `{% include %}` and friends, where it
// may be any expression and so may be attacker-influenced. What a name that
// climbs out of the root should do is *fail*, and it did not: the path was
// cleaned, so `../secret.html` resolved to `secret.html` and rendered it. The
// confinement held either way, which is why it went unnoticed -- but a template
// that asked for a file it should not have got a different file and no error.
//
// The check also has to run before any cleaning, or it cannot fire at all:
// path.Clean on a rooted path resolves every ".." away, so the loop that
// looked like the security control was unreachable code.

func loaderFS() fstest.MapFS {
	return fstest.MapFS{
		"secret.html":     {Data: []byte("SECRET")},
		"tpl/page.html":   {Data: []byte("PAGE")},
		"tpl/sub/in.html": {Data: []byte("IN")},
		"tpl/secret.html": {Data: []byte("TPL-SECRET")},
	}
}

func TestFSLoaderRefusesNamesThatClimbOut(t *testing.T) {
	for _, name := range []string{
		"../secret.html",
		"x/../../secret.html",
		`..\secret.html`,
		"a/../page.html", // refused even though it would land inside
		"..",
		"sub/..",
		"./../secret.html",
	} {
		t.Run(name, func(t *testing.T) {
			for _, root := range []string{"tpl", ""} {
				env := gojja2.New(gojja2.WithLoader(
					gojja2.FSLoader{FS: loaderFS(), Root: root}))
				_, err := env.GetTemplate(name)
				if err == nil {
					t.Fatalf("root %q: %q was loaded; a name that climbs out must not be", root, name)
				}
				if !errors.Is(err, gojja2.ErrNotFound) {
					t.Fatalf("root %q: %q gave %v, want ErrNotFound", root, name, err)
				}
			}
		})
	}
}

// TestFSLoaderResolvesOrdinaryNames guards the other direction, with the
// segments jinja2 drops rather than refuses.
func TestFSLoaderResolvesOrdinaryNames(t *testing.T) {
	env := gojja2.New(gojja2.WithLoader(gojja2.FSLoader{FS: loaderFS(), Root: "tpl"}))
	for _, tc := range []struct{ name, want string }{
		{"page.html", "PAGE"},
		{"./page.html", "PAGE"},
		{"sub/in.html", "IN"},
		{"sub//in.html", "IN"},
		{"sub/./in.html", "IN"},
		{"/page.html", "PAGE"},
	} {
		tmpl, err := env.GetTemplate(tc.name)
		if err != nil {
			t.Errorf("%q: %v", tc.name, err)
			continue
		}
		got, err := tmpl.RenderString(context.Background(), nil)
		if err != nil || got != tc.want {
			t.Errorf("%q = %q (%v), want %q", tc.name, got, err, tc.want)
		}
	}
}

// TestIncludeIgnoreMissingCoversARefusedName: a refused name is a missing
// template, so the tag that tolerates one still does.
func TestIncludeIgnoreMissingCoversARefusedName(t *testing.T) {
	env := gojja2.New(gojja2.WithLoader(gojja2.FSLoader{FS: loaderFS(), Root: "tpl"}))
	out, err := mustRender(t, env, `[{% include "../secret.html" ignore missing %}]`)
	if err != nil {
		t.Fatalf("ignore missing should tolerate a refused name: %v", err)
	}
	if out != "[]" {
		t.Errorf("got %q, want %q", out, "[]")
	}
}
