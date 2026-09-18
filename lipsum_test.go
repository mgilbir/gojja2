// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"context"
	"strings"
	"testing"
	"unicode"
)

// TestLipsumShape: lipsum draws from a random source, so its words cannot be
// compared against CPython's -- but its shape can, and it was wrong.
//
// jinja2 punctuates a paragraph as it builds it: a comma once the last one is
// at least four words behind, a full stop once the last of those is far enough
// behind, a capital on the word after each stop, and never the same word twice
// in a row. gojja2 emitted one run-on sentence of independently drawn words
// with a single stop at the end, and separated HTML paragraphs with a blank
// line where jinja2 uses a single newline -- the tags already mark them apart.
//
// The invariants below are the ones the algorithm guarantees, checked over
// enough paragraphs that a missing rule cannot hide behind the randomness.
func TestLipsumShape(t *testing.T) {
	env := New()
	tmpl, err := env.FromString(`{{ lipsum(n, html, 40, 60) }}`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	render := func(n int, html bool) string {
		out, err := tmpl.RenderString(context.Background(),
			map[string]any{"n": n, "html": html})
		if err != nil {
			t.Fatalf("lipsum(%d, %v): %v", n, html, err)
		}
		return out
	}

	// The HTML form is one newline between paragraphs, each in a <p>; the
	// plain form is a blank line and no tags.
	htmlOut := render(3, true)
	if got := strings.Split(htmlOut, "\n"); len(got) != 3 {
		t.Errorf("html: %d lines, want 3: %q", len(got), htmlOut)
	} else {
		for _, p := range got {
			if !strings.HasPrefix(p, "<p>") || !strings.HasSuffix(p, "</p>") {
				t.Errorf("html paragraph not wrapped: %q", p)
			}
		}
	}
	if strings.Contains(htmlOut, "\n\n") {
		t.Errorf("html form has a blank line between paragraphs: %q", htmlOut)
	}
	plain := render(3, false)
	if got := strings.Split(plain, "\n\n"); len(got) != 3 {
		t.Errorf("plain: %d paragraphs, want 3: %q", len(got), plain)
	}
	if strings.Contains(plain, "<p>") {
		t.Errorf("plain form carries tags: %q", plain)
	}

	// Now the paragraph shape, over enough samples that each rule has to
	// have fired.
	var commas, stops, commaThenStop int
	minGap := 1 << 30
	for range 200 {
		p := render(1, false)
		if !strings.HasSuffix(p, ".") {
			t.Fatalf("paragraph does not end in a full stop: %q", p)
		}
		words := strings.Split(p, " ")
		if len(words) < 40 || len(words) >= 60 {
			t.Fatalf("paragraph is %d words, want [40, 60): %q", len(words), p)
		}
		prev, gap, capitalNext := "", 0, true
		for _, w := range words {
			base := strings.TrimRight(w, ",.")
			if base == "" {
				t.Fatalf("empty word in %q", p)
			}
			if capitalNext && !unicode.IsUpper(rune(base[0])) {
				t.Errorf("sentence starts uncapitalised at %q in %q", w, p)
			}
			capitalNext = false
			if low := strings.ToLower(base); low == prev {
				t.Errorf("the same word twice in a row (%q) in %q", low, p)
			} else {
				prev = low
			}
			gap++
			if strings.Contains(w, ",") {
				commas++
				if gap < 4 {
					t.Errorf("comma only %d words after the last, in %q", gap, p)
				}
				minGap = min(minGap, gap)
				gap = 0
			}
			if strings.HasSuffix(w, ".") {
				stops++
				gap = 0
				capitalNext = true
			}
			if strings.HasSuffix(w, ",.") {
				commaThenStop++
			}
		}
	}
	// Each rule has to have fired, or the check above proves nothing.
	if commas == 0 {
		t.Error("no commas in 200 paragraphs: the comma rule never ran")
	}
	if stops == 0 {
		t.Error("no interior full stops in 200 paragraphs: the sentence rule never ran")
	}
	// Both rules can land on the same word, which is a shape jinja2 really
	// produces -- tidying it away would be a divergence of its own.
	if commaThenStop == 0 {
		t.Error(`no "word,." in 200 paragraphs: the two rules never met`)
	}
	if minGap != 4 {
		t.Errorf("closest comma gap seen was %d, want the algorithm's 4", minGap)
	}
}
