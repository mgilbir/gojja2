// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"

	"github.com/mgilbir/gojja2/errs"
	"github.com/mgilbir/gojja2/value"
)

// filterURLEncode percent-encodes a string, or builds a query string from a
// mapping or a sequence of pairs.
func filterURLEncode(s *State, v value.Value, _ *value.CallArgs) (value.Value, error) {
	pair := func(k, val value.Value) (string, error) {
		key, err := quotePlus(s, value.Str(k))
		if err != nil {
			return "", err
		}
		text, err := quotePlus(s, value.Str(val))
		if err != nil {
			return "", err
		}
		return key + "=" + text, nil
	}
	if d, ok := v.Dict(); ok {
		parts := make([]string, 0, d.Len())
		for _, e := range d.Entries() {
			p, err := pair(e.Key, e.Value)
			if err != nil {
				return value.Undefined, err
			}
			parts = append(parts, p)
		}
		return value.String(strings.Join(parts, "&")), nil
	}
	// Anything iterable that is not a string is a sequence of pairs, a
	// range included.
	if !v.IsString() && isIterableValue(v) {
		// `"&".join(f"..." for k, v in items)` builds each pair as it
		// takes it, which is visible when the input is an iterator
		// something else is also walking -- `loop` inside its own body
		// renders a different position in each pair than it would if
		// they were all collected first.
		seq, err := value.Iterate(v)
		if err != nil {
			return value.Undefined, err
		}
		var parts []string
		for item := range seq {
			if err := s.Step(1); err != nil {
				return value.Undefined, err
			}
			// jinja2 writes `for k, v in items`, so each element is
			// unpacked and fails with Python's unpacking errors --
			// a string of six characters is iterable but too long.
			k, val, err := unpackPair(item)
			if err != nil {
				return value.Undefined, err
			}
			p, err := pair(k, val)
			if err != nil {
				return value.Undefined, err
			}
			parts = append(parts, p)
		}
		return value.String(strings.Join(parts, "&")), nil
	}
	// A bare string keeps "/" unescaped, matching urllib.parse.quote.
	quoted, err := quoteURL(s, value.Str(v), true)
	if err != nil {
		return value.Undefined, err
	}
	return value.String(quoted), nil
}

// quotePlus is urllib.parse.quote_plus, which query strings use: a space
// becomes "+" rather than "%20", and "/" is not exempt.
func quotePlus(st *State, s string) (string, error) {
	quoted, err := quoteURL(st, strings.ReplaceAll(s, " ", "\x00"), false)
	if err != nil {
		return "", err
	}
	return strings.ReplaceAll(quoted, "%00", "+"), nil
}

// hexDigits is the alphabet percent-encoding writes, upper-case as
// urllib.parse.quote produces.
const hexDigits = "0123456789ABCDEF"

// quoteURL percent-encodes everything outside the unreserved set. keepSlash
// mirrors urllib.parse.quote's default safe="/".
//
// Both hex digits are written directly. Formatting the byte and then patching
// in a leading zero meant copying the whole buffer twice for every byte below
// 0x10 -- which includes \n and \t, so ordinary multi-line text hit it. That
// made the filter quadratic: 40,000 newlines took 0.256s against 0.001s for the
// same length of text one byte higher, and a megabyte would have taken minutes.
func quoteURL(st *State, s string, keepSlash bool) (string, error) {
	const unreserved = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_.-~"
	var b strings.Builder
	b.Grow(len(s))
	for i := range len(s) {
		// A long string is a long run of work that writes no output and
		// walks no sequence, so it would otherwise never consult the
		// context. Polling costs an increment per byte.
		if err := st.Poll(); err != nil {
			return "", err
		}
		c := s[i]
		switch {
		case strings.IndexByte(unreserved, c) >= 0, keepSlash && c == '/':
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(hexDigits[c>>4])
			b.WriteByte(hexDigits[c&0x0f])
		}
	}
	return b.String(), nil
}

