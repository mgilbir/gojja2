// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package conformance

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mgilbir/gojja2"
	"github.com/mgilbir/gojja2/errs"
	"github.com/mgilbir/gojja2/value"
)

// Environment profiles.
//
// Some corpora are not written against a bare environment. LLM chat templates
// are written against the one transformers.apply_chat_template builds, which
// sets whitespace options, enables loopcontrols, overrides tojson and injects
// two globals. Rendering such a template under the default environment
// produces a divergence that says nothing about gojja2, so the environment
// travels with the case as `"__profile__": "transformers"`.
//
// This is the second implementation of each profile; the first is
// tools/oracle/profiles.py, and the two must agree exactly or every case using
// one is graded against the wrong answer. TestProfileMatchesOracle pins them
// together.

// ProfileTransformers is the environment transformers renders chat templates
// under. See tools/oracle/profiles.py for what it mirrors and what it leaves
// out.
const ProfileTransformers = "transformers"

// FrozenNow is the instant strftime_now reports, in place of the real clock.
// It must equal profiles.FROZEN_NOW on the Python side: a template that stamps
// the date would otherwise produce a golden that stops matching tomorrow.
var FrozenNow = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// applyProfile installs a profile's filters and globals on env. The name is
// validated by LoadCase, so an unknown one cannot reach here.
func applyProfile(env *gojja2.Environment, name string) {
	switch name {
	case ProfileTransformers:
		env.AddFilter("tojson", filterTransformersToJSON)
		env.AddGlobal("raise_exception", gojja2.Func("raise_exception",
			func(_ *gojja2.State, args *value.CallArgs) (value.Value, error) {
				msg, _ := args.Arg(0)
				return value.Undefined, errs.New(errs.TemplateError, "%s", value.Str(msg))
			}))
		env.AddGlobal("strftime_now", gojja2.Func("strftime_now",
			func(_ *gojja2.State, args *value.CallArgs) (value.Value, error) {
				format, _ := args.Arg(0)
				return value.String(strftime(FrozenNow, value.Str(format))), nil
			}))
	}
}

// profileSettings are the environment options a profile implies. A case's own
// __settings__ are applied on top, so a case can still override one.
func profileSettings(name string) (Settings, bool) {
	if name == ProfileTransformers {
		return Settings{
			TrimBlocks:   true,
			LstripBlocks: true,
			Extensions:   []string{"loopcontrols"},
		}, true
	}
	return Settings{}, false
}

// filterTransformersToJSON is transformers' tojson override.
//
// jinja2's own tojson sorts keys, escapes non-ASCII and escapes the characters
// that would close a <script> tag, because it exists to embed JSON in HTML.
// transformers replaces it with a plain json.dumps(ensure_ascii=False), so a
// tool-calling template emits readable JSON in the model's own key order. The
// difference shows up in almost every tool-calling template.
func filterTransformersToJSON(s *gojja2.State, v value.Value, args *value.CallArgs) (value.Value, error) {
	indent := 0
	if n, ok := args.Arg(0); ok {
		if i, ok := n.Int64(); ok {
			indent = int(i)
		}
	}
	if n, ok := args.Kwarg("indent"); ok {
		if i, ok := n.Int64(); ok {
			indent = int(i)
		}
	}
	opts := jsonOptions{itemSep: ", ", keySep: ": "}
	if b, ok := args.Kwarg("sort_keys"); ok {
		opts.sortKeys = b.AsBool()
	}
	if b, ok := args.Kwarg("ensure_ascii"); ok {
		opts.ensureASCII = b.AsBool()
	}
	// separators is a (item_separator, key_separator) pair, and giving it
	// overrides the spacing an indent would otherwise imply.
	if sep, ok := args.Kwarg("separators"); ok && !sep.IsNone() {
		s, isSeq := sep.Seq()
		if !isSeq || s.Len() != 2 {
			return value.Undefined, errs.New(errs.TypeError,
				"separators must be a pair of strings")
		}
		opts.itemSep, opts.keySep = value.Str(s.At(0)), value.Str(s.At(1))
		opts.explicitSep = true
	} else if indent > 0 {
		// json.dumps drops the space after the item separator once the
		// separator is a newline plus padding.
		opts.itemSep = ","
	}
	var b strings.Builder
	if err := writePlainJSON(&b, v, indent, 0, opts); err != nil {
		return value.Undefined, err
	}
	// A plain str, not Markup: the override does not mark its result safe,
	// so under autoescape the result is escaped like any other string.
	return value.String(b.String()), nil
}

// jsonOptions are the json.dumps keyword arguments the override exposes.
type jsonOptions struct {
	sortKeys    bool
	ensureASCII bool
	itemSep     string
	keySep      string
	explicitSep bool
}

