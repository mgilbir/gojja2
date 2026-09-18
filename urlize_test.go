// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"context"
	"testing"
)

// TestURLizeArguments: |urlize looks at its arguments where jinja2 looks at
// them, which is later and less strictly than a check at the filter's door.
//
//   - trim_url_limit is closed over by trim_url and compared per link, so a
//     text with no links never examines it and a non-number refuses by naming
//     the operator rather than the argument;
//   - target and rel are written `if target else ""` and `(rel or "").split()`,
//     so it is truthiness that decides, and an empty list stands in for the
//     empty string instead of being interpolated or refused;
//   - every extra scheme is matched against a regexp before any linking
//     happens, which gojja2 skipped entirely.
func TestURLizeArguments(t *testing.T) {
	env := New()
	ctx := map[string]any{
		"u":     "go https://example.com/long/path now",
		"plain": "no links here",
		"ftp":   "try ftp://x.com now",
	}
	const link = `<a href="https://example.com/long/path" rel="noopener">` +
		`https://example.com/long/path</a>`

	for _, tc := range []struct{ src, want string }{
		{`{{ u|urlize(10) }}`,
			`go <a href="https://example.com/long/path" rel="noopener">https://ex...</a> now`},
		// No link, so the limit is never looked at.
		{`{{ plain|urlize("x") }}`, "no links here"},
		{`{{ plain|urlize(1.5) }}`, "no links here"},
		// A falsey target writes no attribute; a truthy one does.
		{`{{ u|urlize(none, false, []) }}`, "go " + link + " now"},
		{`{{ u|urlize(none, false, 0) }}`, "go " + link + " now"},
		{`{{ u|urlize(none, false, "_blank") }}`,
			`go <a href="https://example.com/long/path" rel="noopener" target="_blank">` +
				`https://example.com/long/path</a> now`},
		// A falsey rel stands in for "", so it passes where a truthy
		// non-string is asked for a split it has not got.
		{`{{ u|urlize(none, false, none, []) }}`, "go " + link + " now"},
		{`{{ u|urlize(none, false, none, 0) }}`, "go " + link + " now"},
		// A valid extra scheme links.
		{`{{ ftp|urlize(none, false, none, none, ["ftp://"]) }}`,
			`try <a href="ftp://x.com" rel="noopener">ftp://x.com</a> now`},
		// The scheme class is Python's \w, which is Unicode-aware.
		{`{{ u|urlize(none, false, none, none, ["ü2:"]) }}`, "go " + link + " now"},
	} {
		tmpl, err := env.FromString(tc.src)
		if err != nil {
			t.Fatalf("compile %q: %v", tc.src, err)
		}
		got, err := tmpl.RenderString(context.Background(), ctx)
		if err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s\n got %q\nwant %q", tc.src, got, tc.want)
		}
	}

	for _, tc := range []struct{ src, want string }{
		{`{{ u|urlize("x") }}`, "'>' not supported between instances of 'int' and 'str'"},
		{`{{ u|urlize([]) }}`, "'>' not supported between instances of 'int' and 'list'"},
		// A float compares fine and fails at the slice, and it fails
		// there even when it is whole.
		{`{{ u|urlize(1.5) }}`, "slice indices must be integers or None or have an __index__ method"},
		{`{{ u|urlize(10.0) }}`, "slice indices must be integers or None or have an __index__ method"},
		// Only a truthy rel is asked to split.
		{`{{ u|urlize(none, false, none, 1) }}`, "'int' object has no attribute 'split'"},
		{`{{ u|urlize(none, false, none, [1]) }}`, "'list' object has no attribute 'split'"},
		// Every extra scheme is checked, before anything is linked.
		{`{{ u|urlize(none, false, none, none, ["ftp"]) }}`, "'ftp' is not a valid URI scheme prefix."},
		{`{{ u|urlize(none, false, none, none, ["ab:///"]) }}`, "'ab:///' is not a valid URI scheme prefix."},
		// A bare string is iterated character by character, so the
		// first character is the first scheme.
		{`{{ u|urlize(none, false, none, none, "ftp:") }}`, "'f' is not a valid URI scheme prefix."},
		// The check is a regexp match, so a non-string fails as re does.
		{`{{ u|urlize(none, false, none, none, [1]) }}`, "expected string or bytes-like object, got 'int'"},
	} {
		tmpl, err := env.FromString(tc.src)
		if err != nil {
			t.Fatalf("compile %q: %v", tc.src, err)
		}
		out, err := tmpl.RenderString(context.Background(), ctx)
		if err == nil {
			t.Errorf("%s: rendered %q, want %q", tc.src, out, tc.want)
			continue
		}
		if err.Error() != tc.want {
			t.Errorf("%s\n got %q\nwant %q", tc.src, err.Error(), tc.want)
		}
	}
}
