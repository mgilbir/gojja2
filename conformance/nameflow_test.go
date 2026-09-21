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

	"github.com/mgilbir/gojja2"
	"github.com/mgilbir/gojja2/conformance"
)

// Template.Variables answers which of the caller's variables a template can
// print, which only steer it, and which the analysis could not follow. This
// grades that against the same analysis written over jinja2's own AST, in
// tools/oracle/nameflow.py, and recorded in testdata/nameflow.
//
// Two implementations is the point rather than the cost. The answer has to be a
// property of the template, not of either tree's shape -- gojja2 collapses
// jinja2's nine binary-operator classes into one node and carries arguments in
// a struct jinja2 does not have -- so deriving it twice and requiring the
// results to match is what makes the answer mean anything. One implementation
// would be right by definition.
//
// It also catches the failure that cross-checking alone cannot: both being
// wrong the same way. `loop` was reported as a caller's variable by both, until
// the cases below asked about a template that uses it.
func TestVariablesMatchTheReference(t *testing.T) {
	root := repoRoot(t)
	caseRoot := filepath.Join(root, "testdata/corpus")
	refRoot := filepath.Join(root, "testdata/nameflow")

	paths, err := conformance.Collect(caseRoot)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("the committed corpus is empty")
	}

	var checked, mismatched, noRef int
	for _, path := range paths {
		c, err := conformance.LoadCase(caseRoot, path)
		if err != nil {
			t.Fatalf("load case %s: %v", path, err)
		}
		refPath := filepath.Join(refRoot, strings.TrimSuffix(c.Rel, ".jj2")+".json")
		raw, err := os.ReadFile(refPath)
		if err != nil {
			// The reference skips a case jinja2 cannot parse, and so does
			// the engine. A case with no reference must be one of those.
			noRef++
			if _, cerr := compileCase(c); cerr == nil {
				t.Errorf("%s compiles here but has no reference analysis; "+
					"run `make nameflow`", c.Rel)
			}
			continue
		}
		var ref struct {
			Variables map[string]string `json:"variables"`
		}
		if err := json.Unmarshal(raw, &ref); err != nil {
			t.Fatalf("parse %s: %v", refPath, err)
		}

		tmpl, err := compileCase(c)
		if err != nil {
			t.Errorf("%s has a reference analysis but does not compile: %v", c.Rel, err)
			continue
		}
		got := map[string]string{}
		for _, v := range tmpl.Variables() {
			got[v.Name] = encodeVariable(v)
		}
		checked++
		if diff := diffVariables(ref.Variables, got); diff != "" {
			mismatched++
			if mismatched <= 10 {
				t.Errorf("%s: the two analyses disagree\n%s", c.Rel, diff)
			}
		}
	}
	if mismatched > 10 {
		t.Errorf("... and %d more disagreements", mismatched-10)
	}
	t.Logf("%d cases agree with the reference (%d have none, being unparseable)",
		checked-mismatched, noRef)
}

func encodeVariable(v gojja2.Variable) string {
	s := ""
	if v.Output {
		s += "o"
	}
	if v.Flow {
		s += "f"
	}
	if s == "" {
		s = "-"
	}
	if v.Unknown {
		s += "?"
	}
	return s
}

func diffVariables(want, got map[string]string) string {
	names := map[string]bool{}
	for k := range want {
		names[k] = true
	}
	for k := range got {
		names[k] = true
	}
	var keys []string
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

// compileCase builds the case's environment and compiles its template, which is
// what Variables needs and what the reference did on the other side.
func compileCase(c *conformance.Case) (*gojja2.Template, error) {
	env, err := c.Environment()
	if err != nil {
		return nil, err
	}
	return env.GetTemplate(c.Rel)
}
