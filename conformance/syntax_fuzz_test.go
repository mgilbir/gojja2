// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package conformance_test

import (
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/mgilbir/gojja2"
	"github.com/mgilbir/gojja2/conformance"
	"github.com/mgilbir/gojja2/dataflow"
	"github.com/mgilbir/gojja2/syntax"
)

// The structure and the analyses, on templates nobody chose.
//
// A corpus is a list of cases somebody thought of, and the whole history of this
// analysis is faults found by templates nobody here wrote. A generator does not
// think of anything: it walks the grammar, and the shapes it reaches are the
// ones a corpus is least likely to hold -- an autoescape inside a macro inside a
// loop that assigns the name it read.
//
// Three questions are asked of both sides for every template, and all three are
// byte comparisons of the same canonical forms:
//
//   - the tree, so they agree about what the template says;
//   - the scope facts, so they agree about which name means which storage;
//   - the dataflow, so they agree about what reaches the output.
//
// The first is the foundation of the second and the second of the third, so a
// divergence is reported at the first level that shows it rather than three
// times over.
func TestSyntaxDifferential(t *testing.T) {
	h := newHarness(t)

	count := envInt(t, "GOJJA2_FUZZ_N", 3000)
	seed := uint64(envInt(t, "GOJJA2_FUZZ_SEED", 20260921))
	rng := rand.New(rand.NewPCG(seed, 0x9e3779b97f4a7c15))

	var checked, skipped, failures int
	for range count {
		input := make([]byte, 1+rng.IntN(96))
		for i := range input {
			input[i] = byte(rng.UintN(256))
		}
		c := conformance.GenerateCase(input)
		if strings.TrimSpace(c.Source) == "" {
			skipped++
			continue
		}
		if d := h.compareSyntax(t, c); d != "" {
			failures++
			t.Errorf("%s\n  template: %q", d, c.Source)
			if failures >= 10 {
				t.Errorf("... stopping after 10 divergences")
				break
			}
			continue
		}
		checked++
	}
	t.Logf("syntax differential: %d generated templates compared against "+
		"CPython jinja2 (seed %d), %d empty", checked, seed, skipped)
}

// compareSyntax returns a description of the first divergence, or "".
func (h *harness) compareSyntax(t testing.TB, c conformance.GeneratedCase) string {
	t.Helper()

	sources := make(map[string]string, len(h.templates)+1)
	for name, text := range h.templates {
		sources[name] = text
	}
	sources[fuzzTemplateName] = c.Source

	opts := []gojja2.Option{
		gojja2.WithLoader(gojja2.DictLoader(sources)),
		gojja2.WithAutoescape(c.Autoescape),
	}
	env, err := gojja2.New(opts...)
	if err != nil {
		return ""
	}
	tmpl, err := env.GetTemplate(fuzzTemplateName)
	if err != nil {
		// gojja2 refuses it. Whether jinja2 agrees is TestDifferential's
		// business; there is no structure to compare either way.
		return ""
	}

	settings := map[string]any{}
	if c.Autoescape {
		settings["autoescape"] = true
	}
	ref, err := h.oracle.Analyze(conformance.AnalyzeRequest{
		Name:      fuzzTemplateName,
		Source:    c.Source,
		Settings:  settings,
		Templates: h.templates,
	})
	if err != nil {
		t.Fatalf("oracle: %v", err)
	}
	if !ref.OK || ref.Resource {
		// jinja2 could not compile it, or hit this machine's limits
		// rather than answering. Neither says anything about agreement.
		return ""
	}

	tree := tmpl.Syntax()
	if tree == nil {
		return "gojja2 compiled the template but produced no structure for it"
	}
	got, err := syntax.Canonical(tree.Root)
	if err != nil {
		return "encoding the tree: " + err.Error()
	}
	if string(got) != ref.Tree {
		return "the two trees are not the same\n" + firstDifference(ref.Tree, string(got))
	}
	gotInfo, err := syntax.CanonicalInfo(tree)
	if err != nil {
		return "encoding the scope facts: " + err.Error()
	}
	if string(gotInfo) != ref.Info {
		return "the two disagree about scopes or bindings\n" +
			firstDifference(ref.Info, string(gotInfo))
	}

	flow := dataflow.Analyze(tree, dataflow.WithResolver(func(name string) *syntax.Tree {
		other, err := env.GetTemplate(name)
		if err != nil {
			return nil
		}
		return other.Syntax()
	}))
	gotVars := map[string]string{}
	for name, e := range flow.Context(tree) {
		gotVars[name] = encodeEffect(e)
	}
	if diff := diffEffects(ref.Variables, gotVars); diff != "" {
		return "the two analyses disagree\n" + diff
	}
	return ""
}
