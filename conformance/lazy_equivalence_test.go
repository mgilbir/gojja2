// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package conformance_test

import (
	"context"
	"errors"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/gojja2"
	"github.com/mgilbir/gojja2/conformance"
	"github.com/mgilbir/gojja2/value"
)

// TestLazyConversionMatchesEager renders generated templates both ways a Go
// context can reach a template: through Template.Render, which converts each
// record lazily (value.FromGoLazy), and through RenderValues over value.FromGo,
// which converts everything up front. The two must agree on output and error.
//
// TestDifferential cannot see this: it hands gojja2 a context that is already
// a value.Value, so no Go conversion happens on its side at all. Nor can
// CPython -- the question is whether two of this engine's paths agree, and the
// generator is what makes the templates read, iterate, compare and mutate the
// records in every order. No oracle is needed, so this runs in ordinary `go
// test`; GOJJA2_LAZY_N and GOJJA2_FUZZ_SEED set the count and seed for a soak.
//
// Both sides get the same native Go data -- the fuzz context through
// value.ToGo, decoded afresh for each render -- so the sorted key order of a Go
// map is the same on both, and a template that mutates its context mutates its
// own copy. A case that one side cannot finish, because of a deadline or a
// limit, is counted and not compared: the lazy side charges the conversion to
// the render and RenderValues has no conversion to charge.
func TestLazyConversionMatchesEager(t *testing.T) {
	raw, err := conformance.FuzzContext()
	if err != nil {
		t.Fatalf("fuzz context: %v", err)
	}
	h := &harness{rawCtx: raw, templates: conformance.FuzzTemplates()}
	count := envInt(t, "GOJJA2_LAZY_N", 2000)
	seed := uint64(envInt(t, "GOJJA2_FUZZ_SEED", 20261009))
	rng := rand.New(rand.NewPCG(seed, 0x9e3779b97f4a7c15))

	goData := func(c conformance.GeneratedCase) map[string]any {
		_, vars, err := h.contextFor(c)
		if err != nil {
			t.Fatalf("context: %v", err)
		}
		out := make(map[string]any, len(vars))
		for k, v := range vars {
			out[k] = value.ToGo(v)
		}
		return out
	}
	render := func(c conformance.GeneratedCase, lazy bool) (string, error) {
		env := mustEnv(append(caseOptions(c),
			gojja2.WithLoader(gojja2.DictLoader(h.sourcesFor(c))))...)
		tmpl, err := env.GetTemplate(fuzzTemplateName)
		if err != nil {
			return "", err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var buf strings.Builder
		if lazy {
			err = tmpl.Render(ctx, &buf, goData(c))
		} else {
			vars := map[string]value.Value{}
			for k, v := range goData(c) {
				vars[k] = value.FromGo(v)
			}
			err = tmpl.RenderValues(ctx, &buf, vars)
		}
		return buf.String(), err
	}
	bounded := func(err error) bool {
		return errors.Is(err, context.DeadlineExceeded) ||
			errors.Is(err, gojja2.ErrTooManyIterations) ||
			errors.Is(err, gojja2.ErrOutputTooLarge)
	}
	errText := func(err error) string {
		if err == nil {
			return "<nil>"
		}
		return err.Error()
	}

	var compared, skipped, unbounded, failures int
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
		lazyOut, lazyErr := render(c, true)
		eagerOut, eagerErr := render(c, false)
		if bounded(lazyErr) || bounded(eagerErr) {
			unbounded++
			continue
		}
		compared++
		if lazyOut == eagerOut && errText(lazyErr) == errText(eagerErr) {
			continue
		}
		failures++
		t.Errorf("lazy and eager conversion disagree on\n%s\n  lazy:  %q, %s\n  eager: %q, %s",
			c.Source, lazyOut, errText(lazyErr), eagerOut, errText(eagerErr))
		if failures >= 10 {
			t.Fatalf("stopping after 10 disagreements")
		}
	}
	t.Logf("lazy conversion: %d templates compared (seed %d), %d empty, %d stopped by a bound",
		compared, seed, skipped, unbounded)
	if compared < count/2 {
		t.Errorf("only %d of %d generated templates were compared", compared, count)
	}

	// The generator above seldom reads a record: of two thousand templates,
	// ninety-odd mention a user's field at all, and most of those on a
	// number rather than on `users`. What laziness changes is the *order*
	// in which a record is read, filled, mutated and compared, so the
	// second half draws those operations and composes them in random order
	// -- a read before the fill and after it, a mutation through one
	// reference and a read through another.
	var composed int
	for range count {
		n := 1 + rng.IntN(8)
		var src strings.Builder
		for range n {
			src.WriteString(recordOps[rng.IntN(len(recordOps))])
			src.WriteByte('|')
		}
		c := conformance.GeneratedCase{Source: src.String()}
		lazyOut, lazyErr := render(c, true)
		eagerOut, eagerErr := render(c, false)
		if bounded(lazyErr) || bounded(eagerErr) {
			unbounded++
			continue
		}
		composed++
		if lazyOut == eagerOut && errText(lazyErr) == errText(eagerErr) {
			continue
		}
		failures++
		t.Errorf("lazy and eager conversion disagree on\n%s\n  lazy:  %q, %s\n  eager: %q, %s",
			c.Source, lazyOut, errText(lazyErr), eagerOut, errText(eagerErr))
		if failures >= 10 {
			t.Fatalf("stopping after 10 disagreements")
		}
	}
	t.Logf("lazy conversion: %d composed record templates compared", composed)
}

