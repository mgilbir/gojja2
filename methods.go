// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"strconv"
	"strings"
	"unicode"

	"github.com/mgilbir/gojja2/errs"
	"github.com/mgilbir/gojja2/value"
)

// jinja2 exposes real Python objects, so templates reach their methods
// directly: `d.items()`, `s.upper()`, `l.append(x)`. These are the ones
// templates actually use; anything missing shows up as an undefined attribute
// rather than as silently wrong output.

// statefulMethods are the methods that walk an iterable the caller supplies,
// rather than working from the receiver alone. They need the render's budget
// to bound that walk, so they are looked up before the ordinary tables.
//
// s is nil when the lookup comes from constant folding, which has no render to
// charge; State.Step handles that.
var statefulMethods = map[value.Kind]map[string]func(*State, value.Value, *value.CallArgs) (value.Value, error){
	value.KindList: {"extend": methodListExtend},
}

// builtinMethod resolves a method on a built-in type, returning it bound.
func builtinMethod(s *State, recv value.Value, name string) (value.Value, bool) {
	if fn, ok := statefulMethods[recv.Kind()][name]; ok {
		return Func(name, func(_ *State, args *value.CallArgs) (value.Value, error) {
			return fn(s, recv, args)
		}), true
	}
	var table map[string]func(value.Value, *value.CallArgs) (value.Value, error)
	switch recv.Kind() {
	case value.KindString:
		table = stringMethods
	case value.KindDict:
		table = dictMethods
	case value.KindList:
		table = listMethods
	case value.KindTuple:
		table = tupleMethods
	default:
		return value.Undefined, false
	}
	fn, ok := table[name]
	if !ok {
		return value.Undefined, false
	}
	return Func(name, func(_ *State, args *value.CallArgs) (value.Value, error) {
		return fn(recv, args)
	}), true
}

// arg reads a positional or named argument.
func arg(args *value.CallArgs, i int, name string) (value.Value, bool) {
	if v, ok := args.Arg(i); ok {
		return v, true
	}
	if name != "" {
		return args.Kwarg(name)
	}
	return value.Undefined, false
}

func strArg(args *value.CallArgs, i int, name, method string) (string, error) {
	v, ok := arg(args, i, name)
	if !ok {
		return "", errs.New(errs.TypeError, "%s() missing required argument", method)
	}
	if v.Kind() != value.KindString {
		return "", errs.New(errs.TypeError,
			"%s() argument must be str, not %s", method, v.TypeName())
	}
	return v.AsString(), nil
}

func intArg(args *value.CallArgs, i int, name string, def int) (int, error) {
	v, ok := arg(args, i, name)
	if !ok || v.IsNone() {
		return def, nil
	}
	n, fits := v.Int64()
	if !fits {
		return 0, errs.New(errs.TypeError, "expected an integer, not %s", v.TypeName())
	}
	return int(n), nil
}

// --- string methods ----------------------------------------------------------

// stringMethods is populated in init rather than in its declaration: format
// reaches back into attribute lookup, which reads this table, and Go rejects
// the initialisation cycle that would create.
var stringMethods map[string]func(value.Value, *value.CallArgs) (value.Value, error)

func init() {
	stringMethods = map[string]func(value.Value, *value.CallArgs) (value.Value, error){
		"upper": func(r value.Value, _ *value.CallArgs) (value.Value, error) {
			return value.String(strings.ToUpper(r.AsString())), nil
		},
		"lower": func(r value.Value, _ *value.CallArgs) (value.Value, error) {
			return value.String(strings.ToLower(r.AsString())), nil
		},
		"title": func(r value.Value, _ *value.CallArgs) (value.Value, error) {
			return value.String(pythonTitle(r.AsString())), nil
		},
		"capitalize": func(r value.Value, _ *value.CallArgs) (value.Value, error) {
			return value.String(pythonCapitalize(r.AsString())), nil
		},
		"swapcase": func(r value.Value, _ *value.CallArgs) (value.Value, error) {
			return value.String(swapCase(r.AsString())), nil
		},
		"casefold": func(r value.Value, _ *value.CallArgs) (value.Value, error) {
			return value.String(strings.ToLower(r.AsString())), nil
		},

		"strip":  trimMethod(strings.Trim, strings.TrimFunc),
		"lstrip": trimMethod(strings.TrimLeft, strings.TrimLeftFunc),
		"rstrip": trimMethod(strings.TrimRight, strings.TrimRightFunc),

		"split":      splitMethod(false),
		"rsplit":     splitMethod(true),
		"splitlines": methodSplitlines,
		"join":       methodJoin,
		"replace":    methodReplace,
		"startswith": affixMethod(strings.HasPrefix),
		"endswith":   affixMethod(strings.HasSuffix),
		"count":      methodStrCount,
		"find":       findMethod(strings.Index),
		"rfind":      findMethod(strings.LastIndex),
		"index":      indexMethod(strings.Index, "index"),
		"rindex":     indexMethod(strings.LastIndex, "rindex"),
		"format":     methodFormat,
		"format_map": methodFormatMap,
		"zfill":      methodZfill,
		"ljust":      padMethod(padLeftAligned),
		"rjust":      padMethod(padRightAligned),
		"center":     padMethod(padCentered),
		"encode": func(r value.Value, _ *value.CallArgs) (value.Value, error) {
			return value.Bytes([]byte(r.AsString())), nil
		},

		"isdigit": classifyMethod(unicode.IsDigit),
		"isalpha": classifyMethod(unicode.IsLetter),
		"isalnum": classifyMethod(func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }),
		"isspace": classifyMethod(unicode.IsSpace),
		"isupper": caseMethod(unicode.IsUpper, unicode.IsLower),
		"islower": caseMethod(unicode.IsLower, unicode.IsUpper),
	}
}

