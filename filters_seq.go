// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"math/rand/v2"
	"strings"

	"github.com/mgilbir/gojja2/errs"
	"github.com/mgilbir/gojja2/value"
)

func filterLength(_ *State, v value.Value, _ *value.CallArgs) (value.Value, error) {
	// LenValue rather than Len: a Python length is an arbitrary-precision
	// integer, and range() can exceed what an int holds.
	return value.LenValue(v)
}

func filterList(s *State, v value.Value, _ *value.CallArgs) (value.Value, error) {
	items, err := materialize(s, v)
	if err != nil {
		return value.Undefined, err
	}
	return value.NewList(items...), nil
}

// filterItems yields (key, value) pairs, and tolerates undefined so that
// `{% for k, v in missing|items %}` renders nothing rather than failing.
func filterItems(_ *State, v value.Value, _ *value.CallArgs) (value.Value, error) {
	if v.IsUndefined() {
		if v.UndefinedBehavior() == value.UndefinedStrict {
			return value.Undefined, v.UndefinedError()
		}
		return value.NewList(), nil
	}
	if d, ok := v.Dict(); ok {
		items := make([]value.Value, 0, d.Len())
		for _, e := range d.Entries() {
			items = append(items, value.NewTuple(e.Key, e.Value))
		}
		return value.NewList(items...), nil
	}
	if m, ok := v.Interface().(value.Mapping); ok && v.Kind() == value.KindObject {
		keys := m.Keys()
		items := make([]value.Value, 0, len(keys))
		for _, k := range keys {
			val, _ := m.GetItem(k)
			items = append(items, value.NewTuple(k, val))
		}
		return value.NewList(items...), nil
	}
	return value.Undefined, errs.New(errs.TypeError,
		"Can only get item pairs from a mapping.")
}

func filterFirst(s *State, v value.Value, _ *value.CallArgs) (value.Value, error) {
	items, err := materialize(s, v)
	if err != nil {
		return value.Undefined, err
	}
	if len(items) == 0 {
		return s.Undefined(value.UndefinedHint("No first item, sequence was empty.")), nil
	}
	return items[0], nil
}

func filterLast(s *State, v value.Value, _ *value.CallArgs) (value.Value, error) {
	// jinja2 takes the last item through reversed(), which reaches a
	// string by __getitem__ -- so the last character of a Markup is
	// Markup, while |first, which iterates, gives a plain str.
	if v.IsString() {
		last, ok := value.StrIndex(v.AsString(), -1)
		if !ok {
			return s.Undefined(value.UndefinedHint("No last item, sequence was empty.")), nil
		}
		if v.IsSafe() {
			return value.Safe(last), nil
		}
		return value.String(last), nil
	}
	items, err := materialize(s, v)
	if err != nil {
		// jinja2 takes the last item with reversed(), so the failure
		// names reversibility rather than iterability.
		return value.Undefined, errs.New(errs.TypeError,
			"'%s' object is not reversible", v.TypeName())
	}
	if len(items) == 0 {
		return s.Undefined(value.UndefinedHint("No last item, sequence was empty.")), nil
	}
	return items[len(items)-1], nil
}

// filterRandom picks an element. Its result is necessarily not comparable
// against CPython's; see docs/divergences.md.
func filterRandom(s *State, v value.Value, _ *value.CallArgs) (value.Value, error) {
	items, err := materialize(s, v)
	if err != nil {
		return value.Undefined, err
	}
	if len(items) == 0 {
		return s.Undefined(value.UndefinedHint("No random item, sequence was empty.")), nil
	}
	return items[rand.IntN(len(items))], nil
}

