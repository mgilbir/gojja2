// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"fmt"
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

// urlizeTrailing are the characters stripped from the end of a detected link,
// so that a URL at the end of a sentence does not swallow the punctuation.
const urlizeTrailing = ".,:;!?)\"']}>"

// filterUrlize turns bare URLs and email addresses in text into links.
func filterUrlize(s *State, v value.Value, args *value.CallArgs) (value.Value, error) {
	trimLimit, err := intArg(args, 0, "trim_url_limit", 0)
	if err != nil {
		return value.Undefined, err
	}
	rel, _ := arg(args, 1, "rel")
	target, _ := arg(args, 2, "target")

	extra := map[string]bool{}
	if schemes, ok := arg(args, 3, "extra_schemes"); ok && !schemes.IsNone() {
		items, err := materialize(schemes)
		if err != nil {
			return value.Undefined, err
		}
		for _, item := range items {
			extra[value.Str(item)] = true
		}
	}

	var attrs strings.Builder
	if !rel.IsUndefined() && !rel.IsNone() && value.Str(rel) != "" {
		fmt.Fprintf(&attrs, ` rel="%s"`, escapeHTML(value.Str(rel)))
	}
	if !target.IsUndefined() && !target.IsNone() && value.Str(target) != "" {
		fmt.Fprintf(&attrs, ` target="%s"`, escapeHTML(value.Str(target)))
	}

	esc := func(text string) string {
		if s.autoescape {
			return escapeHTML(text)
		}
		return text
	}

	words := strings.Split(value.Str(v), " ")
	for i, word := range words {
		head, middle, tail := splitAffixes(word)
		link, label := linkFor(middle, extra, trimLimit)
		if link == "" {
			words[i] = esc(head) + esc(middle) + esc(tail)
			continue
		}
		words[i] = esc(head) +
			`<a href="` + escapeHTML(link) + `"` + attrs.String() + `>` +
			esc(label) + `</a>` + esc(tail)
	}

	out := strings.Join(words, " ")
	if s.autoescape {
		return value.Safe(out), nil
	}
	return value.String(out), nil
}

// splitAffixes peels leading openers and trailing punctuation off a word.
func splitAffixes(word string) (head, middle, tail string) {
	middle = word
	for len(middle) > 0 && strings.IndexByte("(<\"'", middle[0]) >= 0 {
		head += middle[:1]
		middle = middle[1:]
	}
	for len(middle) > 0 && strings.IndexByte(urlizeTrailing, middle[len(middle)-1]) >= 0 {
		tail = middle[len(middle)-1:] + tail
		middle = middle[:len(middle)-1]
	}
	return head, middle, tail
}

// linkFor decides whether a word is a link, returning its href and the text to
// show, which may be shortened.
func linkFor(word string, extra map[string]bool, trimLimit int) (href, label string) {
	shorten := func(text string) string {
		if trimLimit > 0 && value.StrLen(text) > trimLimit {
			head, _ := value.StrSlice(text, nil, &trimLimit, nil)
			return head + "..."
		}
		return text
	}

	switch {
	case strings.HasPrefix(word, "https://"), strings.HasPrefix(word, "http://"):
		return word, shorten(word)
	case strings.HasPrefix(word, "www."):
		return "https://" + word, shorten(word)
	case strings.Contains(word, "@") && !strings.Contains(word, ":") &&
		strings.Contains(word[strings.Index(word, "@"):], "."):
		return "mailto:" + word, word
	}
	for scheme := range extra {
		if strings.HasPrefix(word, scheme) {
			return word, shorten(word)
		}
	}
	return "", ""
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
		key := value.Str(e.Key)
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
