// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"maps"
	"slices"
	"testing"
)

// TestEveryMethodHasASignature: the generated table in method_arity.go is the
// only thing that refuses a method call of the wrong shape, so a method missing
// from it has no arity check at all.
//
// It used not to be the only thing. Every one of these methods also checked its
// own arity, in its own wording, and `checkMethodArity` runs first -- so
// seventeen of those checks had become unreachable and were removed. Nothing
// noticed them going: the whole suite and a twelve-thousand-template soak
// produced none of them, which is how they were found in the first place. What
// makes removing them safe is this test rather than that measurement, because
// the measurement only covers the methods that exist today.
//
// tools/oracle/gen_methods.py reads these same maps, so a method added without
// re-running `make methods` is exactly the gap being closed here.
func TestEveryMethodHasASignature(t *testing.T) {
	tables := map[string][]string{
		"str":   slices.Sorted(maps.Keys(stringMethods)),
		"list":  slices.Sorted(maps.Keys(listMethods)),
		"dict":  slices.Sorted(maps.Keys(dictMethods)),
		"tuple": slices.Sorted(maps.Keys(tupleMethods)),
		"bytes": slices.Sorted(maps.Keys(registerBytesMethods())),
	}
	checked := 0
	for typ, names := range tables {
		for _, name := range names {
			key := typ + "." + name
			if _, ok := methodSignatures[key]; ok {
				checked++
				continue
			}
			if notCPythonMethods[key] {
				continue
			}
			t.Errorf("%s has no entry in methodSignatures, so nothing checks the "+
				"shape of a call to it. Run `make methods`, or list it in "+
				"notCPythonMethods if CPython has no such method.", key)
		}
	}
	if checked == 0 {
		t.Fatal("no method matched a signature; the test is reading the wrong tables")
	}
	t.Logf("%d methods, each with a signature the generated table carries", checked)
}

// notCPythonMethods are the methods this test accepts without a generated
// signature, with the reason each one is absent. gen_methods.py prints a
// "skipped" line for a method CPython's type does not have; list.sort is
// absent for a different reason and is checked by hand instead.
var notCPythonMethods = map[string]bool{
	// Registered in init() rather than in the map literal -- it calls back
	// into the evaluator, which is an initialisation cycle -- so the
	// generator, which reads the literal as text, has never seen it. Its
	// wording carries no count where the generator looks for one either.
	// methodListSort checks its own shape; own/errors/list_sort_* grade it.
	"list.sort": true,
}
