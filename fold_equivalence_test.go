// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"context"
	"math/big"
	"strings"
	"testing"

	"github.com/mgilbir/gojja2"
	"github.com/mgilbir/gojja2/value"
)

// TestFoldedMatchesUnfolded: an expression over literals is folded at compile
// time and the same expression over a *name* is not, so the two paths must agree
// about every value they can both be handed.
//
// This is a property rather than a differential, and it answers a question the
// differential cannot: jinja2's optimizer folds too, so a fold that changes an
// answer *the same way* in both engines agrees with CPython and diverges from
// itself. Two folds this session were the other kind and the differential caught
// them -- a folded print escaped by the pinned interpreter's isprintable while
// the render used the environment's, and a StrictUndefined's refusal escaped
// `from_string` where the render deferred -- but nothing was asking whether the
// two paths agreed at all.
//
// The substitution is one atom, because folding is bottom-up: a single
// non-constant leaf makes every expression above it non-constant, which is what
// makes the second form the render path.
//
// What this cannot see, measured by planting each one:
//
//   - a fold that does not happen. Skipping one makes the first form take the
//     render path too, so the two agree and the property holds. Coverage and
//     TestFoldingStillHappens are what watch for that.
//   - a fold wrong in the same way as the render. Both sides move together and
//     the property holds; only the differential against CPython sees it.
//
// What it does see is a fold that answers *differently*, which is the shape of
// both folds this session's soak caught. The version axis is load-bearing for
// exactly that reason: without it, putting the pinned interpreter's tables back
// into either the print fold or the concat fold leaves this green.
// pair names one (atom, shape) combination, which is what an exception is listed
// against.
type pair struct{ atom, shape string }