// filterJoin concatenates, escaping items when autoescaping so that a list of
// user strings cannot inject markup through the separator.
func filterJoin(s *State, v value.Value, args *value.CallArgs) (value.Value, error) {
	sep := ""
	if d, ok := arg(args, 0, "d"); ok {
		sep = value.Str(d)
	}
	items, err := materialize(s, v)
	if err != nil {
		return value.Undefined, err
	}
	if attribute, ok := arg(args, 1, "attribute"); ok && !attribute.IsNone() {
		for i, item := range items {
			items[i], err = attrPath(s, item, value.Str(attribute))
			if err != nil {
				return value.Undefined, err
			}
		}
	}

	parts := make([]string, len(items))
	if !s.autoescape {
		for i, item := range items {
			parts[i] = value.Str(item)
		}
		return value.String(strings.Join(parts, sep)), nil
	}
	sepValue := value.String(sep)
	if d, ok := arg(args, 0, "d"); ok {
		sepValue = d
	}
	// Autoescape alone does not make this Markup. do_join only coerces
	// when there is markup to preserve -- a safe delimiter, or a safe item
	// -- and otherwise joins the str()s into a plain string, leaving the
	// escaping to the output. Escaping here regardless looked identical in
	// `{{ xs|join(",") }}` and was wrong for every other use of the
	// result: `{{ ["a", "'"]|join("")|length }}` counted the five
	// characters of `&#39;` and answered 6 where CPython answers 2.
	markup := sepValue.IsSafe()
	for _, item := range items {
		if item.IsSafe() {
			markup = true
		}
	}
	if !markup {
		for i, item := range items {
			parts[i] = value.Str(item)
		}
		return value.String(strings.Join(parts, sep)), nil
	}
	// With markup involved the delimiter is escaped too, and every item
	// that is not already safe -- which is Markup.join's own rule.
	for i, item := range items {
		parts[i] = value.Str(escapeIfNeeded(item))
	}
	return value.Safe(strings.Join(parts, value.Str(escapeIfNeeded(sepValue)))), nil
}

func filterReverse(s *State, v value.Value, _ *value.CallArgs) (value.Value, error) {
	if v.IsString() {
		step := -1
		out, err := value.StrSlice(v.AsString(), nil, nil, &step)
		if err != nil {
			return value.Undefined, err
		}
		if v.IsSafe() {
			return value.Safe(out), nil
		}
		return value.String(out), nil
	}
	items, err := materialize(s, v)
	if err != nil {
		return value.Undefined, errs.New(errs.FilterArgumentError, "argument must be iterable")
	}
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
	return value.NewList(items...), nil
}

func filterSort(s *State, v value.Value, args *value.CallArgs) (value.Value, error) {
	reverse, err := boolArg(args, 0, "reverse", false)
	if err != nil {
		return value.Undefined, err
	}
	caseSensitive, err := boolArg(args, 1, "case_sensitive", false)
	if err != nil {
		return value.Undefined, err
	}
	attribute, _ := arg(args, 2, "attribute")

	items, err := materialize(s, v)
	if err != nil {
		return value.Undefined, err
	}
	if err := stableSortBy(s, items, sortKeyFunc(s, attribute, caseSensitive), reverse); err != nil {
		return value.Undefined, err
	}
	return value.NewList(items...), nil
}

func filterDictsort(s *State, v value.Value, args *value.CallArgs) (value.Value, error) {
	// `by` is settled first, because that is the order jinja2 does it in:
	// the check is the first statement of do_dictsort, ahead of anything
	// that touches the input. `nope|dictsort(1, 2, 3)` therefore reports
	// the bad `by` and not the undefined.
	by := "key"
	if b, ok := arg(args, 1, "by"); ok {
		by = value.Str(b)
	}
	var pos int
	switch by {
	case "key":
		pos = 0
	case "value":
		pos = 1
	default:
		return value.Undefined, errs.New(errs.FilterArgumentError,
			"You can only sort by either \"key\" or \"value\"")
	}

	caseSensitive, err := boolArg(args, 0, "case_sensitive", false)
	if err != nil {
		return value.Undefined, err
	}
	reverse, err := boolArg(args, 2, "reverse", false)
	if err != nil {
		return value.Undefined, err
	}
	if err := requireDefined(v); err != nil {
		return value.Undefined, err
	}

	d, ok := v.Dict()
	if !ok {
		if m, isMapping := v.Interface().(value.Mapping); isMapping && v.Kind() == value.KindObject {
			out := value.NewDict()
			target, _ := out.Dict()
			for _, k := range m.Keys() {
				val, _ := m.GetItem(k)
				_ = target.Set(k, val)
			}
			d, _ = out.Dict()
		} else {
			return value.Undefined, itemsAttributeError(v)
		}
	}

	items := make([]value.Value, 0, d.Len())
	for _, e := range d.Entries() {
		items = append(items, value.NewTuple(e.Key, e.Value))
	}
	key := func(item value.Value) (value.Value, error) {
		s, _ := item.Seq()
		k := s.At(pos)
		if !caseSensitive && k.IsString() {
			return value.String(strings.ToLower(k.AsString())), nil
		}
		return k, nil
	}
	if err := stableSortBy(s, items, key, reverse); err != nil {
		return value.Undefined, err
	}
	return value.NewList(items...), nil
}

