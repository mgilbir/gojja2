// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package value

import (
	"fmt"
	"iter"
	"math/big"
	"reflect"
	"slices"
	"time"

	"github.com/mgilbir/gojja2/errs"
)

// MethodInfo describes a Go method a template is trying to reach.
type MethodInfo struct {
	// Owner is the type the method was found on.
	Owner reflect.Type
	// Name is the method name as Go spells it.
	Name string
	// NumIn is the parameter count, excluding the receiver.
	NumIn int
	// NumOut is the result count.
	NumOut int
}

// MethodPolicy decides whether a Go method may be reached from a template.
//
// The default is [NullaryMethods]. Widening it means templates choose the
// arguments a host method is called with, so it is a decision the host makes
// explicitly rather than one reflection makes on its behalf.
type MethodPolicy func(MethodInfo) bool

// NullaryMethods exposes only methods that take no arguments, which is what a
// template can reach as a plain attribute and what this package documents.
func NullaryMethods(m MethodInfo) bool { return m.NumIn == 0 }

// AllMethods exposes every exported method, arguments included. Use it only
// where template authors are as trusted as the Go code they are calling into.
func AllMethods(MethodInfo) bool { return true }

// containerID identifies a Go container by its runtime identity, so a
// structure that refers to itself can be recognised on the way down.
//
// Length is part of the key because two slices over one backing array share a
// data pointer; without it, a[:1] and a[:2] would be taken for the same value.
type containerID struct {
	typ reflect.Type
	ptr uintptr
	n   int
}

// converter carries the state a single FromGo walk needs.
type converter struct {
	// seen maps a container already being converted to the Value standing
	// for it. A cyclic structure therefore converts into a cyclic Value
	// rather than expanding until memory runs out -- which it did, before
	// any render budget existed to stop it.
	seen   map[containerID]Value
	expose MethodPolicy
	// b bounds the walk. A slice or map is converted in full, so a render
	// argument of the caller's choosing decides how long that takes; with
	// nothing charged and nothing polled, the walk was a region no deadline
	// could reach. `{{ big|length }}` over a million-element argument ran
	// for a second and *returned success* with an already-cancelled
	// context, because length never iterates and so nothing downstream ever
	// looked at the clock.
	b Budget
	// err is the first refusal, after which the walk unwinds. The partial
	// Value it returns is not usable, which is why every entry point that
	// takes a Budget reports the error rather than the value alone.
	err error
}

// charge reserves n elements and reports whether the walk may continue.
func (c *converter) charge(n int) bool {
	if c.err != nil {
		return false
	}
	if err := chargeItems(c.b, int64(n)); err != nil {
		c.err = err
		return false
	}
	return true
}

// step is charge for a single element that has already been counted.
//
// The container's length is charged before the slice holding it is allocated,
// so charging again per element would count the same memory twice. What the
// walk still needs is a yield point: the elements are converted one at a time,
// and a million of them is a million recursive calls between one charge and the
// next.
func (c *converter) step() bool {
	if c.err != nil {
		return false
	}
	if err := poll(c.b); err != nil {
		c.err = err
		return false
	}
	return true
}

func (c *converter) memo(id containerID, v Value) {
	if c.seen == nil {
		c.seen = make(map[containerID]Value, 4)
	}
	c.seen[id] = v
}

// identify returns the memo key for a container, and false for one that cannot
// take part in a cycle. An empty container is excluded deliberately: Go gives
// every zero-length allocation the same address, so keying on it would alias
// unrelated empty values onto one another.
func identify(rv reflect.Value) (containerID, bool) {
	if rv.Len() == 0 {
		return containerID{}, false
	}
	switch rv.Kind() {
	case reflect.Map:
		return containerID{typ: rv.Type(), ptr: rv.Pointer()}, true
	case reflect.Slice:
		return containerID{typ: rv.Type(), ptr: rv.Pointer(), n: rv.Len()}, true
	}
	return containerID{}, false
}