// recordOps are what a template does with the records in the fuzz context:
// users is a list of three dicts, d a three-key dict, nested a dict of a dict
// of a list, ed an empty dict.
var recordOps = []string{
	`{{ users[0].name }}`,
	`{{ users[1]['city'] }}`,
	`{{ users[2].age + 1 }}`,
	`{{ users[0].nope }}`,
	`{{ users[0].get('nope', 'dflt') }}`,
	`{{ 'name' in users[0] }}`,
	`{{ users[0]|length }}`,
	`{{ users|map(attribute='name')|join(',') }}`,
	`{{ users|selectattr('age', 'eq', 30)|list|length }}`,
	`{{ users|sort(attribute='age')|map(attribute='name')|list }}`,
	`{{ users|groupby('city')|list }}`,
	`{{ users|map(attribute='age')|sum }}`,
	`{{ users[0]|dictsort }}`,
	`{{ users[0].items()|list }}`,
	`{{ users[1].keys()|list }}`,
	`{% for k, v in users[2]|dictsort %}{{ k }}={{ v }};{% endfor %}`,
	`{{ users[0] == users[2] }}`,
	`{{ users[0] is sameas users[0] }}`,
	`{{ users[0]|tojson }}`,
	`{{ users }}`,
	`{{ users[0].copy() }}`,
	`{% set _ = users[0].update({'name': 'zed'}) %}`,
	`{% set _ = users[1].pop('city', none) %}`,
	`{% set u = users[2] %}{% set _ = u.clear() %}{{ users[2] }}`,
	`{% set _ = users[0].setdefault('extra', [1]) %}{{ users[0].extra }}`,
	`{{ d.a }}{{ d['C'] }}`,
	`{{ d|dictsort }}`,
	`{{ d.keys()|list }}`,
	`{{ d.values()|list }}`,
	`{% set _ = d.setdefault('z', 9) %}`,
	`{% set _ = d.popitem() %}`,
	`{{ nested.x.y }}`,
	`{{ nested.x }}`,
	`{% set _ = nested.x.y.append(5) %}`,
	`{{ nested }}`,
	`{{ nested.x is sameas nested['x'] }}`,
	`{{ ed|length }}{{ ed }}`,
	`{% set _ = ed.update(d) %}{{ ed }}`,
}
