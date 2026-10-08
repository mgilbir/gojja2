// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package conformance_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/mgilbir/gojja2/conformance"
)

// Shrink's only caller runs after the differential has found a divergence, so
// a clean soak never executes it, and a shrinker that hands back a template
// which no longer diverges turns the one bug report a soak produces into a
// wrong one. These tests drive it with a fake check whose answer is decided by
// the candidate's text, so what a minimal reproduction is can be said exactly.

// containsAll diverges with kind whenever the candidate holds every needle.
func containsAll(kind string, needles ...string) func(string) *conformance.Divergence {
	return func(s string) *conformance.Divergence {
		for _, n := range needles {
			if !strings.Contains(s, n) {
				return nil
			}
		}
		return &conformance.Divergence{Kind: kind, Detail: s}
	}
}

// counted wraps a check, recording every candidate it was asked about.
type counted struct {
	t     *testing.T
	check func(string) *conformance.Divergence
	seen  []string
}

func (c *counted) call(s string) *conformance.Divergence {
	if s == "" {
		c.t.Errorf("Shrink asked about an empty candidate")
	}
	c.seen = append(c.seen, s)
	return c.check(s)
}

// requireReproduces is the property every result must have: it diverges, and
// in the same way the original did.
func requireReproduces(t *testing.T, check func(string) *conformance.Divergence, original, got string) {
	t.Helper()
	want := check(original)
	d := check(got)
	if d == nil {
		t.Fatalf("Shrink returned %q, which does not diverge", got)
	}
	if d.Kind != want.Kind {
		t.Fatalf("Shrink returned %q, which diverges as %q where the original was %q", got, d.Kind, want.Kind)
	}
}

func TestSegments(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"plain text", []string{"plain text"}},
		{"{{ x }}", []string{"{{ x }}"}},
		{"a{{ x }}b{% if y %}{# c #}d",
			[]string{"a", "{{ x }}", "b", "{% if y %}", "{# c #}", "d"}},
		// A tag may span lines, and the shortest match ends it.
		{"{{ a\n}}{{ b }}", []string{"{{ a\n}}", "{{ b }}"}},
		// An unclosed tag is text.
		{"x {{ y", []string{"x {{ y"}},
	}
	for _, c := range cases {
		got := conformance.Segments(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("Segments(%q) = %q, want %q", c.in, got, c.want)
		}
		if j := strings.Join(got, ""); j != c.in {
			t.Errorf("Segments(%q) joins back to %q", c.in, j)
		}
	}
}

func TestShrinkReducesToTheMinimalTemplate(t *testing.T) {
	cases := []struct {
		name     string
		template string
		needles  []string
		want     string
	}{
		{
			name:     "one tag",
			template: "head {{ a }} mid {% if x %}{{ BUG }}{% endif %} tail {{ b }}\n",
			needles:  []string{"{{ BUG }}"},
			want:     "{{ BUG }}",
		},
		{
			// Three tags must survive together; the cut that removes any one
			// of them stops reproducing and is refused.
			name:     "a balanced block",
			template: "{{ a }}{% if x %}{{ b }}{{ BUG }}{{ c }}{% endif %}{{ d }}{{ e }}",
			needles:  []string{"{% if x %}", "{{ BUG }}", "{% endif %}"},
			want:     "{% if x %}{{ BUG }}{% endif %}",
		},
		{
			name:     "surrounding whitespace",
			template: "\n  {{ BUG }}  \n",
			needles:  []string{"{{ BUG }}"},
			want:     "{{ BUG }}",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			check := containsAll(conformance.KindOutput, c.needles...)
			cc := &counted{t: t, check: check}
			got := conformance.Shrink(c.template, 1000, cc.call)
			requireReproduces(t, check, c.template, got)
			if got != c.want {
				t.Errorf("Shrink(%q) = %q, want %q", c.template, got, c.want)
			}
		})
	}
}

