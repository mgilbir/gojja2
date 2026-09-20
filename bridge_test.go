// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mgilbir/gojja2/value"
)

// ptrAccount carries its methods on the pointer, which is the dominant Go
// idiom and the receiver style that used to be invisible to templates.
type ptrAccount struct {
	Name string `json:"name"`
}

func (a *ptrAccount) Label() string           { return "ptr:" + a.Name }
func (a *ptrAccount) Rename(to string) string { a.Name = to; return a.Name }
func (a *ptrAccount) Boom() string            { panic("method exploded") }

// valAccount carries its methods on the value.
type valAccount struct {
	Name string `json:"name"`
}

func (a valAccount) Label() string           { return "val:" + a.Name }
func (a valAccount) Rename(to string) string { return "renamed:" + to }

func renderVars(t *testing.T, env *Environment, src string, vars map[string]any) (string, error) {
	t.Helper()
	tmpl, err := env.FromString(src)
	if err != nil {
		return "", err
	}
	return tmpl.RenderString(context.Background(), vars)
}

// TestCyclicContextTerminates is the one that used to take the process out.
// A self-referential map was converted eagerly, with no cycle detection, before
// the render budget existed -- so no limit and no context could stop it.
func TestCyclicContextTerminates(t *testing.T) {
	m := map[string]any{"k": "v"}
	m["self"] = m

	env := mustNew()
	got, err := renderVars(t, env, `{{ m.k }}/{{ m.self.k }}/{{ m.self.self.self.k }}`,
		map[string]any{"m": m})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if got != "v/v/v" {
		t.Errorf("got %q, want %q", got, "v/v/v")
	}
}

// TestCyclicSliceTerminates covers the other eager container.
func TestCyclicSliceTerminates(t *testing.T) {
	// A slice cannot hold itself directly, so the cycle goes through a map.
	m := map[string]any{"n": 1}
	list := []any{m}
	m["list"] = list

	got, err := renderVars(t, mustNew(), `{{ m.n }}{{ m.list[0].n }}`, map[string]any{"m": m})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if got != "11" {
		t.Errorf("got %q, want %q", got, "11")
	}
}

// TestCyclicReprMarksRecursion pins CPython's own rendering of a container
// that contains itself.
func TestCyclicReprMarksRecursion(t *testing.T) {
	m := map[string]any{"k": "v"}
	m["self"] = m
	got, err := renderVars(t, mustNew(), `{{ m }}`, map[string]any{"m": m})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if got != "{'k': 'v', 'self': {...}}" {
		t.Errorf("got %q, want %q", got, "{'k': 'v', 'self': {...}}")
	}
}

// TestNonCyclicSharingIsStillExpanded guards the recursion marker against
// over-reach: CPython marks only a container on the active path, so two
// references to one non-cyclic dict are both printed in full.
func TestNonCyclicSharingIsStillExpanded(t *testing.T) {
	shared := map[string]any{"x": 1}
	outer := map[string]any{"p": shared, "q": shared}
	got, err := renderVars(t, mustNew(), `{{ m }}`, map[string]any{"m": outer})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if got != "{'p': {'x': 1}, 'q': {'x': 1}}" {
		t.Errorf("got %q, want %q", got, "{'p': {'x': 1}, 'q': {'x': 1}}")
	}
}

// TestCyclicToJSONIsRefused pins json.dumps's own refusal.
func TestCyclicToJSONIsRefused(t *testing.T) {
	m := map[string]any{"k": "v"}
	m["self"] = m
	_, err := renderVars(t, mustNew(), `{{ m|tojson }}`, map[string]any{"m": m})
	if err == nil {
		t.Fatal("expected a circular-reference error, got none")
	}
	if !strings.Contains(err.Error(), "Circular reference detected") {
		t.Errorf("got %q, want CPython's wording", err)
	}
}

// TestCyclicPPrintTerminates only needs to not hang; the marker carries an
// address, which differs between runs in CPython too.
func TestCyclicPPrintTerminates(t *testing.T) {
	m := map[string]any{"padding": strings.Repeat("x", 200)}
	m["self"] = m
	if _, err := renderVars(t, mustNew(), `{{ m|pprint }}`, map[string]any{"m": m}); err != nil {
		t.Fatalf("render: %v", err)
	}
}

// TestCyclicComparisonRaises pins that comparing two distinct cyclic
// structures reports CPython's RecursionError instead of overflowing the Go
// stack, which cannot be recovered.
func TestCyclicComparisonRaises(t *testing.T) {
	a := map[string]any{"s": nil}
	a["s"] = a
	b := map[string]any{"s": nil}
	b["s"] = b
	_, err := renderVars(t, mustNew(), `{{ a == b }}`, map[string]any{"a": a, "b": b})
	if err == nil {
		t.Fatal("expected a RecursionError, got none")
	}
	if !strings.Contains(err.Error(), "maximum recursion depth exceeded") {
		t.Errorf("got %q, want CPython's recursion wording", err)
	}
}

// TestSelfComparisonTerminates pins Python's identity shortcut: a structure
// always equals itself, cyclic or not.
func TestSelfComparisonTerminates(t *testing.T) {
	a := map[string]any{"s": nil}
	a["s"] = a
	got, err := renderVars(t, mustNew(), `{{ a == a }}`, map[string]any{"a": a})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if got != "True" {
		t.Errorf("got %q, want True", got)
	}
}

