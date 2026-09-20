// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"context"
	"testing"
	"time"
)

// A Go time.Time reaches a template as a datetime, and prints as one: str() is
// isoformat with a space, repr() is the constructor call that would rebuild
// it. Both were RFC 3339 before, which meant a template printing a timestamp
// rendered differently here than under jinja2 -- and, because time.RFC3339
// carries no fractional seconds, a sub-second time printed without them.
//
// The corpus cannot reach any of this: its context travels as JSON, which has
// no way to spell a time.Time. Every expectation below was taken from CPython
// for the same moment.

func renderTime(t *testing.T, src string, v time.Time) string {
	t.Helper()
	tmpl, err := mustEnv().FromString(src)
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

// str(datetime) and repr(datetime), across the shapes that change the answer:
// microseconds present, absent and trailing-zeroed; seconds zero; offsets
// positive, negative, zero and not a whole number of minutes; zones named and
// not.
func TestTimePrintsLikeADatetime(t *testing.T) {
	utc := time.UTC
	cet := time.FixedZone("CET", 3600)
	est := time.FixedZone("EST", -18000)
	gmt := time.FixedZone("GMT", 0)
	odd := time.FixedZone("", 1172)
	for _, tc := range []struct {
		name      string
		v         time.Time
		str, repr string
	}{
		{"utc", time.Date(2026, 9, 19, 13, 45, 30, 0, utc),
			"2026-09-19 13:45:30+00:00",
			"datetime.datetime(2026, 9, 19, 13, 45, 30, tzinfo=datetime.timezone.utc)"},
		{"microseconds", time.Date(2026, 9, 19, 13, 45, 30, 123456000, utc),
			"2026-09-19 13:45:30.123456+00:00",
			"datetime.datetime(2026, 9, 19, 13, 45, 30, 123456, tzinfo=datetime.timezone.utc)"},
		{"trailing zeros kept", time.Date(2026, 9, 19, 13, 45, 30, 120000000, utc),
			"2026-09-19 13:45:30.120000+00:00",
			"datetime.datetime(2026, 9, 19, 13, 45, 30, 120000, tzinfo=datetime.timezone.utc)"},
		{"seconds zero", time.Date(2026, 9, 19, 13, 45, 0, 0, utc),
			"2026-09-19 13:45:00+00:00",
			"datetime.datetime(2026, 9, 19, 13, 45, tzinfo=datetime.timezone.utc)"},
		{"seconds zero with microseconds", time.Date(2026, 9, 19, 13, 45, 0, 500000, utc),
			"2026-09-19 13:45:00.000500+00:00",
			"datetime.datetime(2026, 9, 19, 13, 45, 0, 500, tzinfo=datetime.timezone.utc)"},
		{"named offset", time.Date(2026, 9, 19, 13, 45, 30, 0, cet),
			"2026-09-19 13:45:30+01:00",
			"datetime.datetime(2026, 9, 19, 13, 45, 30, tzinfo=datetime.timezone(datetime.timedelta(seconds=3600), 'CET'))"},
		{"negative offset borrows a day", time.Date(2026, 9, 19, 13, 45, 30, 0, est),
			"2026-09-19 13:45:30-05:00",
			"datetime.datetime(2026, 9, 19, 13, 45, 30, tzinfo=datetime.timezone(datetime.timedelta(days=-1, seconds=68400), 'EST'))"},
		{"zero offset with a name", time.Date(2026, 9, 19, 13, 45, 30, 0, gmt),
			"2026-09-19 13:45:30+00:00",
			"datetime.datetime(2026, 9, 19, 13, 45, 30, tzinfo=datetime.timezone(datetime.timedelta(0), 'GMT'))"},
		{"offset with seconds", time.Date(2026, 9, 19, 13, 45, 30, 0, odd),
			"2026-09-19 13:45:30+00:19:32",
			"datetime.datetime(2026, 9, 19, 13, 45, 30, tzinfo=datetime.timezone(datetime.timedelta(seconds=1172)))"},
		{"year below 1000 pads in str only", time.Date(26, 1, 2, 3, 4, 5, 0, utc),
			"0026-01-02 03:04:05+00:00",
			"datetime.datetime(26, 1, 2, 3, 4, 5, tzinfo=datetime.timezone.utc)"},
	} {
		if got := renderTime(t, "{{ t }}", tc.v); got != tc.str {
			t.Errorf("%s: str\n got %q\nwant %q", tc.name, got, tc.str)
		}
		if got := renderTime(t, "{{ [t] }}", tc.v); got != "["+tc.repr+"]" {
			t.Errorf("%s: repr\n got %q\nwant %q", tc.name, got, "["+tc.repr+"]")
		}
		if got := renderTime(t, "{{ t|pprint }}", tc.v); got != tc.repr {
			t.Errorf("%s: pprint\n got %q\nwant %q", tc.name, got, tc.repr)
		}
	}
}

// A datetime holds microseconds, so a Go time's nanoseconds are truncated --
// not rounded, which would move the value.
func TestTimeTruncatesNanosecondsToMicroseconds(t *testing.T) {
	v := time.Date(2026, 9, 19, 13, 45, 30, 123456789, time.UTC)
	if got, want := renderTime(t, "{{ t }}", v), "2026-09-19 13:45:30.123456+00:00"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	// 999 nanoseconds is less than a microsecond, so nothing is printed.
	v = time.Date(2026, 9, 19, 13, 45, 30, 999, time.UTC)
	if got, want := renderTime(t, "{{ t }}", v), "2026-09-19 13:45:30+00:00"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
