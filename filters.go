// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/mgilbir/gojja2/errs"
	"github.com/mgilbir/gojja2/value"
)

func registerDefaultFilters(env *Environment) {
	add := func(name string, f Filter) { env.AddFilter(name, f) }

	// text
	add("upper", stringFilter(strings.ToUpper))
	add("lower", stringFilter(strings.ToLower))
	// title is the one case filter that does not preserve Markup: jinja2
	// assembles it with "".join(...), and joining on a plain str gives a
	// plain str.
	add("title", func(_ *State, v value.Value, _ *value.CallArgs) (value.Value, error) {
		return value.String(jinjaTitle(value.Str(v))), nil
	})
	add("capitalize", stringFilter(pythonCapitalize))
	add("trim", filterTrim)
	add("string", filterString)
	add("replace", filterReplace)
	add("center", filterCenter)
	add("indent", filterIndent)
	add("truncate", filterTruncate)
	add("wordwrap", filterWordwrap)
	add("wordcount", filterWordcount)
	add("striptags", filterStriptags)
	add("format", filterFormat)
	add("urlencode", filterURLEncode)
	add("urlize", filterUrlize)
	add("filesizeformat", definedFilter(filterFilesizeformat))
	add("pprint", filterPprint)
	add("tojson", filterToJSON)
	add("xmlattr", definedFilter(filterXMLAttr))

	// escaping
	add("safe", filterSafe)
	add("escape", filterEscape)
	add("e", filterEscape)
	add("forceescape", filterForceEscape)

	// numbers
	add("abs", filterAbs)
	add("int", definedFilter(filterInt))
	add("float", definedFilter(filterFloat))
	add("round", filterRound)
	add("sum", filterSum)

	// sequences
	add("length", filterLength)
	add("count", filterLength)
	add("list", filterList)
	add("items", filterItems)
	add("first", filterFirst)
	add("last", filterLast)
	add("random", filterRandom)
	add("join", filterJoin)
	add("reverse", filterReverse)
	add("sort", filterSort)
	// Not wrapped in definedFilter: jinja2 validates `by` before it looks
	// at the input at all, so an undefined input with a bad `by` reports
	// the bad `by`.
	add("dictsort", filterDictsort)
	add("unique", filterUnique)
	add("min", filterMinMax(false))
	add("max", filterMinMax(true))
	add("batch", filterBatch)
	add("slice", filterSlice)
	add("groupby", filterGroupby)
	add("map", filterMap)
	add("select", filterSelectReject(true, false))
	add("reject", filterSelectReject(false, false))
	add("selectattr", filterSelectReject(true, true))
	add("rejectattr", filterSelectReject(false, true))

	// misc
	add("default", filterDefault)
	add("d", filterDefault)
	add("attr", filterAttr)
}

// --- helpers -----------------------------------------------------------------

// requireDefined rejects an undefined input.
//
// Most filters tolerate undefined -- `{{ nope|upper }}` is "" -- but the ones
// that do arithmetic or indexing on their argument raise in jinja2, because
// Undefined has no __int__, __iter__ or __len__ to offer them. Which filters
// those are is not guessable; it is whatever CPython does, and it is asserted
// by the conformance corpus.
func requireDefined(v value.Value) error {
	if v.IsUndefined() {
		return v.UndefinedError()
	}
	return nil
}

func definedFilter(f Filter) Filter {
	return func(s *State, v value.Value, args *value.CallArgs) (value.Value, error) {
		if err := requireDefined(v); err != nil {
			return value.Undefined, err
		}
		return f(s, v, args)
	}
}

// stringFilter adapts a plain string transform, keeping Markup markup.
//
// markupsafe's Markup overrides the str methods these filters use, and they
// return Markup: changing the case of escaped text cannot unescape it. So
// `{{ x|safe|upper }}` stays safe, and reports its type as Markup.
func stringFilter(fn func(string) string) Filter {
	return func(_ *State, v value.Value, _ *value.CallArgs) (value.Value, error) {
		out := fn(value.Str(v))
		if v.IsSafe() {
			return value.Safe(out), nil
		}
		return value.String(out), nil
	}
}

// keepSafe carries a value's Markup-ness onto a derived string.
func keepSafe(src value.Value, out string) value.Value {
	if src.IsSafe() {
		return value.Safe(out)
	}
	return value.String(out)
}

