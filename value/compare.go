// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package value

import (
	"math"
	"math/big"
	"strings"

	"github.com/mgilbir/gojja2/errs"
)

// maxCompareDepth bounds how deeply == and the ordering comparisons descend.
//
// A value graph can now contain a cycle, because a cyclic Go context converts
// into a cyclic Value rather than expanding forever. Two *distinct* cyclic
// structures have no fixed point, so the descent has to be bounded or it takes
// the stack out -- and a Go stack overflow cannot be recovered. CPython raises
// RecursionError too, and its message is what EqualErr reports -- but not at the
// same depth any more: 3.11 gave up at 992 and 3.12 onwards goes ten times
// deeper. See docs/limits.md, which measures both.
const maxCompareDepth = 1000

// RecursionMessageComparison is what CPython reports when the stack runs out
// inside a comparison.
const RecursionMessageComparison = "maximum recursion depth exceeded in comparison"

// Equal is Python's ==.
//
// It never fails: comparing values of unrelated types is False, not an error.
// Numbers compare across int, float and bool because bool is an int subclass
// and Python's numeric tower makes 1 == 1.0 == True.
//
// Two structures too deeply nested to compare are reported as unequal here,
// because there is no error to return. Use [EqualErr] where the caller can
// raise, which is what the `==` operator in a template does.
func Equal(a, b Value) bool {
	// The only error equalDepth can report is the recursion one, and this
	// form has nowhere to put it, so the interpreter version cannot show
	// through here. EqualErr is the form that can raise, and it takes one.
	equal, _ := equalDepth(a, b, 0, DefaultPythonVersion)
	return equal
}

// EqualErr is [Equal], reporting the RecursionError CPython raises rather than
// answering a question it cannot decide.
func EqualErr(a, b Value, py PythonVersion) (bool, error) {
	return equalDepth(a, b, 0, py)
}

// EqualBool is CPython's PyObject_RichCompareBool: the same object, or an equal
// one.
//
// This is not what `==` does, and the difference is the whole of what a NaN
// means. `x == x` calls float.__eq__ and is False; `x in [x]`, `[x] == [x]`,
// `{x: 1}[x]` and everything else that searches or compares a *container* asks
// this instead, which answers True for a NaN read twice from one place. CPython
// draws the same line, in the same place, for the same reason: the identity
// check is what makes a container equal to itself whatever it holds.
//
// So every element comparison goes through here, and the `==` operator does
// not. See [SameObject] for which values have an identity to compare.
func EqualBool(a, b Value) bool {
	equal, _ := equalBoolDepth(a, b, 0, DefaultPythonVersion)
	return equal
}

// EqualBoolErr is [EqualBool], reporting the errors a comparison can raise.
func EqualBoolErr(a, b Value, py PythonVersion) (bool, error) {
	return equalBoolDepth(a, b, 0, py)
}

func equalBoolDepth(a, b Value, depth int, py PythonVersion) (bool, error) {
	if SameObject(a, b) {
		return true, nil
	}
	return equalDepth(a, b, depth, py)
}

