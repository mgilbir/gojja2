// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"context"
	"io"
	"strings"
	"testing"
)

// An environment with no loader reports *itself* before it looks at the name.
//
// jinja2's _load_template makes that check before it builds its cache key, so a
// template name that is an unhashable list, or an undefined, or not a string at
// all, is never what gets reported. gojja2 reported the name in three of those
// and the loader in the fourth.
//
// This cannot be a corpus case: the corpus environment has a loader, and a case
// has no way to ask for one without. The with-loader half *is* graded, by
// own/errors/template_name_*, which is the other side of each pair below.
func TestNoLoaderIsReportedBeforeTheName(t *testing.T) {
	const noLoader = "no loader for this environment specified"
	for name, tc := range map[string]struct {
		src  string
		want string
	}{
		// select_template refuses an empty selection before it looks up
		// any name, so this one does not reach the loader check.
		"empty selection":   {`{% set e = [] %}{% include e %}`, "Tried to select from an empty list"},
		"list name":         {`{% set e = [1] %}{% extends e %}`, noLoader},
		"empty list name":   {`{% set e = [] %}{% extends e %}`, noLoader},
		"undefined extends": {`{% extends nope %}`, noLoader},
		"undefined include": {`{% include nope %}`, noLoader},
		"numeric name":      {`{% set n = 1 %}{% extends n %}`, noLoader},
		"string name":       {`{% include 'nope.txt' %}`, noLoader},
	} {
		t.Run(name, func(t *testing.T) {
			tmpl, err := mustEnv().FromString(tc.src)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			err = tmpl.Render(context.Background(), io.Discard, nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("%s\n  = %v\n want something containing %q", tc.src, err, tc.want)
			}
		})
	}
}
