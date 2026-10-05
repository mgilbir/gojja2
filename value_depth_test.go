// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"encoding/json"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/mgilbir/gojja2/value"
)

// TestValueWalkDepthsAreMeasured grades the table in docs/limits.md that says
// how deep a value each walk survives -- CPython's numbers against
// testdata/value_depth.json, and gojja2's against gojja2.
//
// The table was a published number nothing graded, and it went stale in the
// worst way: it was measured on 3.11, where every C-level walk gave up at ~990,
// and it read "the same error, at 1,000" beside gojja2's wall. 3.12 gave the C
// recursion limit a counter of its own and those walks now go ten times deeper,
// so the claim was true of one interpreter and wrong about the pinned one by a
// factor of ten -- and gojja2's wall, described as matching, is now the lower of
// the two.
//
// Both halves are measured here, because a table that only checked one would
// still let the other drift.
func TestValueWalkDepthsAreMeasured(t *testing.T) {
	recorded := readRecordedDepths(t)
	doc := readDepthTable(t)

	// The walks the table names, and how to ask gojja2 for each. A nil
	// template means "no wall": the walk is iterative here.
	walks := []struct {
		row  string // the row's first cell, as the table writes it
		key  string // the key in testdata/value_depth.json
		tmpl string // a template that walks the deep value
	}{
		{"`==`, `<`, `\\|min`", "equal", `{{ a == b }}`},
		{"`\\|sort`", "sort", `{{ [a, b]|sort|length }}`},
		{"`\\|tojson`", "tojson", `{{ a|tojson|length }}`},
		{"`\\|pprint`", "pprint", `{{ a|pprint|length }}`},
		{"`{{ v }}`, `\\|string`, `\\|upper`, a dict key", "print", `{{ a|string|length }}`},
	}
	for _, w := range walks {
		cells, ok := doc[w.row]
		if !ok {
			t.Errorf("docs/limits.md has no row for %s; the table this grades has moved", w.row)
			continue
		}
		// CPython's columns, in the order the header lists them.
		for i, v := range []string{"3.11", "3.12", "3.13", "3.14"} {
			want := recorded[v][w.key]
			if got := cells[i]; got != strconv.Itoa(want) {
				t.Errorf("%s under CPython %s: the table says %s, "+
					"testdata/value_depth.json says %d", w.row, v, got, want)
			}
		}
		// And gojja2's own, measured by rendering.
		wall := gojja2Wall(t, w.tmpl)
		stated := cells[len(cells)-1]
		if wall == 0 {
			if stated != "no wall" {
				t.Errorf("%s: gojja2 walks 20,000 deep, but the table says %q",
					w.row, stated)
			}
			continue
		}
		if stated != strconv.Itoa(wall) {
			t.Errorf("%s: gojja2's wall is at %d, the table says %q", w.row, wall, stated)
		}
	}
}

// gojja2Wall is the deepest value the walk survives, or 0 for no wall within
// twenty thousand levels -- which is the answer for the walks written
// iteratively, and is what the table calls "no wall".
func gojja2Wall(t *testing.T, tmpl string) int {
	t.Helper()
	env := mustNew()
	compiled, err := env.FromString(tmpl)
	if err != nil {
		t.Fatalf("%s: %v", tmpl, err)
	}
	works := func(depth int) bool {
		vars := map[string]value.Value{"a": nested(depth, 1), "b": nested(depth, 2)}
		// RenderValues, so the deep value reaches the walk as it is: the
		// conversion RenderString does would walk it first.
		return compiled.RenderValues(t.Context(), io.Discard, vars) == nil
	}
	const ceiling = 20000
	if works(ceiling) {
		return 0
	}
	lo, hi := 1, ceiling
	for lo+1 < hi {
		mid := (lo + hi) / 2
		if works(mid) {
			lo = mid
		} else {
			hi = mid
		}
	}
	return lo
}

// nested is [[[...[leaf]...]]], depth levels of list. The leaf differs between
// the two values so that a comparison has to descend the whole way.
func nested(depth int, leaf int64) value.Value {
	v := value.NewList(value.Int(leaf))
	for range depth - 1 {
		v = value.NewList(v)
	}
	return v
}

func readRecordedDepths(t *testing.T) map[string]map[string]int {
	t.Helper()
	raw, err := os.ReadFile("testdata/value_depth.json")
	if err != nil {
		t.Fatalf("read the recorded depths (regenerate with `make value-depth`): %v", err)
	}
	var file struct {
		Walks map[string]map[string]int `json:"walks"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatalf("testdata/value_depth.json: %v", err)
	}
	if len(file.Walks) != 4 {
		t.Fatalf("testdata/value_depth.json holds %d interpreters, want 4", len(file.Walks))
	}
	return file.Walks
}

// depthRow matches one row of the table: the walk, four CPython columns and
// gojja2's.
var depthRow = regexp.MustCompile(`^\| (.+?) \| (\S+) \| (\S+) \| (\S+) \| (\S+) \| (.+?) \|$`)

// readDepthTable reads the rows out of docs/limits.md, keyed by the first cell.
func readDepthTable(t *testing.T) map[string][]string {
	t.Helper()
	raw, err := os.ReadFile("docs/limits.md")
	if err != nil {
		t.Fatalf("read docs/limits.md: %v", err)
	}
	out := map[string][]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		m := depthRow.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		out[m[1]] = []string{m[2], m[3], m[4], m[5], m[6]}
	}
	if len(out) == 0 {
		t.Fatal("docs/limits.md has no six-column table; this test would pass on an empty file")
	}
	return out
}
