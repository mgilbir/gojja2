// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"errors"
	"testing"

	"github.com/mgilbir/gojja2"
	"github.com/mgilbir/gojja2/errs"
)

// A PrefixLoader miss reports the name the caller asked for, which is the
// prefixed one. Handing back the inner loader's error names a template nobody
// mentioned -- and one that may well exist under a different prefix, so the
// message points at the wrong file rather than merely being terse.
func TestPrefixLoaderMissNamesTheFullTemplate(t *testing.T) {
	env := gojja2.New(gojja2.WithLoader(gojja2.PrefixLoader{
		Mapping: map[string]gojja2.Loader{
			"app":   gojja2.DictLoader{"i.txt": "I"},
			"other": gojja2.DictLoader{"missing": "DECOY"},
		},
	}))
	for _, tc := range []struct{ name, want string }{
		{"app/missing", "app/missing"},
		{"app/", "app/"},
		{"app//i.txt", "app//i.txt"},
	} {
		_, err := env.GetTemplate(tc.name)
		if !errors.Is(err, errs.TemplateNotFound) {
			t.Errorf("GetTemplate(%q) = %v, want TemplateNotFound", tc.name, err)
			continue
		}
		if got := err.Error(); got != tc.want {
			t.Errorf("GetTemplate(%q) reported %q, want %q", tc.name, got, tc.want)
		}
	}
}

// A custom delimiter changes where the name is cut and nothing else.
func TestPrefixLoaderMissNamesTheFullTemplateWithACustomDelimiter(t *testing.T) {
	env := gojja2.New(gojja2.WithLoader(gojja2.PrefixLoader{
		Mapping:   map[string]gojja2.Loader{"app": gojja2.DictLoader{"i": "I"}},
		Delimiter: "::",
	}))
	for _, name := range []string{"app::missing", "app::"} {
		_, err := env.GetTemplate(name)
		if !errors.Is(err, errs.TemplateNotFound) {
			t.Errorf("GetTemplate(%q) = %v, want TemplateNotFound", name, err)
			continue
		}
		if got := err.Error(); got != name {
			t.Errorf("GetTemplate(%q) reported %q, want %q", name, got, name)
		}
	}
}

// An error that is not a miss still belongs to the inner loader, unchanged:
// only the not-found case is renamed.
func TestPrefixLoaderPassesARealErrorThrough(t *testing.T) {
	boom := errors.New("disk on fire")
	env := gojja2.New(gojja2.WithLoader(gojja2.PrefixLoader{
		Mapping: map[string]gojja2.Loader{"app": missLoader{err: boom}},
	}))
	if _, err := env.GetTemplate("app/x"); !errors.Is(err, boom) {
		t.Errorf("got %v, want the inner loader's own error", err)
	}
}

// A missing template reports its name and nothing else. jinja2's
// FileSystemLoader appends the directories it searched; an fs.FS has no such
// path -- it may be a zip, an embed.FS or a synthesised map -- so there is
// nothing to append. Documented in docs/divergences.md, and pinned here so it
// cannot quietly become something else.
func TestFSLoaderMissNamesOnlyTheTemplate(t *testing.T) {
	loaders := map[string]gojja2.Loader{
		"fs":     gojja2.FSLoader{FS: regularFileFS()},
		"fsroot": gojja2.FSLoader{FS: regularFileFS(), Root: "dir"},
		"dict":   gojja2.DictLoader{"page.html": "PAGE"},
		"choice": gojja2.ChoiceLoader{gojja2.DictLoader{"page.html": "PAGE"}},
	}
	for label, l := range loaders {
		env := gojja2.New(gojja2.WithLoader(l))
		_, err := env.GetTemplate("missing.html")
		if !errors.Is(err, errs.TemplateNotFound) {
			t.Errorf("%s: got %v, want TemplateNotFound", label, err)
			continue
		}
		if got := err.Error(); got != "missing.html" {
			t.Errorf("%s: reported %q, want just the template name", label, got)
		}
	}
}
