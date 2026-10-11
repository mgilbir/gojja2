// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package value

// PythonVersion is the CPython whose behaviour is being reproduced.
//
// "Behaviourally identical to CPython jinja2" leaves a question open that the
// answer depends on: identical on *which* CPython. jinja2 3.1.6 is one library,
// but it runs on an interpreter, and the interpreter decides what `d[1:2]`
// raises, whether `sort(reverse=None)` is an error, how a division by zero is
// worded, and which code points are digits. Across 3.11 to 3.15 a few percent of
// gojja2's corpus cases answer differently, nearly all of them two ways and a
// few three; docs/conformance.md has the measured figures.
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
	Python315 PythonVersion = 315
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
	case Python315:
		return "3.15"
	}
	return "unknown"
}

// Known reports whether v is a version gojja2 reproduces. Anything else is
// refused at New rather than silently treated as the default, because a
// configuration nobody can honour is worse than one that is rejected.
func (v PythonVersion) Known() bool {
	switch v {
	case Python311, Python312, Python313, Python314, Python315:
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

// IssubsetStopsWhenFull reports whether `s.issubset(xs)` for an argument that is
// not a set reads it the way set_intersection does, stopping once every member
// of s has been found.
//
// 3.12 wrote issubset over set_intersection, so an element after that point is
// never hashed: `{'b', 'c'}.issubset(['b', 'c', [1]])` is True there and
// "unhashable type: 'list'" on 3.11, which built the whole set first.
// intersection itself has always stopped early. Corpus:
// sets/argument_issubset_stops_when_full.
func (v PythonVersion) IssubsetStopsWhenFull() bool { return v.AtLeast(Python312) }

// SliceKeysAreHashable reports whether a slice used as a mapping key is a
// KeyError rather than a TypeError.
//
// 3.12 made slice objects hashable, so `{{ d[0:1] }}` stopped being
// "unhashable type: 'slice'" and became a KeyError naming the slice:
// `slice(0, 1, None)`. Corpus: errshape/slice_of_a_dict, errors/truncate_dict.
func (v PythonVersion) SliceKeysAreHashable() bool { return v.AtLeast(Python312) }

// ProxyHashNamesTheMapping reports whether hashing a read-only proxy complains
// about the *mapping behind it*. 3.11 named the proxy -- "unhashable type:
// 'mappingproxy'" -- and 3.12 reaches through to the dict. 3.14 keeps that as
// the inner half of its longer form, where the outer name is the proxy's:
// "cannot use 'mappingproxy' as a dict key (unhashable type: 'dict')".
// Corpus: dictview/mapping_is_unhashable.
func (v PythonVersion) ProxyHashNamesTheMapping() bool { return v.AtLeast(Python312) }

// MinusOneFitsInZeroBytes reports whether `(-1).to_bytes(0, signed=True)` is
// b” rather than an OverflowError.
//
// Zero bytes hold no value at all, and -1 was accepted there until 3.13 --
// every other length has the range CPython documents, and 0 fits in zero bytes
// in every version. Corpus: methods/to_bytes_signed_zero_length_negative.
func (v PythonVersion) MinusOneFitsInZeroBytes() bool { return !v.AtLeast(Python313) }

// BoolArgsAreTruthy reports whether an argument declared as a bool is tested
// for truth rather than coerced to an integer.
//
// 3.12 relaxed the argument clinic, so `{{ x|sort(reverse=none) }}` and
// `{{ "ab".splitlines(1.5) }}` render instead of raising "cannot be interpreted
// as an integer" -- and the other face of the same change is that a value past a
// C int is taken rather than overflowing, which is why
// `{{ [3,1]|sort(reverse=2147483648) }}` renders from 3.12 on. There was a
// second rule for that half, IndexAcceptsWideInt, which nothing ever called;
// TestEveryVersionRuleIsConsulted is what found it.
// Corpus: errors/sort_reverse_none, errors/method_none_keepends,
// errors/index_overflow_c_int_sort, errors/index_overflow_c_int_negative,
// errors/index_overflow_c_int_splitlines.
func (v PythonVersion) BoolArgsAreTruthy() bool { return v.AtLeast(Python312) }

// IntHasIsInteger reports whether int carries is_integer().
//
// 3.12 added it, answering True for every int, so that a caller can ask the
// question of a number without first knowing which kind it is. Before that it
// is a float method only, and `{{ (0).is_integer() }}` raises the ordinary
// missing-attribute error. Corpus: methods/int_is_integer.
func (v PythonVersion) IntHasIsInteger() bool { return v.AtLeast(Python312) }

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

// FillCharMessageNamesTheLength reports whether a bytes padding method says how
// long the fill it was given actually was.
//
// Through 3.13: "center() argument 2 must be a byte string of length 1, not
// bytes". 3.14 adds a colon after the name and the length it got: "center():
// argument 2 must be a byte string of length 1, not a bytes object of length 2".
//
// Only for a bytes of the wrong length. A fill that is not a bytes at all keeps
// the older wording on every version -- `b.rjust(10, 1)` is "rjust() argument 2
// must be a byte string of length 1, not int" in 3.14 too -- and the str
// methods say "The fill character must be exactly one character long"
// throughout. Corpus: errors/bytes_center_fill_length,
// errors/bytes_ljust_fill_length, errors/bytes_rjust_fill_not_bytes.
func (v PythonVersion) FillCharMessageNamesTheLength() bool { return v.AtLeast(Python314) }

// TranslateTableMessagesLoseASpace reports whether the two complaints
// str.maketrans makes about a key are spelled a space short.
//
// 3.14 has "keys in translate table mustbe strings or integers" and "string
// keys in translatetable must be of length 1" -- each missing the space a C
// string concatenation used to supply, and each upstream's own. They are the
// only two that changed: translate's "character mapping must be in
// range(0x110000)" and "character mapping must return integer, None or str"
// read the same on every interpreter.
// Corpus: errors/maketrans_key_is_a_float, errors/maketrans_long_string_key.
func (v PythonVersion) TranslateTableMessagesLoseASpace() bool { return v.AtLeast(Python314) }

// FromhexTakesBytesLike reports whether bytes.fromhex accepts a bytes argument
// as well as a str, and says so when it refuses one.
//
// 3.14 widened it: `bytes.fromhex(b'61')` is b'a' there and a TypeError before,
// the refusal became "fromhex() argument must be str or bytes-like, not int",
// and None in that message went back to being spelled NoneType.
// Corpus: bytes/fromhex_not_a_string and its neighbours.
func (v PythonVersion) FromhexTakesBytesLike() bool { return v.AtLeast(Python314) }

// FromhexCountsTheDigits reports whether a hex string that ends mid-pair is
// refused as an odd number of digits rather than at a position.
//
// 3.14 replaced "non-hexadecimal number found in fromhex() arg at position 1"
// with "fromhex() arg must contain an even number of hexadecimal digits" -- but
// only for a pair cut short by the end of the input. A pair spoiled by an actual
// character still reports that character's position, on every version.
// Corpus: bytes/fromhex_short_pair, bytes/fromhex_one_digit.
func (v PythonVersion) FromhexCountsTheDigits() bool { return v.AtLeast(Python314) }

// DictUpdateNamesTheElement reports whether an element of a dict-update
// sequence that cannot be iterated is reported as "cannot convert dictionary
// update sequence element #N to a sequence".
//
// 3.14 replaced it with a bare "object is not iterable" -- no index, and no type
// name either, which is unusual enough to be worth writing down.
//
// The ValueError beside it, for an element of the right kind and the wrong
// length, did *not* change: "dictionary update sequence element #0 has length 3;
// 2 is required" is the same in every version. The two halves of unpackDictPair
// moved apart, so they are versioned apart. Assuming a neighbouring pair of
// messages moved together is how bytes ended up a version ahead of itself in
// the constructor work.
// Corpus: errors/dict_update_element_not_iterable and its neighbours.
func (v PythonVersion) DictUpdateNamesTheElement() bool { return !v.AtLeast(Python314) }

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
// in list" rather than naming the value that was missing -- and range.index,
// on its arithmetic path, "range.index(x): x not in range".
//
// 3.14. Corpus: errors/list_index_missing, errors/seq_index_window_empty,
// methods/range_index_miss_int and its four neighbours.
func (v PythonVersion) IndexMessageIsGeneric() bool { return v.AtLeast(Python314) }

// PercentCNamesTheType reports whether %c against the wrong type names it.
//
// 3.14 turned "%c requires int or char" into "%c requires an int or a unicode
// character, not float", and did the same to the bytes wording -- "%c requires
// an integer in range(256) or a single byte" gains ", not list". Only the str
// half was version-split. Corpus: errors/percent_c_type, errors/markup_percent_c,
// format/percent_error_bytes_c_two_bytes.
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

// RandrangeNamesItsBounds reports whether an empty range says which call it
// was, rather than which step it computed.
//
// 3.11 answers "empty range for randrange() (5, 3, -2)" -- the two bounds and
// the width between them, which is negative and says nothing a reader wanted.
// 3.12 made it "empty range in randrange(5, 3)".
//
// A template reaches it through `lipsum(min=5, max=3)`, which jinja2 passes
// straight to random.randrange. gojja2 carried the 3.11 wording for every
// interpreter, including the pinned one, and nothing said so: the message had
// never been produced by a corpus case, and an ungraded message reads as
// agreement in every column of the version matrix.
// Corpus: errors/lipsum_empty_range, errors/lipsum_equal_bounds.
func (v PythonVersion) RandrangeNamesItsBounds() bool { return v.AtLeast(Python312) }

// SliceIndexMessageOmitsNone reports whether a slice bound that is not an
// integer is refused as "slice indices must be integers or have an __index__
// method" rather than "... integers or None or have an __index__ method".
//
// 3.15 dropped the "or None" from the sentence _PyEval_SliceIndex raises --
// None is still accepted as a bound, the message just stopped saying so. It is
// the sentence a subscript, str.find's bounds, |truncate's cut, |urlize's
// trim and textwrap's break all share. list.index's bounds never said "None"
// and are unchanged. Corpus: errshape/slice_index_types,
// methods/search_bounds_reject_float, errors/truncate_length_unwhole,
// errors/urlize_limit_float, errshape/wordwrap_width_float_slice.
func (v PythonVersion) SliceIndexMessageOmitsNone() bool { return v.AtLeast(Python315) }

// ProxyOrderingIsRefused reports whether a mappingproxy answers `<`, `<=`, `>`
// and `>=` with NotImplemented rather than handing them to the mapping it
// wraps.
//
// Through 3.14 every comparison was delegated, so a proxy over a string
// ordered as the string -- `C('ab') < 'b'` is True -- and the refusal for one
// over a dict named the dict: "'<' not supported between instances of 'dict'
// and 'int'". 3.15 delegates only == and !=, so ordering a proxy is refused by
// the operator itself, naming the proxy, and the reflected operand is tried
// first: `1.5 >= C({})` is "'>=' not supported between instances of 'float'
// and 'mappingproxy'". Corpus: dictview/proxy_orders_over_a_string,
// dictview/proxy_orders_sorted, dictview/proxy_orders_left_refused,
// value/set_compare_refused_set_and_mapping_proxy.
func (v PythonVersion) ProxyOrderingIsRefused() bool { return v.AtLeast(Python315) }

// HexGroupIsSsizeT reports whether bytes.hex's group size is read as a
// Py_ssize_t rather than a C int.
//
// 3.15 widened bytes_per_sep, so a group past a C int but inside a Py_ssize_t
// is simply a group larger than the data -- no separator at all -- and one past
// that overflows naming ssize_t. Corpus: bytes/hex_group_past_a_c_int,
// bytes/hex_group_below_the_c_int_floor, bytes/hex_group_past_an_ssize_t.
func (v PythonVersion) HexGroupIsSsizeT() bool { return v.AtLeast(Python315) }

// ToBytesSaysLengthCannotBeNegative reports whether int.to_bytes refuses a
// negative length as "length cannot be negative" rather than "length argument
// must be non-negative". 3.15. Corpus: errors/to_bytes_negative_length.
func (v PythonVersion) ToBytesSaysLengthCannotBeNegative() bool { return v.AtLeast(Python315) }

// KwargsSplatMessageIsGeneric reports whether `f(**x)` with a non-mapping x
// reads "Value after ** must be a mapping, not int" rather than naming the
// callee: "jinja2.filters.do_join() argument after ** must be a mapping, not
// int". 3.15. Corpus: errshape/dyn_kwargs_names_a_builtin,
// errshape/dyn_kwargs_names_the_filter, folding/kwargs_splat_none.
func (v PythonVersion) KwargsSplatMessageIsGeneric() bool { return v.AtLeast(Python315) }

// DuplicateParameterIsAParameter reports whether compiling a function with two
// parameters of one name says "duplicate parameter" rather than "duplicate
// argument" -- one of the 3.15 terminology fixes (gh-133382), and a macro with
// a repeated parameter reaches it because jinja2 compiles the macro to a
// Python function. That case is a recorded divergence -- jinja2's message
// names the identifier it generated -- so it cannot grade the word;
// TestDuplicateMacroParameterFollowsThePythonVersion does.
// Corpus: macro/duplicate_parameter.
func (v PythonVersion) DuplicateParameterIsAParameter() bool { return v.AtLeast(Python315) }

// LibraryRepeatOverflowNamesSsizeT reports whether a str repeated by a count
// too wide for an index, *inside jinja2's or the standard library's own Python
// code*, overflows as "Python int too large to convert to C ssize_t" rather
// than "cannot fit 'int' into an index-sized integer".
//
// This is not a reworded message. 3.15's specialising interpreter has a
// str * int form of BINARY_OP that converts the count with PyLong_AsSsize_t,
// and the generic path it replaces still says "cannot fit". Which one answers
// depends on whether that one bytecode instruction has run before: the first
// execution is generic, every later one specialised. A site in jinja2's
// filters or in json.encoder has, in any process that has rendered anything --
// and in the oracle it has always run at least once, because jinja2's constant
// folder calls the filter at compile time before the render calls it again --
// so `|indent` and `|tojson` answer the specialised wording.
//
// A site in the *template* is compiled fresh, so `{{ 'a' * n }}` executed once
// is generic, and the same expression in a loop is generic once and
// specialised after. gojja2 answers the generic wording there; reproducing the
// loop would mean keeping per-site state that outlives a render, which is
// recorded as a divergence. Corpus: filters/tojson_indent_overflows,
// filters/indent_width_overflows.
func (v PythonVersion) LibraryRepeatOverflowNamesSsizeT() bool { return v.AtLeast(Python315) }

// PercentErrorsNameTheArgument reports whether printf-style formatting says
// which argument an error is about, and words the complaint after the verb.
//
// 3.15 rewrote the family. Each refusal of an argument opens with "format
// argument: " for a lone one, "format argument 2: " for the second of a tuple,
// or "format argument 'k': " for a mapping key -- and the sentence after it
// changed too: "%d format: a real number is required, not str" became "%d
// requires a real number, not str", "%c requires an int or a unicode
// character" became "... an integer or ...", "%c arg not in range(256)"
// became "%c argument not in range(256)", "* wants int" became "* requires
// int, not str", a `*` too wide is "too big for width", and the str side's
// "must be real number, not str" for %f is "%f requires a real number, not
// str". The class each one names is %T's: qualified, and unquoted, so %b's
// "not 'Undefined'" is "not jinja2.runtime.Undefined". A Markup format's
// arguments are markupsafe's helper, so that is the class named there.
// Corpus: errors/percent_c_type, errors/percent_d_string, errors/percent_x_float,
// errors/markup_percent_c, errors/bytes_percent_s_wants_bytes,
// format/percent_verb_bytes_float_of_str, format/percent_error_star_named_by_position,
// format/percent_precision_too_big_star_precision_c_int,
// format/percent_error_named_by_key.
func (v PythonVersion) PercentErrorsNameTheArgument() bool { return v.AtLeast(Python315) }

// PercentErrorsGivePositions reports whether a malformed printf-style format
// says where it went wrong.
//
// Through 3.14: "incomplete format", "incomplete format key", "width too big",
// "precision too big", and "unsupported format character 'q' (0x71) at index
// 1" with the index of the character. 3.15 says where the '%' was instead --
// "stray % at position 0", "stray % or incomplete format key at position 0",
// "width too big at position 0" -- and words an unknown character by what it
// is: a letter is "unsupported format %q at position 0", and anything else is
// "stray % at position 0 or unexpected format character '!' at position 1",
// spelled by code point when it is not printable ASCII. Corpus:
// format/percent_error_incomplete, format/percent_error_incomplete_key,
// format/percent_precision_too_big_d, errshape/format_char,
// errshape/format_percent_width, format/percent_unsupported_non_ascii.
func (v PythonVersion) PercentErrorsGivePositions() bool { return v.AtLeast(Python315) }

// PercentErrorsCountArguments reports whether printf-style formatting says how
// many arguments it had when it ran out or had some left over, and what it got
// when it wanted a mapping: "not enough arguments for format string (got 1)",
// "not all arguments converted during string formatting (required 1, got 2)",
// "format requires a mapping, not int". 3.15. Corpus:
// format/percent_error_not_enough_arguments, methods/percent_too_many_arguments,
// format/percent_error_mapping_required.
func (v PythonVersion) PercentErrorsCountArguments() bool { return v.AtLeast(Python315) }

// PercentStarNeedsATuple reports whether a `*` width or precision may only be
// taken from a tuple of arguments.
//
// Through 3.14 a lone argument could stand for a `*`: `'%*s' % ['a', 'b']`
// took the list as the width and said "* wants int". 3.15 asks only a tuple, so
// a lone argument is "not enough arguments for format string (got 1)". A
// Markup format's lone argument is a one-element tuple to CPython, so it is
// still drawn there. Corpus: format/percent_error_star_wants_int,
// format/percent_error_star_precision_wants_int.
func (v PythonVersion) PercentStarNeedsATuple() bool { return v.AtLeast(Python315) }

// PercentKeyExcludesPositional reports whether a conversion without a mapping
// key, or a `*`, after one with a key is refused as malformed.
//
// A `%(name)s` makes the looked-up value the whole argument source. Through
// 3.14 that left a later `%s` with nothing to take -- "not enough arguments
// for format string" -- and let a `*` in the keyed conversion take the value
// itself as the width. 3.15 refuses both by what they are: "format requires a
// parenthesised mapping key at position 6" and "* cannot be used with a
// parenthesised mapping key at position 0". Corpus:
// format/percent_positional_after_a_key, format/percent_star_after_a_key,
// format/percent_star_precision_after_a_key.
func (v PythonVersion) PercentKeyExcludesPositional() bool { return v.AtLeast(Python315) }

// BytesFloatConvertsLikeStr reports whether a bytes format's float verbs let the
// conversion's own error through, as the str side does.
//
// Through 3.14 bytes formatfloat replaced anything float() raised with "float
// argument required, not T" -- so an undefined never reported itself, and an
// int too wide for a float was a TypeError. 3.15 keeps only the TypeError's
// replacement: an undefined raises its own UndefinedError and the wide int is
// "int too large to convert to float". Corpus:
// format/percent_bytes_float_of_an_undefined_default,
// format/percent_verb_bytes_float_of_wide_int.
func (v PythonVersion) BytesFloatConvertsLikeStr() bool { return v.AtLeast(Python315) }

// IntDigitLimitAfterValidation reports whether int() of decimal text checks the
// whole string before it counts the digits.
//
// 3.11 counted the leading run of digits and refused it at once if there were
// more than MaxStrDigits, so `int('1' * 4301 + 'x')` was "Exceeds the limit
// (4300 digits) for integer string conversion: value has 4301 digits". 3.12
// validates first, and the same text is "invalid literal for int() with base
// 10". A template reaches it through Markup's %d, which is int() of the
// argument -- `{{ ("%d"|safe) % s }}` -- and through int's own class. Corpus:
// intstr/markup_percent_d_digit_limit_before_junk,
// intstr/int_class_digit_limit_before_junk.
func (v PythonVersion) IntDigitLimitAfterValidation() bool { return v.AtLeast(Python312) }

// FractionalGrouping reports whether the format mini-language takes a ',' or
// an '_' after the precision, separating the digits after the point into
// threes counted from the point: `'{:.,f}'.format(1234.5678912)` is
// "1234.567,891" and `{:,.,f}` groups both halves.
//
// 3.14 added it (gh-87790). The separator may stand in for the digits, so
// before 3.14 `{:.,f}` is "Format specifier missing precision" and `{:.3,f}`
// leaves two characters where the type goes and is an invalid specifier.
// From 3.14 the same grammar refuses `{:.,_f}` as "both", a separator before
// the digits (`{:._3f}`) as an invalid specifier, and 'n' with either
// separator; an integer or a string reads it and ignores it, so `{:.,d}` of
// 1234 is "1234". Corpus: format/fraction_grouped,
// format/fraction_grouped_padding, format/fraction_grouped_ignored,
// errors/format_fraction_group_with_n_on_an_int,
// errors/format_fraction_group_comma_then_underscore,
// errors/format_fraction_group_before_digits.
func (v PythonVersion) FractionalGrouping() bool { return v.AtLeast(Python314) }
