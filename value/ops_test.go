// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package value_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/mgilbir/gojja2/errs"
	"github.com/mgilbir/gojja2/value"
)

// pool mirrors POOL in tools/oracle/gen_ops.py, index for index. The corpus
// records repr() of each entry and TestOperatorPoolMatches checks it, so the
// two definitions cannot drift apart unnoticed.
func pool() []value.Value {
	big53 := int64(1) << 53
	twoTo63, _ := new(big.Int).SetString("9223372036854775808", 10)
	minus2To63Minus1, _ := new(big.Int).SetString("-9223372036854775809", 10)

	return []value.Value{
		value.None, value.True, value.False,
		value.Int(0), value.Int(1), value.Int(-1), value.Int(7), value.Int(-7),
		value.Int(3), value.Int(2), value.Int(10),
		value.BigInt(twoTo63), value.BigInt(minus2To63Minus1),
		value.Int(big53), value.Int(big53 + 1),
		value.Float(0.0), value.Float(math.Copysign(0, -1)), value.Float(1.0),
		value.Float(2.5), value.Float(-2.5), value.Float(0.5), value.Float(9007199254740992.0),
		value.Float(math.Inf(1)), value.Float(math.Inf(-1)), value.Float(math.NaN()),
		value.String(""), value.String("a"), value.String("ab"), value.String("Z"), value.String("%s"),
		value.NewList(), value.NewList(value.Int(1)), value.NewList(value.Int(1), value.Int(2)),
		value.NewTuple(), value.NewTuple(value.Int(1)), value.NewTuple(value.Int(1), value.Int(2)),
		value.NewDict(),
		value.DictOf(value.String("a"), value.Int(1)),
		value.DictOf(value.String("a"), value.Int(1), value.String("b"), value.Int(2)),
	}
}

// binaryOps maps each operator to the gojja2 entry point that implements it.
//
// The two that take a budget are given none: this corpus is about what each
// operator computes, and a nil budget leaves only the hard ceiling, which no
// case here comes near.
var binaryOps = map[string]func(a, b value.Value) (value.Value, error){
	"+":  value.Add,
	"-":  value.Sub,
	"*":  func(a, b value.Value) (value.Value, error) { return value.Mul(a, b, nil) },
	"/":  value.Div,
	"//": value.FloorDiv,
	"%":  func(a, b value.Value) (value.Value, error) { return value.Mod(a, b, nil) },
	"**": value.Pow,
	"==": func(a, b value.Value) (value.Value, error) { return value.Bool(value.Equal(a, b)), nil },
	"!=": func(a, b value.Value) (value.Value, error) { return value.Bool(!value.Equal(a, b)), nil },
	"<":  ordered("<"),
	"<=": ordered("<="),
	">":  ordered(">"),
	">=": ordered(">="),
	// Python spells this `a in b`, so the container is the second operand.
	"in": func(a, b value.Value) (value.Value, error) {
		ok, err := value.Contains(a, b, nil)
		return value.Bool(ok), err
	},
}

func ordered(op string) func(a, b value.Value) (value.Value, error) {
	return func(a, b value.Value) (value.Value, error) {
		ok, err := value.Ordered(op, a, b)
		return value.Bool(ok), err
	}
}

type opCase struct {
	A    int    `json:"a"`
	Op   string `json:"op"`
	B    int    `json:"b"`
	Repr string `json:"repr"`
	Err  string `json:"err"`
	Msg  string `json:"msg"`
}

