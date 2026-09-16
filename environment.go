// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

// Package gojja2 is a pure Go implementation of the Jinja2 template language,
// built to render exactly what CPython's jinja2 renders.
package gojja2

import (
	"strings"
	"sync"

	"github.com/mgilbir/gojja2/errs"
	"github.com/mgilbir/gojja2/internal/ast"
	"github.com/mgilbir/gojja2/internal/lexer"
	"github.com/mgilbir/gojja2/internal/parser"
	"github.com/mgilbir/gojja2/value"
)

// Filter is a value transformer invoked by `x|name(...)`.
type Filter func(s *State, v value.Value, args *value.CallArgs) (value.Value, error)

// Test is a predicate invoked by `x is name(...)`.
type Test func(s *State, v value.Value, args *value.CallArgs) (bool, error)

// Environment holds the configuration templates are compiled and rendered
// under: syntax, loader, autoescaping policy, and the filter, test and global
// registries.
//
// An Environment is safe for concurrent use once configured. Registering
// filters or globals after templates are in flight is not.
type Environment struct {
	syntax    lexer.Syntax
	parseOpts parser.Options
	loader    Loader

	undefined value.UndefinedBehavior
	// autoescape decides per template name. nil means never.
	autoescape func(name string) bool
	// finalize post-processes every printed value.
	finalize func(value.Value) value.Value

	filters map[string]Filter
	tests   map[string]Test
	globals map[string]value.Value

	// policies mirror jinja2's environment policies, which some filters
	// read for their defaults.
	policies Policies

	// maxRecursion bounds include/extends/macro nesting. It is a safety
	// control, not a tuning knob: without it a self-including template
	// takes the process down.
	maxRecursion int

	cacheMu sync.RWMutex
	cache   map[string]*Template
}

// Policies are the filter defaults jinja2 keeps in Environment.policies.
type Policies struct {
	// URLizeRel is added to the rel attribute of every link urlize
	// generates. jinja2 defaults it to "noopener".
	URLizeRel string
	// URLizeTarget is the target attribute urlize adds, if any.
	URLizeTarget string
	// TruncateLeeway is how much longer than its limit a string may be
	// before truncate shortens it.
	TruncateLeeway int
}

func defaultPolicies() Policies {
	return Policies{URLizeRel: "noopener", TruncateLeeway: 5}
}

// Option configures an Environment.
type Option func(*Environment)

// New returns an Environment with jinja2's defaults, adjusted by opts.
func New(opts ...Option) *Environment {
	env := &Environment{
		syntax:       lexer.DefaultSyntax(),
		undefined:    value.UndefinedDefault,
		policies:     defaultPolicies(),
		maxRecursion: 100,
		filters:      make(map[string]Filter),
		tests:        make(map[string]Test),
		globals:      make(map[string]value.Value),
		cache:        make(map[string]*Template),
	}
	registerDefaultFilters(env)
	registerDefaultTests(env)
	registerDefaultGlobals(env)
	for _, opt := range opts {
		opt(env)
	}
	return env
}

// WithLoader sets where templates are loaded from.
func WithLoader(l Loader) Option { return func(e *Environment) { e.loader = l } }

// WithBlockDelimiters overrides `{%` and `%}`.
func WithBlockDelimiters(start, end string) Option {
	return func(e *Environment) { e.syntax.BlockStart, e.syntax.BlockEnd = start, end }
}

// WithVariableDelimiters overrides `{{` and `}}`.
func WithVariableDelimiters(start, end string) Option {
	return func(e *Environment) { e.syntax.VariableStart, e.syntax.VariableEnd = start, end }
}

// WithCommentDelimiters overrides `{#` and `#}`.
func WithCommentDelimiters(start, end string) Option {
	return func(e *Environment) { e.syntax.CommentStart, e.syntax.CommentEnd = start, end }
}

// WithLineStatementPrefix enables line statements, e.g. "#".
func WithLineStatementPrefix(prefix string) Option {
	return func(e *Environment) { e.syntax.LineStatementPrefix = prefix }
}

// WithLineCommentPrefix enables line comments, e.g. "##".
func WithLineCommentPrefix(prefix string) Option {
	return func(e *Environment) { e.syntax.LineCommentPrefix = prefix }
}

// WithTrimBlocks removes the first newline after a block tag.
func WithTrimBlocks(on bool) Option { return func(e *Environment) { e.syntax.TrimBlocks = on } }