// TestPointerReceiverMethodsAreReachable pins the documented feature for the
// receiver style Go code actually uses. Dereferencing the pointer before
// wrapping the struct discarded its method set entirely.
func TestPointerReceiverMethodsAreReachable(t *testing.T) {
	env := mustNew()
	got, err := renderVars(t, env, `{{ a.Label() }}`, map[string]any{"a": &ptrAccount{Name: "x"}})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if got != "ptr:x" {
		t.Errorf("pointer-receiver method: got %q, want %q", got, "ptr:x")
	}
	// Fields still resolve through the pointer.
	if got := mustRenderVars(t, env, `{{ a.name }}`, map[string]any{"a": &ptrAccount{Name: "y"}}); got != "y" {
		t.Errorf("field through pointer: got %q, want %q", got, "y")
	}
	// And a value receiver keeps working.
	if got := mustRenderVars(t, env, `{{ a.Label() }}`, map[string]any{"a": valAccount{Name: "z"}}); got != "val:z" {
		t.Errorf("value-receiver method: got %q, want %q", got, "val:z")
	}
}

// TestMethodsWithArgumentsAreNotExposedByDefault pins the default policy. A
// template choosing the arguments a host method is called with is a decision
// the host makes, not one reflection makes for it.
func TestMethodsWithArgumentsAreNotExposedByDefault(t *testing.T) {
	for _, vars := range []map[string]any{
		{"a": &ptrAccount{Name: "x"}},
		{"a": valAccount{Name: "x"}},
	} {
		out, err := renderVars(t, mustNew(), `{{ a.Rename("hacked") }}`, vars)
		if err == nil {
			t.Errorf("a method taking arguments should not be callable, got %q", out)
		}
	}
	// The receiver must be genuinely unchanged, not merely unrendered.
	acct := &ptrAccount{Name: "original"}
	_, _ = renderVars(t, mustNew(), `{{ a.Rename("hacked") }}`, map[string]any{"a": acct})
	if acct.Name != "original" {
		t.Errorf("the method ran anyway: name is now %q", acct.Name)
	}
}

// TestWithMethodPolicyWidensExposure pins the explicit opt-in.
func TestWithMethodPolicyWidensExposure(t *testing.T) {
	env := mustNew(WithMethodPolicy(value.AllMethods))
	got, err := renderVars(t, env, `{{ a.Rename("new") }}`, map[string]any{"a": valAccount{Name: "old"}})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if got != "renamed:new" {
		t.Errorf("got %q, want %q", got, "renamed:new")
	}
}

// TestPanickingMethodBecomesAnError pins that host code reached from a
// template cannot unwind the caller's goroutine.
func TestPanickingMethodBecomesAnError(t *testing.T) {
	out, err := renderVars(t, mustNew(), `{{ a.Boom() }}`, map[string]any{"a": &ptrAccount{Name: "x"}})
	if err == nil {
		t.Fatalf("expected an error from a panicking method, got %q", out)
	}
	if !strings.Contains(err.Error(), "panicked") {
		t.Errorf("got %q, want it to name the panic", err)
	}
}

func mustRenderVars(t *testing.T, env *Environment, src string, vars map[string]any) string {
	t.Helper()
	out, err := renderVars(t, env, src, vars)
	if err != nil {
		t.Fatalf("render %q: %v", src, err)
	}
	return out
}

// hostErrors exercises the Go shapes a host method can have. A method whose
// only result is an error is how a validating accessor is written, and it was
// the one shape whose failure did not reach the template.
type hostErrors struct{ Name string }

func (h hostErrors) Greet() string              { return "hi " + h.Name }
func (h hostErrors) Fail() error                { return errors.New("boom") }
func (h hostErrors) Fine() error                { return nil }
func (h hostErrors) Pair() (string, error)      { return "", errors.New("kaboom") }
func (h hostErrors) PairOK() (string, error)    { return "ok", nil }
func (h hostErrors) Nothing()                   {}
func (h hostErrors) Two() (string, string)      { return "a", "b" }
func (h hostErrors) NotAnError() (string, bool) { return "v", false }

// TestHostMethodErrorReachesTheTemplate pins that a method's trailing error is
// surfaced whatever its arity.
//
// The check used to run only for two results or more, so a method returning
// just an error had its failure reflected into an object and rendered as
// "<errors.errorString object>" -- with the render reporting success.
func TestHostMethodErrorReachesTheTemplate(t *testing.T) {
	env := mustNew()
	h := map[string]any{"h": hostErrors{"bob"}}

	for _, tc := range []struct {
		src     string
		want    string
		wantErr string
	}{
		{`{{ h.Greet() }}`, "hi bob", ""},
		{`{{ h.Fail() }}`, "", "boom"},
		{`{{ h.Pair() }}`, "", "kaboom"},
		{`{{ h.PairOK() }}`, "ok", ""},
		// A nil error is not a failure, and renders as None does.
		{`{{ h.Fine() }}`, "None", ""},
		{`{{ h.Nothing() }}`, "None", ""},
		// A trailing result that is not an error is left alone.
		{`{{ h.Two() }}`, "a", ""},
		{`{{ h.NotAnError() }}`, "v", ""},
	} {
		got, err := renderVars(t, env, tc.src, h)
		switch {
		case tc.wantErr == "" && err != nil:
			t.Errorf("%s: %v", tc.src, err)
		case tc.wantErr != "" && err == nil:
			t.Errorf("%s rendered %q, want the error %q", tc.src, got, tc.wantErr)
		case tc.wantErr != "" && err.Error() != tc.wantErr:
			t.Errorf("%s: got %q, want %q", tc.src, err, tc.wantErr)
		case tc.wantErr == "" && got != tc.want:
			t.Errorf("%s = %q, want %q", tc.src, got, tc.want)
		}
	}
}
