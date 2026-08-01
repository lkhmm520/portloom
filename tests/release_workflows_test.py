#!/usr/bin/env python3
from pathlib import Path
import os
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]


class ReleaseWorkflowContracts(unittest.TestCase):
    def test_image_publication_is_exact_immutable_and_external_docs_independent(self):
        text = (ROOT / ".github/workflows/publish-images.yml").read_text()
        self.assertNotIn("https://docs.look4i.com/", text)
        self.assertNotIn("installer unavailable or stale", text)
        workflow_permissions = text.split("jobs:", 1)[0]
        publish_job = text.split("  publish:", 1)[1].split("  verify-published-release:", 1)[0]
        self.assertNotIn("packages: write", workflow_permissions)
        self.assertIn("packages: write", publish_job)
        self.assertIn("Verify release Compose before registry writes", text)
        self.assertIn("needs: [quality-gate, release-contract]", text)
        self.assertIn("scripts/verify-release-compose.py", text)
        self.assertIn("Verify release files embedded in the published docs image", text)
        self.assertIn("published-compose.yml", text)
        self.assertLess(text.index("Verify release Compose before registry writes"), text.index("Build and push exact immutable tag"))
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
        workflow_permissions = text.split("jobs:", 1)[0]
        for job_name in ("verify-production", "promote-stable-channels", "create-release", "cleanup-marker"):
            self.assertIn(f"  {job_name}:\n", text)
        verify_job = text.split("  verify-production:", 1)[1].split("  promote-stable-channels:", 1)[0]
        promotion_job = text.split("  promote-stable-channels:", 1)[1].split("  create-release:", 1)[0]
        release_job = text.split("  create-release:", 1)[1].split("  cleanup-marker:", 1)[0]
        cleanup_job = text.split("  cleanup-marker:", 1)[1]
        self.assertIn("contents: read", workflow_permissions)
        self.assertNotIn("contents: write", workflow_permissions)
        self.assertNotIn("packages: write", workflow_permissions)
        self.assertIn("contents: read", verify_job)
        self.assertNotIn("contents: write", verify_job)
        self.assertNotIn("packages: write", verify_job)
        self.assertIn("persist-credentials: false", verify_job)
        self.assertIn("packages: write", promotion_job)
        self.assertNotIn("contents: write", promotion_job)
        self.assertNotIn("actions/checkout", promotion_job)
        self.assertIn("contents: write", release_job)
        self.assertNotIn("packages: write", release_job)
        self.assertNotIn("actions/checkout", release_job)
        self.assertIn("GH_REPO: ${{ github.repository }}", release_job)
        self.assertIn("contents: write", cleanup_job)
        self.assertNotIn("packages: write", cleanup_job)
        self.assertNotIn("|| echo", cleanup_job)
        self.assertIn("--write-out '%{http_code}'", cleanup_job)
        self.assertIn('case "$status" in', cleanup_job)
        self.assertIn("204)", cleanup_job)
        self.assertIn("404)", cleanup_job)
        self.assertIn("*)", cleanup_job)
        self.assertNotIn("tag='${{ inputs.tag }}'", text)
        self.assertIn("INPUT_TAG: ${{ inputs.tag }}", text)
        self.assertIn("Verify release Compose pins the exact release tag", text)
        self.assertIn("scripts/verify-release-compose.py", text)
        self.assertIn("https://docs.look4i.com/examples/compose.yml", text)
        self.assertIn("production Compose unavailable or stale", text)
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
        promotion = text.index("Promote verified stable release channels")
        self.assertLess(text.index("https://docs.look4i.com/${script}"), promotion)
        self.assertLess(text.index("https://docs.look4i.com/examples/compose.yml"), promotion)
        self.assertLess(
            text.index('scripts/verify-release-compose.py --version "$VERSION" /tmp/production-compose.yml'),
            promotion,
        )
        self.assertLess(promotion, text.index("gh release create"))


    def test_compose_release_contract_executes_and_rejects_rendered_bypasses(self):
        verifier = ROOT / "scripts" / "verify-release-compose.py"
        compose = ROOT / "examples" / "compose.yml"

        def verify(path, version="0.4.3", extra_env=None):
            environment = os.environ.copy()
            environment.update(extra_env or {})
            return subprocess.run(
                ["python3", verifier, "--version", version, path],
                cwd=ROOT,
                env=environment,
                capture_output=True,
                text=True,
            )

        self.assertEqual(0, verify(compose).returncode)
        self.assertNotEqual(0, verify(compose, "0.4.4").returncode)

        source = compose.read_text()
        bait = """x-release-contract-bait:
  expected:
    - image: ghcr.io/lkhmm520/portloom-sshd:0.4.3
    - image: ghcr.io/lkhmm520/portloom-sshd:0.4.3
    - image: ghcr.io/lkhmm520/portloom-server:0.4.3

"""
        deceptive = bait + source.replace(
            "ghcr.io/lkhmm520/portloom-sshd:0.4.3",
            "${PORTLOOM_CONTRACT_SSHD_IMAGE:-ghcr.io/lkhmm520/portloom-sshd:0.4.2}",
        ).replace(
            "ghcr.io/lkhmm520/portloom-server:0.4.3",
            "${PORTLOOM_CONTRACT_SERVER_IMAGE:-ghcr.io/lkhmm520/portloom-server:0.4.2}",
        )
        workflow_env_bypass = source.replace(
            "ghcr.io/lkhmm520/portloom-server:0.4.3",
            "ghcr.io/lkhmm520/portloom-server:${VERSION:-0.4.2}",
            1,
        )
        mutations = {
            "latest": (
                source.replace("portloom-server:0.4.3", "portloom-server:latest", 1),
                {},
            ),
            "extension-bait-with-rendered-old-images": (deceptive, {}),
            "unexpected-service": (
                source + "\n  unexpected:\n    image: ghcr.io/lkhmm520/portloom-agent:latest\n",
                {},
            ),
            "local-build": (
                source.replace(
                    "    image: ghcr.io/lkhmm520/portloom-server:0.4.3",
                    "    image: ghcr.io/lkhmm520/portloom-server:0.4.3\n    build: .",
                    1,
                ),
                {},
            ),
            "workflow-version-environment": (workflow_env_bypass, {"VERSION": "0.4.3"}),
        }
        with tempfile.TemporaryDirectory() as directory:
            for name, (text, environment) in mutations.items():
                mutated = Path(directory) / f"{name}.yml"
                mutated.write_text(text)
                with self.subTest(name=name):
                    result = verify(mutated, extra_env=environment)
                    self.assertNotEqual(
                        0,
                        result.returncode,
                        f"mutation accepted: {name}\nstdout={result.stdout}\nstderr={result.stderr}",
                    )


if __name__ == "__main__":
    unittest.main()
