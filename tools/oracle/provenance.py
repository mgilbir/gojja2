#!/usr/bin/env python3
# Copyright 2026 The gojja2 Authors
# SPDX-License-Identifier: Apache-2.0
"""Where an imported corpus came from.

A corpus built from someone else's repository is only reproducible if the
revision it was built from is written down. `make suites` pins each clone, but
the pin lives in the Makefile and the corpus outlives any particular checkout,
so the resolved SHA is recorded next to the cases themselves.
"""

from __future__ import annotations

import subprocess
from pathlib import Path


def revision(repo: Path) -> str:
    """Return the commit a clone is checked out at, or a marker if unknown."""
    try:
        out = subprocess.run(
            ["git", "-C", str(repo), "rev-parse", "HEAD"],
            capture_output=True,
            text=True,
            check=True,
        )
    except (subprocess.CalledProcessError, FileNotFoundError):
        return "unknown"
    return out.stdout.strip() or "unknown"


def oracle_versions() -> dict[str, str]:
    """The interpreter the goldens were recorded with."""
    import importlib.metadata
    import platform

    import jinja2

    return {
        "jinja2": jinja2.__version__,
        "markupsafe": importlib.metadata.version("markupsafe"),
        "python": platform.python_version(),
    }


def block(sources: list[tuple[str, Path, str]]) -> list[str]:
    """Render the provenance table for a SOURCES.md.

    Each entry is (label, clone path, license).
    """
    lines = [
        "## Provenance",
        "",
        "| source | revision | license |",
        "| --- | --- | --- |",
    ]
    for label, path, license_name in sources:
        lines.append(f"| {label} | `{revision(path)}` | {license_name} |")
    versions = oracle_versions()
    lines += [
        "",
        "Goldens recorded with CPython jinja2 "
        f"{versions['jinja2']}, markupsafe {versions['markupsafe']}, "
        f"Python {versions['python']}. An upgrade of any of the three means "
        "regenerating this corpus wholesale, never piecemeal.",
        "",
    ]
    return lines
