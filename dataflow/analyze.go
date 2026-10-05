// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package dataflow

import "github.com/mgilbir/gojja2/syntax"

type symset map[*syntax.Symbol]bool

func (s symset) add(o symset) {
	for k := range o {
		s[k] = true
	}
}

type analyzer struct {
	tree    *syntax.Tree
	derives map[*syntax.Symbol]symset
	effects map[*syntax.Symbol]Effect

	// capture is the stack of block-set bodies being collected: while one is
	// open, output becomes a value instead of a document.
	capture []symset
	// steered is the same stack again, holding what *decides* how much of
	// each body is emitted rather than what the text is made of. A branch
	// inside a captured body changes the captured string without putting
	// anything of its own in it, and whatever consumes that string --
	// `{% set v | last %}`, or a `{{ v|last }}` much later -- can fail for
	// one value of the branch and not the other. See steerBind.
	steered []symset

	// scopes is the chain of enclosing scope nodes, which is how a macro's
	// name is looked up -- it is an attribute of the macro node rather than
	// a name node, so it has no entry in Defs.
	scopes []*syntax.Node

	// steers records, per symbol, what steered the value it holds. Only
	// Steers and Required travel back along these edges, because a name
	// that decides what a value *is* does not put itself in the document:
	// `{% set v %}{% if t %}secret{% endif %}{% endset %}{{ v }}` prints
	// the secret or nothing, and t is in neither.
	steers map[*syntax.Symbol]symset

	macroOut    map[*syntax.Symbol]symset
	macroSteers map[*syntax.Symbol]symset
	macroParams map[*syntax.Symbol][]*syntax.Symbol
	inMacro     map[*syntax.Symbol]bool

	// opaqueSink records that the whole context can be handed to a template
	// this did not follow, which puts every symbol in play.
	opaqueSink bool

	// resolve, visiting and cache carry the analysis across templates.
	// visiting and cache are shared with every nested analysis, so a cycle
	// terminates and a template pulled in twice is analysed once.
	resolve  Resolver
	visiting map[string]bool
	cache    map[string]map[string]Effect

	// external holds symbols for the caller's variables that only the
	// templates this one pulls in ever mention. They are not in Info,
	// because Info describes this tree.
	external map[string]*syntax.Symbol

	// namespaces holds a symbol per field of each namespace being followed,
	// and aliased marks the ones that got away. See namespace.go.
	namespaces map[*syntax.Symbol]map[string]*syntax.Symbol
	aliased    map[*syntax.Symbol]bool

	// strict is WithStrictUndefined: reading a name that was not passed
	// raises, so an arm that reads one can fail. See canFailIn.
	strict bool
}

func newAnalyzer(t *syntax.Tree, resolve Resolver, strict bool,
	visiting map[string]bool, cache map[string]map[string]Effect) *analyzer {
	return &analyzer{
		tree:        t,
		derives:     map[*syntax.Symbol]symset{},
		steers:      map[*syntax.Symbol]symset{},
		effects:     map[*syntax.Symbol]Effect{},
		macroOut:    map[*syntax.Symbol]symset{},
		macroSteers: map[*syntax.Symbol]symset{},
		macroParams: map[*syntax.Symbol][]*syntax.Symbol{},
		inMacro:     map[*syntax.Symbol]bool{},
		resolve:     resolve,
		visiting:    visiting,
		cache:       cache,
		external:    map[string]*syntax.Symbol{},
		namespaces:  map[*syntax.Symbol]map[string]*syntax.Symbol{},
		aliased:     map[*syntax.Symbol]bool{},
		strict:      strict,
	}
}

// Analyze computes what a template does with the values it is given.
//
// Pass [WithResolver] to follow references to other templates; without one, a
// template that hands its context to another is reported as [Opaque].
func Analyze(t *syntax.Tree, opts ...Option) *Flow {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	a := newAnalyzer(t, o.resolve, o.strict, map[string]bool{},
		map[string]map[string]Effect{})
	a.seedAliases()
	a.stmt(t.Root)
	a.sealNamespaces()
	a.propagate()

	out := &Flow{
		Derives: make(map[*syntax.Symbol][]*syntax.Symbol, len(a.derives)),
		Effects: map[*syntax.Symbol]Effect{},
	}
	for s, deps := range a.derives {
		list := make([]*syntax.Symbol, 0, len(deps))
		for d := range deps {
			list = append(list, d)
		}
		out.Derives[s] = list
	}
	out.external = a.external
	for s, e := range a.effects {
		if a.opaqueSink {
			e |= Opaque
		}
		out.Effects[s] = e
	}
	// A symbol nothing touched still has an answer, and if the context can
	// escape to another template that answer is not "nothing".
	for _, s := range a.allSymbols() {
		if _, ok := out.Effects[s]; !ok {
			var e Effect
			if a.opaqueSink {
				e = Opaque
			}
			out.Effects[s] = e
		}
	}
	return out
}

// seedAliases records that a frame's copy of a name starts out holding whatever
// the enclosing binding held.
//
// A write to the copy does not reach the original, which is the whole reason
// the two are separate symbols -- but everything the original could hold, the
// copy can, so the edge runs one way.
func (a *analyzer) seedAliases() {
	for _, syms := range a.tree.Info.Scopes {
		for _, s := range syms {
			if s.Kind == syntax.SymAlias && s.Aliases != nil {
				a.depend(s, symset{s.Aliases: true})
			}
		}
	}
}

func (a *analyzer) allSymbols() []*syntax.Symbol {
	var out []*syntax.Symbol
	for _, syms := range a.tree.Info.Scopes {
		out = append(out, syms...)
	}
	for _, s := range a.tree.Info.Context {
		out = append(out, s)
	}
	for _, s := range a.external {
		out = append(out, s)
	}
	return out
}