func writePlainJSON(b *strings.Builder, v value.Value, indent, depth int, opts jsonOptions) error {
	// An indent moves the separator onto the next line; without one the
	// items run together with json.dumps' default ", ".
	nl, pad, padEnd := "", "", ""
	comma := opts.itemSep
	if indent > 0 {
		nl = "\n"
		pad = strings.Repeat(" ", indent*(depth+1))
		padEnd = strings.Repeat(" ", indent*depth)
		if !opts.explicitSep {
			comma = ","
		}
	}

	switch v.Kind() {
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
		switch {
		case f != f:
			b.WriteString("NaN")
		case f > 1e308:
			b.WriteString("Infinity")
		case f < -1e308:
			b.WriteString("-Infinity")
		default:
			b.WriteString(value.FormatFloat(f))
		}
	case value.KindString, value.KindBytes:
		writePlainJSONString(b, v.AsString(), opts.ensureASCII)
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
			if err := writePlainJSON(b, item, indent, depth+1, opts); err != nil {
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
		entries := append([]value.DictEntry(nil), d.Entries()...)
		// Insertion order unless the caller asked otherwise -- the whole
		// point of the override.
		if opts.sortKeys {
			sort.SliceStable(entries, func(i, j int) bool {
				return value.Str(entries[i].Key) < value.Str(entries[j].Key)
			})
		}
		b.WriteString("{" + nl)
		for i, e := range entries {
			if i > 0 {
				b.WriteString(comma + nl)
			}
			b.WriteString(pad)
			writePlainJSONString(b, value.Str(e.Key), opts.ensureASCII)
			b.WriteString(opts.keySep)
			if err := writePlainJSON(b, e.Value, indent, depth+1, opts); err != nil {
				return err
			}
		}
		b.WriteString(nl + padEnd + "}")
	case value.KindObject:
		if tv, ok := v.Interface().(value.TupleView); ok {
			return writePlainJSON(b, tv.AsTuple(), indent, depth, opts)
		}
		if m, ok := v.Interface().(value.Mapping); ok {
			out := value.NewDict()
			target, _ := out.Dict()
			for _, k := range m.Keys() {
				val, _ := m.GetItem(k)
				_ = target.Set(k, val)
			}
			return writePlainJSON(b, out, indent, depth, opts)
		}
		return errs.New(errs.TypeError,
			"Object of type %s is not JSON serializable", v.TypeName())
	default:
		return errs.New(errs.TypeError,
			"Object of type %s is not JSON serializable", v.TypeName())
	}
	return nil
}

// writePlainJSONString escapes with ensure_ascii=False: the characters JSON
// requires escaped, and nothing else. Non-ASCII is written through as UTF-8,
// which is the visible half of the override.
func writePlainJSONString(b *strings.Builder, s string, ensureASCII bool) {
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
			// ensure_ascii escapes everything above ASCII, astral
			// planes as surrogate pairs.
			if ensureASCII && r > 0x7e {
				if r > 0xffff {
					r -= 0x10000
					fmt.Fprintf(b, `\u%04x\u%04x`,
						0xd800+(r>>10), 0xdc00+(r&0x3ff))
					continue
				}
				fmt.Fprintf(b, `\u%04x`, r)
				continue
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
}

// strftime formats t with the C directives Python's datetime.strftime uses.
//
// Only the directives a chat template plausibly reaches are implemented; an
// unknown one is written through unchanged, which makes a gap visible as a
// divergence rather than hiding it behind a plausible-looking date.
func strftime(t time.Time, format string) string {
	var b strings.Builder
	for i := 0; i < len(format); i++ {
		if format[i] != '%' || i+1 >= len(format) {
			b.WriteByte(format[i])
			continue
		}
		i++
		switch format[i] {
		case 'Y':
			fmt.Fprintf(&b, "%04d", t.Year())
		case 'y':
			fmt.Fprintf(&b, "%02d", t.Year()%100)
		case 'm':
			fmt.Fprintf(&b, "%02d", int(t.Month()))
		case 'd':
			fmt.Fprintf(&b, "%02d", t.Day())
		case 'H':
			fmt.Fprintf(&b, "%02d", t.Hour())
		case 'I':
			h := t.Hour() % 12
			if h == 0 {
				h = 12
			}
			fmt.Fprintf(&b, "%02d", h)
		case 'M':
			fmt.Fprintf(&b, "%02d", t.Minute())
		case 'S':
			fmt.Fprintf(&b, "%02d", t.Second())
		case 'j':
			fmt.Fprintf(&b, "%03d", t.YearDay())
		case 'B':
			b.WriteString(t.Month().String())
		case 'b':
			b.WriteString(t.Month().String()[:3])
		case 'A':
			b.WriteString(t.Weekday().String())
		case 'a':
			b.WriteString(t.Weekday().String()[:3])
		case 'p':
			if t.Hour() < 12 {
				b.WriteString("AM")
			} else {
				b.WriteString("PM")
			}
		case 'Z':
			zone, _ := t.Zone()
			b.WriteString(zone)
		case '%':
			b.WriteByte('%')
		default:
			b.WriteByte('%')
			b.WriteByte(format[i])
		}
	}
	return b.String()
}
