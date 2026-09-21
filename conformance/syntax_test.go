// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package conformance_test

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mgilbir/gojja2"
	"github.com/mgilbir/gojja2/conformance"
	"github.com/mgilbir/gojja2/syntax"
)

// Template.Syntax exposes a template's structure so callers can ask their own
// questions of it. This requires the engine's tree and jinja2's to encode to the
// same canonical bytes for every committed case.
//
// Byte equality is the strong form of the guarantee, and the reason the
// vocabulary exists. An analysis that agrees proves the two see eye to eye about
// *that* analysis; trees that agree prove it for every question either will ever
// be asked, including the ones nobody has written. Without it, "you can query
// this tree" would be an invitation to reach a conclusion jinja2 would not.
//
// The trees are genuinely different shapes underneath -- jinja2 has nine classes
// for binary operators where gojja2 has one node with the operator on it, and
// carries call arguments in four fields where the vocabulary uses labelled edges.
// The normalisation is in the two emitters, not in the comparison: nothing here
// is allowed to forgive a difference.
func TestSyntaxMatchesTheReference(t *testing.T) {
	root := repoRoot(t)
	caseRoot := filepath.Join(root, "testdata/corpus")

	f, err := os.Open(filepath.Join(root, "testdata/syntax.jsonl"))
	if err != nil {
		t.Fatalf("open the reference trees: %v (run `make syntax`)", err)
	}
	defer func() { _ = f.Close() }()

	want := map[string]string{}
	wantInfo := map[string]string{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<22)
	for sc.Scan() {
		var row struct {
			Case string `json:"case"`
			Tree string `json:"tree"`
			Info string `json:"info"`
		}
		if err := json.Unmarshal(sc.Bytes(), &row); err != nil {
			t.Fatalf("parse the reference trees: %v", err)
		}
		want[row.Case] = row.Tree
		wantInfo[row.Case] = row.Info
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read the reference trees: %v", err)
	}
	if len(want) == 0 {
		t.Fatal("the reference trees are empty")
	}

	paths, err := conformance.Collect(caseRoot)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}

	var matched, differed, infoDiffered int
	for _, path := range paths {
		c, err := conformance.LoadCase(caseRoot, path)
		if err != nil {
			t.Fatalf("load case %s: %v", path, err)
		}
		ref, ok := want[c.Rel]
		if !ok {
			// jinja2 could not compile it, so the engine should not be
			// able to either. A case only one of them accepts is a
			// conformance failure rather than a missing reference.
			if _, cerr := compileCase(c); cerr == nil {
				t.Errorf("%s compiles here but has no reference tree; "+
					"run `make syntax`", c.Rel)
			}
			continue
		}
		tmpl, err := compileCase(c)
		if err != nil {
			t.Errorf("%s has a reference tree but does not compile: %v", c.Rel, err)
			continue
		}
		tree := tmpl.Syntax()
		got, err := syntax.Canonical(tree.Root)
		if err != nil {
			t.Errorf("%s: encoding the tree: %v", c.Rel, err)
			continue
		}
		if string(got) != ref {
			differed++
			if differed <= 5 {
				t.Errorf("%s: the two trees are not the same\n%s", c.Rel,
					firstDifference(ref, string(got)))
			}
			// The scope facts are keyed by the tree's nodes, so comparing
			// them against a tree that already differs would report the
			// same fault twice.
			continue
		}
		matched++

		gotInfo, err := syntax.CanonicalInfo(tree)
		if err != nil {
			t.Errorf("%s: encoding the scope facts: %v", c.Rel, err)
			continue
		}
		if string(gotInfo) != wantInfo[c.Rel] {
			infoDiffered++
			if infoDiffered <= 5 {
				t.Errorf("%s: the two disagree about scopes or bindings\n%s",
					c.Rel, firstDifference(wantInfo[c.Rel], string(gotInfo)))
			}
		}
	}
	if differed > 5 {
		t.Errorf("... and %d more trees differ", differed-5)
	}
	if infoDiffered > 5 {
		t.Errorf("... and %d more disagree about scopes or bindings", infoDiffered-5)
	}
	t.Logf("%d templates encode identically from both trees, scopes and bindings included",
		matched-infoDiffered)
}

// firstDifference shows the two encodings around the first byte they disagree
// on, because a whole tree printed twice says nothing a reader can use.
func firstDifference(want, got string) string {
	i := 0
	for i < len(want) && i < len(got) && want[i] == got[i] {
		i++
	}
	lo := i - 70
	if lo < 0 {
		lo = 0
	}
	clip := func(s string) string {
		hi := i + 90
		if hi > len(s) {
			hi = len(s)
		}
		return s[lo:hi]
	}
	return "    jinja2: ..." + clip(want) + "...\n    gojja2: ..." + clip(got) + "..."
}

// compileCase builds the case's environment and compiles its template, which is
// what Syntax needs and what the reference did on the other side.
func compileCase(c *conformance.Case) (*gojja2.Template, error) {
	env, err := c.Environment()
	if err != nil {
		return nil, err
	}
	return env.GetTemplate(c.Rel)
}
