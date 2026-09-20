// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/mgilbir/gojja2"
	"github.com/mgilbir/gojja2/value"
)

// The reflection bridge, against what guide.md promises of it:
//
//	a struct exposes its exported fields, by name or by json tag, and its
//	methods that take no arguments -- value or pointer receiver
//
// There is no oracle for any of this: CPython jinja2 has no Go structs to
// reflect over, so the differential harness cannot grade it and these are the
// only checks it gets.

type bridgeInner struct {
	Deep   string
	hidden string
}

type bridgeEmbedded struct{ Promoted string }

type bridgeHost struct {
	Name    string
	Tagged  string `json:"tag_name"`
	Renamed string `gojja2:"gj" json:"js"`
	Omitted string `json:"omit,omitempty"`
	Dashed  string `json:"-"`
	Inner   bridgeInner
	bridgeEmbedded
	secret string
	Count  uint64
	Ptr    *bridgeInner
	NilPtr *bridgeInner
	Items  []int
	Dict   map[string]int
	Fn     func() string
}

func (bridgeHost) ValueMethod() string     { return "value" }
func (*bridgeHost) PointerMethod() string  { return "pointer" }
func (bridgeHost) WithArg(s string) string { return "arg:" + s }
func (bridgeHost) hiddenMethod() string    { return "no" }

func bridgeFixture() *bridgeHost {
	return &bridgeHost{
		Name: "n", Tagged: "t", Renamed: "r", Omitted: "o", Dashed: "d",
		Inner:  bridgeInner{Deep: "deep", hidden: "shh"},
		secret: "shh",
		Count:  math.MaxUint64,
		Ptr:    &bridgeInner{Deep: "viaptr"},
		Items:  []int{1, 2},
		Dict:   map[string]int{"k": 1},
	}
}

func renderBridge(t *testing.T, src string, vars map[string]any, opts ...gojja2.Option) (string, error) {
	t.Helper()
	tmpl, err := mustEnv(opts...).FromString(src)
	if err != nil {
		t.Fatalf("compile %q: %v", src, err)
	}
	return tmpl.RenderString(context.Background(), vars)
}

// An unexported field is not reachable by any route a template has: as an
// attribute, as a subscript, by iterating the struct, or through |items.
func TestBridgeHidesUnexportedFields(t *testing.T) {
	h := bridgeFixture()
	// The unexported method exists and works in Go. That is the point: it
	// is reachable here and must not be reachable from a template, so the
	// test would prove nothing if it did not exist at all.
	if got := h.hiddenMethod(); got != "no" {
		t.Fatalf("hiddenMethod() = %q in Go; the fixture is wrong", got)
	}
	vars := map[string]any{"h": h}
	for _, src := range []string{
		`{{ h.secret }}`,
		`{{ h["secret"] }}`,
		`{{ h.hidden }}`,
		`{{ h.Inner.hidden }}`,
		`{{ h.hiddenMethod }}`,
		`{{ h.hiddenMethod() }}`,
	} {
		got, err := renderBridge(t, src, vars)
		if err == nil && strings.Contains(got, "shh") {
			t.Errorf("%s leaked an unexported field: %q", src, got)
		}
	}
	// And they are absent from every listing.
	for _, src := range []string{`{{ h|list|sort }}`, `{{ h|items|list }}`, `{{ h|length }}`} {
		got, err := renderBridge(t, src, vars)
		if err != nil {
			t.Errorf("%s: %v", src, err)
			continue
		}
		for _, bad := range []string{"secret", "hidden", "shh"} {
			if strings.Contains(got, bad) {
				t.Errorf("%s listed %q: %q", src, bad, got)
			}
		}
	}
}

// Exported fields are reachable by Go name and by tag, gojja2's winning over
// json's. A `json:"-"` field keeps its Go name rather than vanishing, which is
// worth pinning because the other reading is just as plausible.
func TestBridgeFieldNaming(t *testing.T) {
	vars := map[string]any{"h": bridgeFixture()}
	for _, tc := range []struct{ src, want string }{
		{`{{ h.Name }}`, "n"},
		{`{{ h.tag_name }}`, "t"},
		{`{{ h.gj }}`, "r"},
		{`{{ h.omit }}`, "o"},
		{`{{ h.Dashed }}`, "d"},
		{`{{ h.Inner.Deep }}`, "deep"},
		{`{{ h.Ptr.Deep }}`, "viaptr"},
		// A tag names the field for *listing*; the Go name stays
		// reachable, which is what "by name or by json tag" means.
		{`{{ h.Tagged }}`, "t"},
		// Only the first tag key counts, so gojja2 wins and json does
		// not also become an alias.
		{`{{ h.js is undefined }}`, "True"},
	} {
		got, err := renderBridge(t, tc.src, vars)
		if err != nil || got != tc.want {
			t.Errorf("%s = %q, %v; want %q", tc.src, got, err, tc.want)
		}
	}
}

