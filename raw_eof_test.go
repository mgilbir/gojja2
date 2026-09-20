// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"context"
	"strings"
	"testing"
)

// A raw tag with nothing after it at all is not an error in jinja2.
//
// Its lexer looks for the body with a regex that requires one, so an empty
// remainder never reaches the "missing end" branch and the tokenizer simply
// stops. `{% raw %}` renders "" there and `{% raw %}abc` raises -- an accident
// of the regex rather than a rule, but it is the specification, and the two
// differ only on a template that is already broken.
//
// The boundary is exactly "is there anything left", which is why a single
// trailing space is enough to bring the error back.
func TestEmptyRawAtEndOfTemplate(t *testing.T) {
	for _, tc := range []struct{ src, want, wantErr string }{
		{`{% raw %}`, "", ""},
		{`{%- raw -%}`, "", ""},
		{`{%+ raw %}`, "", ""},
		{`a{% raw %}`, "a", ""},
		{`{% raw %}{% endraw %}`, "", ""},
		{`{% raw %}x{% endraw %}`, "x", ""},
		// Anything at all after it, and the end is required again.
		{`{% raw %}abc`, "", "Missing end of raw directive"},
		{`{% raw %}   `, "", "Missing end of raw directive"},
		{`{% raw %}{{ 1 }}`, "", "Missing end of raw directive"},
		{`x{% raw %}y`, "", "Missing end of raw directive"},
		{`{% raw %}{% raw %}`, "", "Missing end of raw directive"},
	} {
		tmpl, err := mustNew().FromString(tc.src)
		if tc.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("%q: compile gave %v, want %q", tc.src, err, tc.wantErr)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: compile: %v", tc.src, err)
			continue
		}
		got, err := tmpl.RenderString(context.Background(), nil)
		if err != nil || got != tc.want {
			t.Errorf("%q = %q, %v; want %q", tc.src, got, err, tc.want)
		}
	}
}