func equalDepth(a, b Value, depth int, py PythonVersion) (bool, error) {
	if depth > maxCompareDepth {
		// The suffix stays, in every version. 3.12 dropped "while
		// calling a Python object" from the *call* path's message, and
		// RecursionMessageFor exists for that -- but a comparison
		// still names itself: 3.11, 3.12 and 3.13 all say "maximum
		// recursion depth exceeded in comparison", and 3.14 says
		// "Stack overflow (used N kB) in comparison", where N is the
		// stack this machine had. Unifying it here made
		// `{% do l.append(m) %}{% do m.append(l) %}{{ l == m }}` say
		// the wrong thing on every interpreter but one.
		return false, errs.New(errs.RecursionError, "%s", RecursionMessageComparison)
	}
	// StrictUndefined defines __eq__ and __ne__ as failures, so a
	// comparison involving one is an error rather than an answer -- on
	// either side, because Python tries both operands' __eq__. This is
	// also what makes `nope in [1]` raise: list containment compares.
	if err := StrictRefusal(a); err != nil {
		return false, err
	}
	if err := StrictRefusal(b); err != nil {
		return false, err
	}
	// Identity first, as Python's == does: a structure always equals
	// itself, cyclic or not, and this is what makes `a == a` terminate.
	if a.kind == b.kind && a.obj != nil && a.obj == b.obj {
		switch a.kind {
		case KindList, KindTuple, KindDict, KindObject, KindFunc:
			return true, nil
		}
	}
	if a.IsNumber() && b.IsNumber() {
		ord, ok := compareNumbers(a, b)
		return ok && ord == 0, nil
	}
	// Two set-like operands compare as sets here too, and not only under the
	// orderings: `{}.items() == {}.keys()` is True, and a view equals the
	// set a difference built from it. The kind check below would call them
	// different things and stop, and their own Equals methods only know
	// their own type.
	if handled, equal, err := equalAsSets(a, b, py); handled {
		return equal, err
	}
	// A tuple subclass equals the tuple it stands for, in either position:
	// `p == (1, 2)` and `(1, 2) == p` both go through tuple.__eq__. This
	// has to come before the kind check below, which would otherwise call
	// an object and a tuple different things and stop.
	//
	// An object that states its own equality keeps it. Equaler is an
	// explicit opinion; standing for a tuple only supplies the default a
	// subclass inherits.
	if tupleSubclass(a) || tupleSubclass(b) {
		equal, known, err := statedEqual(a, b, py)
		if err != nil {
			return false, err
		}
		if known {
			return equal, nil
		}
		a, b = AsTupleIfPossible(a), AsTupleIfPossible(b)
	}
	if a.kind != b.kind {
		// An object may still state its equality with something of
		// another kind, which is what a proxy does: mappingproxy
		// delegates __eq__ to the mapping it wraps, so `m == d` and
		// `d == m` are both True. An object that has no opinion about
		// the other operand -- a dict view asked about a dict -- says
		// so and falls through.
		if a.kind == KindObject || b.kind == KindObject {
			if equal, known, err := statedEqual(a, b, py); err != nil || known {
				return equal, err
			}
		}
		// str and bytes never compare equal, and neither do list and
		// tuple -- Python keeps those distinct.
		return false, nil
	}
	switch a.kind {
	case KindNone:
		return true, nil
	case KindUndefined:
		// jinja2's Undefined.__eq__ compares only the class, so any two
		// undefined values of the same flavour are equal.
		return a.undef().behavior == b.undef().behavior, nil
	case KindString, KindBytes:
		return a.str == b.str, nil
	case KindList, KindTuple:
		as, _ := a.Seq()
		bs, _ := b.Seq()
		if as.Len() != bs.Len() {
			return false, nil
		}
		for i := range as.items {
			equal, err := equalBoolDepth(as.items[i], bs.items[i], depth+1, py)
			if err != nil {
				return false, err
			}
			if !equal {
				return false, nil
			}
		}
		return true, nil
	case KindDict:
		ad, _ := a.Dict()
		bd, _ := b.Dict()
		if ad.Len() != bd.Len() {
			return false, nil
		}
		// Order is irrelevant to dict equality, only content.
		for _, e := range ad.entries {
			other, ok := bd.GetKnown(e.Key)
			if !ok {
				return false, nil
			}
			equal, rerr := equalBoolDepth(e.Value, other, depth+1, py)
			if rerr != nil {
				return false, rerr
			}
			if !equal {
				return false, nil
			}
		}
		return true, nil
	case KindObject:
		equal, known, err := statedEqual(a, b, py)
		if err != nil {
			return false, err
		}
		if known {
			return equal, nil
		}
		return a.obj == b.obj, nil
	case KindFunc:
		return a.obj == b.obj, nil
	}
	return false, nil
}

