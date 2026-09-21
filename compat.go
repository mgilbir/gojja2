// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"fmt"

	"github.com/mgilbir/gojja2/value"
)

// PythonVersion is the CPython whose behaviour a render reproduces.
//
// jinja2 3.1.6 is one library, but it runs on an interpreter, and the
// interpreter decides what `{{ d[0:1] }}` raises, whether `{{ x|sort(reverse=
// none) }}` is an error, and how a division by zero is worded. Across CPython
// 3.11 to 3.14, 66 of gojja2's 2,241 corpus cases answer differently.
//
// See [WithPythonVersion]. The rules themselves are named and documented one
// by one in value/pyversion.go.
type PythonVersion = value.PythonVersion

// The interpreters gojja2 reproduces. [DefaultPythonVersion] is the one it is
// generated against, which is not necessarily the newest of them.
const (
	Python311 = value.Python311
	Python312 = value.Python312
	Python313 = value.Python313
	Python314 = value.Python314

	// DefaultPythonVersion is what an environment that does not choose
	// gets: the pinned interpreter, the one every committed table and
	// golden here was generated from.
	DefaultPythonVersion = value.DefaultPythonVersion
)

// WithPythonVersion chooses which CPython's behaviour to reproduce, for the
// places the interpreters disagree.
//
// The default is [DefaultPythonVersion]. Choose another to match a service
// already running on it:
//
//	env, err := gojja2.New(gojja2.WithPythonVersion(gojja2.Python311))
//
// What it changes is a closed list -- sixteen rules, each named in
// value/pyversion.go with the release that moved it and the corpus case that
// grades it. Everything outside that list is identical on every version, which
// is 97.1% of the corpus.
func WithPythonVersion(v PythonVersion) Option {
	return func(e *Environment) error {
		if !v.Known() {
			// The bounds are the oldest and newest gojja2 knows, not
			// the default: the default is the pin, and it need not be
			// the newest.
			return fmt.Errorf("gojja2: unknown PythonVersion %d; gojja2 reproduces %s to %s",
				int(v), value.Python311, value.Python314)
		}
		e.pyVersion = v
		return nil
	}
}

// PythonVersion returns the interpreter this environment reproduces.
func (e *Environment) PythonVersion() PythonVersion { return e.pyVersion }
