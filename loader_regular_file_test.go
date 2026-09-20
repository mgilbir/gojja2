// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/mgilbir/gojja2"
	"github.com/mgilbir/gojja2/errs"
)

// jinja2's FileSystemLoader opens through os.path.isfile, so a name that is
// not a regular file is simply not found. gojja2 read it and reported what the
// filesystem said -- "read sub: is a directory" -- which is an error of the
// wrong *kind*, and the kind is what every caller branches on.

func regularFileFS() fstest.MapFS {
	return fstest.MapFS{
		"dir/inner.html": {Data: []byte("INNER")},
		"page.html":      {Data: []byte("PAGE")},
	}
}

func TestFSLoaderReportsADirectoryAsNotFound(t *testing.T) {
	env := mustEnv(gojja2.WithLoader(gojja2.FSLoader{FS: regularFileFS()}))
	for _, name := range []string{"dir", "dir/"} {
		_, err := env.GetTemplate(name)
		if !errors.Is(err, errs.TemplateNotFound) {
			t.Errorf("GetTemplate(%q) = %v, want TemplateNotFound", name, err)
		}
	}
}

// The kind is load-bearing twice over, so both consequences are pinned rather
// than the error text alone.

func TestChoiceLoaderFallsThroughADirectory(t *testing.T) {
	env := mustEnv(gojja2.WithLoader(gojja2.ChoiceLoader{
		gojja2.FSLoader{FS: regularFileFS()},
		gojja2.DictLoader{"dir": "FALLBACK"},
	}))
	tmpl, err := env.GetTemplate("dir")
	if err != nil {
		t.Fatalf("GetTemplate: %v", err)
	}
	got, err := tmpl.RenderString(context.Background(), nil)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if got != "FALLBACK" {
		t.Errorf("got %q, want the next loader's template", got)
	}
}

func TestIncludeIgnoreMissingCoversADirectory(t *testing.T) {
	env := mustEnv(gojja2.WithLoader(gojja2.FSLoader{FS: regularFileFS()}))
	tmpl, err := env.FromString(`[{% include "dir" ignore missing %}]`)
	if err != nil {
		t.Fatalf("FromString: %v", err)
	}
	got, err := tmpl.RenderString(context.Background(), nil)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if got != "[]" {
		t.Errorf("got %q, want the include ignored", got)
	}
}

// A template chooses the name an include resolves, so names arrive from
// expressions and need not be anything a filesystem accepts. os.DirFS refuses
// a name that is not valid UTF-8, or that carries a NUL byte, with
// fs.ErrInvalid -- which is not IsNotExist, so it used to reach the caller as
// "stat ...: invalid argument". jinja2 answers every one of these with
// TemplateNotFound; its os.path.isfile gate returns False rather than raising.
func TestFSLoaderReportsAnUnusableNameAsNotFound(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "page.html"), []byte("PAGE"), 0o600); err != nil {
		t.Fatal(err)
	}
	names := map[string]string{
		"invalid utf-8":  "\xff\xfe.html",
		"invalid inside": "a/\xffb.html",
		"lone surrogate": "\xed\xa0\x80.html",
		"nul byte":       "a\x00b.html",
	}
	for _, root := range []string{"", "sub"} {
		env := mustEnv(gojja2.WithLoader(
			gojja2.FSLoader{FS: os.DirFS(dir), Root: root}))
		for label, name := range names {
			_, err := env.GetTemplate(name)
			if !errors.Is(err, errs.TemplateNotFound) {
				t.Errorf("root=%q %s: got %v, want TemplateNotFound", root, label, err)
			}
		}
	}
}

// ...and a failure that is about the system rather than the name still
// reaches the caller, so a misconfigured deployment is not silently a miss.
func TestFSLoaderPassesARealStatErrorThrough(t *testing.T) {
	boom := errors.New("disk on fire")
	env := mustEnv(gojja2.WithLoader(gojja2.FSLoader{FS: errFS{err: boom}}))
	if _, err := env.GetTemplate("page.html"); !errors.Is(err, boom) {
		t.Errorf("got %v, want the filesystem's own error", err)
	}
}

// errFS fails every open with the error it was built with.
type errFS struct{ err error }

func (e errFS) Open(string) (fs.File, error) { return nil, e.err }
