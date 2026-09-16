// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package conformance_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgilbir/gojja2/conformance"
)

// TestProfileMatchesOracle pins the two implementations of each environment
// profile together.
//
// A profile exists twice -- tools/oracle/profiles.py builds the environment
// the goldens are recorded under, conformance/profile.go builds the one gojja2
// renders under. If they drift, every case using that profile is graded
// against an answer produced by a different environment, and the corpus
// quietly reports divergences that are the harness's fault. These probes
// exercise each knob the profile touches, so a drift shows up here rather than
// as noise spread across a thousand chat templates.
func TestProfileMatchesOracle(t *testing.T) {
	oracle, err := conformance.StartOracle()
	if errors.Is(err, conformance.ErrNoOracle) {
		t.Skip("no CPython jinja2 oracle; run `make venv`")
	}
	if err != nil {
		t.Fatalf("start oracle: %v", err)
	}
	defer func() { _ = oracle.Close() }()

	// The context is raw JSON text, not a Go map, because these probes turn
	// on key *order* and a Go map has none to give. encoding/json sorts a
	// map's keys, which would hand both sides an already-alphabetical
	// object and leave the key-order probes unable to detect sorting at
	// all; encoding/json/v2 randomises them instead, which would make the
	// same probes flaky. Raw text has one order and keeps it under both.
	probes := map[string]struct {
		src string
		ctx string
	}{
		// tojson: insertion order, raw UTF-8, no HTML escaping, and not
		// marked safe -- all four differ from jinja2's own tojson.
		"tojson key order":         {`{{ d|tojson }}`, `{"d": {"zebra": 1, "apple": 2, "moose": 3}}`},
		"tojson sort_keys":         {`{{ d|tojson(sort_keys=true) }}`, `{"d": {"zebra": 1, "apple": 2}}`},
		"tojson unicode":           {`{{ s|tojson }}`, `{"s": "h\u00e9llo \u2192 <&>'"}`},
		"tojson nested":            {`{{ d|tojson }}`, `{"d": {"x": [1, 2.5, null, true, "a"]}}`},
		"tojson indent":            {`{{ d|tojson(indent=2) }}`, `{"d": {"b": [1, 2], "a": {"c": 3}}}`},
		"tojson empties":           {`{{ a|tojson }}{{ o|tojson }}`, `{"a": [], "o": {}}`},
		"tojson control char":      {`{{ s|tojson }}`, `{"s": "a\u0001b\tc\nd\"e\\f"}`},
		"tojson deep order":        {`{{ d|tojson }}`, `{"d": [{"z": 1, "a": 2}, {"y": 3, "b": 4}]}`},
		"tojson ensure_ascii":      {`{{ s|tojson(ensure_ascii=true) }}`, `{"s": "\u2713 h\u00e9 \ud83d\ude00"}`},
		"tojson ensure_ascii off":  {`{{ s|tojson(ensure_ascii=false) }}`, `{"s": "\u2713 h\u00e9 \ud83d\ude00"}`},
		"tojson separators":        {`{{ d|tojson(separators=(',',':')) }}`, `{"d": {"a": 1, "b": [1, 2]}}`},
		"tojson separators indent": {`{{ d|tojson(indent=2, separators=(',',': ')) }}`, `{"d": {"a": [1, 2]}}`},
		"tojson ascii indent":      {`{{ d|tojson(indent=2, ensure_ascii=true) }}`, `{"d": {"k": "\u00e9"}}`},

		// The globals the profile injects.
		"strftime_now":         {`{{ strftime_now("%Y-%m-%d") }}`, `{}`},
		"strftime_now verbose": {`{{ strftime_now("%A %d %B %Y %H:%M:%S %p") }}`, `{}`},
		"strftime_now literal": {`{{ strftime_now("100%% %j") }}`, `{}`},
		"raise_exception":      {`{{ raise_exception("boom") }}`, `{}`},

		// The whitespace options and the extension the profile turns on.
		"trim_blocks":  {"{% if true %}\nx\n{% endif %}\n", `{}`},
		"lstrip":       {"    {% if true %}\n    x\n    {% endif %}\n", `{}`},
		"loopcontrols": {`{% for i in range(5) %}{% if i == 2 %}{% break %}{% endif %}{{ i }}{% endfor %}`, `{}`},
	}

	for name, probe := range probes {
		t.Run(name, func(t *testing.T) {
			want, err := oracle.Render(conformance.OracleRequest{
				Name:    "probe.jj2",
				Source:  probe.src,
				Context: json.RawMessage(probe.ctx),
				Profile: conformance.ProfileTransformers,
			})
			if err != nil {
				t.Fatalf("oracle: %v", err)
			}

			c := caseFromProbe(t, probe.src, probe.ctx)
			out, renderErr := c.Render()
			if d := conformance.Compare(want.Expected(), out, renderErr); d != nil {
				t.Errorf("profile drift\n%s", d)
			}
		})
	}
}

// caseFromProbe writes a probe out as a real case file and loads it back, so
// the test exercises the same header parsing and environment construction the
// corpus does rather than a shortcut around it.
func caseFromProbe(t *testing.T, src, ctx string) *conformance.Case {
	t.Helper()
	// Spliced as text rather than re-marshalled, so the header keeps the
	// key order the probe wrote.
	header := `{"__profile__": "` + conformance.ProfileTransformers + `"`
	if inner := strings.TrimSpace(ctx); inner != "" && inner != "{}" {
		header += ", " + strings.TrimSuffix(strings.TrimPrefix(inner, "{"), "}")
	}
	header += "}"
	dir := t.TempDir()
	path := filepath.Join(dir, "probe.jj2")
	if err := os.WriteFile(path, []byte(header+conformance.Separator+src), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := conformance.LoadCase(dir, path)
	if err != nil {
		t.Fatalf("load probe: %v", err)
	}
	return c
}