// Ordered evaluates `a op b` for op in "<", "<=", ">", ">=".
//
// Returning a bool rather than a three-way ordering is what lets NaN behave:
// every comparison involving it is false, including NaN <= NaN, which no
// -1/0/1 result can express.
func Ordered(op string, a, b Value, py PythonVersion) (bool, error) {
	// Two set-like operands compare as *sets*, which is a subset test and
	// not an ordering at all: neither `a < b` nor `a > b` need hold. So it
	// is answered here rather than through compare, whose -1/0/1 cannot say
	// "unrelated".
	if handled, res, err := orderedAsSets(op, a, b, py); handled {
		return res, err
	}
	ord, ok, err := compare(op, a, b, 0, py)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil // unordered: NaN was involved
	}
	switch op {
	case "<":
		return ord < 0, nil
	case "<=":
		return ord <= 0, nil
	case ">":
		return ord > 0, nil
	case ">=":
		return ord >= 0, nil
	}
	return false, errs.New(errs.ValueError, "unknown comparison operator %q", op)
}

// equalAsSets answers == between two set-like operands, which CPython decides
// with the same rule as the orderings: the same length, and every element of one
// in the other. Two *empty* views of different types are equal, which is what a
// `d.items() == d.keys()` over an empty dict asks.
//
// A values view is not set-like in either engine, so two of them are equal only
// by identity -- which is why `d.values() == d.values()` is False.
func equalAsSets(a, b Value, py PythonVersion) (handled, equal bool, err error) {
	left, lok := setLikeElements(a)
	right, rok := setLikeElements(b)
	if !lok || !rok {
		return false, false, nil
	}
	if len(left) != len(right) {
		return true, false, nil
	}
	ok, err := allContainedIn(left, b, py)
	return true, ok, err
}

// orderedAsSets answers <, <=, > and >= between two set-like operands -- a dict's
// keys or items view, or a set built from one -- the way CPython's
// dictview_richcompare does: as the subset relation.
//
// The lengths decide first, and that is not an optimisation. A longer view
// cannot be a proper subset, so CPython answers False without looking at an
// element, which is why `{'x': [1]}.items() > {}.keys()` is False rather than a
// complaint about the unhashable list. The containment that follows is the
// view's own -- an items view looks its key up and compares the value, where a
// set hashes -- so the error a template sees is the one the container makes.
//
// A values view is not set-like, in CPython or here: its elements need be
// neither unique nor hashable, so `d.values() < e.values()` is the ordinary
// type error. Found by the generated differential, on a chained comparison that
// reached `nested.items() < ed.keys()`.
func orderedAsSets(op string, a, b Value, py PythonVersion) (handled, result bool, err error) {
	left, lok := setLikeElements(a)
	right, rok := setLikeElements(b)
	if !lok || !rok {
		return false, false, nil
	}
	var subset, of []Value
	var container Value
	switch op {
	case "<":
		if len(left) < len(right) {
			subset, of, container = left, right, b
		}
	case "<=":
		if len(left) <= len(right) {
			subset, of, container = left, right, b
		}
	case ">":
		if len(left) > len(right) {
			subset, of, container = right, left, a
		}
	case ">=":
		if len(left) >= len(right) {
			subset, of, container = right, left, a
		}
	default:
		return false, false, nil
	}
	if of == nil {
		// The lengths already settle it.
		return true, false, nil
	}
	ok, err := allContainedIn(subset, container, py)
	return true, ok, err
}

// setLikeElements are the elements of an operand that compares as a set: a keys
// or items view, or a set. A values view answers false, as PyDictViewSet_Check
// does for one.
func setLikeElements(v Value) ([]Value, bool) {
	switch o := v.Interface().(type) {
	case *Set:
		return o.items, true
	case SetOperand:
		return o.SetElements()
	}
	return nil, false
}

