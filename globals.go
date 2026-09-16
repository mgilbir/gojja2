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
type rangeObject struct {
	start, stop, step int64
}

// bigLen is the exact element count, computed the way CPython computes it:
// (stop - start + step -+ 1) // step, clamped at zero.
//
// It has to be arbitrary precision. `range(-2**63, 2**63-1)` holds 2**64-1
// elements, and the obvious int64 form of this expression overflows and wraps
// negative -- which read as a length of -1, made `|length` render -1 and made
// the loop run zero times.
func (r *rangeObject) bigLen() *big.Int {
	start := big.NewInt(r.start)
	stop := big.NewInt(r.stop)
	step := big.NewInt(r.step)

	span := new(big.Int).Sub(stop, start)
	adjust := big.NewInt(-1)
	if r.step < 0 {
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
func (r *rangeObject) BigLen() *big.Int { return r.bigLen() }

// Len is the length clamped into an int, which is what indexing and iteration
// need. A range longer than maxInt cannot be walked under any budget, so the
// clamp is unobservable except through len(), which uses BigLen instead.
func (r *rangeObject) Len() int {
	n := r.bigLen()
	if !n.IsInt64() || n.Int64() > int64(math.MaxInt) {
		return math.MaxInt
	}
	return int(n.Int64())
}

func (r *rangeObject) GetIndex(i int) (value.Value, bool) {
	if i < 0 || i >= r.Len() {
		return value.Undefined, false
	}
	return value.Int(r.start + int64(i)*r.step), true
}

func (r *rangeObject) GetAttr(name string) (value.Value, bool) {
	switch name {
	case "start":
		return value.Int(r.start), true
	case "stop":
		return value.Int(r.stop), true
	case "step":
		return value.Int(r.step), true
	}
	return value.Undefined, false
}

// Slice returns the sub-range a slice selects. Slicing a range in Python
// yields another range rather than a list, so `range(3)[1:]` renders
// "range(1, 3)" and not "[1, 2]".
func (r *rangeObject) Slice(start, stop, step *int) (value.Value, error) {
	begin, end, st, err := value.SliceBounds(r.Len(), start, stop, step)
	if err != nil {
		return value.Undefined, err
	}
	// The bounds are positions within this range, so they map back onto
	// the original start and step.
	return value.FromObject(&rangeObject{
		start: r.start + int64(begin)*r.step,
		stop:  r.start + int64(end)*r.step,
		step:  r.step * int64(st),
	}), nil
}

// Equals compares ranges the way Python does: by the sequence they stand for,
// so range(0, 3, 2) and range(0, 4, 2) are equal despite differing stops.
func (r *rangeObject) Equals(other value.Value) (bool, bool) {
	o, ok := other.Interface().(*rangeObject)
	if !ok {
		return false, other.Kind() == value.KindObject || other.Kind() == value.KindList
	}
	n := r.Len()
	if n != o.Len() {
		return false, true
	}
	if n == 0 {
		return true, true
	}
	if r.start != o.start {
		return false, true
	}
	return n == 1 || r.step == o.step, true
}

func (r *rangeObject) TypeName() string { return "range" }

func (r *rangeObject) Repr() string {
	var b strings.Builder
	b.WriteString("range(")
	b.WriteString(value.Repr(value.Int(r.start)))
	b.WriteString(", ")
	b.WriteString(value.Repr(value.Int(r.stop)))
	if r.step != 1 {
		b.WriteString(", ")
		b.WriteString(value.Repr(value.Int(r.step)))
	}
	b.WriteByte(')')
	return b.String()
}

func globalRange(s *State, args *value.CallArgs) (value.Value, error) {
	nums := make([]int64, 0, 3)
	for _, v := range args.Pos {
		n, ok := v.Int64()
		if !ok {
			return value.Undefined, errs.New(errs.TypeError,
				"'%s' object cannot be interpreted as an integer", v.TypeName())
		}
		nums = append(nums, n)
	}
	r := &rangeObject{step: 1}
	switch len(nums) {
	case 1:
		r.stop = nums[0]
	case 2:
		r.start, r.stop = nums[0], nums[1]
	case 3:
		r.start, r.stop, r.step = nums[0], nums[1], nums[2]
		if r.step == 0 {
			return value.Undefined, errs.New(errs.ValueError, "range() arg 3 must not be zero")
		}
	default:
		return value.Undefined, errs.New(errs.TypeError,
			"range expected 1 to 3 arguments, got %d", len(nums))
	}
	return value.FromObject(r), nil
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
		return Func("next", func(*State, *value.CallArgs) (value.Value, error) { return c.next() }), true
	case "reset":
		return Func("reset", func(*State, *value.CallArgs) (value.Value, error) {
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

func (c *cyclerObject) Call(*value.CallArgs) (value.Value, error) { return c.next() }
func (c *cyclerObject) TypeName() string                          { return "Cycler" }
func (c *cyclerObject) QualifiedName() string                     { return "jinja2.utils.Cycler" }
func (c *cyclerObject) Repr() string                              { return "<Cycler>" }

func globalCycler(s *State, args *value.CallArgs) (value.Value, error) {
	if len(args.Pos) == 0 {
		return value.Undefined, errs.New(errs.TypeError, "at least one item has to be provided")
	}
	return value.FromObject(&cyclerObject{items: args.Pos}), nil
}

// joinerObject returns nothing the first time it is called and its separator
// every time after, for joining a list whose items are emitted separately.
type joinerObject struct {
	sep  string
	used bool
}

func (j *joinerObject) GetAttr(string) (value.Value, bool) { return value.Undefined, false }
func (j *joinerObject) TypeName() string                   { return "Joiner" }
func (j *joinerObject) QualifiedName() string              { return "jinja2.utils.Joiner" }
func (j *joinerObject) Repr() string                       { return "<Joiner>" }

func (j *joinerObject) Call(*value.CallArgs) (value.Value, error) {
	if !j.used {
		j.used = true
		return value.String(""), nil
	}
	return value.String(j.sep), nil
}

func globalJoiner(s *State, args *value.CallArgs) (value.Value, error) {
	sep := ", "
	if v, ok := arg(args, 0, "sep"); ok && v.Kind() == value.KindString {
		sep = v.AsString()
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
	n, err := intArg(args, 0, "n", 5)
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
	lo, err := intArg(args, 2, "min", 20)
	if err != nil {
		return value.Undefined, err
	}
	hi, err := intArg(args, 3, "max", 100)
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
	// no times and yields an empty paragraph. The span is computed in int64
	// because hi-lo overflows for a wide enough range, and a wrapped negative
	// reaches rand.IntN, which panics on one.
	span := int64(hi) - int64(lo)
	if span > math.MaxInt32 {
		span = math.MaxInt32
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
		words := make([]string, 0, count)
		for range count {
			words = append(words, lipsumWords[rand.IntN(len(lipsumWords))])
		}
		text := strings.Join(words, " ")
		// A zero-word paragraph is a real outcome -- lipsum(1, true, 0, 1)
		// asks for it -- and jinja2 renders it as just the full stop.
		if text != "" {
			text = strings.ToUpper(text[:1]) + text[1:]
		}
		text += "."
		if html {
			text = "<p>" + text + "</p>"
		}
		paragraphs = append(paragraphs, text)
	}
	joined := strings.Join(paragraphs, "\n\n")
	if html {
		return value.Safe(joined), nil
	}
	return value.String(joined), nil
}
