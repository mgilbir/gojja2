// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"errors"
	"io/fs"
	"maps"
	"os"
	"path"
	"slices"
	"sort"
	"strings"

	"github.com/mgilbir/gojja2/errs"
)

// Loader finds template source by name.
//
// A miss must be reported as an error satisfying errors.Is(err, ErrNotFound)
// rather than as an empty template, so that `{% include ... ignore missing %}`,
// ChoiceLoader's fallthrough and select_template's can tell a miss from a
// failure. Returning ErrNotFound itself, or anything wrapping it with %w, does
// that; so does any error carrying a kind that derives from it.
//
// Any other error stops the search and reaches the caller unchanged. That
// distinction is the whole point: a loader whose disk is failing must not read
// as "not here" and let the next loader quietly answer instead.
type Loader interface {
	Load(name string) (source string, err error)
}

// Lister is a Loader that can name every template it serves, which is what
// [Environment.ListTemplates] asks of it -- jinja2's
// BaseLoader.list_templates.
//
// Every loader in this package is one. A loader of your own need not be; asking
// an environment to list through one that is not is the TypeError jinja2
// raises for a loader that cannot iterate.
type Lister interface {
	Loader
	// ListTemplates returns every name Load would serve.
	ListTemplates() ([]string, error)
}

// cannotList is jinja2's refusal for a loader with no list_templates.
func cannotList() error {
	return errs.New(errs.TypeError, "this loader cannot iterate over all templates")
}

// listFrom lists through l, refusing a loader that cannot.
func listFrom(l Loader) ([]string, error) {
	lister, ok := l.(Lister)
	if !ok {
		return nil, cannotList()
	}
	return lister.ListTemplates()
}

// ErrNotFound reports a template that does not exist.
var ErrNotFound = errs.TemplateNotFound

// notFound builds the error jinja2 raises for a missing template, whose
// message is just the name.
func notFound(name string) error {
	return errs.New(errs.TemplateNotFound, "%s", name)
}

// DictLoader serves templates from an in-memory map.
type DictLoader map[string]string

// Load implements Loader.
func (d DictLoader) Load(name string) (string, error) {
	src, ok := d[name]
	if !ok {
		return "", notFound(name)
	}
	return src, nil
}

// ListTemplates implements Lister: the names, sorted.
func (d DictLoader) ListTemplates() ([]string, error) {
	return slices.Sorted(maps.Keys(d)), nil
}

// LoaderFunc adapts a function to a [Loader], which is jinja2's FunctionLoader.
//
// Where FunctionLoader's function returns None for a template it does not
// have, this one returns an error satisfying errors.Is(err, ErrNotFound) --
// ErrNotFound itself will do -- so that `ignore missing`, ChoiceLoader and
// SelectTemplate can tell a miss from a failure. It cannot list, as
// FunctionLoader cannot.
type LoaderFunc func(name string) (source string, err error)

// Load implements Loader.
func (f LoaderFunc) Load(name string) (string, error) { return f(name) }

// FSLoader serves templates from an fs.FS, which is the portable way to load
// from disk, an embed.FS or a zip.
type FSLoader struct {
	FS fs.FS
	// Root is an optional prefix joined ahead of every name.
	Root string
}

// Load implements Loader.
func (l FSLoader) Load(name string) (string, error) {
	p, ok := safeJoin(l.Root, name)
	if !ok {
		return "", notFound(name)
	}
	// jinja2 opens through os.path.isfile, so anything that is not a
	// regular file -- a directory, a device, a socket -- is simply not
	// found. Reading it and reporting what the filesystem said instead
	// leaks an error of the wrong *kind*, and the kind is what callers
	// branch on: ChoiceLoader stops the chain on anything that is not
	// TemplateNotFound, so a name that is a directory in the first loader
	// aborted the lookup rather than falling through to the next one, and
	// `{% include "x" ignore missing %}` failed on it rather than ignoring
	// it.
	if info, err := fs.Stat(l.FS, p); err != nil || !info.Mode().IsRegular() {
		// A name the filesystem will not accept is a name that is not
		// there. os.DirFS refuses one that is not valid UTF-8, or that
		// carries a NUL byte, with fs.ErrInvalid -- and a template
		// chooses the name an include resolves, so those arrive from
		// expressions. jinja2 answers all of them with TemplateNotFound,
		// because its os.path.isfile gate returns False rather than
		// raising. Anything else -- a permission or I/O failure, which
		// is about the system rather than the name -- still propagates.
		if err != nil && !os.IsNotExist(err) && !errors.Is(err, fs.ErrInvalid) {
			return "", err
		}
		return "", notFound(name)
	}
	data, err := fs.ReadFile(l.FS, p)
	if err != nil {
		if os.IsNotExist(err) {
			return "", notFound(name)
		}
		return "", err
	}
	return string(data), nil
}