func trimMethod(withCutset func(string, string) string, withFunc func(string, func(rune) bool) string) func(value.Value, *value.CallArgs) (value.Value, error) {
	return func(r value.Value, args *value.CallArgs) (value.Value, error) {
		if v, ok := arg(args, 0, "chars"); ok && !v.IsNone() {
			if v.Kind() != value.KindString {
				return value.Undefined, errs.New(errs.TypeError,
					"strip argument must be str or None, not %s", v.TypeName())
			}
			return value.String(withCutset(r.AsString(), v.AsString())), nil
		}
		return value.String(withFunc(r.AsString(), unicode.IsSpace)), nil
	}
}

// splitMethod implements str.split and str.rsplit.
//
// Splitting on no separator is not splitting on " ": Python collapses runs of
// whitespace and drops leading and trailing empties, which is why
// `" a  b ".split()` has two elements and `" a  b ".split(" ")` has five.
func splitMethod(fromRight bool) func(value.Value, *value.CallArgs) (value.Value, error) {
	return func(r value.Value, args *value.CallArgs) (value.Value, error) {
		limit, err := intArg(args, 1, "maxsplit", -1)
		if err != nil {
			return value.Undefined, err
		}
		sep, hasSep := arg(args, 0, "sep")

		var parts []string
		if !hasSep || sep.IsNone() {
			parts = strings.FieldsFunc(r.AsString(), unicode.IsSpace)
			if limit >= 0 && len(parts) > limit+1 {
				parts = rejoinTail(r.AsString(), parts, limit, fromRight)
			}
		} else {
			if sep.Kind() != value.KindString {
				return value.Undefined, errs.New(errs.TypeError,
					"must be str or None, not %s", sep.TypeName())
			}
			if sep.AsString() == "" {
				return value.Undefined, errs.New(errs.ValueError, "empty separator")
			}
			n := -1
			if limit >= 0 {
				n = limit + 1
			}
			if fromRight && n > 0 {
				parts = splitRightN(r.AsString(), sep.AsString(), n)
			} else {
				parts = strings.SplitN(r.AsString(), sep.AsString(), n)
			}
		}

		items := make([]value.Value, len(parts))
		for i, p := range parts {
			items[i] = value.String(p)
		}
		return value.NewList(items...), nil
	}
}

// rejoinTail re-merges the parts beyond a whitespace split's maxsplit, keeping
// the original spacing of the remainder.
func rejoinTail(src string, parts []string, limit int, fromRight bool) []string {
	if fromRight {
		keep := parts[len(parts)-limit:]
		head := strings.TrimRightFunc(src, unicode.IsSpace)
		for _, p := range keep {
			head = head[:strings.LastIndex(head, p)]
		}
		return append([]string{strings.TrimRightFunc(head, unicode.IsSpace)}, keep...)
	}
	keep := parts[:limit]
	rest := strings.TrimLeftFunc(src, unicode.IsSpace)
	for _, p := range keep {
		rest = rest[strings.Index(rest, p)+len(p):]
	}
	return append(keep, strings.TrimLeftFunc(rest, unicode.IsSpace))
}

func splitRightN(s, sep string, n int) []string {
	all := strings.Split(s, sep)
	if len(all) <= n {
		return all
	}
	head := strings.Join(all[:len(all)-n+1], sep)
	return append([]string{head}, all[len(all)-n+1:]...)
}

