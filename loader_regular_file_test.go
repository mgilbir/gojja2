// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"context"
	"errors"
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
	env := gojja2.New(gojja2.WithLoader(gojja2.FSLoader{FS: regularFileFS()}))
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
	env := gojja2.New(gojja2.WithLoader(gojja2.ChoiceLoader{
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
	env := gojja2.New(gojja2.WithLoader(gojja2.FSLoader{FS: regularFileFS()}))
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
