// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"context"
	"errors"
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/mgilbir/gojja2"
	"github.com/mgilbir/gojja2/errs"
	"github.com/mgilbir/gojja2/value"
)

// The exported edges below are not reachable from a template, so no corpus case
// grades them and the differential never will: they are the answers a host
// program gets from the API when it is used outside a render. Coverage over the
// whole suite put each at zero.

// TestStateOutsideARender: a State reaches a filter or global during constant
// folding, where no render is in progress, and an extension may hold one no
// budget was ever attached to. Every method an extension author is told to call
// has to answer sensibly there rather than dereference what is not present.
func TestStateOutsideARender(t *testing.T) {
	for name, s := range map[string]*gojja2.State{
		"nil":   nil,
		"empty": {},
	} {
		t.Run(name, func(t *testing.T) {
			if err := s.Step(1 << 40); err != nil {
				t.Errorf("Step with no budget = %v, want nil", err)
			}
			if err := s.Poll(); err != nil {
				t.Errorf("Poll with no budget = %v, want nil", err)
			}
			if got := s.NewlineSequence(); got != "\n" {
				t.Errorf("NewlineSequence = %q, want \"\\n\"", got)
			}
			if got := s.PythonVersion(); got != value.DefaultPythonVersion {
				t.Errorf("PythonVersion = %v, want the default", got)
			}
		})
	}
	// A State with no budget has no context to consult, so it answers the
	// background one rather than a nil that would panic in the caller.
	if ctx := (&gojja2.State{}).Context(); ctx == nil || ctx.Err() != nil {
		t.Errorf("Context of an empty State = %v, want context.Background()", ctx)
	}
}

// TestResolveReportsARefusedConversionAsUnfound: State.Resolve has nowhere to
// put an error, so a render argument whose conversion the budget refuses is
// reported as not found -- and the render still fails, because the budget
// remembers its first refusal.
func TestResolveReportsARefusedConversionAsUnfound(t *testing.T) {
	env := mustEnv(gojja2.WithMaxIterations(100))
	found := true
	env.AddFilter("r", func(s *gojja2.State, v value.Value, a *value.CallArgs) (value.Value, error) {
		n, _ := a.Arg(0)
		got, ok := s.Resolve(value.Str(n))
		found = ok
		if !got.IsUndefined() {
			t.Errorf("Resolve of a refused argument = %v, want undefined", got)
		}
		return v, nil
	})
	tmpl, err := env.FromString(`{{ who|r('big') }}`)
	if err != nil {
		t.Fatal(err)
	}
	big := make([]any, 10_000)
	out, err := tmpl.RenderString(context.Background(), map[string]any{"who": "w", "big": big})
	if found {
		t.Error("Resolve found an argument its conversion was refused for")
	}
	if !errors.Is(err, gojja2.ErrTooManyIterations) {
		t.Errorf("render = %q, %v; want ErrTooManyIterations", out, err)
	}
}

// TestSettingsNameThemselves: the two leniency settings print as the name a
// caller wrote, for both values.
func TestSettingsNameThemselves(t *testing.T) {
	for _, tc := range []struct {
		got  string
		want string
	}{
		{gojja2.RefuseCollidingDelimiters.String(), "RefuseCollidingDelimiters"},
		{gojja2.MatchJinja2Delimiters.String(), "MatchJinja2Delimiters"},
		{gojja2.RefuseImpossibleExtensions.String(), "RefuseImpossibleExtensions"},
		{gojja2.AcceptAnyExtension.String(), "AcceptAnyExtension"},
	} {
		if tc.got != tc.want {
			t.Errorf("String() = %q, want %q", tc.got, tc.want)
		}
	}
}

// vanishingFS reports a file present to Stat and failing to ReadFile, which is
// what a file deleted between the two looks like.
type vanishingFS struct {
	fstest.MapFS
	err error
}

func (v vanishingFS) ReadFile(string) ([]byte, error) { return nil, v.err }

