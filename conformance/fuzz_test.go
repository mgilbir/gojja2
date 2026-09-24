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

// harness holds the live oracle and the context every generated case renders
// against.
//
// The context is kept as JSON and decoded per render rather than decoded once
// and shared, because a render can *mutate* what it is given: `lst.append(9)`,
// `d.update(...)` and `{% set d.v %}...{% endset %}` all write through to the
// caller's value, and [gojja2.Template.RenderValues] skips the conversion that
// would otherwise protect it. Sharing one decoded context let a template poison
// every comparison after it -- a template with no `{% set %}` in it at all
// reported a divergence because an earlier one had added a key to `d`. The
// oracle does json.loads per request, so this is also what makes the two sides
// start from the same place.
type harness struct {
	oracle    *conformance.Oracle
	rawCtx    json.RawMessage
	templates map[string]string
	// py is the interpreter both sides reproduce. GOJJA2_FUZZ_PYTHON moves
	// it, and moves *both* sides together: the oracle runs under that
	// CPython and gojja2 is configured to reproduce it. Setting one without
	// the other would compare two specifications and call the difference a
	// bug.
	py value.PythonVersion
}

// context decodes a fresh copy of the shared context. See the type comment.
func (h *harness) context() (map[string]value.Value, error) {
	return conformance.DecodeContext(h.rawCtx)
}

// newHarness starts the oracle, or skips when there is none to ask.
func newHarness(t testing.TB) *harness {
	t.Helper()
	version := os.Getenv("GOJJA2_FUZZ_PYTHON")
	py, err := conformance.PythonVersionFor(version)
	if err != nil {
		t.Fatalf("GOJJA2_FUZZ_PYTHON: %v", err)
	}
	oracle, err := conformance.StartOracleFor(version)
	if err != nil {
		t.Skipf("%v", err)
	}
	t.Cleanup(func() { _ = oracle.Close() })

	raw, err := conformance.FuzzContext()
	if err != nil {
		t.Fatalf("fuzz context: %v", err)
	}
	if _, err := conformance.DecodeContext(raw); err != nil {
		t.Fatalf("decode fuzz context: %v", err)
	}
	return &harness{
		oracle:    oracle,
		rawCtx:    raw,
		templates: conformance.FuzzTemplates(),
		py:        py,
	}
}

// caseOptions is the environment a generated case renders under, on gojja2's
// side. The oracle is handed the same settings; they have to be applied to both
// or the comparison is between two environments rather than two engines.
func caseOptions(c conformance.GeneratedCase) []gojja2.Option {
	opts := []gojja2.Option{
		gojja2.WithAutoescape(c.Autoescape),
		gojja2.WithTrimBlocks(c.Trim),
		gojja2.WithLstripBlocks(c.Lstrip),
		gojja2.WithKeepTrailingNewline(c.KeepTrailingNewline),
	}
	switch c.Undefined {
	case "strict":
		opts = append(opts, gojja2.WithUndefined(value.UndefinedStrict))
	case "chainable":
		opts = append(opts, gojja2.WithUndefined(value.UndefinedChainable))
	case "debug":
		opts = append(opts, gojja2.WithUndefined(value.UndefinedDebug))
	}
	return opts
}

// caseSettings is caseOptions for the other side: the same environment, in the
// oracle's vocabulary.
//
// One function rather than one per harness. There were two, built by hand a
// hundred lines apart, and they had already drifted -- the syntax soak passed
// autoescape and not the Undefined class, so a whole axis reached one engine
// and not the other. A setting added to caseOptions and forgotten here is a
// comparison between two environments rather than between two engines, and it
// looks exactly like a divergence.
func caseSettings(c conformance.GeneratedCase) map[string]any {
	settings := map[string]any{}
	if c.Autoescape {
		settings["autoescape"] = true
	}
	if c.Undefined != "" {
		settings["undefined"] = c.Undefined
	}
	if c.Trim {
		settings["trim_blocks"] = true
	}
	if c.Lstrip {
		settings["lstrip_blocks"] = true
	}
	if c.KeepTrailingNewline {
		settings["keep_trailing_newline"] = true
	}
	if len(settings) == 0 {
		return nil
	}
	return settings
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

	env := mustEnv(append(caseOptions(c),
		gojja2.WithLoader(gojja2.DictLoader(sources)),
		gojja2.WithPythonVersion(h.py))...)
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
	vars, err := h.context()
	if err != nil {
		return "", "", err
	}
	err = tmpl.RenderValues(ctx, &buf, vars)
	return buf.String(), "", err
}

// check compares one template, returning nil when the two agree or when the
// case cannot be graded.
func (h *harness) check(t testing.TB, c conformance.GeneratedCase) *conformance.Divergence {
	want, err := h.oracle.Render(conformance.OracleRequest{
		Name:      fuzzTemplateName,
		Source:    c.Source,
		Context:   h.rawCtx,
		Settings:  caseSettings(c),
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
	// Every setting the case renders under has to be in the report, or the
	// template alone does not reproduce it.
	env := ""
	if c.Autoescape {
		env = ", autoescape"
	}
	if c.Undefined != "" {
		env += ", " + c.Undefined + " undefined"
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
	undefinedRuns := map[string]int{}
	lexRuns := map[string]int{}
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
		if c.Undefined != "" {
			undefinedRuns[c.Undefined]++
		}
		countLexSettings(c, lexRuns)

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
	// The per-setting counts are reported because a run that silently stopped
	// varying them would otherwise look exactly like a clean one: the axis
	// was added after sixty thousand templates a run had all used the
	// default Undefined without anything saying so.
	t.Logf("differential: %d templates checked against CPython jinja2 (seed %d), "+
		"%d autoescaping, %d empty; undefined %d strict, %d chainable, %d debug; "+
		"lexer %d trim, %d lstrip, %d keep-newline",
		checked, seed, escaping, skipped,
		undefinedRuns["strict"], undefinedRuns["chainable"], undefinedRuns["debug"],
		lexRuns["trim"], lexRuns["lstrip"], lexRuns["keep"])
}

// countLexSettings tallies the lexer axis for the summary line, which is the
// only thing that would say an axis had stopped varying.
func countLexSettings(c conformance.GeneratedCase, into map[string]int) {
	if c.Trim {
		into["trim"]++
	}
	if c.Lstrip {
		into["lstrip"]++
	}
	if c.KeepTrailingNewline {
		into["keep"]++
	}
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
