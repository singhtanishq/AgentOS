#!/usr/bin/env python3
"""Validate README screenshot embedding (Part 31 of the delivery spec).

Verifies:
1. Every supported image under docs/screenshots/ is referenced in README.md.
2. Every image path referenced in README.md exists on disk.
3. Every approved screenshot has an entry in docs/screenshots/SCREENSHOT_INDEX.md.
4. The manifest and README inventories agree.

Exit code 1 with a report when any check fails.
"""

from __future__ import annotations

import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
SCREENSHOT_DIR = ROOT / "docs" / "screenshots"
README = ROOT / "README.md"
MANIFEST = SCREENSHOT_DIR / "SCREENSHOT_INDEX.md"
SUPPORTED = {".png", ".jpg", ".jpeg", ".webp"}


def main() -> int:
    failures: list[str] = []

    readme_text = README.read_text(encoding="utf-8")
    manifest_text = MANIFEST.read_text(encoding="utf-8")

    # All screenshot files on disk.
    on_disk = sorted(
        str(p.relative_to(ROOT))
        for p in SCREENSHOT_DIR.rglob("*")
        if p.suffix.lower() in SUPPORTED
    )

    # Images referenced in README (relative markdown image links).
    referenced = sorted(
        set(re.findall(r"!\[[^\]]*\]\((docs/screenshots/[^)\s]+)\)", readme_text))
    )

    # Files mentioned in the manifest.
    in_manifest = sorted(
        set(
            m.group(1).strip()
            for m in re.finditer(r"\|\s*\d+\s*\|\s*(docs/screenshots/[^|\s]+)\s*\|", manifest_text)
        )
    )

    missing_from_readme = sorted(set(on_disk) - set(referenced))
    broken_paths = sorted(set(referenced) - set(on_disk))
    missing_from_manifest = sorted(set(on_disk) - set(in_manifest))
    manifest_ghosts = sorted(set(in_manifest) - set(on_disk))

    if missing_from_readme:
        failures.append(
            "Screenshots on disk but NOT embedded in README:\n  "
            + "\n  ".join(missing_from_readme)
        )
    if broken_paths:
        failures.append(
            "README references images that DO NOT EXIST:\n  "
            + "\n  ".join(broken_paths)
        )
    if missing_from_manifest:
        failures.append(
            "Screenshots missing from SCREENSHOT_INDEX.md:\n  "
            + "\n  ".join(missing_from_manifest)
        )
    if manifest_ghosts:
        failures.append(
            "Manifest lists files that DO NOT EXIST:\n  "
            + "\n  ".join(manifest_ghosts)
        )

    print(f"Total approved screenshots: {len(on_disk)}")
    print(f"Screenshots embedded in README: {len(referenced)}")
    print(f"Screenshots in manifest: {len(in_manifest)}")
    print(f"Unreferenced screenshots: {len(missing_from_readme)}")
    print(f"Broken image paths: {len(broken_paths)}")

    if failures:
        print("\nVALIDATION FAILED:")
        for failure in failures:
            print(f"\n- {failure}")
        return 1
    print("\nAll screenshot references validated.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
