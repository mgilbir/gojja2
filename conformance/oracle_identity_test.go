// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package conformance_test

import (
	"testing"

	"github.com/mgilbir/gojja2"
	"github.com/mgilbir/gojja2/conformance"
)

// The goldens record which interpreter produced them, and StartOracle refuses a
// live oracle that is not it. This checks the other half of that comparison --
// the expectation -- which needs no Python and so runs everywhere.
//
// It also closes the last leg of the pin. The Makefile, DefaultPythonVersion
// and each generated file's header are held together by
// TestDefaultVersionMatchesThePin; the goldens are the largest artifact of the
// lot and were tied to none of them. TestConformance would notice eventually,
// but only if the two versions happened to disagree about a case in the corpus,
// and it would report it as a few hundred rendering failures rather than as
// "these goldens are someone else's".
func TestGoldensRecordThePinnedOracle(t *testing.T) {
	root := repoRoot(t)
	want, err := conformance.ExpectedOracle(root)
	if err != nil {
		t.Fatalf("read the goldens' oracle identity: %v", err)
	}
	if want.PythonFull == "" || want.Jinja2 == "" || want.MarkupSafe == "" {
		t.Fatalf("the goldens do not record a complete oracle identity: %+v", want)
	}
	if got := gojja2.DefaultPythonVersion.String(); want.Python != got {
		t.Errorf("testdata/golden was recorded under CPython %s, but "+
			"gojja2.DefaultPythonVersion is %s.\nEvery committed golden is one "+
			"interpreter's answers; rendering as another against them is a wrong "+
			"answer nothing else here would name. Run `make oracle`.",
			want.PythonFull, got)
	}
	t.Logf("goldens recorded under %s", want)
}
