// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import (
	"context"
	"strings"
	"testing"
)

// TestStrFormatSpecs: str.format took everything between the braces as a field
// name, so a conversion and a format spec were not parsed at all -- they were
// looked up as part of the name and then quietly dropped. `{:>8}` returned the
// value unpadded and `{!r}` returned str() instead of repr(), both without
// complaint, which is the shape of bug a template renders rather than reports.
func TestStrFormatSpecs(t *testing.T) {
	env := New()
	for _, tc := range []struct{ src, want string }{
		// --- conversions ---
		{`{{ '{!r}'.format('ab') }}`, `'ab'`},
		{`{{ '{!s}'.format('ab') }}`, `ab`},
		{`{{ '{!a}'.format('é') }}`, `'\xe9'`},
		{`{{ '{!r}'.format(1.5) }}|{{ '{!r}'.format(none) }}`, `1.5|None`},
		// The conversion runs first, then the spec lays out its text.
		{`{{ '[{!r:>8}]'.format('ab') }}`, `[    'ab']`},

		// --- strings: align, fill, width, precision ---
		{`[{{ '{:10}'.format('ab') }}]`, `[ab        ]`},
		{`[{{ '{:>10}'.format('ab') }}]`, `[        ab]`},
		{`[{{ '{:^10}'.format('ab') }}]`, `[    ab    ]`},
		{`[{{ '{:*^10}'.format('ab') }}]`, `[****ab****]`},
		{`[{{ '{:.3}'.format('abcdef') }}]`, `[abc]`},
		{`[{{ '{:>10.3}'.format('abcdef') }}]`, `[       abc]`},

		// --- integers ---
		{`[{{ '{:5d}'.format(42) }}]`, `[   42]`},
		{`[{{ '{:<5d}'.format(42) }}]`, `[42   ]`},
		{`[{{ '{:05d}'.format(42) }}]`, `[00042]`},
		// A zero fill is '=' alignment: the sign stays in front of it.
		{`[{{ '{:05d}'.format(-42) }}]`, `[-0042]`},
		{`[{{ '{:+d}'.format(42) }}|{{ '{: d}'.format(42) }}]`, `[+42| 42]`},
		{`[{{ '{:,}'.format(1234567) }}|{{ '{:_}'.format(1234567) }}]`, `[1,234,567|1_234_567]`},
		{`[{{ '{:b}'.format(10) }}|{{ '{:o}'.format(10) }}|{{ '{:x}'.format(255) }}|{{ '{:X}'.format(255) }}]`,
			`[1010|12|ff|FF]`},
		{`[{{ '{:#b}'.format(10) }}|{{ '{:#o}'.format(10) }}|{{ '{:#x}'.format(255) }}]`,
			`[0b1010|0o12|0xff]`},
		// '#' with a zero fill keeps the prefix ahead of the zeros too.
		{`[{{ '{:#06x}'.format(255) }}]`, `[0x00ff]`},
		{`[{{ '{:c}'.format(65) }}]`, `[A]`},
		// An integer takes the float codes by converting first.
		{`[{{ '{:.2f}'.format(42) }}|{{ '{:e}'.format(42) }}]`, `[42.00|4.200000e+01]`},

		// --- floats ---
		{`[{{ '{:f}'.format(1.5) }}|{{ '{:.2f}'.format(1.5) }}|{{ '{:.0f}'.format(1.5) }}]`,
			`[1.500000|1.50|2]`},
		{`[{{ '{:e}'.format(1234.5) }}|{{ '{:E}'.format(1234.5) }}]`,
			`[1.234500e+03|1.234500E+03]`},
		{`[{{ '{:g}'.format(1234.5) }}|{{ '{:.3g}'.format(1234.5) }}]`, `[1234.5|1.23e+03]`},
		{`[{{ '{:%}'.format(0.25) }}|{{ '{:.1%}'.format(0.25) }}]`, `[25.000000%|25.0%]`},
		{`[{{ '{:10.2f}'.format(1.5) }}|{{ '{:<10.2f}'.format(1.5) }}]`, `[      1.50|1.50      ]`},
		{`[{{ '{:08.3f}'.format(-1.5) }}]`, `[-001.500]`},
		{`[{{ '{:,.2f}'.format(1234567.891) }}]`, `[1,234,567.89]`},
		// No type at all keeps str()'s digits rather than six of them.
		{`[{{ '{:10}'.format(2.5) }}]`, `[       2.5]`},

		// --- bool formats as the integer it is, once a spec is given ---
		{`[{{ '{}'.format(true) }}|{{ '{:>8}'.format(true) }}|{{ '{:d}'.format(true) }}]`,
			`[True|       1|1]`},

		// --- nested replacement fields in the spec ---
		{`[{{ '{:{}}'.format(3, 6) }}]`, `[     3]`},
		{`[{{ '{0:{1}.{2}f}'.format(2.5, 9, 3) }}]`, `[    2.500]`},
		{`[{{ '{v:>{w}}'.format(v='ab', w=6) }}]`, `[    ab]`},

		// --- field names with an index or a key, plus a spec ---
		{`[{{ '{0[1]:03d}'.format([7, 8]) }}]`, `[008]`},
		{`[{{ '{x[1]:>4}'.format(x=[7, 8]) }}]`, `[   8]`},

		// An empty spec is str(), which is why these render at all.
		{`{{ '{}'.format(none) }}|{{ '{}'.format([1, 2]) }}`, `None|[1, 2]`},
	} {
		tmpl, err := env.FromString(tc.src)
		if err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		got, err := tmpl.RenderString(context.Background(), nil)
		if err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s = %q, want %q", tc.src, got, tc.want)
		}
	}

	// The refusals, which are as much of the contract as the layouts.
	for _, tc := range []struct{ src, want string }{
		{`{{ '{!z}'.format(1) }}`, "Unknown conversion specifier z"},
		{`{{ '{:*}'.format(1) }}`, "Unknown format code '*' for object of type 'int'"},
		{`{{ '{:qq}'.format(1) }}`, "Invalid format specifier 'qq' for object of type 'int'"},
		{`{{ '{:s}'.format(true) }}`, "Unknown format code 's' for object of type 'bool'"},
		// Only the empty spec reaches object.__format__.
		{`{{ '{:>8}'.format(none) }}`, "unsupported format string passed to NoneType.__format__"},
		{`{{ '{:d}'.format([1]) }}`, "unsupported format string passed to list.__format__"},
		// One string counts its fields or names them, never both.
		{`{{ '{0!r:>{}}'.format(1) }}`,
			"cannot switch from manual field specification to automatic field numbering"},
		{`{{ '{} {0}'.format(1) }}`,
			"cannot switch from automatic field numbering to manual field specification"},
		// Three different complaints about a field that never closes.
		{`{{ '{'.format(1) }}`, "Single '{' encountered in format string"},
		{`{{ '{0'.format(1) }}`, "expected '}' before end of string"},
		{`{{ '{:{}'.format(1) }}`, "unmatched '{' in format spec"},
		{`{{ '{99}'.format(1) }}`, "Replacement index 99 out of range"},
		{`{{ '{missing}'.format(1) }}`, "'missing'"},
		{`{{ '{0.foo}'.format(1) }}`, "'int' object has no attribute 'foo'"},
	} {
		tmpl, err := env.FromString(tc.src)
		if err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		_, err = tmpl.RenderString(context.Background(), nil)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want %q", tc.src, err, tc.want)
		}
	}
}
