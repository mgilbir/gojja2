// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package conformance_test

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestNoTwoCasesAreIdentical: two corpus cases with the same context and the
// same template grade the same thing, and the second one's name is a claim that
// it grades something else.
//
// This found eleven, and one cluster of five that mattered: the run-time half of
// the slice cases substituted the literal into the template but bound `x = 1`
// for every one of them, so subscript/sliceruntime_bool, _float, _dict,
// _dict_full and _none all sliced an integer. Five names, five types, one
// grading -- and all five passed, because an int is refused exactly as the
// others would have been.
//
// Compared as bytes, not as text: two cases differ in a carriage return alone
// (bytes/fromhex_newline_separates and _return_separates), and reading them as
// text through Go's or Python's newline handling would make them look the same.
// That is not hypothetical -- the oracle read the corpus in universal-newline
// mode, so it was handed a template with an LF where gojja2 had a CR.
func TestNoTwoCasesAreIdentical(t *testing.T) {
	root := filepath.Join(repoRoot(t), "testdata", "corpus")
	byContent := map[string][]string{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".jj2") {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		byContent[string(raw)] = append(byContent[string(raw)], filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	if len(byContent) == 0 {
		t.Fatal("no cases found; this test would pass on an empty corpus")
	}
	var reported int
	for _, names := range byContent {
		if len(names) < 2 {
			continue
		}
		sort.Strings(names)
		reported++
		t.Errorf("these cases are byte-identical, so all but one grade nothing: %s",
			strings.Join(names, ", "))
	}
	if reported > 0 {
		t.Logf("%d duplicated case(s) across %d files", reported, len(byContent))
	}
}