// allContainedIn asks the container itself about each element, so the lookup --
// and any refusal it makes -- is the one `x in container` would have made.
func allContainedIn(items []Value, container Value, py PythonVersion) (bool, error) {
	holder, ok := container.obj.(interface {
		ContainsErr(Value, PythonVersion) (bool, bool, error)
	})
	if !ok {
		return false, nil
	}
	for _, item := range items {
		found, known, err := holder.ContainsErr(item, py)
		if err != nil {
			return false, err
		}
		if !known || !found {
			return false, nil
		}
	}
	return true, nil
}

// compare returns the ordering of a and b. The second result is false when the
// two are unordered because of NaN; op is carried only for the error message.
//
// depth bounds the descent for the same reason equalDepth is bounded, and it
// is not optional: two *distinct* cyclic structures have no fixed point, so
// `a < b` over a pair that point at each other recurses forever. Equality was
// guarded and ordering was not, which meant `{{ a == b }}` raised CPython's
// RecursionError while `{{ a < b }}` -- and every sort, min and max, which all
// come through here -- took the process down with a Go stack overflow that
// recover cannot catch.
func compare(op string, a, b Value, depth int, py PythonVersion) (int, bool, error) {
	if depth > maxCompareDepth {
		return 0, false, errs.New(errs.RecursionError, "%s", RecursionMessageComparison)
	}
	// Undefined has no ordering: jinja2's Undefined raises on <, <=, > and
	// >= even though == is answerable. Report the undefined's own error
	// rather than a type mismatch, which is what a template author needs.
	if a.IsUndefined() {
		return 0, false, a.UndefinedError()
	}
	if b.IsUndefined() {
		return 0, false, b.UndefinedError()
	}
	// An object whose rich comparison is another value's is that value when
	// it is on the left -- its own method is tried first and never declines.
	// On the right it is asked below, only where the left side declined.
	if d, ok := a.obj.(OrderDelegate); ok && a.kind == KindObject {
		return compare(op, d.OrderDelegate(), b, depth+1, py)
	}
	// A tuple subclass orders as the tuple it stands for, so |min and |max
	// over |groupby results compare pair by pair. The originals are kept
	// for the error, which names the class the template actually has.
	origA, origB := a, b
	a, b = AsTupleIfPossible(a), AsTupleIfPossible(b)

	// When the right operand's type derives from the left's, Python runs
	// the reflected comparison first: `(1,) < g` is `g.__gt__((1,))`, not
	// `tuple.__lt__((1,), g)`. The ordering is the same either way, but the
	// error is not -- it names the swapped operator and compares the
	// elements in the other order, which is what a template author sees
	// when a group tuple meets a literal one.
	//
	// Both sides being tuple subclasses is left alone: CPython reflects
	// only when one type strictly derives from the other, and nothing here
	// can tell whether two host objects are related at all.
	//
	// The recursive call cannot swap again -- both operands are plain
	// tuples by then -- so this terminates without spending depth.
	if tupleSubclass(origB) && !tupleSubclass(origA) && a.kind == KindTuple && b.kind == KindTuple {
		ord, ok, err := compare(swappedOp(op), b, a, depth, py)
		return -ord, ok, err
	}

	if a.IsNumber() && b.IsNumber() {
		ord, ok := compareNumbers(a, b)
		return ord, ok, nil
	}
	if a.kind == b.kind {
		switch a.kind {
		case KindString, KindBytes:
			return strings.Compare(a.str, b.str), true, nil
		case KindList, KindTuple:
			as, _ := a.Seq()
			bs, _ := b.Seq()
			return compareSeq(op, as.items, bs.items, depth, py)
		}
	}
	// The left side declined, so Python asks the right one with the operator
	// reflected -- and a delegating object answers by comparing what it
	// wraps, whose refusal is then the one that is raised.
	if d, ok := b.obj.(OrderDelegate); ok && b.kind == KindObject {
		ord, ok, err := compare(swappedOp(op), d.OrderDelegate(), a, depth+1, py)
		return -ord, ok, err
	}
	return 0, false, errs.New(errs.TypeError,
		"'%s' not supported between instances of '%s' and '%s'",
		op, origA.TypeName(), origB.TypeName())
}