// guide.md promises "value or pointer receiver", so both have to answer when
// the host hands over a pointer -- which is the shape most Go code uses.
func TestBridgeMethodReceivers(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"pointer host, value method", `{{ h.ValueMethod() }}`, "value"},
		{"pointer host, pointer method", `{{ h.PointerMethod() }}`, "pointer"},
		{"as a bare attribute", `{{ h.ValueMethod }}`, ""},
	} {
		got, err := renderBridge(t, tc.src, map[string]any{"h": bridgeFixture()})
		if tc.want == "" {
			continue // a bound method's repr is not what this pins
		}
		if err != nil || got != tc.want {
			t.Errorf("%s: %s = %q, %v; want %q", tc.name, tc.src, got, err, tc.want)
		}
	}
	// A *value* host cannot offer its pointer-receiver methods: that is Go's
	// method set, not a policy choice, and a template sees the absence.
	got, err := renderBridge(t, `{{ h.PointerMethod is defined }}`,
		map[string]any{"h": *bridgeFixture()})
	if err != nil {
		t.Fatalf("%v", err)
	}
	if got != "False" {
		t.Errorf("a value host offered a pointer-receiver method: %q", got)
	}
}

// The default policy exposes only methods that take nothing. Widening it is the
// host's decision, and it is the only way a template chooses a Go method's
// arguments.
func TestBridgeMethodPolicy(t *testing.T) {
	vars := map[string]any{"h": bridgeFixture()}

	got, err := renderBridge(t, `{{ h.WithArg is defined }}`, vars)
	if err != nil || got != "False" {
		t.Errorf("default policy exposed a method taking an argument: %q, %v", got, err)
	}
	if _, err := renderBridge(t, `{{ h.WithArg("x") }}`, vars); err == nil {
		t.Error("default policy let a template call a method with an argument")
	}

	got, err = renderBridge(t, `{{ h.WithArg("x") }}`, vars,
		gojja2.WithMethodPolicy(value.AllMethods))
	if err != nil || got != "arg:x" {
		t.Errorf("AllMethods = %q, %v; want %q", got, err, "arg:x")
	}
	// Even widened, an unexported method stays unreachable: the policy
	// chooses among what reflection offers, and it is not offered.
	got, err = renderBridge(t, `{{ h.hiddenMethod is defined }}`, vars,
		gojja2.WithMethodPolicy(value.AllMethods))
	if err != nil || got != "False" {
		t.Errorf("AllMethods reached an unexported method: %q, %v", got, err)
	}
}

// Nothing in the bridge should hang, panic, or expand without limit.
func TestBridgeAwkwardValues(t *testing.T) {
	type selfStruct struct {
		Name string
		Self *selfStruct
	}
	self := &selfStruct{Name: "s"}
	self.Self = self

	selfMap := map[string]any{"k": "v"}
	selfMap["self"] = selfMap

	selfSlice := []any{1}
	selfSlice = append(selfSlice, selfSlice)

	ch := make(chan int, 1)
	var nilMap map[string]int
	var nilSlice []int
	var nilFn func()
	var nilIface any

	vars := map[string]any{
		"self": self, "selfMap": selfMap, "selfSlice": selfSlice,
		"ch": ch, "cplx": complex(1, 2), "fn": func() {},
		"nilMap": nilMap, "nilSlice": nilSlice, "nilFn": nilFn,
		"nilIface": nilIface, "h": bridgeFixture(),
	}
	for _, src := range []string{
		`{{ self.Name }}`, `{{ self.Self.Self.Self.Name }}`, `{{ self }}`,
		`{{ selfMap.k }}`, `{{ selfMap }}`, `{{ selfSlice|length }}`,
		`{{ ch }}`, `{{ ch is defined }}`, `{{ cplx }}`, `{{ fn }}`,
		`{{ nilMap }}`, `{{ nilSlice }}`, `{{ nilFn }}`, `{{ nilIface }}`,
		`{{ nilMap|length }}`, `{{ nilSlice|length }}`,
		`{{ h.NilPtr }}`, `{{ h.NilPtr.Deep }}`, `{{ h.Fn }}`,
		`{{ h.Count }}`, `{{ h.Count + 1 }}`,
		`{% for k in selfMap %}{{ k }};{% endfor %}`,
	} {
		// The only requirement is that it terminates and does not panic;
		// what it renders is pinned individually below where it matters.
		if _, err := renderBridge(t, src, vars); err != nil && strings.Contains(err.Error(), "panic") {
			t.Errorf("%s panicked: %v", src, err)
		}
	}
	// A uint64 above MaxInt64 must not wrap negative.
	got, err := renderBridge(t, `{{ h.Count }}`, vars)
	if err != nil || got != "18446744073709551615" {
		t.Errorf("uint64 max = %q, %v; want 18446744073709551615", got, err)
	}
	// A nil pointer is None, not a crash and not an empty struct.
	if got, err := renderBridge(t, `{{ h.NilPtr is none }}`, vars); err != nil || got != "True" {
		t.Errorf("nil pointer = %q, %v; want True", got, err)
	}
}

