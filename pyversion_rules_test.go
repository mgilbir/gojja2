// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Each rule in value/pyversion.go documents the corpus case that pins it, which
// is the only thing separating a rule from an assertion nobody checks. This
// makes the annotation load-bearing: a rule with no case named, or naming one
// the corpus does not have, fails here.
//
// It exists because the opposite happened. `1.0 % 0` is worded three ways
// across the four interpreters, gojja2 knew about two of them, and the corpus
// reached `1 % 0` but never `1 % 0.0` -- so the version that was wrong had
// nothing to answer for. The rule that replaced it names a case that exists,
// and now so must every other.
var corpusRef = regexp.MustCompile(`[a-z][a-z0-9_-]*/[a-z0-9_]+`)

// Corpora built by `make import` from third-party suites. They are gitignored,
// so a rule may cite one but this cannot check it.
var generatedRoots = map[string]bool{
	"minijinja": true, "minja": true, "llamacpp": true, "chat-templates": true,
	"cookiecutter": true, "wild": true, "jinja-harvest": true,
}

func TestEveryVersionRuleNamesACorpusCase(t *testing.T) {
	const src = "value/pyversion.go"
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, src, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse %s: %v", src, err)
	}

	// Only the methods below the "the rules" banner are rules; String, Known
	// and AtLeast above it describe the type itself.
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read %s: %v", src, err)
	}
	banner := strings.Index(string(raw), "// --- the rules ---")
	if banner < 0 {
		t.Fatal("value/pyversion.go has no rules banner; this test cannot tell " +
			"a rule from a helper")
	}
	firstRule := fset.File(f.Pos()).Pos(banner)

	checked := 0
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Pos() < firstRule || fn.Recv == nil || !fn.Name.IsExported() {
			continue
		}
		checked++
		doc := fn.Doc.Text()
		_, after, found := strings.Cut(doc, "Corpus:")
		if !found {
			t.Errorf("%s has no \"Corpus:\" line. Name the case that pins it, or "+
				"the rule is a claim about CPython that nothing checks.", fn.Name.Name)
			continue
		}
		names := corpusRef.FindAllString(after, -1)
		if len(names) == 0 {
			t.Errorf("%s's Corpus: line names no case", fn.Name.Name)
			continue
		}
		for _, n := range names {
			if generatedRoots[n[:strings.IndexByte(n, '/')]] {
				continue // built by `make import`, not committed
			}
			if _, err := os.Stat(filepath.Join("testdata/corpus", n+".jj2")); err != nil {
				t.Errorf("%s names corpus case %q, which does not exist. Add it to "+
					"tools/oracle/gen_corpus.py and run `make oracle`.", fn.Name.Name, n)
			}
		}
	}
	if checked == 0 {
		t.Fatal("found no rules below the banner; the test is reading the wrong thing")
	}
	t.Logf("%d version rules, each naming a corpus case that exists", checked)
}