func methodSplitlines(r value.Value, args *value.CallArgs) (value.Value, error) {
	keepEnds := false
	if v, ok := arg(args, 0, "keepends"); ok {
		ok, err := value.IsTrue(v)
		if err != nil {
			return value.Undefined, err
		}
		keepEnds = ok
	}
	s := r.AsString()
	var items []value.Value
	for len(s) > 0 {
		i := strings.IndexAny(s, "\n\r")
		if i < 0 {
			items = append(items, value.String(s))
			break
		}
		end := i + 1
		if s[i] == '\r' && end < len(s) && s[end] == '\n' {
			end++
		}
		if keepEnds {
			items = append(items, value.String(s[:end]))
		} else {
			items = append(items, value.String(s[:i]))
		}
		s = s[end:]
	}
	return value.NewList(items...), nil
}

func methodJoin(r value.Value, args *value.CallArgs) (value.Value, error) {
	v, ok := arg(args, 0, "iterable")
	if !ok {
		return value.Undefined, errs.New(errs.TypeError, "join() takes exactly one argument")
	}
	seq, err := value.Iterate(v)
	if err != nil {
		return value.Undefined, err
	}
	var parts []string
	for item := range seq {
		if item.Kind() != value.KindString {
			return value.Undefined, errs.New(errs.TypeError,
				"sequence item %d: expected str instance, %s found",
				len(parts), item.TypeName())
		}
		parts = append(parts, item.AsString())
	}
	return value.String(strings.Join(parts, r.AsString())), nil
}

func methodReplace(r value.Value, args *value.CallArgs) (value.Value, error) {
	old, err := strArg(args, 0, "old", "replace")
	if err != nil {
		return value.Undefined, err
	}
	new, err := strArg(args, 1, "new", "replace")
	if err != nil {
		return value.Undefined, err
	}
	count, err := intArg(args, 2, "count", -1)
	if err != nil {
		return value.Undefined, err
	}
	return value.String(strings.Replace(r.AsString(), old, new, count)), nil
}

// affixMethod implements startswith and endswith, which accept a tuple of
// candidates as well as a single string.
func affixMethod(match func(string, string) bool) func(value.Value, *value.CallArgs) (value.Value, error) {
	return func(r value.Value, args *value.CallArgs) (value.Value, error) {
		v, ok := arg(args, 0, "prefix")
		if !ok {
			return value.Undefined, errs.New(errs.TypeError, "missing required argument")
		}
		if s, ok := v.Seq(); ok && v.Kind() == value.KindTuple {
			for _, cand := range s.Items() {
				if cand.Kind() == value.KindString && match(r.AsString(), cand.AsString()) {
					return value.True, nil
				}
			}
			return value.False, nil
		}
		if v.Kind() != value.KindString {
			return value.Undefined, errs.New(errs.TypeError,
				"argument must be str or a tuple of str, not %s", v.TypeName())
		}
		return value.Bool(match(r.AsString(), v.AsString())), nil
	}
}

func methodStrCount(r value.Value, args *value.CallArgs) (value.Value, error) {
	sub, err := strArg(args, 0, "sub", "count")
	if err != nil {
		return value.Undefined, err
	}
	return value.Int(int64(strings.Count(r.AsString(), sub))), nil
}

// findMethod returns a code-point index, or -1, the way str.find does.
func findMethod(search func(string, string) int) func(value.Value, *value.CallArgs) (value.Value, error) {
	return func(r value.Value, args *value.CallArgs) (value.Value, error) {
		sub, err := strArg(args, 0, "sub", "find")
		if err != nil {
			return value.Undefined, err
		}
		at := search(r.AsString(), sub)
		if at < 0 {
			return value.Int(-1), nil
		}
		return value.Int(int64(value.StrLen(r.AsString()[:at]))), nil
	}
}

func indexMethod(search func(string, string) int, name string) func(value.Value, *value.CallArgs) (value.Value, error) {
	find := findMethod(search)
	return func(r value.Value, args *value.CallArgs) (value.Value, error) {
		v, err := find(r, args)
		if err != nil {
			return value.Undefined, err
		}
		if i, _ := v.Int64(); i < 0 {
			return value.Undefined, errs.New(errs.ValueError, "substring not found")
		}
		return v, nil
	}
}