// materialize collects an iterable into a slice, which most sequence filters
// need because they reorder or count their input.
//
// Every filter that needs a sequence in hand goes through here, which makes it
// the one place that can charge the walk against the render's budget. Without
// that, `{{ range(10000000000)|list }}` allocates until the process dies: the
// loop bound in runLoop never sees it, because no {% for %} is involved.
func materialize(s *State, v value.Value) ([]value.Value, error) {
	seq, err := value.Iterate(v)
	if err != nil {
		return nil, err
	}
	var out []value.Value
	for item := range seq {
		// Charged before the append, so the slice never grows past
		// the bound by even one element.
		if err := s.Step(1); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

// attrPath resolves a jinja2 attribute specification, which may be dotted
// ("user.name") and may address a sequence by index ("0.name").
func attrPath(s *State, v value.Value, path string) (value.Value, error) {
	ex := &exec{st: s, sc: s.ctx, autoescape: s.autoescape}
	for _, part := range strings.Split(path, ".") {
		// No early exit for an undefined receiver: the next lookup has
		// to raise, which is what makes a second |map(attribute=...)
		// over the results of a first one fail.
		if n, err := strconv.Atoi(part); err == nil {
			item, err := ex.getItem(v, value.Int(int64(n)))
			if err != nil {
				return value.Undefined, err
			}
			v = item
			continue
		}
		attr, err := ex.getAttr(v, part)
		if err != nil {
			return value.Undefined, err
		}
		v = attr
	}
	return v, nil
}

// attrKeyFunc builds the key extractor for the filters that compare bare
// values: min, max, unique and groupby.
//
// sortKeyFunc wraps the same key in a list, because jinja2's sort uses
// make_multi_attrgetter while these use make_attrgetter. The difference is
// visible: a list comparison settles equal keys without ordering them, so
// `[nope1, nope2]|sort` succeeds where `[nope1, nope2]|max` raises.
func attrKeyFunc(s *State, attribute value.Value, caseSensitive bool) func(value.Value) (value.Value, error) {
	fold := func(v value.Value) value.Value {
		if !caseSensitive && v.IsString() {
			return value.String(strings.ToLower(v.AsString()))
		}
		return v
	}
	if attribute.IsUndefined() || attribute.IsNone() {
		return func(v value.Value) (value.Value, error) { return fold(v), nil }
	}
	path := strings.TrimSpace(value.Str(attribute))
	return func(v value.Value) (value.Value, error) {
		k, err := attrPath(s, v, path)
		if err != nil {
			return value.Undefined, err
		}
		return fold(k), nil
	}
}

// sortKeyFunc builds the key extractor a sorting filter uses.
func sortKeyFunc(s *State, attribute value.Value, caseSensitive bool) func(value.Value) (value.Value, error) {
	fold := func(v value.Value) value.Value {
		if !caseSensitive && v.IsString() {
			return value.String(strings.ToLower(v.AsString()))
		}
		return v
	}
	// The key is always a list, even for a single sort field.
	//
	// That is not decoration: list comparison tests elements for equality
	// before ordering them, so two equal keys never reach `<`. It is what
	// lets `{{ [nope1, nope2]|sort }}` succeed while `{{ nope1 < nope2 }}`
	// raises -- two undefineds are equal, so nothing asks which is smaller.
	if attribute.IsUndefined() || attribute.IsNone() {
		return func(v value.Value) (value.Value, error) {
			return value.NewList(fold(v)), nil
		}
	}
	// A comma-separated specification sorts by several keys in turn.
	paths := strings.Split(value.Str(attribute), ",")
	return func(v value.Value) (value.Value, error) {
		keys := make([]value.Value, len(paths))
		for i, p := range paths {
			k, err := attrPath(s, v, strings.TrimSpace(p))
			if err != nil {
				return value.Undefined, err
			}
			keys[i] = fold(k)
		}
		return value.NewList(keys...), nil
	}
}

// stableSortBy sorts in place, reproducing CPython's comparison order.
//
// The order is observable, because comparing incomparable values raises and
// the message names the two operands the sort happened to reach first.
// CPython reverses the slice *before* sorting when reverse is set and reverses
// it again afterwards -- so `[1, 'a', 2.5, True, None]|sort(true)` fails on
// True against None, the first pair of the reversed list, and not on the first
// pair of the original.
func stableSortBy(s *State, items []value.Value, key func(value.Value) (value.Value, error), reverse bool) error {
	keys := make([]value.Value, len(items))
	for i, item := range items {
		if err := s.Poll(); err != nil {
			return err
		}
		k, err := key(item)
		if err != nil {
			return err
		}
		keys[i] = k
	}

	if reverse {
		reverseBoth(items, keys)
	}
	err := pythonSort(s, items, keys)
	if reverse {
		reverseBoth(items, keys)
	}
	return err
}

func reverseBoth(items, keys []value.Value) {
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
		keys[i], keys[j] = keys[j], keys[i]
	}
}

// binarySortLimit is the length below which CPython sorts a list as a single
// run. Above it timsort splits and merges, and the comparison order stops
// being worth reproducing: the result is the same either way, only the
// operands named by a comparison failure differ.
const binarySortLimit = 64

func pythonSort(s *State, items, keys []value.Value) error {
	n := len(items)
	if n < 2 {
		return nil
	}
	if n > binarySortLimit {
		return stableSortFallback(s, items, keys)
	}

	var failure error
	less := func(i, j int) bool {
		if failure != nil {
			return false
		}
		if err := s.Poll(); err != nil {
			failure = err
			return false
		}
		ok, err := value.Ordered("<", keys[i], keys[j])
		if err != nil {
			failure = err
		}
		return ok
	}
	swap := func(i, j int) {
		items[i], items[j] = items[j], items[i]
		keys[i], keys[j] = keys[j], keys[i]
	}

	// count_run: measure the ordered run the list already starts with, and
	// flip it if it runs downwards.
	runLen := 2
	descending := less(1, 0)
	if failure != nil {
		return failure
	}
	if descending {
		for runLen < n && less(runLen, runLen-1) {
			runLen++
		}
		for i, j := 0, runLen-1; i < j; i, j = i+1, j-1 {
			swap(i, j)
		}
	} else {
		for runLen < n && !less(runLen, runLen-1) {
			runLen++
		}
	}
	if failure != nil {
		return failure
	}

	// binarysort: place each remaining element by binary search, comparing
	// the element being placed against the midpoint.
	for start := runLen; start < n; start++ {
		pivotItem, pivotKey := items[start], keys[start]
		lo, hi := 0, start
		for lo < hi {
			mid := lo + (hi-lo)/2
			if err := s.Poll(); err != nil {
				return err
			}
			ok, err := value.Ordered("<", pivotKey, keys[mid])
			if err != nil {
				return err
			}
			if ok {
				hi = mid
			} else {
				lo = mid + 1
			}
		}
		copy(items[lo+1:start+1], items[lo:start])
		copy(keys[lo+1:start+1], keys[lo:start])
		items[lo], keys[lo] = pivotItem, pivotKey
	}
	return nil
}

// stableSortFallback keeps long lists out of a quadratic sort. The result is
// the same stable ordering; only the comparison order differs.
func stableSortFallback(s *State, items, keys []value.Value) error {
	var failure error
	idx := make([]int, len(items))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool {
		if failure != nil {
			return false
		}
		if err := s.Poll(); err != nil {
			failure = err
			return false
		}
		ok, err := value.Ordered("<", keys[idx[a]], keys[idx[b]])
		if err != nil {
			failure = err
		}
		return ok
	})
	if failure != nil {
		return failure
	}
	sortedItems := make([]value.Value, len(items))
	sortedKeys := make([]value.Value, len(keys))
	for i, j := range idx {
		sortedItems[i], sortedKeys[i] = items[j], keys[j]
	}
	copy(items, sortedItems)
	copy(keys, sortedKeys)
	return nil
}

func boolArg(args *value.CallArgs, i int, name string, def bool) (bool, error) {
	v, ok := arg(args, i, name)
	if !ok {
		return def, nil
	}
	return value.IsTrue(v)
}

// --- text filters ------------------------------------------------------------

func filterTrim(_ *State, v value.Value, args *value.CallArgs) (value.Value, error) {
	text := value.Str(v)
	if chars, ok := arg(args, 0, "chars"); ok && !chars.IsNone() {
		if !chars.IsString() {
			return value.Undefined, errs.New(errs.TypeError,
				"strip arg must be None or str")
		}
		return keepSafe(v, strings.Trim(text, chars.AsString())), nil
	}
	return keepSafe(v, strings.TrimFunc(text, unicode.IsSpace)), nil
}

// filterString converts to str, leaving a Markup value safe.
func filterString(_ *State, v value.Value, _ *value.CallArgs) (value.Value, error) {
	if v.IsString() {
		return v, nil
	}
	return value.String(value.Str(v)), nil
}

// filterReplace implements jinja2's do_replace, whose autoescaping rule is
// finer than it looks; see below.
func filterReplace(s *State, v value.Value, args *value.CallArgs) (value.Value, error) {
	old, ok := arg(args, 0, "old")
	if !ok {
		return value.Undefined, errs.New(errs.FilterArgumentError,
			"replace() missing required argument 'old'")
	}
	new, ok := arg(args, 1, "new")
	if !ok {
		return value.Undefined, errs.New(errs.FilterArgumentError,
			"replace() missing required argument 'new'")
	}
	count, err := intArg(args, 2, "count", -1)
	if err != nil {
		return value.Undefined, err
	}

	if !s.autoescape {
		src, from, to := value.Str(v), value.Str(old), value.Str(new)
		if err := chargeReplace(s, src, from, to, count); err != nil {
			return value.Undefined, err
		}
		return value.String(strings.Replace(src, from, to, count)), nil
	}
	// Under autoescape the rule is not "escape everything", and escaping
	// everything got two things wrong. `old` is matched verbatim -- so
	// `{{ x|replace("&", "+") }}` finds the ampersands the subject really
	// has, not the `&amp;` an eager escape would have left -- and the
	// subject stays a plain string when nothing markup is involved, to be
	// escaped once on the way out like any other value.
	//
	// What jinja2 writes is `escape(s) if old is Markup or (new is Markup
	// and s is not) else soft_str(s)`, followed by a replace that escapes
	// `new` only when the subject it is replacing into is Markup. The odd
	// shape of that condition is Python operator precedence, and it is
	// reproduced rather than tidied.
	markup := v.IsSafe()
	src := value.Str(v)
	if old.IsSafe() || (new.IsSafe() && !v.IsSafe()) {
		if !v.IsSafe() {
			src = escapeHTML(src)
		}
		markup = true
	}
	from, to := value.Str(old), value.Str(new)
	if markup && !new.IsSafe() {
		to = escapeHTML(to)
	}
	if err := chargeReplace(s, src, from, to, count); err != nil {
		return value.Undefined, err
	}
	out := strings.Replace(src, from, to, count)
	if markup {
		return value.Safe(out), nil
	}
	return value.String(out), nil
}

