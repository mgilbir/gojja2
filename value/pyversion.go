// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package value

// PythonVersion is the CPython whose behaviour is being reproduced.
//
// "Behaviourally identical to CPython jinja2" leaves a question open that the
// answer depends on: identical on *which* CPython. jinja2 3.1.6 is one library,
// but it runs on an interpreter, and the interpreter decides what `d[1:2]`
// raises, whether `sort(reverse=None)` is an error, how a division by zero is
// worded, and which code points are digits. Across 3.11 to 3.14, 38 of gojja2's
// 2,197 corpus cases answer differently -- 1.7%, and every one of them answers
// exactly two ways rather than four.
//
// So the version is a value, not a build-time assumption. It is passed as an
// argument to every function whose answer can depend on it, rather than read
// from a package variable or sniffed off an interface: a signature that carries
// it is a signature that declares "this differs by interpreter", and one that
// does not cannot silently start differing.
//
// The numbering is the interpreter's own, so comparisons read as versions do
// and a release added later sorts into place without renumbering anything.
type PythonVersion int

const (
	Python311 PythonVersion = 311
	Python312 PythonVersion = 312
	Python313 PythonVersion = 313
	Python314 PythonVersion = 314
)

// DefaultPythonVersion is the interpreter gojja2 is generated against, and what
// an environment that does not choose gets.
//
// This is the pin: every committed table and every committed golden is what
// this CPython answered, and the other versions are stored as differences from
// it. PYTHON_VERSION in the Makefile is the same fact on the generator side,
// and TestDefaultVersionMatchesThePin fails if a bump moves one and not the
// other -- because gojja2 rendering as one interpreter against tables built
// from another is a wrong answer that nothing else would catch.
//
// It is a deliberate choice rather than "whatever is newest". Freezing on
// whatever happened to be current when the engine was written is how 3.11
// became the specification here by accident; picking the newest release the
// day it lands is the same mistake with the sign flipped, since it makes the
// default an interpreter most deployments are not running yet.
const DefaultPythonVersion = Python313

// String is the interpreter as it spells itself, for an error or a report.
func (v PythonVersion) String() string {
	switch v {
	case Python311:
		return "3.11"
	case Python312:
		return "3.12"
	case Python313:
		return "3.13"
	case Python314:
		return "3.14"
	}
	return "unknown"
}

// Known reports whether v is a version gojja2 reproduces. Anything else is
// refused at New rather than silently treated as the default, because a
// configuration nobody can honour is worse than one that is rejected.
func (v PythonVersion) Known() bool {
	switch v {
	case Python311, Python312, Python313, Python314:
		return true
	}
	return false
}

// AtLeast reports whether v is n or newer, which is how every rule below is
// phrased: a behaviour arrives in some release and stays.
func (v PythonVersion) AtLeast(n PythonVersion) bool { return v >= n }

// --- the rules --------------------------------------------------------------
//
// Every place CPython's own behaviour moved between the versions above, named
// once here and asked for by name at the site that needs it. The alternative --
// writing `if py >= 312` at each site -- spreads the same decision across the
// codebase and is how a fork gets applied in one place and forgotten in
// another. Each rule says which release moved it and what the corpus case is.

// SliceKeysAreHashable reports whether a slice used as a mapping key is a
// KeyError rather than a TypeError.
//
// 3.12 made slice objects hashable, so `{{ d[0:1] }}` stopped being
// "unhashable type: 'slice'" and became a KeyError naming the slice:
// `slice(0, 1, None)`. Corpus: errshape/slice_of_a_dict, errors/truncate_dict.
func (v PythonVersion) SliceKeysAreHashable() bool { return v.AtLeast(Python312) }

// BoolArgsAreTruthy reports whether an argument declared as a bool is tested
// for truth rather than coerced to an integer.
//
// 3.12 relaxed the argument clinic, so `{{ x|sort(reverse=none) }}` and
// `{{ "ab".splitlines(1.5) }}` render instead of raising "cannot be interpreted
// as an integer". Corpus: errors/sort_reverse_none, errors/method_none_keepends.
func (v PythonVersion) BoolArgsAreTruthy() bool { return v.AtLeast(Python312) }

// IndexAcceptsWideInt reports whether an index too large for a C int is taken
// rather than refused.
//
// 3.12 stopped raising "Python int too large to convert to C int" for these.
// Corpus: errors/index_overflow_c_int_sort and its two neighbours.
func (v PythonVersion) IndexAcceptsWideInt() bool { return v.AtLeast(Python312) }

// UnifiedRecursionMessage reports whether every recursion error carries the
// same sentence.
//
// Before 3.12 the wording named where the stack ran out -- "in comparison",
// "while calling a Python object" -- which is why docs/divergences.md records
// only two of the three as gradable. 3.12 collapsed them to one.
// Corpus: errors/recursion_include, errors/recursion_extends.
func (v PythonVersion) UnifiedRecursionMessage() bool { return v.AtLeast(Python312) }

// RecursionMessageFor is the sentence a recursion error carries.
//
// Before 3.12 the wording named where inside CPython the stack ran out --
// "while calling a Python object", "in comparison", "while getting the repr of
// an object". 3.12 collapsed them all to one, which is why this takes the
// older wording rather than returning it: the caller knows which of the three
// it is, and from 3.12 on that no longer matters.
// Corpus: errors/recursion_include, errors/recursion_extends.
func (v PythonVersion) RecursionMessageFor(older string) string {
	if v.UnifiedRecursionMessage() {
		return "maximum recursion depth exceeded"
	}
	return older
}

