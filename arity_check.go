// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"fmt"
	"strings"

	"github.com/mgilbir/gojja2/errs"
	"github.com/mgilbir/gojja2/value"
)

// Filters accepted whatever they were given. Extra positional arguments were
// not merely ignored -- they were read as the *next* parameter, so
// `[1,2]|min(1,2,3,4,5)` took the 2 for an attribute name and failed with
// "str object has no element 2", and `|slice(1,2,3,4,5)` quietly produced
// [['a','b',2]]. An unknown keyword was dropped in silence.
//
// 85 of 96 probed arity cases diverged from CPython while the conformance rate
// sat at 99.8%, because neither the corpus nor the generator ever writes a call
// with the wrong arity: the corpus is real templates, and the generator emits
// argument lists from a hand-curated table of correct ones. Tests already had a
// check of this kind, written by hand; filters had none.
//
// So the signatures are read out of the pinned jinja2 (see
// tools/oracle/gen_arity.py) and the binding below is Python's, reporting
// Python's messages.

// checkArity reports the error CPython's argument binding would raise for a
// call of this shape, or nil.
//
// name is what the template wrote; sig is what jinja2 declares for it.
func checkArity(sig signature, args *value.CallArgs) error {
	// The order the three failures are reported in is CPython's, and it is
	// not the order they are checked in: a keyword problem beats a count
	// problem, and a count problem beats a missing argument. `x|upper(1,
	// zzzz=2)` is "an unexpected keyword argument", not "takes 1
	// positional argument but 2 were given", even though both are true.
	//
	// Python counts the value being filtered or tested, and whatever jinja2
	// injects ahead of it, among the positional arguments.
	given := sig.injected + 1 + len(args.Pos)

	// Bind positionally: the value takes params[0], the rest follow.
	bound := make([]bool, len(sig.params))
	if len(bound) > 0 {
		bound[0] = true
	}
	for i := range args.Pos {
		if i+1 < len(bound) {
			bound[i+1] = true
		}
	}

	// A C function takes no keyword arguments at all, and says so in one
	// message rather than naming the offender -- and then reports any wrong
	// count with a single wording of its own, too few and too many alike.
	// Neither wording can be derived from the signature: abs says "abs()
	// takes exactly one argument (2 given)" where operator.eq, which jinja2
	// registers as the `eq`, `==` and `equalto` tests, says "eq expected 2
	// arguments, got 3" and calls itself "_operator.eq" when refusing a
	// keyword. Both are probed out of CPython; see tools/oracle/gen_arity.py.
	if sig.builtin {
		if len(args.Kwargs) > 0 {
			return errs.New(errs.TypeError, "%s", sig.kwMessage)
		}
		if given != sig.total {
			return errs.New(errs.TypeError, "%s", fmt.Sprintf(sig.countMessage, given))
		}
		return nil
	}

	for _, kw := range args.Kwargs {
		at := -1
		for i, p := range sig.params {
			if p == kw.Name {
				at = i
				break
			}
		}
		switch {
		case at < 0:
			if sig.varKw {
				continue
			}
			return errs.New(errs.TypeError,
				"%s() got an unexpected keyword argument '%s'", sig.pyName, kw.Name)
		case bound[at]:
			return errs.New(errs.TypeError,
				"%s() got multiple values for argument '%s'", sig.pyName, kw.Name)
		default:
			bound[at] = true
		}
	}

	if sig.total >= 0 && given > sig.total {
		return errs.New(errs.TypeError, "%s", tooManyMessage(sig, given))
	}

	// A parameter with no default that nothing bound is missing. The
	// required count includes the injected parameter, which is never
	// missing, so it is discounted first.
	var missing []string
	for i := 1; i < len(sig.params) && i < sig.required-sig.injected; i++ {
		if !bound[i] {
			missing = append(missing, "'"+sig.params[i]+"'")
		}
	}
	if len(missing) > 0 {
		return errs.New(errs.TypeError, "%s() missing %d required positional argument%s: %s",
			sig.pyName, len(missing), plural(len(missing)), joinPythonList(missing))
	}
	return nil
}

// tooManyMessage is CPython's wording for a Python function called with too
// many positional arguments. A C function has its own, generated with the
// signature.
func tooManyMessage(sig signature, given int) string {
	if sig.required == sig.total {
		return fmt.Sprintf("%s() takes %d positional argument%s but %d were given",
			sig.pyName, sig.total, plural(sig.total), given)
	}
	return fmt.Sprintf("%s() takes from %d to %d positional arguments but %d were given",
		sig.pyName, sig.required, sig.total, given)
}

// joinPythonList renders a list of names the way CPython's error does: "'a'",
// "'a' and 'b'", "'a', 'b', and 'c'".
func joinPythonList(items []string) string {
	switch len(items) {
	case 1:
		return items[0]
	case 2:
		return items[0] + " and " + items[1]
	default:
		return strings.Join(items[:len(items)-1], ", ") + ", and " + items[len(items)-1]
	}
}
