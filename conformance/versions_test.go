// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package conformance_test

import (
	"path/filepath"
	"testing"

	"github.com/mgilbir/gojja2"
	"github.com/mgilbir/gojja2/conformance"
)

// pythonVersions are the interpreters gojja2 reproduces, with the goldens that
// record what each one answered.
//
// Only the differences are stored. 2,159 of the 2,197 cases answer identically
// on all four, so an older version is a directory of the few dozen that do not
// -- 38 files for 3.11, 25 for 3.12, 19 for 3.13 -- laid over the default
// version's full set.
var pythonVersions = []struct {
	version  gojja2.PythonVersion
	override string // relative to the repository root; empty for the default
}{
	{gojja2.Python311, "testdata/golden-3.11"},
	{gojja2.Python312, "testdata/golden-3.12"},
	{gojja2.Python313, "testdata/golden-3.13"},
	{gojja2.Python314, ""},
}

// Every corpus case, against every interpreter gojja2 claims to reproduce.
//
// TestConformance grades the default version. This grades the knob: a template
// rendered under WithPythonVersion(3.11) has to produce what CPython 3.11
// produced, not what 3.14 does. Without this the option would compile, read
// plausibly, and be wrong in any of the thirteen places the interpreters
// disagree -- and the corpus would not notice, because it only ever asked one
// of them.
func TestEveryPythonVersion(t *testing.T) {
	root := repoRoot(t)
	caseRoot := filepath.Join(root, "testdata/corpus")
	goldenRoot := filepath.Join(root, "testdata/golden")

	paths, err := conformance.Collect(caseRoot)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("the committed corpus is empty")
	}
	known := loadKnownFailures(t, root)

	for _, pv := range pythonVersions {
		t.Run(pv.version.String(), func(t *testing.T) {
			overrideRoot := ""
			if pv.override != "" {
				overrideRoot = filepath.Join(root, pv.override)
			}
			var matched, failed int
			for _, path := range paths {
				c, err := conformance.LoadCase(caseRoot, path)
				if err != nil {
					t.Fatalf("load case %s: %v", path, err)
				}
				id := "own/" + c.Rel
				golden, err := conformance.LoadGoldenOver(goldenRoot, overrideRoot, c.Rel)
				if err != nil {
					t.Fatalf("load golden for %s: %v", id, err)
				}
				out, renderErr := c.RenderFor(pv.version)
				d := conformance.Compare(golden.Expected(), out, renderErr)
				if _, listed := known[id]; listed {
					continue
				}
				if d != nil {
					failed++
					if failed <= 10 {
						t.Errorf("%s under CPython %s\n%s", id, pv.version, indent(d.String()))
					}
					continue
				}
				matched++
			}
			if failed > 10 {
				t.Errorf("... and %d more under CPython %s", failed-10, pv.version)
			}
			t.Logf("CPython %s: %d cases match", pv.version, matched)
		})
	}
}

// TestVersionOverridesAreAllUsed: an override that matches the base set is a
// file recording nothing, and one naming a case the corpus no longer has is a
// file nothing reads. Either way it rots quietly, so both fail here.
//
// This is what keeps the override directories honest as CPython moves: when a
// future default makes an older version's answer identical again, the file to
// delete says so rather than sitting there agreeing with its neighbour.
func TestVersionOverridesAreAllUsed(t *testing.T) {
	root := repoRoot(t)
	goldenRoot := filepath.Join(root, "testdata/golden")
	for _, pv := range pythonVersions {
		if pv.override == "" {
			continue
		}
		overrideRoot := filepath.Join(root, pv.override)
		files, err := filepath.Glob(filepath.Join(overrideRoot, "*", "*.json"))
		if err != nil {
			t.Fatalf("glob %s: %v", pv.override, err)
		}
		if len(files) == 0 {
			t.Errorf("%s holds no overrides; if every case now agrees with "+
				"the default, drop the directory and its entry", pv.override)
			continue
		}
		for _, f := range files {
			rel, err := filepath.Rel(overrideRoot, f)
			if err != nil {
				t.Fatal(err)
			}
			caseFile := filepath.Join(root, "testdata/corpus",
				rel[:len(rel)-len(".json")]+".jj2")
			if _, err := conformance.LoadCase(filepath.Join(root, "testdata/corpus"),
				caseFile); err != nil {
				t.Errorf("%s/%s overrides a case the corpus no longer has", pv.override, rel)
				continue
			}
			base, err := conformance.LoadGolden(goldenRoot, rel[:len(rel)-len(".json")]+".jj2")
			if err != nil {
				t.Errorf("%s/%s has no base golden to override", pv.override, rel)
				continue
			}
			over, err := conformance.LoadGoldenOver(overrideRoot, "", rel[:len(rel)-len(".json")]+".jj2")
			if err != nil {
				t.Fatalf("read override %s: %v", rel, err)
			}
			if base.Expected().Equal(over.Expected()) {
				t.Errorf("%s/%s records the same answer as the default version; "+
					"delete it", pv.override, rel)
			}
		}
	}
}
