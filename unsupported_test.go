// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mgilbir/gojja2"
	"github.com/mgilbir/gojja2/errs"
)

// The three codec error handlers CPython answers and gojja2 cannot are found
// when the template is compiled, not when a payload finally reaches them.
//
// That is the whole point of the check. The handler is looked up lazily --
// CPython does that too, and gojja2 matches -- so `.encode("ascii",
// "namereplace")` renders for every ASCII string and raises on the first
// accented letter, and `.decode("utf-8", "surrogateescape")` renders for every
// well-formed input and raises on the first malformed byte. A template like
// that passes its tests and fails in production, on the input least convenient
// to fail on.
func TestUnsupportedIsFoundAtCompileTime(t *testing.T) {
	for name, tc := range map[string]struct {
		src  string
		want []string // one Construct per finding, in source order
		line int      // line of the first finding
	}{
		"encode, positional": {
			src:  `{{ s.encode("ascii", "namereplace") }}`,
			want: []string{`.encode(..., "namereplace")`}, line: 1,
		},
		"encode, keyword": {
			src:  `{{ s.encode("ascii", errors="namereplace") }}`,
			want: []string{`.encode(..., "namereplace")`}, line: 1,
		},
		"encode, keyword only": {
			src:  `{{ s.encode(errors="namereplace") }}`,
			want: []string{`.encode(..., "namereplace")`}, line: 1,
		},
		// The check runs after constant folding, so a handler spelled
		// as pieces is a literal by the time it is looked at.
		"encode, folded from constants": {
			src:  `{{ s.encode("ascii", "name" ~ "replace") }}`,
			want: []string{`.encode(..., "namereplace")`}, line: 1,
		},
		"decode, surrogateescape": {
			src:  "{% if x %}\n{{ s.encode().decode('utf-8', 'surrogateescape') }}\n{% endif %}",
			want: []string{`.decode(..., "surrogateescape")`}, line: 2,
		},
		"decode, surrogatepass": {
			src:  `{% macro m(v) %}{{ v.encode().decode("utf-8", "surrogatepass") }}{% endmacro %}`,
			want: []string{`.decode(..., "surrogatepass")`}, line: 1,
		},
		"two in one template": {
			src: `{{ s.encode("ascii", "namereplace") }}` +
				`{{ s.encode().decode("utf-8", "surrogatepass") }}`,
			want: []string{`.encode(..., "namereplace")`, `.decode(..., "surrogatepass")`},
			line: 1,
		},

		// Nothing below is a gap, and a check that cried wolf on these
		// would be worse than no check.
		"a handler gojja2 has":      {src: `{{ s.encode("ascii", "backslashreplace") }}`},
		"the other direction":       {src: `{{ s.encode("ascii", "surrogateescape") }}`},
		"decode, refused by both":   {src: `{{ b.decode("utf-8", "namereplace") }}`},
		"a handler nobody has":      {src: `{{ s.encode("ascii", "nosuch") }}`},
		"no handler named":          {src: `{{ s.encode("ascii") }}`},
		"not a codec call":          {src: `{{ s.replace("ascii", "namereplace") }}`},
		"handler from a variable":   {src: `{{ s.encode("ascii", h) }}`},
		"handler from an attribute": {src: `{{ s.encode("ascii", cfg.errors) }}`},
	} {
		t.Run(name, func(t *testing.T) {
			var reported []gojja2.Unsupported
			env := mustEnv(gojja2.WithUnsupportedReport(func(u gojja2.Unsupported) {
				reported = append(reported, u)
			}))
			tmpl, err := env.FromString(tc.src)
			if err != nil {
				t.Fatalf("compiling: %v", err)
			}
			got := tmpl.Unsupported()
			if len(got) != len(tc.want) {
				t.Fatalf("found %d, want %d: %+v", len(got), len(tc.want), got)
			}
			// The hook and the accessor must agree; one of the two
			// silently going empty is exactly the failure this
			// whole file exists to prevent.
			if len(reported) != len(got) {
				t.Errorf("the report hook saw %d findings, Unsupported() has %d",
					len(reported), len(got))
			}
			for i, want := range tc.want {
				if got[i].Construct != want {
					t.Errorf("finding %d is %q, want %q", i, got[i].Construct, want)
				}
				if got[i].Why == "" {
					t.Errorf("finding %d has no reason", i)
				}
			}
			if len(got) > 0 && got[0].Line != tc.line {
				t.Errorf("first finding is on line %d, want %d", got[0].Line, tc.line)
			}
		})
	}
}

