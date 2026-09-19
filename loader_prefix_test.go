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