// httpRe recognises the URL shapes jinja2 links: a scheme or www prefix with
// a TLD, a bare domain under a handful of generic TLDs, or a scheme with a
// literal IPv4 or IPv6 address, each with an optional port, path and fragment.
var httpRe = regexp.MustCompile(`(?is)^(` +
	`(https?://|www\.)(([\w%-]+\.)+)?([a-z]{2,63}|xn--[\w%]{2,59})` +
	`|([\w%-]{2,63}\.)+(com|net|int|edu|gov|org|info|mil)` +
	`|(https?://)((([\d]{1,3})(\.[\d]{1,3}){3})|(\[([\da-f]{0,4}:){2}([\da-f]{0,4}:?){1,6}\]))` +
	`)(:[\d]{1,5})?([/?#]\S*)?$`)

var emailRe = regexp.MustCompile(`^\S+@\w[\w.-]*\.\w+$`)

var urlizeLeadRe = regexp.MustCompile(`^([(<]|&lt;)+`)
var urlizeTailRe = regexp.MustCompile(`([)>.,\n]|&gt;)+$`)

// filterUrlize turns URLs and email addresses in text into links.
//
// The text is HTML-escaped before anything else, whatever the autoescape
// setting, because the result is markup either way; linking unescaped text
// would turn a URL containing "<" into a tag. The `rel` and `target`
// attributes apply to web links only, not to mailto links.
func filterUrlize(s *State, v value.Value, args *value.CallArgs) (value.Value, error) {
	trimLimit, hasLimit := 0, false
	if lim, ok := arg(args, 0, "trim_url_limit"); ok && !lim.IsNone() {
		n, err := intArg(args, 0, "trim_url_limit", 0)
		if err != nil {
			return value.Undefined, err
		}
		trimLimit, hasLimit = n, true
	}
	// jinja2's signature is (trim_url_limit, nofollow, target, rel,
	// extra_schemes); the positional order matters for templates that pass
	// arguments without naming them.
	nofollow, err := boolArg(args, 1, "nofollow", false)
	if err != nil {
		return value.Undefined, err
	}
	target, _ := arg(args, 2, "target")
	rel, _ := arg(args, 3, "rel")

	// The rel attribute is the union of the argument, "nofollow" when
	// asked, and the environment's urlize.rel policy -- which defaults to
	// "noopener", so every generated link carries it unless the policy is
	// cleared. The parts are sorted, as jinja2 sorts the set.
	// jinja2 writes `rel.split()`, so a rel that is not a string fails as a
	// missing attribute rather than being stringified. target is only
	// interpolated, so anything goes there.
	if !rel.IsUndefined() && !rel.IsNone() && !rel.IsString() {
		return value.Undefined, errs.New(errs.AttributeError,
			"'%s' object has no attribute 'split'", rel.TypeName())
	}
	relParts := map[string]bool{}
	for _, part := range strings.Fields(attrText(rel)) {
		relParts[part] = true
	}
	if nofollow {
		relParts["nofollow"] = true
	}
	for _, part := range strings.Fields(s.env.policies.URLizeRel) {
		relParts[part] = true
	}
	sortedRel := make([]string, 0, len(relParts))
	for part := range relParts {
		sortedRel = append(sortedRel, part)
	}
	sort.Strings(sortedRel)

	if target.IsUndefined() || target.IsNone() {
		target = value.String(s.env.policies.URLizeTarget)
	}

	var extra []string
	if schemes, ok := arg(args, 4, "extra_schemes"); ok && !schemes.IsNone() {
		items, err := materialize(s, schemes)
		if err != nil {
			return value.Undefined, err
		}
		for _, item := range items {
			extra = append(extra, value.Str(item))
		}
	}

	attrs := ""
	if len(sortedRel) > 0 {
		attrs += ` rel="` + escapeHTML(strings.Join(sortedRel, " ")) + `"`
	}
	if targetText := attrText(target); targetText != "" {
		attrs += ` target="` + escapeHTML(targetText) + `"`
	}

	trim := func(x string) string {
		if hasLimit && value.StrLen(x) > trimLimit {
			head, _ := value.StrSlice(x, nil, &trimLimit, nil)
			return head + "..."
		}
		return x
	}

	escaped := value.Str(escapeIfNeeded(v))
	words := splitKeepingSpace(escaped)

	for i, word := range words {
		// Each word is matched against several regexps, so a long text
		// is sustained work with no output written until the end.
		if err := s.Poll(); err != nil {
			return value.Undefined, err
		}
		head, middle, tail := peelPunctuation(word)

		switch {
		case httpRe.MatchString(middle):
			href := middle
			if !strings.HasPrefix(middle, "https://") && !strings.HasPrefix(middle, "http://") {
				href = "https://" + middle
			}
			middle = `<a href="` + href + `"` + attrs + `>` + trim(middle) + `</a>`

		case strings.HasPrefix(middle, "mailto:") && emailRe.MatchString(middle[7:]):
			middle = `<a href="` + middle + `">` + middle[7:] + `</a>`

		case strings.Contains(middle, "@") &&
			!strings.HasPrefix(middle, "www.") &&
			!strings.HasPrefix(middle, "@") &&
			!strings.Contains(middle, ":") &&
			emailRe.MatchString(middle):
			middle = `<a href="mailto:` + middle + `">` + middle + `</a>`

		default:
			for _, scheme := range extra {
				if middle != scheme && strings.HasPrefix(middle, scheme) {
					middle = `<a href="` + middle + `"` + attrs + `>` + middle + `</a>`
					break
				}
			}
		}
		words[i] = head + middle + tail
	}

	out := strings.Join(words, "")
	if s.autoescape {
		return value.Safe(out), nil
	}
	return value.String(out), nil
}

