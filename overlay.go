// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"maps"

	"github.com/mgilbir/gojja2/value"
)

// overlayState is what an Environment made by [Environment.Overlay] knows about
// the one it was made from. A New environment has none.
type overlayState struct {
	// parent is the environment the overlay was made from, jinja2's
	// linked_to.
	parent *Environment

	// sharesCompiled records that the options the overlay was given change
	// nothing a compiled template depends on, so a template the parent has
	// compiled is the template the overlay would compile -- once it is bound
	// to the overlay, which is what renders it. See sharedTemplate for the
	// two conditions checked per template on top of this one.
	sharesCompiled bool

	// borrowsFilters, borrowsTests and borrowsGlobals record that the
	// overlay still reads the parent's registry maps rather than a copy of
	// its own. The first Add* on the overlay copies the map it writes to,
	// so the parent never sees the overlay's registrations.
	borrowsFilters bool
	borrowsTests   bool
	borrowsGlobals bool
}

// Overlay returns a new Environment that shares everything with e except what
// opts override. It is jinja2's Environment.overlay.
//
// A host keeps one configured base -- loader, filters, settings, and the
// templates it has compiled -- and derives an overlay per tenant or per request
// for what varies: extra globals, another autoescape policy, other limits, a
// different Undefined. Any Option [New] accepts is accepted here, and the
// result is checked the way New checks one, so an overlay whose delimiters
// collide is refused.
//
// What the overlay shares and what it does not:
//
//   - Settings are copied when the overlay is made, then overridden by opts.
//     Changing the parent afterwards does not reach the overlay; there is no
//     API that changes a setting after New anyway.
//   - Filters, tests and globals are the parent's until the overlay registers
//     one of its own. AddFilter, AddTest or AddGlobal on the overlay then
//     copies that registry first, so the parent is never changed by it. In
//     jinja2 the three dicts are the same objects in both, and a filter
//     added to an overlay is added to its parent; that is a deliberate
//     divergence, in docs/divergences.md. Until the overlay writes to one,
//     a registration on the parent is visible in the overlay, as in jinja2
//     -- and, as for any Environment, registering while templates are
//     rendering is not safe.
//   - The template cache is the overlay's own, of the parent's size unless
//     [WithCacheSize] says otherwise. jinja2 does the same: its overlay
//     starts with an empty copy of the cache. ClearCache on either does not
//     clear the other.
//   - Every template the overlay returns -- from GetTemplate, and from an
//     include, extends or import in a template it rendered -- renders with
//     the overlay's settings and globals, and reaches the next template
//     through the overlay.
//
// Compiling is shared where it can be. When opts leave alone everything
// compiling depends on -- the syntax, extensions, loader, autoescape policy,
// finalize, Undefined, policies, Python version, integer width and how
// unsupported constructs are handled -- a template the overlay fetches by name
// reuses the parent's compiled tree instead of being parsed and folded again,
// which makes an overlay that changes only limits, the method policy or the
// globals cheap enough to make per request. Two things turn the reuse off for
// a template: the overlay having registered a filter or test of its own, and
// the template's compile having run a filter or test the host registered,
// since such a function was handed the parent while it folded. The overlay
// then compiles the template itself, as jinja2 always does.
//
// It reports an error for an Option that fails, as New does. The returned
// Environment is nil when the error is not, and e is unchanged either way.
func (e *Environment) Overlay(opts ...Option) (*Environment, error) {
	rv := new(Environment)
	*rv = *e
	rv.overlay = &overlayState{
		parent:         e,
		borrowsFilters: true,
		borrowsTests:   true,
		borrowsGlobals: true,
	}
	for _, opt := range opts {
		if err := opt(rv); err != nil {
			return nil, err
		}
	}
	if err := rv.validate(); err != nil {
		return nil, err
	}
	// jinja2's copy_cache: empty, and the same size. A WithCacheSize among
	// opts has already replaced the pointer with a cache of its own size.
	if rv.cache == e.cache {
		rv.cache = newTemplateCache(e.cache.limit)
	}
	rv.overlay.sharesCompiled = compilesAlike(e, rv) && !setsCompileReferences(e, opts)
	return rv, nil
}

// LinkedTo returns the environment e was made from by [Environment.Overlay],
// or nil for one made by [New]. It is jinja2's linked_to.
func (e *Environment) LinkedTo() *Environment {
	if e.overlay == nil {
		return nil
	}
	return e.overlay.parent
}

// compilesAlike compares the settings a compiled template depends on that are
// plain values. The four that are functions or interfaces cannot be compared,
// so setsCompileReferences asks about them instead.
//
// Every one of these is read while compiling: the syntax and parser options by
// the parser, and the rest by the constant folder, which bakes their effect
// into the tree -- an Undefined that refuses to fold, a truncate that read the
// leeway, an integer too wide to fold. unsupportedLeniency decides whether the
// template compiles at all.
//
// The Python version is read by the folder too, and is compared as part of
// syntax: WithPythonVersion sets both, for the lexer, and
// TestOverlayPythonVersionIsInSyntax keeps them together. Comparing pyVersion
// as well could never be the comparison that decides.
//
// What is not here is read only while rendering, through the template's own
// environment, which for a shared template is the overlay: the limits, the
// method policy, the globals and the delimiter leniency, which New and Overlay
// check and nothing reads again.
func compilesAlike(a, b *Environment) bool {
	return a.syntax == b.syntax &&
		a.parseOpts == b.parseOpts &&
		a.undefined == b.undefined &&
		a.policies == b.policies &&
		a.maxIntBits == b.maxIntBits &&
		a.unsupportedLeniency == b.unsupportedLeniency
}

