// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/gojja2"
	"github.com/mgilbir/gojja2/errs"
)

// The fuzzing this repository already had is differential: it generates a
// template, renders it here and in CPython jinja2, and compares. That is the
// sharpest tool for conformance and it is useless in CI, because it needs a
// Python interpreter with jinja2 installed and CI has neither. So the fuzzer
// that could run on every commit was the one nobody was running.
//
// These targets need nothing but Go. They cannot say what a template *means* --
// only CPython can say that -- but they can say what the engine owes every
// input regardless of meaning: do not panic, do not run past the deadline, do
// not write past the bound, and do not hand back an error the caller cannot
// classify. Every defect of that kind found in this engine so far would have
// been caught here.

// fuzzSeeds is every template in the conformance corpus.
//
// They are seeds rather than cases: 846 templates that already reach every
// corner of the grammar, which a mutation can then take somewhere new. Starting
// from nothing, a fuzzer spends its first hours rediscovering that `{%` begins a
// tag.
func fuzzSeeds(f *testing.F) {
	f.Helper()
	root := filepath.Join("testdata", "corpus")
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		// A case is its context, then a separator, then the template.
		// Only the template is of interest here; the context is fixed
		// below so that a mutation changes the template and not the
		// data it is rendered against.
		if _, src, ok := strings.Cut(string(body), "\n---\n"); ok {
			f.Add(src)
		}
		return nil
	})
	if err != nil {
		f.Fatalf("reading seeds: %v", err)
	}
}

// fuzzVars is what every fuzzed template renders against.
//
// Fixed, and varied enough that a template reaching for a name usually finds
// something: a fuzzer that only ever sees undefined never gets past the first
// attribute lookup.
func fuzzVars() map[string]any {
	return map[string]any{
		"s": "hello", "n": 42, "f": 1.5, "b": true, "z": nil,
		"list":  []any{1, 2, 3},
		"dict":  map[string]any{"a": 1, "b": 2},
		"users": []any{map[string]any{"name": "ada", "age": 36}},
		"html":  "<b>&amp;</b>",
	}
}

// The bounds each fuzzed render runs under. They are tight so that a template
// which tries to run away is refused quickly rather than eating the fuzzer's
// time budget, and so that the bound is reached often enough to be tested.
const (
	fuzzMaxOutput     = 1 << 20
	fuzzMaxIterations = 200_000
	fuzzDeadline      = 100 * time.Millisecond
	// A render must return within this. It is far above the deadline,
	// because a single step is allowed to overrun it and a loaded builder
	// running several workers is allowed to be slow; it is far below what a
	// walk that never yields costs, which is seconds and upwards.
	//
	// It was five seconds first, and the first real defect this target found
	// took 4.7 -- so it passed the check and killed the worker instead,
	// which the fuzzer reports as "hung or terminated unexpectedly" with no
	// indication of which property failed. A bound the assertion can catch
	// is worth more than a generous one.
	fuzzWallClock = 3 * time.Second
)

func fuzzEnv() *gojja2.Environment {
	return mustEnv(
		gojja2.WithMaxOutputBytes(fuzzMaxOutput),
		gojja2.WithMaxIterations(fuzzMaxIterations),
		gojja2.WithLoader(gojja2.DictLoader(map[string]string{
			"inner":  `[{% block b %}inner{% endblock %}]`,
			"parent": `{% block b %}parent{% endblock %}`,
		})),
	)
}

