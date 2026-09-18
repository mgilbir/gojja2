// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"math"
	"math/big"
	"math/rand/v2"
	"strings"

	"github.com/mgilbir/gojja2/errs"
	"github.com/mgilbir/gojja2/value"
)

func registerDefaultGlobals(env *Environment) {
	env.AddGlobal("range", Func("range", globalRange))
	env.AddGlobal("dict", Func("dict", globalDict))
	env.AddGlobal("namespace", Func("namespace", globalNamespace))
	env.AddGlobal("cycler", Func("cycler", globalCycler))
	env.AddGlobal("joiner", Func("joiner", globalJoiner))
	env.AddGlobal("lipsum", Func("lipsum", globalLipsum))
}

// rangeObject is Python's range: a sequence with a known length that holds no
// elements, so `{% for i in range(10000000) %}` costs nothing to set up.
//
// The bounds are arbitrary precision, because a template can observe them
// exactly even when the range is far too long to walk: `{{ range(2**70) }}`
// prints them, `{{ 3 in range(2**70) }}` decides by arithmetic, and
// `{{ range(2**70)|first }}` wants one element. Narrowing them to an int64
// refused all three.
//
// They are *stored* as an int64 whenever all three fit, which is every range
// anyone writes on purpose, so that indexing and iteration stay arithmetic on
// machine integers and allocate nothing.
type rangeObject struct {
	// start, stop and step are the bounds, valid when wide is nil.
	start, stop, step int64
	// wide carries them when any one of them does not fit an int64.
	wide *wideRange
	// length is the element count, computed once. A range is immutable and
	// a loop asks Len() once per element, so deriving it on demand cost
	// several big.Int allocations for every iteration of every loop.
	length *big.Int
	// n is length clamped into an int, which is what indexing needs.
	n int
}

// wideRange holds bounds too large for an int64. Its fields are shared with
// the values they came from and must never be mutated in place.
type wideRange struct{ start, stop, step *big.Int }

// newRange builds a range from exact bounds, choosing the representation and
// computing the length once.
func newRange(start, stop, step *big.Int) *rangeObject {
	r := &rangeObject{length: rangeLen(start, stop, step)}
	if start.IsInt64() && stop.IsInt64() && step.IsInt64() {
		r.start, r.stop, r.step = start.Int64(), stop.Int64(), step.Int64()
	} else {
		r.wide = &wideRange{start: start, stop: stop, step: step}
	}
	// A range longer than maxInt cannot be walked under any budget, so the
	// clamp is unobservable except through len(), which uses BigLen.
	if r.length.IsInt64() && r.length.Int64() <= int64(math.MaxInt) {
		r.n = int(r.length.Int64())
	} else {
		r.n = math.MaxInt
	}
	return r
}

// bounds returns the bounds as big.Ints. For a narrow range they are freshly
// allocated; for a wide one they are the stored pointers, which callers read
// and never mutate.
func (r *rangeObject) bounds() (start, stop, step *big.Int) {
	if r.wide != nil {
		return r.wide.start, r.wide.stop, r.wide.step
	}
	return big.NewInt(r.start), big.NewInt(r.stop), big.NewInt(r.step)
}

// rangeLen is the exact element count, computed the way CPython computes it:
// (stop - start + step -+ 1) // step, clamped at zero.
//
// It has to be arbitrary precision. `range(-2**63, 2**63-1)` holds 2**64-1
// elements, and the obvious int64 form of this expression overflows and wraps
// negative -- which read as a length of -1, made `|length` render -1 and made
// the loop run zero times.
func rangeLen(start, stop, step *big.Int) *big.Int {
	span := new(big.Int).Sub(stop, start)
	adjust := big.NewInt(-1)
	if step.Sign() < 0 {
		adjust = big.NewInt(1)
	}
	// span + step - sign(step), then truncated division by step.
	span.Add(span, step)
	span.Add(span, adjust)
	if span.Sign() == 0 {
		return big.NewInt(0)
	}
	n := new(big.Int).Quo(span, step)
	if n.Sign() < 0 {
		return big.NewInt(0)
	}
	return n
}

