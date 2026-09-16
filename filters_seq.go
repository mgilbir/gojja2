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
	n, err := value.Len(v)
	if err != nil {
		return value.Undefined, err
	}
	return value.Int(int64(n)), nil
}

func filterList(_ *State, v value.Value, _ *value.CallArgs) (value.Value, error) {
	items, err := materialize(v)
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
	items, err := materialize(v)
	if err != nil {
		return value.Undefined, err
	}
	if len(items) == 0 {
		return s.Undefined(value.UndefinedHint("No first item, sequence was empty.")), nil
	}
	return items[0], nil
}

func filterLast(s *State, v value.Value, _ *value.CallArgs) (value.Value, error) {
	items, err := materialize(v)
	if err != nil {
		return value.Undefined, err
	}
	if len(items) == 0 {
		return s.Undefined(value.UndefinedHint("No last item, sequence was empty.")), nil
	}
	return items[len(items)-1], nil
}

// filterRandom picks an element. Its result is necessarily not comparable
// against CPython's; see docs/divergences.md.
func filterRandom(s *State, v value.Value, _ *value.CallArgs) (value.Value, error) {
	items, err := materialize(v)
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
	items, err := materialize(v)
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
	for i, item := range items {
		parts[i] = value.Str(escapeIfNeeded(item))
	}
	return value.Safe(strings.Join(parts, value.Str(escapeIfNeeded(sepValue)))), nil
}

func filterReverse(_ *State, v value.Value, _ *value.CallArgs) (value.Value, error) {
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
	items, err := materialize(v)
	if err != nil {
		return value.Undefined, err
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

	items, err := materialize(v)
	if err != nil {
		return value.Undefined, err
	}
	if err := stableSortBy(items, sortKeyFunc(s, attribute, caseSensitive), reverse); err != nil {
		return value.Undefined, err
	}
	return value.NewList(items...), nil
}

func filterDictsort(s *State, v value.Value, args *value.CallArgs) (value.Value, error) {
	caseSensitive, err := boolArg(args, 0, "case_sensitive", false)
	if err != nil {
		return value.Undefined, err
	}
	by := "key"
	if b, ok := arg(args, 1, "by"); ok {
		by = value.Str(b)
	}
	reverse, err := boolArg(args, 2, "reverse", false)
	if err != nil {
		return value.Undefined, err
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
			return value.Undefined, errs.New(errs.TypeError,
				"'%s' object is not a mapping", v.TypeName())
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
	if err := stableSortBy(items, key, reverse); err != nil {
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
	key := sortKeyFunc(s, attribute, caseSensitive)

	items, err := materialize(v)
	if err != nil {
		return value.Undefined, err
	}
	var seen []value.Value
	var out []value.Value
	for _, item := range items {
		k, err := key(item)
		if err != nil {
			return value.Undefined, err
		}
		duplicate := false
		for _, prev := range seen {
			if value.Equal(prev, k) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			seen = append(seen, k)
			out = append(out, item)
		}
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
		key := sortKeyFunc(s, attribute, caseSensitive)

		items, err := materialize(v)
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

func filterBatch(_ *State, v value.Value, args *value.CallArgs) (value.Value, error) {
	size, err := intArg(args, 0, "linecount", 0)
	if err != nil {
		return value.Undefined, err
	}
	if size <= 0 {
		return value.Undefined, errs.New(errs.ValueError, "linecount must be positive")
	}
	fill, hasFill := arg(args, 1, "fill_with")

	items, err := materialize(v)
	if err != nil {
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
func filterSlice(_ *State, v value.Value, args *value.CallArgs) (value.Value, error) {
	count, err := intArg(args, 0, "slices", 0)
	if err != nil {
		return value.Undefined, err
	}
	if count <= 0 {
		return value.Undefined, errs.New(errs.ValueError, "slices must be positive")
	}
	fill, hasFill := arg(args, 1, "fill_with")

	items, err := materialize(v)
	if err != nil {
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
		if hasFill && !fill.IsNone() && i >= remainder && remainder > 0 {
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

	items, err := materialize(v)
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
	if err := stableSortBy(items, sortKey, false); err != nil {
		return value.Undefined, err
	}

	var out []value.Value
	var current []value.Value
	var currentKey value.Value
	flush := func() {
		if len(current) > 0 {
			out = append(out, value.NewTuple(currentKey, value.NewList(current...)))
			current = nil
		}
	}
	for _, item := range items {
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
	items, err := materialize(v)
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
		return value.Undefined, errs.New(errs.TemplateAssertionError,
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
		items, err := materialize(v)
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
				return value.Undefined, errs.New(errs.TemplateAssertionError,
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
