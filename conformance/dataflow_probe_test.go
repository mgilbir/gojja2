// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package conformance_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mgilbir/gojja2/conformance"
	"github.com/mgilbir/gojja2/dataflow"
	"github.com/mgilbir/gojja2/syntax"
	"github.com/mgilbir/gojja2/value"
)

// The analysis is graded against a second implementation, which cannot catch
// the two being wrong the same way. This can: it asks the engine.
//
// Where the analysis says a variable cannot affect the output, render the
// template twice with two very different values for it and require the result
// to be identical. That is not a second opinion, it is the thing itself --
// whether the variable can change what comes out. A negative is the strongest
// claim this analysis makes and the only one that can be checked directly, so it
// is the one worth checking.
func TestNegativesSurviveRendering(t *testing.T) {
	root := repoRoot(t)
	paths, err := conformance.Collect(root + "/testdata/corpus")
	if err != nil {
		t.Fatalf("collect: %v", err)
	}

	// Two values with nothing in common: a different type, a different
	// length, a different truthiness. If the variable matters at all, one of
	// those differences should show.
	probes := []value.Value{
		value.String("gojja2-probe-alpha"),
		value.Int(0),
		value.None,
		value.NewList(value.Int(1)),
	}
	// A string no template in the corpus contains, so finding it in the
	// output means it came from the variable and nowhere else.
	const markerText = "zqxjmarkerzqxj"
	marker := value.String(markerText)

	var checked, claimed int
	for _, path := range paths {
		c, err := conformance.LoadCase(root+"/testdata/corpus", path)
		if err != nil {
			t.Fatalf("load %s: %v", path, err)
		}
		env, err := c.Environment()
		if err != nil {
			continue
		}
		tmpl, err := env.GetTemplate(c.Rel)
		if err != nil {
			continue
		}
		tree := tmpl.Syntax()
		flow := dataflow.Analyze(tree, dataflow.WithResolver(func(n string) *syntax.Tree {
			other, err := env.GetTemplate(n)
			if err != nil {
				return nil
			}
			return other.Syntax()
		}))

		render := func(name string, probe value.Value) (string, bool) {
			// Fresh per render. A shallow copy would share the values
			// themselves, so a case that mutates one -- a dict written
			// by `{% set d.v %}...{% endset %}`, say -- would change
			// what the next probe sees. See [conformance.Case.Context].
			vars, err := c.Context()
			if err != nil {
				return "\x00context: " + err.Error(), false
			}
			vars[name] = probe
			var sb strings.Builder
			if err := tmpl.RenderValues(context.Background(), &sb, vars); err != nil {
				return "\x00error: " + err.Error(), false
			}
			return sb.String(), true
		}

		for name, e := range flow.Context(tree) {
			if e&dataflow.Opaque != 0 {
				continue // it claims nothing to check
			}

			// "Its value cannot appear in the output." A marker nothing
			// else in the template could produce is put in, and the
			// output must not contain it.
			if e&dataflow.Printed == 0 {
				claimed++
				if out, ok := render(name, marker); ok {
					checked++
					if strings.Contains(out, markerText) {
						t.Errorf("%s: the analysis says %q is never printed, "+
							"but its value is in the output:\n  %q",
							c.Rel, name, out)
					}
				}
			}

			// "The render cannot fail because of it." Whatever is
			// passed, a render that succeeded must still succeed and
			// one that failed must still fail.
			if e&dataflow.Required == 0 {
				claimed++
				first, firstOK := render(name, probes[0])
				same := true
				for _, probe := range probes[1:] {
					_, ok := render(name, probe)
					if ok != firstOK {
						same = false
					}
				}
				checked++
				if !same {
					t.Errorf("%s: the analysis says the render cannot fail "+
						"because of %q, but changing it changes whether it "+
						"does\n  with %v: ok=%v %q", c.Rel, name, probes[0],
						firstOK, first)
				}
			}

			// "It cannot change the output at all." Two values with
			// nothing in common must render the same thing.
			if e != 0 {
				continue
			}
			claimed++
			a, aok := render(name, probes[0])
			b, bok := render(name, probes[1])
			if !aok && !bok && a != b {
				// Both failed, differently: the variable decided how.
				t.Errorf("%s: the analysis says %q cannot affect the output, "+
					"but it changes how the render fails\n  %s\n  %s", c.Rel, name, a, b)
				continue
			}
			checked++
			if a != b {
				t.Errorf("%s: the analysis says %q cannot affect the output, "+
					"but it does\n  with %v: %q\n  with %v: %q",
					c.Rel, name, probes[0], a, probes[1], b)
			}
		}
	}
	t.Logf("%d claims checked by rendering, of %d made", checked, claimed)
	if claimed == 0 {
		t.Error("no such claims were made, so this checked nothing")
	}
}
