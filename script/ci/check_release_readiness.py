#!/usr/bin/env python3
"""Validate version metadata and signing readiness without printing secrets."""
import os
import re
import sys
from pathlib import Path

MACOS_SIGNING_VARS = (
    "MACOS_CERTIFICATE_P12_BASE64",
    "MACOS_CERTIFICATE_PASSWORD",
    "MACOS_CODESIGN_IDENTITY",
)


def check_version_text(version, ref_name):
    errors = []
    bare = version.lstrip("v")
    if not re.fullmatch(r"\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?", bare):
        errors.append(f"VERSION {version!r} is not a semantic version")
    if ref_name.startswith("v") and ref_name.lstrip("v") != bare:
        errors.append(f"tag {ref_name!r} does not match VERSION {version!r}")
    return errors


def signing_warnings(env):
    missing = [name for name in MACOS_SIGNING_VARS if not env.get(name, "").strip()]
    if not missing:
        return []
    return ["macOS Developer ID signing is not fully configured (missing " + ", ".join(missing) + "); builds fall back to ad-hoc signing"]


def main(argv):
    require_signing = "--require-signing" in argv or os.environ.get("PVM_REQUIRE_SIGNING") == "1"
    root = Path(__file__).resolve().parents[2]
    errors = []
    try:
        version = (root / "VERSION").read_text(encoding="utf-8").strip()
        errors += check_version_text(version, os.environ.get("GITHUB_REF_NAME", ""))
    except OSError as e:
        errors.append(f"VERSION unreadable: {e}")

    warnings = signing_warnings(os.environ)
    for w in warnings:
        print(f"::warning::{w}")
    if require_signing:
        errors += warnings
    for e in errors:
        print(f"::error::{e}", file=sys.stderr)
    if errors:
        return 1
    print("release readiness checks passed")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