// TestFSLoaderNamesWithNoSegmentsAreMissing: jinja2's split_template_path drops
// empty and "." segments, so a name made of nothing else has no file to open
// and is a miss -- its os.path.isfile is False for the directory it names.
func TestFSLoaderNamesWithNoSegmentsAreMissing(t *testing.T) {
	for _, name := range []string{"", ".", "./", "//", "./.", "././/."} {
		for _, root := range []string{"tpl", ""} {
			loader := gojja2.FSLoader{FS: loaderFS(), Root: root}
			_, err := loader.Load(name)
			if !errors.Is(err, errs.TemplateNotFound) {
				t.Errorf("Load(%q) under root %q = %v, want TemplateNotFound", name, root, err)
				continue
			}
			if got := err.Error(); got != name {
				t.Errorf("Load(%q) reported %q, want the name asked for", name, got)
			}
		}
	}
}

// everywhereFS answers a regular file for any valid name, which is what makes
// the loader's own refusal of an empty name visible: over a real filesystem the
// stat of "" or of the bare root is refused first, by a different rule.
type everywhereFS struct{}

func (everywhereFS) Open(name string) (fs.File, error) {
	return fstest.MapFS{name: {Data: []byte("X")}}.Open(name)
}

// TestFSLoaderNamesWithNoSegmentsAreNotAskedOfTheFilesystem: the name is
// answered before the filesystem is consulted, so a filesystem that has a file
// at the root path does not turn "" into a template.
func TestFSLoaderNamesWithNoSegmentsAreNotAskedOfTheFilesystem(t *testing.T) {
	loader := gojja2.FSLoader{FS: everywhereFS{}, Root: "tpl"}
	if src, err := loader.Load("real.txt"); err != nil || src != "X" {
		t.Fatalf("Load(real.txt) = %q, %v; the stand-in filesystem is broken", src, err)
	}
	for _, name := range []string{"", ".", "//"} {
		if src, err := loader.Load(name); !errors.Is(err, errs.TemplateNotFound) {
			t.Errorf("Load(%q) = %q, %v; want TemplateNotFound", name, src, err)
		}
	}
}

// TestFSLoaderFileGoneAfterTheStat: a file that vanishes between the check that
// it is a regular file and the read is still a miss, and not an I/O error the
// caller has to tell from one -- while a read that fails for any other reason
// is reported as what it is.
func TestFSLoaderFileGoneAfterTheStat(t *testing.T) {
	files := fstest.MapFS{"a.txt": {Data: []byte("A")}}
	gone := gojja2.FSLoader{FS: vanishingFS{files, fs.ErrNotExist}}
	if _, err := gone.Load("a.txt"); !errors.Is(err, errs.TemplateNotFound) {
		t.Errorf("a vanished file: Load = %v, want TemplateNotFound", err)
	}
	broken := errors.New("disk on fire")
	bad := gojja2.FSLoader{FS: vanishingFS{files, broken}}
	if _, err := bad.Load("a.txt"); !errors.Is(err, broken) || errors.Is(err, errs.TemplateNotFound) {
		t.Errorf("a failing read: Load = %v, want the read's own error", err)
	}
}

// TestPrefixLoaderNamesItCannotRoute: a name with no delimiter, or with a
// prefix nobody registered, is a miss that names what was asked for -- which
// is PrefixLoader.get_source's ValueError and KeyError, both answered with
// TemplateNotFound(template).
func TestPrefixLoaderNamesItCannotRoute(t *testing.T) {
	loader := gojja2.PrefixLoader{
		Mapping: map[string]gojja2.Loader{"app": gojja2.DictLoader{"i": "I", "": "EMPTY"}},
	}
	// The registered loader has a template with the empty name, which "app"
	// must not reach by way of a prefix with nothing after it.
	for _, name := range []string{"i", "", "nope/i", "/i", "app"} {
		_, err := loader.Load(name)
		if !errors.Is(err, errs.TemplateNotFound) {
			t.Errorf("Load(%q) = %v, want TemplateNotFound", name, err)
			continue
		}
		if got := err.Error(); got != name {
			t.Errorf("Load(%q) reported %q, want the name asked for", name, got)
		}
	}
	if src, err := loader.Load("app/i"); err != nil || src != "I" {
		t.Errorf("Load(app/i) = %q, %v; want I", src, err)
	}
}
