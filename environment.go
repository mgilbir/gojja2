// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

// Package gojja2 is a pure Go implementation of the Jinja2 template language,
// built to render exactly what CPython's jinja2 renders.
package gojja2

import (
	"strings"

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
	// autoescape decides per template. nil means never.
	autoescape AutoescapeFunc
	// finalize post-processes every printed value.
	finalize func(value.Value) value.Value
	// methods decides which Go methods a template may reach. nil means
	// value.NullaryMethods.
	methods value.MethodPolicy

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

	// maxIterations and maxOutputBytes bound the work of one render, for a
	// caller who has no deadline to give it. Safety controls in the same
	// sense as maxRecursion: a loop over an attacker-influenced range, or
	// a template whose output grows without limit, would otherwise run
	// until the machine gives up.
	maxIterations  int64
	maxOutputBytes int64

	// cache holds compiled templates, bounded and least-recently-used.
	cache *templateCache
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
		syntax:         lexer.DefaultSyntax(),
		undefined:      value.UndefinedDefault,
		policies:       defaultPolicies(),
		maxRecursion:   100,
		maxIterations:  defaultMaxIterations,
		maxOutputBytes: defaultMaxOutputBytes,
		filters:        make(map[string]Filter),
		tests:          make(map[string]Test),
		globals:        make(map[string]value.Value),
		cache:          newTemplateCache(defaultCacheSize),
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

// AutoescapeFunc decides whether a template autoescapes.
//
// fromString is true for a template compiled by [Environment.FromString], which
// has no name to decide by; name is "" in that case. jinja2 passes None for
// exactly that case and its select_autoescape escapes such templates by
// default, so a policy that ignores fromString escapes less than jinja2 does.
// Distinguishing it from a named template that happens to match nothing is the
// whole point of the parameter.
type AutoescapeFunc func(name string, fromString bool) bool

// WithAutoescape turns HTML escaping on or off for every template.
func WithAutoescape(on bool) Option {
	return func(e *Environment) {
		if !on {
			e.autoescape = nil
			return
		}
		e.autoescape = func(string, bool) bool { return true }
	}
}

// WithAutoescapeFunc decides escaping per template, the way jinja2's
// select_autoescape does.
func WithAutoescapeFunc(fn AutoescapeFunc) Option {
	return func(e *Environment) { e.autoescape = fn }
}

// SelectAutoescapeConfig mirrors the parameters of jinja2's select_autoescape.
//
// The zero value follows jinja2's own defaults, with one documented exception
// noted on Enabled: the HTML and XML extensions escape, nothing is explicitly
// disabled, a template compiled from a string escapes, and anything else does
// not. That is deliberate -- every field here is written so that the quiet
// reading is the safe one, and a caller has to say something explicit to
// escape less.
type SelectAutoescapeConfig struct {
	// Enabled lists the extensions that turn escaping on. A leading dot is
	// optional and case is ignored. Empty means html, htm, xml and xhtml.
	//
	// jinja2's default set omits xhtml. Keeping it is a deliberate
	// divergence: the set only ever turns escaping *on*, so a superset is
	// strictly safer, and narrowing it would make this package escape less
	// than the version that shipped before. See docs/divergences.md.
	Enabled []string
	// Disabled lists extensions that turn escaping off. It is consulted
	// after Enabled, so an extension in both escapes.
	Disabled []string
	// DisableForString turns escaping off for templates compiled from a
	// string. The zero value leaves it on, which is jinja2's
	// default_for_string=True.
	DisableForString bool
	// Default is what a template matching neither list gets.
	Default bool
}

// SelectAutoescape escapes templates whose name ends in one of the given
// extensions, and is the usual choice for a mixed HTML and text project.
//
// Matching ignores case on both sides and a leading dot is optional, so
// ".HTML", ".html" and "html" all select the same templates. jinja2 does the
// same, and documents the case-insensitivity as a security property: an
// extension list that is merely spelled unexpectedly must not silently turn
// escaping off.
//
// Templates compiled from a string are escaped, as jinja2's
// default_for_string does. Use [SelectAutoescapeWith] for the full set of
// knobs.
func SelectAutoescape(extensions ...string) AutoescapeFunc {
	return SelectAutoescapeWith(SelectAutoescapeConfig{Enabled: extensions})
}

// defaultAutoescapeExtensions is what an empty Enabled means. See the note on
// SelectAutoescapeConfig.Enabled for why xhtml is here and not in jinja2's.
var defaultAutoescapeExtensions = []string{"html", "htm", "xml", "xhtml"}

// SelectAutoescapeWith is [SelectAutoescape] with every parameter jinja2's
// select_autoescape takes.
func SelectAutoescapeWith(cfg SelectAutoescapeConfig) AutoescapeFunc {
	enabled := cfg.Enabled
	if len(enabled) == 0 {
		enabled = defaultAutoescapeExtensions
	}
	enabledPatterns := normalizeExtensions(enabled)
	disabledPatterns := normalizeExtensions(cfg.Disabled)
	escapeStrings := !cfg.DisableForString

	return func(name string, fromString bool) bool {
		if fromString {
			return escapeStrings
		}
		lower := strings.ToLower(name)
		if hasAnySuffix(lower, enabledPatterns) {
			return true
		}
		if hasAnySuffix(lower, disabledPatterns) {
			return false
		}
		return cfg.Default
	}
}

// normalizeExtensions renders each extension as jinja2 does -- lower-cased and
// carrying exactly one leading dot -- so that matching happens on an extension
// boundary rather than on any trailing substring. Without the dot, "tml" would
// select "page.html".
func normalizeExtensions(extensions []string) []string {
	out := make([]string, 0, len(extensions))
	for _, ext := range extensions {
		trimmed := strings.TrimLeft(ext, ".")
		if trimmed == "" {
			continue
		}
		out = append(out, "."+strings.ToLower(trimmed))
	}
	return out
}

func hasAnySuffix(name string, patterns []string) bool {
	for _, pattern := range patterns {
		if strings.HasSuffix(name, pattern) {
			return true
		}
	}
	return false
}

// WithUndefined selects the Undefined behaviour for missing values.
func WithUndefined(b value.UndefinedBehavior) Option {
	return func(e *Environment) { e.undefined = b }
}

// WithMethodPolicy decides which methods of a Go value in the render context a
// template may call.
//
// The default exposes only methods that take no arguments, which is what a
// template reaches as a plain attribute. Widening it -- with
// [value.AllMethods], or a policy of your own -- lets a template choose the
// arguments a host method is called with, so it is worth doing only where
// template authors are as trusted as the Go code they call into.
func WithMethodPolicy(p value.MethodPolicy) Option {
	return func(e *Environment) { e.methods = p }
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

// WithMaxIterations bounds the loop iterations one render may take, counting
// every {% for %} pass and every item a filter pulls out of a sequence.
// Exceeding it fails the render with an error wrapping [ErrTooManyIterations].
//
// Zero restores the default. To remove the bound entirely, pass a negative n or
// use [WithoutLimits]; do that only when the templates are trusted and a context
// deadline is doing the job instead.
func WithMaxIterations(n int64) Option {
	return func(e *Environment) {
		if n == 0 {
			n = defaultMaxIterations
		}
		e.maxIterations = n
	}
}

// WithMaxOutputBytes bounds how much text one render may produce, counting
// text captured into a buffer by {% filter %}, a block {% set %} or a macro as
// well as text that reaches the writer. Exceeding it fails the render with an
// error wrapping [ErrOutputTooLarge].
//
// Zero restores the default, with the same caveat as [WithMaxIterations].
func WithMaxOutputBytes(n int64) Option {
	return func(e *Environment) {
		if n == 0 {
			n = defaultMaxOutputBytes
		}
		e.maxOutputBytes = n
	}
}

// WithoutLimits removes the iteration and output bounds.
//
// It exists so that removing them is something a reader can find. The three
// limit options used to disagree about what zero meant -- WithMaxRecursion(0)
// restored its default while WithMaxIterations(0) and WithMaxOutputBytes(0)
// switched their bounds off -- so a config struct deserialised from YAML or
// flags, with fields nobody set, quietly disabled two of the three. Zero now
// means "default" for all three, and turning a safety control off has to be
// said out loud.
func WithoutLimits() Option {
	return func(e *Environment) {
		e.maxIterations = -1
		e.maxOutputBytes = -1
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

// escapes reports whether a template is autoescaped. fromString marks one
// compiled by FromString, which has no name for the policy to decide by.
func (e *Environment) escapes(name string, fromString bool) bool {
	return e.autoescape != nil && e.autoescape(name, fromString)
}

// FromString compiles a template that has no name, and so cannot be the target
// of extends or include.
func (e *Environment) FromString(source string) (*Template, error) {
	return e.compile(source, "", true)
}

// FromNamedString compiles a template under a name, which decides autoescaping
// and appears in error messages.
func (e *Environment) FromNamedString(name, source string) (*Template, error) {
	return e.compile(source, name, false)
}

// GetTemplate loads and compiles a template by name, caching the result.
func (e *Environment) GetTemplate(name string) (*Template, error) {
	if tmpl, ok := e.cache.get(name); ok {
		return tmpl, nil
	}

	if e.loader == nil {
		return nil, errs.New(errs.TemplateNotFound, "%s", name)
	}
	source, err := e.loader.Load(name)
	if err != nil {
		return nil, err
	}
	tmpl, err := e.compile(source, name, false)
	if err != nil {
		return nil, err
	}

	e.cache.put(name, tmpl)
	return tmpl, nil
}

// WithCacheSize bounds how many compiled templates the environment keeps.
//
// Zero restores the default of 400, which is jinja2's. A negative size makes
// the cache unbounded; that is worth asking for only when the set of template
// names is closed and known, since an unbounded cache over names a template can
// influence grows for the life of the process -- and retains the constants
// folded into each tree along with it.
func WithCacheSize(n int) Option {
	return func(e *Environment) {
		if n == 0 {
			n = defaultCacheSize
		}
		e.cache = newTemplateCache(n)
	}
}

// ClearCache drops every compiled template, so the next GetTemplate reads from
// the loader again.
//
// This is how a long-running process picks up an edited template. There is no
// automatic reload: a Loader returns source and nothing else, so an environment
// has no way to ask whether what it compiled is still current. See
// docs/divergences.md.
func (e *Environment) ClearCache() { e.cache.clear() }

// ForgetTemplate drops one compiled template from the cache.
func (e *Environment) ForgetTemplate(name string) { e.cache.forget(name) }

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

func (e *Environment) compile(source, name string, fromString bool) (tmpl *Template, err error) {
	// Compilation runs the optimizer, which runs real filters. A panic
	// there is a bug here, and it must not escape a function whose job is
	// to report whether a template is valid.
	defer catchPanic(&err)
	tree, terr := parser.Parse(e.syntax, e.parseOpts, source, name)
	if terr != nil {
		return nil, terr
	}
	// Order matters: the general fold runs first, as jinja2's optimizer
	// does, and the print-specific one then catches the undefined results
	// the optimizer refuses to turn into constants.
	folder := newConstEvaluator(e, name, fromString)
	foldConstantExpressions(folder, tree.Body)
	foldConstantPrints(folder, tree.Body)
	if derr := e.checkDependencies(tree.Body, name, source); derr != nil {
		return nil, derr
	}
	blocks, berr := collectBlocks(tree.Body, name, source)
	if berr != nil {
		return nil, berr
	}
	return &Template{
		env:        e,
		name:       name,
		fromString: fromString,
		source:     source,
		tree:       tree,
		blocks:     blocks,
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
