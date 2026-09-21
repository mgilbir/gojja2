// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package conformance_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mgilbir/gojja2/conformance"
	"github.com/mgilbir/gojja2/dataflow"
)

// dataflow.Analyze answers which of the caller's variables a template can print
// and which only steer it. This grades it against the same analysis written over
// jinja2's own AST, in tools/oracle/nameflow.py.
//
// The trees already encode identically and the scope facts already agree, which
// makes this a check of one thing only: the dataflow reasoning on top. It is
// also the demonstration that the exposed tree is enough to reason with --
// everything in package dataflow reads syntax.Tree and nothing else, so an
// answer it can reach is an answer any caller can reach.
func TestDataflowMatchesTheReference(t *testing.T) {
	root := repoRoot(t)
	caseRoot := filepath.Join(root, "testdata/corpus")
	refRoot := filepath.Join(root, "testdata/nameflow")

	paths, err := conformance.Collect(caseRoot)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}

	var checked, mismatched int
	for _, path := range paths {
		c, err := conformance.LoadCase(caseRoot, path)
		if err != nil {
			t.Fatalf("load case %s: %v", path, err)
		}
		raw, err := os.ReadFile(filepath.Join(refRoot,
			strings.TrimSuffix(c.Rel, ".jj2")+".json"))
		if err != nil {
			continue // jinja2 could not compile it; TestSyntax covers that
		}
		var ref struct {
			Variables map[string]string `json:"variables"`
		}
		if err := json.Unmarshal(raw, &ref); err != nil {
			t.Fatalf("parse the reference: %v", err)
		}
		tmpl, err := compileCase(c)
		if err != nil {
			continue
		}
		tree := tmpl.Syntax()
		got := map[string]string{}
		for name, e := range dataflow.Analyze(tree).Context(tree) {
			got[name] = encodeEffect(e)
		}
		checked++
		if diff := diffEffects(ref.Variables, got); diff != "" {
			mismatched++
			if mismatched <= 10 {
				t.Errorf("%s: the two analyses disagree\n%s", c.Rel, diff)
			}
		}
	}
	if mismatched > 10 {
		t.Errorf("... and %d more disagreements", mismatched-10)
	}
	t.Logf("%d cases agree with the reference", checked-mismatched)
}

func encodeEffect(e dataflow.Effect) string {
	s := ""
	if e&dataflow.Printed != 0 {
		s += "o"
	}
	if e&dataflow.Steers != 0 {
		s += "f"
	}
	if s == "" {
		s = "-"
	}
	if e&dataflow.Opaque != 0 {
		s += "?"
	}
	return s
}

func diffEffects(want, got map[string]string) string {
	names := map[string]bool{}
	for k := range want {
		names[k] = true
	}
	for k := range got {
		names[k] = true
	}
	keys := make([]string, 0, len(names))
	for k := range names {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	for _, k := range keys {
		w, g := want[k], got[k]
		if w == g {
			continue
		}
		if w == "" {
			w = "(absent)"
		}
		if g == "" {
			g = "(absent)"
		}
		b.WriteString("    " + k + ": jinja2 says " + w + ", gojja2 says " + g + "\n")
	}
	return b.String()
}