func TestFoldedMatchesUnfolded(t *testing.T) {
	// Each atom is a literal and a name bound to exactly that value, so the
	// two forms differ only in whether the folder can see it.
	atoms := []struct {
		name    string
		literal string
		v       value.Value
	}{
		{"an_int", "3", value.Int(3)},
		{"a_float", "2.5", value.Float(2.5)},
		{"a_string", "'ab'", value.String("ab")},
		{"markup", "('<b>'|safe)", value.Safe("<b>")},
		{"true", "true", value.Bool(true)},
		{"none", "none", value.None},
		{"a_list", "[1, 2]", value.NewList(value.Int(1), value.Int(2))},
		{"a_tuple", "((1, 2))", value.NewTuple(value.Int(1), value.Int(2))},
		{"a_dict", "{'a': 1}", value.StringDict([]string{"a"}, []value.Value{value.Int(1)})},
		{"a_wide_int", "(10 ** 30)", value.BigInt(wide(30))},
		{"a_nonprintable", "'Ᲊ'", value.String("Ᲊ")},
	}
	// %s is the atom. Each shape is something whose answer a fold could get
	// wrong: a conversion to text, an escape, a container's repr, a
	// comparison, a short circuit, a subscript, a refusal.
	shapes := []string{
		`{{ @ }}`,
		`{{ @|string }}`,
		`{{ @|pprint }}`,
		`{{ @|upper }}`,
		`{{ @|escape }}`,
		`{{ @|forceescape }}`,
		`{{ @|safe }}`,
		`{{ @|tojson }}`,
		`{{ @|urlencode }}`,
		`{{ @|length }}`,
		`{{ @|list }}`,
		`{{ @|first }}`,
		`{{ @|join('-') }}`,
		`{{ @|abs }}`,
		`{{ @|int }}`,
		`{{ @|round }}`,
		`{{ @ ~ 'x' }}`,
		// A *container* through `~`, because str() of one is its repr and a
		// repr escapes by the interpreter's isprintable -- which is the
		// only route to the concat fold's own conversion. Without these,
		// planting the pin's tables back into that fold leaves this green.
		`{{ [@] ~ 'x' }}`,
		`{{ {'k': @} ~ 'x' }}`,
		`{{ ((@, 1)) ~ 'x' }}`,
		`{{ 'x' ~ @ }}`,
		`{{ @ + 1 }}`,
		`{{ @ * 2 }}`,
		`{{ @ == 1 }}`,
		`{{ @ < 2 }}`,
		`{{ @ in [1, 2] }}`,
		`{{ 1 in @ }}`,
		`{{ not @ }}`,
		`{{ @ and 1 }}`,
		`{{ @ or 1 }}`,
		`{{ 1 if @ else 2 }}`,
		`{{ @ if 1 else 2 }}`,
		`{{ [@] }}`,
		`{{ {'k': @} }}`,
		`{{ ((@,)) }}`,
		`{{ @[0] }}`,
		`{{ @.nope }}`,
		`{{ @|attr('nope') }}`,
		`{{ '%s' % @ }}`,
		`{{ '{}'.format(@) }}`,
		`{{ @ is defined }}`,
		`{{ @ is number }}`,
		`{{ @|default('d') }}`,
		`{% if @ %}y{% else %}n{% endif %}`,
		`{% for i in @ %}[{{ i }}]{% endfor %}`,
	}
	undefineds := []struct {
		name string
		b    value.UndefinedBehavior
	}{
		{"default", value.UndefinedDefault},
		{"strict", value.UndefinedStrict},
		{"chainable", value.UndefinedChainable},
		{"debug", value.UndefinedDebug},
	}

	// The one shape that must *not* agree, and the reason. Concat.as_const
	// joins `str()` of each operand, so a Markup the folder can see loses its
	// safety and the output escapes it -- where the run-time concat answers
	// Markup and the output leaves it alone. jinja2 does the same, and
	// escape/concat_markup_folded_and_not pins both answers against CPython.
	//
	// Listing it is an admission, not a waiver: a pair named here that starts
	// agreeing fails too, so the exception cannot quietly become true.
	knownAsymmetric := map[pair]string{
		{"markup", `{{ @ ~ 'x' }}`}: "Concat.as_const joins str(), losing Markup",
		{"markup", `{{ 'x' ~ @ }}`}: "Concat.as_const joins str(), losing Markup",
	}

	const hole = "gojja2foldprobe"
	checked, mismatched := 0, 0
	sawAsymmetry := map[pair]bool{}
	// The version axis matters as much as the other two: a fold that reads the
	// *pinned* interpreter's tables where the render reads the environment's
	// agrees with itself on the pin and nowhere else. That is one of the two
	// folds this session's soak found, and without this loop planting it back
	// leaves the test green.
	versions := []value.PythonVersion{
		value.Python311, value.Python312, value.Python313, value.Python314,
	}
	for _, autoescape := range []bool{false, true} {
		for _, u := range undefineds {
			for _, pv := range versions {
				env, err := gojja2.New(gojja2.WithAutoescape(autoescape),
					gojja2.WithUndefined(u.b), gojja2.WithPythonVersion(pv))
				if err != nil {
					t.Fatal(err)
				}
				for _, atom := range atoms {
					vars := map[string]value.Value{hole: atom.v}
					for _, shape := range shapes {
						folded := render(t, env, fill(shape, atom.literal), vars)
						unfolded := render(t, env, fill(shape, hole), vars)
						checked++
						key := pair{atom.name, shape}
						// The exception applies only where escaping is
						// on: with it off there is nothing to escape
						// and the two paths agree again.
						expectDiffer := autoescape && knownAsymmetric[key] != ""
						if expectDiffer {
							sawAsymmetry[key] = true
							if folded == unfolded {
								t.Errorf("%s is listed as asymmetric (%s) but both "+
									"paths now answer %s -- remove the entry",
									fill(shape, atom.literal), knownAsymmetric[key], folded)
							}
							continue
						}
						if folded == unfolded {
							continue
						}
						mismatched++
						if mismatched <= 12 {
							t.Errorf("autoescape=%v undefined=%s python=%s atom=%s\n"+
								"  %s\n    folded   = %s\n    unfolded = %s",
								autoescape, u.name, pv, atom.name,
								fill(shape, atom.literal), folded, unfolded)
						}
					}
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("compared nothing; the table is empty")
	}
	for key, why := range knownAsymmetric {
		if !sawAsymmetry[key] {
			t.Errorf("the asymmetry listed for %s of %s (%s) was never reached; "+
				"the table no longer produces it", key.shape, key.atom, why)
		}
	}
	t.Logf("%d folded/unfolded pairs agree, %d listed as asymmetric",
		checked-mismatched-len(knownAsymmetric), len(knownAsymmetric))
}

// fill puts the atom in the shape's hole. A plain placeholder rather than a
// printf verb, because half of these templates contain a `%` of their own.
func fill(shape, atom string) string { return strings.ReplaceAll(shape, "@", atom) }

// wide is 10**n as an exact integer, which is what the literal in the table
// folds to.
func wide(n int) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil)
}

// render returns the output, or the error, as one comparable string. A compile
// error counts: a fold that refuses where the render would not is the divergence
// this test exists for, and it shows up as one side compiling and the other not.
func render(t *testing.T, env *gojja2.Environment, src string,
	vars map[string]value.Value) string {
	t.Helper()
	tmpl, err := env.FromString(src)
	if err != nil {
		return "COMPILE: " + err.Error()
	}
	var b strings.Builder
	if err := tmpl.RenderValues(context.Background(), &b, vars); err != nil {
		return "ERROR: " + err.Error()
	}
	return "OK: " + b.String()
}