// --- recording ---------------------------------------------------------

func (a *analyzer) apply(srcs symset, e Effect) {
	for s := range srcs {
		a.effects[s] |= e
	}
}

func (a *analyzer) taint(srcs symset) { a.apply(srcs, Opaque) }

// emit records that a value reaches the document, unless a block set is
// capturing it into a variable instead.
func (a *analyzer) emit(srcs symset) {
	if n := len(a.capture); n > 0 {
		a.capture[n-1].add(srcs)
		return
	}
	a.apply(srcs, Printed)
}

// steerBind records that these symbols decide what the target holds, without
// being part of it.
func (a *analyzer) steerBind(target *syntax.Symbol, srcs symset) {
	if target == nil || len(srcs) == 0 {
		return
	}
	if a.steers[target] == nil {
		a.steers[target] = symset{}
	}
	a.steers[target].add(srcs)
}

// steerTarget is steerBind through an assignment target node, which may be a
// namespace field or a tuple rather than a plain name.
func (a *analyzer) steerTarget(target *syntax.Node, srcs symset) {
	if target == nil || len(srcs) == 0 {
		return
	}
	switch target.Kind {
	case syntax.KindName, syntax.KindNSRef:
		if s := a.tree.Info.Symbol(target); s != nil {
			a.steerBind(s, srcs)
		}
	default:
		// A tuple target, or anything else: steer every name in it.
		syntax.Walk(target, func(nd *syntax.Node, _ syntax.Role) bool {
			if nd.Kind == syntax.KindName {
				a.steerBind(a.tree.Info.Symbol(nd), srcs)
			}
			return true
		})
	}
}

// steerEmit records that these symbols decide how much of the surrounding body
// is emitted, into the capture that is collecting it.
//
// Nothing to do when there is no capture: every caller applies Steers to these
// symbols itself, one or two lines earlier, because a name that decides how
// much of a body runs steers whether or not anything is collecting it. That is
// true of all five -- the two in ifStmt and forStmt apply it directly, and the
// other three hand on a set captureBody collected, whose members were applied
// where they were recorded. The arm that applied it a second time here was
// carried until `make mutate` reported it as a survivor: removing it changes no
// effect in any shape, which is what a survivor means when the answer is not a
// missing test.
func (a *analyzer) steerEmit(srcs symset) {
	if len(srcs) == 0 {
		return
	}
	if n := len(a.steered); n > 0 {
		a.steered[n-1].add(srcs)
	}
}

func (a *analyzer) depend(target *syntax.Symbol, srcs symset) {
	if target == nil || len(srcs) == 0 {
		return
	}
	if a.derives[target] == nil {
		a.derives[target] = symset{}
	}
	a.derives[target].add(srcs)
}

// bind records that an assignment target now holds a value derived from srcs.
func (a *analyzer) bind(target *syntax.Node, srcs symset) {
	if target == nil {
		return
	}
	switch target.Kind {
	case syntax.KindName:
		a.depend(a.tree.Info.Symbol(target), srcs)
	case syntax.KindNSRef:
		s := a.tree.Info.Symbol(target)
		if f := a.namespaceField(s, target.Attr("attr")); f != nil {
			a.depend(f, srcs)
			return
		}
		// Not a namespace this is following -- one that was passed in, or
		// one that got away. The write still happened, so the whole thing
		// becomes opaque rather than silently lost, and writing a field of
		// something that is not a namespace can stop the render: always for
		// `{% set ns.v = value %}`, and for the block form on everything
		// but a dict. Either way the name decides whether the render
		// finishes, which is what Required claims.
		a.depend(s, srcs)
		if s != nil {
			a.effects[s] |= Opaque | Required
		}
	case syntax.KindTuple, syntax.KindList:
		// Unpacking: which element lands where is not tracked, so every
		// target derives from the whole right-hand side.
		//
		// The unpacking itself can stop the render, whatever is done
		// with the names afterwards: `{% set a, b = x %}` fails for an
		// x that is not iterable and for one of the wrong length, so
		// the value decides whether the render finishes even when
		// nothing reads a or b. That is what Required claims, and
		// without it `{% set a, b = x %}` reported no effect at all --
		// which this package promises never to do.
		a.apply(srcs, Required)
		for _, item := range target.Children(syntax.RoleItem) {
			a.bind(item, srcs)
		}
	default:
		a.taint(srcs)
	}
}

// propagate pushes effects backwards along the dependency edges to a fixpoint.
//
// A value that reaches the output means everything it came from reaches the
// output; the same for steering, and for not having been understood. The graph
// can hold cycles -- a loop that accumulates into a variable it also reads --
// so this iterates rather than recursing.
func (a *analyzer) propagate() {
	for changed := true; changed; {
		changed = false
		for s, deps := range a.derives {
			e := a.effects[s]
			if e == 0 {
				continue
			}
			for d := range deps {
				if a.effects[d]|e != a.effects[d] {
					a.effects[d] |= e
					changed = true
				}
			}
		}
		for s, deps := range a.steers {
			e := steeredEffect(a.effects[s])
			if e == 0 {
				continue
			}
			for d := range deps {
				if a.effects[d]|e != a.effects[d] {
					a.effects[d] |= e
					changed = true
				}
			}
		}
	}
}

// steeredEffect is what a value's effects mean for a name that decided what it
// held. Printed becomes Steers -- the name changed the document without
// appearing in it -- Steers and Required carry over as they are, and Opaque
// does too, because a route that was not followed is not followed for the name
// that steered it either.
func steeredEffect(e Effect) Effect {
	var out Effect
	if e&(Printed|Steers) != 0 {
		out |= Steers
	}
	out |= e & (Required | Opaque)
	return out
}
