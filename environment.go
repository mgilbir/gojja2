// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

// Package gojja2 is a pure Go implementation of the Jinja2 template language,
// built to render exactly what CPython's jinja2 renders.
package gojja2

import (
	"errors"
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

	// stockFilters and stockTests name the ones that are still jinja2's
	// own. A call is checked against jinja2's signature only while it is:
	// a filter a caller replaced through AddFilter answers to whatever
	// that caller accepts, and the conformance profiles rely on it --
	// transformers supplies a tojson that takes ensure_ascii, which
	// jinja2's does not.
	stockFilters map[string]bool
	stockTests   map[string]bool

	// delimiterLeniency decides whether the block and comment openings may
	// be equal, which is the one pair jinja2's own check does not compare.
	delimiterLeniency DelimiterLeniency
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

	// unsupportedLeniency and unsupportedReport decide what compiling a
	// template does about a construct gojja2 cannot honour the way jinja2
	// does. See unsupported.go.
	unsupportedLeniency UnsupportedLeniency
	unsupportedReport   func(Unsupported)

	// pyVersion is the CPython whose behaviour renders reproduce, for the
	// places the interpreters disagree. See compat.go.
	pyVersion PythonVersion

	// maxIntBits bounds the width of an integer an expression computes.
	// Unlike the others it is a conformance question as much as a safety
	// one -- CPython computes what this refuses -- which is why it is
	// configurable at all. See value.MaxIntBits.
	maxIntBits int64

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

// Option configures an Environment. It reports what it could not accept, so
// that a misconfiguration is refused at New rather than surfacing later as a
// template that behaves oddly.
type Option func(*Environment) error

// New returns an Environment with jinja2's defaults, adjusted by opts.
//
// It reports an error for a configuration it cannot honour -- an unknown
// extension, a newline sequence that is not one, delimiters that collide --
// rather than accepting it and rendering something the caller did not ask
// for. The returned Environment is nil when the error is not.
func New(opts ...Option) (*Environment, error) {
	env := &Environment{
		syntax:         lexer.DefaultSyntax(),
		undefined:      value.UndefinedDefault,
		policies:       defaultPolicies(),
		maxRecursion:   100,
		maxIterations:  defaultMaxIterations,
		maxOutputBytes: defaultMaxOutputBytes,
		pyVersion:      DefaultPythonVersion,
		maxIntBits:     value.MaxIntBits,
		filters:        make(map[string]Filter),
		tests:          make(map[string]Test),
		globals:        make(map[string]value.Value),
		cache:          newTemplateCache(defaultCacheSize),
	}
	registerDefaultFilters(env)
	registerDefaultTests(env)
	registerDefaultGlobals(env)
	env.stockFilters = make(map[string]bool, len(env.filters))
	for name := range env.filters {
		env.stockFilters[name] = true
	}
	env.stockTests = make(map[string]bool, len(env.tests))
	for name := range env.tests {
		env.stockTests[name] = true
	}
	for _, opt := range opts {
		if err := opt(env); err != nil {
			return nil, err
		}
	}
	if err := env.validate(); err != nil {
		return nil, err
	}
	return env, nil
}

// DelimiterLeniency is how strictly [New] checks that the tag openings differ.
//
// The zero value is the strict one, so a caller who says nothing gets the
// reading that cannot be ambiguous.
type DelimiterLeniency int

const (
	// RefuseCollidingDelimiters refuses any two of the block, variable and
	// comment openings being equal. A template cannot be read two ways,
	// and which reading wins is a detail of the lexer rather than
	// something a template author chose.
	//
	// This is stricter than jinja2 in one case; see MatchJinja2Delimiters.
	RefuseCollidingDelimiters DelimiterLeniency = iota

	// MatchJinja2Delimiters reproduces jinja2's check exactly.
	//
	// jinja2 writes it as `assert block != variable != comment`, a chained
	// comparison, so it compares block against variable and variable
	// against comment and never compares block against comment. Setting
	// the block and comment openings to the same string is therefore
	// accepted there, and is accepted here under this setting. Which of
	// the two such a template opens is then decided by the lexer's
	// ordering rather than by the template.
	MatchJinja2Delimiters
)

func (l DelimiterLeniency) String() string {
	if l == MatchJinja2Delimiters {
		return "MatchJinja2Delimiters"
	}
	return "RefuseCollidingDelimiters"
}

// WithDelimiterLeniency selects how strictly the tag openings are checked
// against one another. See [RefuseCollidingDelimiters], which is the default.
func WithDelimiterLeniency(l DelimiterLeniency) Option {
	return func(e *Environment) error { e.delimiterLeniency = l; return nil }
}

// validate checks what only the finished configuration can show, which is the
// settings that have to differ from one another.
//
// A line-statement or line-comment prefix may equal any delimiter: jinja2
// allows that and renders it, and so does this.
func (e *Environment) validate() error {
	pairs := []struct{ aName, a, bName, b string }{
		{"block", e.syntax.BlockStart, "variable", e.syntax.VariableStart},
		{"variable", e.syntax.VariableStart, "comment", e.syntax.CommentStart},
	}
	if e.delimiterLeniency == RefuseCollidingDelimiters {
		// The pair jinja2's chained comparison skips.
		pairs = append(pairs, struct{ aName, a, bName, b string }{
			"block", e.syntax.BlockStart, "comment", e.syntax.CommentStart})
	}
	for _, pair := range pairs {
		if pair.a == pair.b {
			return errs.New(errs.TemplateError,
				"the %s and %s start strings are both %q; they must differ",
				pair.aName, pair.bName, pair.a)
		}
	}
	return nil
}

// WithLoader sets where templates are loaded from.
func WithLoader(l Loader) Option { return func(e *Environment) error { e.loader = l; return nil } }

// WithBlockDelimiters overrides `{%` and `%}`.
func WithBlockDelimiters(start, end string) Option {
	return func(e *Environment) error { e.syntax.BlockStart, e.syntax.BlockEnd = start, end; return nil }
}

// WithVariableDelimiters overrides `{{` and `}}`.
func WithVariableDelimiters(start, end string) Option {
	return func(e *Environment) error { e.syntax.VariableStart, e.syntax.VariableEnd = start, end; return nil }
}

// WithCommentDelimiters overrides `{#` and `#}`.
func WithCommentDelimiters(start, end string) Option {
	return func(e *Environment) error { e.syntax.CommentStart, e.syntax.CommentEnd = start, end; return nil }
}

// WithLineStatementPrefix enables line statements, e.g. "#".
func WithLineStatementPrefix(prefix string) Option {
	return func(e *Environment) error { e.syntax.LineStatementPrefix = prefix; return nil }
}

// WithLineCommentPrefix enables line comments, e.g. "##".
func WithLineCommentPrefix(prefix string) Option {
	return func(e *Environment) error { e.syntax.LineCommentPrefix = prefix; return nil }
}

// WithTrimBlocks removes the first newline after a block tag.
func WithTrimBlocks(on bool) Option {
	return func(e *Environment) error { e.syntax.TrimBlocks = on; return nil }
}

// WithLstripBlocks strips indentation in front of a block tag.
func WithLstripBlocks(on bool) Option {
	return func(e *Environment) error { e.syntax.LstripBlocks = on; return nil }
}

// WithKeepTrailingNewline keeps a template's final newline.
func WithKeepTrailingNewline(on bool) Option {
	return func(e *Environment) error { e.syntax.KeepTrailingNewline = on; return nil }
}

// WithNewlineSequence sets what newlines in template data render as.
//
// It must be one of "\n", "\r\n" or "\r", which is what jinja2 asserts. Any
// other string used to be accepted and rendered, so a template that worked
// here raised under CPython for a reason the template could not see.
func WithNewlineSequence(seq string) Option {
	return func(e *Environment) error {
		switch seq {
		case "\n", "\r\n", "\r":
			e.syntax.NewlineSequence = seq
			return nil
		}
		return errs.New(errs.TemplateError,
			`newline sequence %q is not one of "\n", "\r\n" or "\r"`, seq)
	}
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
	return func(e *Environment) error {
		if !on {
			e.autoescape = nil
			return nil
		}
		e.autoescape = func(string, bool) bool { return true }
		return nil
	}
}

// WithAutoescapeFunc decides escaping per template, the way jinja2's
// select_autoescape does.
func WithAutoescapeFunc(fn AutoescapeFunc) Option {
	return func(e *Environment) error { e.autoescape = fn; return nil }
}

// SelectAutoescapeConfig mirrors the parameters of jinja2's select_autoescape.
//
// The zero value follows jinja2's own defaults, with one documented exception
// noted on Enabled: the HTML and XML extensions escape, nothing is explicitly
// disabled, a template compiled from a string escapes, and anything else does
// not. That is deliberate -- every field here is written so that the quiet
// reading is the safe one, and a caller has to say something explicit to
// escape less.
// AutoescapeLeniency is how much [WithAutoescapeSelection] and
// [WithAutoescapeExtensions] will accept in an extension list.
//
// The zero value is the strict one, so a caller who says nothing gets the
// safer reading -- as everywhere else in [SelectAutoescapeConfig].
type AutoescapeLeniency int

const (
	// RefuseImpossibleExtensions refuses an entry that cannot be a file
	// extension: a glob, a path, an empty or dots-only string, or one
	// containing whitespace. Extensions are matched as a suffix, so such
	// an entry matches nothing -- and in Enabled, matching nothing means
	// escaping nothing. This is the default and the recommended setting.
	RefuseImpossibleExtensions AutoescapeLeniency = iota

	// AcceptAnyExtension accepts every entry, which is what jinja2's
	// select_autoescape does. `Enabled: []string{"*.html"}` is then taken
	// literally, matches no template, and escapes none of them -- exactly
	// as CPython behaves. Choose this when matching jinja2 matters more
	// than catching the mistake.
	AcceptAnyExtension
)

func (l AutoescapeLeniency) String() string {
	if l == AcceptAnyExtension {
		return "AcceptAnyExtension"
	}
	return "RefuseImpossibleExtensions"
}

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
	// Leniency decides what [WithAutoescapeSelection] accepts in Enabled
	// and Disabled. The zero value refuses entries that cannot be
	// extensions.
	//
	// [SelectAutoescapeWith] ignores it: that function returns an
	// AutoescapeFunc and has nowhere to report a refusal, so it is always
	// jinja2's behaviour. The checking lives in the Option, which has an
	// error to return.
	Leniency AutoescapeLeniency
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

// WithAutoescapeExtensions turns escaping on for the templates whose names end
// in one of these extensions, and checks that each one could be an extension.
//
// It is [WithAutoescapeSelection] with only Enabled set, which means the strict
// default: see [RefuseImpossibleExtensions] for what that refuses and why. To
// match jinja2 instead, use WithAutoescapeSelection with
// Leniency: AcceptAnyExtension.
func WithAutoescapeExtensions(extensions ...string) Option {
	return WithAutoescapeSelection(SelectAutoescapeConfig{Enabled: extensions})
}

// WithAutoescapeSelection is [WithAutoescapeFunc] over [SelectAutoescapeWith],
// with the extension lists checked according to cfg.Leniency.
//
// SelectAutoescapeWith returns an AutoescapeFunc and so has nowhere to report
// a mistake in what it was given. Extensions are matched as a suffix, so an
// entry that is not one matches nothing -- and an Enabled entry that matches
// nothing escapes nothing. `SelectAutoescape("*.html")`, which is how one
// would write it thinking of a glob, leaves every .html template unescaped and
// says so nowhere; jinja2 does the same.
//
// The default refuses that. Being stricter than jinja2 here is the reasoning
// [SelectAutoescapeConfig.Enabled] already carries: this is the one setting
// whose failure mode is cross-site scripting, so the quiet reading has to be
// the safe one. Set Leniency to [AcceptAnyExtension] to have jinja2's
// behaviour exactly.
//
// A Disabled entry is checked too. One that cannot match errs toward escaping
// *more*, which is the safe direction, but it is still not the configuration
// the caller wrote down.
func WithAutoescapeSelection(cfg SelectAutoescapeConfig) Option {
	return func(e *Environment) error {
		if cfg.Leniency == RefuseImpossibleExtensions {
			for _, group := range []struct {
				field string
				exts  []string
			}{{"Enabled", cfg.Enabled}, {"Disabled", cfg.Disabled}} {
				for _, ext := range group.exts {
					if err := checkAutoescapeExtension(group.field, ext); err != nil {
						return err
					}
				}
			}
		}
		e.autoescape = SelectAutoescapeWith(cfg)
		return nil
	}
}

// checkAutoescapeExtension rejects what cannot be a file extension.
func checkAutoescapeExtension(field, ext string) error {
	reject := func(why string) error {
		return errs.New(errs.TemplateError,
			"autoescape %s extension %q %s; extensions are matched as a suffix, so it "+
				"would never match. Set Leniency to AcceptAnyExtension to take it "+
				"literally, as jinja2 does", field, ext, why)
	}
	switch {
	case strings.TrimLeft(ext, ".") == "":
		return reject("is empty")
	case strings.ContainsAny(ext, "*?[]"):
		return reject("looks like a glob")
	case strings.ContainsAny(ext, `/\`):
		return reject("looks like a path")
	case strings.ContainsAny(ext, " \t\n\r"):
		return reject("contains whitespace")
	}
	return nil
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
//
// An extension that is empty once the dots are trimmed becomes the pattern ".",
// which is what jinja2 builds and which selects a name ending in a dot.
// Dropping it instead made SelectAutoescape("") escape nothing at all -- the
// wrong direction for the one setting whose failure mode is cross-site
// scripting, and the direction this package treats as a bug everywhere else.
func normalizeExtensions(extensions []string) []string {
	out := make([]string, 0, len(extensions))
	for _, ext := range extensions {
		out = append(out, "."+strings.ToLower(strings.TrimLeft(ext, ".")))
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
	return func(e *Environment) error { e.undefined = b; return nil }
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
	return func(e *Environment) error { e.methods = p; return nil }
}

// WithFinalize post-processes every value before it is printed.
func WithFinalize(fn func(value.Value) value.Value) Option {
	return func(e *Environment) error { e.finalize = fn; return nil }
}

// WithExtensions enables the optional tags. `do` provides `{% do %}`;
// `loopcontrols` provides `{% break %}` and `{% continue %}`.
func WithExtensions(names ...string) Option {
	return func(e *Environment) error {
		for _, name := range names {
			switch name {
			case "do", "jinja2.ext.do":
				e.parseOpts.Do = true
			case "loopcontrols", "jinja2.ext.loopcontrols":
				e.parseOpts.LoopControls = true
			default:
				// Ignoring this turned a typo into a feature
				// that was asked for and not enabled, and the
				// template only said so later, by failing on a
				// tag that should have existed.
				return errs.New(errs.TemplateError,
					"unknown extension %q: the known ones are do and loopcontrols",
					name)
			}
		}
		return nil
	}
}

// WithMaxRecursion bounds how deeply templates may include, extend or call
// into each other. Zero restores the default.
func WithMaxRecursion(n int) Option {
	return func(e *Environment) error {
		if n <= 0 {
			n = 100
		}
		e.maxRecursion = n
		return nil
	}
}

// WithMaxIterations bounds the units of work one render may take, counting
// every {% for %} pass, every item a filter pulls out of a sequence, and every
// element of a render argument converted from Go.
// Exceeding it fails the render with an error wrapping [ErrTooManyIterations].
//
// Zero restores the default. To remove the bound entirely, pass a negative n or
// use [WithoutLimits]; do that only when the templates are trusted and a context
// deadline is doing the job instead.
func WithMaxIterations(n int64) Option {
	return func(e *Environment) error {
		if n == 0 {
			n = defaultMaxIterations
		}
		e.maxIterations = n
		return nil
	}
}

// WithMaxOutputBytes bounds how much text one render may produce, counting
// text captured into a buffer by {% filter %}, a block {% set %} or a macro as
// well as text that reaches the writer. Exceeding it fails the render with an
// error wrapping [ErrOutputTooLarge].
//
// Zero restores the default, with the same caveat as [WithMaxIterations].
func WithMaxOutputBytes(n int64) Option {
	return func(e *Environment) error {
		if n == 0 {
			n = defaultMaxOutputBytes
		}
		e.maxOutputBytes = n
		return nil
	}
}

// WithMaxIntBits bounds the width, in bits, of an integer an expression
// computes. Exceeding it fails the render with an `OverflowError`.
//
// This is the one bound here that is also a conformance question: CPython
// computes `(2**500000) * (2**500000)` and gojja2 refuses it. The bound exists
// because multiplication doubles the operand width, so a loop that squares
// grows exponentially while the template stays the same size -- twelve
// iterations were enough to exhaust the machine with the output and iteration
// budgets both set, because neither counts the memory a big.Int occupies.
//
// Zero restores the default of 2**20 bits. A negative n removes the bound and
// gives CPython's arithmetic back.
//
// [WithoutLimits] does *not* remove it, which is deliberate. This is a ceiling
// rather than a budget, and WithoutLimits leaves the other ceilings alone too --
// the 2**31 cap on a repetition and on any sized allocation are not affected by
// it either. Removing this one has to be asked for by name.
//
// Removing it is not free, and the cost is worth stating plainly: the only
// backstop left is the 2**31-byte ceiling on a single allocation, and an
// integer of 2 GiB is enough to exhaust most processes on its own. A squaring
// loop reaches it in about forty iterations. Keeping a WithMaxOutputBytes
// budget still bounds such a loop, because the width is charged against it
// before it is allocated -- so removing this bound and keeping that one is the
// combination that gives CPython's answers without giving up the floor.
func WithMaxIntBits(n int64) Option {
	return func(e *Environment) error {
		if n == 0 {
			n = value.MaxIntBits
		}
		e.maxIntBits = n
		return nil
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
	return func(e *Environment) error {
		e.maxIterations = -1
		e.maxOutputBytes = -1
		return nil
	}
}

// WithPolicies overrides the filter default policies.
func WithPolicies(p Policies) Option {
	return func(e *Environment) error {
		// truncate refuses a negative leeway when it runs, which meant
		// a misconfigured environment compiled and then failed on
		// every render that reached the filter.
		if p.TruncateLeeway < 0 {
			return errs.New(errs.TemplateError,
				"truncate leeway must not be negative, got %d", p.TruncateLeeway)
		}
		e.policies = p
		return nil
	}
}

// Policies returns the environment's filter defaults.
func (e *Environment) Policies() Policies { return e.policies }

// AddFilter registers a filter, replacing any filter of the same name.
//
// Replacing one of jinja2's own also gives up the argument checking that goes
// with its signature: what the replacement accepts is the replacement's
// business.
func (e *Environment) AddFilter(name string, f Filter) {
	e.filters[name] = f
	delete(e.stockFilters, name)
}

// AddTest registers a test, replacing any test of the same name.
func (e *Environment) AddTest(name string, t Test) {
	e.tests[name] = t
	delete(e.stockTests, name)
}

// AddGlobal registers a global, replacing any global of the same name.
func (e *Environment) AddGlobal(name string, v value.Value) { e.globals[name] = v }

// Globals returns a copy of the registered globals.
//
// A copy, because the map it used to hand back was the environment's own: a
// caller who wrote to it replaced a built-in for every template compiled from
// that environment, and `range` is as easy to clobber as anything else. Use
// [Environment.AddGlobal] to change one on purpose.
func (e *Environment) Globals() map[string]value.Value {
	out := make(map[string]value.Value, len(e.globals))
	for k, v := range e.globals {
		out[k] = v
	}
	return out
}

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

	// No loader at all is a misconfiguration, not a missing template, and
	// jinja2 says so: TypeError rather than TemplateNotFound. The
	// difference is visible, because it is the one failure `ignore missing`
	// does not swallow -- an environment with no loader rendered
	// `{% include "x" ignore missing %}` as nothing at all here, quietly,
	// where CPython reports the environment.
	if e.loader == nil {
		return nil, errs.New(errs.TypeError,
			"no loader for this environment specified")
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
	return func(e *Environment) error {
		if n == 0 {
			n = defaultCacheSize
		}
		e.cache = newTemplateCache(n)
		return nil
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

// selectTemplateValue is jinja2's select_template over one value.
//
// The order is Python's, and each step is reachable from a template. Emptiness
// is truthiness and is checked first, so `{% include none %}` and
// `{% include [] %}` are both "an empty list of templates" -- neither ever
// reaches the iteration that a number fails at.
func (e *Environment) selectTemplateValue(v value.Value) (*Template, error) {
	on, err := value.IsTrue(v)
	if err != nil {
		return nil, err
	}
	if !on {
		return nil, errs.New(errs.TemplatesNotFound,
			"Tried to select from an empty list of templates.")
	}
	seq, err := value.Iterate(v)
	if err != nil {
		return nil, err
	}
	var names []value.Value
	for item := range seq {
		names = append(names, item)
	}
	tmpl, err := e.selectTemplateValues(names)
	if err != nil && v.Kind() == value.KindDict &&
		errors.Is(err, errs.TemplatesNotFound) {
		// TemplatesNotFound builds its `name` as `names and names[-1]`,
		// which subscripts what it was handed. A mapping iterates its
		// keys happily and then has no key -1, so CPython's own
		// reporting path raises here instead -- and a template that
		// selects from a dict sees that, not the message.
		return nil, errs.New(errs.KeyError, "-1")
	}
	return tmpl, err
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
		if !errors.Is(err, errs.TemplateNotFound) {
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
	// After the fold, so a handler spelled as constant pieces is a literal
	// by now, and last, so a template that is broken outright says so
	// before it is told about a construct that merely diverges.
	found := findUnsupported(tree.Body, source, name)
	if uerr := e.reportUnsupported(found, name, source); uerr != nil {
		return nil, uerr
	}
	return &Template{
		env:          e,
		name:         name,
		fromString:   fromString,
		source:       source,
		tree:         tree,
		blocks:       blocks,
		countsChunks: hasFilterBlock(tree.Body),
		unsupported:  found,
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