func filterUnique(s *State, v value.Value, args *value.CallArgs) (value.Value, error) {
	caseSensitive, err := boolArg(args, 0, "case_sensitive", false)
	if err != nil {
		return value.Undefined, err
	}
	attribute, _ := arg(args, 1, "attribute")
	key := attrKeyFunc(s, attribute, caseSensitive)

	items, err := materialize(s, v)
	if err != nil {
		return value.Undefined, err
	}
	// jinja2 tracks what it has seen in a set, and so does this: a dict
	// keyed by the same hash the language already defines answers in
	// constant time and reports an unhashable key as the error a set would.
	//
	// Scanning the keys seen so far instead is quadratic, and it was not
	// interruptible either -- neither the budget nor the context was
	// consulted between comparisons, so 60,000 distinct items under a
	// three-second deadline were still being compared ninety seconds later.
	seen := value.NewDict()
	index, _ := seen.Dict()
	var out []value.Value
	for _, item := range items {
		k, err := key(item)
		if err != nil {
			return value.Undefined, err
		}
		_, duplicate, err := index.Get(k)
		if err != nil {
			return value.Undefined, err
		}
		if duplicate {
			continue
		}
		if err := index.Set(k, value.None); err != nil {
			return value.Undefined, err
		}
		out = append(out, item)
	}
	return value.NewList(out...), nil
}

func filterMinMax(wantMax bool) Filter {
	return func(s *State, v value.Value, args *value.CallArgs) (value.Value, error) {
		caseSensitive, err := boolArg(args, 0, "case_sensitive", false)
		if err != nil {
			return value.Undefined, err
		}
		attribute, _ := arg(args, 1, "attribute")
		key := attrKeyFunc(s, attribute, caseSensitive)

		items, err := materialize(s, v)
		if err != nil {
			return value.Undefined, err
		}
		if len(items) == 0 {
			return s.Undefined(value.UndefinedHint("No aggregated item, sequence was empty.")), nil
		}

		best := items[0]
		bestKey, err := key(best)
		if err != nil {
			return value.Undefined, err
		}
		for _, item := range items[1:] {
			if err := s.Poll(); err != nil {
				return value.Undefined, err
			}
			k, err := key(item)
			if err != nil {
				return value.Undefined, err
			}
			// Strict comparison keeps the first of equal items,
			// which is what Python's min and max both do.
			op := "<"
			if wantMax {
				op = ">"
			}
			better, err := value.Ordered(op, k, bestKey)
			if err != nil {
				return value.Undefined, err
			}
			if better {
				best, bestKey = item, k
			}
		}
		return best, nil
	}
}

func filterBatch(s *State, v value.Value, args *value.CallArgs) (value.Value, error) {
	size, err := intArg(args, 0, "linecount", 0)
	if err != nil {
		return value.Undefined, err
	}
	if size <= 0 {
		return value.Undefined, errs.New(errs.ValueError, "linecount must be positive")
	}
	fill, hasFill := arg(args, 1, "fill_with")

	items, err := materialize(s, v)
	if err != nil {
		return value.Undefined, err
	}
	// With a fill, every row is padded out to size, so the result holds
	// ceil(len/size)*size elements however few items there are:
	// `[1]|batch(100000000, 0)` is one row of a hundred million. Charged
	// before the rows are built, not after.
	total := int64(len(items))
	if hasFill && !fill.IsNone() && len(items) > 0 {
		rows := int64((len(items) + size - 1) / size)
		total = saturatingMulInt(rows, int64(size))
	}
	if err := s.ChargeItems(total); err != nil {
		return value.Undefined, err
	}

	var rows []value.Value
	for i := 0; i < len(items); i += size {
		row := items[i:min(i+size, len(items))]
		batch := append([]value.Value(nil), row...)
		if hasFill && !fill.IsNone() {
			for len(batch) < size {
				batch = append(batch, fill)
			}
		}
		rows = append(rows, value.NewList(batch...))
	}
	return value.NewList(rows...), nil
}