func attrText(v value.Value) string {
	if v.IsUndefined() || v.IsNone() {
		return ""
	}
	return value.Str(v)
}

// splitKeepingSpace splits on whitespace runs but keeps them, so rejoining
// reproduces the original spacing exactly.
func splitKeepingSpace(s string) []string {
	var out []string
	i := 0
	for i < len(s) {
		start := i
		inSpace := isASCIISpace(s[i])
		for i < len(s) && isASCIISpace(s[i]) == inSpace {
			i++
		}
		out = append(out, s[start:i])
	}
	return out
}

func isASCIISpace(b byte) bool {
	switch b {
	case ' ', '\t', '\n', '\r', '\v', '\f':
		return true
	}
	return false
}

// peelPunctuation splits leading openers and trailing punctuation off a word,
// then moves back whatever is needed to keep brackets balanced -- so a URL
// inside parentheses keeps its own closing bracket.
func peelPunctuation(word string) (head, middle, tail string) {
	middle = word
	if m := urlizeLeadRe.FindString(middle); m != "" {
		head, middle = m, middle[len(m):]
	}
	if strings.HasSuffix(middle, ")") || strings.HasSuffix(middle, ">") ||
		strings.HasSuffix(middle, ".") || strings.HasSuffix(middle, ",") ||
		strings.HasSuffix(middle, "\n") || strings.HasSuffix(middle, "&gt;") {
		if loc := urlizeTailRe.FindStringIndex(middle); loc != nil {
			tail, middle = middle[loc[0]:], middle[:loc[0]]
		}
	}

	for _, pair := range [][2]string{{"(", ")"}, {"<", ">"}, {"&lt;", "&gt;"}} {
		open, close := pair[0], pair[1]
		opens := strings.Count(middle, open)
		if opens <= strings.Count(middle, close) {
			continue
		}
		for range min(opens, strings.Count(tail, close)) {
			end := strings.Index(tail, close) + len(close)
			middle += tail[:end]
			tail = tail[end:]
		}
	}
	return head, middle, tail
}