// BigLen reports the exact length, which len() must render even when it does
// not fit in an int.
func (r *rangeObject) BigLen() *big.Int { return r.length }

// Len is the length clamped into an int, which is what indexing and iteration
// need.
func (r *rangeObject) Len() int { return r.n }

func (r *rangeObject) GetIndex(i int) (value.Value, bool) {
	if i < 0 || i >= r.n {
		return value.Undefined, false
	}
	if r.wide == nil {
		// i < n and every element lies between start and stop, so this
		// cannot overflow.
		return value.Int(r.start + int64(i)*r.step), true
	}
	e := new(big.Int).Mul(r.wide.step, big.NewInt(int64(i)))
	return value.BigInt(e.Add(e, r.wide.start)), true
}

// bound returns one of the three bounds as a value, which is what the start,
// stop and step attributes answer.
func (r *rangeObject) bound(narrow int64, wide func(*wideRange) *big.Int) value.Value {
	if r.wide == nil {
		return value.Int(narrow)
	}
	return value.BigInt(wide(r.wide))
}

func (r *rangeObject) GetAttr(name string) (value.Value, bool) {
	switch name {
	case "start":
		return r.bound(r.start, func(w *wideRange) *big.Int { return w.start }), true
	case "stop":
		return r.bound(r.stop, func(w *wideRange) *big.Int { return w.stop }), true
	case "step":
		return r.bound(r.step, func(w *wideRange) *big.Int { return w.step }), true
	}
	return value.Undefined, false
}

// Slice returns the sub-range a slice selects. Slicing a range in Python
// yields another range rather than a list, so `range(3)[1:]` renders
// "range(1, 3)" and not "[1, 2]".
func (r *rangeObject) Slice(start, stop, step *int) (value.Value, error) {
	begin, end, st, err := value.SliceBounds(r.n, start, stop, step)
	if err != nil {
		return value.Undefined, err
	}
	// The bounds are positions within this range, so they map back onto
	// the original start and step: position p stands for start + p*step.
	bs, _, bstep := r.bounds()
	at := func(pos int) *big.Int {
		v := new(big.Int).Mul(bstep, big.NewInt(int64(pos)))
		return v.Add(v, bs)
	}
	return value.FromObject(newRange(
		at(begin), at(end), new(big.Int).Mul(bstep, big.NewInt(int64(st))),
	)), nil
}

// Contains decides `x in range(...)` by arithmetic, as Python's range does.
//
// Falling through to the generic scan makes membership cost the length of the
// range: `{{ -1 in range(9223372036854775807) }}` walked toward nine quintillion
// elements, and a three-second deadline was still running ninety seconds later.
//
// A range holds nothing but integers, and no value in this model can compare
// equal to an integer without being a number itself, so every other value is
// answered False without looking. CPython only takes this path for an exact
// int and scans for anything else; the answer is the same either way, so the
// arithmetic is used for every number that is one.
func (r *rangeObject) Contains(item value.Value) (found, known bool) {
	n, ok := integerOf(item)
	if !ok {
		// Not an integer -- a float with a fraction, a string, a list.
		// None of them can equal an element of a range.
		return false, true
	}
	start, stop, step := r.bounds()
	offset := new(big.Int).Sub(n, start)
	// Before the start, or at or past the stop, in the step's direction.
	if step.Sign() > 0 {
		if offset.Sign() < 0 || n.Cmp(stop) >= 0 {
			return false, true
		}
	} else {
		if offset.Sign() > 0 || n.Cmp(stop) <= 0 {
			return false, true
		}
	}
	return new(big.Int).Rem(offset, step).Sign() == 0, true
}

