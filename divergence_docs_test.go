// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"context"
	"testing"

	"github.com/mgilbir/gojja2"
	"github.com/mgilbir/gojja2/errs"
)

// gojja2's side of the entries in docs/divergences.md that no corpus case or
// other test asserts. The page promises that each divergence is asserted by a
// test, so it cannot quietly turn into something else; each test below names
// the heading it pins, and CPython's answer is in the comment beside each case
// because the corpus cannot hold it (an address, a reference file that is not
// UTF-8) or does not yet.

// divCase is one template and what gojja2 answers: the output, or the class and
// the exact message of the error.
type divCase struct {
	src     string
	want    string    // the output, when msg is empty
	kind    errs.Kind // the error's class, when msg is not empty
	msg     string    // the error's message; empty when the render succeeds
	tpls    map[string]string
	options []gojja2.Option
}

func (c divCase) check(t *testing.T) {
	t.Helper()
	tpls := map[string]string{"main": c.src}
	for name, src := range c.tpls {
		tpls[name] = src
	}
	env, err := gojja2.New(append([]gojja2.Option{gojja2.WithLoader(gojja2.DictLoader(tpls))}, c.options...)...)
	if err != nil {
		t.Fatal(err)
	}
	tmpl, err := env.GetTemplate("main")
	var out string
	if err == nil {
		out, err = tmpl.RenderString(context.Background(), nil)
	}
	if c.msg == "" {
		if err != nil || out != c.want {
			t.Errorf("%s\n got  %q, %v\n want %q", c.src, out, err, c.want)
		}
		return
	}
	if err == nil {
		t.Errorf("%s\n got  %q\n want %s: %s", c.src, out, c.kind, c.msg)
		return
	}
	if k := errs.KindOf(err); k != c.kind || err.Error() != c.msg {
		t.Errorf("%s\n got  %s: %v\n want %s: %s", c.src, k, err, c.kind, c.msg)
	}
}

// Pins "Lazy sequence filters": batch and slice return lists here, where
// jinja2's return generators.
func TestDivergenceBatchAndSliceAreLists(t *testing.T) {
	for _, c := range []divCase{
		// CPython: "T" -- a generator is always true.
		{src: `{% if []|batch(2) %}T{% endif %}`, want: ``},
		// CPython: TypeError: object of type 'generator' has no len()
		{src: `{{ [1, 2]|batch(1)|length }}`, want: `2`},
		{src: `{{ [1, 2]|slice(2)|length }}`, want: `2`},
		// CPython: TypeError: 'generator' object is not reversible
		{src: `{{ [1, 2]|batch(1)|last }}`, want: `[2]`},
		{src: `{{ [1, 2]|slice(2)|last }}`, want: `[2]`},
		// CPython: TypeError: Object of type generator is not JSON serializable
		{src: `{{ [1, 2]|batch(1)|tojson }}`, want: `[[1], [2]]`},
		// CPython: <generator object do_batch at 0x...>
		{src: `{{ [1, 2]|batch(1) }}`, want: `[[1], [2]]`},
		// CPython: <generator object sync_do_slice at 0x...>; only
		// consuming it raises.
		{src: `{{ [1, 2]|slice(0) }}`, kind: errs.ZeroDivisionError, msg: `integer division or modulo by zero`},
		// Agreements: walking the result forwards, and slice's truth, since
		// slice(n) always yields n slices.
		{src: `{{ [1, 2, 3]|batch(2)|list }}`, want: `[[1, 2], [3]]`},
		{src: `{% if []|slice(2) %}T{% endif %}`, want: `T`},
	} {
		c.check(t)
	}
}