// ClinicNamesTheCallee reports whether a converted-argument error names the
// method it belongs to.
//
// 3.13 turned "must be str, not int" into "count() argument 1 must be str, not
// int", and spells None as None rather than NoneType.
// Corpus: errors/search_arg_bare, errors/search_arg_bare_find.
func (v PythonVersion) ClinicNamesTheCallee() bool { return v.AtLeast(Python313) }

// ArityMessageIsExpected reports whether an arity error reads "expected at most
// N arguments, got M" rather than "takes at most N arguments (M given)".
//
// 3.13. Corpus: errors/method_count_too_many.
func (v PythonVersion) ArityMessageIsExpected() bool { return v.AtLeast(Python313) }

// KeywordMessageNamesTheCallee reports whether an unexpected keyword reads
// "split() got an unexpected keyword argument 'zz'" rather than "'zz' is an
// invalid keyword argument for split()".
//
// 3.13. Corpus: errors/method_invalid_keyword.
func (v PythonVersion) KeywordMessageNamesTheCallee() bool { return v.AtLeast(Python313) }

// WordwrapDropsTheSpaceBeforeABreak reports whether wrapping drops the
// whitespace at a line end even when the word that follows had to be broken
// with nothing left to break into.
//
// textwrap appends a zero-width head in that case, and before 3.13 the
// "drop one trailing whitespace chunk" step removed that empty string rather
// than the space in front of it, so the line kept a trailing space. 3.13 drops
// the space. It is only visible when a long word lands exactly on the width,
// which in practice means an escaped entity: `&#39;` is six characters that
// cannot be split anywhere useful.
// Corpus: layout/wordwrap_escaped.
func (v PythonVersion) WordwrapDropsTheSpaceBeforeABreak() bool { return v.AtLeast(Python313) }

// UnpackErrorNamesTheCount reports whether "too many values to unpack" says
// how many there actually were.
//
// Through 3.13 it named only what was expected -- "(expected 2)" -- while the
// too-few form had said "(expected 2, got 1)" all along. 3.14 made the two
// symmetrical. Corpus: minijinja/loop_bad_unpacking_wrong_len_txt.
func (v PythonVersion) UnpackErrorNamesTheCount() bool { return v.AtLeast(Python314) }

// UnhashableNamesTheUse reports whether an unhashable value says what it was
// about to be used as.
//
// 3.14 turned "unhashable type: 'list'" into "cannot use 'list' as a dict key
// (unhashable type: 'list')", with "a set element" for the other use. The use
// is not something the hashing code knows, so it is passed in alongside the
// version. Corpus: errors/is_filter_unhashable, errors/round_method_unhashable.
func (v PythonVersion) UnhashableNamesTheUse() bool { return v.AtLeast(Python314) }

// ContainerMessageIsLonger reports whether `in` against a non-container reads
// "argument of type 'T' is not a container or iterable".
//
// 3.14. Corpus: classes/global_range_contains and its four neighbours.
func (v PythonVersion) ContainerMessageIsLonger() bool { return v.AtLeast(Python314) }

// IndexMessageIsGeneric reports whether list.index reads "list.index(x): x not
// in list" rather than naming the value that was missing.
//
// 3.14. Corpus: errors/list_index_missing, errors/seq_index_window_empty.
func (v PythonVersion) IndexMessageIsGeneric() bool { return v.AtLeast(Python314) }

// PercentCNamesTheType reports whether %c against the wrong type names it.
//
// 3.14 turned "%c requires int or char" into "%c requires an int or a unicode
// character, not float". Corpus: errors/percent_c_type, errors/markup_percent_c.
func (v PythonVersion) PercentCNamesTheType() bool { return v.AtLeast(Python314) }

// UnifiedDivisionByZero reports whether every division by zero carries the same
// sentence.
//
// Before 3.14 the wording named the operand kinds. There were six: "division
// by zero", "float division by zero", "integer division or modulo by zero",
// "float floor division by zero", "integer modulo by zero" and "float modulo".
// 3.14 collapsed all of them to "division by zero", and changed the neighbouring
// "0.0 cannot be raised to a negative power" to "zero to a negative power" --
// which is the same release and the same idea, so it lives under this rule
// rather than one of its own.
// Corpus: filters/slice_count_divides_by_zero, errors/round_ceil_underflow.
func (v PythonVersion) UnifiedDivisionByZero() bool { return v.AtLeast(Python314) }

// FloatModuloNamesZero reports whether `1.0 % 0` says what went wrong.
//
// 3.11 and 3.12 answer the bare "float modulo", which names the operation and
// not the fault; 3.13 made it "float modulo by zero", matching its five
// neighbours, and 3.14 then collapsed the lot to "division by zero".
//
// This one was invisible for a while. gojja2 passed "float modulo" as the
// pre-3.14 wording for every version, which is right for two of the four, and
// the corpus reached `1 % 0` but never `1 % 0.0` -- so the only case that could
// have shown it was never asked. Corpus: errors/zero_division_float_mod,
// errors/zero_division_float_mod_lhs, errors/zero_division_float_mod_both.
func (v PythonVersion) FloatModuloNamesZero() bool { return v.AtLeast(Python313) }
