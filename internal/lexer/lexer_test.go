// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package lexer_test

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/mgilbir/gojja2/errs"
	"github.com/mgilbir/gojja2/internal/lexer"
	"github.com/mgilbir/gojja2/value"
)

// jinjaTypeNames maps gojja2 token kinds onto the type names jinja2's lexer
// reports, which is what the corpus records.
var jinjaTypeNames = map[lexer.Kind]string{
	lexer.Data:          "data",
	lexer.BlockBegin:    "block_begin",
	lexer.BlockEnd:      "block_end",
	lexer.VariableBegin: "variable_begin",
	lexer.VariableEnd:   "variable_end",
	lexer.Name:          "name",
	lexer.String:        "string",
	lexer.Integer:       "integer",
	lexer.Float:         "float",
	lexer.Add:           "add",
	lexer.Sub:           "sub",
	lexer.Div:           "div",
	lexer.FloorDiv:      "floordiv",
	lexer.Mul:           "mul",
	lexer.Mod:           "mod",
	lexer.Pow:           "pow",
	lexer.Tilde:         "tilde",
	lexer.LBracket:      "lbracket",
	lexer.RBracket:      "rbracket",
	lexer.LParen:        "lparen",
	lexer.RParen:        "rparen",
	lexer.LBrace:        "lbrace",
	lexer.RBrace:        "rbrace",
	lexer.Eq:            "eq",
	lexer.Ne:            "ne",
	lexer.Gt:            "gt",
	lexer.Gteq:          "gteq",
	lexer.Lt:            "lt",
	lexer.Lteq:          "lteq",
	lexer.Assign:        "assign",
	lexer.Dot:           "dot",
	lexer.Colon:         "colon",
	lexer.Pipe:          "pipe",
	lexer.Comma:         "comma",
	lexer.Semicolon:     "semicolon",
}

type lexCase struct {
	Src    string              `json:"src"`
	Syntax map[string]any      `json:"syntax"`
	Tokens [][]json.RawMessage `json:"tokens"`
	Err    string              `json:"err"`
	Msg    string              `json:"msg"`
	Line   int                 `json:"line"`
}

// syntaxFrom rebuilds a Syntax from the Environment options the corpus
// recorded. An unrecognised option fails the test rather than being ignored,
// so a new setting cannot silently go untested.
func syntaxFrom(t *testing.T, opts map[string]any) lexer.Syntax {
	t.Helper()
	syn := lexer.DefaultSyntax()
	for k, v := range opts {
		switch k {
		case "block_start_string":
			syn.BlockStart = v.(string)
		case "block_end_string":
			syn.BlockEnd = v.(string)
		case "variable_start_string":
			syn.VariableStart = v.(string)
		case "variable_end_string":
			syn.VariableEnd = v.(string)
		case "comment_start_string":
			syn.CommentStart = v.(string)
		case "comment_end_string":
			syn.CommentEnd = v.(string)
		case "line_statement_prefix":
			syn.LineStatementPrefix = v.(string)
		case "line_comment_prefix":
			syn.LineCommentPrefix = v.(string)
		case "newline_sequence":
			syn.NewlineSequence = v.(string)
		case "trim_blocks":
			syn.TrimBlocks = v.(bool)
		case "lstrip_blocks":
			syn.LstripBlocks = v.(bool)
		case "keep_trailing_newline":
			syn.KeepTrailingNewline = v.(bool)
		default:
			t.Fatalf("corpus uses unsupported lexer setting %q", k)
		}
	}
	return syn
}

func loadLexCorpus(t *testing.T) []lexCase {
	t.Helper()
	f, err := os.Open("testdata/lex.jsonl")
	if err != nil {
		t.Fatalf("open corpus: %v", err)
	}
	defer func() { _ = f.Close() }()

	var cases []lexCase
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for sc.Scan() {
		var c lexCase
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

// renderToken formats a gojja2 token the way the corpus records a jinja2 one,
// so the two can be compared as plain strings.
func renderToken(t *testing.T, tok lexer.Token) (string, error) {
	name, ok := jinjaTypeNames[tok.Kind]
	if !ok {
		t.Fatalf("no jinja2 type name for token kind %v", tok.Kind)
	}
	text := tok.Value
	switch tok.Kind {
	case lexer.Integer:
		v, err := lexer.ParseInteger(tok.Value)
		if err != nil {
			return "", err
		}
		text = value.Repr(v)
	case lexer.Float:
		v, err := lexer.ParseFloat(tok.Value)
		if err != nil {
			return "", err
		}
		text = value.Repr(v)
	}
	return fmt.Sprintf("%d %s %q", tok.Line, name, text), nil
}

func expectedToken(t *testing.T, row []json.RawMessage) string {
	t.Helper()
	if len(row) != 3 {
		t.Fatalf("malformed corpus token %v", row)
	}
	var line int
	var name, text string
	if err := json.Unmarshal(row[0], &line); err != nil {
		t.Fatalf("token line: %v", err)
	}
	if err := json.Unmarshal(row[1], &name); err != nil {
		t.Fatalf("token type: %v", err)
	}
	if err := json.Unmarshal(row[2], &text); err != nil {
		t.Fatalf("token value: %v", err)
	}
	return fmt.Sprintf("%d %s %q", line, name, text)
}

func TestLexerMatchesJinja2(t *testing.T) {
	cases := loadLexCorpus(t)
	errorCases := 0

	for _, c := range cases {
		name := fmt.Sprintf("%q", c.Src)
		if len(c.Syntax) > 0 {
			keys := make([]string, 0, len(c.Syntax))
			for k := range c.Syntax {
				keys = append(keys, k)
			}
			name += " " + strings.Join(keys, ",")
		}

		t.Run(name, func(t *testing.T) {
			syn := syntaxFrom(t, c.Syntax)
			tokens, err := lexer.Tokenize(syn, c.Src, "<lex>")

			if c.Err != "" {
				errorCases++
				if err == nil {
					t.Fatalf("jinja2 raises %s: %s\ngojja2 lexed %d tokens without error",
						c.Err, c.Msg, len(tokens))
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
				t.Fatalf("gojja2 failed to lex: %v (jinja2 produced %d tokens)",
					err, len(c.Tokens))
			}

			var got []string
			for _, tok := range tokens {
				if tok.Kind == lexer.EOF {
					break
				}
				s, err := renderToken(t, tok)
				if err != nil {
					t.Fatalf("render token %v: %v", tok, err)
				}
				got = append(got, s)
			}
			want := make([]string, len(c.Tokens))
			for i, row := range c.Tokens {
				want[i] = expectedToken(t, row)
			}

			if len(got) != len(want) {
				t.Errorf("token count: got %d, jinja2 gives %d", len(got), len(want))
			}
			for i := range max(len(got), len(want)) {
				g, w := "<missing>", "<missing>"
				if i < len(got) {
					g = got[i]
				}
				if i < len(want) {
					w = want[i]
				}
				if g != w {
					t.Errorf("token %d:\n  gojja2: %s\n  jinja2: %s", i, g, w)
				}
			}
		})
	}
	t.Logf("checked %d lexer cases", len(cases))
}