// methodFormat implements str.format for the positional and named forms
// templates use. Format specs beyond a bare field name are not supported.
//
// Formatting a Markup string escapes every substituted value and yields
// Markup, which is markupsafe's whole point: `("a{}"|safe).format("<x>")`
// renders the escaped "<x>" rather than raw markup, so marking a *template*
// safe does not mark its arguments safe.
func methodFormat(r value.Value, args *value.CallArgs) (value.Value, error) {
	var b strings.Builder
	s := r.AsString()
	safe := r.IsSafe()
	auto := 0
	for i := 0; i < len(s); {
		switch {
		case strings.HasPrefix(s[i:], "{{"):
			b.WriteByte('{')
			i += 2
		case strings.HasPrefix(s[i:], "}}"):
			b.WriteByte('}')
			i += 2
		case s[i] == '{':
			end := strings.IndexByte(s[i:], '}')
			if end < 0 {
				return value.Undefined, errs.New(errs.ValueError,
					"Single '{' encountered in format string")
			}
			field := s[i+1 : i+end]
			i += end + 1
			v, err := resolveFormatField(field, args, &auto)
			if err != nil {
				return value.Undefined, err
			}
			if safe && !v.IsSafe() {
				b.WriteString(escapeHTML(value.Str(v)))
			} else {
				b.WriteString(value.Str(v))
			}
		case s[i] == '}':
			return value.Undefined, errs.New(errs.ValueError,
				"Single '}' encountered in format string")
		default:
			b.WriteByte(s[i])
			i++
		}
	}
	if safe {
		return value.Safe(b.String()), nil
	}
	return value.String(b.String()), nil
}

// methodFormatMap is str.format_map: the same substitution, with the fields
// looked up in a single mapping argument rather than in keyword arguments.
func methodFormatMap(r value.Value, args *value.CallArgs) (value.Value, error) {
	mapping, ok := arg(args, 0, "mapping")
	if !ok {
		return value.Undefined, errs.New(errs.TypeError,
			"format_map() takes exactly one argument (0 given)")
	}
	d, ok := mapping.Dict()
	if !ok {
		return value.Undefined, errs.New(errs.TypeError,
			"format_map() argument must be a mapping, not %s", mapping.TypeName())
	}
	kwargs := make([]value.Kwarg, 0, d.Len())
	for _, e := range d.Entries() {
		kwargs = append(kwargs, value.Kwarg{Name: value.Str(e.Key), Value: e.Value})
	}
	return methodFormat(r, &value.CallArgs{Kwargs: kwargs})
}

// resolveFormatField resolves one replacement field.
//
// A field is a name or position followed by any number of `.attr` and `[key]`
// accessors: "{0.name}", "{user[id]}", "{0.a[1].b}". The attribute form is a
// real attribute lookup with no fall-back to items, which is why
// `"{0.foo}".format({"foo": 42})` raises rather than finding the entry.
func resolveFormatField(field string, args *value.CallArgs, auto *int) (value.Value, error) {
	name, accessors := splitFieldName(field)

	v, err := resolveFieldBase(name, args, auto)
	if err != nil {
		return value.Undefined, err
	}
	for _, a := range accessors {
		if v, err = a.apply(v); err != nil {
			return value.Undefined, err
		}
	}
	return v, nil
}

// fieldAccessor is one `.attr` or `[key]` step.
type fieldAccessor struct {
	name    string
	isIndex bool
}

func (a fieldAccessor) apply(v value.Value) (value.Value, error) {
	if a.isIndex {
		key := value.String(a.name)
		if isAllDigits(a.name) {
			n, err := strconv.ParseInt(a.name, 10, 64)
			if err != nil {
				return value.Undefined, errs.New(errs.ValueError,
					"invalid index %q", a.name)
			}
			key = value.Int(n)
		}
		if item, ok := lookupItem(v, key); ok {
			return item, nil
		}
		if idx, ok := key.Int64(); ok {
			if seq, isSeq := v.Seq(); isSeq {
				i := int(idx)
				if i < 0 {
					i += seq.Len()
				}
				if i >= 0 && i < seq.Len() {
					return seq.At(i), nil
				}
				return value.Undefined, errs.New(errs.IndexError,
					"%s index out of range", v.TypeName())
			}
		}
		return value.Undefined, errs.New(errs.KeyError, "%s", value.Repr(key))
	}

	// Attribute access, with no item fall-back.
	if attr, ok := lookupAttr(nil, v, a.name); ok {
		return attr, nil
	}
	return value.Undefined, errs.New(errs.AttributeError,
		"'%s' object has no attribute '%s'", v.TypeName(), a.name)
}

