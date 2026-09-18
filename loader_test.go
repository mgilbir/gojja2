// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"testing/fstest"

	"github.com/mgilbir/gojja2"
	"github.com/mgilbir/gojja2/errs"
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

// A Loader may report a miss in any of the three shapes the interface
// documents, and every fallback path has to recognise all of them.
//
// "A miss must be reported as gojja2.ErrNotFound (or an error wrapping it)" is what
// Loader's own doc comment promises. Only one shape actually worked:
// errs.New(errs.TemplateNotFound, ...), which is not what the sentence says
// and reaches past the exported API to say it. Returning the exported
// gojja2.ErrNotFound -- the plainest reading -- and wrapping it with %w both
// failed, because the classification was a type assertion that a bare Kind and
// a wrapped error each fall outside.
//
// The failure was silent and it failed the wrong way: ChoiceLoader stopped at
// the first loader instead of trying the next, so a template that *existed* in
// a later loader was reported as an error, and `ignore missing` propagated a
// miss it was written to swallow.
func TestLoaderMissIsRecognisedInEveryDocumentedShape(t *testing.T) {
	for _, tc := range []struct {
		name string
		miss error
	}{
		{"the exported sentinel", gojja2.ErrNotFound},
		{"the sentinel wrapped", fmt.Errorf("reading: %w", gojja2.ErrNotFound)},
		{"an errs.New not-found", errs.New(errs.TemplateNotFound, "gone.html")},
		{"an errs.New not-found wrapped",
			fmt.Errorf("reading: %w", errs.New(errs.TemplateNotFound, "gone.html"))},
		// A subclass is a miss too: select_template raises this one.
		{"a TemplatesNotFound", errs.New(errs.TemplatesNotFound, "none of them")},
		// And as a bare sentinel, where the match is by hierarchy rather
		// than by equality -- which is what Kind.Is answers.
		{"a bare TemplatesNotFound", errs.TemplatesNotFound},
		{"a bare TemplatesNotFound wrapped",
			fmt.Errorf("reading: %w", errs.TemplatesNotFound)},
	} {
		missing := missLoader{err: tc.miss}

		// ChoiceLoader must fall through to the loader that has it.
		env := gojja2.New(gojja2.WithLoader(gojja2.ChoiceLoader{missing, gojja2.DictLoader{"t.html": "SECOND"}}))
		tmpl, err := env.GetTemplate("t.html")
		if err != nil {
			t.Errorf("%s: ChoiceLoader: %v, want the second loader's template", tc.name, err)
		} else if got, err := tmpl.RenderString(context.Background(), nil); err != nil || got != "SECOND" {
			t.Errorf("%s: ChoiceLoader rendered %q, %v; want \"SECOND\"", tc.name, got, err)
		}

		// `ignore missing` must swallow it.
		env2 := gojja2.New(gojja2.WithLoader(missing))
		t2, err := env2.FromString(`[{% include "gone.html" ignore missing %}]`)
		if err != nil {
			t.Errorf("%s: compile: %v", tc.name, err)
			continue
		}
		if got, err := t2.RenderString(context.Background(), nil); err != nil || got != "[]" {
			t.Errorf("%s: ignore missing rendered %q, %v; want \"[]\"", tc.name, got, err)
		}
	}
}

// An error that is not a miss must still stop the search, or a misconfigured
// or failing loader reads as "not here" and the next one quietly answers.
func TestLoaderRealErrorStopsTheSearch(t *testing.T) {
	boom := errors.New("disk on fire")
	env := gojja2.New(gojja2.WithLoader(gojja2.ChoiceLoader{
		missLoader{err: boom}, gojja2.DictLoader{"t.html": "SECOND"},
	}))
	if _, err := env.GetTemplate("t.html"); !errors.Is(err, boom) {
		t.Errorf("got %v, want the loader's own error", err)
	}
}

// missLoader reports every lookup as the error it was built with.
type missLoader struct{ err error }

func (m missLoader) Load(string) (string, error) { return "", m.err }