// integerOf reports the exact integer a value stands for: an int, a bool, or a
// float with no fractional part. Python's `1.0 in range(3)` is True.
func integerOf(v value.Value) (*big.Int, bool) {
	if v.IsInteger() {
		return v.BigInt()
	}
	if v.Kind() != value.KindFloat {
		return nil, false
	}
	f := v.AsFloat()
	if math.IsInf(f, 0) || math.IsNaN(f) || f != math.Trunc(f) {
		return nil, false
	}
	n, _ := big.NewFloat(f).Int(nil)
	return n, true
}

// Equals compares ranges the way Python does: by the sequence they stand for,
// so range(0, 3, 2) and range(0, 4, 2) are equal despite differing stops.
func (r *rangeObject) Equals(other value.Value) (bool, bool) {
	o, ok := other.Interface().(*rangeObject)
	if !ok {
		return false, other.Kind() == value.KindObject || other.Kind() == value.KindList
	}
	// By the sequence, so the exact length decides rather than the clamped
	// one: two ranges longer than an int are not equal merely because both
	// clamp to the same maximum.
	if r.length.Cmp(o.length) != 0 {
		return false, true
	}
	if r.length.Sign() == 0 {
		return true, true
	}
	rStart, _, rStep := r.bounds()
	oStart, _, oStep := o.bounds()
	if rStart.Cmp(oStart) != 0 {
		return false, true
	}
	return r.length.Cmp(big.NewInt(1)) == 0 || rStep.Cmp(oStep) == 0, true
}

func (r *rangeObject) TypeName() string { return "range" }

func (r *rangeObject) Repr() string {
	start, stop, step := r.bounds()
	var b strings.Builder
	b.WriteString("range(")
	b.WriteString(value.Repr(value.BigInt(start)))
	b.WriteString(", ")
	b.WriteString(value.Repr(value.BigInt(stop)))
	if step.Cmp(big.NewInt(1)) != 0 {
		b.WriteString(", ")
		b.WriteString(value.Repr(value.BigInt(step)))
	}
	b.WriteByte(')')
	return b.String()
}

func globalRange(s *State, args *value.CallArgs) (value.Value, error) {
	// range is a C function, and it checks in this order: keywords at all,
	// then the count, then each argument. Every step matters -- a keyword
	// beats a wrong count, and a wrong count beats an argument that is not
	// an integer, so `range("x",1,2,3)` is about the count and not the
	// string. Checking the count last, after converting, reported the
	// wrong one of the three for every mixed call.
	if len(args.Kwargs) > 0 {
		return value.Undefined, errs.New(errs.TypeError, "%s", rangeKwMessage)
	}
	switch {
	case len(args.Pos) == 0:
		return value.Undefined, errs.New(errs.TypeError, "%s", rangeFewMessage)
	case len(args.Pos) > 3:
		return value.Undefined, errs.New(errs.TypeError, rangeManyMessage, len(args.Pos))
	}
	// The bounds are kept exactly. range() is a C function, but the object
	// it builds holds Python ints, so `range(2**70)` is a legal range whose
	// repr and membership are exact -- it simply cannot be walked far.
	nums := make([]*big.Int, 0, 3)
	for _, v := range args.Pos {
		n, ok := v.BigInt()
		if !ok {
			return value.Undefined, errs.New(errs.TypeError,
				"'%s' object cannot be interpreted as an integer", v.TypeName())
		}
		nums = append(nums, n)
	}
	start, stop, step := big.NewInt(0), big.NewInt(0), big.NewInt(1)
	switch len(nums) {
	case 1:
		stop = nums[0]
	case 2:
		start, stop = nums[0], nums[1]
	case 3:
		start, stop, step = nums[0], nums[1], nums[2]
		if step.Sign() == 0 {
			return value.Undefined, errs.New(errs.ValueError, "range() arg 3 must not be zero")
		}
	}
	return value.FromObject(newRange(start, stop, step)), nil
}

