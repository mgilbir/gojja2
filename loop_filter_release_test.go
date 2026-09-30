// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"bytes"
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/mgilbir/gojja2"
)

// A `{% for %}` with an `if` pulls its items through a coroutine, and a coroutine
// is released only when the sequence runs out or someone stops it. A loop that
// ends early -- a `{% break %}`, or a body that raises -- used to leave one
// parked for the life of the process, so a server rendering such a template
// leaked one per request. This renders 3,000 and counts what is still alive.
func TestFilteredLoopLeftEarlyReleasesItsCoroutine(t *testing.T) {
	env, err := gojja2.New(gojja2.WithExtensions("loopcontrols"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, src string }{
		{"break", `{% for x in [1, 2, 3] if x > 0 %}{% break %}{% endfor %}`},
		{"error in the body", `{% for x in [1, 2, 3] if x > 0 %}{{ 1 / 0 }}{% endfor %}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmpl, err := env.FromString(tc.src)
			if err != nil {
				t.Fatal(err)
			}
			runtime.GC()
			before := runtime.NumGoroutine()
			const renders = 3000
			for range renders {
				var b bytes.Buffer
				_ = tmpl.Render(context.Background(), &b, nil)
			}
			// The release runs after a collection finds the source
			// unreachable, on a goroutine of its own, so give it a few
			// rounds rather than one.
			var after int
			for range 50 {
				runtime.GC()
				after = runtime.NumGoroutine()
				if after < before+renders/10 {
					return
				}
				time.Sleep(20 * time.Millisecond)
			}
			t.Errorf("%d renders left %d goroutines behind (%d before, %d after)",
				renders, after-before, before, after)
		})
	}
}
