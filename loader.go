// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"errors"
	"io/fs"
	"os"
	"path"
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
		if err != nil && !os.IsNotExist(err) {
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