// filterSlice splits into a fixed number of columns, distributing the
// remainder across the leading ones -- the transpose of batch.
func filterSlice(s *State, v value.Value, args *value.CallArgs) (value.Value, error) {
	count, err := intArg(args, 0, "slices", 0)
	if err != nil {
		return value.Undefined, err
	}
	if count <= 0 {
		return value.Undefined, errs.New(errs.ValueError, "slices must be positive")
	}
	fill, hasFill := arg(args, 1, "fill_with")

	items, err := materialize(s, v)
	if err != nil {
		return value.Undefined, err
	}
	// count is the number of lists about to be built, whether or not there
	// are any items to put in them: `[]|slice(100000000)` allocates a
	// hundred million empty lists. The loop below runs count times before
	// the render sees a single iteration, so the per-pass charge in runLoop
	// would never be reached.
	if err := s.ChargeItems(saturatingMulInt(int64(count), 1)); err != nil {
		return value.Undefined, err
	}
	perSlice, remainder := len(items)/count, len(items)%count

	var out []value.Value
	offset := 0
	for i := range count {
		size := perSlice
		if i < remainder {
			size++
		}
		row := append([]value.Value(nil), items[offset:min(offset+size, len(items))]...)
		offset += size
		// Every slice that did not get one of the extra items is padded,
		// including when there are no items at all.
		if hasFill && !fill.IsNone() && i >= remainder {
			row = append(row, fill)
		}
		out = append(out, value.NewList(row...))
	}
	return value.NewList(out...), nil
}

// filterGroupby sorts by the attribute and then runs together adjacent items
// that share it, yielding (grouper, list) pairs.
func filterGroupby(s *State, v value.Value, args *value.CallArgs) (value.Value, error) {
	attribute, ok := arg(args, 0, "attribute")
	if !ok {
		return value.Undefined, errs.New(errs.FilterArgumentError,
			"groupby() missing required argument 'attribute'")
	}
	def, hasDef := arg(args, 1, "default")
	caseSensitive, err := boolArg(args, 2, "case_sensitive", false)
	if err != nil {
		return value.Undefined, err
	}

	items, err := materialize(s, v)
	if err != nil {
		return value.Undefined, err
	}

	groupKey := func(item value.Value) (value.Value, error) {
		k, err := attrPath(s, item, value.Str(attribute))
		if err != nil {
			return value.Undefined, err
		}
		if k.IsUndefined() && hasDef {
			k = def
		}
		return k, nil
	}
	sortKey := func(item value.Value) (value.Value, error) {
		k, err := groupKey(item)
		if err != nil {
			return value.Undefined, err
		}
		if !caseSensitive && k.IsString() {
			return value.String(strings.ToLower(k.AsString())), nil
		}
		return k, nil
	}
	if err := stableSortBy(s, items, sortKey, false); err != nil {
		return value.Undefined, err
	}

	var out []value.Value
	var current []value.Value
	var currentKey value.Value
	flush := func() {
		if len(current) > 0 {
			out = append(out, value.FromObject(&groupObject{
				key:   currentKey,
				items: value.NewList(current...),
			}))
			current = nil
		}
	}
	for _, item := range items {
		if err := s.Poll(); err != nil {
			return value.Undefined, err
		}
		k, err := groupKey(item)
		if err != nil {
			return value.Undefined, err
		}
		if len(current) == 0 || !value.Equal(k, currentKey) {
			flush()
			currentKey = k
		}
		current = append(current, item)
	}
	flush()
	return value.NewList(out...), nil
}

// filterMap applies a filter, or extracts an attribute, across a sequence.
func filterMap(s *State, v value.Value, args *value.CallArgs) (value.Value, error) {
	// jinja2 short-circuits on a falsey input, so `none|map(...)` is empty
	// rather than a type error.
	if empty, err := isFalsey(v); err != nil || empty {
		return value.NewList(), err
	}
	items, err := materialize(s, v)
	if err != nil {
		return value.Undefined, err
	}

	if attribute, ok := args.Kwarg("attribute"); ok {
		def, hasDef := args.Kwarg("default")
		out := make([]value.Value, len(items))
		for i, item := range items {
			got, err := attrPath(s, item, value.Str(attribute))
			if err != nil {
				return value.Undefined, err
			}
			if got.IsUndefined() && hasDef {
				got = def
			}
			out[i] = got
		}
		return value.NewList(out...), nil
	}

	name, ok := args.Arg(0)
	if !ok {
		return value.Undefined, errs.New(errs.FilterArgumentError,
			"map requires a filter argument")
	}
	fn, ok := s.env.filters[value.Str(name)]
	if !ok {
		// Looked up through Environment.call_filter, whose message has
		// no trailing "found." unlike the deferred compile-time one.
		return value.Undefined, errs.New(errs.TemplateRuntimeError,
			"No filter named %s.", value.Repr(value.String(value.Str(name))))
	}
	rest := &value.CallArgs{Pos: args.Pos[1:], Kwargs: args.Kwargs}

	out := make([]value.Value, len(items))
	for i, item := range items {
		got, err := fn(s, item, rest)
		if err != nil {
			return value.Undefined, err
		}
		out[i] = got
	}
	return value.NewList(out...), nil
}