// splitFieldName separates the base of a replacement field from its accessors,
// stopping at the conversion or format spec. The scan is bracket-aware, so a
// colon inside "[a:b]" does not end the field name.
func splitFieldName(field string) (string, []fieldAccessor) {
	end := len(field)
	depth := 0
	for i := 0; i < len(field); i++ {
		switch field[i] {
		case '[':
			depth++
		case ']':
			if depth > 0 {
				depth--
			}
		case '!', ':':
			if depth == 0 {
				end = i
				i = len(field)
			}
		}
	}
	field = field[:end]

	// The base runs to the first accessor.
	base := field
	if i := strings.IndexAny(field, ".["); i >= 0 {
		base, field = field[:i], field[i:]
	} else {
		field = ""
	}

	var accessors []fieldAccessor
	for field != "" {
		switch field[0] {
		case '.':
			field = field[1:]
			next := strings.IndexAny(field, ".[")
			if next < 0 {
				next = len(field)
			}
			accessors = append(accessors, fieldAccessor{name: field[:next]})
			field = field[next:]
		case '[':
			close := strings.IndexByte(field, ']')
			if close < 0 {
				accessors = append(accessors,
					fieldAccessor{name: field[1:], isIndex: true})
				field = ""
				continue
			}
			accessors = append(accessors,
				fieldAccessor{name: field[1:close], isIndex: true})
			field = field[close+1:]
		default:
			field = ""
		}
	}
	return base, accessors
}

// resolveFieldBase finds the argument a field names: automatic numbering when
// empty, positional when all digits, keyword otherwise.
func resolveFieldBase(name string, args *value.CallArgs, auto *int) (value.Value, error) {
	switch {
	case name == "":
		v, ok := args.Arg(*auto)
		*auto++
		if !ok {
			return value.Undefined, errs.New(errs.IndexError,
				"Replacement index %d out of range for positional args tuple", *auto-1)
		}
		return v, nil
	case isAllDigits(name):
		i := 0
		for _, c := range name {
			i = i*10 + int(c-'0')
		}
		v, ok := args.Arg(i)
		if !ok {
			return value.Undefined, errs.New(errs.IndexError,
				"Replacement index %d out of range for positional args tuple", i)
		}
		return v, nil
	default:
		v, ok := args.Kwarg(name)
		if !ok {
			return value.Undefined, errs.New(errs.KeyError,
				"%s", value.Repr(value.String(name)))
		}
		return v, nil
	}
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func methodZfill(r value.Value, args *value.CallArgs) (value.Value, error) {
	width, err := intArg(args, 0, "width", 0)
	if err != nil {
		return value.Undefined, err
	}
	s := r.AsString()
	n := value.StrLen(s)
	if n >= width {
		return value.String(s), nil
	}
	sign := ""
	if strings.HasPrefix(s, "-") || strings.HasPrefix(s, "+") {
		sign, s = s[:1], s[1:]
	}
	return value.String(sign + strings.Repeat("0", width-n) + s), nil
}

type padAlign int

const (
	padLeftAligned padAlign = iota
	padRightAligned
	padCentered
)

func padMethod(align padAlign) func(value.Value, *value.CallArgs) (value.Value, error) {
	return func(r value.Value, args *value.CallArgs) (value.Value, error) {
		width, err := intArg(args, 0, "width", 0)
		if err != nil {
			return value.Undefined, err
		}
		fill, err := fillCharArg(args, 1, "fillchar")
		if err != nil {
			return value.Undefined, err
		}
		return value.String(pad(r.AsString(), width, fill, align)), nil
	}
}

// fillCharArg reads the fill character str.center, str.ljust and str.rjust
// take, with CPython's own refusals.
//
// Both checks are load-bearing rather than pedantic. A non-string fill used to
// be ignored, so `"a".center(10, 5)` padded with spaces where CPython raises;
// and a multi-character fill was accepted, so `"a".center(10, "ab")` returned
// nineteen characters from a call that asked for ten. Silently returning a
// string of the wrong length is worse than refusing.
func fillCharArg(args *value.CallArgs, i int, name string) (string, error) {
	v, ok := arg(args, i, name)
	if !ok || v.IsNone() {
		return " ", nil
	}
	if v.Kind() != value.KindString {
		return "", errs.New(errs.TypeError,
			"The fill character must be a unicode character, not %s", v.TypeName())
	}
	fill := v.AsString()
	if value.StrLen(fill) != 1 {
		return "", errs.New(errs.TypeError,
			"The fill character must be exactly one character long")
	}
	return fill, nil
}

func pad(s string, width int, fill string, align padAlign) string {
	missing := width - value.StrLen(s)
	if missing <= 0 {
		return s
	}
	switch align {
	case padLeftAligned:
		return s + strings.Repeat(fill, missing)
	case padRightAligned:
		return strings.Repeat(fill, missing) + s
	default:
		left := missing / 2
		// Python's str.center puts the odd character on the right.
		return strings.Repeat(fill, left) + s + strings.Repeat(fill, missing-left)
	}
}

func classifyMethod(pred func(rune) bool) func(value.Value, *value.CallArgs) (value.Value, error) {
	return func(r value.Value, _ *value.CallArgs) (value.Value, error) {
		s := r.AsString()
		if s == "" {
			return value.False, nil
		}
		for _, c := range s {
			if !pred(c) {
				return value.False, nil
			}
		}
		return value.True, nil
	}
}

// caseMethod implements isupper and islower: at least one cased character, and
// no character of the opposite case.
func caseMethod(want, other func(rune) bool) func(value.Value, *value.CallArgs) (value.Value, error) {
	return func(r value.Value, _ *value.CallArgs) (value.Value, error) {
		seen := false
		for _, c := range r.AsString() {
			if other(c) {
				return value.False, nil
			}
			if want(c) {
				seen = true
			}
		}
		return value.Bool(seen), nil
	}
}

// pythonTitle uppercases the first letter of each run of letters, so
// "hello world's" becomes "Hello World'S" exactly as Python does.
func pythonTitle(s string) string {
	var b strings.Builder
	inWord := false
	for _, r := range s {
		isLetter := unicode.IsLetter(r) || unicode.IsDigit(r)
		switch {
		case !isLetter:
			b.WriteRune(r)
			inWord = false
		case inWord:
			b.WriteRune(unicode.ToLower(r))
		default:
			b.WriteRune(unicode.ToUpper(r))
			inWord = true
		}
	}
	return b.String()
}

func pythonCapitalize(s string) string {
	if s == "" {
		return s
	}
	runes := []rune(s)
	out := make([]rune, len(runes))
	out[0] = unicode.ToUpper(runes[0])
	for i := 1; i < len(runes); i++ {
		out[i] = unicode.ToLower(runes[i])
	}
	return string(out)
}

func swapCase(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case unicode.IsUpper(r):
			return unicode.ToLower(r)
		case unicode.IsLower(r):
			return unicode.ToUpper(r)
		}
		return r
	}, s)
}

