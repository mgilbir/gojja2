// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/mgilbir/gojja2/errs"
	"github.com/mgilbir/gojja2/value"
)

// filterURLEncode percent-encodes a string, or builds a query string from a
// mapping or a sequence of pairs.
func filterURLEncode(_ *State, v value.Value, _ *value.CallArgs) (value.Value, error) {
	if d, ok := v.Dict(); ok {
		parts := make([]string, 0, d.Len())
		for _, e := range d.Entries() {
			parts = append(parts, quoteURL(value.Str(e.Key), false)+"="+
				quoteURL(value.Str(e.Value), false))
		}
		return value.String(strings.Join(parts, "&")), nil
	}
	if v.Kind() == value.KindList || v.Kind() == value.KindTuple {
		s, _ := v.Seq()
		parts := make([]string, 0, s.Len())
		for _, item := range s.Items() {
			pair, ok := item.Seq()
			if !ok || pair.Len() != 2 {
				return value.Undefined, errs.New(errs.TypeError,
					"urlencode expects a mapping or a sequence of pairs")
			}
			parts = append(parts, quoteURL(value.Str(pair.At(0)), false)+"="+
				quoteURL(value.Str(pair.At(1)), false))
		}
		return value.String(strings.Join(parts, "&")), nil
	}
	// A bare string keeps "/" unescaped, matching urllib.parse.quote.
	return value.String(quoteURL(value.Str(v), true)), nil
}

// quoteURL percent-encodes everything outside the unreserved set. keepSlash
// mirrors urllib.parse.quote's default safe="/".
func quoteURL(s string, keepSlash bool) string {
	const unreserved = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_.-~"
	var b strings.Builder
	for i := range len(s) {
		c := s[i]
		switch {
		case strings.IndexByte(unreserved, c) >= 0, keepSlash && c == '/':
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteString(strings.ToUpper(strconv.FormatUint(uint64(c), 16)))
			if c < 0x10 {
				// Two digits always; the leading zero was lost.
				text := b.String()
				b.Reset()
				b.WriteString(text[:len(text)-1] + "0" + text[len(text)-1:])
			}
		}
	}
	return b.String()
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
		items, err := materialize(schemes)
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
		return value.Undefined, errs.New(errs.TypeError,
			"xmlattr expects a mapping, not %s", v.TypeName())
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
	indent, err := intArg(args, 0, "indent", 0)
	if err != nil {
		return value.Undefined, err
	}
	if v.IsUndefined() {
		return value.Undefined, errs.New(errs.TypeError,
			"Object of type %s is not JSON serializable", v.TypeName())
	}
	var b strings.Builder
	if err := writeJSON(&b, v, indent, 0); err != nil {
		return value.Undefined, err
	}
	out := htmlSafeJSON(b.String())
	if s.autoescape {
		return value.Safe(out), nil
	}
	return value.String(out), nil
}

var jsonHTMLEscaper = strings.NewReplacer(
	"<", `<`,
	">", `>`,
	"&", `&`,
	"'", `'`,
)

func htmlSafeJSON(s string) string { return jsonHTMLEscaper.Replace(s) }

func writeJSON(b *strings.Builder, v value.Value, indent, depth int) error {
	// json.dumps separates with ", " until an indent is given, at which
	// point the space moves onto the next line.
	nl, pad, padEnd, comma := "", "", "", ", "
	if indent > 0 {
		nl = "\n"
		pad = strings.Repeat(" ", indent*(depth+1))
		padEnd = strings.Repeat(" ", indent*depth)
		comma = ","
	}

	switch v.Kind() {
	case value.KindUndefined, value.KindNone:
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
		if f != f || f > 1e308 || f < -1e308 {
			// Python's json emits NaN and Infinity bare, which is
			// not valid JSON but is what jinja2 produces.
			b.WriteString(map[bool]string{true: "NaN", false: "Infinity"}[f != f])
			if f < 0 {
				b.Reset()
				b.WriteString("-Infinity")
			}
			return nil
		}
		b.WriteString(value.FormatFloat(f))
	case value.KindString, value.KindBytes:
		writeJSONString(b, v.AsString())
	case value.KindList, value.KindTuple:
		s, _ := v.Seq()
		if s.Len() == 0 {
			b.WriteString("[]")
			return nil
		}
		b.WriteString("[" + nl)
		for i, item := range s.Items() {
			if i > 0 {
				b.WriteString(comma + nl)
			}
			b.WriteString(pad)
			if err := writeJSON(b, item, indent, depth+1); err != nil {
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
			writeJSONString(b, value.Str(e.Key))
			b.WriteString(": ")
			if err := writeJSON(b, e.Value, indent, depth+1); err != nil {
				return err
			}
		}
		b.WriteString(nl + padEnd + "}")
	case value.KindObject:
		if m, ok := v.Interface().(value.Mapping); ok {
			out := value.NewDict()
			target, _ := out.Dict()
			for _, k := range m.Keys() {
				val, _ := m.GetItem(k)
				_ = target.Set(k, val)
			}
			return writeJSON(b, out, indent, depth)
		}
		if seq, ok := v.Interface().(value.Sequence); ok {
			items := make([]value.Value, seq.Len())
			for i := range items {
				items[i], _ = seq.GetIndex(i)
			}
			return writeJSON(b, value.NewList(items...), indent, depth)
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
			if r < 0x20 {
				fmt.Fprintf(b, `\u%04x`, r)
				continue
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
}
