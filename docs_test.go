// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The documentation is graded like anything else here.
//
// Two failures these guards exist for, both found by reading rather than by a
// test: docs/scope.md had no inbound link from anywhere, so the one document
// that answers "is X in scope?" was reachable only by listing the directory;
// and docs/divergences.md carried three references written as
// `[ErrOutputTooLarge]`, which is godoc's link syntax and renders in Markdown
// as literal brackets pointing nowhere.
//
// Both are the kind of rot that no reviewer notices and no reader reports --
// they just leave.

// skipDirs are not ours to grade: pinned upstream checkouts, generated corpora,
// the oracle virtualenv.
var skipDirs = map[string]bool{
	".git":        true,
	".venv":       true,
	"third_party": true,
	"testdata":    true,
}

// markdownFiles returns every first-party .md file, repo-root-relative and
// slash-separated.
func markdownFiles(t *testing.T) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(".", func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), ".md") {
			out = append(out, filepath.ToSlash(p))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking for markdown: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("found no markdown files; the walk is wrong")
	}
	sort.Strings(out)
	return out
}

var (
	fenceRe    = regexp.MustCompile("(?s)```.*?```")
	inlineRe   = regexp.MustCompile("`[^`\n]*`")
	linkRe     = regexp.MustCompile(`\[[^\]\[]*\]\(([^)\s]+)\)`)
	headingRe  = regexp.MustCompile(`(?m)^#{1,6}[ \t]+(.*)$`)
	slugDropRe = regexp.MustCompile(`[^\w\- ]`)
	godocRefRe = regexp.MustCompile(`\[([A-Z][A-Za-z0-9_]*)\]`)
	refDefRe   = regexp.MustCompile(`(?m)^\[([^\]]+)\]:`)
	hasLowerRe = regexp.MustCompile(`[a-z]`)
	externalRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*:`)
)

// stripCode removes fenced blocks and inline spans, so a link or a bracketed
// identifier shown as an example is not mistaken for a real one.
func stripCode(s string) string {
	s = fenceRe.ReplaceAllStringFunc(s, func(m string) string {
		return strings.Repeat("\n", strings.Count(m, "\n"))
	})
	return inlineRe.ReplaceAllString(s, "")
}

// slug renders a heading the way GitHub's anchor generator does: lower-case,
// punctuation dropped, spaces to hyphens.
func slug(heading string) string {
	s := strings.ToLower(strings.TrimSpace(heading))
	s = slugDropRe.ReplaceAllString(s, "")
	s = strings.TrimSpace(s)
	return strings.ReplaceAll(s, " ", "-")
}

// anchors returns every anchor a file offers, including GitHub's -1, -2 suffixes
// for repeated headings.
func anchors(body string) map[string]bool {
	seen := map[string]int{}
	out := map[string]bool{}
	for _, m := range headingRe.FindAllStringSubmatch(stripCode(body), -1) {
		s := slug(m[1])
		if s == "" {
			continue
		}
		if n := seen[s]; n > 0 {
			out[fmt.Sprintf("%s-%d", s, n)] = true
		} else {
			out[s] = true
		}
		seen[s]++
	}
	return out
}

// docLink is one resolved inline link.
type docLink struct {
	from   string // file it appears in
	raw    string // as written
	target string // repo-relative path, or "" for a same-file anchor
	anchor string
}

func collectLinks(t *testing.T, files []string) []docLink {
	t.Helper()
	var out []docLink
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("reading %s: %v", f, err)
		}
		for _, m := range linkRe.FindAllStringSubmatch(stripCode(string(b)), -1) {
			raw := m[1]
			if externalRe.MatchString(raw) || strings.HasPrefix(raw, "//") {
				continue
			}
			p, frag, _ := strings.Cut(raw, "#")
			l := docLink{from: f, raw: raw, anchor: frag}
			if p != "" {
				l.target = path.Clean(path.Join(path.Dir(f), p))
			}
			out = append(out, l)
		}
	}
	return out
}

// TestDocLinksResolve requires every relative link in a first-party Markdown
// file to point at something that exists, and every anchor to name a heading
// that exists.
func TestDocLinksResolve(t *testing.T) {
	files := markdownFiles(t)
	links := collectLinks(t, files)
	if len(links) == 0 {
		t.Fatal("found no relative links; the extractor is wrong")
	}

	anchorCache := map[string]map[string]bool{}
	anchorsOf := func(p string) map[string]bool {
		if a, ok := anchorCache[p]; ok {
			return a
		}
		b, err := os.ReadFile(p)
		if err != nil {
			anchorCache[p] = nil
			return nil
		}
		a := anchors(string(b))
		anchorCache[p] = a
		return a
	}

	for _, l := range links {
		target := l.target
		if target == "" {
			target = l.from // a bare #anchor refers to the same file
		}
		info, err := os.Stat(target)
		if err != nil {
			t.Errorf("%s: link %q points at %s, which does not exist", l.from, l.raw, target)
			continue
		}
		if l.anchor == "" || info.IsDir() || !strings.HasSuffix(target, ".md") {
			continue
		}
		if a := anchorsOf(target); a != nil && !a[l.anchor] {
			t.Errorf("%s: link %q names anchor #%s, which %s has no heading for",
				l.from, l.raw, l.anchor, target)
		}
	}
}

// TestNoDocIsOrphaned requires every first-party Markdown file to be reachable
// by a link from another one.
//
// docs/scope.md was orphaned for long enough that the README grew its own,
// shorter, lossier answer to the same question. A document nobody can reach is
// a document that gets rewritten badly somewhere else.
func TestNoDocIsOrphaned(t *testing.T) {
	files := markdownFiles(t)
	linked := map[string]bool{}
	for _, l := range collectLinks(t, files) {
		if l.target == "" {
			continue
		}
		linked[l.target] = true
		// A link to a directory is a link to its README.
		if info, err := os.Stat(l.target); err == nil && info.IsDir() {
			linked[path.Join(l.target, "README.md")] = true
		}
	}
	for _, f := range files {
		if f == "README.md" {
			continue // the repository's front door; nothing above it to link from
		}
		if !linked[f] {
			t.Errorf("%s is not linked from any other document; either link it or delete it", f)
		}
	}
}

// TestNoGodocLinkSyntaxInMarkdown catches `[Identifier]` written in a .md file.
//
// That is godoc's syntax for linking a symbol. In Markdown it is not a link at
// all: it renders as literal square brackets, so the reader sees something that
// looks clickable, is not, and points nowhere. docs/divergences.md had three.
func TestNoGodocLinkSyntaxInMarkdown(t *testing.T) {
	for _, f := range markdownFiles(t) {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("reading %s: %v", f, err)
		}
		body := string(b)
		defined := map[string]bool{}
		for _, m := range refDefRe.FindAllStringSubmatch(body, -1) {
			defined[m[1]] = true
		}
		stripped := stripCode(body)
		for _, loc := range godocRefRe.FindAllStringSubmatchIndex(stripped, -1) {
			name := stripped[loc[2]:loc[3]]
			// A real Markdown link or reference definition ends in ( or :.
			if end := loc[1]; end < len(stripped) {
				if c := stripped[end]; c == '(' || c == ':' || c == '[' {
					continue
				}
			}
			if defined[name] {
				continue // reference-style link, defined elsewhere in the file
			}
			// Require mixed case, so prose like [NEW] or [TODO] is left alone
			// and only Go-identifier-shaped tokens are flagged.
			if !hasLowerRe.MatchString(name) {
				continue
			}
			t.Errorf("%s: %q is godoc link syntax; in Markdown it renders as literal "+
				"brackets. Use a real link, or plain backticks.", f, "["+name+"]")
		}
	}
}
