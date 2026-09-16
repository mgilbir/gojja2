// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
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

func (r *rangeObject) Len() int {
	if r.step > 0 {
		if r.stop <= r.start {
			return 0
		}
		return int((r.stop - r.start + r.step - 1) / r.step)
	}
	if r.stop >= r.start {
		return 0
	}
	return int((r.start - r.stop - r.step - 1) / -r.step)
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

func globalRange(args *value.CallArgs) (value.Value, error) {
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
func globalDict(args *value.CallArgs) (value.Value, error) {
	out := value.NewDict()
	d, _ := out.Dict()
	if len(args.Pos) > 1 {
		return value.Undefined, errs.New(errs.TypeError,
			"dict expected at most 1 argument, got %d", len(args.Pos))
	}
	if len(args.Pos) == 1 {
		src := args.Pos[0]
		if sd, ok := src.Dict(); ok {
			for _, e := range sd.Entries() {
				if err := d.Set(e.Key, e.Value); err != nil {
					return value.Undefined, err
				}
			}
		} else if m, ok := src.Interface().(value.Mapping); ok {
			for _, k := range m.Keys() {
				v, _ := m.GetItem(k)
				if err := d.Set(k, v); err != nil {
					return value.Undefined, err
				}
			}
		} else {
			// dict() also accepts an iterable of key/value pairs.
			pairs, err := value.Iterate(src)
			if err != nil {
				return value.Undefined, errs.New(errs.TypeError,
					"'%s' object is not iterable", src.TypeName())
			}
			for pair := range pairs {
				seq, ok := pair.Seq()
				if !ok || seq.Len() != 2 {
					return value.Undefined, errs.New(errs.ValueError,
						"dictionary update sequence element has length %d; 2 is required",
						seqLen(pair))
				}
				if err := d.Set(seq.At(0), seq.At(1)); err != nil {
					return value.Undefined, err
				}
			}
		}
	}
	for _, kw := range args.Kwargs {
		d.SetString(kw.Name, kw.Value)
	}
	return out, nil
}

func seqLen(v value.Value) int {
	if s, ok := v.Seq(); ok {
		return s.Len()
	}
	n, _ := value.Len(v)
	return n
}

func globalNamespace(args *value.CallArgs) (value.Value, error) {
	// Namespace(*args, **kwargs) is dict(*args, **kwargs) underneath, so a
	// non-mapping argument fails as an iterable rather than as a mapping.
	built, err := globalDict(args)
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
	case "current":
		if len(c.items) == 0 {
			return value.Undefined, true
		}
		return c.items[c.pos], true
	case "next":
		return Func("next", func(*value.CallArgs) (value.Value, error) { return c.next() }), true
	case "reset":
		return Func("reset", func(*value.CallArgs) (value.Value, error) {
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
func (c *cyclerObject) Repr() string                              { return "<Cycler>" }

func globalCycler(args *value.CallArgs) (value.Value, error) {
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
func (j *joinerObject) Repr() string                       { return "<Joiner>" }

func (j *joinerObject) Call(*value.CallArgs) (value.Value, error) {
	if !j.used {
		j.used = true
		return value.String(""), nil
	}
	return value.String(j.sep), nil
}

func globalJoiner(args *value.CallArgs) (value.Value, error) {
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
func globalLipsum(args *value.CallArgs) (value.Value, error) {
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
	if hi <= lo {
		hi = lo + 1
	}

	paragraphs := make([]string, 0, n)
	for range n {
		count := lo + rand.IntN(hi-lo)
		words := make([]string, 0, count)
		for range count {
			words = append(words, lipsumWords[rand.IntN(len(lipsumWords))])
		}
		text := strings.Join(words, " ")
		text = strings.ToUpper(text[:1]) + text[1:] + "."
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
