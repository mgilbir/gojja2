// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mgilbir/gojja2"
)

// A constructor reached through `__class__` sizes its result from an argument
// the template chose, and there is no {% for %} anywhere for runLoop to see.
//
// `{{ b.__class__(500000000) }}` is half a gigabyte of zeroes in one
// expression, and `{{ lst.__class__(range(5000000)) }}` is five million
// elements; both are ordinary Python. So each of these has to be charged where
// it is built, and each test below names *which* bound must stop it -- an
// earlier version of this passed while bytes(iterable) walked uncharged, and
// only stopped because element 256 was out of range. Stopping for the wrong
// reason looks exactly like stopping for the right one.
func TestConstructionIsCharged(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"bytes from a count", "{{ b.__class__(500000000)|length }}", "bytes"},
		{"bytes from a walk", "{{ b.__class__(range(5000000)|map('int')|list) }}", "iterations"},
		{"list from a range", "{{ lst.__class__(range(5000000))|length }}", "iterations"},
		{"tuple from a range", "{{ tup.__class__(range(5000000))|length }}", "iterations"},
		{"str from long bytes", "{{ b.__class__(500000)|length }}", "bytes"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env, err := gojja2.New(
				gojja2.WithMaxOutputBytes(1<<16),
				gojja2.WithMaxIterations(1000))
			if err != nil {
				t.Fatal(err)
			}
			tpl, err := env.FromString(c.src)
			if err != nil {
				t.Fatal(err)
			}
			ctx := map[string]any{
				"b": []byte("ab"), "lst": []any{1}, "tup": []any{1},
			}
			out, err := tpl.RenderString(context.Background(), ctx)
			if err == nil {
				t.Fatalf("no budget error; rendered %q", out)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("stopped for the wrong reason, wanted %q: %v", c.want, err)
			}
		})
	}
}
