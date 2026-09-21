// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package syntax

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

// Canonical writes a node as the form both trees are compared in.
//
// The encoding exists so that "gojja2 and jinja2 see the same template" can be
// checked rather than believed. tools/oracle/syntax_emit.py writes jinja2's own
// tree in exactly this form, and the corpus is graded by diffing the bytes --
// which is a stronger statement than agreeing about the answer to one question,
// because it holds for every question either of them will ever be asked.
//
// It is therefore a contract, and its details are load-bearing:
//
//   - Attributes are sorted by key, so two encoders cannot differ by map order.
//   - Line numbers are excluded. Two parsers can agree entirely about what a
//     template says and still disagree about which line a construct starts on.
//   - Edges keep their order, because a template's order is its meaning.
//   - HTML escaping is off. encoding/json writes `<` as `\u003c` by default,
//     which is a precaution for embedding JSON in a page and has nothing to do
//     with what a template says.
//   - Numbers are written by encoding/json's rules, and the Python side is
//     required to match them rather than the other way round.
func Canonical(n *Node) ([]byte, error) {
	var b bytes.Buffer
	if err := encode(&b, n); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func encode(b *bytes.Buffer, n *Node) error {
	if n == nil {
		b.WriteString("null")
		return nil
	}
	b.WriteString(`{"k":`)
	writeJSON(b, string(n.Kind))
	if len(n.Attrs) > 0 {
		keys := make([]string, 0, len(n.Attrs))
		for k := range n.Attrs {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteString(`,"a":{`)
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			writeJSON(b, k)
			b.WriteByte(':')
			raw, err := marshal(n.Attrs[k])
			if err != nil {
				return fmt.Errorf("syntax: attribute %q of %s: %w", k, n.Kind, err)
			}
			b.Write(raw)
		}
		b.WriteByte('}')
	}
	if len(n.Edges) > 0 {
		b.WriteString(`,"e":[`)
		for i, e := range n.Edges {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteByte('[')
			writeJSON(b, string(e.Role))
			b.WriteByte(',')
			if err := encode(b, e.Node); err != nil {
				return err
			}
			b.WriteByte(']')
		}
		b.WriteByte(']')
	}
	b.WriteByte('}')
	return nil
}

func writeJSON(b *bytes.Buffer, s string) {
	raw, _ := marshal(s)
	b.Write(raw)
}

// marshal is json.Marshal without the HTML escaping, which would otherwise
// write `<` as `\u003c` and make an identical template look different.
func marshal(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}