// FromGo converts a Go value into a template value.
//
// Scalars, slices and maps are converted eagerly; structs are wrapped lazily,
// so a large struct costs nothing until a template touches a field. A
// container that refers to itself, directly or through other containers, is
// converted once and then shared, so the result is a cyclic value rather than
// an endless expansion of one.
//
// Go maps have no iteration order, so their keys are sorted. This is a
// deliberate divergence from a Python dict, which preserves insertion order:
// there is no insertion order to preserve, and an arbitrary one would make
// `{% for k, v in m|items %}` render differently on each run.
func FromGo(v any) Value { return (&converter{}).fromAny(v) }

// FromGoWith is [FromGo] under an explicit method policy.
func FromGoWith(v any, expose MethodPolicy) Value {
	return (&converter{expose: expose}).fromAny(v)
}

// FromGoBudget is [FromGoWith] with the walk charged to b and stopped when b
// says to stop.
//
// A slice or map converts in full, so how long a conversion takes is decided by
// the value handed in rather than by the template that mentions it. Without a
// budget that is work no deadline can interrupt and no bound can count, and it
// happens before the template does anything a bound would notice.
//
// The returned Value is only meaningful when the error is nil; a refused walk
// hands back what it had built so far.
func FromGoBudget(v any, expose MethodPolicy, b Budget) (Value, error) {
	c := &converter{expose: expose, b: b}
	out := c.fromAny(v)
	if c.err != nil {
		return Undefined, c.err
	}
	return out, nil
}

func (c *converter) fromAny(v any) Value {
	switch v := v.(type) {
	case nil:
		return None
	case Value:
		return v
	case bool:
		return Bool(v)
	case string:
		return String(v)
	case []byte:
		return Bytes(v)
	case int:
		return Int(int64(v))
	case int8:
		return Int(int64(v))
	case int16:
		return Int(int64(v))
	case int32:
		return Int(int64(v))
	case int64:
		return Int(v)
	case uint:
		return Uint(uint64(v))
	case uint8:
		return Uint(uint64(v))
	case uint16:
		return Uint(uint64(v))
	case uint32:
		return Uint(uint64(v))
	case uint64:
		return Uint(v)
	case uintptr:
		return Uint(uint64(v))
	case float32:
		return Float(float64(v))
	case float64:
		return Float(v)
	case *big.Int:
		if v == nil {
			return None
		}
		return BigInt(v)
	case time.Time:
		return FromObject(timeObject{v})
	case Object:
		return FromObject(v)
	case map[string]any:
		// The common case, worth avoiding reflection for -- but still
		// keyed by identity, because a map holding itself is exactly the
		// shape that used to expand until the process died.
		rv := reflect.ValueOf(v)
		if id, ok := identify(rv); ok {
			if seen, hit := c.seen[id]; hit {
				return seen
			}
			d := NewDict()
			c.memo(id, d)
			c.fillStringMap(d, v)
			return d
		}
		d := NewDict()
		c.fillStringMap(d, v)
		return d
	case []any:
		rv := reflect.ValueOf(v)
		if id, ok := identify(rv); ok {
			if seen, hit := c.seen[id]; hit {
				return seen
			}
			if !c.charge(len(v)) {
				return NewList()
			}
			list := NewList(make([]Value, 0, len(v))...)
			c.memo(id, list)
			seq, _ := list.Seq()
			for _, item := range v {
				if !c.step() {
					return list
				}
				seq.Append(c.fromAny(item))
			}
			return list
		}
		if !c.charge(len(v)) {
			return NewList()
		}
		items := make([]Value, len(v))
		for i, item := range v {
			if !c.step() {
				return NewList(items[:i]...)
			}
			items[i] = c.fromAny(item)
		}
		return NewList(items...)
	}
	return c.fromReflect(reflect.ValueOf(v))
}