// --- dict methods ------------------------------------------------------------

var dictMethods = map[string]func(value.Value, *value.CallArgs) (value.Value, error){
	"keys": func(r value.Value, _ *value.CallArgs) (value.Value, error) {
		d, _ := r.Dict()
		return value.NewList(d.Keys()...), nil
	},
	"values": func(r value.Value, _ *value.CallArgs) (value.Value, error) {
		d, _ := r.Dict()
		return value.NewList(d.Values()...), nil
	},
	"items":      methodDictItems,
	"get":        methodDictGet,
	"pop":        methodDictPop,
	"update":     methodDictUpdate,
	"copy":       func(r value.Value, _ *value.CallArgs) (value.Value, error) { d, _ := r.Dict(); return d.Clone(), nil },
	"clear":      methodDictClear,
	"setdefault": methodDictSetdefault,
}

func methodDictItems(r value.Value, _ *value.CallArgs) (value.Value, error) {
	d, _ := r.Dict()
	items := make([]value.Value, 0, d.Len())
	for _, e := range d.Entries() {
		items = append(items, value.NewTuple(e.Key, e.Value))
	}
	return value.NewList(items...), nil
}

func methodDictGet(r value.Value, args *value.CallArgs) (value.Value, error) {
	d, _ := r.Dict()
	key, ok := arg(args, 0, "key")
	if !ok {
		return value.Undefined, errs.New(errs.TypeError, "get expected at least 1 argument")
	}
	v, found, err := d.Get(key)
	if err != nil {
		return value.Undefined, err
	}
	if found {
		return v, nil
	}
	if def, ok := arg(args, 1, "default"); ok {
		return def, nil
	}
	return value.None, nil
}

func methodDictPop(r value.Value, args *value.CallArgs) (value.Value, error) {
	d, _ := r.Dict()
	key, ok := arg(args, 0, "key")
	if !ok {
		// CPython names the count it actually got, and dict.pop() is a
		// C function so the message comes from the argument clinic
		// rather than from Python.
		return value.Undefined, errs.New(errs.TypeError,
			"pop expected at least 1 argument, got %d", len(args.Pos))
	}
	v, found, err := d.Get(key)
	if err != nil {
		return value.Undefined, err
	}
	if found {
		if _, err := d.Delete(key); err != nil {
			return value.Undefined, err
		}
		return v, nil
	}
	if def, ok := arg(args, 1, "default"); ok {
		return def, nil
	}
	return value.Undefined, errs.New(errs.KeyError, "%s", value.Repr(key))
}