func loadOpCorpus(t *testing.T) ([]string, []opCase) {
	t.Helper()
	f, err := os.Open("testdata/ops.jsonl")
	if err != nil {
		t.Fatalf("open corpus: %v", err)
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	if !sc.Scan() {
		t.Fatal("corpus is empty")
	}
	var header struct {
		Pool []string `json:"pool"`
	}
	if err := json.Unmarshal(sc.Bytes(), &header); err != nil {
		t.Fatalf("parse corpus header: %v", err)
	}

	var cases []opCase
	for sc.Scan() {
		var c opCase
		if err := json.Unmarshal(sc.Bytes(), &c); err != nil {
			t.Fatalf("parse corpus line: %v", err)
		}
		cases = append(cases, c)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read corpus: %v", err)
	}
	return header.Pool, cases
}

// TestOperatorPoolMatches guards the two hand-written pools against drift. If
// it fails, the operator corpus below is comparing the wrong values and its
// result means nothing.
func TestOperatorPoolMatches(t *testing.T) {
	want, _ := loadOpCorpus(t)
	got := pool()
	if len(got) != len(want) {
		t.Fatalf("pool size: Go has %d, corpus has %d", len(got), len(want))
	}
	for i := range got {
		if r := value.Repr(got[i]); r != want[i] {
			t.Errorf("pool[%d]: Go has %s, CPython has %s", i, r, want[i])
		}
	}
}

func TestOperatorsMatchCPython(t *testing.T) {
	poolRepr, cases := loadOpCorpus(t)
	if len(poolRepr) != len(pool()) {
		t.Skip("pool drift; see TestOperatorPoolMatches")
	}
	vals := pool()

	type failure struct {
		desc  string
		count int
	}
	failures := map[string]*failure{}
	note := func(key, desc string) {
		f, ok := failures[key]
		if !ok {
			f = &failure{desc: desc}
			failures[key] = f
		}
		f.count++
	}

	complexCases := 0
	for _, c := range cases {
		fn, ok := binaryOps[c.Op]
		if !ok {
			t.Fatalf("no implementation registered for operator %q", c.Op)
		}
		expr := fmt.Sprintf("%s %s %s", poolRepr[c.A], c.Op, poolRepr[c.B])
		got, err := fn(vals[c.A], vals[c.B])

		// The one deliberate divergence: CPython answers a negative base
		// raised to a fractional power with a complex number. complex is
		// a Python type, not a jinja2 one -- it cannot be written as a
		// literal in a template and no filter accepts or produces it --
		// so gojja2 refuses instead of inventing an approximation. The
		// refusal is asserted, not waived, so the behaviour stays pinned.
		if isComplexRepr(c.Repr) {
			complexCases++
			if err == nil {
				note("**/complex-not-refused", fmt.Sprintf(
					"%s: CPython gives the complex %s; gojja2 returned %s instead of refusing",
					expr, c.Repr, value.Repr(got)))
			} else if errs.KindOf(err) != errs.ValueError {
				note("**/complex-wrong-error", fmt.Sprintf(
					"%s: expected ValueError refusing a complex result, got %s: %s",
					expr, errs.KindOf(err), err.Error()))
			}
			continue
		}

		switch {
		case c.Err != "":
			if err == nil {
				note(c.Op+"/missing-error", fmt.Sprintf(
					"%s: CPython raises %s, gojja2 returned %s", expr, c.Err, value.Repr(got)))
				continue
			}
			if kind := errs.KindOf(err).String(); kind != c.Err {
				note(c.Op+"/wrong-error-class", fmt.Sprintf(
					"%s: CPython raises %s, gojja2 raises %s (%q)", expr, c.Err, kind, err.Error()))
				continue
			}
			if err.Error() != c.Msg {
				note(c.Op+"/error-message", fmt.Sprintf(
					"%s:\n    CPython: %s\n    gojja2 : %s", expr, c.Msg, err.Error()))
			}
		case err != nil:
			note(c.Op+"/unexpected-error", fmt.Sprintf(
				"%s: CPython gives %s, gojja2 raises %s: %s",
				expr, c.Repr, errs.KindOf(err), err.Error()))
		default:
			if r := value.Repr(got); r != c.Repr {
				note(c.Op+"/wrong-result", fmt.Sprintf(
					"%s: CPython gives %s, gojja2 gives %s", expr, c.Repr, r))
			}
		}
	}

	if complexCases == 0 {
		t.Error("corpus no longer contains a complex-valued ** case; the " +
			"documented divergence is no longer being exercised")
	}

	if len(failures) > 0 {
		keys := make([]string, 0, len(failures))
		total := 0
		for k, f := range failures {
			keys = append(keys, k)
			total += f.count
		}
		sort.Strings(keys)
		for _, k := range keys {
			f := failures[k]
			t.Errorf("[%s] %d case(s), first:\n  %s", k, f.count, f.desc)
		}
		t.Errorf("%d of %d operator cases diverge from CPython", total, len(cases))
	}
	t.Logf("checked %d operator cases against CPython (%d complex-result refusals)",
		len(cases), complexCases)
}

// isComplexRepr reports whether a CPython repr denotes a complex number, which
// is always either "<imag>j" or "(<real><sign><imag>j)".
func isComplexRepr(repr string) bool {
	return strings.HasSuffix(repr, "j)") ||
		(strings.HasSuffix(repr, "j") && !strings.HasSuffix(repr, "'"))
}