// globalDict builds a dict from an optional mapping plus keyword arguments,
// which keep the order they were written in.
func globalDict(s *State, args *value.CallArgs) (value.Value, error) {
	out := value.NewDict()
	d, _ := out.Dict()
	if len(args.Pos) > 1 {
		return value.Undefined, errs.New(errs.TypeError,
			"dict expected at most 1 argument, got %d", len(args.Pos))
	}
	if len(args.Pos) == 1 {
		// dict() and dict.update() accept exactly the same shapes and
		// refuse them the same way, so they share one implementation.
		if err := updateDictFrom(d, args.Pos[0]); err != nil {
			return value.Undefined, err
		}
	}
	for _, kw := range args.Kwargs {
		d.SetString(kw.Name, kw.Value)
	}
	return out, nil
}

func globalNamespace(s *State, args *value.CallArgs) (value.Value, error) {
	// Namespace(*args, **kwargs) is dict(*args, **kwargs) underneath, so a
	// non-mapping argument fails as an iterable rather than as a mapping.
	built, err := globalDict(s, args)
	if err != nil {
		return value.Undefined, err
	}
	ns := newNamespace()
	d, _ := built.Dict()
	for _, e := range d.Entries() {
		ns.SetAttr(value.Str(e.Key), e.Value)
	}
	return value.FromObject(ns), nil
}

// cyclerObject walks a list of items forever, one call at a time.
type cyclerObject struct {
	items []value.Value
	pos   int
}

func (c *cyclerObject) GetAttr(name string) (value.Value, bool) {
	switch name {
	case "items":
		// jinja2's Cycler stores its rotation here. It is not a method,
		// which is why `cycler(...)|xmlattr` fails calling a tuple.
		return value.NewTuple(c.items...), true
	case "current":
		if len(c.items) == 0 {
			return value.Undefined, true
		}
		return c.items[c.pos], true
	case "next":
		return Func("next", func(_ *State, a *value.CallArgs) (value.Value, error) {
			if err := bindArgs(runtimeSignatures["Cycler.next"], a, 1); err != nil {
				return value.Undefined, err
			}
			return c.next()
		}), true
	case "reset":
		return Func("reset", func(_ *State, a *value.CallArgs) (value.Value, error) {
			if err := bindArgs(runtimeSignatures["Cycler.reset"], a, 1); err != nil {
				return value.Undefined, err
			}
			c.pos = 0
			return value.None, nil
		}), true
	}
	return value.Undefined, false
}

func (c *cyclerObject) next() (value.Value, error) {
	if len(c.items) == 0 {
		return value.Undefined, errs.New(errs.TypeError, "no items for cycling given")
	}
	v := c.items[c.pos]
	c.pos = (c.pos + 1) % len(c.items)
	return v, nil
}

// A Cycler is not callable. jinja2 rotates it through .next(), and giving it a
// Call made `{{ c() }}` advance the cycle where CPython refuses the call
// outright -- a template written against that would silently do nothing on the
// other implementation.
func (c *cyclerObject) TypeName() string      { return "Cycler" }
func (c *cyclerObject) QualifiedName() string { return "jinja2.utils.Cycler" }

// A Cycler defines no __repr__, so it prints as any bare Python object does.
func (c *cyclerObject) Repr() string { return pyObjectRepr(c.QualifiedName(), c) }

func globalCycler(s *State, args *value.CallArgs) (value.Value, error) {
	// Python binds the call before __init__ runs, so a keyword beats the
	// empty-cycle RuntimeError that the body raises.
	if err := bindArgs(runtimeSignatures["Cycler.__init__"], args, 1); err != nil {
		return value.Undefined, err
	}
	if len(args.Pos) == 0 {
		return value.Undefined, errs.New(errs.RuntimeError, "at least one item has to be provided")
	}
	return value.FromObject(&cyclerObject{items: args.Pos}), nil
}

// joinerObject returns nothing the first time it is called and its separator
// every time after, for joining a list whose items are emitted separately.
type joinerObject struct {
	// The separator is whatever was passed, not its string form. jinja2
	// stores it and hands it back, so `joiner(1)` yields the integer 1 and
	// `joiner(none)` yields None -- where forcing a string, or falling back
	// to the default for anything that was not one, printed ", ".
	sep  value.Value
	used bool
}

