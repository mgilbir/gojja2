// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"fmt"
	"math"
	"math/big"
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
	add("title", stringFilter(jinjaTitle))
	add("capitalize", stringFilter(pythonCapitalize))
	add("trim", filterTrim)
	add("string", filterString)
	add("replace", filterReplace)
	add("center", filterCenter)
	add("indent", definedFilter(filterIndent))
	add("truncate", filterTruncate)
	add("wordwrap", definedFilter(filterWordwrap))
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
	add("dictsort", definedFilter(filterDictsort))
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

func stringFilter(fn func(string) string) Filter {
	return func(_ *State, v value.Value, _ *value.CallArgs) (value.Value, error) {
		return value.String(fn(value.Str(v))), nil
	}
}

// materialize collects an iterable into a slice, which most sequence filters
// need because they reorder or count their input.
func materialize(v value.Value) ([]value.Value, error) {
	seq, err := value.Iterate(v)
	if err != nil {
		return nil, err
	}
	var out []value.Value
	for item := range seq {
		out = append(out, item)
	}
	return out, nil
}

// attrPath resolves a jinja2 attribute specification, which may be dotted
// ("user.name") and may address a sequence by index ("0.name").
func attrPath(s *State, v value.Value, path string) (value.Value, error) {
	ex := &exec{st: s, sc: s.ctx, autoescape: s.autoescape}
	for _, part := range strings.Split(path, ".") {
		if v.IsUndefined() {
			return v, nil
		}
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

// sortKeyFunc builds the key extractor a sorting filter uses.
func sortKeyFunc(s *State, attribute value.Value, caseSensitive bool) func(value.Value) (value.Value, error) {
	fold := func(v value.Value) value.Value {
		if !caseSensitive && v.IsString() {
			return value.String(strings.ToLower(v.AsString()))
		}
		return v
	}
	if attribute.IsUndefined() || attribute.IsNone() {
		return func(v value.Value) (value.Value, error) { return fold(v), nil }
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
		if len(keys) == 1 {
			return keys[0], nil
		}
		return value.NewTuple(keys...), nil
	}
}

// stableSortBy sorts in place, keeping the first comparison error. Go's sort
// cannot return one, and silently ordering values Python refuses to compare
// would be worse than reporting it.
func stableSortBy(items []value.Value, key func(value.Value) (value.Value, error), reverse bool) error {
	keys := make([]value.Value, len(items))
	for i, item := range items {
		k, err := key(item)
		if err != nil {
			return err
		}
		keys[i] = k
	}

	idx := make([]int, len(items))
	for i := range idx {
		idx[i] = i
	}
	var failure error
	less := func(a, b int) bool {
		if failure != nil {
			return false
		}
		ok, err := value.Ordered("<", keys[a], keys[b])
		if err != nil {
			failure = err
			return false
		}
		return ok
	}
	// Insertion sort keeps the order stable and lets an error stop early
	// without leaving the slice half-ordered by a broken comparison.
	for i := 1; i < len(idx); i++ {
		for j := i; j > 0; j-- {
			a, b := idx[j-1], idx[j]
			if reverse {
				a, b = b, a
			}
			if !less(b, a) {
				break
			}
			idx[j-1], idx[j] = idx[j], idx[j-1]
		}
		if failure != nil {
			return failure
		}
	}

	sorted := make([]value.Value, len(items))
	for i, j := range idx {
		sorted[i] = items[j]
	}
	copy(items, sorted)
	return failure
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
		return value.String(strings.Trim(text, chars.AsString())), nil
	}
	return value.String(strings.TrimFunc(text, unicode.IsSpace)), nil
}

// filterString converts to str, leaving a Markup value safe.
func filterString(_ *State, v value.Value, _ *value.CallArgs) (value.Value, error) {
	if v.IsString() {
		return v, nil
	}
	return value.String(value.Str(v)), nil
}

// filterReplace escapes its arguments when autoescaping, so that replacing
// with user data cannot inject markup.
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
		return value.String(strings.Replace(value.Str(v), value.Str(old), value.Str(new), count)), nil
	}
	// Under autoescape everything is escaped first, so the replacement
	// operates on escaped text and the result is safe.
	esc := func(x value.Value) string {
		if x.IsSafe() {
			return value.Str(x)
		}
		return escapeHTML(value.Str(x))
	}
	return value.Safe(strings.Replace(esc(v), esc(old), esc(new), count)), nil
}

func filterCenter(_ *State, v value.Value, args *value.CallArgs) (value.Value, error) {
	width, err := intArg(args, 0, "width", 80)
	if err != nil {
		return value.Undefined, err
	}
	return value.String(pad(value.Str(v), width, " ", padCentered)), nil
}

