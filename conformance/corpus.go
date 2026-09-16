// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

// Package conformance loads the oracle corpus and grades gojja2 against it.
//
// Every case is a template plus a context; the expected result is whatever
// CPython's jinja2 produced for it, recorded by tools/oracle/oracle.py. The
// corpus format is deliberately MiniJinja's, so its fixtures import without
// rewriting -- but the answers are always jinja2's.
package conformance

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"strings"

	"github.com/mgilbir/gojja2"
	"github.com/mgilbir/gojja2/value"
)

// Separator divides a case's JSON header from its template source.
const Separator = "\n---\n"

// Case is one corpus entry.
type Case struct {
	// Rel is the case's path relative to the corpus root, and its name.
	Rel string
	// Context is the render context.
	Context map[string]value.Value
	// Templates are extra templates the case can include or extend.
	Templates map[string]string
	// Settings are the environment options the case runs under.
	Settings Settings
	// Source is the main template.
	Source string
}

// Settings mirrors the environment options a case may set. The field names are
// the jinja2 keyword arguments, so a case reads the same in both languages.
type Settings struct {
	BlockStart          string `json:"block_start_string"`
	BlockEnd            string `json:"block_end_string"`
	VariableStart       string `json:"variable_start_string"`
	VariableEnd         string `json:"variable_end_string"`
	CommentStart        string `json:"comment_start_string"`
	CommentEnd          string `json:"comment_end_string"`
	LineStatementPrefix string `json:"line_statement_prefix"`
	LineCommentPrefix   string `json:"line_comment_prefix"`
	TrimBlocks          bool   `json:"trim_blocks"`
	LstripBlocks        bool   `json:"lstrip_blocks"`
	NewlineSequence     string `json:"newline_sequence"`
	KeepTrailingNewline bool   `json:"keep_trailing_newline"`
	Autoescape          bool   `json:"autoescape"`
	Undefined           string `json:"undefined"`
	// Extensions names the optional tags the case needs, e.g. "do".
	Extensions []string `json:"extensions"`
}

// Golden is the oracle's recorded answer for a case.
type Golden struct {
	Case   string         `json:"case"`
	Oracle map[string]any `json:"oracle"`
	OK     bool           `json:"ok"`
	Output string         `json:"output"`
	Error  *GoldenError   `json:"error"`
}

// GoldenError is the exception CPython raised.
type GoldenError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Lineno  int    `json:"lineno"`
	Name    string `json:"name"`
}

// LoadCase reads one .jj2 case file.
func LoadCase(root, path string) (*Case, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return nil, err
	}

	text := string(raw)
	header, source, found := strings.Cut(text, Separator)
	if !found {
		header, source = "{}", text
	}

	var fields map[string]json.RawMessage
	if strings.TrimSpace(header) != "" {
		if err := json.Unmarshal([]byte(header), &fields); err != nil {
			return nil, fmt.Errorf("%s: bad JSON header: %w", rel, err)
		}
	}

	c := &Case{Rel: filepath.ToSlash(rel), Source: source}
	if raw, ok := fields["__settings__"]; ok {
		if err := json.Unmarshal(raw, &c.Settings); err != nil {
			return nil, fmt.Errorf("%s: bad __settings__: %w", c.Rel, err)
		}
		delete(fields, "__settings__")
	}
	if raw, ok := fields["__templates__"]; ok {
		if err := json.Unmarshal(raw, &c.Templates); err != nil {
			return nil, fmt.Errorf("%s: bad __templates__: %w", c.Rel, err)
		}
		delete(fields, "__templates__")
	}

	c.Context = make(map[string]value.Value, len(fields))
	for name, raw := range fields {
		v, err := fromJSON(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: context %q: %w", c.Rel, name, err)
		}
		c.Context[name] = v
	}
	return c, nil
}

// fromJSON decodes a context value.
//
// It is written against the token stream rather than unmarshalled into `any`
// for two reasons, both of which are observable in rendered output: the
// int/float distinction would be flattened to float64, so `{{ 1 }}` and
// `{{ 1.0 }}` would render the same; and object key order would be lost, so a
// dict would sort differently here than in CPython, which preserves insertion
// order.

func fromJSON(raw json.RawMessage) (value.Value, error) {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	v, err := decodeValue(dec)
	if err != nil {
		return value.Undefined, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return value.Undefined, fmt.Errorf("trailing content after JSON value")
	}
	return v, nil
}

func decodeValue(dec *json.Decoder) (value.Value, error) {
	tok, err := dec.Token()
	if err != nil {
		return value.Undefined, err
	}
	return decodeFrom(dec, tok)
}

