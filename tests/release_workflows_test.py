#!/usr/bin/env python3
from pathlib import Path
import unittest

ROOT = Path(__file__).resolve().parents[1]


class ReleaseWorkflowContracts(unittest.TestCase):
    def test_image_publication_is_exact_immutable_and_external_docs_independent(self):
        text = (ROOT / ".github/workflows/publish-images.yml").read_text()
        self.assertNotIn("https://docs.look4i.com/", text)
        self.assertNotIn("installer unavailable or stale", text)
        self.assertIn("Verify installers embedded in the published docs image", text)
        self.assertIn("linux/amd64", text)
        self.assertIn("linux/arm64", text)
        self.assertNotIn("type=semver", text)
        self.assertNotIn("type=sha", text)
        self.assertNotIn("value=latest", text)
        self.assertNotIn("value=edge", text)
        self.assertIn("Check immutable exact tag", text)
        self.assertIn("steps.existing.outputs.exists != 'true'", text)
        self.assertIn("org.opencontainers.image.revision", text)
        self.assertGreaterEqual(text.count('docker pull --platform "linux/$arch"'), 2)
        self.assertIn("docker run --platform linux/amd64", text)
        self.assertIn("registry_auth", text)
        self.assertNotIn("Bearer ***", text)
        self.assertNotIn("\\+", text)

    def test_finalization_verifies_production_release_state_and_then_promotes(self):
        text = (ROOT / ".github/workflows/finalize-release.yml").read_text()
        self.assertIn("workflow_dispatch", text)
        self.assertIn("group: finalize-release", text)
        self.assertNotIn("group: finalize-${{ inputs.tag }}", text)
        self.assertIn("contents: write", text)
        self.assertNotIn("tag='${{ inputs.tag }}'", text)
        self.assertIn("INPUT_TAG: ${{ inputs.tag }}", text)
        self.assertIn("https://docs.look4i.com/${script}", text)
        self.assertIn("sha256sum", text)
        self.assertIn("org.opencontainers.image.revision", text)
        self.assertIn("gh release create", text)
        self.assertIn("--prerelease", text)
        self.assertIn("isPrerelease", text)
        self.assertIn("Preflight stable channels against rollback", text)
        self.assertIn("refusing stable channel rollback", text)
        self.assertIn("PortLoom-Revision:", text)
        self.assertIn('docker pull --platform "linux/$arch"', text)
        self.assertIn("Promote verified stable release channels", text)
        self.assertNotIn("\\+", text)
        self.assertLess(text.index("https://docs.look4i.com/${script}"), text.index("Promote verified stable release channels"))
        self.assertLess(text.index("Promote verified stable release channels"), text.index("gh release create"))


if __name__ == "__main__":
    unittest.main()
