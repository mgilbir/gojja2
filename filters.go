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
	add("upper", stringFilter(pyUpperString))
	add("lower", stringFilter(pyLowerString))
	// title is the one case filter that does not preserve Markup: jinja2
	// assembles it with "".join(...), and joining on a plain str gives a
	// plain str.
	add("title", func(_ *State, v value.Value, _ *value.CallArgs) (value.Value, error) {
		return value.String(jinjaTitle(value.Str(v))), nil
	})
	add("capitalize", stringFilter(pyCapitalizeString))
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

// attrParts is jinja2's _prepare_attribute_parts: what a filter's
// `attribute=` means as a sequence of lookups.
//
// None is no lookup at all, so `|groupby(attribute=none)` groups by the item
// itself. A string is dotted ("user.name") and a run of digits in it is an
// index ("0.name") -- by Python's str.isdigit, so "-1" stays a name. Anything
// else is one lookup with that value as the key, which is how
// `|max(attribute=false)` reads element 0 and `|max(attribute=2.5)` reports
// "no element 2.5" rather than looking for an attribute called "2.5".
func attrParts(attribute value.Value) []value.Value {
	if attribute.IsNone() || attribute.IsUndefined() {
		return nil
	}
	if attribute.Kind() != value.KindString {
		return []value.Value{attribute}
	}
	fields := strings.Split(attribute.AsString(), ".")
	parts := make([]value.Value, 0, len(fields))
	for _, part := range fields {
		if n, ok := pyDigitsToInt(part); ok {
			parts = append(parts, value.Int(n))
			continue
		}
		parts = append(parts, value.String(part))
	}
	return parts
}

// pyDigitsToInt reads a part the way `int(x) if x.isdigit() else x` does.
//
// str.isdigit accepts no sign and no spaces, so "-1" and " 1" are names; it
// does accept other scripts' digits, and int() reads those, so "١" is 1.
func pyDigitsToInt(part string) (int64, bool) {
	if part == "" {
		return 0, false
	}
	var n int64
	for _, r := range part {
		d := pyDigitValue(r)
		if d < 0 {
			return 0, false
		}
		// An attribute specification long enough to overflow is not
		// an index anyone meant; Python would build the integer, and
		// the lookup would miss either way.
		if n > (math.MaxInt64-int64(d))/10 {
			return 0, false
		}
		n = n*10 + int64(d)
	}
	return n, true
}

// pyDigitValue is the decimal value of a rune str.isdigit accepts, or -1.
//
// Python's isdigit is wider than a decimal digit: it also takes the ones with
// a Numeric_Type of Digit, such as the superscripts -- but int() rejects those,
// so a part containing one is left as a name rather than raising the way
// jinja2 does.
func pyDigitValue(r rune) int {
	if !unicode.IsDigit(r) {
		return -1
	}
	return int(r - runeZero(r))
}

// runeZero is the zero of the decimal-digit block r belongs to.
func runeZero(r rune) rune {
	for z := r; ; z-- {
		if !unicode.IsDigit(z) {
			return z + 1
		}
	}
}

// trimSpec drops the spaces around a single attribute specification, which
// only a string one has.
func trimSpec(attribute value.Value) value.Value {
	if attribute.Kind() != value.KindString {
		return attribute
	}
	return value.String(strings.TrimSpace(attribute.AsString()))
}

