// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package value_test

import "github.com/mgilbir/gojja2/value"

// dictOf is value.DictOf for a test whose keys are literals, where an error is
// a bug in the test. The tests that are *about* a refused key call DictOf
// directly.
func dictOf(kv ...value.Value) value.Value {
	d, err := value.DictOf(kv...)
	if err != nil {
		panic("value.DictOf: " + err.Error())
	}
	return d
}