// filterXMLAttr renders a mapping as HTML attributes, skipping entries whose
// value is none or undefined.
func filterXMLAttr(s *State, v value.Value, args *value.CallArgs) (value.Value, error) {
	autospace, err := boolArg(args, 0, "autospace", true)
	if err != nil {
		return value.Undefined, err
	}
	d, ok := v.Dict()
	if !ok {
		return value.Undefined, itemsAttributeError(v)
	}

	var parts []string
	for _, e := range d.Entries() {
		if e.Value.IsNone() || e.Value.IsUndefined() {
			continue
		}
		if !e.Key.IsString() {
			return value.Undefined, errs.New(errs.TypeError,
				"expected string or bytes-like object, got '%s'", e.Key.TypeName())
		}
		key := e.Key.AsString()
		// A key with whitespace or a quote could close the attribute
		// and start another, so it is refused rather than escaped.
		if strings.ContainsAny(key, " \t\n\r\f\v/>=") {
			return value.Undefined, errs.New(errs.ValueError,
				"Invalid character %s in attribute name.",
				value.Repr(value.String(key)))
		}
		parts = append(parts, fmt.Sprintf(`%s="%s"`, escapeHTML(key), escapeHTML(value.Str(e.Value))))
	}

	out := strings.Join(parts, " ")
	if autospace && out != "" {
		out = " " + out
	}
	if s.autoescape {
		return value.Safe(out), nil
	}
	return value.String(out), nil
}

// filterToJSON serialises a value as JSON that is safe to embed in a <script>
// block: the characters that could close the tag or start an entity are
// written as escapes.
func filterToJSON(s *State, v value.Value, args *value.CallArgs) (value.Value, error) {
	// json.dumps splits "no indent" from "an indent of zero": only None
	// gives the one-line form, while 0 -- and any negative, which clamps to
	// 0 -- still puts every element on its own line. A default of 0 here
	// collapsed the two, so `{{ x|tojson(0) }}` came out on one line where
	// CPython breaks it. A negative indent means none, internally.
	indent := -1
	if a, ok := arg(args, 0, "indent"); ok && !a.IsNone() {
		n, err := intArg(args, 0, "indent", 0)
		if err != nil {
			return value.Undefined, err
		}
		indent = max(n, 0)
	}
	var b strings.Builder
	if err := writeJSON(s, &b, v, indent, 0, nil); err != nil {
		return value.Undefined, err
	}
	// htmlsafe_json_dumps returns Markup whatever the autoescape setting,
	// because its output is escaped by construction.
	return value.Safe(htmlSafeJSON(b.String())), nil
}

// jsonHTMLEscaper makes serialised JSON safe to embed in a <script> block:
// the characters that could close the tag or open an entity are written as
// JSON unicode escapes, which parse back to themselves.
var jsonHTMLEscaper = strings.NewReplacer(
	"<", "\\u003c",
	">", "\\u003e",
	"&", "\\u0026",
	"'", "\\u0027",
)

func htmlSafeJSON(s string) string { return jsonHTMLEscaper.Replace(s) }

// jsonPath tracks the containers currently being serialised. A value graph can
// contain a cycle -- a Go context that refers to itself converts into one --
// and json.dumps refuses those rather than recursing forever.
type jsonPath map[any]bool

func (p jsonPath) enter(key any) (jsonPath, bool) {
	if p[key] {
		return p, false
	}
	if p == nil {
		p = make(jsonPath, 4)
	}
	p[key] = true
	return p, true
}

func (p jsonPath) leave(key any) { delete(p, key) }

// maxJSONDepth bounds how deeply tojson descends.
//
// The nesting of a value graph is chosen at render time, so the walk needs a
// wall of its own or a deep one takes the stack out. CPython has the same wall
// and the same message, at about the same depth: json.dumps runs out of
// interpreter stack at 986 levels of list.
const maxJSONDepth = 1000