// A render must not write back into the caller's data. The exception is a
// pointer the caller exposed on purpose, which is the caller's object.
func TestBridgeDoesNotMutateCallerData(t *testing.T) {
	items := []int{3, 1, 2}
	dict := map[string]int{"b": 2, "a": 1}
	vars := map[string]any{"items": items, "dict": dict}
	for _, src := range []string{
		`{{ items|sort }}`, `{{ items.append(9) }}`, `{{ items|reverse|list }}`,
		`{{ dict.clear() }}`, `{{ dict.popitem() }}`, `{{ dict.update({"c": 3}) }}`,
	} {
		_, _ = renderBridge(t, src, vars)
	}
	if len(items) != 3 || items[0] != 3 || items[1] != 1 || items[2] != 2 {
		t.Errorf("the caller's slice was mutated: %v", items)
	}
	if len(dict) != 2 || dict["a"] != 1 || dict["b"] != 2 {
		t.Errorf("the caller's map was mutated: %v", dict)
	}
}

// A map's keys come out in a stable order, whatever Go's map iteration does on
// the day. Without that a template's output is not reproducible.
func TestBridgeMapOrderIsStable(t *testing.T) {
	m := map[string]int{}
	for _, k := range strings.Split("q w e r t y u i o p a s d f g h j k l z x c v b n m", " ") {
		m[k] = len(k)
	}
	vars := map[string]any{"m": m}
	first, err := renderBridge(t, `{% for k in m %}{{ k }};{% endfor %}`, vars)
	if err != nil {
		t.Fatalf("%v", err)
	}
	for range 25 {
		again, err := renderBridge(t, `{% for k in m %}{{ k }};{% endfor %}`, vars)
		if err != nil || again != first {
			t.Fatalf("map order changed between renders:\n  %q\n  %q", first, again)
		}
	}
}

// Go promotes an embedded struct's fields to the outer type, and so does the
// bridge.
//
// It did not, and the shape of that was bad: reflect's *method* set promotes,
// so a type embedding a common base exposed `d.Describe()` and hid `d.ID` --
// and hid it silently, rendering nothing rather than failing, which is how a
// host finds out in production.
//
// The rules followed are Go's for reaching a field and encoding/json's for
// listing one, which is the pair that makes `{{ user.ID }}` and
// `{{ user|tojson }}` both mean what a Go author expects. There is no oracle
// for any of it: CPython has no structs to reflect over.
type embedBase struct {
	ID   int
	Kind string
}

func (embedBase) Describe() string { return "base" }

type embedLower struct{ Hidden string }

type embedDerived struct {
	embedBase
	embedLower
	Name string
}

type embedPtr struct {
	*embedBase
	Name string
}

type embedShadow struct {
	embedBase
	ID string
}

// EmbedBase is exported so that a tag on it can name a field whose *value*
// reflection is allowed to read. See embedTaggedUnexported for the corner where
// it is not.
type EmbedBase struct {
	ID   int
	Kind string
}

type embedTagged struct {
	EmbedBase `json:"base"`
	Name      string
}

// embedTaggedUnexported is the one place the bridge cannot follow
// encoding/json. A tag names an embedded field, so the field's own value has to
// be read -- and reflect refuses to hand over the value of an unexported field
// without unsafe access. json gets there by walking the reflect.Value rather
// than taking its interface; this converts through interfaces throughout, so
// the tag is ignored and the fields are promoted as if it were not there.
type embedTaggedUnexported struct {
	embedBase `json:"base"`
	Name      string
}

// embedExported is the shape the flattening rule is actually about: an
// exported embedded type with no tag. Every other case here embeds an
// unexported one, which takes a different branch -- so without this the rule
// that an embedded field is reachable but not *listed* was never exercised.
type embedExported struct {
	EmbedBase
	Name string
}

type embedDupA struct{ Dup int }
type embedDupB struct{ Dup string }

type embedAmbiguous struct {
	embedDupA
	embedDupB
}