func methodDictSetdefault(r value.Value, args *value.CallArgs) (value.Value, error) {
	d, _ := r.Dict()
	key, ok := arg(args, 0, "key")
	if !ok {
		return value.Undefined, errs.New(errs.TypeError, "setdefault expected at least 1 argument")
	}
	if v, found, err := d.Get(key); err != nil {
		return value.Undefined, err
	} else if found {
		return v, nil
	}
	def, _ := arg(args, 1, "default")
	if !def.IsUndefined() {
		return def, d.Set(key, def)
	}
	return value.None, d.Set(key, value.None)
}

func methodDictUpdate(r value.Value, args *value.CallArgs) (value.Value, error) {
	d, _ := r.Dict()
	if other, ok := arg(args, 0, ""); ok {
		// Anything dict() accepts, update() accepts, and anything dict()
		// refuses it refuses the same way. This used to test only for a
		// mapping and discard everything else in silence, so
		// `d.update([("a", 1)])` left the dict empty and reported success.
		if err := updateDictFrom(d, other); err != nil {
			return value.Undefined, err
		}
	}
	for _, kw := range args.Kwargs {
		d.SetString(kw.Name, kw.Value)
	}
	return value.None, nil
}

// updateDictFrom merges src into d, accepting the three shapes Python's
// dict.update and dict() both take: a dict, any mapping, or an iterable of
// key/value pairs. It is the single implementation behind both, so the two
// cannot drift apart again.
func updateDictFrom(d *value.Dict, src value.Value) error {
	if src.IsUndefined() {
		// dict() probes for a keys() method first, and that probe is what
		// fails on an Undefined.
		return src.UndefinedError()
	}
	if sd, ok := src.Dict(); ok {
		for _, e := range sd.Entries() {
			if err := d.Set(e.Key, e.Value); err != nil {
				return err
			}
		}
		return nil
	}
	if m, ok := src.Interface().(value.Mapping); ok {
		for _, k := range m.Keys() {
			v, _ := m.GetItem(k)
			if err := d.Set(k, v); err != nil {
				return err
			}
		}
		return nil
	}
	pairs, err := value.Iterate(src)
	if err != nil {
		return errs.New(errs.TypeError, "'%s' object is not iterable", src.TypeName())
	}
	index := 0
	for pair := range pairs {
		key, val, err := unpackDictPair(pair, index)
		if err != nil {
			return err
		}
		if err := d.Set(key, val); err != nil {
			return err
		}
		index++
	}
	return nil
}

// unpackDictPair splits one element of a dict-update sequence into its key and
// value, with CPython's two refusals.
//
// The pair is *iterated* rather than required to be a list or tuple, because
// Python unpacks anything iterable of length two: `dict(["ab", "cd"])` is
// {'a': 'b', 'c': 'd'}, each pair being a two-character string. Testing for a
// sequence instead rejected that, and reported it as "has length 2; 2 is
// required" -- a message that contradicts itself, which is what gave the bug
// away.
func unpackDictPair(pair value.Value, index int) (value.Value, value.Value, error) {
	items, err := value.Iterate(pair)
	if err != nil {
		return value.Undefined, value.Undefined, errs.New(errs.TypeError,
			"cannot convert dictionary update sequence element #%d to a sequence", index)
	}
	var first, second value.Value
	n := 0
	for item := range items {
		switch n {
		case 0:
			first = item
		case 1:
			second = item
		}
		n++
	}
	if n != 2 {
		return value.Undefined, value.Undefined, errs.New(errs.ValueError,
			"dictionary update sequence element #%d has length %d; 2 is required",
			index, n)
	}
	return first, second, nil
}

func methodDictClear(r value.Value, _ *value.CallArgs) (value.Value, error) {
	d, _ := r.Dict()
	for _, k := range d.Keys() {
		if _, err := d.Delete(k); err != nil {
			return value.Undefined, err
		}
	}
	return value.None, nil
}

// --- list and tuple methods --------------------------------------------------

var listMethods = map[string]func(value.Value, *value.CallArgs) (value.Value, error){
	"append":  methodListAppend,
	"insert":  methodListInsert,
	"pop":     methodListPop,
	"remove":  methodListRemove,
	"index":   methodSeqIndex,
	"count":   methodSeqCount,
	"reverse": methodListReverse,
	"copy":    func(r value.Value, _ *value.CallArgs) (value.Value, error) { return r.AsList(), nil },
	"clear":   methodListClear,
}