func filterCenter(s *State, v value.Value, args *value.CallArgs) (value.Value, error) {
	width, err := intArg(args, 0, "width", 80)
	if err != nil {
		return value.Undefined, err
	}
	padded, err := pad(s, value.Str(v), width, " ", padCentered)
	if err != nil {
		return value.Undefined, err
	}
	return keepSafe(v, value.Str(padded)), nil
}

func filterIndent(s *State, v value.Value, args *value.CallArgs) (value.Value, error) {
	// The width may be given as the indent string itself.
	prefix := "    "
	if w, ok := arg(args, 0, "width"); ok {
		if w.IsString() {
			prefix = w.AsString()
		} else {
			// jinja2 writes `" " * width`, so a width that is not
			// an integer fails as a sequence repetition, naming the
			// type it could not repeat by -- not as a bad argument.
			// Going through the operator inherits that wording, the
			// bool that counts as 1, the negative that repeats
			// nothing, and the charge against the render budget.
			p, err := value.Mul(value.String(" "), w, s)
			if err != nil {
				return value.Undefined, err
			}
			prefix = p.AsString()
		}
	}
	first, err := boolArg(args, 1, "first", false)
	if err != nil {
		return value.Undefined, err
	}
	blank, err := boolArg(args, 2, "blank", false)
	if err != nil {
		return value.Undefined, err
	}

	// Only now is the value itself touched: jinja2 sizes the indent from
	// `width` first, so an undefined input outlives a width that cannot be
	// repeated by.
	if err := requireDefined(v); err != nil {
		return value.Undefined, err
	}

	// jinja2 writes `s += newline` and then calls s.splitlines(). The
	// augmented assignment is not plain `+`: a list has __iadd__ and
	// extends, so it survives and dies on splitlines instead, while
	// everything else fails on the assignment with whatever `+` would
	// have said -- reworded to name `+=`.
	switch {
	case v.Kind() == value.KindList:
		return value.Undefined, errs.New(errs.AttributeError,
			"'%s' object has no attribute 'splitlines'", v.TypeName())
	case !v.IsString():
		if _, err := value.Add(v, value.String("\n")); err != nil {
			return value.Undefined, augmentedAssign(err)
		}
		return value.Undefined, errs.New(errs.AttributeError,
			"'%s' object has no attribute 'splitlines'", v.TypeName())
	}
	// The first line is handled apart from the rest: `first` indents it
	// whether or not it is blank, while `blank` governs only the lines
	// after it. Conflating the two makes `""|indent(2, true)` empty
	// instead of two spaces.
	// jinja2 writes `s += newline` and then s.splitlines(), so the append
	// is the reason a trailing line survives -- and splitlines is the
	// reason a carriage return breaks a line here too.
	lines := splitLines(value.Str(v)+"\n", false)
	head, rest := lines[0], lines[1:]
	out := head
	if len(rest) > 0 {
		indented := make([]string, len(rest))
		for i, line := range rest {
			if blank || line != "" {
				line = prefix + line
			}
			indented[i] = line
		}
		out += "\n" + strings.Join(indented, "\n")
	}
	if first {
		out = prefix + out
	}

	// jinja2 makes the indentation and the newline Markup when the input
	// is, so the joined result stays Markup.
	return keepSafe(v, out), nil
}

// splitLines is Python's str.splitlines: it breaks on a newline, a carriage
// return or the pair, keeps no empty last element for a trailing break, and
// gives an empty string no lines at all -- which is why `{{ ""|wordwrap("z") }}`
// renders nothing rather than complaining about the width.
func splitLines(s string, keepEnds bool) []string {
	var out []string
	for len(s) > 0 {
		i := strings.IndexAny(s, "\n\r")
		if i < 0 {
			out = append(out, s)
			break
		}
		end := i + 1
		if s[i] == '\r' && end < len(s) && s[end] == '\n' {
			end++
		}
		if keepEnds {
			out = append(out, s[:end])
		} else {
			out = append(out, s[:i])
		}
		s = s[end:]
	}
	return out
}

func filterTruncate(s *State, v value.Value, args *value.CallArgs) (value.Value, error) {
	length, err := intArg(args, 0, "length", 255)
	if err != nil {
		return value.Undefined, err
	}
	killwords, err := boolArg(args, 1, "killwords", false)
	if err != nil {
		return value.Undefined, err
	}
	end, endLen := "...", 3
	if e, ok := arg(args, 2, "end"); ok {
		// jinja2 asserts `length >= len(end)`, so an end with no length
		// -- a number, say -- fails as len() does rather than being
		// stringified. And the comparison counts characters, which for
		// a non-ASCII end is not the same as counting bytes.
		n, err := value.Len(e)
		if err != nil {
			return value.Undefined, err
		}
		end, endLen = value.Str(e), n
	}
	leeway, err := intArg(args, 3, "leeway", s.env.policies.TruncateLeeway)
	if err != nil {
		return value.Undefined, err
	}
	if length < endLen {
		// jinja2 spells this as a bare assert, so the class is
		// AssertionError rather than the ValueError the wording
		// suggests.
		return value.Undefined, errs.New(errs.AssertionError,
			"expected length >= %d, got %d", endLen, length)
	}

	// jinja2 measures len(s) on the value itself, not on its string form,
	// and returns it unchanged when it is short enough -- as the value. A
	// dict of two entries is length 2 however long its repr is, and
	// `[]|truncate(15)` is the empty list, which is falsey where its
	// string form "[]" would be truthy.
	size, err := value.Len(v)
	if err != nil {
		return value.Undefined, err
	}
	if size <= length+leeway {
		return v, nil
	}
	text := value.Str(v)
	// Past the length check jinja2 slices the value and then, unless
	// killwords, calls rsplit on it -- so a non-string gets this far and
	// fails on one of those rather than on being the wrong kind of input.
	if !v.IsString() {
		if killwords {
			sliced, err := sliceValue(v, length-value.StrLen(end))
			if err != nil {
				return value.Undefined, err
			}
			_, err = value.Add(sliced, value.String(end))
			return value.Undefined, err
		}
		return value.Undefined, errs.New(errs.AttributeError,
			"'%s' object has no attribute 'rsplit'", v.TypeName())
	}

	head, _ := value.StrSlice(text, nil, ptr(length-value.StrLen(end)), nil)
	if killwords {
		return keepSafe(v, head+end), nil
	}
	if i := strings.LastIndexByte(head, ' '); i >= 0 {
		head = head[:i]
	}
	return keepSafe(v, head+end), nil
}

func ptr[T any](v T) *T { return &v }

