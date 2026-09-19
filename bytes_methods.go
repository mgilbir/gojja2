// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"encoding/hex"
	"math/big"
	"strings"

	"github.com/mgilbir/gojja2/errs"
	"github.com/mgilbir/gojja2/value"
)

// The methods a bytes answers.
//
// bytes had exactly one -- decode -- so `{{ "x".encode().upper() }}` was an
// attribute error on a type the engine otherwise supports fully. These are the
// str methods' counterparts, and they are written out rather than delegated to
// the str ones because three things differ throughout:
//
//   - Case and classification are ASCII only. b"\xc3\x9f".upper() is unchanged
//     where "ß".upper() is "SS"; a byte above 127 has no case, because bytes
//     carries no encoding to interpret it with.
//   - Positions and lengths are bytes, not code points.
//   - Arguments must be bytes-like, and the refusals name bytes rather than
//     str. Several accept an integer as a byte value as well.

// --- ASCII primitives ---------------------------------------------------------

func asciiUpper(b byte) byte {
	if b >= 'a' && b <= 'z' {
		return b - 'a' + 'A'
	}
	return b
}

func asciiLower(b byte) byte {
	if b >= 'A' && b <= 'Z' {
		return b - 'A' + 'a'
	}
	return b
}

func asciiIsUpper(b byte) bool { return b >= 'A' && b <= 'Z' }
func asciiIsLower(b byte) bool { return b >= 'a' && b <= 'z' }
func asciiIsAlpha(b byte) bool { return asciiIsUpper(b) || asciiIsLower(b) }
func asciiIsDigit(b byte) bool { return b >= '0' && b <= '9' }
func asciiIsAlnum(b byte) bool { return asciiIsAlpha(b) || asciiIsDigit(b) }

// asciiIsSpace is what CPython's bytes methods treat as whitespace: the six
// ASCII characters, and no more. A byte above 127 is never space here.
func asciiIsSpace(b byte) bool {
	switch b {
	case ' ', '\t', '\n', '\r', '\v', '\f':
		return true
	}
	return false
}

func mapBytes(s string, f func(byte) byte) value.Value {
	out := []byte(s)
	for i := range out {
		out[i] = f(out[i])
	}
	return value.Bytes(out)
}

// --- argument helpers ---------------------------------------------------------

// bytesLike is the check almost every one of these makes on an argument. The
// message names neither the method nor the parameter, because CPython raises it
// from a helper they all share.
func bytesLike(v value.Value) (string, error) {
	if v.Kind() != value.KindBytes {
		return "", errs.New(errs.TypeError,
			"a bytes-like object is required, not '%s'", v.TypeName())
	}
	return v.AsString(), nil
}

// searchArg is what find, rfind, index, rindex and count accept: a bytes to
// look for, or an integer standing for a single byte.
func searchArg(v value.Value) (string, error) {
	if v.Kind() == value.KindBytes {
		return v.AsString(), nil
	}
	if v.IsInteger() {
		n, fits := v.Int64()
		if !fits || n < 0 || n > 255 {
			return "", errs.New(errs.ValueError, "byte must be in range(0, 256)")
		}
		return string([]byte{byte(n)}), nil
	}
	return "", errs.New(errs.TypeError,
		"argument should be integer or bytes-like object, not '%s'", v.TypeName())
}

// bytesBounds resolves the optional start and end of a search, in bytes.
//
// ok reports whether the window exists at all, which is not the same as being
// empty. A start past the end of the data finds nothing -- b"abc".find(b"", 4)
// is -1 where b"abc".find(b"", 3) is 3 -- and so does an end before the start.
// Clamping the start up to the length instead made an empty needle match at the
// end of every such search: 34 of them in a random sweep, all of this shape.
func bytesBounds(s string, args *value.CallArgs, first int) (from, to int, ok bool, err error) {
	read := func(i, def int, clampUp bool) (int, error) {
		v, got := args.Arg(i)
		if !got || v.IsNone() {
			return def, nil
		}
		idx, err := sliceIndexOf(v)
		if err != nil {
			return 0, err
		}
		if idx < 0 {
			idx += len(s)
			if idx < 0 {
				idx = 0
			}
		}
		if clampUp && idx > len(s) {
			idx = len(s)
		}
		return idx, nil
	}
	if from, err = read(first, 0, false); err != nil {
		return 0, 0, false, err
	}
	if to, err = read(first+1, len(s), true); err != nil {
		return 0, 0, false, err
	}
	if from > len(s) || to < from {
		return 0, 0, false, nil
	}
	return from, to, true, nil
}

