#!/usr/bin/env python3
# Copyright 2026 The gojja2 Authors
# SPDX-License-Identifier: Apache-2.0
"""Measure how deep a *value* each interpreter will walk.

The nesting of a value is chosen at render time, not in the template, so every
walk of one -- printing it, comparing two, encoding JSON, pretty-printing --
needs a wall or a deep value takes the stack out. gojja2 walls each at 1,000 and
docs/limits.md published CPython's own wall beside it as "about the same depth".

That was measured once, on 3.11, and CPython moved: 3.12 gave the C recursion
limit its own counter, so the walks that recurse in C go ten times deeper, and
3.14 grows the stack instead of counting frames at all. pprint did not move,
because it recurses in Python.

  walk      3.11    3.12    3.13    3.14
  pprint     328     329     329      329
  tojson     989   9,994   9,995   52,162

So the published number was true of one interpreter and wrong about the pinned
one by a factor of ten. It is measured here now, per interpreter, and
TestValueWalkDepthsAreMeasured grades the table against this file -- both halves
of it, because gojja2's own wall is asserted by rendering at it.

The numbers are the *last depth that works*: one more raises RecursionError.
They are not exact frame counts and will drift with a patch release; what the
table says is the order of magnitude and which side of gojja2's wall it falls.
"""

from __future__ import annotations

import json
import sys

import pyversions

ROOT = pyversions.ROOT
DST = ROOT / "testdata/value_depth.json"
VERSIONS = [pyversions.default()] + pyversions.others()

# One walk per entry, keyed the way docs/limits.md names it.
WALKS = {
    "pprint": "{{ ns.l|pprint|length }}",
    "tojson": "{{ ns.l|tojson|length }}",
    "print": "{{ ns.l|string|length }}",
    "equal": "{{ (ns.l == ns.m)|string }}",
    "less": "{{ (ns.l < ns.m)|string }}",
    "sort": "{{ [ns.l, ns.m]|sort|length }}",
}

# Runs in the child interpreter: builds a value N deep and walks it.
PROBE = '''
import json, resource, sys
resource.setrlimit(resource.RLIMIT_AS, (6 << 30, 6 << 30))
import jinja2

BUILD = ("{% set ns = namespace(l=[1], m=[1]) %}"
         "{% for i in range(N) %}{% set ns.l = [ns.l] %}{% set ns.m = [ns.m] %}"
         "{% endfor %}")
WALKS = json.loads(sys.argv[1])
CEILING = 1 << 18
env = jinja2.Environment()


def works(walk, n):
    src = BUILD.replace("range(N)", "range(%d)" % n) + WALKS[walk]
    try:
        env.from_string(src).render()
        return True
    except RecursionError:
        return False


def wall(walk):
    """The last depth that works, by doubling and then bisecting."""
    lo = hi = 1
    while works(walk, hi):
        lo, hi = hi, hi * 2
        if hi > CEILING:
            return None
    while lo + 1 < hi:
        mid = (lo + hi) // 2
        if works(walk, mid):
            lo = mid
        else:
            hi = mid
    return lo


print(json.dumps({w: wall(w) for w in WALKS}))
'''


def measure(version: str) -> dict[str, int | None]:
    proc = pyversions.run(version, ["-c", PROBE, json.dumps(WALKS)],
                          capture_output=True)
    out = json.loads(proc.stdout)
    sys.stderr.write(f"  CPython {version}: " +
                     ", ".join(f"{k}={v}" for k, v in out.items()) + "\n")
    return out


def main() -> int:
    sys.stderr.write("measuring how deep a value each interpreter walks:\n")
    data = {
        "_comment": ("The last value nesting depth each walk survives, measured "
                     "by tools/oracle/gen_value_depth.py. Regenerate with "
                     "`make value-depth`."),
        "walks": {v: measure(v) for v in VERSIONS},
    }
    DST.write_text(json.dumps(data, indent=2, sort_keys=True) + "\n",
                   encoding="utf-8")
    sys.stderr.write(f"wrote {DST.relative_to(ROOT)}\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
