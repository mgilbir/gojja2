// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/mgilbir/gojja2/value"
)

// countingLoader records how often each name was read, so a test can tell a
// cache hit from a recompile.
type countingLoader struct {
	mu     sync.Mutex
	source map[string]string
	reads  map[string]int
}

func newCountingLoader() *countingLoader {
	return &countingLoader{source: map[string]string{}, reads: map[string]int{}}
}

func (l *countingLoader) Load(name string) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.reads[name]++
	src, ok := l.source[name]
	if !ok {
		return "", notFound(name)
	}
	return src, nil
}

func (l *countingLoader) set(name, src string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.source[name] = src
}

func (l *countingLoader) readCount(name string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.reads[name]
}

// TestCacheIsBounded pins that the cache evicts. It used to be an unbounded map
// with no eviction, so every name ever loaded was retained for the life of the
// process -- and with `{% include %}` over a name a template can influence, that
// is unbounded growth an attacker can drive.
func TestCacheIsBounded(t *testing.T) {
	loader := newCountingLoader()
	for i := range 100 {
		loader.set(fmt.Sprintf("t%d", i), "x")
	}
	env := New(WithLoader(loader), WithCacheSize(10))
	for i := range 100 {
		if _, err := env.GetTemplate(fmt.Sprintf("t%d", i)); err != nil {
			t.Fatalf("GetTemplate: %v", err)
		}
	}
	if n := env.cache.len(); n > 10 {
		t.Errorf("cache holds %d entries, limit is 10", n)
	}
}

// TestCacheEvictsLeastRecentlyUsed pins the eviction order. Template use is
// strongly skewed -- a handful of layouts on every request, a long tail rarely
// -- so evicting the hot ones would recompile them constantly.
func TestCacheEvictsLeastRecentlyUsed(t *testing.T) {
	loader := newCountingLoader()
	for _, n := range []string{"hot", "a", "b", "c"} {
		loader.set(n, "x")
	}
	env := New(WithLoader(loader), WithCacheSize(2))

	mustGet := func(name string) {
		t.Helper()
		if _, err := env.GetTemplate(name); err != nil {
			t.Fatalf("GetTemplate(%q): %v", name, err)
		}
	}

	mustGet("hot")
	mustGet("a")
	mustGet("hot") // promote: "a" is now least recently used
	mustGet("b")   // evicts "a"
	mustGet("hot") // still cached, so no second read

	if got := loader.readCount("hot"); got != 1 {
		t.Errorf("the hot template was read %d times; it should have stayed cached", got)
	}
	mustGet("a")
	if got := loader.readCount("a"); got != 2 {
		t.Errorf("the cold template was read %d times, want 2 (evicted then reloaded)", got)
	}
}

// TestClearCachePicksUpAnEditedTemplate pins the supported way to invalidate.
// There was none: a long-running process using FSLoader over a live directory
// could never observe an edit, short of discarding the whole Environment.
func TestClearCachePicksUpAnEditedTemplate(t *testing.T) {
	loader := newCountingLoader()
	loader.set("page", "before")
	env := New(WithLoader(loader))

	render := func() string {
		t.Helper()
		tmpl, err := env.GetTemplate("page")
		if err != nil {
			t.Fatalf("GetTemplate: %v", err)
		}
		out, err := tmpl.RenderString(context.Background(), nil)
		if err != nil {
			t.Fatalf("render: %v", err)
		}
		return out
	}

	if got := render(); got != "before" {
		t.Fatalf("got %q, want %q", got, "before")
	}
	loader.set("page", "after")
	if got := render(); got != "before" {
		t.Errorf("a cached template should not change under you, got %q", got)
	}
	env.ClearCache()
	if got := render(); got != "after" {
		t.Errorf("after ClearCache, got %q, want %q", got, "after")
	}

	// And one name at a time.
	loader.set("page", "third")
	env.ForgetTemplate("page")
	if got := render(); got != "third" {
		t.Errorf("after ForgetTemplate, got %q, want %q", got, "third")
	}
}

