// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package conformance_test

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/gojja2"
	"github.com/mgilbir/gojja2/conformance"
	"github.com/mgilbir/gojja2/dataflow"
	"github.com/mgilbir/gojja2/syntax"
	"github.com/mgilbir/gojja2/value"
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

	var checked, skipped, failures, claims int
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
		if d := h.compareSyntax(t, c, &claims); d != "" {
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
		"CPython jinja2 (seed %d), %d empty; %d of the analysis's negatives "+
		"checked by rendering", checked, seed, skipped, claims)
}

// compareSyntax returns a description of the first divergence, or "".
func (h *harness) compareSyntax(t testing.TB, c conformance.GeneratedCase, claims *int) string {
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
	return h.verifyNegatives(tmpl, flow.Context(tree), claims)
}

// Probe values with nothing in common: a different type, a different length, a
// different truthiness. A marker string no generated template can contain, so
// finding it in the output means it came from the variable.
const fuzzMarker = "zqxjmarkerzqxj"

var fuzzProbes = []value.Value{
	value.String(fuzzMarker), value.Int(0), value.None,
}

// verifyNegatives asks the engine whether the analysis told the truth.
//
// The differential above proves the two implementations agree, and they are
// both written here, so agreement is close to a statement about transcription.
// This is the part that is not: where the analysis says a variable's value
// cannot be printed, the engine is handed a marker and the output must not
// contain it; where it says a variable cannot break the render, two values with
// nothing in common must not change whether it does.
//
// A generated template is free to ask for a billion iterations, so every render
// gets the same deadline the differential gives them and a render that runs out
// is not evidence either way.
func (h *harness) verifyNegatives(tmpl *gojja2.Template, effects map[string]dataflow.Effect,
	claims *int) string {
	render := func(name string, probe value.Value) (string, bool, bool) {
		vars := make(map[string]value.Value, len(h.context)+1)
		for k, v := range h.context {
			vars[k] = v
		}
		vars[name] = probe
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var sb strings.Builder
		err := tmpl.RenderValues(ctx, &sb, vars)
		if err != nil && ctx.Err() != nil {
			return "", false, false // ran out of time; says nothing
		}
		return sb.String(), err == nil, true
	}

	for name, e := range effects {
		if e&dataflow.Opaque != 0 {
			continue
		}
		if e&dataflow.Printed == 0 {
			out, _, usable := render(name, fuzzProbes[0])
			if usable {
				*claims++
				if strings.Contains(out, fuzzMarker) {
					return fmt.Sprintf("the analysis says %q is never printed, "+
						"but its value is in the output:\n  %q", name, out)
				}
			}
		}
		if e&dataflow.Required == 0 {
			_, first, usable := render(name, fuzzProbes[0])
			if !usable {
				continue
			}
			for _, probe := range fuzzProbes[1:] {
				_, ok, usable := render(name, probe)
				if !usable {
					continue
				}
				*claims++
				if ok != first {
					return fmt.Sprintf("the analysis says the render cannot fail "+
						"because of %q, but changing it changes whether it does",
						name)
				}
			}
		}
	}
	return ""
}

// Two templates that encode the same must render the same.
//
// Comparing gojja2's tree to jinja2's cannot catch a field neither side writes
// down. This can: if two different sources produce identical canonical bytes and
// then render differently, the vocabulary dropped whatever distinguishes them,
// and every query built on it is answering about a template it cannot see the
// whole of.
//
// Templates that differ only in whitespace control or a comment encode the same
// and render the same, which is the point rather than a problem: the vocabulary
// is meant to describe what a template means.
func TestEncodingTheSameMeansRenderingTheSame(t *testing.T) {
	h := newHarness(t)

	count := envInt(t, "GOJJA2_FUZZ_N", 4000)
	seed := uint64(envInt(t, "GOJJA2_FUZZ_SEED", 20260922))
	rng := rand.New(rand.NewPCG(seed, 0x9e3779b97f4a7c15))

	type seen struct{ source, output string }
	byTree := make(map[string]seen, count)

	var compared, collisions int
	for range count {
		input := make([]byte, 1+rng.IntN(96))
		for i := range input {
			input[i] = byte(rng.UintN(256))
		}
		c := conformance.GenerateCase(input)
		if strings.TrimSpace(c.Source) == "" {
			continue
		}
		out, panicked, err := h.renderGojja2(c)
		if panicked != "" {
			continue
		}
		if err != nil {
			out = "\x00error: " + err.Error()
		}

		sources := make(map[string]string, len(h.templates)+1)
		for name, text := range h.templates {
			sources[name] = text
		}
		sources[fuzzTemplateName] = c.Source
		env, err := gojja2.New(
			gojja2.WithLoader(gojja2.DictLoader(sources)),
			gojja2.WithAutoescape(c.Autoescape))
		if err != nil {
			continue
		}
		tmpl, err := env.GetTemplate(fuzzTemplateName)
		if err != nil {
			continue
		}
		raw, err := syntax.Canonical(tmpl.Syntax().Root)
		if err != nil {
			continue
		}
		// Autoescaping is the environment's, not the template's, so two
		// templates that encode the same under different settings are not a
		// collision.
		key := string(raw)
		if c.Autoescape {
			key = "escaped\x00" + key
		}

		prev, ok := byTree[key]
		if !ok {
			byTree[key] = seen{c.Source, out}
			continue
		}
		if prev.source == c.Source {
			continue
		}
		compared++
		if prev.output != out {
			collisions++
			if collisions <= 5 {
				t.Errorf("these encode identically and render differently, so "+
					"the vocabulary is missing what tells them apart:\n"+
					"  %q -> %q\n  %q -> %q", prev.source, prev.output,
					c.Source, out)
			}
		}
	}
	if collisions > 5 {
		t.Errorf("... and %d more", collisions-5)
	}
	t.Logf("injectivity: %d distinct templates encoded (seed %d); %d pairs shared "+
		"an encoding and were compared by rendering", len(byTree), seed, compared)
}
