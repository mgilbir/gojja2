// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package conformance_test

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mgilbir/gojja2/conformance"
	"github.com/mgilbir/gojja2/dataflow"
	"github.com/mgilbir/gojja2/syntax"
)

// The same three checks, against the templates nobody here wrote.
//
// The committed corpus is written in this repository and so knows what it is
// testing. The imported corpora are chat templates real models ship, Jinja's own
// suite, cookiecutter projects, minja and llama.cpp -- and they are where a
// construct the author of a parser did not think to include turns up.
//
// They are gitignored, so this skips unless `make import` has been run. That
// makes it a check a contributor gets rather than one CI enforces, which is the
// same bargain the imported corpora are on already.
func TestGeneratedCorporaAgree(t *testing.T) {
	root := repoRoot(t)
	refs := filepath.Join(root, "testdata/generated/references.jsonl")
	f, err := os.Open(refs)
	if err != nil {
		t.Skip("no imported corpora; run `make import` to include them")
	}
	defer func() { _ = f.Close() }()

	type ref struct {
		Tree      string            `json:"tree"`
		Info      string            `json:"info"`
		Variables map[string]string `json:"variables"`
	}
	want := map[string]ref{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<24)
	for sc.Scan() {
		var row struct {
			Case string `json:"case"`
			ref
		}
		if err := json.Unmarshal(sc.Bytes(), &row); err != nil {
			t.Fatalf("parse %s: %v", refs, err)
		}
		want[row.Case] = row.ref
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read %s: %v", refs, err)
	}
	if len(want) == 0 {
		t.Fatalf("%s is empty; run `make import`", refs)
	}

	var trees, infos, flows, checked int
	for _, corpus := range corporaWithReferences(root) {
		caseRoot := filepath.Join(root, "testdata/generated", corpus)
		paths, err := conformance.Collect(caseRoot)
		if err != nil {
			continue
		}
		for _, p := range paths {
			c, err := conformance.LoadCase(caseRoot, p)
			if err != nil {
				continue
			}
			r, ok := want[corpus+"/"+c.Rel]
			if !ok {
				continue
			}
			env, err := c.Environment()
			if err != nil {
				continue
			}
			tmpl, err := env.GetTemplate(c.Rel)
			if err != nil {
				t.Errorf("%s/%s: jinja2 compiled it and gojja2 did not: %v",
					corpus, c.Rel, err)
				continue
			}
			checked++
			tree := tmpl.Syntax()

			got, err := syntax.Canonical(tree.Root)
			if err != nil || string(got) != r.Tree {
				trees++
				if trees <= 3 {
					t.Errorf("%s/%s: the two trees are not the same\n%s",
						corpus, c.Rel, firstDifference(r.Tree, string(got)))
				}
				continue
			}
			gotInfo, err := syntax.CanonicalInfo(tree)
			if err != nil || string(gotInfo) != r.Info {
				infos++
				if infos <= 3 {
					t.Errorf("%s/%s: the two disagree about scopes or bindings\n%s",
						corpus, c.Rel, firstDifference(r.Info, string(gotInfo)))
				}
				continue
			}
			flow := dataflow.Analyze(tree, dataflow.WithResolver(func(n string) *syntax.Tree {
				other, err := env.GetTemplate(n)
				if err != nil {
					return nil
				}
				return other.Syntax()
			}))
			gotVars := map[string]string{}
			for name, e := range flow.Context(tree) {
				gotVars[name] = encodeEffect(e)
			}
			if diff := diffEffects(r.Variables, gotVars); diff != "" {
				flows++
				if flows <= 3 {
					t.Errorf("%s/%s: the two analyses disagree\n%s",
						corpus, c.Rel, diff)
				}
			}
		}
	}
	for what, n := range map[string]int{"trees": trees, "symbol tables": infos,
		"analyses": flows} {
		if n > 3 {
			t.Errorf("... and %d more %s differ", n-3, what)
		}
	}
	t.Logf("%d imported templates agree on tree, scopes and dataflow", checked)
}

func corporaWithReferences(root string) []string {
	entries, err := os.ReadDir(filepath.Join(root, "testdata/generated"))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasSuffix(e.Name(), "-golden") {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}