func filterWordwrap(_ *State, v value.Value, args *value.CallArgs) (value.Value, error) {
	// The width is carried as a value rather than converted here. jinja2
	// hands it straight to textwrap, which only looks at it once it has a
	// line to wrap -- so `{{ ""|wordwrap("z") }}` renders nothing, and a
	// width that is a float wraps happily until something has to be sliced
	// by it.
	width := value.Int(79)
	if w, ok := arg(args, 0, "width"); ok {
		width = w
	}
	breakLong, err := boolArg(args, 1, "break_long_words", true)
	if err != nil {
		return value.Undefined, err
	}
	breakOnHyphens, err := boolArg(args, 3, "break_on_hyphens", true)
	if err != nil {
		return value.Undefined, err
	}
	wrapString := "\n"
	if w, ok := arg(args, 2, "wrapstring"); ok && !w.IsNone() {
		// jinja2's body is `wrapstring.join([... for line in
		// s.splitlines()])`, and Python resolves the attribute on the
		// left before evaluating the argument -- so a wrapstring that
		// is not a string fails first, ahead of anything about s.
		if !w.IsString() {
			return value.Undefined, errs.New(errs.AttributeError,
				"'%s' object has no attribute 'join'", w.TypeName())
		}
		wrapString = value.Str(w)
	}

	// s is reached only at s.splitlines(), which Python evaluates after it
	// has resolved the attribute on the left of the join -- so an
	// undefined input outlives a wrapstring that has no join to call.
	if err := requireDefined(v); err != nil {
		return value.Undefined, err
	}

	// jinja2 calls value.splitlines(), so a non-string fails as a missing
	// attribute rather than being stringified.
	if !v.IsString() {
		return value.Undefined, errs.New(errs.AttributeError,
			"'%s' object has no attribute 'splitlines'", v.TypeName())
	}

	var out []string
	for _, paragraph := range splitLines(value.Str(v), false) {
		wrapped, err := wrapLine(paragraph, width, breakLong, breakOnHyphens)
		if err != nil {
			return value.Undefined, err
		}
		out = append(out, wrapped...)
	}
	return value.String(strings.Join(out, wrapString)), nil
}

// wrapLine reproduces textwrap._wrap_chunks for one paragraph.
//
// The details decide where the breaks land, and guessing at them gives output
// that is close but wrong on most inputs:
//
//   - a chunk is only treated as an over-long word when it exceeds the *whole*
//     width, not merely the space left on the current line;
//   - such a word is cut at exactly the space remaining, which may be nothing,
//     leaving an empty piece;
//   - exactly one trailing whitespace chunk is dropped from a finished line,
//     so a line can still end in a space when an empty piece was dropped
//     ahead of it.
func wrapLine(text string, widthVal value.Value, breakLong, breakOnHyphens bool) ([]string, error) {
	// textwrap checks the width before anything else, and it checks it with
	// Python's own comparison -- which is what refuses a string, a list or
	// None here, naming the operator, rather than an argument check at the
	// filter's door. It happens once per line, so a value with no lines
	// never reaches it.
	tooNarrow, err := value.Ordered("<=", widthVal, value.Int(0))
	if err != nil {
		return nil, err
	}
	if tooNarrow {
		return nil, errs.New(errs.ValueError,
			"invalid width %s (must be > 0)", value.Repr(widthVal))
	}
	// Past that, the width is only compared against -- so a float wraps as
	// its value -- until a word has to be cut at it, which is a slice, and
	// a slice index has to be an integer.
	width, _ := widthVal.Float64()
	sliceable := widthVal.IsInteger()

	chunks := wrapChunks(text, breakOnHyphens)
	var lines []string

	for len(chunks) > 0 {
		// Progress is either consuming a chunk or shortening the one
		// at the front, so both are watched.
		beforeCount, beforeHead := len(chunks), len(chunks[0])

		// Leading whitespace is dropped on every line but the first.
		if len(lines) > 0 && strings.TrimSpace(chunks[0]) == "" {
			chunks = chunks[1:]
			if len(chunks) == 0 {
				break
			}
		}

		var cur []string
		curLen := 0
		for len(chunks) > 0 {
			size := value.StrLen(chunks[0])
			if float64(curLen+size) > width {
				break
			}
			cur = append(cur, chunks[0])
			chunks = chunks[1:]
			curLen += size
		}

		// The next chunk is too big for any line, not just this one.
		if len(chunks) > 0 && float64(value.StrLen(chunks[0])) > width {
			if breakLong {
				// A width below 1 cuts one character, which is
				// a literal 1 and so always a valid index; any
				// other width becomes the index itself, and
				// textwrap gives up on one that is not whole.
				space := 1
				if width >= 1 {
					if !sliceable {
						return nil, errs.New(errs.TypeError,
							"slice indices must be integers or None or have an __index__ method")
					}
					space = max(int(width)-curLen, 0)
				}
				if breakOnHyphens && space > 0 {
					if at := lastHyphenBefore(chunks[0], space); at > 0 {
						space = at + 1
					}
				}
				head, _ := value.StrSlice(chunks[0], nil, &space, nil)
				tail, _ := value.StrSlice(chunks[0], &space, nil, nil)
				cur = append(cur, head)
				chunks[0] = tail
			} else if len(cur) == 0 {
				cur = append(cur, chunks[0])
				chunks = chunks[1:]
			}
		}

		// Exactly one trailing whitespace chunk goes, not every one.
		if len(cur) > 0 && strings.TrimSpace(cur[len(cur)-1]) == "" {
			cur = cur[:len(cur)-1]
		}
		if len(cur) > 0 {
			lines = append(lines, strings.Join(cur, ""))
		}
		// A pass can legitimately produce no line -- a lone space
		// consumed and then dropped -- but it must make progress, or
		// the loop would spin.
		if len(chunks) == beforeCount && (len(chunks) == 0 || len(chunks[0]) == beforeHead) {
			break
		}
	}
	if len(lines) == 0 {
		return []string{""}, nil
	}
	return lines, nil
}

// lastHyphenBefore finds the hyphen a long word may be broken after, which
// textwrap prefers over cutting mid-word. It reports -1 when there is none, or
// when everything before it is hyphens.
func lastHyphenBefore(chunk string, limit int) int {
	runes := []rune(chunk)
	if limit > len(runes) {
		limit = len(runes)
	}
	for i := limit - 1; i > 0; i-- {
		if runes[i] != '-' {
			continue
		}
		for _, r := range runes[:i] {
			if r != '-' {
				return i
			}
		}
		return -1
	}
	return -1
}

// wrapChunks splits text into the pieces textwrap considers indivisible:
// whitespace runs, and words, optionally broken after an internal hyphen.
func wrapChunks(text string, breakOnHyphens bool) []string {
	var chunks []string
	runes := []rune(text)
	i := 0
	for i < len(runes) {
		start := i
		inSpace := unicode.IsSpace(runes[i])
		for i < len(runes) && unicode.IsSpace(runes[i]) == inSpace {
			i++
		}
		word := string(runes[start:i])
		if inSpace || !breakOnHyphens {
			chunks = append(chunks, word)
			continue
		}
		chunks = append(chunks, splitOnHyphens(word)...)
	}
	return chunks
}

// splitOnHyphens breaks a word where textwrap's wordsep_re allows a line to
// end. "After a hyphen between two letters" is close, and close is wrong often
// enough to see: the pattern asks for more on both sides.
//
// A single hyphen splits when what precedes it is two letters, or a letter, a
// hyphen and a letter -- and when what follows is a letter, then optionally one
// hyphen, then another letter. `well-known` splits and `a-b` does not, because
// one letter is not two; `a-b-c-d` splits once, after `a-b-`, because `c-d` has
// nothing to follow it. A letter here is Python's [^\d\W]: a digit does not
// count, which is why `a-1-b` is one chunk, and an underscore does.
//
// Two or more hyphens are an em-dash instead, and become a chunk of their own
// when they sit between a word character and a word character: `a--b` is three
// chunks where `a-b` is one.
func splitOnHyphens(word string) []string {
	runes := []rune(word)
	var out []string
	start := 0
	for i := 0; i < len(runes); i++ {
		if runes[i] != '-' {
			continue
		}
		if run := dashRun(runes, i); run >= 2 {
			if i > 0 && isWordPunct(runes[i-1]) && i+run < len(runes) && isWordChar(runes[i+run]) {
				if i > start {
					out = append(out, string(runes[start:i]))
				}
				out = append(out, string(runes[i:i+run]))
				start = i + run
			}
			i += run - 1
			continue
		}
		if splitsAfterHyphen(runes, i) {
			out = append(out, string(runes[start:i+1]))
			start = i + 1
		}
	}
	return append(out, string(runes[start:]))
}

