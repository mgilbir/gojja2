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
	// contextJSON is the render context, still undecoded. It is not a
	// decoded map, and that is deliberate: see [Case.Context].
	contextJSON map[string]json.RawMessage
	// Templates are extra templates the case can include or extend.
	Templates map[string]string
	// Settings are the environment options the case runs under.
	Settings Settings
	// Profile names the environment profile the case renders under, empty
	// for a bare environment. See profile.go.
	Profile string
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
	// The profile is read first, because it seeds the settings a case may
	// then override. Unmarshalling onto the seeded struct gives the same
	// "case wins, key by key" merge the oracle does with dict.update.
	if raw, ok := fields["__profile__"]; ok {
		if err := json.Unmarshal(raw, &c.Profile); err != nil {
			return nil, fmt.Errorf("%s: bad __profile__: %w", c.Rel, err)
		}
		seed, known := profileSettings(c.Profile)
		if !known {
			return nil, fmt.Errorf("%s: unknown __profile__ %q", c.Rel, c.Profile)
		}
		c.Settings = seed
		delete(fields, "__profile__")
	}
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

	c.contextJSON = fields
	// Decoded once here so a malformed case fails at load rather than at
	// the first render. The values are discarded; Context decodes its own.
	if _, err := c.Context(); err != nil {
		return nil, err
	}
	return c, nil
}

// Context decodes the case's render context, fresh on every call.
//
// Returning a new map of new values each time is the whole point, and costs a
// small JSON decode to get. A render can *mutate* what it is given --
// `{% set _ = lst.append(9) %}`, `{% set _ = d.update(x) %}` and
// `{% set d.v %}...{% endset %}` all write through, and
// [gojja2.Template.RenderValues] skips the conversion that would otherwise
// protect the caller -- so a context shared between two renders carries
// whatever the first one did to it into the second.
//
// That has gone wrong three times in this package, in three different shapes:
// a field decoded once in a constructor, and twice a shallow copy of such a
// field, which copies the map and shares the values it holds. None of the
// three looks wrong at the call site, and all three fail the same way: not by
// erroring, but by quietly comparing against a context an earlier template had
// already edited. Handing out a decoded map at all is what made them writable,
// so this does not.
//
// TestRenderingACaseTwiceGivesTheSameAnswer is the guard.
func (c *Case) Context() (map[string]value.Value, error) {
	out := make(map[string]value.Value, len(c.contextJSON))
	for name, raw := range c.contextJSON {
		v, err := fromJSON(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: context %q: %w", c.Rel, name, err)
		}
		out[name] = v
	}
	return out, nil
}

// fromJSON decodes a context value.
//
// It is written against the token stream rather than unmarshalled into `any`
// for two reasons, both of which are observable in rendered output: the
// int/float distinction would be flattened to float64, so `{{ 1 }}` and
// `{{ 1.0 }}` would render the same; and object key order would be lost, so a
// dict would sort differently here than in CPython, which preserves insertion
// order.
//
// The same reasoning is why a context never travels as a Go map: a map has no
// order to preserve, and the standard library does not agree with itself about
// what to do with that -- encoding/json sorts the keys, encoding/json/v2
// randomises them. Anywhere a context crosses a boundary it does so as raw
// JSON text.
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

// Environment builds the environment a case runs under, reproducing the
// default interpreter.
func (c *Case) Environment() (*gojja2.Environment, error) {
	return c.EnvironmentFor(gojja2.DefaultPythonVersion)
}

// EnvironmentFor is Environment for one interpreter version, which is what
// grading the whole matrix needs: a case whose answer moved between CPython
// releases has a golden per version, and the engine has to be told which one
// it is being asked to reproduce.
func (c *Case) EnvironmentFor(py gojja2.PythonVersion) (*gojja2.Environment, error) {
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
	opts = append(opts, gojja2.WithPythonVersion(py))
	env, err := gojja2.New(opts...)
	if err != nil {
		return nil, err
	}
	applyProfile(env, c.Profile)
	return env, nil
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
	return c.RenderFor(gojja2.DefaultPythonVersion)
}

