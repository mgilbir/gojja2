// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package parser_test

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/mgilbir/gojja2/errs"
	"github.com/mgilbir/gojja2/internal/ast"
	"github.com/mgilbir/gojja2/internal/lexer"
	"github.com/mgilbir/gojja2/internal/parser"
	"github.com/mgilbir/gojja2/value"
)

// dump renders a node as the same S-expression tools/oracle/gen_parse.py
// produces from jinja2's tree. Strings go through value.Repr, which is already
// known to match CPython's repr exactly, so the two formatters cannot disagree
// about quoting.
func dump(n any) string {
	switch n := n.(type) {
	case nil:
		return "nil"
	case bool:
		if n {
			return "True"
		}
		return "False"
	case string:
		return value.Repr(value.String(n))

	case *ast.Template:
		return node("template", seqStmt(n.Body))
	case *ast.Output:
		return node("output", seqExpr(n.Nodes))
	case *ast.TemplateData:
		return node("templatedata", dump(n.Data))
	case *ast.Const:
		return node("const", value.Repr(n.Value))
	case *ast.Name:
		return node("name", dump(n.Name), dump(n.Store))
	case *ast.NSRef:
		return node("nsref", dump(n.Name), dump(n.Attr))
	case *ast.Tuple:
		return node("tuple", seqExpr(n.Items))
	case *ast.List:
		return node("list", seqExpr(n.Items))
	case *ast.Dict:
		parts := make([]string, len(n.Items))
		for i, p := range n.Items {
			parts[i] = dump(p)
		}
		return node("dict", "["+strings.Join(parts, " ")+"]")
	case *ast.Pair:
		return node("pair", dump(n.Key), dump(n.Value))
	case *ast.Keyword:
		return node("keyword", dump(n.Key), dump(n.Value))
	case *ast.CondExpr:
		return node("condexpr", dump(n.Test), dump(n.True), dump(n.False))
	case *ast.BinOp:
		return node("binop", n.Op.String(), dump(n.Left), dump(n.Right))
	case *ast.UnaryOp:
		return node("unaryop", n.Op.String(), dump(n.Node))
	case *ast.Concat:
		return node("concat", seqExpr(n.Nodes))
	case *ast.Compare:
		parts := make([]string, len(n.Ops))
		for i, op := range n.Ops {
			parts[i] = dump(op)
		}
		return node("compare", dump(n.Expr), "["+strings.Join(parts, " ")+"]")
	case *ast.Operand:
		return node("operand", dump(n.Op), dump(n.Expr))
	case *ast.Getattr:
		return node("getattr", dump(n.Node), dump(n.Attr))
	case *ast.Getitem:
		return node("getitem", dump(n.Node), dump(n.Arg))
	case *ast.Slice:
		return node("slice", dump(n.Start), dump(n.Stop), dump(n.Step))
	case *ast.Call:
		return node("call", append([]string{dump(n.Node)}, callArgs(n.Args)...)...)
	case *ast.Filter:
		return node("filter",
			append([]string{dump(n.Node), dump(n.Name)}, callArgs(n.Args)...)...)
	case *ast.Test:
		return node("test",
			append([]string{dump(n.Node), dump(n.Name)}, callArgs(n.Args)...)...)

	case *ast.For:
		return node("for", dump(n.Target), dump(n.Iter), seqStmt(n.Body),
			seqStmt(n.Else), dump(n.Test), dump(n.Recursive))
	case *ast.If:
		parts := make([]string, len(n.Elif))
		for i, e := range n.Elif {
			parts[i] = dump(e)
		}
		return node("if", dump(n.Test), seqStmt(n.Body),
			"["+strings.Join(parts, " ")+"]", seqStmt(n.Else))
	case *ast.Assign:
		return node("assign", dump(n.Target), dump(n.Node))
	case *ast.AssignBlock:
		return node("assignblock", dump(n.Target), dump(n.Filter), seqStmt(n.Body))
	case *ast.Macro:
		return node("macro", dump(n.Name), seqNames(n.Args), seqExpr(n.Defaults),
			seqStmt(n.Body))
	case *ast.CallBlock:
		return node("callblock", dump(n.Call), seqNames(n.Args), seqExpr(n.Defaults),
			seqStmt(n.Body))
	case *ast.FilterBlock:
		return node("filterblock", seqStmt(n.Body), dump(n.Filter))
	case *ast.With:
		return node("with", seqExpr(n.Targets), seqExpr(n.Values), seqStmt(n.Body))
	case *ast.Block:
		return node("block", dump(n.Name), seqStmt(n.Body), dump(n.Scoped), dump(n.Required))
	case *ast.Extends:
		return node("extends", dump(n.Template))
	case *ast.Include:
		return node("include", dump(n.Template), dump(n.WithContext), dump(n.IgnoreMissing))
	case *ast.Import:
		return node("import", dump(n.Template), dump(n.Target), dump(n.WithContext))
	case *ast.FromImport:
		parts := make([]string, len(n.Names))
		for i, e := range n.Names {
			parts[i] = node("alias", dump(e.Name), dump(e.Alias))
		}
		return node("fromimport", dump(n.Template),
			"["+strings.Join(parts, " ")+"]", dump(n.WithContext))
	case *ast.ExprStmt:
		return node("exprstmt", dump(n.Node))
	case *ast.AutoescapeBlock:
		return node("autoescape", dump(n.Value), seqStmt(n.Body))
	case *ast.Scope:
		return node("scope", seqStmt(n.Body))
	case *ast.Break:
		return node("break")
	case *ast.Continue:
		return node("continue")
	}
	panic(fmt.Sprintf("no dump for %T", n))
}

