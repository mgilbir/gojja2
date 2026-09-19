// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"context"
	"testing"
	"time"

	"github.com/mgilbir/gojja2"
)

// A Go time.Time reaches a template as a value that calls itself a datetime.
// Everything but its rendering matches CPython exactly; the rendering is the
// divergence recorded in docs/divergences.md, and these pin both halves so
// neither can drift unnoticed.
//
// The corpus cannot reach any of this: its context travels as JSON, which has
// no way to spell a time.Time.

func renderTime(t *testing.T, src string, v time.Time) string {
	t.Helper()
	tmpl, err := gojja2.New().FromString(src)
	if err != nil {
		t.Fatalf("FromString(%q): %v", src, err)
	}
	out, err := tmpl.RenderString(context.Background(), map[string]any{"t": v})
	if err != nil {
		return "ERR: " + err.Error()
	}
	return out
}

// What matches CPython, given the same moment as a datetime.
func TestTimeBehavesLikeADatetime(t *testing.T) {
	v := time.Date(2026, 9, 19, 13, 45, 30, 0, time.UTC)
	for _, tc := range []struct{ src, want string }{
		{"{{ t.year }}", "2026"},
		{"{{ t.month }}", "9"},
		{"{{ t.day }}", "19"},
		{"{{ t.hour }}", "13"},
		{"{{ t.minute }}", "45"},
		{"{{ t.second }}", "30"},
		{"[{{ t.nosuch }}]", "[]"},
		{"{{ t.__class__.__name__ }}", "datetime"},
		{"{{ t is string }}", "False"},
		{"{{ t is number }}", "False"},
		{"{{ t is sequence }}", "False"},
		{"{{ t is mapping }}", "False"},
		{"{{ t == t }}", "True"},
		{"{{ t|int }}", "0"},
		// The messages name the type the way CPython's do.
		{"{{ t + 1 }}", "ERR: unsupported operand type(s) for +: 'datetime' and 'int'"},
		{"{{ t|length }}", "ERR: object of type 'datetime' has no len()"},
		{"{{ t|tojson }}", "ERR: Object of type datetime is not JSON serializable"},
	} {
		if got := renderTime(t, tc.src, v); got != tc.want {
			t.Errorf("%s\n got %q\nwant %q", tc.src, got, tc.want)
		}
	}
}

// What does not, which is the documented divergence. CPython renders
// "2026-09-19 13:45:30+00:00" for the first of these, the datetime constructor
// for the second, and keeps the microseconds in the third.
func TestTimeRendersAsRFC3339(t *testing.T) {
	cet := time.FixedZone("CET", 3600)
	for _, tc := range []struct {
		name, src string
		v         time.Time
		want      string
	}{
		{"utc", "{{ t }}", time.Date(2026, 9, 19, 13, 45, 30, 0, time.UTC), "2026-09-19T13:45:30Z"},
		{"offset", "{{ t }}", time.Date(2026, 9, 19, 13, 45, 30, 0, cet), "2026-09-19T13:45:30+01:00"},
		{"in a container", "{{ [t] }}", time.Date(2026, 9, 19, 13, 45, 30, 0, time.UTC), "[2026-09-19T13:45:30Z]"},
		{"pprint", "{{ t|pprint }}", time.Date(2026, 9, 19, 13, 45, 30, 0, time.UTC), "2026-09-19T13:45:30Z"},
		// time.RFC3339 carries no fractional seconds, so a sub-second
		// value prints without them. CPython would show .123456.
		{"microseconds dropped", "{{ t }}", time.Date(2026, 9, 19, 13, 45, 30, 123456000, time.UTC), "2026-09-19T13:45:30Z"},
	} {
		if got := renderTime(t, tc.src, tc.v); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}