func (j *joinerObject) GetAttr(string) (value.Value, bool) { return value.Undefined, false }
func (j *joinerObject) TypeName() string                   { return "Joiner" }
func (j *joinerObject) QualifiedName() string              { return "jinja2.utils.Joiner" }
func (j *joinerObject) Repr() string                       { return pyObjectRepr(j.QualifiedName(), j) }

func (j *joinerObject) Call(args *value.CallArgs) (value.Value, error) {
	if err := bindArgs(runtimeSignatures["Joiner.__call__"], args, 1); err != nil {
		return value.Undefined, err
	}
	if !j.used {
		j.used = true
		return value.String(""), nil
	}
	return j.sep, nil
}

func globalJoiner(s *State, args *value.CallArgs) (value.Value, error) {
	if err := bindArgs(runtimeSignatures["Joiner.__init__"], args, 1); err != nil {
		return value.Undefined, err
	}
	sep := value.String(", ")
	if v, ok := arg(args, 0, "sep"); ok {
		sep = v
	}
	return value.FromObject(&joinerObject{sep: sep}), nil
}

// lipsumWords is the vocabulary jinja2 generates placeholder text from.
var lipsumWords = strings.Fields(`a ac accumsan ad adipiscing aenean aliquam aliquet amet
ante aptent arcu at auctor augue bibendum blandit class commodo condimentum congue
consectetuer consequat conubia convallis cras cubilia cum curabitur curae cursus dapibus
diam dictum dictumst dignissim dis dolor donec dui duis egestas eget eleifend elementum
elit enim erat eros est et etiam eu euismod facilisi facilisis fames faucibus felis
fermentum feugiat fringilla fusce gravida habitant habitasse hac hendrerit hymenaeos iaculis
id imperdiet in inceptos integer interdum ipsum justo lacinia lacus laoreet lectus leo
libero ligula litora lobortis lorem luctus maecenas magna magnis malesuada massa mattis
mauris metus mi molestie mollis montes morbi mus nam nascetur natoque nec neque netus nibh
nisi nisl non nonummy nostra nulla nullam nunc odio orci ornare parturient pede pellentesque
penatibus per pharetra phasellus placerat platea porta porttitor posuere potenti praesent
pretium primis proin pulvinar purus quam quis quisque rhoncus ridiculus risus rutrum sagittis
sapien scelerisque sed sem semper senectus sit sociis sociosqu sodales sollicitudin
suscipit suspendisse taciti tellus tempor tempus tincidunt torquent tortor tristique turpis
ullamcorper ultrices ultricies urna ut varius vehicula vel velit venenatis vestibulum vitae
vivamus viverra volutpat vulputate`)