func node(name string, fields ...string) string {
	return "(" + strings.Join(append([]string{name}, fields...), " ") + ")"
}

func callArgs(a ast.Args) []string {
	kwargs := make([]string, len(a.Kwargs))
	for i, kw := range a.Kwargs {
		kwargs[i] = dump(kw)
	}
	return []string{
		seqExpr(a.Args),
		"[" + strings.Join(kwargs, " ") + "]",
		dump(a.DynArgs),
		dump(a.DynKwargs),
	}
}

func seqStmt(body []ast.Stmt) string {
	parts := make([]string, len(body))
	for i, s := range body {
		parts[i] = dump(s)
	}
	return "[" + strings.Join(parts, " ") + "]"
}

func seqExpr(items []ast.Expr) string {
	parts := make([]string, len(items))
	for i, e := range items {
		parts[i] = dump(e)
	}
	return "[" + strings.Join(parts, " ") + "]"
}

func seqNames(names []*ast.Name) string {
	parts := make([]string, len(names))
	for i, n := range names {
		parts[i] = dump(n)
	}
	return "[" + strings.Join(parts, " ") + "]"
}

// dump must be given a typed nil as an untyped one, or the `case nil` arm
// never fires for a nil ast.Expr held in an interface.
func init() {
	// A nil ast.Expr arrives as a nil interface from the parser, so the
	// switch's nil case handles it; this is asserted rather than assumed.
	var e ast.Expr
	if dump(e) != "nil" {
		panic("dump does not render a nil expression as nil")
	}
}

type parseCase struct {
	Src  string          `json:"src"`
	Opts map[string]bool `json:"opts"`
	AST  string          `json:"ast"`
	Err  string          `json:"err"`
	Msg  string          `json:"msg"`
	Line int             `json:"line"`
}

func loadParseCorpus(t *testing.T) []parseCase {
	t.Helper()
	f, err := os.Open("testdata/parse.jsonl")
	if err != nil {
		t.Fatalf("open corpus: %v", err)
	}
	defer f.Close()

	var cases []parseCase
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for sc.Scan() {
		var c parseCase
		if err := json.Unmarshal(sc.Bytes(), &c); err != nil {
			t.Fatalf("parse corpus line: %v", err)
		}
		cases = append(cases, c)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read corpus: %v", err)
	}
	if len(cases) == 0 {
		t.Fatal("corpus is empty")
	}
	return cases
}

func TestParserMatchesJinja2(t *testing.T) {
	cases := loadParseCorpus(t)
	for _, c := range cases {
		t.Run(fmt.Sprintf("%q", c.Src), func(t *testing.T) {
			opts := parser.Options{Do: c.Opts["do"], LoopControls: c.Opts["loopcontrols"]}
			tree, err := parser.Parse(lexer.DefaultSyntax(), opts, c.Src, "<parse>")

			if c.Err != "" {
				if err == nil {
					t.Fatalf("jinja2 raises %s: %s\ngojja2 parsed it as\n  %s",
						c.Err, c.Msg, dump(tree))
				}
				if got := errs.KindOf(err).String(); got != c.Err {
					t.Errorf("error class: got %s, jinja2 raises %s", got, c.Err)
				}
				if err.Error() != c.Msg {
					t.Errorf("error message:\n  gojja2: %s\n  jinja2: %s", err.Error(), c.Msg)
				}
				var e *errs.Error
				if errors.As(err, &e) && e.Line != c.Line {
					t.Errorf("error line: got %d, jinja2 reports %d", e.Line, c.Line)
				}
				return
			}

			if err != nil {
				t.Fatalf("gojja2 failed to parse: %v\njinja2 gives\n  %s", err, c.AST)
			}
			if got := dump(tree); got != c.AST {
				t.Errorf("AST differs:\n  gojja2: %s\n  jinja2: %s", got, c.AST)
			}
		})
	}
	t.Logf("checked %d parser cases", len(cases))
}