// Reporting is the default because refusing would reject a template that works
// today, having never handed its handler anything to do.
func TestUnsupportedReportsByDefault(t *testing.T) {
	const src = `{{ s.encode("ascii", "namereplace") }}`
	tmpl, err := mustEnv().FromString(src)
	if err != nil {
		t.Fatalf("the default must compile the template: %v", err)
	}
	if n := len(tmpl.Unsupported()); n != 1 {
		t.Fatalf("Unsupported() has %d findings, want 1", n)
	}
	// And it still renders, right up to the payload that needs the handler.
	out, err := tmpl.RenderString(context.Background(), map[string]any{"s": "abc"})
	if err != nil || out != `b'abc'` {
		t.Errorf("rendered %q, %v; want b'abc' and no error", out, err)
	}
	_, err = tmpl.RenderString(context.Background(), map[string]any{"s": "é"})
	if !errors.Is(err, errs.LookupError) {
		t.Errorf("the payload that needs the handler gave %v, want LookupError", err)
	}
}

// RefuseUnsupported moves that failure to compile time, where it cannot depend
// on which input arrives.
func TestRefuseUnsupported(t *testing.T) {
	env := mustEnv(gojja2.WithUnsupportedLeniency(gojja2.RefuseUnsupported))
	_, err := env.FromString(`{{ s.encode("ascii", "namereplace") }}`)
	if err == nil {
		t.Fatal("compiled; want a refusal")
	}
	if !errors.Is(err, errs.TemplateAssertionError) {
		t.Errorf("got %v, want TemplateAssertionError", errs.KindOf(err))
	}
	for _, want := range []string{"namereplace", "Unicode name database", "backslashreplace"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q: %v", want, err)
		}
	}
	// A template with nothing to find still compiles under the strict
	// setting, or the setting would simply be "refuse everything".
	if _, err := env.FromString(`{{ s.encode("ascii", "backslashreplace") }}`); err != nil {
		t.Errorf("a template with no findings was refused: %v", err)
	}
}

func TestUnsupportedLeniencyRejectsAnUnknownValue(t *testing.T) {
	if _, err := gojja2.New(gojja2.WithUnsupportedLeniency(gojja2.UnsupportedLeniency(7))); err == nil {
		t.Error("New accepted an UnsupportedLeniency that is neither value")
	}
}

// The name database is not carried, so every well-formed \N{...} is refused --
// and the message says that rather than borrowing CPython's wording for a name
// that does not exist. BULLET is a real name; being told it is unknown sent
// readers looking for a typo that was not there.
//
// A *malformed* escape is wrong under CPython too, so those keep CPython's own
// message, and testdata/corpus/errors/n_escape_malformed_* grades them. The
// boundary is exact: an empty name is malformed, and a single space is a name.
func TestNEscapeSaysWhyItIsRefused(t *testing.T) {
	env := mustEnv()
	for name, tc := range map[string]struct{ src, want string }{
		"a real name": {`{{ "\N{BULLET}" }}`, `\N{BULLET} needs the Unicode name database`},
		"a name that is not": {`{{ "\N{NOT A REAL NAME}" }}`,
			`\N{NOT A REAL NAME} needs the Unicode name database`},
		"a space is a name":  {`{{ "\N{ }" }}`, `\N{ } needs the Unicode name database`},
		"empty is malformed": {`{{ "\N{}" }}`, `malformed \N character escape`},
		"unclosed":           {`{{ "\N{BULLET" }}`, `malformed \N character escape`},
		"no brace":           {`{{ "\N" }}`, `malformed \N character escape`},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := env.FromString(tc.src)
			if err == nil {
				t.Fatalf("%s compiled; every \\N{...} is refused", tc.src)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %q, want it to contain %q", err, tc.want)
			}
		})
	}
	// The escape that does work, and what the message tells you to use:
	// the same code point, spelled the way the refusal suggests.
	tmpl, err := env.FromString(`{{ "\u2022" }}`)
	if err != nil {
		t.Fatalf("compiling a \\u escape: %v", err)
	}
	out, err := tmpl.RenderString(context.Background(), nil)
	if err != nil || out != "•" {
		t.Errorf("a \\u escape rendered %q, %v", out, err)
	}
}
