// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// Output buffers are pooled between renders, so the thing to prove is that
// nothing travels from one render to the next through a reused buffer.

// failWriter accepts n bytes and then refuses, so a render fails with output
// still sitting in the buffer -- the state a pooled buffer must not carry.
type failWriter struct {
	n    int
	seen strings.Builder
}

func (f *failWriter) Write(p []byte) (int, error) {
	if f.n <= 0 {
		return 0, errors.New("writer closed")
	}
	if len(p) > f.n {
		p = p[:f.n]
	}
	f.n -= len(p)
	f.seen.Write(p)
	return len(p), nil
}

func TestAFailedRenderLeavesNothingInThePool(t *testing.T) {
	env := mustEnv()
	tmpl, err := env.FromString(strings.Repeat("SECRET", 200))
	if err != nil {
		t.Fatal(err)
	}
	// This render cannot write everything, so it fails part-written.
	fw := &failWriter{n: 10}
	if err := tmpl.Render(context.Background(), fw, nil); err == nil {
		t.Fatal("expected the write to fail")
	}
	// Whatever buffer that render used is now back in the pool. A later
	// render must produce exactly its own output.
	clean, err := env.FromString("clean")
	if err != nil {
		t.Fatal(err)
	}
	for range 50 {
		got, err := clean.RenderString(context.Background(), nil)
		if err != nil {
			t.Fatalf("render: %v", err)
		}
		if got != "clean" {
			t.Fatalf("got %q, want %q -- a pooled buffer carried output over", got, "clean")
		}
	}
}

// A partly-written render still delivers what it produced, which is what the
// flush-either-way is for; pooling must not change that.
func TestAFailedRenderStillDeliversWhatItWrote(t *testing.T) {
	tmpl, err := mustEnv().FromString(strings.Repeat("ab", 4000))
	if err != nil {
		t.Fatal(err)
	}
	fw := &failWriter{n: 25}
	if err := tmpl.Render(context.Background(), fw, nil); err == nil {
		t.Fatal("expected the write to fail")
	}
	if got := fw.seen.String(); got != strings.Repeat("ab", 4000)[:25] {
		t.Errorf("delivered %q, want the first 25 bytes", got)
	}
}

// Concurrent renders must each get their own buffer.
func TestPooledWritersDoNotCrossBetweenRenders(t *testing.T) {
	env := mustEnv()
	tmpl, err := env.FromString("{{ n }}:" + strings.Repeat("x", 300))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan string, 64)
	for i := range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			want := fmt.Sprintf("%d:%s", i, strings.Repeat("x", 300))
			for range 20 {
				got, err := tmpl.RenderString(context.Background(), map[string]any{"n": i})
				if err != nil {
					errs <- err.Error()
					return
				}
				if got != want {
					errs <- fmt.Sprintf("got %q, want %q", got[:20], want[:20])
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}