// dashRun counts the hyphens starting at i.
func dashRun(runes []rune, i int) int {
	n := 0
	for i+n < len(runes) && runes[i+n] == '-' {
		n++
	}
	return n
}

// isWordLetter is Python's [^\d\W]: a word character that is not a digit.
func isWordLetter(r rune) bool { return r == '_' || unicode.IsLetter(r) }

// isWordChar is Python's \w.
func isWordChar(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// isWordPunct is textwrap's word_punct, the class an em-dash must follow.
func isWordPunct(r rune) bool {
	return isWordChar(r) || strings.ContainsRune(`!"'&.,?`, r)
}

// splitsAfterHyphen reports whether the single hyphen at i is one a line may
// end after: (?<=LL-|L-L-) at the hyphen, and (?=L-?L) past it.
func splitsAfterHyphen(runes []rune, i int) bool {
	twoLetters := i >= 2 && isWordLetter(runes[i-1]) && isWordLetter(runes[i-2])
	letterHyphenLetter := i >= 3 && isWordLetter(runes[i-1]) &&
		runes[i-2] == '-' && isWordLetter(runes[i-3])
	if !twoLetters && !letterHyphenLetter {
		return false
	}
	j := i + 1
	if j >= len(runes) || !isWordLetter(runes[j]) {
		return false
	}
	j++
	if j < len(runes) && runes[j] == '-' {
		j++
	}
	return j < len(runes) && isWordLetter(runes[j])
}

// wordRe matches what jinja2's wordcount counts: runs of word characters. It
// is not the same as splitting on whitespace -- "[]" has one field and no
// words.
// Go's \w is ASCII-only; Python's is not, so the class is spelled out.
var wordRe = regexp.MustCompile(`[\p{L}\p{N}_]+`)

func filterWordcount(_ *State, v value.Value, _ *value.CallArgs) (value.Value, error) {
	return value.Int(int64(len(wordRe.FindAllString(value.Str(v), -1)))), nil
}

// stripTagsRe matches what jinja2 removes: an HTML comment, or a complete
// tag. An unpaired "<" is left alone, which a depth counter would swallow.
var stripTagsRe = regexp.MustCompile(`(?s)<!--.*?-->|<[^>]*>`)

// filterStriptags removes markup and normalises whitespace, the way jinja2
// does before handing text to something that cannot render HTML.
func filterStriptags(_ *State, v value.Value, _ *value.CallArgs) (value.Value, error) {
	text := unescapeHTML(stripTagsRe.ReplaceAllString(value.Str(v), ""))
	return value.String(strings.Join(strings.Fields(text), " ")), nil
}

var htmlUnescaper = strings.NewReplacer(
	"&lt;", "<", "&gt;", ">", "&#39;", "'", "&#34;", `"`, "&quot;", `"`,
	"&apos;", "'", "&nbsp;", " ", "&amp;", "&",
)

func unescapeHTML(s string) string { return htmlUnescaper.Replace(s) }

// filterFormat is jinja2's `|format`, which is `%` interpolation.
//
// On a Markup receiver that is markupsafe's Markup.__mod__, not str's: it
// escapes every substituted argument and returns Markup. Dropping the safe
// flag and interpolating raw, as this did, put an unescaped argument inside a
// value the template had already been told to trust -- so
// `{{ tmpl|safe|format(comment) }}` emitted the comment's markup verbatim
// where CPython emits it escaped.
func filterFormat(s *State, v value.Value, args *value.CallArgs) (value.Value, error) {
	safe := v.IsSafe()
	format := value.String(value.Str(v))

	var out value.Value
	var err error
	if len(args.Kwargs) > 0 {
		d := value.NewDict()
		dict, _ := d.Dict()
		for _, kw := range args.Kwargs {
			dict.SetString(kw.Name, escapeArg(safe, kw.Value))
		}
		out, err = value.Mod(format, d, s)
	} else {
		pos := make([]value.Value, len(args.Pos))
		for i, arg := range args.Pos {
			pos[i] = escapeArg(safe, arg)
		}
		out, err = value.Mod(format, value.NewTuple(pos...), s)
	}
	if err != nil {
		return value.Undefined, err
	}
	return keepSafe(v, value.Str(out)), nil
}

// escapeArg escapes a value about to be interpolated into Markup. A non-Markup
// format string interpolates its arguments as they are, so nothing is escaped
// there -- the escaping is Markup's doing, not `%`'s.
//
// A number, bool or None is handed over untouched. markupsafe wraps each
// argument in a helper that escapes only when the conversion asks for text, so
// `"%d" % 5` still sees an int; escaping it to the string "5" here would make
// `{{ "%d"|safe|format(5) }}` fail with "a real number is required". Their
// rendered forms contain nothing to escape either way.
func escapeArg(safe bool, v value.Value) value.Value {
	if !safe {
		return v
	}
	switch v.Kind() {
	case value.KindInt, value.KindFloat, value.KindBool, value.KindNone:
		return v
	}
	return escapeIfNeeded(v)
}

// filterPprint renders a value the way Python's pprint.pformat does: repr()
// with dict keys sorted, wrapped across lines once it no longer fits.
func filterPprint(_ *State, v value.Value, _ *value.CallArgs) (value.Value, error) {
	// pformat returns a str even for Markup input -- what it renders is the
	// repr, which for Markup is `Markup('...')`.
	sorted, err := sortDictKeys(v, 0)
	if err != nil {
		return value.Undefined, err
	}
	var b strings.Builder
	if err := pformat(&b, sorted, 0, 0, 0); err != nil {
		return value.Undefined, err
	}
	return value.String(b.String()), nil
}

// maxPPrintDepth bounds how deeply pprint descends, for the same reason
// maxJSONDepth does: the nesting is chosen at render time and the walk would
// otherwise exhaust the stack. CPython's pprint hits its own wall at 326
// levels, three interpreter frames per level, and reports it as a failure to
// take the repr -- which is where it happens.
const maxPPrintDepth = 1000

// RecursionMessageRepr is what CPython reports when it runs out of stack
// taking an object's repr, which is how both pprint and a plain print of a
// deeply nested value fail there.
const RecursionMessageRepr = "maximum recursion depth exceeded while getting the repr of an object"

func tooDeepToPrint() error {
	return errs.New(errs.RecursionError, "%s", RecursionMessageRepr)
}

// pprintWidth is pprint.pformat's default line width.
const pprintWidth = 80

// pformat lays a value out the way pprint does.
//
// The rule is one line if it fits and one element per line if it does not,
// with the continuation indented past the opening bracket. indent is the
// column the value starts at; allowance is the space reserved on the last line
// for whatever closes around it; level counts how deep the dispatch has gone,
// because a long string only gains its wrapping parentheses at the top.
func pformat(b *strings.Builder, v value.Value, indent, allowance, level int) error {
	return pformatSeen(b, v, indent, allowance, level, nil)
}

// pformatSeen carries the containers on the active path.
//
// Repr already collapses a cycle to "{...}", so a small cyclic value never
// reaches the recursive arms below; one whose repr is too wide to print on a
// line does. CPython's pprint marks that case with the container's id, which
// differs between runs there as it does here -- see docs/divergences.md.
func pformatSeen(b *strings.Builder, v value.Value, indent, allowance, level int, seen map[any]bool) error {
	if level > maxPPrintDepth {
		return tooDeepToPrint()
	}
	rep := value.Repr(v)
	if len(rep) <= pprintWidth-indent-allowance {
		b.WriteString(rep)
		return nil
	}

	switch v.Kind() {
	case value.KindList, value.KindTuple, value.KindDict:
		if seen[v.Interface()] {
			fmt.Fprintf(b, "<Recursion on %s with id=%d>",
				v.TypeName(), recursionID(v))
			return nil
		}
		seen = markSeen(seen, v.Interface())
		defer delete(seen, v.Interface())
	}

	switch v.Kind() {
	case value.KindString:
		// pprint dispatches on type(obj).__repr__, and Markup defines its
		// own, so a Markup never reaches the str handler that splits a
		// long string into parenthesised chunks. It is printed by repr on
		// one line however long it is.
		if v.IsSafe() {
			b.WriteString(rep)
			return nil
		}
		pformatString(b, v.AsString(), rep, indent, allowance, level+1)

	case value.KindList, value.KindTuple:
		s, _ := v.Seq()
		open, close := "[", "]"
		if v.Kind() == value.KindTuple {
			open, close = "(", ")"
		}
		b.WriteString(open)
		err := pformatItems(b, s.Items(), indent, allowance+1, func(b *strings.Builder, item value.Value, at, room int) error {
			return pformatSeen(b, item, at, room, level+1, seen)
		})
		if err != nil {
			return err
		}
		if v.Kind() == value.KindTuple && s.Len() == 1 {
			b.WriteString(",")
		}
		b.WriteString(close)

	case value.KindDict:
		d, _ := v.Dict()
		b.WriteString("{")
		err := pformatItems(b, d.Keys(), indent, allowance+1, func(b *strings.Builder, key value.Value, at, room int) error {
			keyRep := value.Repr(key)
			b.WriteString(keyRep)
			b.WriteString(": ")
			val, _, _ := d.Get(key)
			return pformatSeen(b, val, at+len(keyRep)+2, room, level+1, seen)
		})
		if err != nil {
			return err
		}
		b.WriteString("}")

	default:
		b.WriteString(rep)
	}
	return nil
}

// wordChunkRe matches a run of non-space followed by the space after it, which
// is where pprint may break a long string.
var wordChunkRe = regexp.MustCompile(`\S*\s*`)

// pformatString breaks a string that does not fit into one repr per line,
// wrapping the whole in parentheses when it is the outermost value.
func pformatString(b *strings.Builder, text, rep string, indent, allowance, level int) {
	if text == "" {
		b.WriteString(rep)
		return
	}
	if level == 1 {
		indent++
		allowance++
	}

	maxWidth := pprintWidth - indent
	var chunks []string
	lines := splitLinesKeepingEnds(text)
	for i, line := range lines {
		lineRep := value.Repr(value.String(line))
		limit := maxWidth
		if i == len(lines)-1 {
			limit -= allowance
		}
		if len(lineRep) <= limit {
			chunks = append(chunks, lineRep)
			continue
		}
		// Break the line between words, keeping each piece's repr
		// inside the width.
		parts := wordChunkRe.FindAllString(line, -1)
		if n := len(parts); n > 0 && parts[n-1] == "" {
			parts = parts[:n-1]
		}
		current := ""
		for j, part := range parts {
			candidate := current + part
			limit := maxWidth
			if j == len(parts)-1 && i == len(lines)-1 {
				limit -= allowance
			}
			if len(value.Repr(value.String(candidate))) > limit {
				if current != "" {
					chunks = append(chunks, value.Repr(value.String(current)))
				}
				current = part
				continue
			}
			current = candidate
		}
		if current != "" {
			chunks = append(chunks, value.Repr(value.String(current)))
		}
	}

	if len(chunks) == 1 {
		b.WriteString(chunks[0])
		return
	}
	if level == 1 {
		b.WriteString("(")
	}
	for i, chunk := range chunks {
		if i > 0 {
			b.WriteString("\n" + pprintIndent(indent))
		}
		b.WriteString(chunk)
	}
	if level == 1 {
		b.WriteString(")")
	}
}

// pprintIndent is the leading space for one pprint line.
//
// The indent grows with the depth of the value being printed, and that depth
// is the caller's -- a deeply nested structure handed in from Go would
// otherwise size an allocation per line from it. Indenting past the line width
// carries no information, so it is capped there.
func pprintIndent(n int) string {
	if n <= 0 {
		return ""
	}
	if n > pprintWidth {
		n = pprintWidth
	}
	return strings.Repeat(" ", n)
}

// splitLinesKeepingEnds is Python's str.splitlines(True).
func splitLinesKeepingEnds(s string) []string {
	var out []string
	for len(s) > 0 {
		i := strings.IndexAny(s, "\n\r")
		if i < 0 {
			out = append(out, s)
			break
		}
		end := i + 1
		if s[i] == '\r' && end < len(s) && s[end] == '\n' {
			end++
		}
		out = append(out, s[:end])
		s = s[end:]
	}
	return out
}

// pformatItems writes a sequence of entries one per line, indented one column
// past the bracket that opened them.
func pformatItems[T any](b *strings.Builder, items []T, indent, allowance int,
	write func(*strings.Builder, T, int, int) error,
) error {
	inner := indent + 1
	separator := ",\n" + pprintIndent(inner)
	for i, item := range items {
		if i > 0 {
			b.WriteString(separator)
		}
		room := 1
		if i == len(items)-1 {
			room = allowance
		}
		if err := write(b, item, inner, room); err != nil {
			return err
		}
	}
	return nil
}

// sortDictKeys rebuilds a value with every dict in key order.
//
// seen carries the containers on the active path. A value graph can contain a
// cycle, and rebuilding one without noticing runs until memory is gone; a
// container already being rebuilt is left as it is, which is enough for
// pformat to reach it and print its recursion marker.
func sortDictKeys(v value.Value, level int) (value.Value, error) {
	return sortDictKeysSeen(v, nil, level)
}

func sortDictKeysSeen(v value.Value, seen map[any]bool, level int) (value.Value, error) {
	if level > maxPPrintDepth {
		return value.Undefined, tooDeepToPrint()
	}
	switch v.Kind() {
	case value.KindDict:
		if seen[v.Interface()] {
			return v, nil
		}
		seen = markSeen(seen, v.Interface())
		defer delete(seen, v.Interface())
		d, _ := v.Dict()
		entries := append([]value.DictEntry(nil), d.Entries()...)
		sort.SliceStable(entries, func(i, j int) bool {
			return value.Str(entries[i].Key) < value.Str(entries[j].Key)
		})
		out := value.NewDict()
		target, _ := out.Dict()
		for _, e := range entries {
			sorted, err := sortDictKeysSeen(e.Value, seen, level+1)
			if err != nil {
				return value.Undefined, err
			}
			_ = target.Set(e.Key, sorted)
		}
		return out, nil
	case value.KindList, value.KindTuple:
		if seen[v.Interface()] {
			return v, nil
		}
		seen = markSeen(seen, v.Interface())
		defer delete(seen, v.Interface())
		seq, _ := v.Seq()
		items := make([]value.Value, seq.Len())
		for i, item := range seq.Items() {
			sorted, err := sortDictKeysSeen(item, seen, level+1)
			if err != nil {
				return value.Undefined, err
			}
			items[i] = sorted
		}
		if v.Kind() == value.KindTuple {
			return value.NewTuple(items...), nil
		}
		return value.NewList(items...), nil
	}
	return v, nil
}

// recursionID is the identity CPython's pprint prints for a repeated
// container. Python uses id(), which is the object's address; so is this.
func recursionID(v value.Value) uintptr {
	return reflect.ValueOf(v.Interface()).Pointer()
}

func markSeen(seen map[any]bool, key any) map[any]bool {
	if seen == nil {
		seen = make(map[any]bool, 4)
	}
	seen[key] = true
	return seen
}

// --- escaping filters --------------------------------------------------------

func filterSafe(_ *State, v value.Value, _ *value.CallArgs) (value.Value, error) {
	return value.Safe(value.Str(v)), nil
}

func filterEscape(_ *State, v value.Value, _ *value.CallArgs) (value.Value, error) {
	return escapeIfNeeded(v), nil
}

// filterForceEscape escapes even an already-safe value, which is how a
// template un-trusts something it was handed as Markup.
func filterForceEscape(_ *State, v value.Value, _ *value.CallArgs) (value.Value, error) {
	return value.Safe(escapeHTML(value.Str(v))), nil
}

// --- number filters ----------------------------------------------------------

func filterAbs(_ *State, v value.Value, _ *value.CallArgs) (value.Value, error) {
	switch {
	case v.Kind() == value.KindFloat:
		return value.Float(math.Abs(v.AsFloat())), nil
	case v.IsInteger():
		b, _ := v.BigInt()
		return value.BigInt(new(big.Int).Abs(b)), nil
	}
	return value.Undefined, errs.New(errs.TypeError,
		"bad operand type for abs(): '%s'", v.TypeName())
}

func filterInt(_ *State, v value.Value, args *value.CallArgs) (value.Value, error) {
	def, hasDef := arg(args, 0, "default")
	if !hasDef {
		def = value.Int(0)
	}
	base, err := intArg(args, 1, "base", 10)
	if err != nil {
		return value.Undefined, err
	}

	switch {
	case v.IsInteger():
		if v.Kind() == value.KindBool {
			n, _ := v.Int64()
			return value.Int(n), nil
		}
		return v, nil
	case v.Kind() == value.KindFloat:
		f := v.AsFloat()
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return def, nil
		}
		t := math.Trunc(f)
		if n, ok := value.FloatToInt64(t); ok {
			return value.Int(n), nil
		}
		b, _ := big.NewFloat(t).Int(nil)
		return value.BigInt(b), nil
	case v.IsString():
		text := strings.TrimSpace(v.AsString())
		if base != 10 {
			text = strings.TrimPrefix(strings.TrimPrefix(text, "0"), map[int]string{
				2: "b", 8: "o", 16: "x",
			}[base])
		}
		// Python accepts base 0 or 2..36 and raises ValueError otherwise;
		// jinja2's filter catches that and falls through to the float
		// path, so `"10"|int(0, 99999)` is 10. big.Int.SetString panics
		// on a base outside its own range rather than reporting it, so
		// the check has to happen here.
		if validIntBase(base) {
			if n, ok := new(big.Int).SetString(text, base); ok {
				return value.BigInt(n), nil
			}
		}
		// jinja2 accepts "3.5" here by falling back to float then int.
		if f, err := strconv.ParseFloat(text, 64); err == nil {
			return value.Int(int64(math.Trunc(f))), nil
		}
	}
	return def, nil
}