// setsCompileReferences reports whether any of opts assigns one of the
// compile-time settings that cannot be compared: the loader, the autoescape
// policy, finalize, and the unsupported-construct report. WithFinalizeFunc is
// among them without being asked about: it clears finalize as it sets its own,
// and that assignment is what is seen.
//
// Two functions cannot be compared in Go, and comparing the code pointer would
// call two closures over different values equal. What can be told is whether
// an option assigned the field at all, by applying opts to two scratch copies
// in which the field starts out nil in one and non-nil in the other: an option
// that leaves it alone leaves them different, and one that assigns anything --
// nil included -- leaves them the same. It errs toward "assigned", which only
// costs a compile: WithAutoescape(false) over a parent that never escaped is
// reported as a change.
//
// Options only assign fields, so applying them again is harmless; the errors
// were reported when they were applied for real.
func setsCompileReferences(base *Environment, opts []Option) bool {
	unset, set := *base, *base
	unset.loader, unset.autoescape, unset.finalize, unset.unsupportedReport = nil, nil, nil, nil
	set.loader = probeLoader{}
	set.autoescape = func(string, bool) bool { return false }
	set.finalize = func(v value.Value) value.Value { return v }
	set.unsupportedReport = func(Unsupported) {}
	for _, opt := range opts {
		_ = opt(&unset)
		_ = opt(&set)
	}
	assigned := func(unsetIsNil, setIsNil bool) bool { return unsetIsNil == setIsNil }
	return assigned(unset.loader == nil, set.loader == nil) ||
		assigned(unset.autoescape == nil, set.autoescape == nil) ||
		assigned(unset.finalize == nil, set.finalize == nil) ||
		assigned(unset.unsupportedReport == nil, set.unsupportedReport == nil)
}

// probeLoader stands in for a loader in setsCompileReferences, and is never
// asked for anything.
type probeLoader struct{}

func (probeLoader) Load(string) (string, error) { panic("gojja2: probeLoader is never loaded from") }

// sharedTemplate is GetTemplate's way of reusing a template the parent compiled,
// bound to e. It returns nil and no error when e has to compile the template
// itself, which is the case when
//
//   - e is not an overlay, or one whose options changed something compiling
//     reads (sharesCompiled);
//   - e has registered a filter or test of its own, since the folder runs
//     them -- `{{ 'a'|upper }}` is folded at compile time, and an overlay
//     that replaced upper must not print the parent's answer;
//   - the template's compile ran a filter or test the host registered. Such
//     a function is handed the State of the compile, and through it the
//     environment that compiled, which for the parent's tree is the parent.
//     Stock filters and tests read nothing there that compilesAlike has not
//     already compared.
func (e *Environment) sharedTemplate(name string) (*Template, error) {
	ov := e.overlay
	if ov == nil || !ov.sharesCompiled || !ov.borrowsFilters || !ov.borrowsTests {
		return nil, nil
	}
	tmpl, err := ov.parent.GetTemplate(name)
	if err != nil {
		return nil, err
	}
	if tmpl.foldsHostCode {
		return nil, nil
	}
	return tmpl.boundTo(e), nil
}

// boundTo returns t as env would have compiled it: the same compiled tree,
// rendered with env's settings and globals and loading what it includes
// through env.
//
// Every field but env and the frameLocals memo is the product of compiling and
// is shared; TestBoundToCopiesEveryField fails when a field is added to
// Template and not decided on here.
func (t *Template) boundTo(env *Environment) *Template {
	return &Template{
		env:           env,
		name:          t.name,
		fromString:    t.fromString,
		source:        t.source,
		tree:          t.tree,
		blocks:        t.blocks,
		countsChunks:  t.countsChunks,
		unsupported:   t.unsupported,
		foldsHostCode: t.foldsHostCode,
	}
}

// ownFilters gives an overlay a filter registry of its own before it writes to
// one, so that the parent's is never written through it. The stock-name map
// goes with it, because AddFilter writes both.
func (e *Environment) ownFilters() {
	if e.overlay == nil || !e.overlay.borrowsFilters {
		return
	}
	e.filters = maps.Clone(e.filters)
	e.stockFilters = maps.Clone(e.stockFilters)
	e.overlay.borrowsFilters = false
}

// ownTests is ownFilters for the test registry.
func (e *Environment) ownTests() {
	if e.overlay == nil || !e.overlay.borrowsTests {
		return
	}
	e.tests = maps.Clone(e.tests)
	e.stockTests = maps.Clone(e.stockTests)
	e.overlay.borrowsTests = false
}

// ownGlobals is ownFilters for the globals.
func (e *Environment) ownGlobals() {
	if e.overlay == nil || !e.overlay.borrowsGlobals {
		return
	}
	e.globals = maps.Clone(e.globals)
	e.globalRefs = maps.Clone(e.globalRefs)
	e.overlay.borrowsGlobals = false
}
