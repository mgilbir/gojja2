// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package conformance

import (
	"regexp"
	"strings"
)

// tagRe matches one template tag, so a template can be cut at boundaries that
// mean something rather than at arbitrary byte offsets.
var tagRe = regexp.MustCompile(`(?s)\{\{.*?\}\}|\{%.*?%\}|\{#.*?#\}`)

// Segments splits a template into tags and the text between them.
func Segments(template string) []string {
	var out []string
	last := 0
	for _, loc := range tagRe.FindAllStringIndex(template, -1) {
		if loc[0] > last {
			out = append(out, template[last:loc[0]])
		}
		out = append(out, template[loc[0]:loc[1]])
		last = loc[1]
	}
	if last < len(template) {
		out = append(out, template[last:])
	}
	return out
}

// Shrink reduces a diverging template while it keeps diverging the same way.
//
// A generated template is mostly noise around the one construct that actually
// differs, and a 400-character case is not a bug report. Reduction works on
// tag boundaries: dropping half the tags usually leaves something unbalanced,
// which simply fails to reproduce and is rejected, so correctness comes from
// the check rather than from the cutting being clever.
//
// budget bounds the number of oracle round trips spent, since each one costs a
// render on both sides.
func Shrink(template string, budget int, check func(string) *Divergence) string {
	original := check(template)
	if original == nil {
		return template
	}

	spent := 0
	reproduces := func(candidate string) bool {
		if spent >= budget || candidate == "" {
			return false
		}
		spent++
		d := check(candidate)
		return d != nil && d.Kind == original.Kind
	}

	best := template
	for spent < budget {
		segments := Segments(best)
		if len(segments) <= 1 {
			break
		}
		improved := false

		// Coarse first: try to keep only one half, then drop runs.
		for width := len(segments) / 2; width >= 1; width /= 2 {
			for start := 0; start+width <= len(segments); start += width {
				candidate := strings.Join(
					append(append([]string{}, segments[:start]...), segments[start+width:]...), "")
				if reproduces(candidate) {
					best = candidate
					improved = true
					break
				}
			}
			if improved {
				break
			}
		}
		if !improved {
			break
		}
	}

	// Finally trim surrounding literal text, which never carries the bug.
	for _, candidate := range []string{
		strings.TrimSpace(best),
		strings.Trim(best, " \n\t"),
	} {
		if candidate != best && reproduces(candidate) {
			best = candidate
		}
	}
	return best
}