// WithLstripBlocks strips indentation in front of a block tag.
func WithLstripBlocks(on bool) Option { return func(e *Environment) { e.syntax.LstripBlocks = on } }

// WithKeepTrailingNewline keeps a template's final newline.
func WithKeepTrailingNewline(on bool) Option {
	return func(e *Environment) { e.syntax.KeepTrailingNewline = on }
}

// WithNewlineSequence sets what newlines in template data render as.
func WithNewlineSequence(seq string) Option {
	return func(e *Environment) { e.syntax.NewlineSequence = seq }
}

// WithAutoescape turns HTML escaping on or off for every template.
func WithAutoescape(on bool) Option {
	return func(e *Environment) {
		if !on {
			e.autoescape = nil
			return
		}
		e.autoescape = func(string) bool { return true }
	}
}

// WithAutoescapeFunc decides escaping per template name, the way jinja2's
// select_autoescape does.
func WithAutoescapeFunc(fn func(name string) bool) Option {
	return func(e *Environment) { e.autoescape = fn }
}

// SelectAutoescape escapes templates whose name ends in one of the given
// extensions, and is the usual choice for a mixed HTML and text project.
func SelectAutoescape(extensions ...string) func(string) bool {
	if len(extensions) == 0 {
		extensions = []string{".html", ".htm", ".xml", ".xhtml"}
	}
	return func(name string) bool {
		lower := strings.ToLower(name)
		for _, ext := range extensions {
			if strings.HasSuffix(lower, ext) {
				return true
			}
		}
		return false
	}
}

// WithUndefined selects the Undefined behaviour for missing values.
func WithUndefined(b value.UndefinedBehavior) Option {
	return func(e *Environment) { e.undefined = b }
}

// WithFinalize post-processes every value before it is printed.
func WithFinalize(fn func(value.Value) value.Value) Option {
	return func(e *Environment) { e.finalize = fn }
}

// WithExtensions enables the optional tags. `do` provides `{% do %}`;
// `loopcontrols` provides `{% break %}` and `{% continue %}`.
func WithExtensions(names ...string) Option {
	return func(e *Environment) {
		for _, name := range names {
			switch name {
			case "do", "jinja2.ext.do":
				e.parseOpts.Do = true
			case "loopcontrols", "jinja2.ext.loopcontrols":
				e.parseOpts.LoopControls = true
			}
		}
	}
}

// WithMaxRecursion bounds how deeply templates may include, extend or call
// into each other. Zero restores the default.
func WithMaxRecursion(n int) Option {
	return func(e *Environment) {
		if n <= 0 {
			n = 100
		}
		e.maxRecursion = n
	}
}

// WithPolicies overrides the filter default policies.
func WithPolicies(p Policies) Option { return func(e *Environment) { e.policies = p } }

// Policies returns the environment's filter defaults.
func (e *Environment) Policies() Policies { return e.policies }

// AddFilter registers a filter, replacing any filter of the same name.
func (e *Environment) AddFilter(name string, f Filter) { e.filters[name] = f }

// AddTest registers a test, replacing any test of the same name.
func (e *Environment) AddTest(name string, t Test) { e.tests[name] = t }

// AddGlobal registers a global, replacing any global of the same name.
func (e *Environment) AddGlobal(name string, v value.Value) { e.globals[name] = v }

// Globals returns the registered globals. The map must not be mutated.
func (e *Environment) Globals() map[string]value.Value { return e.globals }

// escapes reports whether a template of the given name is autoescaped.
func (e *Environment) escapes(name string) bool {
	return e.autoescape != nil && e.autoescape(name)
}

// FromString compiles a template that has no name, and so cannot be the target
// of extends or include.
func (e *Environment) FromString(source string) (*Template, error) {
	return e.compile(source, "")
}

// FromNamedString compiles a template under a name, which decides autoescaping
// and appears in error messages.
func (e *Environment) FromNamedString(name, source string) (*Template, error) {
	return e.compile(source, name)
}