func filterIndent(_ *State, v value.Value, args *value.CallArgs) (value.Value, error) {
	// The width may be given as the indent string itself.
	prefix := "    "
	if w, ok := arg(args, 0, "width"); ok {
		if w.IsString() {
			prefix = w.AsString()
		} else {
			width, err := intArg(args, 0, "width", 4)
			if err != nil {
				return value.Undefined, err
			}
			prefix = strings.Repeat(" ", width)
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

	// jinja2 strips one trailing newline, indents, then puts it back.
	text := value.Str(v) + "\n"
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	for i, line := range lines {
		if i == 0 && !first {
			continue
		}
		if line == "" && !blank {
			continue
		}
		lines[i] = prefix + line
	}
	return value.String(strings.Join(lines, "\n")), nil
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
	end := "..."
	if e, ok := arg(args, 2, "end"); ok {
		end = value.Str(e)
	}
	leeway, err := intArg(args, 3, "leeway", s.env.policies.TruncateLeeway)
	if err != nil {
		return value.Undefined, err
	}
	if length < len(end) {
		return value.Undefined, errs.New(errs.ValueError,
			"expected length >= %d, got %d", len(end), length)
	}

	text := value.Str(v)
	if value.StrLen(text) <= length+leeway {
		return value.String(text), nil
	}
	head, _ := value.StrSlice(text, nil, ptr(length-value.StrLen(end)), nil)
	if killwords {
		return value.String(head + end), nil
	}
	if i := strings.LastIndexByte(head, ' '); i >= 0 {
		head = head[:i]
	}
	return value.String(head + end), nil
}

func ptr[T any](v T) *T { return &v }

func filterWordwrap(_ *State, v value.Value, args *value.CallArgs) (value.Value, error) {
	width, err := intArg(args, 0, "width", 79)
	if err != nil {
		return value.Undefined, err
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
		wrapString = value.Str(w)
	}

	var out []string
	for _, paragraph := range strings.Split(value.Str(v), "\n") {
		out = append(out, wrapLine(paragraph, width, breakLong, breakOnHyphens)...)
	}
	return value.String(strings.Join(out, wrapString)), nil
}

// wrapLine reproduces textwrap.wrap for one paragraph.
//
// The differences from a naive greedy fill are visible: whitespace between
// words is a chunk of its own, so the first line keeps its indentation while
// later lines do not, and every line has its trailing whitespace removed.
func wrapLine(text string, width int, breakLong, breakOnHyphens bool) []string {
	chunks := wrapChunks(text, breakOnHyphens)
	var lines []string

	for len(chunks) > 0 {
		// Leading whitespace is dropped on every line but the first.
		if len(lines) > 0 && strings.TrimSpace(chunks[0]) == "" {
			chunks = chunks[1:]
			if len(chunks) == 0 {
				break
			}
		}

		var cur []string
		curLen := 0
		for len(chunks) > 0 && curLen+value.StrLen(chunks[0]) <= width {
			curLen += value.StrLen(chunks[0])
			cur = append(cur, chunks[0])
			chunks = chunks[1:]
		}

		// A single chunk wider than the line has to be split, or kept
		// whole and allowed to overflow.
		if len(chunks) > 0 && value.StrLen(chunks[0]) > width {
			space := max(width-curLen, 1)
			if breakLong {
				head, _ := value.StrSlice(chunks[0], nil, &space, nil)
				tail, _ := value.StrSlice(chunks[0], &space, nil, nil)
				cur = append(cur, head)
				chunks[0] = tail
			} else if len(cur) == 0 {
				cur = append(cur, chunks[0])
				chunks = chunks[1:]
			}
		}

		for len(cur) > 0 && strings.TrimSpace(cur[len(cur)-1]) == "" {
			cur = cur[:len(cur)-1]
		}
		if len(cur) > 0 {
			lines = append(lines, strings.Join(cur, ""))
		}
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
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

// splitOnHyphens breaks a word after a hyphen that joins two letters, which is
// where textwrap allows a line to end.
func splitOnHyphens(word string) []string {
	runes := []rune(word)
	var out []string
	start := 0
	for i := 1; i < len(runes)-1; i++ {
		if runes[i] == '-' && unicode.IsLetter(runes[i-1]) && unicode.IsLetter(runes[i+1]) {
			out = append(out, string(runes[start:i+1]))
			start = i + 1
		}
	}
	return append(out, string(runes[start:]))
}

func filterWordcount(_ *State, v value.Value, _ *value.CallArgs) (value.Value, error) {
	return value.Int(int64(len(strings.Fields(value.Str(v))))), nil
}

// filterStriptags removes markup and normalises whitespace, the way jinja2
// does before handing text to something that cannot render HTML.
func filterStriptags(_ *State, v value.Value, _ *value.CallArgs) (value.Value, error) {
	s := value.Str(v)
	var b strings.Builder
	depth := 0
	for _, r := range s {
		switch {
		case r == '<':
			depth++
		case r == '>' && depth > 0:
			depth--
		case depth == 0:
			b.WriteRune(r)
		}
	}
	text := unescapeHTML(b.String())
	return value.String(strings.Join(strings.Fields(text), " ")), nil
}

var htmlUnescaper = strings.NewReplacer(
	"&lt;", "<", "&gt;", ">", "&#39;", "'", "&#34;", `"`, "&quot;", `"`,
	"&apos;", "'", "&nbsp;", " ", "&amp;", "&",
)

func unescapeHTML(s string) string { return htmlUnescaper.Replace(s) }

// filterFormat is the `%` operator in filter form.
func filterFormat(_ *State, v value.Value, args *value.CallArgs) (value.Value, error) {
	if len(args.Kwargs) > 0 {
		d := value.NewDict()
		dict, _ := d.Dict()
		for _, kw := range args.Kwargs {
			dict.SetString(kw.Name, kw.Value)
		}
		return value.Mod(value.String(value.Str(v)), d)
	}
	return value.Mod(value.String(value.Str(v)), value.NewTuple(args.Pos...))
}

// filterPprint renders a value the way Python's pprint.pformat does, which is
// repr() with dict keys sorted rather than in insertion order.
func filterPprint(_ *State, v value.Value, _ *value.CallArgs) (value.Value, error) {
	return value.String(value.Repr(sortDictKeys(v))), nil
}

func sortDictKeys(v value.Value) value.Value {
	switch v.Kind() {
	case value.KindDict:
		d, _ := v.Dict()
		entries := append([]value.DictEntry(nil), d.Entries()...)
		sort.SliceStable(entries, func(i, j int) bool {
			return value.Str(entries[i].Key) < value.Str(entries[j].Key)
		})
		out := value.NewDict()
		target, _ := out.Dict()
		for _, e := range entries {
			_ = target.Set(e.Key, sortDictKeys(e.Value))
		}
		return out
	case value.KindList, value.KindTuple:
		seq, _ := v.Seq()
		items := make([]value.Value, seq.Len())
		for i, item := range seq.Items() {
			items[i] = sortDictKeys(item)
		}
		if v.Kind() == value.KindTuple {
			return value.NewTuple(items...)
		}
		return value.NewList(items...)
	}
	return v
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
		if t >= math.MinInt64 && t <= math.MaxInt64 {
			return value.Int(int64(t)), nil
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
		if n, ok := new(big.Int).SetString(text, base); ok {
			return value.BigInt(n), nil
		}
		// jinja2 accepts "3.5" here by falling back to float then int.
		if f, err := strconv.ParseFloat(text, 64); err == nil {
			return value.Int(int64(math.Trunc(f))), nil
		}
	}
	return def, nil
}

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

func filterRound(_ *State, v value.Value, args *value.CallArgs) (value.Value, error) {
	precision, err := intArg(args, 0, "precision", 0)
	if err != nil {
		return value.Undefined, err
	}
	method := "common"
	if m, ok := arg(args, 1, "method"); ok {
		method = value.Str(m)
	}
	f, ok := v.Float64()
	if !ok {
		return value.Undefined, errs.New(errs.TypeError,
			"type %s doesn't define __round__ method", value.Repr(value.String(v.TypeName())))
	}

	scale := math.Pow(10, float64(precision))
	switch method {
	case "common":
		// Python rounds the decimal value, not the value scaled by a
		// power of ten: 2.675 is really 2.67499..., so round(2.675, 2)
		// is 2.67, while 2.675*100 rounds up to 267.5 and would give
		// 2.68. Formatting to the requested precision rounds correctly
		// against the true value, ties to even included.
		if precision >= 0 {
			rounded, err := strconv.ParseFloat(
				strconv.FormatFloat(f, 'f', precision, 64), 64)
			if err != nil {
				return value.Undefined, err
			}
			return value.Float(rounded), nil
		}
		return value.Float(math.RoundToEven(f*scale) / scale), nil
	case "ceil":
		return value.Float(math.Ceil(f*scale) / scale), nil
	case "floor":
		return value.Float(math.Floor(f*scale) / scale), nil
	}
	return value.Undefined, errs.New(errs.FilterArgumentError,
		"method must be common, ceil or floor")
}

func filterSum(s *State, v value.Value, args *value.CallArgs) (value.Value, error) {
	items, err := materialize(v)
	if err != nil {
		return value.Undefined, err
	}
	attribute, _ := arg(args, 0, "attribute")
	total, hasStart := arg(args, 1, "start")
	if !hasStart {
		total = value.Int(0)
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
		f, convErr := strconv.ParseFloat(value.Str(v), 64)
		if convErr != nil {
			return value.Undefined, errs.New(errs.TypeError,
				"cannot convert %s to a number", value.Repr(v))
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
		return value.String(fmt.Sprintf("%.0f Bytes", bytes)), nil
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
	// getattr() on an Undefined raises rather than missing.
	if err := requireDefined(v); err != nil {
		return value.Undefined, err
	}
	if attr, ok := lookupAttr(v, value.Str(name)); ok {
		return attr, nil
	}
	return s.Undefined(value.UndefinedAttr(v, value.Str(name))), nil
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