func TestBridgePromotesEmbeddedFields(t *testing.T) {
	vars := map[string]any{
		"d":    embedDerived{embedBase: embedBase{ID: 7, Kind: "k"}, embedLower: embedLower{Hidden: "h"}, Name: "n"},
		"p":    embedPtr{embedBase: &embedBase{ID: 8, Kind: "pk"}, Name: "pn"},
		"pnil": embedPtr{Name: "only"},
		"sh":   embedShadow{embedBase: embedBase{ID: 1}, ID: "outer"},
		"te":   embedTagged{EmbedBase: EmbedBase{ID: 5}, Name: "tn"},
		"tu":   embedTaggedUnexported{embedBase: embedBase{ID: 6}, Name: "un"},
		"am":   embedAmbiguous{embedDupA{1}, embedDupB{"x"}},
		"ex":   embedExported{EmbedBase: EmbedBase{ID: 3, Kind: "ek"}, Name: "en"},
	}
	for _, tc := range []struct{ src, want string }{
		// Promoted, including through an embedded type that is itself
		// unexported -- the fields inside it are not.
		{`{{ d.ID }}`, "7"},
		{`{{ d.Kind }}`, "k"},
		{`{{ d.Hidden }}`, "h"},
		{`{{ d.Name }}`, "n"},
		// Promotion does not take the embedded field away.
		{`{{ d.embedBase is undefined }}`, "True"},
		// Methods promoted before and still do.
		{`{{ d.Describe() }}`, "base"},
		// Through an embedded pointer.
		{`{{ p.ID }}`, "8"},
		// A nil embedded pointer is a panic in Go; here the name is
		// simply not there.
		{`{{ pnil.ID is defined }}`, "False"},
		{`{{ pnil.Name }}`, "only"},
		// Shallower wins: the outer ID shadows the embedded one, which
		// stays reachable through the embedded field.
		{`{{ sh.ID }}`, "outer"},
		// Two fields of one name at one depth promote neither.
		{`{{ am.Dup is defined }}`, "False"},
		// A tag on an embedded field names it, and a named field is not
		// promoted through.
		{`{{ te.base.ID }}`, "5"},
		{`{{ te.ID is defined }}`, "False"},
		// Unless the embedded type is unexported, whose value reflection
		// will not hand over: the tag is ignored and the fields promote.
		{`{{ tu.ID }}`, "6"},
		{`{{ tu.base is undefined }}`, "True"},
		// An exported embedded type with no tag: promoted, and the
		// embedded field itself still reachable by its own name.
		{`{{ ex.ID }}`, "3"},
		{`{{ ex.Kind }}`, "ek"},
		{`{{ ex.EmbedBase.ID }}`, "3"},
		// ...but not listed, so the struct serialises flat.
		{`{{ ex|list }}`, "['Name', 'ID', 'Kind']"},
	} {
		got, err := renderBridge(t, tc.src, vars)
		if err != nil || got != tc.want {
			t.Errorf("%s = %q, %v; want %q", tc.src, got, err, tc.want)
		}
	}
}

// Listing follows encoding/json: an embedded struct serialises flat, a tagged
// one nests, a shadowed name appears once, and an ambiguous one not at all.
//
// The check is against encoding/json itself rather than against a string I
// wrote down, so it stays true if Go's rules turn out to be subtler than I
// think they are.
func TestBridgeListingMatchesEncodingJSON(t *testing.T) {
	for _, v := range []any{
		embedDerived{embedBase: embedBase{ID: 7, Kind: "k"}, embedLower: embedLower{Hidden: "h"}, Name: "n"},
		embedPtr{embedBase: &embedBase{ID: 8, Kind: "pk"}, Name: "pn"},
		embedShadow{embedBase: embedBase{ID: 1}, ID: "outer"},
		embedTagged{EmbedBase: EmbedBase{ID: 5}, Name: "tn"},
		embedAmbiguous{embedDupA{1}, embedDupB{"x"}},
		embedExported{EmbedBase: EmbedBase{ID: 3, Kind: "ek"}, Name: "en"},
		bridgeInner{Deep: "d", hidden: "shh"},
	} {
		want, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("%T: %v", v, err)
		}
		got, rerr := renderBridge(t, `{{ v|tojson }}`, map[string]any{"v": v})
		if rerr != nil {
			t.Errorf("%T: %v", v, rerr)
			continue
		}
		var a, b any
		if err := json.Unmarshal(want, &a); err != nil {
			t.Fatalf("%T: %v", v, err)
		}
		if err := json.Unmarshal([]byte(got), &b); err != nil {
			t.Errorf("%T: gojja2 produced invalid JSON %q", v, got)
			continue
		}
		if fmt.Sprint(a) != fmt.Sprint(b) {
			t.Errorf("%T:\n  encoding/json %s\n  gojja2        %s", v, want, got)
		}
	}
}
