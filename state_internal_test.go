// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import "testing"

// A State whose environment was never given a newline sequence, or whose budget
// was never given a context, still answers what a template author expects. The
// exported constructors cannot build either -- WithNewlineSequence refuses "",
// and a render always has a context -- so the answers are pinned from inside
// the package; TestStateOutsideARender covers the nil and empty State.
func TestStateWithNoNewlineSequenceOrContext(t *testing.T) {
	s := &State{env: &Environment{}}
	if got := s.NewlineSequence(); got != "\n" {
		t.Errorf("NewlineSequence of an environment with none = %q, want \"\\n\"", got)
	}
	s = &State{budget: &budget{}}
	if ctx := s.Context(); ctx == nil || ctx.Err() != nil {
		t.Errorf("Context of a budget with none = %v, want context.Background()", ctx)
	}
}
