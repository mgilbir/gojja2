// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package conformance_test

import (
	"context"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgilbir/gojja2"
	"github.com/mgilbir/gojja2/conformance"
)

// TestBothRenderPathsAgree runs every committed case through Template.Render as
// well as Template.RenderValues.
//
// The corpus grades RenderValues, which is handed a context that is already
// converted. Render is the one callers use, and it carries the caller's map
// unconverted so the argument scope can convert a name at a time -- so a bug in
// that laziness would be invisible to all 500-odd cases and to the fuzzer.
// Anything that differs here is the conversion path, not the template.
func TestBothRenderPathsAgree(t *testing.T) {
	const root = "../testdata/corpus"
	var paths []string
	if err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".jj2") {
			paths = append(paths, path)
		}
		return nil
	}); err != nil {
		t.Fatalf("walk corpus: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("no cases")
	}
	for _, path := range paths {
		c, err := conformance.LoadCase(root, path)
		if err != nil {
			t.Fatalf("load %s: %v", path, err)
		}
		t.Run(c.Rel, func(t *testing.T) {
			// A case is loaded once per path. Context holds values,
			// not a description of them, so the two renders would
			// otherwise share them -- and a case that appends to a
			// list would see the first render's append in the
			// second, reporting a disagreement between the paths
			// that is really a disagreement between two renders.
			fresh, err := conformance.LoadCase(root, path)
			if err != nil {
				t.Fatalf("reload %s: %v", path, err)
			}
			wantOut, wantErr := c.Render()
			gotOut, gotErr := fresh.RenderViaGo()
			if (wantErr == nil) != (gotErr == nil) {
				t.Fatalf("RenderValues err=%v, Render err=%v", wantErr, gotErr)
			}
			if wantErr != nil {
				if wantErr.Error() != gotErr.Error() {
					t.Errorf("RenderValues: %v\nRender     : %v", wantErr, gotErr)
				}
				return
			}
			if gotOut != wantOut {
				t.Errorf("RenderValues: %q\nRender     : %q", wantOut, gotOut)
			}
		})
	}
	t.Logf("both render paths agree on %d cases", len(paths))
}

// TestUnusedContextEntryIsNotConverted is the property the laziness exists for:
// a variable the template never mentions must cost nothing.
//
// Converting the whole context up front made `{{ title }}` with a fifty-element
// list beside it in the map allocate 379 times instead of 15.
func TestUnusedContextEntryIsNotConverted(t *testing.T) {
	big := make([]any, 0, 200)
	for i := range 200 {
		big = append(big, map[string]any{"a": i, "b": strings.Repeat("x", 8)})
	}
	small := map[string]any{"title": "t"}
	large := map[string]any{"title": "t", "unused": big}

	tmpl, err := gojja2.New().FromString(`{{ title }}`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	measure := func(vars map[string]any) float64 {
		return testing.AllocsPerRun(50, func() {
			if _, err := tmpl.RenderString(context.Background(), vars); err != nil {
				t.Fatalf("render: %v", err)
			}
		})
	}
	base, withUnused := measure(small), measure(large)
	// A wide margin: the point is that it does not scale with the unused
	// value, not that it is exactly equal.
	if withUnused > base+8 {
		t.Errorf("an unused 200-element context entry cost %.0f allocations "+
			"on top of %.0f; it should cost about none", withUnused-base, base)
	}
}