// Pins "Comparison order inside a long sort": up to 64 elements the failing
// pair is CPython's; above, it is whichever pair gojja2's own sort reached.
func TestDivergenceLongSortNamesItsOwnPair(t *testing.T) {
	for _, c := range []divCase{
		// 61 elements: CPython names 'str' and 'int' too.
		{src: `{{ (range(30)|list + ['a'] + range(30)|list)|sort }}`,
			kind: errs.TypeError, msg: `'<' not supported between instances of 'str' and 'int'`},
		// 201 elements: CPython names 'str' and 'int'.
		{src: `{{ (range(100)|list + ['a'] + range(100)|list)|sort }}`,
			kind: errs.TypeError, msg: `'<' not supported between instances of 'int' and 'str'`},
	} {
		c.check(t)
	}
}

// Pins "`is sameas` on two literals".
func TestDivergenceSameasOnTwoLiterals(t *testing.T) {
	for _, c := range []divCase{
		// CPython: False -- one literal sits in a marshalled tuple.
		{src: `{{ 1.5 is sameas(1.5) }}`, want: `True`},
		// CPython: False -- two receivers, each folded on its own.
		{src: `{{ (300).bit_length == (300).bit_length }}`, want: `True`},
		// Agreements: over variables, and on a cached small int.
		{src: `{% set a = 1.5 %}{% set b = 1.5 %}{{ a is sameas(b) }}`, want: `True`},
		{src: `{{ (1).bit_length == (1).bit_length }}`, want: `True`},
	} {
		c.check(t)
	}
}

// Pins "A macro containing a context-free include".
func TestDivergenceContextFreeIncludeInAMacro(t *testing.T) {
	// CPython: <generator object root.<locals>.macro at 0x...>
	divCase{
		src:  `{% macro m(x) %}{% include "other.txt" without context %}{% endmacro %}{{ m(1) }}`,
		tpls: map[string]string{"other.txt": "inc"},
		want: `inc`,
	}.check(t)
}

// Pins "Python object introspection": the attributes gojja2 does not carry.
func TestDivergenceIntrospectionGaps(t *testing.T) {
	for _, c := range []divCase{
		// CPython: "Built-in mutable sequence. ..."
		{src: `{{ [1].__doc__ }}`, want: ``},
		// CPython: (<class 'int'>, <class 'object'>)
		{src: `{{ (1).__class__.__mro__ }}`, want: ``},
		// CPython: &lt; and a. The attributes are undefined here, so
		// calling one raises.
		{src: `{{ ('a'|safe).escape('<') }}`, kind: errs.UndefinedError, msg: `'markupsafe.Markup object' has no attribute 'escape'`},
		{src: `{{ ('a'|safe).__html__() }}`, kind: errs.UndefinedError, msg: `'markupsafe.Markup object' has no attribute '__html__'`},
	} {
		c.check(t)
	}
}

// Pins "A callable fourth argument to `Undefined`".
func TestDivergenceCallableUndefinedExc(t *testing.T) {
	for _, c := range []divCase{
		// CPython: TypeError: exceptions must derive from BaseException
		{src: `{{ -nope.__class__(1, 2, 3, range) }}`, kind: errs.UndefinedError, msg: `1`},
		// CPython: TypeError: 'int' object is not iterable
		{src: `{{ -nope.__class__(1, 2, 3, dict) }}`, kind: errs.UndefinedError, msg: `1`},
		// Agreement: a fourth argument that cannot be called fails before
		// the message is used.
		{src: `{{ -nope.__class__(1, 2, 3, 5) }}`, kind: errs.TypeError, msg: `'int' object is not callable`},
	} {
		c.check(t)
	}
}

// Pins "The debug extension": jinja2.ext.debug is not offered, so asking for it
// is refused when the environment is built, and the tag does not compile.
func TestDivergenceDebugExtensionIsRefused(t *testing.T) {
	_, err := gojja2.New(gojja2.WithExtensions("debug"))
	if want := `unknown extension "debug": the known ones are do and loopcontrols`; err == nil || err.Error() != want {
		t.Errorf("WithExtensions(\"debug\"): got %v, want %q", err, want)
	}
	divCase{src: `{% debug %}`, kind: errs.TemplateSyntaxError, msg: `Encountered unknown tag 'debug'.`}.check(t)
}