func decodeFrom(dec *json.Decoder, tok json.Token) (value.Value, error) {
	switch t := tok.(type) {
	case nil:
		return value.None, nil
	case bool:
		return value.Bool(t), nil
	case string:
		return value.String(t), nil
	case json.Number:
		// A number with no fraction or exponent is a Python int, and
		// int and float do not render alike.
		if !strings.ContainsAny(t.String(), ".eE") {
			if i, err := t.Int64(); err == nil {
				return value.Int(i), nil
			}
			if b, ok := new(big.Int).SetString(t.String(), 10); ok {
				return value.BigInt(b), nil
			}
		}
		f, err := t.Float64()
		if err != nil {
			return value.Undefined, err
		}
		return value.Float(f), nil
	case json.Delim:
		switch t {
		case '[':
			var items []value.Value
			for dec.More() {
				item, err := decodeValue(dec)
				if err != nil {
					return value.Undefined, err
				}
				items = append(items, item)
			}
			if _, err := dec.Token(); err != nil { // closing ]
				return value.Undefined, err
			}
			return value.NewList(items...), nil
		case '{':
			out := value.NewDict()
			d, _ := out.Dict()
			for dec.More() {
				keyTok, err := dec.Token()
				if err != nil {
					return value.Undefined, err
				}
				key, ok := keyTok.(string)
				if !ok {
					return value.Undefined, fmt.Errorf("object key is not a string")
				}
				val, err := decodeValue(dec)
				if err != nil {
					return value.Undefined, err
				}
				d.SetString(key, val)
			}
			if _, err := dec.Token(); err != nil { // closing }
				return value.Undefined, err
			}
			return out, nil
		}
	}
	return value.Undefined, fmt.Errorf("unsupported JSON token %v", tok)
}

// DecodeContext decodes a JSON object into template values, preserving key
// order and the int/float distinction. It is how the fuzzer hands gojja2 the
// same bytes the oracle is given.
func DecodeContext(raw json.RawMessage) (map[string]value.Value, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	out := make(map[string]value.Value, len(fields))
	for name, field := range fields {
		v, err := fromJSON(field)
		if err != nil {
			return nil, fmt.Errorf("context %q: %w", name, err)
		}
		out[name] = v
	}
	return out, nil
}

// Environment builds the environment a case runs under.
func (c *Case) Environment() *gojja2.Environment {
	sources := make(map[string]string, len(c.Templates)+1)
	for name, src := range c.Templates {
		sources[name] = src
	}
	sources[c.Rel] = c.Source

	opts := []gojja2.Option{gojja2.WithLoader(gojja2.DictLoader(sources))}
	s := c.Settings
	if s.BlockStart != "" || s.BlockEnd != "" {
		opts = append(opts, gojja2.WithBlockDelimiters(s.BlockStart, s.BlockEnd))
	}
	if s.VariableStart != "" || s.VariableEnd != "" {
		opts = append(opts, gojja2.WithVariableDelimiters(s.VariableStart, s.VariableEnd))
	}
	if s.CommentStart != "" || s.CommentEnd != "" {
		opts = append(opts, gojja2.WithCommentDelimiters(s.CommentStart, s.CommentEnd))
	}
	if s.LineStatementPrefix != "" {
		opts = append(opts, gojja2.WithLineStatementPrefix(s.LineStatementPrefix))
	}
	if s.LineCommentPrefix != "" {
		opts = append(opts, gojja2.WithLineCommentPrefix(s.LineCommentPrefix))
	}
	if s.NewlineSequence != "" {
		opts = append(opts, gojja2.WithNewlineSequence(s.NewlineSequence))
	}
	opts = append(opts,
		gojja2.WithTrimBlocks(s.TrimBlocks),
		gojja2.WithLstripBlocks(s.LstripBlocks),
		gojja2.WithKeepTrailingNewline(s.KeepTrailingNewline),
		gojja2.WithAutoescape(s.Autoescape),
		gojja2.WithUndefined(undefinedBehavior(s.Undefined)),
	)
	if len(s.Extensions) > 0 {
		opts = append(opts, gojja2.WithExtensions(s.Extensions...))
	}
	return gojja2.New(opts...)
}

func undefinedBehavior(name string) value.UndefinedBehavior {
	switch name {
	case "strict":
		return value.UndefinedStrict
	case "chainable":
		return value.UndefinedChainable
	case "debug":
		return value.UndefinedDebug
	default:
		return value.UndefinedDefault
	}
}

// Render runs the case and reports what gojja2 produced.
func (c *Case) Render() (string, error) {
	env := c.Environment()
	tmpl, err := env.GetTemplate(c.Rel)
	if err != nil {
		return "", err
	}
	var out strings.Builder
	if err := tmpl.RenderValues(context.Background(), &out, c.Context); err != nil {
		return "", err
	}
	return out.String(), nil
}

// LoadGolden reads the oracle's answer for a case.
func LoadGolden(goldenRoot, rel string) (*Golden, error) {
	path := filepath.Join(goldenRoot, strings.TrimSuffix(rel, ".jj2")+".json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var g Golden
	if err := json.Unmarshal(raw, &g); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &g, nil
}

// Collect lists the case files under a corpus root.
func Collect(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".jj2") {
			out = append(out, path)
		}
		return nil
	})
	if os.IsNotExist(err) {
		return nil, nil
	}
	return out, err
}