// attrPath resolves a jinja2 attribute specification against one item.
//
// Every part goes through Environment.getitem, the way make_attrgetter does,
// so the item wins over the attribute of the same name and a lookup that
// misses answers undefined instead of raising.
func attrPath(s *State, v value.Value, parts []value.Value) (value.Value, error) {
	for _, part := range parts {
		// No early exit for an undefined receiver: the next lookup has
		// to raise, which is what makes a second |map(attribute=...)
		// over the results of a first one fail.
		if v.IsUndefined() {
			if v.UndefinedBehavior() == value.UndefinedChainable {
				continue
			}
			return value.Undefined, v.UndefinedError()
		}
		v = envGetItem(s, v, part)
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
			// Markup.lower() is Markup: markupsafe overrides the
			// case methods, and escaped text cannot be unescaped
			// by changing its case. Dropping that here made a
			// comparison error inside a sort name 'str' where
			// CPython names 'Markup'.
			return keepSafe(v, pyLowerString(v.AsString()))
		}
		return v
	}
	if attribute.IsUndefined() || attribute.IsNone() {
		return func(v value.Value) (value.Value, error) { return fold(v), nil }
	}
	parts := attrParts(trimSpec(attribute))
	return func(v value.Value) (value.Value, error) {
		k, err := attrPath(s, v, parts)
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
			// Markup.lower() is Markup: markupsafe overrides the
			// case methods, and escaped text cannot be unescaped
			// by changing its case. Dropping that here made a
			// comparison error inside a sort name 'str' where
			// CPython names 'Markup'.
			return keepSafe(v, pyLowerString(v.AsString()))
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
	// A comma-separated specification sorts by several keys in turn --
	// but only a string one: make_multi_attrgetter splits nothing else.
	specs := []value.Value{attribute}
	if attribute.Kind() == value.KindString {
		specs = nil
		for _, field := range strings.Split(attribute.AsString(), ",") {
			specs = append(specs, value.String(strings.TrimSpace(field)))
		}
	}
	paths := make([][]value.Value, len(specs))
	for i, spec := range specs {
		paths[i] = attrParts(spec)
	}
	return func(v value.Value) (value.Value, error) {
		keys := make([]value.Value, len(paths))
		for i, p := range paths {
			k, err := attrPath(s, v, p)
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
	count, err := intArg(args, 2, "count", -1, cSSizeT)
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
	// str.center's width has no None to fall back on: 80 is the filter's
	// default for an argument that was not written, and an explicit None
	// reaches str.center and is refused.
	width, err := indexArg(args, 0, "width", 80, cSSizeT)
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
		if _, err := value.Add(v, value.String("\n"), s); err != nil {
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
	// The length and the leeway are carried as values rather than converted
	// here, because jinja2 never converts them. It asserts `length >=
	// len(end)` and `leeway >= 0` -- Python's own comparison, which numbers
	// pass and everything else refuses by naming the operator -- and only
	// slices by the length once the text is long enough to cut. So a float
	// compares happily and fails at the slice, a string never gets that
	// far, and a value short enough to keep is returned without the length
	// being looked at as a number at all.
	length := value.Int(255)
	if l, ok := arg(args, 0, "length"); ok {
		length = l
	}
	killwords, err := boolArg(args, 1, "killwords", false)
	if err != nil {
		return value.Undefined, err
	}
	// The end is carried as a value too. jinja2 concatenates it rather than
	// formatting it, so a list end is a TypeError on a string input where
	// stringifying it would have appended "['z']" and said nothing.
	end, endLen := value.String("..."), 3
	if e, ok := arg(args, 2, "end"); ok {
		// jinja2 asserts `length >= len(end)`, so an end with no length
		// -- a number, say -- fails as len() does rather than being
		// stringified. And the comparison counts characters, which for
		// a non-ASCII end is not the same as counting bytes.
		n, err := value.Len(e)
		if err != nil {
			return value.Undefined, err
		}
		end, endLen = e, n
	}
	// leeway is the one argument jinja2 does read None for: its default is
	// None and it stands in the policy, so `truncate(10, false, "...",
	// none)` is the policy's leeway where `truncate(none)` is an error.
	leeway := value.Int(int64(s.env.policies.TruncateLeeway))
	if l, ok := arg(args, 3, "leeway"); ok && !l.IsNone() {
		leeway = l
	}

	// Both of these are bare asserts in jinja2, so the class is
	// AssertionError rather than the ValueError the wording suggests, and
	// the value is reported as Python prints it -- `truncate(true)` says
	// "got True", not "got 1".
	if ok, err := value.Ordered(">=", length, value.Int(int64(endLen))); err != nil {
		return value.Undefined, err
	} else if !ok {
		return value.Undefined, errs.New(errs.AssertionError,
			"expected length >= %d, got %s", endLen, value.Str(length))
	}
	if ok, err := value.Ordered(">=", leeway, value.Int(0)); err != nil {
		return value.Undefined, err
	} else if !ok {
		return value.Undefined, errs.New(errs.AssertionError,
			"expected leeway >= 0, got %s", value.Str(leeway))
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
	room, err := value.Add(length, leeway, s)
	if err != nil {
		return value.Undefined, err
	}
	if fits, err := value.Ordered("<=", value.Int(int64(size)), room); err != nil {
		return value.Undefined, err
	} else if fits {
		return v, nil
	}

	// Only now is the length an index, and only now does a float refuse.
	cut, err := truncPoint(length, endLen)
	if err != nil {
		return value.Undefined, err
	}

	text := value.Str(v)
	// Past the length check jinja2 slices the value and then, unless
	// killwords, calls rsplit on it -- so a non-string gets this far and
	// fails on one of those rather than on being the wrong kind of input.
	if !v.IsString() {
		if killwords {
			sliced, err := sliceValue(v, cut)
			if err != nil {
				return value.Undefined, err
			}
			// A list end really does append to a list input, so
			// this is a result and not only a way to fail.
			return value.Add(sliced, end, s)
		}
		return value.Undefined, errs.New(errs.AttributeError,
			"'%s' object has no attribute 'rsplit'", v.TypeName())
	}

	head, _ := value.StrSlice(text, nil, ptr(cut), nil)
	if !killwords {
		if i := strings.LastIndexByte(head, ' '); i >= 0 {
			head = head[:i]
		}
	}
	if !end.IsString() {
		// str + non-str, which is where jinja2 fails.
		return value.Add(value.String(head), end, s)
	}
	return keepSafe(v, head+value.Str(end)), nil
}

// truncPoint is `length - len(end)`, the index jinja2 cuts at. Working it out
// here is what refuses a float: the length is only ever an index at the slice,
// so `truncate(3.0)` compares its way past both assertions and fails on the
// cut, which is where Python fails too.
func truncPoint(length value.Value, endLen int) (int, error) {
	n, ok := length.Int64()
	if !ok {
		return 0, errs.New(errs.TypeError,
			"slice indices must be integers or None or have an __index__ method")
	}
	return int(n) - endLen, nil
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
	//
	// A bytes is the exception: it *has* splitlines, so it gets past the
	// attribute and into textwrap, whose pattern is a str one and refuses
	// a bytes-like object. An empty bytes splits to no lines at all, so
	// nothing is ever handed to textwrap and the filter answers "" --
	// which is why the emptiness is checked rather than assumed.
	if !v.IsString() {
		if v.Kind() != value.KindBytes {
			return value.Undefined, errs.New(errs.AttributeError,
				"'%s' object has no attribute 'splitlines'", v.TypeName())
		}
		if len(splitLines(v.AsString(), false)) == 0 {
			return value.String(""), nil
		}
		return value.Undefined, errs.New(errs.TypeError,
			"cannot use a string pattern on a bytes-like object")
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
//
// The order is markupsafe's and it is load-bearing: tags go, then the spaces
// collapse, and only then are the character references resolved. Resolving
// first would let a reference standing for a space -- `&nbsp;`, `&#32;` -- be
// collapsed away as though the author had typed one.
func filterStriptags(_ *State, v value.Value, _ *value.CallArgs) (value.Value, error) {
	text := stripTagsRe.ReplaceAllString(value.Str(v), "")
	return value.String(unescapeHTML(strings.Join(strings.Fields(text), " "))), nil
}

// charrefRe is Python's _charref, which decides how much of the text after an
// "&" a reference may claim: digits for a decimal one, hex digits after "&#x",
// and otherwise up to 32 characters that are none of tab, newline, form feed,
// space, "<", "&", "#" or ";". The trailing ";" is optional, which is what
// lets `&amp` resolve.
var charrefRe = regexp.MustCompile("&(#[0-9]+;?|#[xX][0-9a-fA-F]+;?|[^\t\n\f <&#;]{1,32};?)")

// unescapeHTML is Python's html.unescape, which is what markupsafe's unescape
// is, and therefore what |striptags ends in.
//
// It used to be a replacer over eight entities. That left `&AMP;` -- the
// uppercase spelling the standard also defines, and what `{{ html|upper }}`
// produces -- and the other 2,223 named references sitting in the output.
func unescapeHTML(s string) string {
	if !strings.Contains(s, "&") {
		return s
	}
	return charrefRe.ReplaceAllStringFunc(s, func(match string) string {
		return resolveCharref(match[1:])
	})
}

// resolveCharref resolves one reference, given the text after the "&".
func resolveCharref(ref string) string {
	if strings.HasPrefix(ref, "#") {
		digits, base := strings.TrimSuffix(ref[1:], ";"), 10
		if digits != "" && (digits[0] == 'x' || digits[0] == 'X') {
			digits, base = digits[1:], 16
		}
		// The pattern admits any number of digits, so a long one
		// overflows; anything that does is far past the last code
		// point, which the standard answers with the replacement
		// character like any other value out of range.
		num, err := strconv.ParseInt(digits, base, 32)
		if err != nil {
			return "\uFFFD"
		}
		if text, ok := invalidCharrefs[int(num)]; ok {
			return text
		}
		if (num >= 0xD800 && num <= 0xDFFF) || num > 0x10FFFF {
			return "\uFFFD"
		}
		if invalidCodepoints[int(num)] {
			return ""
		}
		return string(rune(num))
	}
	if text, ok := htmlEntities[ref]; ok {
		return text
	}
	// A named reference may be missing its semicolon and run into the text
	// after it, so the longest prefix that is a name wins and the rest
	// stays where it was: `&notit` is "\u00acit".
	for i := len(ref) - 1; i > 1; i-- {
		if text, ok := htmlEntities[ref[:i]]; ok {
			return text + ref[i:]
		}
	}
	return "&" + ref
}

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

// pyObjectRepr is Python's repr for an object whose type defines none:
// `<module.Qualname object at 0xADDR>`. jinja2's Cycler, Joiner and
// BlockReference all fall back to it, so a template that prints one sees this
// rather than a name gojja2 invented -- and a template that prints
// `{{ self.body }}` sees it instead of the block, which is the difference that
// mattered.
//
// The address is this object's, as reproducible as CPython's own: the same
// bargain |pprint already strikes for a container that contains itself.
func pyObjectRepr(qualified string, obj any) string {
	return fmt.Sprintf("<%s object at 0x%x>", qualified, reflect.ValueOf(obj).Pointer())
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
	// Markup(x) asks x for its own escaped form when it has one, which is
	// how `{{ module|safe }}` is the module's body and not its repr.
	if html, ok := value.HTML(v); ok {
		return value.Safe(html), nil
	}
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
	// do_int wraps the whole conversion in `except (TypeError, ValueError)`,
	// so a base that is not usable as one is *swallowed*: `int("10", 1.5)`
	// raises TypeError inside, is caught, and the filter falls through to
	// int(float(value)) and then to the default. Refusing it here reported
	// an error CPython never lets out -- and only for a str value at that,
	// since nothing else passes the base on.
	base, baseOK := 10, true
	if b, ok := arg(args, 1, "base"); ok {
		n, whole := b.Int64()
		base, baseOK = int(n), whole
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
		if math.IsNaN(f) {
			// int(nan) is a ValueError, which do_int catches
			// twice over and answers with the default.
			return def, nil
		}
		if math.IsInf(f, 0) {
			// int(inf) is an OverflowError, and do_int's first
			// attempt catches only TypeError and ValueError -- so
			// this one is the filter's answer rather than a reason
			// to fall back on the default.
			return value.Undefined, overflowToInt(f)
		}
		return intFromFloat(f), nil
	case isNumericText(v):
		raw, _ := numericText(v)
		text := strings.TrimSpace(raw)
		// Python accepts base 0 or 2..36 and raises ValueError otherwise;
		// jinja2's filter catches that and falls through to the float
		// path, so `"10"|int(0, 99999)` is 10.
		// The base reaches the conversion only for a str. do_int tests
		// `isinstance(value, str)` before passing it, so a bytes goes
		// to the bare int(value) -- base ten, whatever was asked for --
		// and `{{ "ff".encode()|int(0, 16) }}` is the default and not
		// 255.
		useBase := base
		if !v.IsString() {
			useBase = 10
		}
		if baseOK || !v.IsString() {
			if n, ok := pyParseInt(raw, useBase); ok {
				return value.BigInt(n), nil
			}
		}
		// jinja2 accepts "3.5" here by falling back to float then int,
		// and int() of a float is exact however large it is -- which a
		// raw int64 conversion is not: "9.223372036854776e+18"|int
		// came out as the most negative int64 rather than 2**63.
		if f, ok := value.ParseFloat(text); ok {
			if math.IsNaN(f) || math.IsInf(f, 0) {
				// The second attempt is int(float(value)), and
				// that one *does* catch OverflowError -- so
				// "inf"|int is the default and not an error,
				// where a float inf would have raised.
				return def, nil
			}
			return intFromFloat(f), nil
		}
	}
	return def, nil
}

// pyParseInt is Python's int(str, base), which big.Int.SetString is not.
//
// The old reading stripped "0"+"x" off the front and handed the rest to
// SetString, which got the easy case right and little else: the prefix match
// was case-sensitive so "0X1F" failed, a sign put the prefix out of reach so
// "-0x10" failed, base 0 was never detected, and underscores -- which Python
// allows between digits -- only worked in the bases SetString itself accepts.
//
// Failure is not an error here: do_int catches it and falls back to
// int(float(value)), which is why `"010"|int(0, 0)` is 10 even though Python
// refuses that string with base 0.
func pyParseInt(text string, base int) (*big.Int, bool) {
	s := strings.TrimSpace(text)
	neg := false
	if s != "" && (s[0] == '+' || s[0] == '-') {
		neg, s = s[0] == '-', s[1:]
	}
	// Python takes exactly one sign. big.Int.SetString reads one of its
	// own, so without this "--4" came out as 4 and "-+4" as -4, where
	// int("--4") is a ValueError -- and `|int` answers its default rather
	// than raising, so a template got a plausible number and no signal.
	if s != "" && (s[0] == '+' || s[0] == '-') {
		return nil, false
	}
	// A prefix selects the base when none was given, and is allowed -- but
	// not required -- when it matches the one that was. It is matched
	// case-insensitively, so 0X and 0x are the same.
	prefixed := false
	if len(s) >= 2 && s[0] == '0' {
		want := 0
		switch s[1] {
		case 'x', 'X':
			want = 16
		case 'o', 'O':
			want = 8
		case 'b', 'B':
			want = 2
		}
		if want != 0 && (base == 0 || base == want) {
			base, s, prefixed = want, s[2:], true
		}
	}
	if base == 0 {
		// No prefix and no base: decimal, and Python refuses a leading
		// zero unless the whole thing is zeros.
		base = 10
		if len(s) > 1 && s[0] == '0' && strings.Trim(s, "0_") != "" {
			return nil, false
		}
	}
	if !validIntBase(base) {
		return nil, false
	}
	// An underscore separates digits: never doubled, never trailing, and
	// never leading unless a prefix just ended -- "0x_1f" is 31.
	if strings.Contains(s, "__") || strings.HasSuffix(s, "_") ||
		(!prefixed && strings.HasPrefix(s, "_")) {
		return nil, false
	}
	s = strings.ReplaceAll(s, "_", "")
	if s == "" {
		return nil, false
	}
	n, ok := new(big.Int).SetString(s, base)
	if !ok {
		return nil, false
	}
	if neg {
		n.Neg(n)
	}
	return n, true
}

// overflowToInt is what int() says about an infinity.
func overflowToInt(float64) error {
	return errs.New(errs.OverflowError, "cannot convert float infinity to integer")
}

// intFromFloat is Python's int(float): it truncates toward zero, and it is
// exact at any size, so a value past int64 becomes a big integer instead of
// wrapping round to a negative one.
func intFromFloat(f float64) value.Value {
	t := math.Trunc(f)
	if n, ok := value.FloatToInt64(t); ok {
		return value.Int(n)
	}
	b, _ := big.NewFloat(t).Int(nil)
	return value.BigInt(b)
}

// numericText is the text Python's float() and int() read a value as.
//
// Both accept a bytes exactly as they accept a str -- float(b"1.5") is 1.5 and
// int(b"15") is 15, because the conversion parses ASCII digits and does not
// care which of the two carried them. Asking IsString alone left a bytes
// falling through to the filter's *default*, so `{{ "1.5".encode()|float }}`
// answered 0.0 rather than 1.5: a wrong number rather than an error, and
// silent.
func numericText(v value.Value) (string, bool) {
	switch v.Kind() {
	case value.KindString, value.KindBytes:
		return v.AsString(), true
	}
	return "", false
}

// isNumericText is numericText as a predicate, for a switch case.
func isNumericText(v value.Value) bool {
	_, ok := numericText(v)
	return ok
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
	if text, ok := numericText(v); ok {
		if f, ok := value.ParseFloat(strings.TrimSpace(text)); ok {
			return value.Float(f), nil
		}
	}
	return def, nil
}

// filterRound is jinja2's do_round, which is three different things depending
// on the method -- and checks the method before it looks at anything else.
//
// "common" is Python's round(value, precision): the method is looked up on the
// *value* first, so a list is refused before the precision is examined; a
// precision of None asks for an integer rather than a float; and the numeric
// type is preserved otherwise. "ceil" and "floor" multiply by 10**precision
// instead, which accepts a float precision, fails on None at the exponent, and
// divides by zero once the power underflows.
func filterRound(s *State, v value.Value, args *value.CallArgs) (value.Value, error) {
	method := "common"
	if m, ok := arg(args, 1, "method"); ok {
		// jinja2 writes `method not in {...}`, and membership of a set
		// asks whether the value can be hashed -- so a list here is
		// about the list, not about the method.
		if err := value.Hashable(m); err != nil {
			return value.Undefined, err
		}
		method = value.Str(m)
	}
	if method != "common" && method != "ceil" && method != "floor" {
		// Checked first, and by equality, so a method that is not even
		// a string lands here rather than anywhere later.
		return value.Undefined, errs.New(errs.FilterArgumentError,
			"method must be common, ceil or floor")
	}
	precision := value.Int(0)
	if p, ok := arg(args, 0, "precision"); ok {
		precision = p
	}

	if method != "common" {
		// jinja2 writes `func(value * (10 ** precision)) / (10 ** precision)`,
		// and the exponent is evaluated first -- so a precision that is
		// not a number fails there, whatever the value is. 10**precision
		// is an *integer* for a non-negative whole precision, which
		// matters: `[a, b] * 1` is a list, which math.ceil then rejects
		// as "must be real number, not list" rather than the
		// multiplication failing first.
		scale, err := value.Pow(value.Int(10), precision, s)
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
		if divisor == 0 {
			// 10**-400 underflows to 0.0, and Python then divides by
			// it. gojja2 answered NaN, which is not a number any
			// template asked for.
			return value.Undefined, errs.New(errs.ZeroDivisionError, "float division by zero")
		}
		// math.ceil and math.floor answer a Python int, so they refuse a
		// value that is not one -- which is where an infinity raises,
		// rather than dividing through as an infinity of its own.
		if math.IsInf(f, 0) {
			return value.Undefined, errs.New(errs.OverflowError,
				"cannot convert float infinity to integer")
		}
		if math.IsNaN(f) {
			return value.Undefined, errs.New(errs.ValueError,
				"cannot convert float NaN to integer")
		}
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

	// round() looks __round__ up on the value, so a type that has none is
	// refused here -- before the precision is looked at at all.
	if !v.IsNumber() {
		return value.Undefined, errs.New(errs.TypeError,
			"type %s doesn't define __round__ method", v.TypeName())
	}
	// round(x) and round(x, None) are the same call, and both answer an
	// *integer*: round(2.5) is 2, not 2.0.
	if precision.IsNone() {
		return roundToInteger(v)
	}
	digits, whole := precision.BigInt()
	if !whole {
		return value.Undefined, errs.New(errs.TypeError,
			"'%s' object cannot be interpreted as an integer", precision.TypeName())
	}

	// Python's round preserves the numeric type: round(5, 2) is the int 5,
	// while round(2.5, 0) is the float 2.0. A bool is an int, so
	// round(true, -1) is 0 and not 1.
	if v.IsInteger() {
		b, _ := v.BigInt()
		if digits.Sign() >= 0 {
			return value.BigInt(b), nil
		}
		// A negative precision rounds to a multiple of a power of ten,
		// and the answer is still an int: round(3, -1) is 0, not 0.0.
		k := new(big.Int).Neg(digits)
		// Past the width of the number every multiple is zero, and the
		// power of ten would not fit in memory. CPython computes it
		// anyway and takes minutes over a large enough precision; this
		// answers what it would have answered.
		if k.Cmp(big.NewInt(int64(len(new(big.Int).Abs(b).String())))) > 0 {
			return value.Int(0), nil
		}
		return value.BigInt(roundToPowerOfTen(b, int(k.Int64()))), nil
	}

	f, _ := v.Float64()
	// A float carries no decimal beyond ~1080 places, so a precision past
	// that cannot change the answer -- and clamping keeps every power of
	// ten below in range. CPython hangs rather than answering for a
	// precision of 2**70; this does not.
	p := clampPrecision(digits)
	if p >= 0 {
		// p is the number of digits FormatFloat is about to write, so it
		// sizes the allocation directly: round(2000000000) formats a
		// two-billion-digit decimal. A float64 carries no information
		// past ~17 significant digits, so anything past the charge is
		// padding zeroes -- but they still have to be paid for before
		// they are written.
		if err := s.ChargeBytes(int64(p)); err != nil {
			return value.Undefined, err
		}
		// Python rounds the decimal value, not the value scaled by a
		// power of ten: 2.675 is really 2.67499..., so round(2.675, 2)
		// is 2.67, while 2.675*100 rounds up to 267.5 and would give
		// 2.68. Formatting to the requested precision rounds correctly
		// against the true value, ties to even included.
		rounded, err := strconv.ParseFloat(strconv.FormatFloat(f, 'f', p, 64), 64)
		if err != nil {
			return value.Undefined, err
		}
		return value.Float(rounded), nil
	}
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return value.Float(f), nil
	}
	// The same exactness on the other side of the point. Scaling by a
	// float power of ten and dividing back loses digits -- it turned
	// 1e300|round(-300) into 1.0000000000000006e+300 -- and underflows to
	// NaN once the power reaches zero.
	pow := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(-p)), nil)
	r := new(big.Rat).SetFloat64(f)
	r.Quo(r, new(big.Rat).SetInt(pow))
	out := new(big.Rat).SetInt(ratRoundHalfEven(r))
	out.Mul(out, new(big.Rat).SetInt(pow))
	got, _ := out.Float64()
	if got == 0 && math.Signbit(f) {
		// A rational has no signed zero; Python's round keeps the sign.
		got = math.Copysign(0, -1)
	}
	return value.Float(got), nil
}

// maxFloatDecimals bounds a precision. The exact decimal expansion of a
// float64 terminates within 1075 places after the point and needs at most 309
// before it, so rounding anywhere past this leaves the value alone or takes it
// to zero, whatever the precision says.
const maxFloatDecimals = 1100

func clampPrecision(digits *big.Int) int {
	if digits.Cmp(big.NewInt(maxFloatDecimals)) > 0 {
		return maxFloatDecimals
	}
	if digits.Cmp(big.NewInt(-maxFloatDecimals)) < 0 {
		return -maxFloatDecimals
	}
	return int(digits.Int64())
}

// roundToInteger is round(x) with no precision, which Python answers as an int
// -- exactly, ties to even. A float past 2**53 has no digits to spare, so the
// answer has to be arbitrary precision: round(1e308) is 309 digits.
func roundToInteger(v value.Value) (value.Value, error) {
	if v.IsInteger() {
		b, _ := v.BigInt()
		return value.BigInt(b), nil
	}
	f, _ := v.Float64()
	if math.IsInf(f, 0) {
		return value.Undefined, errs.New(errs.OverflowError,
			"cannot convert float infinity to integer")
	}
	if math.IsNaN(f) {
		return value.Undefined, errs.New(errs.ValueError,
			"cannot convert float NaN to integer")
	}
	return value.BigInt(ratRoundHalfEven(new(big.Rat).SetFloat64(f))), nil
}

// ratRoundHalfEven rounds an exact rational to the nearest integer, ties to
// even -- Python's rule at every precision.
func ratRoundHalfEven(r *big.Rat) *big.Int {
	num, den := r.Num(), r.Denom()
	q, rem := new(big.Int).QuoRem(num, den, new(big.Int))
	twice := new(big.Int).Abs(rem)
	twice.Lsh(twice, 1)
	step := func() {
		if num.Sign() < 0 {
			q.Sub(q, big.NewInt(1))
		} else {
			q.Add(q, big.NewInt(1))
		}
	}
	switch twice.Cmp(den) {
	case 1:
		step()
	case 0:
		// Bit(0) is the parity of the magnitude for a negative too,
		// which is the parity Python's tie-break asks about.
		if q.Bit(0) == 1 {
			step()
		}
	}
	return q
}

// roundToPowerOfTen rounds n to the nearest multiple of 10**k, ties going to
// the even multiple -- which is what Python's round does for an integer.
//
// Exact, rather than by way of a float: an integer past 2**53 cannot be scaled
// and divided back without losing the digits that decide the answer.
func roundToPowerOfTen(n *big.Int, k int) *big.Int {
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(k)), nil)
	mag := new(big.Int).Abs(n)
	q, r := new(big.Int).QuoRem(mag, scale, new(big.Int))
	twice := new(big.Int).Lsh(r, 1)
	switch twice.Cmp(scale) {
	case 1:
		q.Add(q, big.NewInt(1))
	case 0:
		// A tie goes to the even multiple.
		if q.Bit(0) == 1 {
			q.Add(q, big.NewInt(1))
		}
	}
	out := q.Mul(q, scale)
	if n.Sign() < 0 {
		out.Neg(out)
	}
	return out
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
	parts := attrParts(attribute)
	for _, item := range items {
		if len(parts) > 0 {
			item, err = attrPath(s, item, parts)
			if err != nil {
				return value.Undefined, err
			}
		}
		total, err = value.Add(total, item, s)
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
		// jinja2 calls float(value), so the failure is float()'s -- and
		// float() takes a bytes as readily as a str.
		text, textual := numericText(v)
		if !textual {
			return value.Undefined, errs.New(errs.TypeError,
				"float() argument must be a string or a real number, not '%s'",
				v.TypeName())
		}
		f, ok := value.ParseFloat(strings.TrimSpace(text))
		if !ok {
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
	// do_attr starts with inspect.getattr_static, which looks the name up
	// in the type's dictionaries -- so an unhashable name is refused by
	// the lookup before anything checks that it is a string at all, and a
	// hashable one that is not a string is refused by that check. Neither
	// reaches the object, so both answer the same whatever it is.
	if err := value.Hashable(name); err != nil {
		return value.Undefined, err
	}
	if name.Kind() != value.KindString {
		return value.Undefined, errs.New(errs.TypeError,
			"attribute name must be string, not %s",
			value.Repr(value.String(name.TypeName())))
	}
	attrName := name.AsString()
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
		// jinja2 builds this as `item[0].upper() + item[1:].lower()`,
		// which are full case mappings over *slices* -- so the first
		// character of a word may become several.
		b.WriteString(pyUpperString(string(word[0])))
		b.WriteString(pyLowerString(string(word[1:])))
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
