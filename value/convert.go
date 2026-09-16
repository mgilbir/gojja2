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
)

// FromGo converts a Go value into a template value.
//
// Scalars, slices and maps are converted eagerly; structs are wrapped lazily,
// so a large struct costs nothing until a template touches a field.
//
// Go maps have no iteration order, so their keys are sorted. This is a
// deliberate divergence from a Python dict, which preserves insertion order:
// there is no insertion order to preserve, and an arbitrary one would make
// `{% for k, v in m|items %}` render differently on each run.
func FromGo(v any) Value {
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
		// The common case, worth avoiding reflection for.
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		d := NewDict()
		dict, _ := d.Dict()
		for _, k := range keys {
			dict.SetString(k, FromGo(v[k]))
		}
		return d
	case []any:
		items := make([]Value, len(v))
		for i, item := range v {
			items[i] = FromGo(item)
		}
		return NewList(items...)
	}
	return fromReflect(reflect.ValueOf(v))
}

func fromReflect(rv reflect.Value) Value {
	if !rv.IsValid() {
		return None
	}
	switch rv.Kind() {
	case reflect.Pointer, reflect.Interface:
		if rv.IsNil() {
			return None
		}
		return fromReflect(rv.Elem())

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
		items := make([]Value, rv.Len())
		for i := range items {
			items[i] = FromGo(rv.Index(i).Interface())
		}
		return NewList(items...)

	case reflect.Map:
		keys := rv.MapKeys()
		// Sorting by rendered key gives a stable order for any key type.
		slices.SortFunc(keys, func(a, b reflect.Value) int {
			return compareReflectKeys(a, b)
		})
		d := NewDict()
		dict, _ := d.Dict()
		for _, k := range keys {
			if err := dict.Set(FromGo(k.Interface()), FromGo(rv.MapIndex(k).Interface())); err != nil {
				// An unhashable key cannot occur: Go map keys are
				// always comparable, so this is unreachable.
				continue
			}
		}
		return d

	case reflect.Struct:
		return FromObject(&structObject{rv: rv})

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
	rv reflect.Value
}

func (o *structObject) GetAttr(name string) (Value, bool) {
	rt := o.rv.Type()
	for i := range rt.NumField() {
		f := rt.Field(i)
		if !f.IsExported() {
			continue
		}
		if f.Name == name || tagName(f) == name {
			return FromGo(o.rv.Field(i).Interface()), true
		}
	}
	// Methods with no arguments and one result read as attributes.
	if m := o.rv.MethodByName(name); m.IsValid() {
		return FromObject(&methodObject{fn: m, name: name}), true
	}
	return Undefined, false
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
	rt := o.rv.Type()
	var keys []Value
	for i := range rt.NumField() {
		f := rt.Field(i)
		if !f.IsExported() {
			continue
		}
		if name := tagName(f); name != "" {
			keys = append(keys, String(name))
			continue
		}
		keys = append(keys, String(f.Name))
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

func (o *structObject) TypeName() string { return o.rv.Type().Name() }

func (o *structObject) Repr() string {
	return fmt.Sprintf("<%s object>", o.rv.Type())
}

// methodObject is a zero-argument Go method reached as an attribute.
type methodObject struct {
	fn   reflect.Value
	name string
}

func (m *methodObject) GetAttr(string) (Value, bool) { return Undefined, false }

func (m *methodObject) Call(args *CallArgs) (Value, error) {
	t := m.fn.Type()
	if t.NumIn() != len(args.Pos) || len(args.Kwargs) > 0 {
		return Undefined, fmt.Errorf("%s() takes %d arguments, got %d",
			m.name, t.NumIn(), len(args.Pos))
	}
	in := make([]reflect.Value, len(args.Pos))
	for i, a := range args.Pos {
		want := t.In(i)
		got := reflect.ValueOf(ToGo(a))
		if !got.IsValid() || !got.Type().AssignableTo(want) {
			return Undefined, fmt.Errorf("%s(): argument %d is not a %s", m.name, i+1, want)
		}
		in[i] = got
	}
	out := m.fn.Call(in)
	switch len(out) {
	case 0:
		return None, nil
	case 1:
		return FromGo(out[0].Interface()), nil
	}
	// A (value, error) pair is the idiomatic Go shape; surface the error.
	if err, ok := out[len(out)-1].Interface().(error); ok && err != nil {
		return Undefined, err
	}
	return FromGo(out[0].Interface()), nil
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
func ToGo(v Value) any {
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
			out[i] = ToGo(item)
		}
		return out
	case KindDict:
		d, _ := v.Dict()
		out := make(map[string]any, d.Len())
		for _, e := range d.Entries() {
			out[Str(e.Key)] = ToGo(e.Value)
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