// filterSelectReject implements select, reject, selectattr and rejectattr.
//
// With no test named, the item's own truthiness decides, which is what makes
// `|select` drop the falsey entries.
func filterSelectReject(keep, byAttribute bool) Filter {
	return func(s *State, v value.Value, args *value.CallArgs) (value.Value, error) {
		if empty, err := isFalsey(v); err != nil || empty {
			return value.NewList(), err
		}
		items, err := materialize(s, v)
		if err != nil {
			return value.Undefined, err
		}

		pos := args.Pos
		var attribute value.Value
		if byAttribute {
			if len(pos) == 0 {
				return value.Undefined, errs.New(errs.FilterArgumentError,
					"selectattr requires an attribute name")
			}
			attribute, pos = pos[0], pos[1:]
		}

		var testFn Test
		var testArgs *value.CallArgs
		if len(pos) > 0 {
			name := value.Str(pos[0])
			fn, ok := s.env.tests[name]
			if !ok {
				return value.Undefined, errs.New(errs.TemplateRuntimeError,
					"No test named %s.", value.Repr(value.String(name)))
			}
			testFn = fn
			testArgs = &value.CallArgs{Pos: pos[1:], Kwargs: args.Kwargs}
		}

		var out []value.Value
		for _, item := range items {
			subject := item
			if byAttribute {
				subject, err = attrPath(s, item, value.Str(attribute))
				if err != nil {
					return value.Undefined, err
				}
			}

			var matched bool
			if testFn != nil {
				matched, err = testFn(s, subject, testArgs)
			} else {
				matched, err = value.IsTrue(subject)
			}
			if err != nil {
				return value.Undefined, err
			}
			if matched == keep {
				out = append(out, item)
			}
		}
		return value.NewList(out...), nil
	}
}

// isFalsey reports whether a filter should short-circuit on its input.
//
// jinja2's prepare_map and prepare_select_or_reject both begin with
// `if not seq: return`, so an empty or falsey sequence -- None and undefined
// included -- yields nothing instead of failing.
func isFalsey(v value.Value) (bool, error) {
	if v.IsUndefined() {
		if v.UndefinedBehavior() == value.UndefinedStrict {
			return false, v.UndefinedError()
		}
		return true, nil
	}
	truth, err := value.IsTrue(v)
	return !truth, err
}

// groupObject is one result of |groupby.
//
// jinja2 returns a named tuple whose fields are `grouper` and `list`, so a
// template can write either `{{ group.grouper }}` or `{% for key, items in
// ... %}`. It reports the tuple repr to hide the subclass, but names itself
// _GroupTuple in a type error -- both of which are visible, so both are here.
type groupObject struct {
	key   value.Value
	items value.Value
}

func (g *groupObject) GetAttr(name string) (value.Value, bool) {
	switch name {
	case "grouper":
		return g.key, true
	case "list":
		return g.items, true
	}
	return value.Undefined, false
}

// AsTuple exposes the pair this stands for, so JSON and anything else that
// handles tuples treats it as one.
func (g *groupObject) AsTuple() value.Value { return value.NewTuple(g.key, g.items) }

func (g *groupObject) Len() int { return 2 }

func (g *groupObject) GetIndex(i int) (value.Value, bool) {
	switch i {
	case 0:
		return g.key, true
	case 1:
		return g.items, true
	}
	return value.Undefined, false
}

func (g *groupObject) Repr() string {
	return "(" + value.Repr(g.key) + ", " + value.Repr(g.items) + ")"
}

func (g *groupObject) TypeName() string { return "_GroupTuple" }

func (g *groupObject) QualifiedName() string { return "jinja2.filters._GroupTuple" }
