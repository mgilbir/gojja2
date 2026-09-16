// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package conformance_test

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mgilbir/gojja2/conformance"
)

// corpus names a set of cases and the goldens recorded for it.
type corpus struct {
	name string
	// cases and golden are relative to the repository root.
	cases  string
	golden string
	// optional marks a corpus produced by `make import`, which is
	// gitignored and absent on a fresh checkout.
	optional bool
}

var corpora = []corpus{
	{name: "own", cases: "testdata/corpus", golden: "testdata/golden"},
	{
		name:     "minijinja",
		cases:    "testdata/generated/minijinja",
		golden:   "testdata/generated/minijinja-golden",
		optional: true,
	},
	{
		name:     "jinja-harvest",
		cases:    "testdata/generated/jinja-harvest",
		golden:   "testdata/generated/jinja-harvest-golden",
		optional: true,
	},
	{
		name:     "minja",
		cases:    "testdata/generated/minja",
		golden:   "testdata/generated/minja-golden",
		optional: true,
	},
	{
		name:     "llamacpp",
		cases:    "testdata/generated/llamacpp",
		golden:   "testdata/generated/llamacpp-golden",
		optional: true,
	},
	{
		name:     "chat-templates",
		cases:    "testdata/generated/chat-templates",
		golden:   "testdata/generated/chat-templates-golden",
		optional: true,
	},
}

const knownFailuresPath = "testdata/known_failures.txt"

// repoRoot is the directory the corpus paths are relative to.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("locate repository root: %v", err)
	}
	return root
}

// loadKnownFailures reads the cases that are expected to diverge.
//
// The file is an admission, not a waiver: a case on it that starts passing
// fails the test, so the list can only shrink deliberately.
func loadKnownFailures(t *testing.T, root string) map[string]string {
	t.Helper()
	f, err := os.Open(filepath.Join(root, knownFailuresPath))
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}
		}
		t.Fatalf("open %s: %v", knownFailuresPath, err)
	}
	defer f.Close()

	known := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, reason, _ := strings.Cut(line, "#")
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		reason = strings.TrimSpace(reason)
		// A row may instead carry an inline reason, which is how an
		// ungradable case is marked.
		if fields := strings.SplitN(name, " ", 2); len(fields) == 2 {
			name, reason = fields[0], strings.TrimSpace(fields[1])
		}
		known[name] = reason
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read %s: %v", knownFailuresPath, err)
	}
	return known
}

// result describes how one case compared against the oracle.
type result struct {
	id      string
	ok      bool
	details string
}

func TestConformance(t *testing.T) {
	root := repoRoot(t)
	known := loadKnownFailures(t, root)

	var all []result
	usedKnown := map[string]bool{}
	// present records which corpora were actually graded. A known-failure
	// entry can only be checked for staleness against a corpus that is
	// there: the generated ones are gitignored, so on a checkout that has
	// not run `make import` their entries are not stale, merely unreachable.
	present := map[string]bool{}

	for _, c := range corpora {
		caseRoot := filepath.Join(root, c.cases)
		goldenRoot := filepath.Join(root, c.golden)

		paths, err := conformance.Collect(caseRoot)
		if err != nil {
			t.Fatalf("collect %s: %v", c.name, err)
		}
		if len(paths) == 0 {
			if c.optional {
				t.Logf("corpus %q not present; run `make import` to include it", c.name)
				continue
			}
			t.Errorf("corpus %q is empty at %s", c.name, c.cases)
			continue
		}
		present[c.name] = true

		for _, path := range paths {
			r := runCase(t, c.name, caseRoot, goldenRoot, path)
			all = append(all, r)
			if reason, listed := known[r.id]; listed {
				usedKnown[r.id] = true
				if r.ok {
					t.Errorf("%s is listed in %s (%q) but now passes; remove the entry",
						r.id, knownFailuresPath, reason)
				}
				continue
			}
			if !r.ok {
				t.Errorf("%s\n%s", r.id, indent(r.details))
			}
		}
	}

	for id := range known {
		if usedKnown[id] {
			continue
		}
		corpusName, _, _ := strings.Cut(id, "/")
		if !present[corpusName] {
			// Its corpus was not built; nothing can be concluded.
			continue
		}
		t.Errorf("%s lists %q, which is not in corpus %q", knownFailuresPath, id, corpusName)
	}

	// A case marked ungradable has no answer for anyone to match -- jinja2
	// fails it too -- so it is reported apart from the rate rather than
	// counted against it. Nothing else is excluded: a case we simply fail
	// stays in the denominator.
	passed, gradable, ungradable := 0, 0, 0
	for _, r := range all {
		if strings.HasPrefix(known[r.id], "ungradable:") {
			ungradable++
			continue
		}
		gradable++
		if r.ok {
			passed++
		}
	}
	if gradable > 0 {
		t.Logf("conformance: %d/%d gradable cases match CPython jinja2 (%.1f%%); "+
			"%d known divergence(s), %d ungradable",
			passed, gradable, 100*float64(passed)/float64(gradable),
			len(known)-ungradable, ungradable)
	}
}

func runCase(t *testing.T, corpusName, caseRoot, goldenRoot, path string) result {
	t.Helper()
	c, err := conformance.LoadCase(caseRoot, path)
	if err != nil {
		t.Fatalf("load case %s: %v", path, err)
	}
	id := corpusName + "/" + c.Rel

	golden, err := conformance.LoadGolden(goldenRoot, c.Rel)
	if err != nil {
		t.Fatalf("load golden for %s: %v", id, err)
	}

	out, renderErr := c.Render()
	if d := conformance.Compare(golden.Expected(), out, renderErr); d != nil {
		return result{id: id, details: d.String()}
	}
	return result{id: id, ok: true}
}

func indent(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = "    " + line
	}
	return strings.Join(lines, "\n")
}

// TestCorpusGoldensExist guards against a case file with no recorded answer,
// which would otherwise fail as a load error rather than as a missing golden.
func TestCorpusGoldensExist(t *testing.T) {
	root := repoRoot(t)
	for _, c := range corpora {
		paths, err := conformance.Collect(filepath.Join(root, c.cases))
		if err != nil || len(paths) == 0 {
			continue
		}
		var missing []string
		for _, path := range paths {
			rel, _ := filepath.Rel(filepath.Join(root, c.cases), path)
			rel = filepath.ToSlash(rel)
			if _, err := conformance.LoadGolden(filepath.Join(root, c.golden), rel); err != nil {
				missing = append(missing, rel)
			}
		}
		sort.Strings(missing)
		if len(missing) > 0 {
			t.Errorf("corpus %q has %d case(s) with no golden; run `make oracle`:\n  %s",
				c.name, len(missing), strings.Join(missing, "\n  "))
		}
	}
}
