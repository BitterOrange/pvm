#!/usr/bin/env python3
import unittest
from pathlib import Path

from check_release_readiness import MACOS_SIGNING_VARS, check_version_text, signing_warnings


class ReleaseReadinessTests(unittest.TestCase):
    def test_repo_version_is_valid(self):
        root = Path(__file__).resolve().parents[2]
        version = (root / "VERSION").read_text(encoding="utf-8").strip()
        self.assertEqual(check_version_text(version, "v" + version), [])

    def test_version_and_tag_mismatch(self):
        self.assertEqual(check_version_text("1.2.3", "master"), [])
        self.assertEqual(check_version_text("v1.2.3", "v1.2.3"), [])
        self.assertTrue(check_version_text("1.2.3", "v1.2.4"))
        self.assertTrue(check_version_text("v1.2.3", "v1.2.4"))
        self.assertTrue(check_version_text("latest", "v1.2.3"))

    def test_signing_warnings_never_leak_secret_values(self):
        warns = signing_warnings({})
        self.assertEqual(len(warns), 1)
        self.assertIn("MACOS_CERTIFICATE_P12_BASE64", warns[0])
        env = {k: "YOUR_TOKEN_VALUE" for k in MACOS_SIGNING_VARS}
        self.assertEqual(signing_warnings(env), [])
        for w in signing_warnings({MACOS_SIGNING_VARS[0]: "YOUR_TOKEN_VALUE"}):
            self.assertNotIn("YOUR_TOKEN_VALUE", w)


if __name__ == "__main__":
    unittest.main()