// validIntBase reports whether Python's int() would accept this base.
func validIntBase(base int) bool { return base == 0 || (base >= 2 && base <= 36) }

func filterFloat(_ *State, v value.Value, args *value.CallArgs) (value.Value, error) {
	def, hasDef := arg(args, 0, "default")
	if !hasDef {
		def = value.Float(0)
	}
	if f, ok := v.Float64(); ok {
		return value.Float(f), nil
	}
	if v.IsString() {
		if f, err := strconv.ParseFloat(strings.TrimSpace(v.AsString()), 64); err == nil {
			return value.Float(f), nil
		}
	}
	return def, nil
}

func filterRound(s *State, v value.Value, args *value.CallArgs) (value.Value, error) {
	precision, err := intArg(args, 0, "precision", 0)
	if err != nil {
		return value.Undefined, err
	}
	method := "common"
	if m, ok := arg(args, 1, "method"); ok {
		method = value.Str(m)
	}
	if method != "common" && method != "ceil" && method != "floor" {
		return value.Undefined, errs.New(errs.FilterArgumentError,
			"method must be common, ceil or floor")
	}

	// The two methods fail differently, because jinja2 implements them
	// differently: "common" calls round(), so a value with no __round__ is
	// a TypeError, while ceil and floor multiply by a power of ten first,
	// so an undefined raises its own error before any rounding happens.
	if method != "common" {
		// jinja2 writes `func(value * (10 ** precision)) / (10 ** precision)`,
		// and 10**precision is an *integer* for a non-negative precision.
		// That matters: `[a, b] * 1` is a list, which math.ceil then
		// rejects as "must be real number, not list" rather than the
		// multiplication failing first.
		scale, err := value.Pow(value.Int(10), value.Int(int64(precision)))
		if err != nil {
			return value.Undefined, err
		}
		scaled, err := value.Mul(v, scale, s)
		if err != nil {
			return value.Undefined, err
		}
		f, ok := scaled.Float64()
		if !ok {
			return value.Undefined, errs.New(errs.TypeError,
				"must be real number, not %s", scaled.TypeName())
		}
		divisor, _ := scale.Float64()
		rounded := math.Floor(f)
		if method == "ceil" {
			rounded = math.Ceil(f)
		}
		// math.ceil and math.floor return a Python *int*, which has no
		// signed zero, so dividing it yields +0.0. Go's return a float
		// and keep the sign, which made `-0.0|round(1, "floor")` render
		// "-0.0" where jinja2 renders "0.0". Assigning the literal
		// normalises -0.0 to +0.0 and leaves every other value alone.
		if rounded == 0 {
			rounded = 0
		}
		return value.Float(rounded / divisor), nil
	}

	// Python's round() preserves the numeric type: round(5, 2) is the int
	// 5, while round(2.5, 0) is the float 2.0.
	if v.IsInteger() {
		if v.Kind() == value.KindBool {
			n, _ := v.Int64()
			return value.Int(n), nil
		}
		if precision >= 0 {
			return v, nil
		}
	}

	f, ok := v.Float64()
	if !ok {
		return value.Undefined, errs.New(errs.TypeError,
			"type %s doesn't define __round__ method", v.TypeName())
	}
	// Python rounds the decimal value, not the value scaled by a power of
	// ten: 2.675 is really 2.67499..., so round(2.675, 2) is 2.67, while
	// 2.675*100 rounds up to 267.5 and would give 2.68. Formatting to the
	// requested precision rounds correctly against the true value, ties to
	// even included.
	if precision >= 0 {
		// precision is the number of digits FormatFloat is about to
		// write, so it sizes the allocation directly: round(2000000000)
		// formats a two-billion-digit decimal. A float64 carries no
		// information past ~17 significant digits, so anything past the
		// charge is padding zeroes -- but they still have to be paid for
		// before they are written.
		if err := s.ChargeBytes(int64(precision)); err != nil {
			return value.Undefined, err
		}
		rounded, err := strconv.ParseFloat(strconv.FormatFloat(f, 'f', precision, 64), 64)
		if err != nil {
			return value.Undefined, err
		}
		return value.Float(rounded), nil
	}
	scale := math.Pow(10, float64(precision))
	return value.Float(math.RoundToEven(f*scale) / scale), nil
}