// ListTemplates implements Lister: every regular file under Root, named with
// "/" whatever the platform, sorted.
//
// It is jinja2's FileSystemLoader.list_templates, which walks without
// following a symlink to a directory, with two differences. A name Load would
// refuse is left out, because listing a template that cannot then be loaded
// helps nobody: on Linux jinja2 lists a file called `a\b.html`, which Load
// here reads as a path. And a failure to read part of the tree is reported,
// where os.walk skips it silently; a Root that does not exist is still an
// empty list, as it is there.
func (l FSLoader) ListTemplates() ([]string, error) {
	root := "."
	if l.Root != "" {
		root = path.Clean(l.Root)
	}
	var names []string
	err := fs.WalkDir(l.FS, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == root && errors.Is(err, fs.ErrNotExist) {
				return fs.SkipAll
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		name := p
		if root != "." {
			name = strings.TrimPrefix(p, root+"/")
		}
		// What Load would open for this name must be this file.
		if joined, ok := safeJoin(l.Root, name); !ok || joined != p {
			return nil
		}
		// Load serves regular files only, following a symlink to one.
		if info, err := fs.Stat(l.FS, p); err != nil || !info.Mode().IsRegular() {
			return nil
		}
		names = append(names, name)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	return names, nil
}

// safeJoin resolves a template name under root, refusing any name that would
// escape it.
//
// This is jinja2's split_template_path: the name is cut on "/", a segment of
// ".." is refused outright, and empty and "." segments are dropped. Refusing
// is the point. Cleaning the path instead -- which is what this did -- silently
// answers a different question: `{% include "../secret.html" %}` resolved to
// "secret.html" and rendered it, so a template that asked for a file it should
// not have got a *different* file and no error. The confinement held either
// way, which is why it went unnoticed; the honest answer to a name that climbs
// out is that there is no such template.
//
// The rejection also has to happen before any cleaning, or it can never fire:
// path.Clean on a rooted path resolves every ".." away, so a check that ran
// afterwards was unreachable code standing where the security control appeared
// to be.
//
// A backslash is treated as a separator on every platform. jinja2 only refuses
// one where the operating system says so, which leaves `..\secret` a valid
// single segment on Linux; here it is refused everywhere, because a loader
// backed by a Windows filesystem would read it as a path either way.
func safeJoin(root, name string) (string, bool) {
	var parts []string
	for _, part := range strings.Split(strings.ReplaceAll(name, "\\", "/"), "/") {
		switch part {
		case "..":
			return "", false
		case "", ".":
			continue
		}
		parts = append(parts, part)
	}
	if len(parts) == 0 {
		return "", false
	}
	joined := strings.Join(parts, "/")
	if root == "" {
		return joined, true
	}
	return path.Join(root, joined), true
}

// ChoiceLoader tries each loader in turn and uses the first hit.
type ChoiceLoader []Loader

// Load implements Loader.
func (c ChoiceLoader) Load(name string) (string, error) {
	for _, l := range c {
		src, err := l.Load(name)
		if err == nil {
			return src, nil
		}
		if !errors.Is(err, errs.TemplateNotFound) {
			return "", err
		}
	}
	return "", notFound(name)
}

// ListTemplates implements Lister: every loader's names, without duplicates,
// sorted. A loader that cannot list makes the whole list fail, as in jinja2.
func (c ChoiceLoader) ListTemplates() ([]string, error) {
	seen := map[string]bool{}
	var names []string
	for _, l := range c {
		inner, err := listFrom(l)
		if err != nil {
			return nil, err
		}
		for _, name := range inner {
			if !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}
	}
	sort.Strings(names)
	return names, nil
}

// PrefixLoader dispatches on a leading path segment: "admin/index.html" is
// looked up as "index.html" in the loader registered under "admin".
type PrefixLoader struct {
	Mapping   map[string]Loader
	Delimiter string
}

// Load implements Loader.
func (p PrefixLoader) Load(name string) (string, error) {
	delim := p.Delimiter
	if delim == "" {
		delim = "/"
	}
	prefix, rest, ok := strings.Cut(name, delim)
	if !ok {
		return "", notFound(name)
	}
	loader, ok := p.Mapping[prefix]
	if !ok {
		return "", notFound(name)
	}
	src, err := loader.Load(rest)
	if errors.Is(err, errs.TemplateNotFound) {
		// The name the caller asked for is the prefixed one, so that
		// is the name the miss reports. Handing back the inner
		// loader's error names a template nobody mentioned:
		// `{% include "app/missing" %}` said "missing", which is a
		// different name, and one that may well exist elsewhere.
		return "", notFound(name)
	}
	return src, err
}

// ListTemplates implements Lister: each loader's names under its prefix.
//
// jinja2 walks its mapping in insertion order, which a Go map does not have, so
// the prefixes are taken in sorted order instead; within one, the names come in
// whatever order that loader lists them.
func (p PrefixLoader) ListTemplates() ([]string, error) {
	delim := p.Delimiter
	if delim == "" {
		delim = "/"
	}
	var names []string
	for _, prefix := range slices.Sorted(maps.Keys(p.Mapping)) {
		inner, err := listFrom(p.Mapping[prefix])
		if err != nil {
			return nil, err
		}
		for _, name := range inner {
			names = append(names, prefix+delim+name)
		}
	}
	return names, nil
}