// RenderFor is Render reproducing one interpreter version.
func (c *Case) RenderFor(py gojja2.PythonVersion) (string, error) {
	env, err := c.EnvironmentFor(py)
	if err != nil {
		return "", err
	}
	tmpl, err := env.GetTemplate(c.Rel)
	if err != nil {
		return "", err
	}
	vars, err := c.Context()
	if err != nil {
		return "", err
	}
	var out strings.Builder
	if err := tmpl.RenderValues(context.Background(), &out, vars); err != nil {
		return "", err
	}
	return out.String(), nil
}

// RenderViaGo runs the case through Template.Render, the entry point that
// takes the caller's own Go map.
//
// It is a different path from Render above: RenderValues is handed values that
// are already converted, while Render carries the map unconverted and lets the
// argument scope convert one name at a time. The whole corpus grades the first
// path, so without this the second -- the one every caller actually uses --
// would be exercised only by the unit tests.
//
// A value.Value passes through the conversion unchanged, so the two paths are
// being given the same context and must agree on every case.
// HasOrderedDict reports a context holding a dictionary of more than one key,
// anywhere inside it.
//
// Such a context cannot be handed over as Go values and come back the same. A
// Python dict keeps its insertion order and a Go map has none, so this engine
// sorts the keys -- a deliberate divergence, recorded in docs/divergences.md,
// and the whole of the difference between the two render paths for fourteen of
// the committed cases. One key cannot be out of order, so the question is only
// about two or more.
func (c *Case) HasOrderedDict() bool {
	vars, err := c.Context()
	if err != nil {
		return false
	}
	for _, v := range vars {
		if holdsOrderedDict(v, 0) {
			return true
		}
	}
	return false
}

func holdsOrderedDict(v value.Value, depth int) bool {
	if depth > 32 {
		return false
	}
	switch v.Kind() {
	case value.KindDict:
		d, _ := v.Dict()
		if len(d.Keys()) > 1 {
			return true
		}
		for _, k := range d.Keys() {
			val, _ := d.GetKnown(k)
			if holdsOrderedDict(val, depth+1) {
				return true
			}
		}
	case value.KindList, value.KindTuple:
		s, _ := v.Seq()
		for _, item := range s.Items() {
			if holdsOrderedDict(item, depth+1) {
				return true
			}
		}
	}
	return false
}

func (c *Case) RenderViaGo() (string, error) {
	env, err := c.Environment()
	if err != nil {
		return "", err
	}
	tmpl, err := env.GetTemplate(c.Rel)
	if err != nil {
		return "", err
	}
	// Native Go values, not the case's own value.Values.
	//
	// Converting a Value returns it unchanged, so handing them straight
	// over exercises the lazy per-name scope path and nothing of the
	// Go-to-value conversion beneath it -- which is where the worst defect
	// in this engine lived, and where a corpus of 869 cases was looking at
	// nothing at all.
	values, err := c.Context()
	if err != nil {
		return "", err
	}
	vars := make(map[string]any, len(values))
	for k, v := range values {
		vars[k] = value.ToGo(v)
	}
	var out strings.Builder
	if err := tmpl.Render(context.Background(), &out, vars); err != nil {
		return "", err
	}
	return out.String(), nil
}

// LoadGolden reads the oracle's answer for a case.
func LoadGolden(goldenRoot, rel string) (*Golden, error) {
	return LoadGoldenOver(goldenRoot, "", rel)
}

// LoadGoldenOver reads the oracle's answer for a case, preferring an override
// recorded for one interpreter version.
//
// Only 66 of 2,241 cases answer differently across CPython 3.11 to 3.14, so a
// non-pinned version is stored as those few files rather than a second copy of
// everything: the base set is the pinned version's, and an override sits on
// top of it. An override that stops differing is a file to delete, which is a
// signal worth having.
func LoadGoldenOver(goldenRoot, overrideRoot, rel string) (*Golden, error) {
	name := strings.TrimSuffix(rel, ".jj2") + ".json"
	if overrideRoot != "" {
		if g, err := readGolden(filepath.Join(overrideRoot, name)); err == nil {
			return g, nil
		} else if !os.IsNotExist(err) {
			return nil, err
		}
	}
	return readGolden(filepath.Join(goldenRoot, name))
}

func readGolden(path string) (*Golden, error) {
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