// FuzzRender is the whole pipeline: compile a template, then render it under a
// deadline and a budget.
func FuzzRender(f *testing.F) {
	fuzzSeeds(f)
	f.Fuzz(func(t *testing.T, src string) {
		// A template long enough to be slow to compile says nothing
		// about correctness, and the corpus seeds are far below this.
		if len(src) > 64<<10 {
			t.Skip("oversized input")
		}
		env := fuzzEnv()

		tmpl, err := env.FromString(src)
		if err != nil {
			requireClassifiable(t, "compile", src, err)
			// Compiling is deterministic: the same source cannot
			// fail once and succeed the next time, which is the
			// shape a cache keyed on the wrong thing takes.
			if _, again := fuzzEnv().FromString(src); again == nil {
				t.Fatalf("compiling %q failed once and succeeded once:\n  %v",
					src, err)
			}
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), fuzzDeadline)
		defer cancel()
		start := time.Now()
		out, err := tmpl.RenderString(ctx, fuzzVars())
		took := time.Since(start)

		if took > fuzzWallClock {
			t.Fatalf("rendering %q took %s against a %s deadline; the "+
				"render is not yielding while it works",
				src, took.Round(time.Millisecond), fuzzDeadline)
		}
		if err != nil {
			requireClassifiable(t, "render", src, err)
			// RenderString is all or nothing: a caller that gets an
			// error must not also get half a document, because it
			// has no way to tell that half from the whole.
			if out != "" {
				t.Fatalf("rendering %q failed with %v and still returned %d bytes",
					src, err, len(out))
			}
			return
		}
		if int64(len(out)) > fuzzMaxOutput {
			t.Fatalf("rendering %q returned %d bytes with no error, past the "+
				"%d byte bound", src, len(out), fuzzMaxOutput)
		}

		// The same template over the same data renders the same
		// document. Go maps have no iteration order, and this engine
		// sorts their keys rather than exposing one -- so a render that
		// differed between runs would mean an unsorted map had reached
		// the output, which is the kind of thing that is reproducible
		// once in fifty runs and never in a test.
		if !mayVary(src) && !hasAddress(out) {
			ctx2, cancel2 := context.WithTimeout(context.Background(), fuzzDeadline)
			defer cancel2()
			again, err2 := tmpl.RenderString(ctx2, fuzzVars())
			if err2 == nil && !hasAddress(again) && again != out {
				t.Fatalf("rendering %q twice gave different documents:\n  %q\n  %q",
					src, out, again)
			}
		}
	})
}

// mayVary reports a template that is allowed to render differently each time.
//
// A name is enough to excuse it: matching too much only skips a check, while
// matching too little fails a build over a template that was never meant to be
// reproducible. `random` and `shuffle` say so, `lipsum` generates text, and
// `now` and `range(...)|random` reach the same places by other names.
func mayVary(src string) bool {
	for _, name := range []string{"random", "shuffle", "lipsum", "now"} {
		if strings.Contains(src, name) {
			return true
		}
	}
	return false
}

// hasAddress reports output carrying an object's address.
//
// Printing a thing with no repr of its own gives CPython's default, which
// contains the object's id: `<jinja2.utils.Cycler object at 0x7f...>`. That is
// conformant and it is different on every render by construction. The
// differential harness excludes the same outputs for the same reason -- there
// is nothing there to compare -- and this is the first thing the determinism
// check found.
func hasAddress(out string) bool { return strings.Contains(out, " object at 0x") }

// FuzzParse is compilation alone, which is where a template's *shape* is
// decided and where a parser is most likely to be surprised.
//
// It is separate from FuzzRender because it is perhaps fifty times faster per
// input, so the coverage-guided search reaches far deeper into the grammar for
// the same fuzzing budget -- and a parser that loops or overflows its stack is
// a defect whether or not anything is ever rendered.
func FuzzParse(f *testing.F) {
	fuzzSeeds(f)
	f.Fuzz(func(t *testing.T, src string) {
		if len(src) > 64<<10 {
			t.Skip("oversized input")
		}
		start := time.Now()
		_, err := fuzzEnv().FromString(src)
		if took := time.Since(start); took > fuzzWallClock {
			t.Fatalf("compiling %q took %s", src, took.Round(time.Millisecond))
		}
		if err != nil {
			requireClassifiable(t, "compile", src, err)
		}
	})
}

// requireClassifiable insists that a failure is one a caller can act on.
//
// Every error out of this package is supposed to carry a Kind, which is the
// jinja2 exception class a Python caller would catch and the only thing a Go
// caller can branch on. An error that arrives without one is an internal
// failure that escaped: it reads as a bare sentence, it cannot be matched, and
// it is a bug however reasonable the sentence sounds.
func requireClassifiable(t *testing.T, stage, src string, err error) {
	t.Helper()
	var e *errs.Error
	if !errors.As(err, &e) {
		// A budget refusal wraps the context's error and is classified
		// through it, so those are accounted for too.
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) ||
			errors.Is(err, gojja2.ErrTooManyIterations) || errors.Is(err, gojja2.ErrOutputTooLarge) {
			return
		}
		t.Fatalf("%s of %q failed with an unclassifiable error:\n  %T: %v",
			stage, src, err, err)
	}
	// TemplateNotFound is exempt, and the exemption is CPython's rather than
	// a concession: jinja2 defines it as `message = name if message is
	// None`, so `{% include "" %}` raises an exception whose str() is the
	// empty string. Checked against CPython rather than assumed -- it is
	// the first thing this target found, and the engine was right.
	if e.Msg == "" && e.Kind != errs.TemplateNotFound {
		t.Fatalf("%s of %q failed with an empty %v message", stage, src, e.Kind)
	}
}