// RecursionMessageJSON is what CPython reports when json.dumps runs out of
// stack, and therefore what tojson must report here.
const RecursionMessageJSON = "maximum recursion depth exceeded while encoding a JSON object"

func writeJSON(st *State, b *strings.Builder, v value.Value, indent, depth int, path jsonPath) error {
	if depth > maxJSONDepth {
		return errs.New(errs.RecursionError, "%s", RecursionMessageJSON)
	}
	// json.dumps separates with ", " until an indent is given, at which
	// point the space moves onto the next line.
	nl, pad, padEnd, comma := "", "", "", ", "
	if indent >= 0 {
		// The indent is repeated once per level and once per element, so
		// a template-chosen one sizes the whole document: tojson(2000000000)
		// asked for a two-gigabyte prefix. indent*(depth+1) can also
		// overflow an int and reach strings.Repeat as a negative count,
		// which panics -- so the multiplication saturates rather than
		// wrapping, and the result is charged before it is built.
		var err error
		pad, err = st.repeatStringN(" ", saturatingMulInt(int64(indent), int64(depth+1)))
		if err != nil {
			return err
		}
		padEnd, err = st.repeatStringN(" ", saturatingMulInt(int64(indent), int64(depth)))
		if err != nil {
			return err
		}
		nl = "\n"
		comma = ","
	}

	switch v.Kind() {
	case value.KindUndefined:
		return errs.New(errs.TypeError,
			"Object of type %s is not JSON serializable", v.TypeName())
	case value.KindNone:
		b.WriteString("null")
	case value.KindBool:
		if v.AsBool() {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case value.KindInt:
		b.WriteString(value.Repr(v))
	case value.KindFloat:
		f := v.AsFloat()
		// Python's json emits NaN and Infinity bare, which is not valid
		// JSON but is what jinja2 produces. Writing "Infinity" and then
		// retracting it for a negative one reset the whole builder, not
		// this element: `{{ [1, -1e308*10, 2]|tojson }}` rendered
		// "-Infinity, 2]", a truncated document, with no error.
		switch {
		case math.IsNaN(f):
			b.WriteString("NaN")
		case math.IsInf(f, 1):
			b.WriteString("Infinity")
		case math.IsInf(f, -1):
			b.WriteString("-Infinity")
		default:
			b.WriteString(value.FormatFloat(f))
		}
	case value.KindString, value.KindBytes:
		writeJSONString(b, v.AsString())
	case value.KindList, value.KindTuple:
		s, _ := v.Seq()
		if s.Len() == 0 {
			b.WriteString("[]")
			return nil
		}
		next, ok := path.enter(v.Interface())
		if !ok {
			return errs.New(errs.ValueError, "Circular reference detected")
		}
		defer next.leave(v.Interface())
		path = next
		b.WriteString("[" + nl)
		for i, item := range s.Items() {
			if i > 0 {
				b.WriteString(comma + nl)
			}
			b.WriteString(pad)
			if err := writeJSON(st, b, item, indent, depth+1, path); err != nil {
				return err
			}
		}
		b.WriteString(nl + padEnd + "]")
	case value.KindDict:
		d, _ := v.Dict()
		if d.Len() == 0 {
			b.WriteString("{}")
			return nil
		}
		next, ok := path.enter(v.Interface())
		if !ok {
			return errs.New(errs.ValueError, "Circular reference detected")
		}
		defer next.leave(v.Interface())
		path = next
		b.WriteString("{" + nl)
		// jinja2's tojson policy sets sort_keys, so a dict serialises in
		// key order rather than insertion order.
		entries := append([]value.DictEntry(nil), d.Entries()...)
		sort.SliceStable(entries, func(i, j int) bool {
			return value.Str(entries[i].Key) < value.Str(entries[j].Key)
		})
		for i, e := range entries {
			if i > 0 {
				b.WriteString(comma + nl)
			}
			b.WriteString(pad)
			if !jsonKeyable(e.Key) {
				return errs.New(errs.TypeError,
					"keys must be str, int, float, bool or None, not %s",
					e.Key.TypeName())
			}
			writeJSONString(b, value.Str(e.Key))
			b.WriteString(": ")
			if err := writeJSON(st, b, e.Value, indent, depth+1, path); err != nil {
				return err
			}
		}
		b.WriteString(nl + padEnd + "}")
	case value.KindObject:
		// A tuple subclass serialises as the array it is.
		if tv, ok := v.Interface().(value.TupleView); ok {
			return writeJSON(st, b, tv.AsTuple(), indent, depth, path)
		}
		// A Go struct or map reaches a template as a Mapping and is
		// serialised like the dict it stands for. Everything else --
		// a range, a cycler, a macro -- is not serialisable, which is
		// what json.dumps says about jinja2's own types too.
		if m, ok := v.Interface().(value.Mapping); ok {
			out := value.NewDict()
			target, _ := out.Dict()
			for _, k := range m.Keys() {
				val, _ := m.GetItem(k)
				_ = target.Set(k, val)
			}
			return writeJSON(st, b, out, indent, depth, path)
		}
		return errs.New(errs.TypeError,
			"Object of type %s is not JSON serializable", v.TypeName())
	default:
		return errs.New(errs.TypeError,
			"Object of type %s is not JSON serializable", v.TypeName())
	}
	return nil
}

func writeJSONString(b *strings.Builder, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		default:
			// json.dumps defaults to ensure_ascii, so everything
			// outside ASCII is escaped, astral planes as surrogate
			// pairs.
			switch {
			case r < 0x20 || r > 0x7e:
				if r > 0xffff {
					r -= 0x10000
					fmt.Fprintf(b, `\u%04x\u%04x`,
						0xd800+(r>>10), 0xdc00+(r&0x3ff))
					continue
				}
				fmt.Fprintf(b, `\u%04x`, r)
			default:
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
}

// itemsAttributeError reports what `d.items()` does to a value that is not a
// mapping.
//
// Usually the attribute is simply missing. A Cycler is the exception: jinja2's
// keeps its rotation in an attribute named `items`, so the call finds a tuple
// and fails trying to call it -- which is the error a template author sees.
func itemsAttributeError(v value.Value) error {
	if attr, ok := lookupAttr(nil, v, "items"); ok {
		if _, callable := attr.Interface().(value.Caller); !callable {
			return errs.New(errs.TypeError, "'%s' object is not callable", attr.TypeName())
		}
	}
	if o, ok := v.Interface().(interface{ AttributeError(string) string }); ok {
		return errs.New(errs.AttributeError, "%s", o.AttributeError("items"))
	}
	return errs.New(errs.AttributeError,
		"'%s' object has no attribute 'items'", v.TypeName())
}

// unpackPair destructures one element into a key and a value, reporting the
// failure the way `k, v = item` does in Python.
func unpackPair(item value.Value) (value.Value, value.Value, error) {
	seq, err := value.Iterate(item)
	if err != nil {
		return value.Undefined, value.Undefined, errs.New(errs.TypeError,
			"cannot unpack non-iterable %s object", item.TypeName())
	}
	var items []value.Value
	for v := range seq {
		items = append(items, v)
		if len(items) > 2 {
			return value.Undefined, value.Undefined, errs.New(errs.ValueError,
				"too many values to unpack (expected 2)")
		}
	}
	if len(items) < 2 {
		return value.Undefined, value.Undefined, errs.New(errs.ValueError,
			"not enough values to unpack (expected 2, got %d)", len(items))
	}
	return items[0], items[1], nil
}

// jsonKeyable reports whether a dict key can be a JSON object name. json.dumps
// coerces the scalar types and refuses everything else.
func jsonKeyable(v value.Value) bool {
	switch v.Kind() {
	case value.KindString, value.KindInt, value.KindFloat, value.KindBool, value.KindNone:
		return true
	}
	return false
}