// statedEqual asks either operand whether it decides equality for itself,
// which is what an Object implementing Equaler is for. known is false when
// neither has an opinion and the caller's own rule applies.
func statedEqual(a, b Value, py PythonVersion) (equal, known bool, err error) {
	for _, pair := range [2][2]Value{{a, b}, {b, a}} {
		self, other := pair[0], pair[1]
		if self.kind != KindObject {
			continue
		}
		// EqualerErr first: an Object that can fail the comparison is
		// also an Equaler, and the erroring form is the fuller answer.
		if e, ok := self.obj.(EqualerErr); ok {
			if equal, known, err := e.EqualsErr(other, py); err != nil || known {
				return equal, known, err
			}
			continue
		}
		if e, ok := self.obj.(Equaler); ok {
			if equal, known := e.Equals(other); known {
				return equal, known, nil
			}
		}
	}
	return false, false, nil
}

// AsTupleIfPossible returns the tuple v stands for when v is a TupleView, and
// v unchanged otherwise.
//
// It marks the places where Python's *tuple type* decides the behaviour, and
// so sees a subclass as the tuple it is: concatenation, repetition, slicing,
// the tuple methods, %-formatting's argument tuple and ordering. Everything
// else -- the type name in an error, repr, attribute lookup -- belongs to the
// subclass and must not be unwrapped.
func AsTupleIfPossible(v Value) Value {
	if tv, ok := tupleViewOf(v); ok {
		return tv.AsTuple()
	}
	return v
}

// tupleSubclass reports whether v is an Object standing for a tuple subclass.
func tupleSubclass(v Value) bool {
	_, ok := tupleViewOf(v)
	return ok
}

func tupleViewOf(v Value) (TupleView, bool) {
	if v.kind != KindObject {
		return nil, false
	}
	tv, ok := v.obj.(TupleView)
	return tv, ok
}

// swappedOp is the comparison Python runs on the reflected operands.
func swappedOp(op string) string {
	switch op {
	case "<":
		return ">"
	case "<=":
		return ">="
	case ">":
		return "<"
	case ">=":
		return "<="
	}
	return op
}

// compareSeq is Python's lexicographic sequence ordering: the first differing
// element decides, and if one runs out first the shorter sequence is smaller.
func compareSeq(op string, a, b []Value, depth int, py PythonVersion) (int, bool, error) {
	n := min(len(a), len(b))
	for i := range n {
		if Equal(a[i], b[i]) {
			continue
		}
		ord, ok, err := compare(op, a[i], b[i], depth+1, py)
		if err != nil {
			return 0, false, err
		}
		if !ok {
			// Elements are unequal but unordered, so the sequences
			// are too.
			return 0, false, nil
		}
		return ord, true, nil
	}
	switch {
	case len(a) < len(b):
		return -1, true, nil
	case len(a) > len(b):
		return 1, true, nil
	}
	return 0, true, nil
}

// compareNumbers orders two numbers exactly, without the precision loss of
// converting both to float64.
//
// Python compares int against float by value, not by coercion, so
// 2**53 + 1 == float(2**53) is False. Anything that rounds the int first gets
// that wrong, so wide integers are compared as exact rationals.
func compareNumbers(a, b Value) (int, bool) {
	af, aIsFloat := floatOf(a)
	bf, bIsFloat := floatOf(b)

	switch {
	case aIsFloat && bIsFloat:
		if math.IsNaN(af) || math.IsNaN(bf) {
			return 0, false
		}
		return cmpFloat(af, bf), true
	case !aIsFloat && !bIsFloat:
		ai, _ := a.BigInt()
		bi, _ := b.BigInt()
		return ai.Cmp(bi), true
	}

	// Exactly one side is a float. Order the integer against it exactly and
	// flip the result if the integer was the right-hand operand, since
	// cmpIntFloat answers cmp(int, float).
	f, i, sign := bf, a, 1
	if aIsFloat {
		f, i, sign = af, b, -1
	}
	if math.IsNaN(f) {
		return 0, false
	}
	switch {
	case math.IsInf(f, 1):
		return -sign, true
	case math.IsInf(f, -1):
		return sign, true
	}
	bi, _ := i.BigInt()
	ord := new(big.Rat).SetInt(bi).Cmp(new(big.Rat).SetFloat64(f))
	return ord * sign, true
}

