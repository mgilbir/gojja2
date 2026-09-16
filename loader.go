// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"io/fs"
	"os"
	"path"
	"strings"

	"github.com/mgilbir/gojja2/errs"
)

// Loader finds template source by name.
//
// A miss must be reported as ErrNotFound (or an error wrapping it) rather than
// as an empty template, so that `{% include ... ignore missing %}` can tell the
// two apart.
type Loader interface {
	Load(name string) (source string, err error)
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
	data, err := fs.ReadFile(l.FS, p)
	if err != nil {
		if os.IsNotExist(err) {
			return "", notFound(name)
		}
		return "", err
	}
	return string(data), nil
}

// safeJoin resolves a template name under root, refusing any name that would
// escape it.
//
// Template names reach here from `{% include %}` and friends, where the name
// may be an arbitrary expression and therefore attacker-influenced. Rejecting
// traversal at the loader is the only place it can be done once.
func safeJoin(root, name string) (string, bool) {
	name = strings.TrimPrefix(path.Clean("/"+strings.ReplaceAll(name, "\\", "/")), "/")
	if name == "" || name == "." {
		return "", false
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return "", false
		}
	}
	if root == "" {
		return name, true
	}
	return path.Join(root, name), true
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
		if !errs.KindOf(err).DerivesFrom(errs.TemplateNotFound) {
			return "", err
		}
	}
	return "", notFound(name)
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
	return loader.Load(rest)
}
