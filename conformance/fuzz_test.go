// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package conformance_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/gojja2"
	"github.com/mgilbir/gojja2/conformance"
	"github.com/mgilbir/gojja2/value"
)

// The differential runner: generate a template, render it with CPython jinja2
// and with gojja2, and require them to agree. Everything either side does --
// output, exception class, message, line -- is compared, so a divergence is a
// finding whether the template works or fails.

// harness holds the live oracle and the decoded shared context.
type harness struct {
	oracle    *conformance.Oracle
	context   map[string]value.Value
	rawCtx    json.RawMessage
	templates map[string]string
}

// newHarness starts the oracle, or skips when there is none to ask.
func newHarness(t testing.TB) *harness {
	t.Helper()
	oracle, err := conformance.StartOracle()
	if err != nil {
		t.Skipf("%v", err)
	}
	t.Cleanup(func() { _ = oracle.Close() })

	raw, err := conformance.FuzzContext()
	if err != nil {
		t.Fatalf("fuzz context: %v", err)
	}
	ctx, err := conformance.DecodeContext(raw)
	if err != nil {
		t.Fatalf("decode fuzz context: %v", err)
	}
	return &harness{
		oracle:    oracle,
		context:   ctx,
		rawCtx:    raw,
		templates: conformance.FuzzTemplates(),
	}
}

const fuzzTemplateName = "fuzz.txt"

// renderGojja2 renders with gojja2, turning a panic into a reportable result
// rather than taking the test process down mid-run.
func (h *harness) renderGojja2(c conformance.GeneratedCase) (out string, panicked string, err error) {
	sources := make(map[string]string, len(h.templates)+1)
	for name, text := range h.templates {
		sources[name] = text
	}
	sources[fuzzTemplateName] = c.Source

	defer func() {
		if r := recover(); r != nil {
			panicked = fmt.Sprintf("%v\n%s", r, debug.Stack())
		}
	}()

	env := mustEnv(
		gojja2.WithLoader(gojja2.DictLoader(sources)),
		gojja2.WithAutoescape(c.Autoescape),
	)
	tmpl, err := env.GetTemplate(fuzzTemplateName)
	if err != nil {
		return "", "", err
	}
	// The oracle runs each case under a five-second alarm, so gojja2 gets
	// the same deadline: a generated template is free to ask for a billion
	// iterations, and the two sides have to give up in the same way rather
	// than one of them hanging the fuzzer.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var buf strings.Builder
	err = tmpl.RenderValues(ctx, &buf, h.context)
	return buf.String(), "", err
}

// check compares one template, returning nil when the two agree or when the
// case cannot be graded.
func (h *harness) check(t testing.TB, c conformance.GeneratedCase) *conformance.Divergence {
	var settings map[string]any
	if c.Autoescape {
		settings = map[string]any{"autoescape": true}
	}
	want, err := h.oracle.Render(conformance.OracleRequest{
		Name:      fuzzTemplateName,
		Source:    c.Source,
		Context:   h.rawCtx,
		Settings:  settings,
		Templates: h.templates,
	})
	if err != nil {
		t.Fatalf("oracle: %v", err)
	}
	if !conformance.Comparable(want) {
		return nil
	}

	out, panicked, renderErr := h.renderGojja2(c)
	if panicked != "" {
		return &conformance.Divergence{Kind: conformance.KindPanic, Detail: panicked}
	}
	if conformance.ResourceError(renderErr) {
		// The generator is free to ask for a billion iterations. The
		// oracle's side of that is already discarded by Comparable;
		// this is the same discard for ours.
		return nil
	}
	return conformance.Compare(want.Expected(), out, renderErr)
}

// minimize shrinks a diverging template and re-reads the divergence from the
// reduced one.
//
// Reporting the original divergence beside the reduced template would be
// actively misleading: reduction only preserves the *kind*, so the detail --
// which outputs differed, which exception was raised -- has to be taken from
// the template that is actually printed.
func (h *harness) minimize(t testing.TB, c conformance.GeneratedCase, budget int) (conformance.GeneratedCase, *conformance.Divergence) {
	// Only the source shrinks: the environment is part of what diverged,
	// so changing it would reduce a different case.
	with := func(source string) conformance.GeneratedCase {
		return conformance.GeneratedCase{Source: source, Autoescape: c.Autoescape}
	}
	minimal := with(conformance.Shrink(c.Source, budget, func(candidate string) *conformance.Divergence {
		return h.check(t, with(candidate))
	}))
	d := h.check(t, minimal)
	if d == nil {
		// Reduction lost the divergence; report what was actually seen.
		return c, h.check(t, c)
	}
	return minimal, d
}

// report prints a divergence compactly: the kind, what each side did, and the
// minimised template. The shared context is a constant, so it is named rather
// than dumped -- a hundred lines of JSON per finding buries the finding.
func report(t testing.TB, c conformance.GeneratedCase, d *conformance.Divergence) {
	t.Helper()
	if d == nil {
		return
	}
	env := ""
	if c.Autoescape {
		env = ", autoescape"
	}
	t.Errorf("[%s] %s\n%s\n  (context: conformance.FuzzContextJSON%s)",
		d.Kind, strconv.Quote(c.Source), indent(d.Detail), env)
}

// FuzzTemplate is the coverage-guided target. Input bytes are the generator's
// decisions, so a mutation changes one grammar choice rather than corrupting
// a byte of template text.
func FuzzTemplate(f *testing.F) {
	h := newHarness(f)

	// Seeds steer the generator toward each area rather than leaving it to
	// find them by mutation.
	for _, seed := range [][]byte{
		{}, {1}, {2, 3}, {4, 5, 6},
		[]byte("filters"), []byte("inheritance"), []byte("loops and macros"),
		[]byte("\x00\x01\x02\x03\x04\x05\x06\x07"),
		[]byte("\xff\xfe\xfd\xfc\xfb\xfa"),
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > 512 {
			input = input[:512]
		}
		c := conformance.GenerateCase(input)
		d := h.check(t, c)
		if d == nil {
			return
		}
		min, minD := h.minimize(t, c, 300)
		report(t, min, minD)
	})
}

// TestDifferential runs the same comparison over a fixed number of seeded
// inputs, so ordinary `go test` gets differential coverage without anyone
// having to remember to start a fuzzer.
//
// GOJJA2_FUZZ_N and GOJJA2_FUZZ_SEED override the count and the seed, which is
// how a long soak is run: GOJJA2_FUZZ_N=200000 go test ./conformance/ -run Differential
func TestDifferential(t *testing.T) {
	h := newHarness(t)

	count := envInt(t, "GOJJA2_FUZZ_N", 3000)
	seed := uint64(envInt(t, "GOJJA2_FUZZ_SEED", 20260916))
	rng := rand.New(rand.NewPCG(seed, 0x9e3779b97f4a7c15))

	var checked, skipped, escaping int
	var failures int
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
		checked++
		if c.Autoescape {
			escaping++
		}

		d := h.check(t, c)
		if d == nil {
			continue
		}
		failures++
		if failures > 10 {
			t.Errorf("... stopping after 10 divergences")
			break
		}
		min, minD := h.minimize(t, c, 200)
		report(t, min, minD)
	}
	t.Logf("differential: %d templates checked against CPython jinja2 (seed %d), "+
		"%d autoescaping, %d empty", checked, seed, escaping, skipped)
}

func envInt(t testing.TB, name string, def int) int {
	t.Helper()
	text := os.Getenv(name)
	if text == "" {
		return def
	}
	n, err := strconv.Atoi(text)
	if err != nil {
		t.Fatalf("%s=%q: %v", name, text, err)
	}
	return n
}