// TestCacheIsConcurrencySafe drives the cache from many goroutines, because the
// eviction list is the kind of structure that works until it is shared. Run
// with -race.
func TestCacheIsConcurrencySafe(t *testing.T) {
	loader := newCountingLoader()
	for i := range 50 {
		loader.set(fmt.Sprintf("t%d", i), "x")
	}
	env := New(WithLoader(loader), WithCacheSize(8))

	var wg sync.WaitGroup
	for g := range 16 {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := range 200 {
				name := fmt.Sprintf("t%d", (i*7+g)%50)
				if _, err := env.GetTemplate(name); err != nil {
					t.Errorf("GetTemplate(%q): %v", name, err)
					return
				}
				if i%50 == 0 {
					env.ForgetTemplate(name)
				}
			}
		}(g)
	}
	wg.Wait()
	if n := env.cache.len(); n > 8 {
		t.Errorf("cache holds %d entries after concurrent use, limit is 8", n)
	}
}

// TestZeroMeansDefaultForEveryLimit pins that the three limit options agree.
//
// They used to disagree: WithMaxRecursion(0) restored its default while
// WithMaxIterations(0) and WithMaxOutputBytes(0) switched their bounds off. A
// config struct deserialised from YAML or flags, with fields nobody set,
// therefore disabled two of the three safety controls in silence.
func TestZeroMeansDefaultForEveryLimit(t *testing.T) {
	env := New(WithMaxIterations(0), WithMaxOutputBytes(0), WithMaxRecursion(0), WithCacheSize(0))
	if env.maxIterations != defaultMaxIterations {
		t.Errorf("WithMaxIterations(0) = %d, want the default %d",
			env.maxIterations, int64(defaultMaxIterations))
	}
	if env.maxOutputBytes != defaultMaxOutputBytes {
		t.Errorf("WithMaxOutputBytes(0) = %d, want the default %d",
			env.maxOutputBytes, int64(defaultMaxOutputBytes))
	}
	if env.maxRecursion != 100 {
		t.Errorf("WithMaxRecursion(0) = %d, want the default 100", env.maxRecursion)
	}
	if env.cache.limit != defaultCacheSize {
		t.Errorf("WithCacheSize(0) = %d, want the default %d", env.cache.limit, defaultCacheSize)
	}

	// A zero-valued environment must still bound a runaway template.
	tmpl, err := env.FromString(`{% for i in range(100000000) %}x{% endfor %}`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if _, err := tmpl.RenderString(context.Background(), nil); err == nil {
		t.Error("a zero-valued configuration left the render unbounded")
	}
}

// TestWithoutLimitsIsExplicit pins that turning the bounds off still works, and
// has to be said out loud.
func TestWithoutLimitsIsExplicit(t *testing.T) {
	env := New(WithoutLimits())
	if env.maxIterations >= 0 || env.maxOutputBytes >= 0 {
		t.Fatalf("WithoutLimits left bounds in place: iterations=%d bytes=%d",
			env.maxIterations, env.maxOutputBytes)
	}
	tmpl, err := env.FromString(`{% for i in range(20000) %}x{% endfor %}`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	out, err := tmpl.RenderString(context.Background(), nil)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if got := strings.Count(out, "x"); got != 20000 {
		t.Errorf("got %d xs, want 20000", got)
	}
}

// TestGlobalsIsACopy pins that reading the globals cannot change them.
//
// The map handed back used to be the environment's own, so a caller who wrote
// to it replaced a built-in for every template compiled from that environment.
// `range` is as easy to clobber as anything else, and the doc comment saying
// "must not be mutated" is not a mechanism.
func TestGlobalsIsACopy(t *testing.T) {
	env := New()
	g := env.Globals()
	if _, ok := g["range"]; !ok {
		t.Fatal("range is not among the globals")
	}
	g["range"] = value.String("clobbered")
	delete(g, "dict")

	out := mustRenderVars(t, env, `{{ range(3)|list }}{{ dict(a=1) }}`, nil)
	if want := "[0, 1, 2]{'a': 1}"; out != want {
		t.Errorf("writing to the returned map changed the environment: got %q, want %q", out, want)
	}
	if again := env.Globals(); value.Str(again["range"]) == "clobbered" {
		t.Error("the environment kept the caller's write")
	}
}
