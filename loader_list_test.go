// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"testing/fstest"

	"github.com/mgilbir/gojja2"
	"github.com/mgilbir/gojja2/errs"
)

// TestFSLoaderListsWhatItServes builds a tree on disk and lists it. The
// expectation is what CPython jinja2's FileSystemLoader.list_templates
// answered for the same tree, less the two names it lists and gojja2 cannot
// load: a dangling symlink, which jinja2 cannot load either, and a file whose
// name holds a backslash, which Load reads as a path.
func TestFSLoaderListsWhatItServes(t *testing.T) {
	root := t.TempDir()
	write := func(name string) {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"a.html", "sub/b.txt", "sub/deep/c.html", ".hidden", "noext"} {
		write(name)
	}
	if err := os.Mkdir(filepath.Join(root, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	want := []string{".hidden", "a.html", "noext", "sub/b.txt", "sub/deep/c.html"}
	if runtime.GOOS != "windows" {
		// A symlink to a file is listed and one to a directory is not
		// walked, as os.walk(followlinks=False) does.
		for link, target := range map[string]string{
			"filelink.html": "a.html", "dirlink": "sub", "dangling.html": "nowhere",
		} {
			if err := os.Symlink(target, filepath.Join(root, link)); err != nil {
				t.Fatal(err)
			}
		}
		write(`x\y.html`)
		want = []string{".hidden", "a.html", "filelink.html", "noext", "sub/b.txt", "sub/deep/c.html"}
	}

	loader := gojja2.FSLoader{FS: os.DirFS(root)}
	got, err := loader.ListTemplates()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, want) {
		t.Errorf("listed %q\nwant      %q", got, want)
	}
	// Everything listed can be loaded, and is the file of that name.
	for _, name := range got {
		src, err := loader.Load(name)
		if err != nil {
			t.Errorf("listed %q and could not load it: %v", name, err)
		}
		if name != "filelink.html" && src != name {
			t.Errorf("loaded %q and got the source of %q", name, src)
		}
	}

	// Root narrows the walk, and the names are relative to it.
	for _, r := range []string{"sub", "sub/", "./sub"} {
		got, err := gojja2.FSLoader{FS: os.DirFS(root), Root: r}.ListTemplates()
		if err != nil {
			t.Fatal(err)
		}
		if want := []string{"b.txt", "deep/c.html"}; !slices.Equal(got, want) {
			t.Errorf("Root %q: listed %q, want %q", r, got, want)
		}
	}
	// A Root that is not there is an empty list, as in jinja2.
	got, err = gojja2.FSLoader{FS: os.DirFS(root), Root: "missing"}.ListTemplates()
	if err != nil || len(got) != 0 {
		t.Errorf("missing Root: %q, %v; want nothing and no error", got, err)
	}
}

// TestFSLoaderListingReportsFailures holds the one place listing departs from
// os.walk on purpose: a tree that cannot be read is an error, not a shorter
// list, for the reason Load's own documentation gives.
func TestFSLoaderListingReportsFailures(t *testing.T) {
	broken := readDirFails{fstest.MapFS{"a.html": {Data: []byte("a")}}}
	if _, err := (gojja2.FSLoader{FS: broken}).ListTemplates(); !errors.Is(err, errBroken) {
		t.Errorf("err = %v, want the filesystem's", err)
	}
}

var errBroken = errors.New("disk on fire")

type readDirFails struct{ fstest.MapFS }

func (f readDirFails) ReadDir(name string) ([]os.DirEntry, error) { return nil, errBroken }

// TestComposedLoadersList checks DictLoader, PrefixLoader and ChoiceLoader
// against what jinja2 answered for the same mappings: sorted names, prefixes
// joined with the delimiter, and a sorted union without duplicates.
func TestComposedLoadersList(t *testing.T) {
	for _, tc := range []struct {
		name   string
		loader gojja2.Lister
		want   []string
	}{
		{"dict", gojja2.DictLoader{"b": "", "a": ""}, []string{"a", "b"}},
		// jinja2 answers z/a z/b a/q, its dict's insertion order; a Go
		// map has none, so the prefixes are sorted.
		{"prefix", gojja2.PrefixLoader{Mapping: map[string]gojja2.Loader{
			"z": gojja2.DictLoader{"b": "", "a": ""}, "a": gojja2.DictLoader{"q": ""},
		}}, []string{"a/q", "z/a", "z/b"}},
		{"prefix, own delimiter", gojja2.PrefixLoader{Delimiter: ":", Mapping: map[string]gojja2.Loader{
			"p": gojja2.DictLoader{"x": ""},
		}}, []string{"p:x"}},
		{"choice", gojja2.ChoiceLoader{
			gojja2.DictLoader{"b": "", "a": ""}, gojja2.DictLoader{"a": "", "c": ""},
		}, []string{"a", "b", "c"}},
	} {
		got, err := tc.loader.ListTemplates()
		if err != nil || !slices.Equal(got, tc.want) {
			t.Errorf("%s: %q, %v; want %q", tc.name, got, err, tc.want)
		}
		for _, name := range got {
			if _, err := tc.loader.Load(name); err != nil {
				t.Errorf("%s: listed %q and could not load it: %v", tc.name, name, err)
			}
		}
	}
}

// loadOnly is a Loader that cannot list, like jinja2's FunctionLoader.
type loadOnly struct{}

func (loadOnly) Load(name string) (string, error) { return "", gojja2.ErrNotFound }

// TestListTemplates is the environment's half: the filter, the extensions
// helper, and the TypeError for a loader that cannot list -- directly or from
// inside a ChoiceLoader, as jinja2 raises it from either.
func TestListTemplates(t *testing.T) {
	env := mustEnv(gojja2.WithLoader(gojja2.DictLoader{
		"a.html": "", "b.txt": "", "c.HTML": "", "noext": "", "d.x/e": "", "f.tar.gz": "",
	}))
	all, err := env.ListTemplates(nil)
	if want := []string{"a.html", "b.txt", "c.HTML", "d.x/e", "f.tar.gz", "noext"}; err != nil || !slices.Equal(all, want) {
		t.Errorf("ListTemplates(nil) = %q, %v; want %q", all, err, want)
	}
	// jinja2's rule is the text after the last dot, compared exactly --
	// so "d.x/e" has the extension "x/e" and "f.tar.gz" has "gz".
	for _, tc := range []struct {
		exts []string
		want []string
	}{
		{[]string{"html"}, []string{"a.html"}},
		{[]string{"html", "txt"}, []string{"a.html", "b.txt"}},
		{[]string{"gz"}, []string{"f.tar.gz"}},
		{[]string{"x/e"}, []string{"d.x/e"}},
		{[]string{".html"}, nil},
	} {
		got, err := env.ListTemplates(gojja2.HasExtension(tc.exts...))
		if err != nil || !slices.Equal(got, tc.want) {
			t.Errorf("HasExtension(%q) = %q, %v; want %q", tc.exts, got, err, tc.want)
		}
	}
	got, _ := env.ListTemplates(func(name string) bool { return len(name) == 5 })
	if want := []string{"b.txt", "d.x/e", "noext"}; !slices.Equal(got, want) {
		t.Errorf("own filter = %q, want %q", got, want)
	}

	for name, l := range map[string]gojja2.Loader{
		"loader":          loadOnly{},
		"inside a choice": gojja2.ChoiceLoader{gojja2.DictLoader{}, loadOnly{}},
		"inside a prefix": gojja2.PrefixLoader{Mapping: map[string]gojja2.Loader{"p": loadOnly{}}},
	} {
		_, err := mustEnv(gojja2.WithLoader(l)).ListTemplates(nil)
		if errs.KindOf(err) != errs.TypeError || err.Error() != "this loader cannot iterate over all templates" {
			t.Errorf("%s that cannot list: %v", name, err)
		}
	}
	if _, err := mustEnv().ListTemplates(nil); errs.KindOf(err) != errs.TypeError {
		t.Errorf("no loader: %v, want a TypeError", err)
	}
}