func (c *converter) fillStringMap(d Value, m map[string]any) {
	// Charged before the key slice and the dict behind it are sized.
	if !c.charge(len(m)) {
		return
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	dict, _ := d.Dict()
	dict.Reserve(len(keys))
	for _, k := range keys {
		if !c.step() {
			return
		}
		dict.SetString(k, c.fromAny(m[k]))
	}
}

func (c *converter) fromReflect(rv reflect.Value) Value {
	if !rv.IsValid() {
		return None
	}
	switch rv.Kind() {
	case reflect.Pointer, reflect.Interface:
		if rv.IsNil() {
			return None
		}
		if rv.Kind() == reflect.Pointer && rv.Elem().Kind() == reflect.Struct {
			// Wrap the pointer rather than the struct it points at:
			// dereferencing here would discard the pointer's method
			// set, which in Go is where methods usually live. That
			// made a documented feature absent for exactly the
			// receiver style most code uses.
			return FromObject(&structObject{rv: rv, expose: c.expose, b: c.b})
		}
		return c.fromReflect(rv.Elem())

	case reflect.Bool:
		return Bool(rv.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return Int(rv.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return Uint(rv.Uint())
	case reflect.Float32, reflect.Float64:
		return Float(rv.Float())
	case reflect.String:
		return String(rv.String())

	case reflect.Slice, reflect.Array:
		if rv.Kind() == reflect.Slice && rv.Type().Elem().Kind() == reflect.Uint8 {
			return Bytes(rv.Bytes())
		}
		id, cyclable := identify(rv)
		if cyclable {
			if seen, hit := c.seen[id]; hit {
				return seen
			}
		}
		if !c.charge(rv.Len()) {
			return NewList()
		}
		list := NewList(make([]Value, 0, rv.Len())...)
		if cyclable {
			c.memo(id, list)
		}
		seq, _ := list.Seq()
		for i := range rv.Len() {
			if !c.step() {
				return list
			}
			seq.Append(c.fromAny(rv.Index(i).Interface()))
		}
		return list

	case reflect.Map:
		id, cyclable := identify(rv)
		if cyclable {
			if seen, hit := c.seen[id]; hit {
				return seen
			}
		}
		d := NewDict()
		if cyclable {
			c.memo(id, d)
		}
		if !c.charge(rv.Len()) {
			return d
		}
		keys := rv.MapKeys()
		// Sorting by rendered key gives a stable order for any key type.
		// Each comparison converts both keys, so the sort is n log n
		// conversions on top of the walk itself and needs a yield point
		// of its own.
		slices.SortFunc(keys, func(a, b reflect.Value) int {
			c.step()
			return compareReflectKeys(a, b)
		})
		dict, _ := d.Dict()
		dict.Reserve(len(keys))
		for _, k := range keys {
			if !c.step() {
				return d
			}
			if err := dict.Set(c.fromAny(k.Interface()), c.fromAny(rv.MapIndex(k).Interface())); err != nil {
				// An unhashable key cannot occur: Go map keys are
				// always comparable, so this is unreachable.
				continue
			}
		}
		return d

	case reflect.Struct:
		return FromObject(&structObject{rv: rv, expose: c.expose, b: c.b})

	case reflect.Func:
		if rv.IsNil() {
			return None
		}
	}
	return FromObject(&opaqueObject{rv: rv})
}

func compareReflectKeys(a, b reflect.Value) int {
	ka, kb := FromGo(a.Interface()), FromGo(b.Interface())
	if ord, ok := compareNumbers(ka, kb); ok && ka.IsNumber() && kb.IsNumber() {
		return ord
	}
	sa, sb := Str(ka), Str(kb)
	switch {
	case sa < sb:
		return -1
	case sa > sb:
		return 1
	}
	return 0
}

// structObject exposes a Go struct's exported fields as attributes.
//
// Field names are matched as written and, for convenience in templates that
// were written against JSON, by their `gojja2` or `json` tag.
type structObject struct {
	// rv is the struct, or a pointer to it. A pointer is kept as one so
	// that the pointer's method set stays reachable.
	rv     reflect.Value
	expose MethodPolicy
	// b is the budget of the conversion this wrapper came out of.
	//
	// Wrapping a struct lazily defers part of one conversion rather than
	// finishing it, so reading a field later is the same walk resumed, and
	// it is charged where the rest of the walk was. Without this a field
	// holding a large slice was a way to reach an unbounded conversion from
	// inside a render that had a budget: `{{ h.Items|length }}` behaved
	// exactly like the render argument it was reached through.
	//
	// It is the budget of the render that built the wrapper. That is the
	// render the wrapper belongs to: it is memoised into that render's
	// scope and does not outlive it. A wrapper built outside a render --
	// from FromGo, or a global registered before any render -- has none,
	// and is bounded by the hard ceiling alone, as it was before.
	b Budget
}

// fields returns the struct value whose fields are being read, following one
// level of pointer when rv is one.
func (o *structObject) fields() reflect.Value {
	if o.rv.Kind() == reflect.Pointer {
		return o.rv.Elem()
	}
	return o.rv
}

func (o *structObject) GetAttr(name string) (Value, bool) {
	fields := o.fields()
	for _, f := range visibleFields(fields.Type()) {
		if f.name != name && f.goName != name {
			continue
		}
		fv, err := fields.FieldByIndexErr(f.index)
		if err != nil {
			// The path runs through a nil embedded pointer, which
			// in Go is a panic on access. A template gets the
			// undefined it would get for any missing name.
			return Undefined, false
		}
		v, err := FromGoBudget(fv.Interface(), o.expose, o.b)
		if err != nil {
			// GetAttr has nowhere to put an error. The refusal
			// came from the budget, which remembers it, so the
			// render fails on its next charge rather than on this
			// undefined; see the Budget contract.
			return Undefined, false
		}
		return v, true
	}
	if m, ok := o.method(name); ok {
		return m, true
	}
	return Undefined, false
}

// fieldRef is one field a template can reach, after promotion.
type fieldRef struct {
	// name is what the field is listed as: its tag if it has one, else its
	// Go name.
	name string
	// goName is the Go spelling, which stays reachable even when a tag has
	// renamed it -- guide.md promises "by name or by json tag".
	goName string
	index  []int
}

// visibleFields is the fields of a struct as Go sees them, embedding included.
//
// Go promotes an embedded struct's fields to the outer type: `d.ID` reaches
// Base.ID when Derived embeds Base, and that is idiomatic enough that leaving
// it out made `{{ user.ID }}` render *nothing* for a host whose type embeds a
// common base. Methods were promoted all along -- reflect's method set does it
// -- so the same embedding exposed d.Describe() and hid d.ID.
//
// The rules are Go's: shallower wins, and two fields of one name at the same
// depth promote neither. An embedded struct is descended into even when its
// own type is unexported, because the fields inside it may not be; that is why
// `d.Hidden` reaches lowerBase.Hidden.
//
// The embedded field itself is not listed, which is encoding/json's rule rather
// than reflect.VisibleFields': a struct that embeds another serialises flat,
// which is what `{{ user|tojson }}` has to mean for the json tags this already
// honours to be worth anything. It stays *reachable* by its own name, as Go
// allows, so nothing that worked before stops working.
func visibleFields(rt reflect.Type) []fieldRef {
	type queued struct {
		typ   reflect.Type
		index []int
	}
	var out []fieldRef
	seen := map[string]int{} // name -> depth it was claimed at
	ambiguous := map[string]bool{}

	level := []queued{{typ: rt}}
	for depth := 0; len(level) > 0; depth++ {
		var next []queued
		var found []fieldRef
		claimed := map[string]int{}
		for _, q := range level {
			for i := range q.typ.NumField() {
				f := q.typ.Field(i)
				index := append(append([]int{}, q.index...), i)

				ft := f.Type
				if ft.Kind() == reflect.Pointer {
					ft = ft.Elem()
				}
				if f.Anonymous && ft.Kind() == reflect.Struct {
					// The tag is honoured only when the
					// embedded type is exported: naming the
					// field means reading its value, and
					// reflect will not hand over an
					// unexported field's. encoding/json
					// reaches it by walking the reflect
					// value; this converts through
					// interfaces, so an unexported embedded
					// type promotes as if untagged.
					if tag := tagName(f); tag != "" && f.IsExported() {
						// A tag on an embedded field
						// names it, and a named field
						// is not promoted through --
						// encoding/json's rule, and the
						// only way to ask for the
						// nested shape.
						found = append(found, fieldRef{
							name: tag, goName: f.Name, index: index})
						claimed[tag]++
						claimed[f.Name]++
						continue
					}
					// Descend whether or not the embedded
					// type is exported: the fields inside
					// it may be.
					next = append(next, queued{typ: ft, index: index})
					if !f.IsExported() {
						continue
					}
					// An untagged embedded field is
					// reachable by its own name but not
					// listed, so the struct serialises flat.
					found = append(found, fieldRef{name: "", goName: f.Name, index: index})
					claimed[f.Name]++
					continue
				}
				if !f.IsExported() {
					continue
				}
				ref := fieldRef{name: tagName(f), goName: f.Name, index: index}
				if ref.name == "" {
					ref.name = f.Name
				}
				found = append(found, ref)
				claimed[ref.name]++
				if ref.goName != ref.name {
					claimed[ref.goName]++
				}
			}
		}
		for name, n := range claimed {
			if n > 1 {
				// Two fields of one name at one depth promote
				// neither, which is Go's rule.
				ambiguous[name] = true
			}
		}
		for _, ref := range found {
			key := ref.name
			if key == "" {
				key = ref.goName
			}
			if ambiguous[key] {
				continue
			}
			if d, taken := seen[key]; taken && d < depth {
				continue // a shallower field of this name won
			}
			seen[key] = depth
			out = append(out, ref)
		}
		level = next
	}
	return out
}

// method resolves an exported method the policy allows.
//
// The default policy is NullaryMethods, which is what this package documents
// and what a template can reach as a plain attribute. Anything wider means the
// template chooses the arguments a host method is called with, so it has to be
// asked for.
func (o *structObject) method(name string) (Value, bool) {
	m := o.rv.MethodByName(name)
	if !m.IsValid() {
		return Undefined, false
	}
	t := m.Type()
	expose := o.expose
	if expose == nil {
		expose = NullaryMethods
	}
	if !expose(MethodInfo{
		Owner:  o.rv.Type(),
		Name:   name,
		NumIn:  t.NumIn(),
		NumOut: t.NumOut(),
	}) {
		return Undefined, false
	}
	return FromObject(&methodObject{fn: m, name: name, expose: o.expose, b: o.b}), true
}

func tagName(f reflect.StructField) string {
	for _, key := range []string{"gojja2", "json"} {
		if tag, ok := f.Tag.Lookup(key); ok {
			if name, _, _ := cutComma(tag); name != "" && name != "-" {
				return name
			}
		}
	}
	return ""
}

func cutComma(s string) (string, string, bool) {
	for i := range len(s) {
		if s[i] == ',' {
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}

// Keys lets a struct participate in `|items` and `{% for %}` over a mapping.
func (o *structObject) Keys() []Value {
	var keys []Value
	for _, f := range visibleFields(o.fields().Type()) {
		// An embedded field with no tag has an empty name: it is
		// reachable but not listed, so that a struct which embeds
		// another serialises flat.
		if f.name == "" {
			continue
		}
		keys = append(keys, String(f.name))
	}
	return keys
}

func (o *structObject) GetItem(key Value) (Value, bool) {
	if key.Kind() != KindString {
		return Undefined, false
	}
	return o.GetAttr(key.AsString())
}

func (o *structObject) Len() int { return len(o.Keys()) }

func (o *structObject) TypeName() string { return o.fields().Type().Name() }

func (o *structObject) Repr() string {
	return fmt.Sprintf("<%s object>", o.fields().Type())
}

// methodObject is a Go method reached as an attribute.
type methodObject struct {
	fn     reflect.Value
	name   string
	expose MethodPolicy
	// b is the budget of the conversion this wrapper came out of; see
	// structObject.b. A method's result is converted when it is called,
	// which is as unbounded as any other conversion and happens mid-render.
	b Budget
}

func (m *methodObject) GetAttr(string) (Value, bool) { return Undefined, false }

func (m *methodObject) Call(args *CallArgs) (Value, error) {
	t := m.fn.Type()
	if t.NumIn() != len(args.Pos) || len(args.Kwargs) > 0 {
		return Undefined, errs.New(errs.TypeError, "%s() takes %d arguments, got %d",
			m.name, t.NumIn(), len(args.Pos))
	}
	in := make([]reflect.Value, len(args.Pos))
	for i, a := range args.Pos {
		want := t.In(i)
		got := reflect.ValueOf(ToGo(a))
		if !got.IsValid() || !got.Type().AssignableTo(want) {
			return Undefined, errs.New(errs.TypeError,
				"%s(): argument %d is not a %s", m.name, i+1, want)
		}
		in[i] = got
	}
	out, err := m.call(in)
	if err != nil {
		return Undefined, err
	}
	if len(out) == 0 {
		return None, nil
	}
	// A trailing error is the idiomatic Go shape and is surfaced whatever
	// the arity. Checking it only for two results or more meant a method
	// whose *only* result is an error -- which is how a validating
	// accessor is written -- had its failure reflected into an object and
	// rendered as "<errors.errorString object>", with the render reporting
	// success.
	if last := out[len(out)-1]; last.Type() == errorType {
		if e, _ := last.Interface().(error); e != nil {
			return Undefined, e
		}
		if len(out) == 1 {
			// The method returned only an error, and it was nil.
			return None, nil
		}
	}
	return FromGoBudget(out[0].Interface(), m.expose, m.b)
}

// errorType is the error interface, for recognising a method's trailing
// result. Comparing the static type rather than type-asserting the value is
// what tells a nil error apart from a result that merely happens to be nil.
var errorType = reflect.TypeOf((*error)(nil)).Elem()

// call invokes the method, turning a panic into an error.
//
// Host code reached from a template is called with arguments a template
// chose, so it can be driven into states its author never tested. A panic
// crossing Render unwinds the caller's goroutine, which for a template engine
// is never the right answer: the template failed, so the render should fail.
func (m *methodObject) call(in []reflect.Value) (out []reflect.Value, err error) {
	defer func() {
		if r := recover(); r != nil {
			out = nil
			err = errs.New(errs.TemplateRuntimeError,
				"%s() panicked: %v", m.name, r)
		}
	}()
	return m.fn.Call(in), nil
}

func (m *methodObject) Repr() string { return "<bound method " + m.name + ">" }

// opaqueObject wraps a Go value gojja2 has no structure for, so that passing
// it into a template is not silently lossy.
type opaqueObject struct {
	rv reflect.Value
}

func (o *opaqueObject) GetAttr(string) (Value, bool) { return Undefined, false }
func (o *opaqueObject) TypeName() string             { return o.rv.Type().String() }
func (o *opaqueObject) Repr() string                 { return fmt.Sprintf("<%s>", o.rv.Type()) }

// timeObject renders a time.Time as RFC 3339 and exposes its components.
type timeObject struct{ t time.Time }

func (o timeObject) GetAttr(name string) (Value, bool) {
	switch name {
	case "year":
		return Int(int64(o.t.Year())), true
	case "month":
		return Int(int64(o.t.Month())), true
	case "day":
		return Int(int64(o.t.Day())), true
	case "hour":
		return Int(int64(o.t.Hour())), true
	case "minute":
		return Int(int64(o.t.Minute())), true
	case "second":
		return Int(int64(o.t.Second())), true
	}
	return Undefined, false
}

func (o timeObject) Str() string      { return o.t.Format(time.RFC3339) }
func (o timeObject) Repr() string     { return o.t.Format(time.RFC3339) }
func (o timeObject) TypeName() string { return "datetime" }

// ToGo converts a template value back into an ordinary Go value, for handing
// to Go code such as a struct method.
//
// A value graph can contain a cycle, so the walk is depth-bounded: an
// unbounded one would overflow the stack, which Go cannot recover from. The
// bound is far past any structure a caller would hand to a method.
func ToGo(v Value) any { return toGoDepth(v, 0) }

// maxToGoDepth bounds the conversion back to Go.
const maxToGoDepth = 100

func toGoDepth(v Value, depth int) any {
	if depth > maxToGoDepth {
		return nil
	}
	switch v.Kind() {
	case KindUndefined, KindNone:
		return nil
	case KindBool:
		return v.AsBool()
	case KindInt:
		if i, ok := v.Int64(); ok {
			return i
		}
		b, _ := v.BigInt()
		return b
	case KindFloat:
		return v.AsFloat()
	case KindString:
		return v.AsString()
	case KindBytes:
		return []byte(v.AsString())
	case KindList, KindTuple:
		s, _ := v.Seq()
		out := make([]any, s.Len())
		for i, item := range s.Items() {
			out[i] = toGoDepth(item, depth+1)
		}
		return out
	case KindDict:
		d, _ := v.Dict()
		out := make(map[string]any, d.Len())
		for _, e := range d.Entries() {
			out[Str(e.Key)] = toGoDepth(e.Value, depth+1)
		}
		return out
	}
	return v.Interface()
}

// Iterate walks any iterable value, yielding its elements.
//
// It reports an error only for values that are not iterable at all; an empty
// result is not an error, which is what lets `{% for x in undefined %}` render
// nothing rather than fail.
func Iterate(v Value) (iter.Seq[Value], error) {
	switch v.Kind() {
	case KindUndefined:
		if v.UndefinedBehavior() == UndefinedStrict {
			return nil, v.UndefinedError()
		}
		return func(func(Value) bool) {}, nil
	case KindList, KindTuple:
		s, _ := v.Seq()
		return slices.Values(s.Items()), nil
	case KindDict:
		// Iterating a dict yields its keys, as in Python.
		d, _ := v.Dict()
		return slices.Values(d.Keys()), nil
	case KindString:
		return func(yield func(Value) bool) {
			for _, r := range v.AsString() {
				if !yield(String(string(r))) {
					return
				}
			}
		}, nil
	case KindBytes:
		return func(yield func(Value) bool) {
			for i := range len(v.AsString()) {
				if !yield(Int(int64(v.AsString()[i]))) {
					return
				}
			}
		}, nil
	case KindObject:
		switch o := v.Interface().(type) {
		case Iterable:
			return o.Iterate(), nil
		case Mapping:
			return slices.Values(o.Keys()), nil
		case Sequence:
			return func(yield func(Value) bool) {
				for i := range o.Len() {
					item, ok := o.GetIndex(i)
					if !ok || !yield(item) {
						return
					}
				}
			}, nil
		}
	}
	return nil, errTypeNotIterable(v)
}

// Copy returns a deep copy of a container value, and the value itself for
// anything immutable.
//
// It exists for constant folding: an expression folded to a literal container
// would otherwise hand every render the same list, and a template that
// appended to it would see its own history. jinja2 has that bug; copying is
// the same behaviour without it.
func Copy(v Value) Value {
	switch v.Kind() {
	case KindList, KindTuple:
		s, _ := v.Seq()
		items := make([]Value, s.Len())
		for i, item := range s.Items() {
			items[i] = Copy(item)
		}
		if v.Kind() == KindTuple {
			return NewTuple(items...)
		}
		return NewList(items...)
	case KindDict:
		d, _ := v.Dict()
		out := NewDict()
		target, _ := out.Dict()
		for _, e := range d.Entries() {
			if err := target.Set(e.Key, Copy(e.Value)); err != nil {
				return v
			}
		}
		return out
	}
	return v
}
