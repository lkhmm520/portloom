import pathlib
import stat
import subprocess
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[1]


class BeginnerComposeOnboardingTest(unittest.TestCase):
    def test_beginner_compose_bundle_and_pages_are_published(self):
        expected_files = [
            ROOT / "examples" / "compose.yml",
            ROOT / "examples" / "compose.env.example",
            ROOT / "docs" / "guide" / "compose-install.md",
            ROOT / "docs" / "en" / "guide" / "compose-install.md",
        ]
        for path in expected_files:
            with self.subTest(path=path):
                self.assertTrue(path.is_file(), f"missing beginner artifact: {path}")

        sync_script = (ROOT / "scripts" / "sync-doc-examples.mjs").read_text()
        self.assertIn("'compose.yml'", sync_script)
        self.assertIn("'compose.env.example'", sync_script)

    def test_synced_downloads_are_readable_by_the_unprivileged_docs_server(self):
        subprocess.run(["node", "scripts/sync-doc-examples.mjs"], cwd=ROOT, check=True)
        for name in ["compose.yml", "compose.env.example"]:
            path = ROOT / "docs" / "public" / "examples" / name
            with self.subTest(path=path):
                self.assertEqual(0o644, stat.S_IMODE(path.stat().st_mode))

    def test_compose_install_is_a_primary_navigation_path(self):
        config = (ROOT / "docs" / ".vitepress" / "config.mts").read_text()
        for link in ["/guide/compose-install", "/en/guide/compose-install"]:
            with self.subTest(link=link):
                self.assertIn(link, config)

        for relative in [
            "README.md",
            "README.en.md",
            "docs/index.md",
            "docs/en/index.md",
            "docs/guide/quick-start.md",
            "docs/en/guide/quick-start.md",
            "docs/install/docker.md",
            "docs/en/install/docker.md",
        ]:
            text = (ROOT / relative).read_text()
            with self.subTest(path=relative):
                self.assertIn("compose-install", text)

        home_flow = (ROOT / "docs" / ".vitepress" / "theme" / "components" / "HomeFlow.vue").read_text()
        self.assertIn("compose.yml", home_flow)
        self.assertNotIn("Run one script", home_flow)
        self.assertNotIn("执行一个脚本", home_flow)
        self.assertNotIn("portloom.look4i.com", config)

    def test_beginner_env_template_fails_closed_until_required_values_are_set(self):
        env_template = (ROOT / "examples" / "compose.env.example").read_text()
        self.assertIn("\nTM_PUBLIC_HOST=\n", env_template)
        self.assertIn("\nTM_ADMIN_TOKEN=\n", env_template)
        self.assertNotIn("REPLACE_WITH_A_LONG_RANDOM_VALUE", env_template)

    def test_beginner_template_is_repeatable_pinned_and_token_safe(self):
        compose = (ROOT / "examples" / "compose.yml").read_text()
        env_template = (ROOT / "examples" / "compose.env.example").read_text()
        self.assertIn("ghcr.io/lkhmm520/portloom-server:0.4.1", compose)
        self.assertIn("ghcr.io/lkhmm520/portloom-sshd:0.4.1", compose)
        self.assertNotIn(":latest", compose)
        self.assertIn("./data/server:/data", compose)
        self.assertIn("./data/ssh-auth:/auth", compose)
        self.assertIn("chown 0:0 /data/certs", compose)
        self.assertLess(
            compose.index("chown 0:0 /data/certs"), compose.index("chmod 0700 /data /data/certs /auth")
        )
        advanced_compose = (ROOT / "examples" / "docker-compose.server.yml").read_text()
        self.assertIn("chown 0:0 /data/certs", advanced_compose)
        self.assertLess(
            advanced_compose.index("chown 0:0 /data/certs"),
            advanced_compose.index("chmod 0700 /data /data/certs /auth"),
        )
        self.assertIn("openssl rand -hex 32", env_template)
        self.assertNotIn("PORTLOOM_SERVER_IMAGE=", env_template)
        self.assertNotIn("TM_SERVER_DATA_DIR=", env_template)
        self.assertNotIn("TM_TLS_CACHE_DIR=", env_template)
        self.assertIn("TM_TLS_CACHE_DIR: /data/certs", compose)

        for relative in ["docs/guide/compose-install.md", "docs/en/guide/compose-install.md"]:
            text = (ROOT / relative).read_text()
            with self.subTest(path=relative):
                self.assertIn("openssl rand -hex 32", text)
                self.assertIn("chmod 0600 .env", text)
                self.assertIn("chmod 0711 .", text)
                self.assertIn("docker compose ps -a", text)

    def test_lifecycle_cleanup_never_removes_foreign_fixed_name_containers(self):
        lifecycle = (ROOT / "tests" / "beginner_compose_lifecycle_test.sh").read_text()
        conflict_guard = ROOT / "tests" / "beginner_compose_conflict_guard_test.sh"
        self.assertNotIn("docker rm -f portloom-server portloom-sshd", lifecycle)
        self.assertIn('com.docker.compose.project', lifecycle)
        self.assertIn('refusing to run: container name $name is already in use', lifecycle)
        self.assertTrue(conflict_guard.exists())

    def test_current_install_and_upgrade_commands_use_current_patch_version(self):
        for relative in [
            "docs/install/production.md",
            "docs/en/install/production.md",
            "docs/operations/backup-upgrade.md",
            "docs/en/operations/backup-upgrade.md",
        ]:
            text = (ROOT / relative).read_text()
            with self.subTest(path=relative):
                self.assertNotIn("--version 0.4.0", text)

    def test_public_copy_does_not_imply_a_required_portloom_subdomain(self):
        public_files = [ROOT / "README.md", ROOT / "README.en.md", ROOT / "web" / "index.html"]
        public_files.extend((ROOT / "docs").rglob("*.md"))
        public_files.extend((ROOT / "examples").glob("*.example"))
        public_files.append(ROOT / "docs" / "public" / "install-server.sh")

        offenders = []
        for path in public_files:
            if ".vitepress/dist" in path.as_posix():
                continue
            if "portloom.example.com" in path.read_text():
                offenders.append(path.relative_to(ROOT).as_posix())
        self.assertEqual([], offenders, f"misleading management hostname remains in: {offenders}")

    def test_compose_pages_explain_that_the_domain_is_user_selected(self):
        required_zh = ["不要求 `portloom.` 前缀", "根域名", "任意子域名"]
        required_en = ["no `portloom.` prefix", "apex domain", "any subdomain"]
        pages = [
            (ROOT / "docs" / "guide" / "compose-install.md", required_zh),
            (ROOT / "docs" / "en" / "guide" / "compose-install.md", required_en),
        ]
        for path, phrases in pages:
            text = path.read_text()
            for phrase in phrases:
                with self.subTest(path=path, phrase=phrase):
                    self.assertIn(phrase, text)


if __name__ == "__main__":
    unittest.main()