func filterSum(s *State, v value.Value, args *value.CallArgs) (value.Value, error) {
	items, err := materialize(s, v)
	if err != nil {
		return value.Undefined, err
	}
	attribute, _ := arg(args, 0, "attribute")
	total, hasStart := arg(args, 1, "start")
	if !hasStart {
		total = value.Int(0)
	}
	// Python's sum refuses a str start outright, before it looks at the
	// sequence -- adding strings one at a time is quadratic, and it points
	// at join instead. Concatenating them silently was the wrong answer to
	// a question CPython declines to answer at all.
	if total.Kind() == value.KindString {
		return value.Undefined, errs.New(errs.TypeError,
			"sum() can't sum strings [use ''.join(seq) instead]")
	}
	if total.Kind() == value.KindBytes {
		return value.Undefined, errs.New(errs.TypeError,
			"sum() can't sum bytes [use b''.join(seq) instead]")
	}
	for _, item := range items {
		if !attribute.IsUndefined() && !attribute.IsNone() {
			item, err = attrPath(s, item, value.Str(attribute))
			if err != nil {
				return value.Undefined, err
			}
		}
		total, err = value.Add(total, item)
		if err != nil {
			return value.Undefined, err
		}
	}
	return total, nil
}

func filterFilesizeformat(_ *State, v value.Value, args *value.CallArgs) (value.Value, error) {
	binary, err := boolArg(args, 0, "binary", false)
	if err != nil {
		return value.Undefined, err
	}
	bytes, ok := v.Float64()
	if !ok {
		// jinja2 calls float(value), so the failure is float()'s.
		if !v.IsString() {
			return value.Undefined, errs.New(errs.TypeError,
				"float() argument must be a string or a real number, not '%s'",
				v.TypeName())
		}
		f, convErr := strconv.ParseFloat(strings.TrimSpace(v.AsString()), 64)
		if convErr != nil {
			return value.Undefined, errs.New(errs.ValueError,
				"could not convert string to float: %s", value.Repr(v))
		}
		bytes = f
	}

	base := 1000.0
	prefixes := []string{"kB", "MB", "GB", "TB", "PB", "EB", "ZB", "YB"}
	if binary {
		base = 1024.0
		prefixes = []string{"KiB", "MiB", "GiB", "TiB", "PiB", "EiB", "ZiB", "YiB"}
	}

	if bytes == 1 {
		return value.String("1 Byte"), nil
	}
	if bytes < base {
		// jinja2 writes int(bytes) here, which truncates: 1.5 bytes is
		// "1 Bytes", not the "2 Bytes" a rounding format would give.
		return value.String(fmt.Sprintf("%d Bytes", int64(bytes))), nil
	}
	for i, prefix := range prefixes {
		unit := math.Pow(base, float64(i+2))
		if bytes < unit || i == len(prefixes)-1 {
			return value.String(fmt.Sprintf("%.1f %s", base*bytes/unit, prefix)), nil
		}
	}
	return value.String(fmt.Sprintf("%.1f %s", bytes, prefixes[len(prefixes)-1])), nil
}