// GetTemplate loads and compiles a template by name, caching the result.
func (e *Environment) GetTemplate(name string) (*Template, error) {
	e.cacheMu.RLock()
	tmpl, ok := e.cache[name]
	e.cacheMu.RUnlock()
	if ok {
		return tmpl, nil
	}

	if e.loader == nil {
		return nil, errs.New(errs.TemplateNotFound, "%s", name)
	}
	source, err := e.loader.Load(name)
	if err != nil {
		return nil, err
	}
	tmpl, err = e.compile(source, name)
	if err != nil {
		return nil, err
	}

	e.cacheMu.Lock()
	e.cache[name] = tmpl
	e.cacheMu.Unlock()
	return tmpl, nil
}

// SelectTemplate returns the first of names that exists, which is what an
// `{% extends %}` or `{% include %}` given a list does.
func (e *Environment) SelectTemplate(names []string) (*Template, error) {
	values := make([]value.Value, len(names))
	for i, name := range names {
		values[i] = value.String(name)
	}
	return e.selectTemplateValues(values)
}

// selectTemplateValues is SelectTemplate over raw values, so that an undefined
// entry can describe itself when nothing is found.
//
// jinja2's message lists each candidate, substituting an undefined's own
// explanation for its (empty) string form -- which is what tells an author
// that the variable holding the name was never set.
func (e *Environment) selectTemplateValues(names []value.Value) (*Template, error) {
	if len(names) == 0 {
		return nil, errs.New(errs.TemplatesNotFound,
			"Tried to select from an empty list of templates.")
	}
	parts := make([]string, len(names))
	for i, name := range names {
		if name.IsUndefined() {
			parts[i] = name.UndefinedError().Error()
			continue
		}
		parts[i] = value.Str(name)
		tmpl, err := e.GetTemplate(parts[i])
		if err == nil {
			return tmpl, nil
		}
		if !errs.KindOf(err).DerivesFrom(errs.TemplateNotFound) {
			return nil, err
		}
	}
	return nil, errs.New(errs.TemplatesNotFound,
		"none of the templates given were found: %s", strings.Join(parts, ", "))
}

func (e *Environment) compile(source, name string) (*Template, error) {
	tree, err := parser.Parse(e.syntax, e.parseOpts, source, name)
	if err != nil {
		return nil, err
	}
	// Order matters: the general fold runs first, as jinja2's optimizer
	// does, and the print-specific one then catches the undefined results
	// the optimizer refuses to turn into constants.
	folder := newConstEvaluator(e, name)
	foldConstantExpressions(folder, tree.Body)
	foldConstantPrints(folder, tree.Body)
	if err := e.checkDependencies(tree.Body, name, source); err != nil {
		return nil, err
	}
	blocks, err := collectBlocks(tree.Body, name, source)
	if err != nil {
		return nil, err
	}
	return &Template{
		env:    e,
		name:   name,
		source: source,
		tree:   tree,
		blocks: blocks,
	}, nil
}

// collectBlocks indexes a template's blocks by name.
//
// The walk descends into every construct, because `{% block %}` is legal
// inside a loop or a condition: the block is still registered at template
// level, and only its rendering is affected by where it sits.
func collectBlocks(body []ast.Stmt, name, source string) (map[string]*ast.Block, error) {
	blocks := make(map[string]*ast.Block)
	var dup error
	var walk func([]ast.Stmt)
	walk = func(stmts []ast.Stmt) {
		for _, stmt := range stmts {
			switch n := stmt.(type) {
			case *ast.Block:
				if _, seen := blocks[n.Name]; seen && dup == nil {
					// Two blocks of one name would make
					// super() and overriding ambiguous.
					e := errs.New(errs.TemplateAssertionError,
						"block %s defined twice", value.Repr(value.String(n.Name)))
					e.Line, e.Name, e.Source = n.Line(), name, source
					dup = e
				}
				blocks[n.Name] = n
				walk(n.Body)
			case *ast.For:
				walk(n.Body)
				walk(n.Else)
			case *ast.If:
				walk(n.Body)
				for _, elif := range n.Elif {
					walk(elif.Body)
				}
				walk(n.Else)
			case *ast.With:
				walk(n.Body)
			case *ast.FilterBlock:
				walk(n.Body)
			case *ast.AssignBlock:
				walk(n.Body)
			case *ast.Macro:
				walk(n.Body)
			case *ast.CallBlock:
				walk(n.Body)
			case *ast.Scope:
				walk(n.Body)
			case *ast.AutoescapeBlock:
				walk(n.Body)
			}
		}
	}
	walk(body)
	if dup != nil {
		return nil, dup
	}
	return blocks, nil
}
