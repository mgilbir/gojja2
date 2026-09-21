#!/usr/bin/env python3
# Copyright 2026 The gojja2 Authors
# SPDX-License-Identifier: Apache-2.0
"""Generate the operator conformance corpus for value/ops_test.go.

Every binary operator is applied to every ordered pair drawn from a pool that
covers each boundary Python's numeric tower and sequence protocols have:
bool-as-int, wide integers either side of the float53 cliff, signed zero,
infinities, NaN, empty and non-empty sequences of each kind, and mappings.

The pool is defined twice -- here and in ops_test.go -- so the corpus also
records repr() of each element, and the Go test asserts the pools agree before
it compares a single result. Otherwise the two could drift apart and the test
would quietly be checking the wrong values against each other.
"""

from __future__ import annotations

import json
import operator
import sys
from pathlib import Path

OUT = Path(__file__).resolve().parents[2] / "value" / "testdata" / "ops.jsonl"

POOL = [
    None, True, False,
    0, 1, -1, 7, -7, 3, 2, 10,
    2**63, -(2**63) - 1, 2**53, 2**53 + 1,
    0.0, -0.0, 1.0, 2.5, -2.5, 0.5, 9007199254740992.0,
    float("inf"), float("-inf"), float("nan"),
    "", "a", "ab", "Z", "%s",
    [], [1], [1, 2],
    (), (1,), (1, 2),
    {}, {"a": 1}, {"a": 1, "b": 2},
]

OPS = {
    "+": operator.add,
    "-": operator.sub,
    "*": operator.mul,
    "/": operator.truediv,
    "//": operator.floordiv,
    "%": operator.mod,
    "**": operator.pow,
    "==": operator.eq,
    "!=": operator.ne,
    "<": operator.lt,
    "<=": operator.le,
    ">": operator.gt,
    ">=": operator.ge,
    "in": lambda a, b: a in b,
}


def is_smallint(v: object) -> bool:
    return isinstance(v, int) and not isinstance(v, bool) and abs(v) <= 1000


def skip(op: str, a: object, b: object) -> bool:
    """Skip pairs whose *correct* answer is an allocation the size of a planet.

    CPython would sit there trying to build the value; the answer these cases
    would give is uninteresting either way, and refusing to ask is not the same
    as refusing to implement.
    """
    if op == "**":
        for v in (a, b):
            if isinstance(v, int) and not isinstance(v, bool) and abs(v) > 64:
                return True
            if isinstance(v, float) and abs(v) > 64:
                return True
        return False
    if op == "*":
        seqish = (str, list, tuple)
        if isinstance(a, seqish) and isinstance(b, int) and not is_smallint(b):
            return True
        if isinstance(b, seqish) and isinstance(a, int) and not is_smallint(a):
            return True
    return False


def evaluate(op: str, a: object, b: object) -> dict:
    try:
        result = OPS[op](a, b)
    except Exception as exc:  # noqa: BLE001 - the exception type is the answer
        return {"err": type(exc).__name__, "msg": str(exc)}
    return {"repr": repr(result)}


def main() -> int:
    cases = []
    for i, a in enumerate(POOL):
        for j, b in enumerate(POOL):
            for op in OPS:
                if skip(op, a, b):
                    continue
                case = {"a": i, "op": op, "b": j}
                case.update(evaluate(op, a, b))
                cases.append(case)

    # One JSON object per line: compact enough to commit, still diffable.
    # The interpreter goes in the header because several operators word a
    # failure differently by release -- every division by zero collapsed to one
    # sentence in 3.14 -- so a corpus replayed against the wrong version
    # disagrees about a hundred cases for no reason anyone would look for. The
    # Go test refuses to run if this does not match value.DefaultPythonVersion.
    header = {
        "python": ".".join(map(str, sys.version_info[:2])),
        "pool": [repr(v) for v in POOL],
    }
    lines = [json.dumps(header, ensure_ascii=False)]
    lines += [json.dumps(c, ensure_ascii=False, separators=(",", ":")) for c in cases]
    OUT.parent.mkdir(parents=True, exist_ok=True)
    OUT.write_text("\n".join(lines) + "\n", encoding="utf-8")
    print(f"{OUT.name}: {len(POOL)} pool values, {len(cases)} cases", file=sys.stderr)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
