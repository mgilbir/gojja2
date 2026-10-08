// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/mgilbir/gojja2"
	"github.com/mgilbir/gojja2/errs"
	"github.com/mgilbir/gojja2/value"
)

// TestCompileExpression holds CompileExpression to CPython jinja2 3.1.6's
// Environment.compile_expression. Every expectation below is what the oracle
// answered for the same source and keyword arguments -- the value's type and
// repr, or the error's class, message and line -- under the default Undefined
// unless strict is set, and with undefined_to_none=False where keep is.
func TestCompileExpression(t *testing.T) {
	for _, tc := range []struct {
		src, vars    string
		strict, keep bool
		typ, repr    string
		compileErr   string
		line         int
		evalKind     errs.Kind
		evalErr      string
	}{
		{src: "foo == 42", vars: "{\"foo\":23}", typ: "bool", repr: "False"},
		{src: "foo == 42", vars: "{\"foo\":42}", typ: "bool", repr: "True"},
		{src: "var", vars: "{}", typ: "NoneType", repr: "None"},
		{src: "var", vars: "{}", keep: true, typ: "Undefined", repr: "Undefined"},
		{src: "var", vars: "{}", strict: true, typ: "NoneType", repr: "None"},
		{src: "var", vars: "{}", strict: true, keep: true, typ: "StrictUndefined", repr: "Undefined"},
		{src: "1 + 2", vars: "{}", typ: "int", repr: "3"},
		{src: "a, b", vars: "{}", compileErr: "chunk after expression", line: 1},
		{src: "a b", vars: "{}", compileErr: "chunk after expression", line: 1},
		{src: "a }} b", vars: "{}", compileErr: "chunk after expression", line: 1},
		{src: "", vars: "{}", compileErr: "unexpected 'end of template'", line: 1},
		{src: "1/0", vars: "{}", evalKind: errs.ZeroDivisionError, evalErr: "division by zero"},
		{src: "x|upper", vars: "{\"x\":\"a\"}", typ: "str", repr: "'A'"},
		{src: "\"<\"|e", vars: "{}", typ: "Markup", repr: "Markup('&lt;')"},
		{src: "nope.attr", vars: "{}", evalKind: errs.UndefinedError, evalErr: "'nope' is undefined"},
		{src: "a\n+\n", vars: "{}", compileErr: "unexpected 'end of template'", line: 2},
		{src: "range(3)", vars: "{}", typ: "range", repr: "range(0, 3)"},
		{src: "{{ a }}", vars: "{\"a\":1}", compileErr: "expected token ':', got '}'", line: 1},
		{src: "a if b", vars: "{\"a\":1}", typ: "NoneType", repr: "None"},
		{src: "a if b", vars: "{\"a\":1}", keep: true, typ: "Undefined", repr: "Undefined"},
		{src: "(a, b)", vars: "{\"a\":1,\"b\":2}", typ: "tuple", repr: "(1, 2)"},
		{src: "result", vars: "{\"result\":5}", typ: "int", repr: "5"},
		{src: "a }}", vars: "{\"a\":1}", compileErr: "chunk after expression", line: 1},
		{src: "a %}", vars: "{\"a\":1}", compileErr: "unexpected '}'", line: 1},
		{src: "a -}}", vars: "{\"a\":1}", compileErr: "chunk after expression", line: 1},
		{src: "x.y", vars: "{\"x\":{\"y\":[1]}}", typ: "list", repr: "[1]"},
		{src: "[1][5]", vars: "{}", typ: "NoneType", repr: "None"},
		{src: "a +", vars: "{}", compileErr: "unexpected 'end of template'", line: 1},
		{src: "a ~ b", vars: "{\"a\":1,\"b\":\"x\"}", typ: "str", repr: "'1x'"},
		{src: "loop", vars: "{}", typ: "NoneType", repr: "None"},
		{src: "self", vars: "{}", typ: "TemplateReference", repr: "<TemplateReference None>"},
		{src: "range", vars: "{}", typ: "type", repr: "<class 'range'>"},
		{src: "nope", vars: "{\"nope\":null}", keep: true, typ: "NoneType", repr: "None"},
		{src: "a.b", vars: "{\"a\":{}}", keep: true, typ: "Undefined", repr: "Undefined"},
	} {
		var opts []gojja2.Option
		if tc.strict {
			opts = append(opts, gojja2.WithUndefined(value.UndefinedStrict))
		}
		x, err := mustEnv(opts...).CompileExpression(tc.src)
		if tc.compileErr != "" {
			var e *errs.Error
			if !errors.As(err, &e) || e.Kind != errs.TemplateSyntaxError || e.Msg != tc.compileErr || e.Line != tc.line {
				t.Errorf("%q: compile error %v; want TemplateSyntaxError %q on line %d", tc.src, err, tc.compileErr, tc.line)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: compile: %v", tc.src, err)
			continue
		}
		if tc.keep {
			x = x.KeepUndefined()
		}
		dec := json.NewDecoder(strings.NewReader(tc.vars))
		dec.UseNumber()
		var raw map[string]any
		if err := dec.Decode(&raw); err != nil {
			t.Fatal(err)
		}
		v, err := x.Eval(context.Background(), jsonInts(raw).(map[string]any))
		if tc.evalErr != "" {
			var e *errs.Error
			if !errors.As(err, &e) || e.Kind != tc.evalKind || e.Msg != tc.evalErr {
				t.Errorf("%q: eval error %v; want %s %q", tc.src, err, tc.evalKind, tc.evalErr)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: eval: %v", tc.src, err)
			continue
		}
		if got := value.Repr(v); v.TypeName() != tc.typ || got != tc.repr {
			t.Errorf("%q = %s %s; want %s %s", tc.src, v.TypeName(), got, tc.typ, tc.repr)
		}
	}
}

// TestExpressionEscapesAsAStringTemplate checks an expression is compiled as a
// template from a string, which is what jinja2's compile_expression does: it
// escapes when select_autoescape says a string template does, whatever the
// named-template default. The answers are CPython's for the same settings.
func TestExpressionEscapesAsAStringTemplate(t *testing.T) {
	for _, tc := range []struct {
		forString bool
		typ, repr string
	}{
		{true, "Markup", "Markup('<&lt;')"},
		{false, "str", "'<<'"},
	} {
		env := mustEnv(gojja2.WithAutoescapeSelection(gojja2.SelectAutoescapeConfig{
			DisableForString: !tc.forString, Default: !tc.forString,
		}))
		x, err := env.CompileExpression("x|safe ~ y")
		if err != nil {
			t.Fatal(err)
		}
		v, err := x.Eval(context.Background(), map[string]any{"x": "<", "y": "<"})
		if got := value.Repr(v); err != nil || v.TypeName() != tc.typ || got != tc.repr {
			t.Errorf("escaping strings %v: %s %s, %v; want %s %s", tc.forString, v.TypeName(), got, err, tc.typ, tc.repr)
		}
	}
}

// jsonInts reads JSON numbers the way Python's json module does, which is
// how the oracle received the same arguments: integers as int, the rest float.
func jsonInts(v any) any {
	switch t := v.(type) {
	case json.Number:
		if i, err := t.Int64(); err == nil {
			return i
		}
		f, _ := t.Float64()
		return f
	case map[string]any:
		for k, x := range t {
			t[k] = jsonInts(x)
		}
	case []any:
		for i, x := range t {
			t[i] = jsonInts(x)
		}
	}
	return v
}

// TestExpressionIsBoundedLikeARender checks the evaluation runs under the
// environment's limits and the caller's context, as a render does.
func TestExpressionIsBoundedLikeARender(t *testing.T) {
	x, err := mustEnv(gojja2.WithMaxIterations(1000)).CompileExpression("range(n)|list|length")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := x.Eval(context.Background(), map[string]any{"n": 1_000_000}); !errors.Is(err, gojja2.ErrTooManyIterations) {
		t.Errorf("err = %v, want ErrTooManyIterations", err)
	}
	// The context is consulted every few thousand units of work, as in a
	// render, so the evaluation is given enough to reach a check.
	unbounded, err := mustEnv(gojja2.WithMaxIterations(1 << 40)).CompileExpression("range(n)|list|length")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := unbounded.Eval(ctx, map[string]any{"n": 100_000}); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: err = %v, want context.Canceled", err)
	}
	v, err := x.EvalValues(context.Background(), map[string]value.Value{"n": value.Int(10)})
	if err != nil || value.Repr(v) != "10" {
		t.Errorf("EvalValues = %v, %v; want 10", v, err)
	}
}

// TestExpressionConcurrentEval evaluates one compiled expression from many
// goroutines; run under -race it is the check that an Expression holds no
// per-evaluation state.
func TestExpressionConcurrentEval(t *testing.T) {
	x, err := mustEnv().CompileExpression("[a] * 3")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Go(func() {
			v, err := x.Eval(context.Background(), map[string]any{"a": i})
			if want := value.Repr(value.NewList(value.Int(int64(i)), value.Int(int64(i)), value.Int(int64(i)))); err != nil || value.Repr(v) != want {
				t.Errorf("a=%d: %v, %v; want %s", i, value.Repr(v), err, want)
			}
		})
	}
	wg.Wait()
}
