#!/usr/bin/env python3
from pathlib import Path
import hashlib
import os
import subprocess
import tempfile
import textwrap
import unittest

ROOT = Path(__file__).resolve().parents[1]


class ReleaseWorkflowContracts(unittest.TestCase):
    def test_cli_and_cutover_are_part_of_the_read_only_quality_gate(self):
        text = (ROOT / ".github/workflows/test.yml").read_text()
        self.assertIn('EXPECTED_REVISION="$revision" make release-portloomctl', text)
        self.assertIn("PORTLOOM_CUTOVER_SSHD_IMAGE: portloom-sshd:test", text)
        self.assertIn("run: make cutover-e2e", text)
        self.assertIn("persist-credentials: false", text)
        self.assertIn("--no-tags --depth=1 https://github.com/lkhmm520/portloom.git refs/tags/v0.4.6:refs/tags/v0.4.6", text)
        self.assertIn("./tests/downgrade_interlock_test.sh", text)
        self.assertNotIn("packages: write", text)
        self.assertNotIn("contents: write", text)

    def test_cli_release_assets_are_verified_before_creation(self):
        text = (ROOT / ".github/workflows/finalize-release.yml").read_text()
        verify_job = text.split("  verify-production:\n", 1)[1].split("  promote-stable-channels:\n", 1)[0]
        create_job = text.split("  create-release:\n", 1)[1].split("  cleanup-marker:\n", 1)[0]
        self.assertIn("make release-portloomctl", verify_job)
        self.assertIn("actions/upload-artifact@", verify_job)
        self.assertIn("actions/download-artifact@", create_job)
        self.assertIn("artifact-ids: ${{ needs.verify-production.outputs.cli-artifact-id }}", create_job)
        self.assertLess(create_job.index("cmp - SHA256SUMS"), create_job.index("gh release create"))
        self.assertIn("gh release download", create_job)
        self.assertIn('cmp --silent "$asset" "$download/$asset"', create_job)
        self.assertNotIn("--clobber", create_job)

    def test_cli_release_shell_rejects_tampering_before_running_gh(self):
        workflow = (ROOT / ".github/workflows/finalize-release.yml").read_text()
        job = workflow.split("  create-release:\n", 1)[1].split("  cleanup-marker:\n", 1)[0]
        script = textwrap.dedent(job.split("        run: |\n", 1)[1])
        with tempfile.TemporaryDirectory() as directory:
            work = Path(directory)
            gh = work / "gh"
            gh.write_text("#!/bin/sh\ntouch gh-called\nexit 1\n")
            gh.chmod(0o755)
            env = dict(os.environ, PATH=f"{work}:{os.environ['PATH']}",
                       TAG="v1.2.3", EXPECTED_REVISION="test", EXPECTED_PRERELEASE="false")
            names = ("portloomctl-linux-amd64", "portloomctl-linux-arm64")
            manifest = "".join(f"{hashlib.sha256(name.encode()).hexdigest()}  {name}\n" for name in names)
            env["EXPECTED_CLI_CHECKSUMS"] = hashlib.sha256(manifest.encode()).hexdigest()
            for mutation in ("manifest", "binary", "missing", "none"):
                with self.subTest(mutation=mutation):
                    for name in names:
                        (work / name).write_text(name)
                    (work / "SHA256SUMS").write_text(manifest)
                    if mutation == "manifest":
                        (work / "SHA256SUMS").write_text(manifest + "\n")
                    if mutation == "binary":
                        (work / names[0]).write_text("changed")
                    if mutation == "missing":
                        (work / names[1]).unlink()
                    result = subprocess.run(["bash", "-e", "-o", "pipefail", "-c", script],
                                            cwd=work, env=env, capture_output=True, text=True)
                    self.assertNotEqual(0, result.returncode)
                    self.assertEqual(mutation == "none", (work / "gh-called").exists(), result.stderr)

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

        def verify(path, version="0.5.0", extra_env=None):
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
    - image: ghcr.io/lkhmm520/portloom-sshd:0.5.0
    - image: ghcr.io/lkhmm520/portloom-sshd:0.5.0
    - image: ghcr.io/lkhmm520/portloom-server:0.5.0

"""
        deceptive = bait + source.replace(
            "ghcr.io/lkhmm520/portloom-sshd:0.5.0",
            "${PORTLOOM_CONTRACT_SSHD_IMAGE:-ghcr.io/lkhmm520/portloom-sshd:0.4.2}",
        ).replace(
            "ghcr.io/lkhmm520/portloom-server:0.5.0",
            "${PORTLOOM_CONTRACT_SERVER_IMAGE:-ghcr.io/lkhmm520/portloom-server:0.4.2}",
        )
        workflow_env_bypass = source.replace(
            "ghcr.io/lkhmm520/portloom-server:0.5.0",
            "ghcr.io/lkhmm520/portloom-server:${VERSION:-0.4.2}",
            1,
        )
        mutations = {
            "latest": (
                source.replace("portloom-server:0.5.0", "portloom-server:latest", 1),
                {},
            ),
            "extension-bait-with-rendered-old-images": (deceptive, {}),
            "unexpected-service": (
                source + "\n  unexpected:\n    image: ghcr.io/lkhmm520/portloom-agent:latest\n",
                {},
            ),
            "unexpected-profiled-service": (
                source + "\n  unexpected:\n    profiles: [optional]\n    image: ghcr.io/lkhmm520/portloom-agent:latest\n",
                {},
            ),
            "local-build": (
                source.replace(
                    "    image: ghcr.io/lkhmm520/portloom-server:0.5.0",
                    "    image: ghcr.io/lkhmm520/portloom-server:0.5.0\n    build: .",
                    1,
                ),
                {},
            ),
            "workflow-version-environment": (workflow_env_bypass, {"VERSION": "0.5.0"}),
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
