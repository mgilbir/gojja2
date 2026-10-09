// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"context"
	"errors"
	"testing"
)

// TestPollWorkReadsTheContextForABatch: a render reads its context once every
// checkInterval units of work. A poll that stands for a batch of that much work
// is a read; a poll that stands for one unit is not.
func TestPollWorkReadsTheContextForABatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	env := mustNew()
	st := &State{env: env, budget: newBudget(ctx, env)}
	if err := st.PollWork(1); err != nil {
		t.Fatalf("one unit read the context: %v", err)
	}
	if err := st.PollWork(checkInterval); !errors.Is(err, context.Canceled) {
		t.Errorf("a batch of %d units: %v, want the cancelled context", checkInterval, err)
	}
}