func methodListClear(r value.Value, _ *value.CallArgs) (value.Value, error) {
	s, _ := r.Seq()
	*s = *mustSeq(value.NewList())
	return value.None, nil
}

var tupleMethods = map[string]func(value.Value, *value.CallArgs) (value.Value, error){
	"index": methodSeqIndex,
	"count": methodSeqCount,
}

func methodListAppend(r value.Value, args *value.CallArgs) (value.Value, error) {
	v, ok := arg(args, 0, "")
	if !ok {
		return value.Undefined, errs.New(errs.TypeError,
			"append() takes exactly one argument (0 given)")
	}
	s, _ := r.Seq()
	s.Append(v)
	return value.None, nil
}

func methodListExtend(st *State, r value.Value, args *value.CallArgs) (value.Value, error) {
	v, ok := arg(args, 0, "")
	if !ok {
		return value.Undefined, errs.New(errs.TypeError, "extend() takes exactly one argument")
	}
	seq, err := value.Iterate(v)
	if err != nil {
		return value.Undefined, err
	}
	s, _ := r.Seq()
	for item := range seq {
		// `l.extend(range(10000000000))` grows the receiver without any
		// {% for %} to bound it, so the walk is charged here.
		if err := st.Step(1); err != nil {
			return value.Undefined, err
		}
		s.Append(item)
	}
	return value.None, nil
}

func methodListInsert(r value.Value, args *value.CallArgs) (value.Value, error) {
	at, err := intArg(args, 0, "", 0)
	if err != nil {
		return value.Undefined, err
	}
	v, ok := arg(args, 1, "")
	if !ok {
		return value.Undefined, errs.New(errs.TypeError, "insert() takes exactly 2 arguments")
	}
	s, _ := r.Seq()
	items := s.Items()
	if at < 0 {
		at += len(items)
	}
	at = min(max(at, 0), len(items))
	items = append(items, value.None)
	copy(items[at+1:], items[at:])
	items[at] = v
	*s = *mustSeq(value.NewList(items...))
	return value.None, nil
}

func methodListPop(r value.Value, args *value.CallArgs) (value.Value, error) {
	s, _ := r.Seq()
	items := s.Items()
	if len(items) == 0 {
		return value.Undefined, errs.New(errs.IndexError, "pop from empty list")
	}
	at, err := intArg(args, 0, "", len(items)-1)
	if err != nil {
		return value.Undefined, err
	}
	if at < 0 {
		at += len(items)
	}
	if at < 0 || at >= len(items) {
		return value.Undefined, errs.New(errs.IndexError, "pop index out of range")
	}
	out := items[at]
	*s = *mustSeq(value.NewList(append(append([]value.Value{}, items[:at]...), items[at+1:]...)...))
	return out, nil
}

func methodListRemove(r value.Value, args *value.CallArgs) (value.Value, error) {
	v, ok := arg(args, 0, "")
	if !ok {
		return value.Undefined, errs.New(errs.TypeError, "remove() takes exactly one argument")
	}
	s, _ := r.Seq()
	for i, item := range s.Items() {
		if value.Equal(item, v) {
			items := s.Items()
			*s = *mustSeq(value.NewList(append(append([]value.Value{}, items[:i]...), items[i+1:]...)...))
			return value.None, nil
		}
	}
	return value.Undefined, errs.New(errs.ValueError, "list.remove(x): x not in list")
}

func methodListReverse(r value.Value, _ *value.CallArgs) (value.Value, error) {
	s, _ := r.Seq()
	items := s.Items()
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
	return value.None, nil
}

func methodSeqIndex(r value.Value, args *value.CallArgs) (value.Value, error) {
	v, ok := arg(args, 0, "")
	if !ok {
		return value.Undefined, errs.New(errs.TypeError, "index() takes at least one argument")
	}
	s, _ := r.Seq()
	for i, item := range s.Items() {
		if value.Equal(item, v) {
			return value.Int(int64(i)), nil
		}
	}
	return value.Undefined, errs.New(errs.ValueError, "%s is not in list", value.Repr(v))
}

func methodSeqCount(r value.Value, args *value.CallArgs) (value.Value, error) {
	v, ok := arg(args, 0, "")
	if !ok {
		return value.Undefined, errs.New(errs.TypeError, "count() takes exactly one argument")
	}
	s, _ := r.Seq()
	n := 0
	for _, item := range s.Items() {
		if value.Equal(item, v) {
			n++
		}
	}
	return value.Int(int64(n)), nil
}

func mustSeq(v value.Value) *value.Seq {
	s, _ := v.Seq()
	return s
}