// A reduction that diverges some other way is a different bug, and taking it
// would report that one under the original's name.
func TestShrinkKeepsTheKindOfDivergence(t *testing.T) {
	check := func(s string) *conformance.Divergence {
		switch {
		case strings.Contains(s, "{{ BUG }}"):
			return &conformance.Divergence{Kind: conformance.KindOutput}
		case strings.Contains(s, "{{ OTHER }}"):
			return &conformance.Divergence{Kind: conformance.KindErrorMessage}
		}
		return nil
	}
	// BUG comes first, so the first cut tried leaves only OTHER.
	template := "{{ BUG }}{{ OTHER }}"
	got := conformance.Shrink(template, 1000, check)
	requireReproduces(t, check, template, got)
	if got != "{{ BUG }}" {
		t.Errorf("Shrink(%q) = %q, want %q", template, got, "{{ BUG }}")
	}
}

// The trim at the end is a reduction like any other: when the surrounding
// whitespace is part of what diverges, it stays.
func TestShrinkTrimsOnlyWhatStillReproduces(t *testing.T) {
	check := func(s string) *conformance.Divergence {
		if strings.HasPrefix(s, "\n") && strings.Contains(s, "{{ BUG }}") {
			return &conformance.Divergence{Kind: conformance.KindOutput}
		}
		return nil
	}
	template := "\n{{ x }}{{ BUG }}\n"
	got := conformance.Shrink(template, 1000, check)
	requireReproduces(t, check, template, got)
	if got != "\n{{ BUG }}" {
		t.Errorf("Shrink(%q) = %q, want %q", template, got, "\n{{ BUG }}")
	}
}

// Every candidate costs a render on both sides, so the budget is a bound on
// calls, not a suggestion. The first call, which establishes that the
// original diverges at all, is not charged against it.
func TestShrinkRespectsTheBudget(t *testing.T) {
	// BUG leads, so the first cut at every width removes it and is refused:
	// a round costs more than one call, which is what lets an overspend show.
	// The trailing newline gives the final trim something to ask about.
	var b strings.Builder
	b.WriteString("{{ BUG }}")
	for range 64 {
		b.WriteString("{{ noise }}")
	}
	b.WriteString("\n")
	template := b.String()
	check := containsAll(conformance.KindOutput, "{{ BUG }}")

	for _, budget := range []int{0, 1, 3, 10} {
		cc := &counted{t: t, check: check}
		got := conformance.Shrink(template, budget, cc.call)
		if len(cc.seen) > budget+1 {
			t.Errorf("budget %d: Shrink asked %d times", budget, len(cc.seen))
		}
		requireReproduces(t, check, template, got)
		if budget == 0 && got != template {
			t.Errorf("budget 0: Shrink returned %q, want the template untouched", got)
		}
	}

	// And with enough of it, the same template does reach the minimum.
	got := conformance.Shrink(template, 1000, check)
	if got != "{{ BUG }}" {
		t.Errorf("with a large budget Shrink returned %q", got)
	}
}

func TestShrinkLeavesAnAgreeingTemplateAlone(t *testing.T) {
	cc := &counted{t: t, check: containsAll(conformance.KindOutput, "{{ BUG }}")}
	template := "{{ a }}{{ b }}"
	if got := conformance.Shrink(template, 1000, cc.call); got != template {
		t.Errorf("Shrink(%q) = %q for a template that does not diverge", template, got)
	}
	if len(cc.seen) != 1 {
		t.Errorf("Shrink asked %d times about a template that does not diverge", len(cc.seen))
	}
}

// A template that is nothing but whitespace trims to the empty string, which
// is not a template and must not be offered to the check.
func TestShrinkNeverOffersTheEmptyTemplate(t *testing.T) {
	always := func(string) *conformance.Divergence {
		return &conformance.Divergence{Kind: conformance.KindOutput}
	}
	cc := &counted{t: t, check: always}
	template := " \n\t "
	if got := conformance.Shrink(template, 1000, cc.call); got != template {
		t.Errorf("Shrink(%q) = %q", template, got)
	}
}
