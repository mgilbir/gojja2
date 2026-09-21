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
// Only the differences are stored. The great majority of the corpus answers
// identically on all four, so a non-pinned version is a directory of the few
// dozen cases that do not, laid over the pinned version's full set.
//
// Which one is pinned is read from gojja2.DefaultPythonVersion rather than
// written out again here: the pin already lives in the Makefile and in that
// constant, and a third copy is one more place a bump can be half-applied.
// TestDefaultVersionMatchesThePin checks the first two agree.
var pythonVersions = []gojja2.PythonVersion{
	gojja2.Python311,
	gojja2.Python312,
	gojja2.Python313,
	gojja2.Python314,
}

// overrideDir is where a version's differences live, or "" for the pinned one,
// whose answers are testdata/golden itself.
func overrideDir(v gojja2.PythonVersion) string {
	if v == gojja2.DefaultPythonVersion {
		return ""
	}
	return "testdata/golden-" + v.String()
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
		t.Run(pv.String(), func(t *testing.T) {
			overrideRoot := ""
			if o := overrideDir(pv); o != "" {
				overrideRoot = filepath.Join(root, o)
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
				out, renderErr := c.RenderFor(pv)
				d := conformance.Compare(golden.Expected(), out, renderErr)
				if _, listed := known[id]; listed {
					continue
				}
				if d != nil {
					failed++
					if failed <= 10 {
						t.Errorf("%s under CPython %s\n%s", id, pv, indent(d.String()))
					}
					continue
				}
				matched++
			}
			if failed > 10 {
				t.Errorf("... and %d more under CPython %s", failed-10, pv)
			}
			t.Logf("CPython %s: %d cases match", pv, matched)
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
		dir := overrideDir(pv)
		if dir == "" {
			continue
		}
		overrideRoot := filepath.Join(root, dir)
		files, err := filepath.Glob(filepath.Join(overrideRoot, "*", "*.json"))
		if err != nil {
			t.Fatalf("glob %s: %v", dir, err)
		}
		if len(files) == 0 {
			t.Errorf("%s holds no overrides; if every case now agrees with "+
				"the pin, drop the directory and the version's entry", dir)
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
				t.Errorf("%s/%s overrides a case the corpus no longer has", dir, rel)
				continue
			}
			base, err := conformance.LoadGolden(goldenRoot, rel[:len(rel)-len(".json")]+".jj2")
			if err != nil {
				t.Errorf("%s/%s has no base golden to override", dir, rel)
				continue
			}
			over, err := conformance.LoadGoldenOver(overrideRoot, "", rel[:len(rel)-len(".json")]+".jj2")
			if err != nil {
				t.Fatalf("read override %s: %v", rel, err)
			}
			if base.Expected().Equal(over.Expected()) {
				t.Errorf("%s/%s records the same answer as the default version; "+
					"delete it", dir, rel)
			}
		}
	}
}