// --- misc --------------------------------------------------------------------

// filterDefault substitutes for an undefined value, or for any falsey value
// when its boolean argument is set.
func filterDefault(_ *State, v value.Value, args *value.CallArgs) (value.Value, error) {
	fallback, ok := arg(args, 0, "default_value")
	if !ok {
		fallback = value.String("")
	}
	asBool, err := boolArg(args, 1, "boolean", false)
	if err != nil {
		return value.Undefined, err
	}
	if asBool {
		truth, err := value.IsTrue(v)
		if err != nil {
			return value.Undefined, err
		}
		if !truth {
			return fallback, nil
		}
		return v, nil
	}
	if v.IsUndefined() {
		return fallback, nil
	}
	return v, nil
}

// filterAttr fetches an attribute without the item-lookup fallback `.` has, so
// `d|attr("items")` is the method and `d["items"]` would be the entry.
func filterAttr(s *State, v value.Value, args *value.CallArgs) (value.Value, error) {
	name, ok := arg(args, 0, "name")
	if !ok {
		return value.Undefined, errs.New(errs.FilterArgumentError,
			"attr() missing required argument 'name'")
	}
	attrName := value.Str(name)
	if attr, ok := lookupAttr(s, v, attrName); ok {
		return attr, nil
	}
	// getattr() on an Undefined raises -- except for a dunder name, which
	// Undefined.__getattr__ reports as an ordinary missing attribute. That
	// is why `nope|attr("items")` fails immediately while
	// `nope|attr("__subclasses__")` yields an undefined that only fails
	// when it is used.
	if v.IsUndefined() && !strings.HasPrefix(attrName, "__") {
		return value.Undefined, v.UndefinedError()
	}
	return s.Undefined(value.UndefinedAttr(v, attrName)), nil
}

// wordBeginnings splits on the runs jinja2 treats as starting a new word:
// hyphens, whitespace, and the opening brackets.
func isWordBreak(r rune) bool {
	switch r {
	case '-', ' ', '\t', '\n', '\r', '\v', '\f', '(', '{', '[', '<':
		return true
	}
	return false
}

// jinjaTitle is jinja2's do_title, which is not str.title().
//
// It splits on runs of hyphen, whitespace and opening brackets, then
// uppercases the first character of each remaining chunk and lowercases the
// rest. An apostrophe does not start a word, so "foo's bar" becomes
// "Foo's Bar" where str.title() would give "Foo'S Bar".
func jinjaTitle(s string) string {
	var b strings.Builder
	runes := []rune(s)
	i := 0
	for i < len(runes) {
		if isWordBreak(runes[i]) {
			for i < len(runes) && isWordBreak(runes[i]) {
				b.WriteRune(runes[i])
				i++
			}
			continue
		}
		start := i
		for i < len(runes) && !isWordBreak(runes[i]) {
			i++
		}
		word := runes[start:i]
		b.WriteRune(unicode.ToUpper(word[0]))
		for _, r := range word[1:] {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

// augmentedAssign rewords a `+` failure as the `+=` jinja2 actually performed.
func augmentedAssign(err error) error {
	var e *errs.Error
	if errors.As(err, &e) && strings.HasPrefix(e.Msg, "unsupported operand type(s) for +:") {
		e.Msg = strings.Replace(e.Msg, "for +:", "for +=:", 1)
	}
	return err
}

// sliceValue takes the first n elements of a sequence value.
func sliceValue(v value.Value, n int) (value.Value, error) {
	seq, ok := v.Seq()
	if !ok {
		return v, nil
	}
	begin, stride, count, err := value.SliceSpan(seq.Len(), nil, &n, nil)
	if err != nil {
		return value.Undefined, err
	}
	items := make([]value.Value, count)
	for i := range count {
		items[i] = seq.At(begin + i*stride)
	}
	if v.Kind() == value.KindTuple {
		return value.NewTuple(items...), nil
	}
	return value.NewList(items...), nil
}