// globalLipsum generates placeholder text.
//
// jinja2 draws from a random source, so this cannot render the same bytes as
// CPython and is excluded from conformance comparison. See docs/divergences.md.
func globalLipsum(s *State, args *value.CallArgs) (value.Value, error) {
	// A plain Python function, so the binding is the ordinary one: an
	// unexpected keyword first, then one a positional already filled, then
	// too many positionals.
	if err := bindArgs(runtimeSignatures["generate_lorem_ipsum"], args, 0); err != nil {
		return value.Undefined, err
	}
	n, err := intArg(args, 0, "n", 5, cSSizeT)
	if err != nil {
		return value.Undefined, err
	}
	html := true
	if v, ok := arg(args, 1, "html"); ok {
		on, err := value.IsTrue(v)
		if err != nil {
			return value.Undefined, err
		}
		html = on
	}
	lo, err := intArg(args, 2, "min", 20, cSSizeT)
	if err != nil {
		return value.Undefined, err
	}
	hi, err := intArg(args, 3, "max", 100, cSSizeT)
	if err != nil {
		return value.Undefined, err
	}
	// jinja2 calls randrange(min, max), which raises on an empty range.
	// Quietly repairing it to lo+1 instead is what used to manufacture the
	// count==0 case that then panicked on text[:1] below: a silently fixed
	// argument turned a clean ValueError into a crash.
	if hi <= lo {
		return value.Undefined, errs.New(errs.ValueError,
			"empty range for randrange() (%d, %d, %d)", lo, hi, hi-lo)
	}
	if n < 0 {
		n = 0
	}
	// randrange(lo, hi) may legitimately be negative -- lipsum(1, true, -5, -1)
	// is a real call, and jinja2 then loops over range(negative), which runs
	// no times and yields an empty paragraph.
	//
	// The width of the range is computed unsigned. hi-lo overflows int64 for
	// a wide enough range -- lipsum(1, true, -2**63, 0) is the smallest case
	// -- and the wrapped negative reaches rand.IntN, which panics on one.
	// Since hi > lo is established above, the unsigned difference is exact.
	span := int64(math.MaxInt32)
	if width := uint64(hi) - uint64(lo); width < uint64(math.MaxInt32) {
		span = int64(width)
	}

	// n paragraphs of at most hi words each, charged before any of them is
	// built. lipsum is a global, and until globals were handed the render
	// state there was no way for one to do this at all.
	if err := s.ChargeItems(saturatingMulInt(int64(n), int64(max(hi, 0)))); err != nil {
		return value.Undefined, err
	}

	paragraphs := make([]string, 0, n)
	for range n {
		count := lo + rand.IntN(int(span))
		if count < 0 {
			count = 0
		}
		// A paragraph is not a flat run of words. jinja2 punctuates it as
		// it goes: a comma once the last one is far enough behind, a
		// full stop once the last of those is, and a capital on the word
		// after each full stop. Both can land on the same word, so
		// "word,." is a shape jinja2 really produces. Emitting one
		// run-on sentence with a single trailing stop was visibly not
		// lorem ipsum.
		words := make([]string, 0, count)
		nextCapitalized := true
		lastComma, lastFullStop := 0, 0
		last := -1
		for idx := range count {
			// jinja2 redraws until the word differs from the one
			// before it. Drawing from the rest is the same
			// distribution and is bounded, where redrawing is not.
			j := rand.IntN(len(lipsumWords))
			if last >= 0 && len(lipsumWords) > 1 {
				if j = rand.IntN(len(lipsumWords) - 1); j >= last {
					j++
				}
			}
			last = j
			word := lipsumWords[j]
			if nextCapitalized {
				// str.capitalize also lowers the rest, which
				// these words already are.
				word = strings.ToUpper(word[:1]) + word[1:]
				nextCapitalized = false
			}
			if idx-(3+rand.IntN(5)) > lastComma {
				lastComma = idx
				lastFullStop += 2
				word += ","
			}
			if idx-(10+rand.IntN(10)) > lastFullStop {
				lastComma, lastFullStop = idx, idx
				word += "."
				nextCapitalized = true
			}
			words = append(words, word)
		}
		text := strings.Join(words, " ")
		// The paragraph has to end in a full stop. A trailing comma
		// becomes one; a zero-word paragraph -- which lipsum(1, true, 0,
		// 1) really asks for -- is just the stop.
		switch {
		case strings.HasSuffix(text, ","):
			text = text[:len(text)-1] + "."
		case !strings.HasSuffix(text, "."):
			text += "."
		}
		if html {
			text = "<p>" + text + "</p>"
		}
		paragraphs = append(paragraphs, text)
	}
	// The paragraphs are separated by one newline in the HTML form, where
	// the tags already mark them apart, and by a blank line in the plain
	// one. Using the blank line for both put a stray one between every
	// pair of <p> tags.
	sep := "\n\n"
	if html {
		sep = "\n"
	}
	joined := strings.Join(paragraphs, sep)
	if html {
		return value.Safe(joined), nil
	}
	return value.String(joined), nil
}
