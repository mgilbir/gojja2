// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

// mustNew is New for a test whose options are written out in the test itself,
// where a configuration error is a bug in the test rather than something to
// handle. Tests that are *about* a rejected configuration call New directly
// and inspect the error.
func mustNew(opts ...Option) *Environment {
	env, err := New(opts...)
	if err != nil {
		panic("gojja2: test environment: " + err.Error())
	}
	return env
}
