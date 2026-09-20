// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import "github.com/mgilbir/gojja2"

// mustEnv is gojja2.New for a test whose options are written out in the test
// itself, where a configuration error is a bug in the test rather than
// something to handle. Tests that are *about* a rejected configuration call
// gojja2.New directly and inspect the error.
func mustEnv(opts ...gojja2.Option) *gojja2.Environment {
	env, err := gojja2.New(opts...)
	if err != nil {
		panic("gojja2: test environment: " + err.Error())
	}
	return env
}