// floatOf reports the float value of v and whether v is a float at all.
func floatOf(v Value) (float64, bool) {
	if v.kind == KindFloat {
		return v.AsFloat(), true
	}
	return 0, false
}

func cmpFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// Contains implements `in`: substring for strings, membership for sequences,
// and key lookup for mappings.
//
// budget is charged one element per element examined, which is what makes a
// membership test over something large interruptible. `{{ -1 in range(2**63-1) }}`
// walked to the end with nothing counting and nothing able to stop it: a
// three-second deadline was still running ninety seconds later. An Object that
// knows a better answer than a scan says so through Container, which is how a
// range answers arithmetically rather than by searching.
func Contains(item, container Value, budget Budget, py PythonVersion) (bool, error) {
	// __contains__ on a StrictUndefined container fails; an item that is
	// one fails through the comparison each candidate makes, which
	// equalDepth reports, but a container that is empty or short-circuits
	// would never reach it. Both are refused here.
	if err := StrictRefusal(container); err != nil {
		return false, err
	}
	// str.__contains__ and bytes.__contains__ type-check their left operand
	// before they look at it at all, so an undefined there is a TypeError
	// naming its class and not the undefined's own refusal. Every other
	// container reaches the item through a comparison, and that is where
	// the refusal comes from -- which is why this is two cases and not a
	// rule about undefineds.
	if !searchable(container) {
		return false, notAContainer(container, py)
	}
	// A container that examines its item in its own way answers before the
	// item's refusals are consulted, because *what* it examines decides
	// whether they apply at all: a keys view hashes the item, an items view
	// answers False for anything that is not a pair and hashes the key of
	// one that is, and a values view scans and so defers to the generic path
	// below. `{{ [1] in d.keys() }}` answered False, and
	// `{{ nope in d.items() }}` raised where CPython answers False.
	if c, ok := container.obj.(interface {
		ContainsErr(Value, PythonVersion) (bool, bool, error)
	}); ok {
		if found, known, err := c.ContainsErr(item, py); err != nil || known {
			return found, err
		}
	}
	switch {
	case container.kind == KindString && item.kind != KindString:
		return false, errs.New(errs.TypeError,
			"'in <string>' requires string as left operand, not %s", item.TypeName())
	case container.kind == KindBytes && !item.IsInteger() && item.kind != KindBytes:
		return false, errs.New(errs.TypeError,
			"a bytes-like object is required, not '%s'", item.TypeName())
	}
	// The item's own refusal is *not* consulted here: it comes from the
	// comparison each candidate makes, so a container with no candidates
	// never reaches it. `{{ nope in [] }}` is False, and so is
	// `{{ nope in range(0) }}`. A dict is the exception below, because it
	// hashes the item before it looks for it, which is why `{{ nope in {} }}`
	// raises on an empty dict where an empty list does not.
	if o, ok := container.obj.(Container); ok && container.kind == KindObject {
		if found, known := o.Contains(item); known {
			return found, nil
		}
	}
	switch container.kind {
	case KindString:
		// A non-string item was refused above, before the item's own
		// refusal was consulted, because str.__contains__ type-checks
		// its left operand first. There was a second copy of that check
		// here and nothing could reach it.
		return strings.Contains(container.str, item.str), nil
	case KindBytes:
		// bytes is a sequence of integers, so an integer on the left is
		// asking whether that *byte value* occurs -- `97 in b"ab"` is
		// True. It is refused outside a byte's range rather than simply
		// answered False, because the question is malformed rather than
		// unsatisfied. A bool is an int here as everywhere in Python.
		if item.IsInteger() {
			n, fits := item.Int64()
			if !fits || n < 0 || n > 255 {
				return false, errs.New(errs.ValueError,
					"byte must be in range(0, 256)")
			}
			return strings.IndexByte(container.str, byte(n)) >= 0, nil
		}
		// Neither an integer nor a bytes was refused above, for the
		// same reason as the string arm.
		return strings.Contains(container.str, item.str), nil
	case KindList, KindTuple:
		s, _ := container.Seq()
		for _, v := range s.items {
			if err := chargeItems(budget, 1); err != nil {
				return false, err
			}
			// Each candidate is a real `==`, so a StrictUndefined
			// among the *elements* refuses just as one in the item
			// position does -- `{{ 1 in [yes, nope] }}` raises
			// rather than answering False. Comparing with Equal
			// swallowed that, and the same held for the two object
			// arms below.
			//
			// The element is the *left* operand, which is what
			// decides whose refusal is reported when both sides
			// have one: CPython's list_contains, tuplecontains and
			// _PySequence_IterSearch all compare the candidate
			// against the item, so `{{ nope in [d.nope] }}` names
			// the element's complaint and only an element with no
			// opinion hands the question back to the item.
			eq, err := EqualBoolErr(v, item, py)
			if err != nil {
				return false, err
			}
			if eq {
				return true, nil
			}
		}
		return false, nil
	case KindDict:
		d, _ := container.Dict()
		// Hashed before it is looked for, so this refuses whether the
		// dict holds anything or not. CheckHashable reports a
		// StrictUndefined's own error, because hashing one is what
		// raises it.
		if err := CheckHashable(item, py, AsDictKey); err != nil {
			return false, err
		}
		_, ok := d.GetKnown(item)
		return ok, nil
	case KindUndefined:
		if container.undef().behavior == UndefinedStrict {
			return false, container.UndefinedError()
		}
		return false, nil
	case KindObject:
		switch o := container.obj.(type) {
		case Mapping:
			_, ok := o.GetItem(item)
			return ok, nil
		case Sequence:
			for i := range o.Len() {
				if err := chargeItems(budget, 1); err != nil {
					return false, err
				}
				v, ok := o.GetIndex(i)
				if !ok {
					continue
				}
				eq, err := EqualBoolErr(v, item, py)
				if err != nil {
					return false, err
				}
				if eq {
					return true, nil
				}
			}
			return false, nil
		case Iterable:
			for v := range o.Iterate() {
				if err := chargeItems(budget, 1); err != nil {
					return false, err
				}
				eq, err := EqualBoolErr(v, item, py)
				if err != nil {
					return false, err
				}
				if eq {
					return true, nil
				}
			}
			return false, nil
		}
	}
	return false, notAContainer(container, py)
}

// notAContainer words `x in y` for a y that cannot be searched.
func notAContainer(container Value, py PythonVersion) error {
	if py.ContainerMessageIsLonger() {
		return errs.New(errs.TypeError,
			"argument of type '%s' is not a container or iterable", container.TypeName())
	}
	return errs.New(errs.TypeError,
		"argument of type '%s' is not iterable", container.TypeName())
}

// searchable reports whether `x in container` has anywhere to look. It mirrors
// the switch above, and exists because the answer is needed *before* the item is
// examined: Python asks the container for a `__contains__` before it looks at
// what is being searched for, so `{{ nope in 1.5 }}` is "argument of type
// 'float' is not iterable" and not the undefined's own refusal.
func searchable(container Value) bool {
	switch container.kind {
	case KindString, KindBytes, KindList, KindTuple, KindDict, KindUndefined:
		return true
	case KindObject:
		switch container.obj.(type) {
		case Container, Mapping, Sequence, Iterable:
			return true
		}
	}
	return false
}

// errTypeNotIterable is the error Python raises for `for x in <non-iterable>`.
func errTypeNotIterable(v Value) error {
	return errs.New(errs.TypeError, "'%s' object is not iterable", v.TypeName())
}