// fillByte reads the optional fill character of center, ljust and rjust, which
// must be exactly one byte.
func fillByte(method string, args *value.CallArgs) (byte, error) {
	v, ok := args.Arg(1)
	if !ok {
		return ' ', nil
	}
	if v.Kind() != value.KindBytes || len(v.AsString()) != 1 {
		return 0, errs.New(errs.TypeError,
			"%s() argument 2 must be a byte string of length 1, not %s",
			method, v.TypeName())
	}
	return v.AsString()[0], nil
}

// byteCount and byteReplace are strings.Count and strings.Replace with the
// empty needle counted in bytes.
//
// Go's answer for an empty needle is "one more than the number of *runes*",
// because a Go string is text. A bytes is not, so b"\xc3\xa9".count(b"") is 3
// there and 2 here -- and replace with an empty old inserts between bytes
// rather than between characters.
func byteCount(s, needle string) int {
	if needle != "" {
		return strings.Count(s, needle)
	}
	return len(s) + 1
}

func byteReplace(s, old, repl string, count int) string {
	if old != "" {
		return strings.Replace(s, old, repl, count)
	}
	if count < 0 || count > len(s)+1 {
		count = len(s) + 1
	}
	var b strings.Builder
	b.Grow(len(s) + count*len(repl))
	for i := range len(s) + 1 {
		if i < count {
			b.WriteString(repl)
		}
		if i < len(s) {
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// bytesList wraps the pieces a split produced, charging one element each.
//
// A subject of the caller's length splits into as many pieces as it has words,
// and every method that splits ends here -- so this is the one place that has
// to count them and the one place a deadline can reach.
func bytesList(st *State, parts []string) (value.Value, error) {
	if err := st.ChargeItems(int64(len(parts))); err != nil {
		return value.Undefined, err
	}
	items := make([]value.Value, len(parts))
	for i, p := range parts {
		if err := st.Poll(); err != nil {
			return value.Undefined, err
		}
		items[i] = value.Bytes([]byte(p))
	}
	return value.NewList(items...), nil
}

// --- the table ----------------------------------------------------------------

func registerBytesMethods() map[string]func(*State, value.Value, *value.CallArgs) (value.Value, error) {
	type fn = func(*State, value.Value, *value.CallArgs) (value.Value, error)

	simple := func(f func(string) value.Value) fn {
		return func(_ *State, r value.Value, _ *value.CallArgs) (value.Value, error) {
			return f(r.AsString()), nil
		}
	}
	classify := func(pred func(byte) bool, needCased bool) fn {
		return func(_ *State, r value.Value, _ *value.CallArgs) (value.Value, error) {
			s := r.AsString()
			if s == "" {
				return value.False, nil
			}
			cased := false
			for i := range len(s) {
				if !pred(s[i]) {
					return value.False, nil
				}
				cased = true
			}
			return value.Bool(cased || !needCased), nil
		}
	}

	m := map[string]fn{
		"decode": methodDecode,

		"upper":      simple(func(s string) value.Value { return mapBytes(s, asciiUpper) }),
		"lower":      simple(func(s string) value.Value { return mapBytes(s, asciiLower) }),
		"title":      simple(bytesTitle),
		"capitalize": simple(bytesCapitalize),
		"swapcase": simple(func(s string) value.Value {
			return mapBytes(s, func(b byte) byte {
				if asciiIsUpper(b) {
					return asciiLower(b)
				}
				return asciiUpper(b)
			})
		}),

		"isalpha": classify(asciiIsAlpha, false),
		"isdigit": classify(asciiIsDigit, false),
		"isalnum": classify(asciiIsAlnum, false),
		"isspace": classify(asciiIsSpace, false),
		"isascii": func(_ *State, r value.Value, _ *value.CallArgs) (value.Value, error) {
			s := r.AsString()
			for i := range len(s) {
				if s[i] > 127 {
					return value.False, nil
				}
			}
			// The one predicate that is true of the empty bytes: it
			// asks whether anything is *out* of range, and nothing is.
			return value.True, nil
		},
		"islower": bytesCasedPredicate(asciiIsLower, asciiIsUpper),
		"isupper": bytesCasedPredicate(asciiIsUpper, asciiIsLower),
		"istitle": bytesIsTitle,

		"find":   bytesFind(false, false),
		"rfind":  bytesFind(true, false),
		"index":  bytesFind(false, true),
		"rindex": bytesFind(true, true),
		"count":  bytesCount,

		"startswith": bytesAffix("startswith", strings.HasPrefix),
		"endswith":   bytesAffix("endswith", strings.HasSuffix),

		"replace":      bytesReplace,
		"strip":        bytesTrim(true, true),
		"lstrip":       bytesTrim(true, false),
		"rstrip":       bytesTrim(false, true),
		"split":        bytesSplit(false),
		"rsplit":       bytesSplit(true),
		"splitlines":   bytesSplitlines,
		"partition":    bytesPartition(false),
		"rpartition":   bytesPartition(true),
		"join":         bytesJoin,
		"center":       bytesPad(padCentered),
		"ljust":        bytesPad(padLeftAligned),
		"rjust":        bytesPad(padRightAligned),
		"zfill":        bytesZfill,
		"expandtabs":   bytesExpandtabs,
		"removeprefix": bytesCut(true),
		"removesuffix": bytesCut(false),
		"hex":          bytesHex,
		"fromhex":      bytesFromhex,
		"maketrans":    bytesMaketrans,
		"translate":    bytesTranslate,
	}
	return m
}

func bytesCasedPredicate(want, other func(byte) bool) func(*State, value.Value, *value.CallArgs) (value.Value, error) {
	return func(_ *State, r value.Value, _ *value.CallArgs) (value.Value, error) {
		s := r.AsString()
		cased := false
		for i := range len(s) {
			if other(s[i]) {
				return value.False, nil
			}
			if want(s[i]) {
				cased = true
			}
		}
		return value.Bool(cased), nil
	}
}

// --- case -------------------------------------------------------------------

// bytesTitle titlecases each word, a word being a run of ASCII letters. A digit
// is not cased here, so b"a1b" titlecases to b"A1B".
func bytesTitle(s string) value.Value {
	out := []byte(s)
	prevCased := false
	for i := range out {
		if prevCased {
			out[i] = asciiLower(out[i])
		} else {
			out[i] = asciiUpper(out[i])
		}
		prevCased = asciiIsAlpha(s[i])
	}
	return value.Bytes(out)
}

func bytesCapitalize(s string) value.Value {
	out := []byte(s)
	for i := range out {
		if i == 0 {
			out[i] = asciiUpper(out[i])
			continue
		}
		out[i] = asciiLower(out[i])
	}
	return value.Bytes(out)
}

func bytesIsTitle(_ *State, r value.Value, _ *value.CallArgs) (value.Value, error) {
	s := r.AsString()
	cased, prevCased := false, false
	for i := range len(s) {
		switch {
		case asciiIsUpper(s[i]):
			if prevCased {
				return value.False, nil
			}
			cased, prevCased = true, true
		case asciiIsLower(s[i]):
			if !prevCased {
				return value.False, nil
			}
			cased, prevCased = true, true
		default:
			prevCased = false
		}
	}
	return value.Bool(cased), nil
}

// --- search -----------------------------------------------------------------

func bytesFind(fromRight, raising bool) func(*State, value.Value, *value.CallArgs) (value.Value, error) {
	return func(_ *State, r value.Value, args *value.CallArgs) (value.Value, error) {
		v, _ := args.Arg(0)
		needle, err := searchArg(v)
		if err != nil {
			return value.Undefined, err
		}
		s := r.AsString()
		from, to, ok, err := bytesBounds(s, args, 1)
		if err != nil {
			return value.Undefined, err
		}
		if !ok {
			if raising {
				return value.Undefined, errs.New(errs.ValueError, "subsection not found")
			}
			return value.Int(-1), nil
		}
		window := s[from:to]
		at := strings.Index(window, needle)
		if fromRight {
			at = strings.LastIndex(window, needle)
		}
		if at < 0 {
			if raising {
				// "subsection", not "substring": bytes words this
				// differently from str, and the difference is
				// visible to a template.
				return value.Undefined, errs.New(errs.ValueError, "subsection not found")
			}
			return value.Int(-1), nil
		}
		return value.Int(int64(from + at)), nil
	}
}

func bytesCount(_ *State, r value.Value, args *value.CallArgs) (value.Value, error) {
	v, _ := args.Arg(0)
	needle, err := searchArg(v)
	if err != nil {
		return value.Undefined, err
	}
	s := r.AsString()
	from, to, ok, err := bytesBounds(s, args, 1)
	if err != nil {
		return value.Undefined, err
	}
	if !ok {
		return value.Int(0), nil
	}
	return value.Int(int64(byteCount(s[from:to], needle))), nil
}

// bytesAffix is startswith and endswith, which take a bytes or a tuple of them
// and word their refusal in terms of the method that was called.
func bytesAffix(name string, match func(string, string) bool) func(*State, value.Value, *value.CallArgs) (value.Value, error) {
	return func(_ *State, r value.Value, args *value.CallArgs) (value.Value, error) {
		v, _ := args.Arg(0)
		s := r.AsString()
		from, to, inRange, err := bytesBounds(s, args, 1)
		if err != nil {
			return value.Undefined, err
		}
		window := ""
		if inRange {
			window = s[from:to]
		}

		var candidates []string
		switch {
		case v.Kind() == value.KindBytes:
			candidates = []string{v.AsString()}
		case v.Kind() == value.KindTuple:
			seq, _ := v.Seq()
			for _, item := range seq.Items() {
				if item.Kind() != value.KindBytes {
					return value.Undefined, errs.New(errs.TypeError,
						"a bytes-like object is required, not '%s'", item.TypeName())
				}
				candidates = append(candidates, item.AsString())
			}
		default:
			return value.Undefined, errs.New(errs.TypeError,
				"%s first arg must be bytes or a tuple of bytes, not %s",
				name, v.TypeName())
		}
		if !inRange {
			// A start past the end matches nothing, not even the
			// empty prefix.
			return value.False, nil
		}
		for _, c := range candidates {
			if match(window, c) {
				return value.True, nil
			}
		}
		return value.False, nil
	}
}

// --- transforms ---------------------------------------------------------------

func bytesReplace(st *State, r value.Value, args *value.CallArgs) (value.Value, error) {
	oldV, _ := args.Arg(0)
	old, err := bytesLike(oldV)
	if err != nil {
		return value.Undefined, err
	}
	newV, _ := args.Arg(1)
	repl, err := bytesLike(newV)
	if err != nil {
		return value.Undefined, err
	}
	count, err := indexArg(args, 2, "count", -1, cSSizeT)
	if err != nil {
		return value.Undefined, err
	}
	s := r.AsString()
	if err := st.ChargeBytes(int64(len(s)) + int64(len(repl))); err != nil {
		return value.Undefined, err
	}
	return value.Bytes([]byte(byteReplace(s, old, repl, count))), nil
}

func bytesTrim(left, right bool) func(*State, value.Value, *value.CallArgs) (value.Value, error) {
	return func(_ *State, r value.Value, args *value.CallArgs) (value.Value, error) {
		s := r.AsString()
		cut := ""
		useSpace := true
		if v, ok := args.Arg(0); ok && !v.IsNone() {
			c, err := bytesLike(v)
			if err != nil {
				return value.Undefined, err
			}
			cut, useSpace = c, false
		}
		drop := func(b byte) bool {
			if useSpace {
				return asciiIsSpace(b)
			}
			return strings.IndexByte(cut, b) >= 0
		}
		i, j := 0, len(s)
		if left {
			for i < j && drop(s[i]) {
				i++
			}
		}
		if right {
			for j > i && drop(s[j-1]) {
				j--
			}
		}
		return value.Bytes([]byte(s[i:j])), nil
	}
}

func bytesSplit(fromRight bool) func(*State, value.Value, *value.CallArgs) (value.Value, error) {
	return func(s *State, r value.Value, args *value.CallArgs) (value.Value, error) {
		src := r.AsString()
		limit, err := indexArg(args, 1, "maxsplit", -1, cSSizeT)
		if err != nil {
			return value.Undefined, err
		}
		sepV, given := args.Arg(0)
		if !given || sepV.IsNone() {
			// No separator splits on runs of ASCII whitespace and
			// drops the empties at both ends, so b"  x  ".split()
			// has one element and not three.
			parts, err := splitWhitespace(s, src, limit, fromRight)
			if err != nil {
				return value.Undefined, err
			}
			return bytesList(s, parts)
		}
		sep, err := bytesLike(sepV)
		if err != nil {
			return value.Undefined, err
		}
		if sep == "" {
			return value.Undefined, errs.New(errs.ValueError, "empty separator")
		}
		n := -1
		if limit >= 0 {
			n = limit + 1
		}
		if fromRight {
			parts, err := rsplitN(s, src, sep, n)
			if err != nil {
				return value.Undefined, err
			}
			return bytesList(s, parts)
		}
		return bytesList(s, strings.SplitN(src, sep, n))
	}
}

// splitWhitespace is str.split()'s no-argument form over bytes.
func splitWhitespace(st *State, s string, limit int, fromRight bool) ([]string, error) {
	if fromRight {
		return rsplitWhitespace(st, s, limit)
	}
	var out []string
	i := 0
	for {
		// Once per field, and a field always advances, so the walk
		// yields in the number of pieces rather than at the end of them.
		if err := st.Poll(); err != nil {
			return nil, err
		}
		for i < len(s) && asciiIsSpace(s[i]) {
			i++
		}
		if i == len(s) {
			break
		}
		if limit >= 0 && len(out) == limit {
			out = append(out, s[i:])
			break
		}
		j := i
		for j < len(s) && !asciiIsSpace(s[j]) {
			j++
		}
		out = append(out, s[i:j])
		i = j
	}
	return out, nil
}

func rsplitWhitespace(st *State, s string, limit int) ([]string, error) {
	var out []string
	j := len(s)
	for {
		if err := st.Poll(); err != nil {
			return nil, err
		}
		for j > 0 && asciiIsSpace(s[j-1]) {
			j--
		}
		if j == 0 {
			break
		}
		if limit >= 0 && len(out) == limit {
			out = append(out, s[:j])
			break
		}
		i := j
		for i > 0 && !asciiIsSpace(s[i-1]) {
			i--
		}
		out = append(out, s[i:j])
		j = i
	}
	for l, r := 0, len(out)-1; l < r; l, r = l+1, r-1 {
		out[l], out[r] = out[r], out[l]
	}
	return out, nil
}

// rsplitN is strings.SplitN counting from the right.
func rsplitN(st *State, s, sep string, n int) ([]string, error) {
	if n == 0 {
		return nil, nil
	}
	var out []string
	for n < 0 || len(out) < n-1 {
		if err := st.Poll(); err != nil {
			return nil, err
		}
		at := strings.LastIndex(s, sep)
		if at < 0 {
			break
		}
		out = append(out, s[at+len(sep):])
		s = s[:at]
	}
	out = append(out, s)
	for l, r := 0, len(out)-1; l < r; l, r = l+1, r-1 {
		out[l], out[r] = out[r], out[l]
	}
	return out, nil
}

// bytesSplitlines splits on the three ASCII line boundaries bytes knows --
// \n, \r and \r\n -- and on nothing else. str.splitlines also breaks on the
// vertical tab, the form feed and several Unicode separators; bytes does not,
// because it has no encoding to recognise them in.
func bytesSplitlines(st *State, r value.Value, args *value.CallArgs) (value.Value, error) {
	keep := false
	if v, ok := args.Arg(0); ok {
		n, err := indexOf(v, cInt)
		if err != nil {
			return value.Undefined, err
		}
		keep = n != 0
	}
	s := r.AsString()
	var out []string
	i := 0
	for i < len(s) {
		j := i
		for j < len(s) && s[j] != '\n' && s[j] != '\r' {
			j++
		}
		end := j
		if j < len(s) {
			if s[j] == '\r' && j+1 < len(s) && s[j+1] == '\n' {
				j += 2
			} else {
				j++
			}
		}
		if keep {
			out = append(out, s[i:j])
		} else {
			out = append(out, s[i:end])
		}
		i = j
	}
	return bytesList(st, out)
}

func bytesPartition(fromRight bool) func(*State, value.Value, *value.CallArgs) (value.Value, error) {
	return func(_ *State, r value.Value, args *value.CallArgs) (value.Value, error) {
		v, _ := args.Arg(0)
		sep, err := bytesLike(v)
		if err != nil {
			return value.Undefined, err
		}
		if sep == "" {
			return value.Undefined, errs.New(errs.ValueError, "empty separator")
		}
		s := r.AsString()
		at := strings.Index(s, sep)
		if fromRight {
			at = strings.LastIndex(s, sep)
		}
		if at < 0 {
			// A miss puts the whole string on the side the search
			// came from, which is the end it would have reached.
			if fromRight {
				return value.NewTuple(value.Bytes(nil), value.Bytes(nil),
					value.Bytes([]byte(s))), nil
			}
			return value.NewTuple(value.Bytes([]byte(s)), value.Bytes(nil),
				value.Bytes(nil)), nil
		}
		return value.NewTuple(
			value.Bytes([]byte(s[:at])),
			value.Bytes([]byte(sep)),
			value.Bytes([]byte(s[at+len(sep):])),
		), nil
	}
}

func bytesJoin(st *State, r value.Value, args *value.CallArgs) (value.Value, error) {
	v, _ := args.Arg(0)
	var items []value.Value
	switch {
	case v.Kind() == value.KindBytes:
		// A bytes is itself iterable, and it yields integers -- so
		// joining over one is not a type error at the argument, it is a
		// type error at its first element.
		raw := v.AsString()
		for i := range len(raw) {
			items = append(items, value.Int(int64(raw[i])))
		}
	default:
		seq, ok := v.Seq()
		if !ok {
			return value.Undefined, errs.New(errs.TypeError,
				"can only join an iterable")
		}
		items = seq.Items()
	}
	sep := r.AsString()
	var b strings.Builder
	for i, item := range items {
		if item.Kind() != value.KindBytes {
			return value.Undefined, errs.New(errs.TypeError,
				"sequence item %d: expected a bytes-like object, %s found",
				i, item.TypeName())
		}
		if i > 0 {
			b.WriteString(sep)
		}
		if err := st.ChargeBytes(int64(len(item.AsString())) + int64(len(sep))); err != nil {
			return value.Undefined, err
		}
		b.WriteString(item.AsString())
	}
	return value.Bytes([]byte(b.String())), nil
}

func bytesPad(align padAlign) func(*State, value.Value, *value.CallArgs) (value.Value, error) {
	names := map[padAlign]string{
		padLeftAligned: "ljust", padRightAligned: "rjust", padCentered: "center",
	}
	return func(st *State, r value.Value, args *value.CallArgs) (value.Value, error) {
		width, err := indexArg(args, 0, "width", 0, cSSizeT)
		if err != nil {
			return value.Undefined, err
		}
		fill, err := fillByte(names[align], args)
		if err != nil {
			return value.Undefined, err
		}
		s := r.AsString()
		// Counted in bytes, not code points: padding a two-byte
		// character to a width of ten leaves eight bytes of fill, not
		// nine. strings.Builder over pad() would have counted runes.
		gap := width - len(s)
		if gap <= 0 {
			return value.Bytes([]byte(s)), nil
		}
		if err := st.ChargeBytes(int64(width)); err != nil {
			return value.Undefined, err
		}
		left, right := 0, 0
		switch align {
		case padLeftAligned:
			right = gap
		case padRightAligned:
			left = gap
		default:
			left, right = centerSplit(gap, width)
		}
		out := make([]byte, 0, width)
		for range left {
			out = append(out, fill)
		}
		out = append(out, s...)
		for range right {
			out = append(out, fill)
		}
		return value.Bytes(out), nil
	}
}

func bytesZfill(st *State, r value.Value, args *value.CallArgs) (value.Value, error) {
	width, err := indexArg(args, 0, "width", 0, cSSizeT)
	if err != nil {
		return value.Undefined, err
	}
	s := r.AsString()
	if len(s) >= width {
		return value.Bytes([]byte(s)), nil
	}
	sign := ""
	body := s
	if len(s) > 0 && (s[0] == '+' || s[0] == '-') {
		sign, body = s[:1], s[1:]
	}
	zeros, err := st.repeatString("0", width-len(s))
	if err != nil {
		return value.Undefined, err
	}
	return value.Bytes([]byte(sign + zeros + body)), nil
}

func bytesExpandtabs(st *State, r value.Value, args *value.CallArgs) (value.Value, error) {
	size, err := indexArg(args, 0, "tabsize", 8, cInt)
	if err != nil {
		return value.Undefined, err
	}
	s := r.AsString()
	if err := st.ChargeBytes(int64(len(s))); err != nil {
		return value.Undefined, err
	}
	var b strings.Builder
	col := 0
	for i := range len(s) {
		// The charge above is the allocation, made once; this is the
		// yield, which has to be in the walk. Charging the whole
		// subject up front consults the context once and then runs to
		// the end of it.
		if err := st.Poll(); err != nil {
			return value.Undefined, err
		}
		switch s[i] {
		case '\t':
			n := 0
			if size > 0 {
				n = size - col%size
			}
			gap, err := st.repeatString(" ", n)
			if err != nil {
				return value.Undefined, err
			}
			b.WriteString(gap)
			col += n
		case '\n', '\r':
			b.WriteByte(s[i])
			col = 0
		default:
			b.WriteByte(s[i])
			col++
		}
	}
	return value.Bytes([]byte(b.String())), nil
}

func bytesCut(prefix bool) func(*State, value.Value, *value.CallArgs) (value.Value, error) {
	return func(_ *State, r value.Value, args *value.CallArgs) (value.Value, error) {
		v, _ := args.Arg(0)
		affix, err := bytesLike(v)
		if err != nil {
			return value.Undefined, err
		}
		s := r.AsString()
		if prefix && affix != "" && strings.HasPrefix(s, affix) {
			return value.Bytes([]byte(s[len(affix):])), nil
		}
		if !prefix && affix != "" && strings.HasSuffix(s, affix) {
			return value.Bytes([]byte(s[:len(s)-len(affix)])), nil
		}
		return value.Bytes([]byte(s)), nil
	}
}

// --- hex --------------------------------------------------------------------

func bytesHex(st *State, r value.Value, args *value.CallArgs) (value.Value, error) {
	s := r.AsString()
	if err := st.ChargeBytes(2 * int64(len(s))); err != nil {
		return value.Undefined, err
	}
	sep := ""
	if v, ok := arg(args, 0, "sep"); ok {
		if !v.IsString() {
			return value.Undefined, errs.New(errs.TypeError,
				"sep must be str or bytes, not %s", v.TypeName())
		}
		sep = value.Str(v)
	}
	perSep := 1
	if v, ok := arg(args, 1, "bytes_per_sep"); ok {
		n, err := indexOf(v, cSSizeT)
		if err != nil {
			return value.Undefined, err
		}
		perSep = n
	}
	if sep == "" {
		return value.String(hex.EncodeToString([]byte(s))), nil
	}
	// A negative grouping counts from the left, a positive one from the
	// right, which is what makes hex("-", 2) group the *trailing* bytes.
	group := perSep
	fromRight := group > 0
	if group < 0 {
		group = -group
	}
	if group == 0 {
		return value.String(hex.EncodeToString([]byte(s))), nil
	}
	var parts []string
	if fromRight {
		for end := len(s); end > 0; end -= group {
			start := max(end-group, 0)
			parts = append([]string{hex.EncodeToString([]byte(s[start:end]))}, parts...)
		}
	} else {
		for start := 0; start < len(s); start += group {
			parts = append(parts, hex.EncodeToString([]byte(s[start:min(start+group, len(s))])))
		}
	}
	return value.String(strings.Join(parts, sep)), nil
}

func bytesFromhex(_ *State, _ value.Value, args *value.CallArgs) (value.Value, error) {
	v, _ := args.Arg(0)
	if !v.IsString() {
		return value.Undefined, errs.New(errs.TypeError,
			"fromhex() argument must be str, not %s", v.TypeName())
	}
	out, err := decodeHexIgnoringSpaces(value.Str(v))
	if err != nil {
		return value.Undefined, err
	}
	return value.Bytes(out), nil
}

// decodeHexIgnoringSpaces is bytes.fromhex, which skips ASCII spaces between
// pairs and names the position of the first character it cannot read.
func decodeHexIgnoringSpaces(s string) ([]byte, error) {
	var out []byte
	i := 0
	for i < len(s) {
		if s[i] == ' ' {
			i++
			continue
		}
		if i+1 >= len(s) || !isHexDigit(s[i]) || !isHexDigit(s[i+1]) {
			return nil, errs.New(errs.ValueError,
				"non-hexadecimal number found in fromhex() arg at position %d", i)
		}
		b, _ := hex.DecodeString(s[i : i+2])
		out = append(out, b[0])
		i += 2
	}
	return out, nil
}

func isHexDigit(b byte) bool {
	return asciiIsDigit(b) || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')
}

// --- translate ----------------------------------------------------------------

func bytesMaketrans(_ *State, _ value.Value, args *value.CallArgs) (value.Value, error) {
	fromV, _ := args.Arg(0)
	from, err := bytesLike(fromV)
	if err != nil {
		return value.Undefined, err
	}
	toV, _ := args.Arg(1)
	to, err := bytesLike(toV)
	if err != nil {
		return value.Undefined, err
	}
	if len(from) != len(to) {
		return value.Undefined, errs.New(errs.ValueError,
			"maketrans arguments must have same length")
	}
	table := make([]byte, 256)
	for i := range table {
		table[i] = byte(i)
	}
	for i := range len(from) {
		table[from[i]] = to[i]
	}
	return value.Bytes(table), nil
}

func bytesTranslate(st *State, r value.Value, args *value.CallArgs) (value.Value, error) {
	tableV, _ := args.Arg(0)
	var table string
	if !tableV.IsNone() {
		t, err := bytesLike(tableV)
		if err != nil {
			return value.Undefined, err
		}
		if len(t) != 256 {
			return value.Undefined, errs.New(errs.ValueError,
				"translation table must be 256 characters long")
		}
		table = t
	}
	del := ""
	if v, ok := arg(args, 1, "delete"); ok && !v.IsNone() {
		d, err := bytesLike(v)
		if err != nil {
			return value.Undefined, err
		}
		del = d
	}
	s := r.AsString()
	if err := st.ChargeBytes(int64(len(s))); err != nil {
		return value.Undefined, err
	}
	out := make([]byte, 0, len(s))
	for i := range len(s) {
		if del != "" && strings.IndexByte(del, s[i]) >= 0 {
			continue
		}
		if table == "" {
			out = append(out, s[i])
			continue
		}
		out = append(out, table[s[i]])
	}
	return value.Bytes(out), nil
}

// bigFromBytes is int.from_bytes, reached through an integer receiver because
// a template has no way to name the class.
func bigFromBytes(_ *State, _ value.Value, args *value.CallArgs) (value.Value, error) {
	v, _ := args.Arg(0)
	if v.Kind() != value.KindBytes {
		// int.from_bytes words this as a conversion rather than as a
		// bytes-like requirement, unlike every bytes method.
		return value.Undefined, errs.New(errs.TypeError,
			"cannot convert '%s' object to bytes", v.TypeName())
	}
	raw := v.AsString()
	order := "big"
	if o, ok := arg(args, 1, "byteorder"); ok {
		if !o.IsString() {
			return value.Undefined, errs.New(errs.TypeError,
				"from_bytes() argument 'byteorder' must be str, not %s", o.TypeName())
		}
		order = value.Str(o)
	}
	switch order {
	case "big", "little":
	default:
		return value.Undefined, errs.New(errs.ValueError,
			"byteorder must be either 'little' or 'big'")
	}
	signed := false
	if s, ok := arg(args, 2, "signed"); ok {
		t, err := value.IsTrue(s)
		if err != nil {
			return value.Undefined, err
		}
		signed = t
	}
	be := []byte(raw)
	if order == "little" {
		be = make([]byte, len(raw))
		for i := range raw {
			be[len(raw)-1-i] = raw[i]
		}
	}
	n := new(big.Int).SetBytes(be)
	if signed && len(be) > 0 && be[0]&0x80 != 0 {
		// Two's complement: subtract 2**(8*len).
		n.Sub(n, new(big.Int).Lsh(big.NewInt(1), uint(len(be))*8))
	}
	return value.BigInt(n), nil
}
