// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package syntax

import (
	"bytes"
	"sort"
)

// Index numbers every node of a tree in pre-order, following edges in order.
//
// This is what lets the scope and binding facts be compared between two
// implementations at all. The trees already encode to identical bytes, so the
// same walk visits the same nodes in the same order on both sides, and a
// position is a name for a node that neither side had to agree on separately.
func Index(root *Node) map[*Node]int {
	out := map[*Node]int{}
	n := 0
	Walk(root, func(node *Node, _ Role) bool {
		out[node] = n
		n++
		return true
	})
	return out
}

// CanonicalInfo writes a tree's scope and binding facts as the form both
// implementations are compared in.
//
// Nodes are named by their index, symbols by the order they are introduced --
// scope by scope in pre-order, then the names that come from outside the
// template, sorted. Both are derived from the tree rather than from anything
// either side chose, so there is nothing here for two encoders to disagree
// about except the facts themselves.
func CanonicalInfo(t *Tree) ([]byte, error) {
	idx := Index(t.Root)

	// Scopes in the order their nodes appear, and the symbols each owns in
	// the order they were declared.
	type scopeRow struct {
		node int
		syms []*Symbol
	}
	rows := make([]scopeRow, 0, len(t.Info.Scopes))
	for node, syms := range t.Info.Scopes {
		rows = append(rows, scopeRow{idx[node], syms})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].node < rows[j].node })

	id := map[*Symbol]int{}
	var order []*Symbol
	for _, r := range rows {
		for _, s := range r.syms {
			id[s] = len(order)
			order = append(order, s)
		}
	}
	outside := make([]*Symbol, 0, len(t.Info.Context))
	for _, s := range t.Info.Context {
		outside = append(outside, s)
	}
	sort.Slice(outside, func(i, j int) bool { return outside[i].Name < outside[j].Name })
	for _, s := range outside {
		id[s] = len(order)
		order = append(order, s)
	}

	var b bytes.Buffer
	b.WriteString(`{"symbols":[`)
	for i, s := range order {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"kind":`)
		writeJSON(&b, string(s.Kind))
		b.WriteString(`,"name":`)
		writeJSON(&b, s.Name)
		b.WriteString(`,"scope":`)
		if s.Scope == nil {
			b.WriteString("-1")
		} else {
			writeInt(&b, idx[s.Scope])
		}
		if s.Aliases != nil {
			b.WriteString(`,"aliases":`)
			writeInt(&b, id[s.Aliases])
		}
		b.WriteByte('}')
	}
	b.WriteString(`],"scopes":[`)
	for i, r := range rows {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('[')
		writeInt(&b, r.node)
		b.WriteString(",[")
		for j, s := range r.syms {
			if j > 0 {
				b.WriteByte(',')
			}
			writeInt(&b, id[s])
		}
		b.WriteString("]]")
	}
	b.WriteString(`],"defs":`)
	writePairs(&b, t.Info.Defs, idx, id)
	b.WriteString(`,"uses":`)
	writePairs(&b, t.Info.Uses, idx, id)
	b.WriteByte('}')
	return b.Bytes(), nil
}

func writePairs(b *bytes.Buffer, m map[*Node]*Symbol, idx map[*Node]int, id map[*Symbol]int) {
	pairs := make([][2]int, 0, len(m))
	for n, s := range m {
		pairs = append(pairs, [2]int{idx[n], id[s]})
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i][0] < pairs[j][0] })
	b.WriteByte('[')
	for i, p := range pairs {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('[')
		writeInt(b, p[0])
		b.WriteByte(',')
		writeInt(b, p[1])
		b.WriteByte(']')
	}
	b.WriteByte(']')
}

func writeInt(b *bytes.Buffer, n int) {
	raw, _ := marshal(n)
	b.Write(raw)
}
